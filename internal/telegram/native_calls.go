package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gotd/td/bin"
	calls "github.com/paramon-tech/tuigram/internal/tgcalls"
	"github.com/gotd/td/tg"
	"github.com/paramon-tech/tuigram/internal/core"
	"github.com/paramon-tech/tuigram/internal/platform"
	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

var _ core.CallClient = (*client)(nil)

var errRelayOnlyCall = errors.New("this call requires a relay-only connection for privacy; the native call library cannot honor that restriction")

// Each session owns a calls.Client. In particular, rejecting a second incoming
// call must not use IncomingCall.Reject, which replaces that client's active call.
type nativeCalls struct {
	client *client
	ctx    context.Context
	mu     sync.Mutex
	state  core.CallState
	active *nativeCall
	nextID uint64

	checkAudio func(context.Context) error
	startAudio func(context.Context, func([]byte) error) (callAudio, error)
	newDriver  func(*nativeCall) callDriver
	discard    func(context.Context, tg.InputPhoneCall, tg.PhoneCallDiscardReasonClass) error
	received   func(context.Context, tg.InputPhoneCall) error
}

type callAudio interface {
	WriteRTP([]byte) error
	SetMuted(bool)
	Errors() <-chan error
	Close() error
}

type callConnection interface {
	WriteRTP([]byte) error
	OnTrack(func(*webrtc.TrackRemote, *webrtc.RTPReceiver))
	OnConnected(func())
	OnDisconnected(func())
	Close() error
}

type callDriver struct {
	request func(context.Context, tg.InputUserClass) (callConnection, error)
	accept  func(context.Context) (callConnection, error)
	handle  func(context.Context, *tg.UpdatePhoneCall) error
	signal  func(context.Context, *tg.UpdatePhoneCallSignalingData) error
}

type nativeCall struct {
	manager         *nativeCalls
	ctx             context.Context
	cancel          context.CancelFunc
	done            chan struct{}
	begin           chan struct{}
	user            tg.InputUserClass
	incoming        *tg.PhoneCallRequested
	driver          callDriver
	remoteEnd       atomic.Bool
	muted           atomic.Bool
	sentPackets     atomic.Uint64
	receivedPackets atomic.Uint64
	stopMu          sync.Mutex
	stopError       error

	inputMu sync.Mutex
	input   tg.InputPhoneCall
	pending []*tg.UpdatePhoneCall

	// Upstream Conn.OnTrack is not synchronized and has no replay. Buffer
	// signaling until Request/Accept returns and all callbacks are installed.
	signalMu sync.Mutex
	ready    bool
	signals  []*tg.UpdatePhoneCallSignalingData
	audioMu  sync.Mutex
	audio    callAudio
}

func newNativeCalls(ctx context.Context, c *client, opts platform.CallAudioOptions) *nativeCalls {
	m := &nativeCalls{client: c, ctx: ctx, state: core.CallState{Status: "idle"}}
	m.checkAudio = func(ctx context.Context) error { return platform.CheckCallAudio(ctx, opts) }
	m.startAudio = func(ctx context.Context, send func([]byte) error) (callAudio, error) {
		return platform.StartCallAudio(ctx, opts, send)
	}
	m.newDriver = realCallDriver
	m.discard = func(ctx context.Context, input tg.InputPhoneCall, reason tg.PhoneCallDiscardReasonClass) error {
		_, err := c.api.PhoneDiscardCall(ctx, &tg.PhoneDiscardCallRequest{Peer: input, Reason: reason})
		return err
	}
	m.received = func(ctx context.Context, input tg.InputPhoneCall) error {
		_, err := c.api.PhoneReceivedCall(ctx, input)
		return err
	}
	return m
}

func (c *client) CallState() core.CallState {
	if c.native == nil {
		return core.CallState{Status: "idle"}
	}
	c.native.mu.Lock()
	defer c.native.mu.Unlock()
	state := c.native.state
	if s := c.native.active; s != nil {
		state.SentPackets = s.sentPackets.Load()
		state.ReceivedPackets = s.receivedPackets.Load()
		s.audioMu.Lock()
		copyCallOutputState(&state, s.audio)
		s.audioMu.Unlock()
	}
	return state
}

func copyCallOutputState(state *core.CallState, audio callAudio) {
	// Audio telemetry is optional so other bridges and existing backends can
	// continue providing calls without implementing decoder diagnostics.
	if output, ok := audio.(interface {
		OutputState() platform.CallOutputState
	}); ok {
		snapshot := output.OutputState()
		state.AudioDecodedFrames = snapshot.DecodedFrames
		state.AudioLevelDB = snapshot.LevelDB
		state.AudioLastDecodedAt = snapshot.LastDecodedAt
		state.AudioOutputError = snapshot.Error
	}
}

func (c *client) StartCall(ctx context.Context, chat core.Chat) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.native == nil {
		return errors.New("native calls require a connected Telegram session")
	}
	record, err := c.peer(chat.ID)
	if err != nil {
		return err
	}
	user, ok := record.input.(*tg.InputPeerUser)
	if !ok || record.bot || user.UserID == c.accountID {
		return errors.New("select another person's private chat for a voice call")
	}
	m := c.native
	if err := m.available(); err != nil {
		return err
	}
	if err := m.checkAudio(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.active != nil {
		return errors.New("end the current call before starting another")
	}
	if err := m.ctx.Err(); err != nil {
		return err
	}
	s := m.newSessionLocked(chat, nil)
	s.user = &tg.InputUser{UserID: user.UserID, AccessHash: user.AccessHash}
	s.driver = m.newDriver(s)
	close(s.begin)
	go s.run()
	return nil
}

func (m *nativeCalls) available() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != nil {
		return errors.New("end the current call before starting another")
	}
	return m.ctx.Err()
}

func (c *client) AnswerCall(ctx context.Context, id uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.native == nil {
		return errors.New("there is no incoming call")
	}
	m := c.native
	m.mu.Lock()
	s := m.active
	ringing := s != nil && m.state.Status == "ringing" && m.state.ID == id
	m.mu.Unlock()
	if !ringing {
		return errors.New("there is no incoming call to answer")
	}
	if err := m.checkAudio(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.active != s || m.state.Status != "ringing" || s.ctx.Err() != nil {
		return errors.New("the incoming call is no longer ringing")
	}
	m.state.Status = "connecting"
	close(s.begin)
	return nil
}

func (c *client) EndCall(ctx context.Context, id uint64) error {
	if c.native == nil {
		return nil
	}
	m := c.native
	m.mu.Lock()
	s := m.active
	if id != 0 && m.state.ID != id {
		m.mu.Unlock()
		return errors.New("the selected call is no longer current")
	}
	if s != nil {
		s.cancel()
	}
	m.mu.Unlock()
	if s == nil {
		return nil
	}
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *client) SetCallMuted(id uint64, muted bool) error {
	if c.native == nil {
		return errors.New("there is no active call")
	}
	m := c.native
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || !m.state.Active() || m.active.ctx.Err() != nil || m.state.ID != id {
		return errors.New("there is no active call")
	}
	m.state.Muted = muted
	m.active.muted.Store(muted)
	m.active.audioMu.Lock()
	defer m.active.audioMu.Unlock()
	if m.active.audio != nil {
		m.active.audio.SetMuted(muted)
	}
	return nil
}

func (m *nativeCalls) newSessionLocked(chat core.Chat, incoming *tg.PhoneCallRequested) *nativeCall {
	ctx, cancel := context.WithCancel(m.ctx)
	s := &nativeCall{manager: m, ctx: ctx, cancel: cancel, begin: make(chan struct{}), done: make(chan struct{}), incoming: incoming}
	m.nextID++
	m.active = s
	m.state = core.CallState{ID: m.nextID, Chat: chat, Status: "dialing", Incoming: incoming != nil}
	if incoming != nil {
		m.state.Status = "ringing"
		s.input = tg.InputPhoneCall{ID: incoming.ID, AccessHash: incoming.AccessHash}
	}
	return s
}

func (m *nativeCalls) handle(ctx context.Context, entities tg.Entities, update *tg.UpdatePhoneCall) error {
	users := make([]tg.UserClass, 0, len(entities.Users))
	for _, user := range entities.Users {
		users = append(users, user)
	}
	m.client.remember(users, nil)
	if incoming, ok := update.PhoneCall.(*tg.PhoneCallRequested); ok {
		return m.incoming(ctx, incoming)
	}
	m.mu.Lock()
	s := m.active
	m.mu.Unlock()
	if s != nil {
		return s.handle(update)
	}
	return nil
}

func (m *nativeCalls) incoming(_ context.Context, req *tg.PhoneCallRequested) error {
	if req.AdminID == m.client.accountID || req.ParticipantID != m.client.accountID || m.ctx.Err() != nil {
		return nil
	}
	m.mu.Lock()
	if m.active != nil {
		m.active.inputMu.Lock()
		duplicate := m.active.input.ID == req.ID
		m.active.inputMu.Unlock()
		m.mu.Unlock()
		if !duplicate {
			go m.decline(req)
		}
		return nil
	}
	if req.Video {
		m.mu.Unlock()
		go m.decline(req)
		return nil
	}
	chat := core.Chat{ID: "user:" + strconv.FormatInt(req.AdminID, 10), Title: "Telegram user " + strconv.FormatInt(req.AdminID, 10), Kind: "private"}
	if peer, err := m.client.peer(chat.ID); err == nil {
		chat.Title = peer.title
	}
	s := m.newSessionLocked(chat, req)
	s.driver = m.newDriver(s)
	m.mu.Unlock()
	go s.run()
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
		defer cancel()
		_ = m.received(ctx, tg.InputPhoneCall{ID: req.ID, AccessHash: req.AccessHash})
	}()
	return nil
}

func (m *nativeCalls) decline(req *tg.PhoneCallRequested) {
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()
	_ = m.discard(ctx, tg.InputPhoneCall{ID: req.ID, AccessHash: req.AccessHash}, &tg.PhoneCallDiscardReasonBusy{})
}

func (s *nativeCall) capture(input tg.InputPhoneCall) {
	s.inputMu.Lock()
	s.input = input
	pending := s.pending
	s.pending = nil
	s.inputMu.Unlock()
	for _, u := range pending {
		_ = s.handle(u)
	}
}

func (s *nativeCall) handle(u *tg.UpdatePhoneCall) error {
	s.inputMu.Lock()
	id := s.input.ID
	if id == 0 {
		if len(s.pending) < 32 {
			s.pending = append(s.pending, u)
		}
		s.inputMu.Unlock()
		return nil
	}
	s.inputMu.Unlock()
	if u.PhoneCall.GetID() != id || s.ctx.Err() != nil {
		return nil
	}
	if _, discarded := u.PhoneCall.(*tg.PhoneCallDiscarded); discarded {
		// Upstream's discarded handler neither matches IDs nor synchronizes
		// conn access during setup. Our session owns cancellation and cleanup.
		s.remoteEnd.Store(true)
		s.cancel()
		return nil
	}
	if call, ok := u.PhoneCall.(*tg.PhoneCall); ok && !call.P2PAllowed {
		// gotd v0.162 ignores this server/peer privacy restriction and would
		// publish host and server-reflexive ICE candidates. Stop before its
		// Accept goroutine can consume the confirmation and open transport.
		s.stopWithError(errRelayOnlyCall)
		return nil
	}
	return s.driver.handle(s.ctx, u)
}

func (s *nativeCall) stopWithError(err error) {
	s.stopMu.Lock()
	if s.stopError == nil {
		s.stopError = err
	}
	s.stopMu.Unlock()
	s.cancel()
}

func (m *nativeCalls) signal(_ context.Context, u *tg.UpdatePhoneCallSignalingData) error {
	m.mu.Lock()
	s := m.active
	m.mu.Unlock()
	if s == nil || s.ctx.Err() != nil {
		return nil
	}
	s.inputMu.Lock()
	id := s.input.ID
	s.inputMu.Unlock()
	if id == 0 || id != u.PhoneCallID {
		return nil
	}
	s.signalMu.Lock()
	defer s.signalMu.Unlock()
	if s.ctx.Err() != nil {
		return nil
	}
	if !s.ready {
		if len(s.signals) < 128 && len(u.Data) <= 256*1024 {
			copyUpdate := *u
			copyUpdate.Data = append([]byte(nil), u.Data...)
			s.signals = append(s.signals, &copyUpdate)
		}
		return nil
	}
	return s.driver.signal(s.ctx, u)
}

func (s *nativeCall) status(status string, err error) {
	m := s.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != s {
		return
	}
	m.state.Status = status
	if err != nil {
		m.state.Error = err.Error()
	}
	if status == "active" && m.state.StartedAt.IsZero() {
		m.state.StartedAt = time.Now()
	}
}

func (s *nativeCall) run() {
	var conn callConnection
	var failure error
	defer func() { s.finish(conn, failure) }()
	ringTimeout := time.NewTimer(90 * time.Second)
	defer ringTimeout.Stop()
	select {
	case <-s.ctx.Done():
		return
	case <-ringTimeout.C:
		return
	case <-s.begin:
	}
	handshakeCtx, cancel := context.WithTimeout(s.ctx, 90*time.Second)
	var err error
	if s.incoming != nil {
		conn, err = s.driver.accept(handshakeCtx)
	} else {
		conn, err = s.driver.request(handshakeCtx, s.user)
	}
	cancel()
	if err != nil {
		if s.ctx.Err() == nil {
			failure = fmt.Errorf("establish voice call: %w", err)
		}
		return
	}
	if s.ctx.Err() != nil {
		return
	}
	s.status("connecting", nil)
	connected, disconnected := make(chan struct{}, 1), make(chan struct{}, 1)
	s.signalMu.Lock()
	conn.OnTrack(s.onTrack)
	conn.OnConnected(func() {
		select {
		case connected <- struct{}{}:
		default:
		}
	})
	conn.OnDisconnected(func() {
		select {
		case disconnected <- struct{}{}:
		default:
		}
	})
	s.ready = true
	for _, signal := range s.signals {
		_ = s.driver.signal(s.ctx, signal)
	}
	s.signals = nil
	s.signalMu.Unlock()
	connectTimeout := time.NewTimer(45 * time.Second)
	defer connectTimeout.Stop()
	select {
	case <-s.ctx.Done():
		return
	case <-disconnected:
		failure = errors.New("voice-call connection closed before audio connected")
		return
	case <-connectTimeout.C:
		failure = errors.New("voice-call connection timed out; this network may require a Telegram relay unsupported by the call library")
		return
	case <-connected:
	}
	audio, err := s.manager.startAudio(s.ctx, func(data []byte) error {
		if s.muted.Load() || s.ctx.Err() != nil {
			return nil
		}
		if err := conn.WriteRTP(data); err != nil {
			return err
		}
		s.sentPackets.Add(1)
		return nil
	})
	if err != nil {
		if s.ctx.Err() == nil {
			failure = err
		}
		return
	}
	m := s.manager
	m.mu.Lock()
	s.audioMu.Lock()
	s.audio = audio
	audio.SetMuted(s.muted.Load())
	s.audioMu.Unlock()
	m.mu.Unlock()
	s.status("active", nil)
	select {
	case <-s.ctx.Done():
	case <-disconnected:
		if !s.remoteEnd.Load() {
			failure = errors.New("voice-call connection lost")
		}
	case err := <-audio.Errors():
		if s.ctx.Err() == nil {
			if err == nil {
				err = errors.New("voice-call audio stopped")
			}
			failure = err
		}
	}
}

func (s *nativeCall) onTrack(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
	closeReceiver := func() {
		if receiver != nil {
			_ = receiver.Stop()
		}
	}
	if track == nil || track.Kind() != webrtc.RTPCodecTypeAudio {
		closeReceiver()
		return
	}
	go s.receiveAudioUntilClosed(track, closeReceiver)
}

type incomingCallAudio interface {
	ReadRTP() (*rtp.Packet, interceptor.Attributes, error)
	Codec() webrtc.RTPCodecParameters
}

func (s *nativeCall) receiveAudioUntilClosed(track incomingCallAudio, closeReceiver func()) {
	// gotd closes its DTLS transport but does not stop each RTP receiver.
	// Explicitly close the receiver's buffered RTP/RTCP streams so its readers
	// unblock even when the remote peer sends nothing during hangup.
	stop := context.AfterFunc(s.ctx, closeReceiver)
	defer stop()
	defer closeReceiver()
	s.receiveAudio(track)
}

func (s *nativeCall) receiveAudio(track incomingCallAudio) {
	for {
		packet, _, err := track.ReadRTP()
		if err != nil {
			// Closing a call unblocks its RTP reader; only unexpected read
			// failures should replace a normal hangup with an error.
			if s.ctx.Err() == nil {
				s.stopWithError(fmt.Errorf("receive call audio: %w", err))
			}
			return
		}
		if s.ctx.Err() != nil {
			return
		}
		codec := track.Codec().MimeType
		if !strings.EqualFold(codec, webrtc.MimeTypeOpus) {
			s.stopWithError(fmt.Errorf("receive call audio: unsupported codec %q", codec))
			return
		}
		data, err := packet.Marshal()
		if err != nil {
			s.stopWithError(fmt.Errorf("receive call audio packet: %w", err))
			return
		}
		s.audioMu.Lock()
		if s.audio != nil {
			err = s.audio.WriteRTP(data)
			if err == nil {
				s.receivedPackets.Add(1)
			}
		}
		s.audioMu.Unlock()
		if err != nil {
			if s.ctx.Err() == nil {
				s.stopWithError(fmt.Errorf("queue incoming call audio: %w", err))
			}
			return
		}
	}
}

func (s *nativeCall) finish(conn callConnection, failure error) {
	s.cancel()
	s.stopMu.Lock()
	if s.stopError != nil {
		failure = s.stopError
	}
	s.stopMu.Unlock()
	s.audioMu.Lock()
	audio := s.audio
	s.audio = nil
	s.audioMu.Unlock()
	s.signalMu.Lock()
	s.ready = false
	s.signals = nil
	if conn != nil {
		_ = conn.Close()
	}
	s.signalMu.Unlock()
	// Closing transport first releases any RTP write still in progress before
	// the audio bridge waits for its capture worker to stop.
	if audio != nil {
		_ = audio.Close()
	}
	s.inputMu.Lock()
	input := s.input
	s.inputMu.Unlock()
	if input.ID != 0 && !s.remoteEnd.Load() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(s.manager.ctx), 5*time.Second)
		reason := tg.PhoneCallDiscardReasonClass(&tg.PhoneCallDiscardReasonHangup{})
		if failure != nil {
			reason = &tg.PhoneCallDiscardReasonDisconnect{}
		} else if s.incoming != nil && audio == nil {
			reason = &tg.PhoneCallDiscardReasonBusy{}
		}
		if err := s.manager.discard(ctx, input, reason); err != nil && failure == nil {
			failure = fmt.Errorf("audio stopped; Telegram hangup could not be confirmed: %w", err)
		}
		cancel()
	}
	m := s.manager
	m.mu.Lock()
	if m.active == s {
		m.state.SentPackets = s.sentPackets.Load()
		m.state.ReceivedPackets = s.receivedPackets.Load()
		copyCallOutputState(&m.state, audio)
		m.active = nil
		m.state.Status = "ended"
		if failure != nil {
			m.state.Status, m.state.Error = "failed", failure.Error()
		}
	}
	m.mu.Unlock()
	close(s.done)
}

// realCallDriver adapts gotd's transport. No microphone is opened here.
func realCallDriver(s *nativeCall) callDriver {
	api := tg.NewClient(callInvoker{base: s.manager.client.api.Invoker(), session: s})
	engine := calls.NewClient(api, calls.Options{})
	driver := callDriver{handle: engine.Handle, signal: engine.HandleSignalingData}
	driver.request = func(ctx context.Context, user tg.InputUserClass) (callConnection, error) {
		conn, err := engine.Request(ctx, user)
		if err != nil {
			return nil, err
		}
		return gotdCallConnection{conn}, nil
	}
	if s.incoming != nil {
		engine.OnIncoming(func(in *calls.IncomingCall) {
			driver.accept = func(ctx context.Context) (callConnection, error) {
				conn, err := in.Accept(ctx)
				if err != nil {
					return nil, err
				}
				return gotdCallConnection{conn}, nil
			}
		})
		_ = engine.Handle(s.ctx, &tg.UpdatePhoneCall{PhoneCall: s.incoming})
	}
	return driver
}

type gotdCallConnection struct{ *calls.Conn }

func (c gotdCallConnection) WriteRTP(data []byte) error {
	_, err := c.AudioTrack().Write(data)
	return err
}

// callInvoker retains the server call identity independently of gotd, whose
// Request/Accept clears its active state on cancellation. This permits a real
// phone.discardCall even when the user hangs up during the DH handshake.
type callInvoker struct {
	base    tg.Invoker
	session *nativeCall
}

func (i callInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	var cancel context.CancelFunc
	switch input.(type) {
	case *tg.PhoneRequestCallRequest:
		if err := ctx.Err(); err != nil {
			return err
		}
		// Once dispatched, let the server acknowledge a created call so that
		// local cancellation can send a matching discard instead of orphaning it.
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	case *tg.PhoneSendSignalingDataRequest:
		// gotd emits signaling with Background; tie it to our session lifetime.
		ctx, cancel = context.WithTimeout(i.session.ctx, 10*time.Second)
	default:
		ctx, cancel = context.WithTimeout(ctx, 15*time.Second)
	}
	defer cancel()
	if err := i.base.Invoke(ctx, input, output); err != nil {
		return err
	}
	if response, ok := output.(*tg.PhonePhoneCall); ok {
		if call, ok := response.PhoneCall.(interface {
			GetID() int64
			GetAccessHash() int64
		}); ok {
			i.session.capture(tg.InputPhoneCall{ID: call.GetID(), AccessHash: call.GetAccessHash()})
		}
		i.session.manager.client.remember(response.Users, nil)
		if call, ok := response.PhoneCall.(*tg.PhoneCall); ok && !call.P2PAllowed {
			// Outgoing calls obtain the final object from phone.confirmCall,
			// not necessarily from an update. Reject it before Request can
			// initialize ICE, which currently cannot enforce relay-only mode.
			return errRelayOnlyCall
		}
	}
	return nil
}
