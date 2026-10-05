package telegram

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/paramon-tech/tuigram/internal/core"
	"github.com/pion/webrtc/v4"
)

type fakeCallConnection struct {
	mu           sync.Mutex
	trackReady   bool
	closed       atomic.Bool
	writes       atomic.Int32
	onDisconnect func()
}

func (c *fakeCallConnection) WriteRTP([]byte) error {
	c.writes.Add(1)
	return nil
}
func (c *fakeCallConnection) OnTrack(func(*webrtc.TrackRemote, *webrtc.RTPReceiver)) {
	c.mu.Lock()
	c.trackReady = true
	c.mu.Unlock()
}
func (*fakeCallConnection) OnConnected(fn func()) { fn() }
func (c *fakeCallConnection) OnDisconnected(fn func()) {
	c.mu.Lock()
	c.onDisconnect = fn
	c.mu.Unlock()
}
func (c *fakeCallConnection) Close() error {
	c.closed.Store(true)
	return nil
}

type fakeCallAudio struct {
	muted  atomic.Bool
	closed atomic.Bool
	errors chan error
}

func (*fakeCallAudio) WriteRTP([]byte) error  { return nil }
func (a *fakeCallAudio) SetMuted(v bool)      { a.muted.Store(v) }
func (a *fakeCallAudio) Errors() <-chan error { return a.errors }
func (a *fakeCallAudio) Close() error {
	a.closed.Store(true)
	return nil
}

type discardRecord struct {
	input  tg.InputPhoneCall
	reason tg.PhoneCallDiscardReasonClass
}

func nativeTestClient(t *testing.T) (*client, *fakeCallConnection, *fakeCallAudio, <-chan discardRecord, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c := testClient(func(context.Context, bin.Encoder, bin.Decoder) error {
		t.Error("lifecycle test must never contact Telegram")
		return errors.New("unexpected network request")
	})
	c.accountID = 1
	c.remember([]tg.UserClass{&tg.User{ID: 2, AccessHash: 200, FirstName: "Alice"}, &tg.User{ID: 3, Bot: true}}, nil)
	conn := &fakeCallConnection{}
	audio := &fakeCallAudio{errors: make(chan error, 1)}
	discards := make(chan discardRecord, 8)
	m := &nativeCalls{client: c, ctx: ctx, state: core.CallState{Status: "idle"}}
	m.checkAudio = func(context.Context) error { return nil }
	m.startAudio = func(context.Context, func([]byte) error) (callAudio, error) { return audio, nil }
	m.discard = func(ctx context.Context, input tg.InputPhoneCall, reason tg.PhoneCallDiscardReasonClass) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		discards <- discardRecord{input: input, reason: reason}
		return nil
	}
	m.received = func(context.Context, tg.InputPhoneCall) error { return nil }
	m.newDriver = func(s *nativeCall) callDriver {
		return callDriver{
			request: func(context.Context, tg.InputUserClass) (callConnection, error) {
				s.capture(tg.InputPhoneCall{ID: 100, AccessHash: 200})
				return conn, nil
			},
			accept: func(context.Context) (callConnection, error) { return conn, nil },
			handle: func(context.Context, *tg.UpdatePhoneCall) error { return nil },
			signal: func(context.Context, *tg.UpdatePhoneCallSignalingData) error { return nil },
		}
	}
	c.native = m
	t.Cleanup(func() {
		cancel()
		endCtx, endCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer endCancel()
		if err := c.EndCall(endCtx, 0); err != nil {
			t.Errorf("end test call: %v", err)
		}
	})
	return c, conn, audio, discards, cancel
}

func waitCallStatus(t *testing.T, c *client, want string) core.CallState {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		state := c.CallState()
		if state.Status == want {
			return state
		}
		select {
		case <-timer.C:
			t.Fatalf("wanted call status %s, got %+v", want, state)
		case <-tick.C:
		}
	}
}

func nextDiscard(t *testing.T, discards <-chan discardRecord) discardRecord {
	t.Helper()
	select {
	case record := <-discards:
		return record
	case <-time.After(3 * time.Second):
		t.Fatal("server call was not discarded")
		return discardRecord{}
	}
}

func TestNativeCallOutlivesCommandAndStopsAllAudio(t *testing.T) {
	c, conn, audio, discards, _ := nativeTestClient(t)
	command, cancel := context.WithCancel(context.Background())
	if err := c.StartCall(command, core.Chat{ID: "user:2", Title: "Alice", Kind: "private"}); err != nil {
		t.Fatal(err)
	}
	cancel()
	state := waitCallStatus(t, c, "active")
	if state.StartedAt.IsZero() || state.ID == 0 || state.Incoming {
		t.Fatalf("missing outgoing state: %+v", state)
	}
	if err := c.SetCallMuted(state.ID, true); err != nil || !audio.muted.Load() || !c.CallState().Muted {
		t.Fatalf("mute not propagated: %v", err)
	}
	if err := c.SetCallMuted(state.ID+1, false); err == nil || !audio.muted.Load() {
		t.Fatal("a stale UI command changed the current microphone")
	}
	if err := c.EndCall(context.Background(), state.ID+1); err == nil || !c.CallState().Active() {
		t.Fatal("a stale UI command ended the current call")
	}
	if err := c.EndCall(context.Background(), state.ID); err != nil {
		t.Fatal(err)
	}
	if !conn.closed.Load() || !audio.closed.Load() || c.CallState().Status != "ended" {
		t.Fatalf("call resources survived hangup: conn=%v, audio=%v, state=%+v", conn.closed.Load(), audio.closed.Load(), c.CallState())
	}
	if record := nextDiscard(t, discards); record.input.ID != 100 || record.input.AccessHash != 200 {
		t.Fatalf("wrong discarded call: %+v", record)
	}
}

func TestNativeCallIncomingNeedsAnswerAndBusyDoesNotReplaceCall(t *testing.T) {
	c, _, audio, discards, _ := nativeTestClient(t)
	var starts atomic.Int32
	c.native.startAudio = func(context.Context, func([]byte) error) (callAudio, error) {
		starts.Add(1)
		return audio, nil
	}
	first := &tg.PhoneCallRequested{ID: 100, AccessHash: 200, AdminID: 2, ParticipantID: 1}
	if err := c.native.incoming(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	state := c.CallState()
	if state.Status != "ringing" || !state.Incoming || starts.Load() != 0 {
		t.Fatalf("incoming call opened audio without answer: %+v", state)
	}
	if err := c.AnswerCall(context.Background(), state.ID+1); err == nil {
		t.Fatal("stale caller ID was answered")
	}
	second := &tg.PhoneCallRequested{ID: 101, AccessHash: 201, AdminID: 4, ParticipantID: 1}
	if err := c.native.incoming(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if record := nextDiscard(t, discards); record.input.ID != 101 {
		t.Fatalf("wrong busy call: %+v", record)
	}
	if err := c.native.handle(context.Background(), tg.Entities{}, &tg.UpdatePhoneCall{PhoneCall: &tg.PhoneCallDiscarded{ID: 101}}); err != nil {
		t.Fatal(err)
	}
	if c.CallState().Status != "ringing" || c.CallState().ID != state.ID {
		t.Fatal("the other caller's discard replaced the ringing call")
	}
	if err := c.AnswerCall(context.Background(), state.ID); err != nil {
		t.Fatal(err)
	}
	waitCallStatus(t, c, "active")
	if starts.Load() != 1 {
		t.Fatalf("audio starts = %d", starts.Load())
	}
	if err := c.native.handle(context.Background(), tg.Entities{}, &tg.UpdatePhoneCall{PhoneCall: &tg.PhoneCallDiscarded{ID: 100}}); err != nil {
		t.Fatal(err)
	}
	waitCallStatus(t, c, "ended")
	if !audio.closed.Load() {
		t.Fatal("remote hangup kept the microphone open")
	}
	select {
	case record := <-discards:
		t.Fatalf("remote hangup unexpectedly sent a second discard: %+v", record)
	default:
	}
}

func TestNativeCallHangupDuringRequestStillDiscardsServerCall(t *testing.T) {
	c, _, _, discards, _ := nativeTestClient(t)
	requestStarted := make(chan struct{})
	c.native.newDriver = func(s *nativeCall) callDriver {
		return callDriver{request: func(ctx context.Context, _ tg.InputUserClass) (callConnection, error) {
			s.capture(tg.InputPhoneCall{ID: 999, AccessHash: 88})
			close(requestStarted)
			<-ctx.Done()
			return nil, ctx.Err()
		}}
	}
	if err := c.StartCall(context.Background(), core.Chat{ID: "user:2"}); err != nil {
		t.Fatal(err)
	}
	<-requestStarted
	if err := c.EndCall(context.Background(), c.CallState().ID); err != nil {
		t.Fatal(err)
	}
	if record := nextDiscard(t, discards); record.input.ID != 999 || record.input.AccessHash != 88 {
		t.Fatalf("canceled setup lost server call identity: %+v", record)
	}
}

func TestNativeCallBuffersSignalingUntilTrackHandlerInstalled(t *testing.T) {
	c, conn, _, _, _ := nativeTestClient(t)
	requestStarted, release := make(chan struct{}), make(chan struct{})
	signalHandled := make(chan bool, 1)
	c.native.newDriver = func(s *nativeCall) callDriver {
		return callDriver{
			request: func(ctx context.Context, _ tg.InputUserClass) (callConnection, error) {
				s.capture(tg.InputPhoneCall{ID: 100, AccessHash: 200})
				close(requestStarted)
				select {
				case <-release:
					return conn, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
			signal: func(context.Context, *tg.UpdatePhoneCallSignalingData) error {
				conn.mu.Lock()
				signalHandled <- conn.trackReady
				conn.mu.Unlock()
				return nil
			},
		}
	}
	if err := c.StartCall(context.Background(), core.Chat{ID: "user:2"}); err != nil {
		t.Fatal(err)
	}
	<-requestStarted
	if err := c.native.signal(context.Background(), &tg.UpdatePhoneCallSignalingData{PhoneCallID: 100, Data: []byte("signal")}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-signalHandled:
		t.Fatal("signaling ran before callbacks could be installed")
	default:
	}
	close(release)
	waitCallStatus(t, c, "active")
	if ready := <-signalHandled; !ready {
		t.Fatal("remote media track could be dropped because OnTrack was installed too late")
	}
}

func TestNativeCallSessionShutdownAndAudioFailure(t *testing.T) {
	for _, stop := range []string{"session", "audio"} {
		t.Run(stop, func(t *testing.T) {
			c, conn, audio, discards, cancel := nativeTestClient(t)
			if err := c.StartCall(context.Background(), core.Chat{ID: "user:2"}); err != nil {
				t.Fatal(err)
			}
			waitCallStatus(t, c, "active")
			want := "ended"
			if stop == "session" {
				cancel()
			} else {
				audio.errors <- errors.New("microphone permission denied")
				want = "failed"
			}
			state := waitCallStatus(t, c, want)
			if !audio.closed.Load() || !conn.closed.Load() {
				t.Fatal("stopped session retained audio or transport")
			}
			if stop == "audio" && state.Error != "microphone permission denied" {
				t.Fatalf("audio failure not visible: %+v", state)
			}
			nextDiscard(t, discards)
		})
	}
}

func TestNativeCallMutedBeforeConnectionNeverSendsInitialAudio(t *testing.T) {
	c, conn, audio, _, _ := nativeTestClient(t)
	requestStarted, release := make(chan struct{}), make(chan struct{})
	senders := make(chan func([]byte) error, 1)
	c.native.newDriver = func(s *nativeCall) callDriver {
		return callDriver{request: func(ctx context.Context, _ tg.InputUserClass) (callConnection, error) {
			s.capture(tg.InputPhoneCall{ID: 100, AccessHash: 200})
			close(requestStarted)
			select {
			case <-release:
				return conn, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}}
	}
	c.native.startAudio = func(ctx context.Context, send func([]byte) error) (callAudio, error) {
		// A recorder can emit before StartCallAudio returns its bridge handle.
		if err := send([]byte("first microphone packet")); err != nil {
			return nil, err
		}
		senders <- send
		return audio, nil
	}
	if err := c.StartCall(context.Background(), core.Chat{ID: "user:2"}); err != nil {
		t.Fatal(err)
	}
	<-requestStarted
	id := c.CallState().ID
	if err := c.SetCallMuted(id, true); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitCallStatus(t, c, "active")
	if conn.writes.Load() != 0 || !audio.muted.Load() {
		t.Fatal("a pre-muted call sent microphone audio during startup")
	}
	send := <-senders
	if err := c.SetCallMuted(id, false); err != nil {
		t.Fatal(err)
	}
	if err := send([]byte("unmuted packet")); err != nil || conn.writes.Load() != 1 {
		t.Fatalf("unmute did not resume audio: %v", err)
	}
}

func TestNativeCallAudioPreflightAndPrivateChatValidation(t *testing.T) {
	c, _, _, _, _ := nativeTestClient(t)
	var requests atomic.Int32
	c.native.newDriver = func(*nativeCall) callDriver { requests.Add(1); return callDriver{} }
	for _, id := range []string{"user:3", "group:2", "missing"} {
		if err := c.StartCall(context.Background(), core.Chat{ID: id}); err == nil {
			t.Fatalf("started unsupported chat %q", id)
		}
	}
	c.native.checkAudio = func(context.Context) error { return errors.New("install FFmpeg") }
	if err := c.StartCall(context.Background(), core.Chat{ID: "user:2"}); err == nil || err.Error() != "install FFmpeg" {
		t.Fatalf("preflight error lost: %v", err)
	}
	if requests.Load() != 0 || c.CallState().Active() {
		t.Fatal("called a person despite failed audio preflight")
	}
}

func TestCallInvokerCapturesCreatedCallAfterCommandCancellation(t *testing.T) {
	c, _, _, _, _ := nativeTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &nativeCall{manager: c.native, ctx: context.Background()}
	base := invokeFunc(func(callCtx context.Context, in bin.Encoder, out bin.Decoder) error {
		cancel() // A local hangup arrives while the request is in flight.
		if callCtx.Err() != nil {
			t.Fatal("call creation response cannot be lost on local hangup")
		}
		response := out.(*tg.PhonePhoneCall)
		response.PhoneCall = &tg.PhoneCallWaiting{ID: 123, AccessHash: 456}
		return nil
	})
	invoker := callInvoker{base: base, session: s}
	if err := invoker.Invoke(ctx, &tg.PhoneRequestCallRequest{}, &tg.PhonePhoneCall{}); err != nil {
		t.Fatal(err)
	}
	if s.input.ID != 123 || s.input.AccessHash != 456 {
		t.Fatalf("lost server identity: %+v", s.input)
	}
}

func TestCallInvokerHonorsOutgoingRelayOnlyRestriction(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		name := "relay-only"
		if allowed {
			name = "p2p-allowed"
		}
		t.Run(name, func(t *testing.T) {
			c, _, _, _, _ := nativeTestClient(t)
			s := &nativeCall{manager: c.native, ctx: context.Background()}
			base := invokeFunc(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
				out.(*tg.PhonePhoneCall).PhoneCall = &tg.PhoneCall{ID: 789, AccessHash: 90, P2PAllowed: allowed}
				return nil
			})
			invoker := callInvoker{base: base, session: s}
			err := invoker.Invoke(context.Background(), &tg.PhoneConfirmCallRequest{}, &tg.PhonePhoneCall{})
			if allowed && err != nil {
				t.Fatalf("P2P call rejected: %v", err)
			}
			if !allowed && !errors.Is(err, errRelayOnlyCall) {
				t.Fatalf("relay-only response could reach gotd's ICE setup: %v", err)
			}
			if s.input.ID != 789 || s.input.AccessHash != 90 {
				t.Fatal("privacy rejection must retain server identity for hangup")
			}
		})
	}
}

func TestNativeIncomingRelayOnlyRestrictionStopsBeforeTransport(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		name := "relay-only"
		if allowed {
			name = "p2p-allowed"
		}
		t.Run(name, func(t *testing.T) {
			c, _, _, discards, _ := nativeTestClient(t)
			started := make(chan struct{})
			var forwarded, audioStarts atomic.Int32
			c.native.startAudio = func(context.Context, func([]byte) error) (callAudio, error) {
				audioStarts.Add(1)
				return nil, errors.New("audio must not start before a valid call confirmation")
			}
			c.native.newDriver = func(*nativeCall) callDriver {
				return callDriver{
					accept: func(ctx context.Context) (callConnection, error) {
						close(started)
						<-ctx.Done()
						return nil, ctx.Err()
					},
					handle: func(context.Context, *tg.UpdatePhoneCall) error { forwarded.Add(1); return nil },
				}
			}
			request := &tg.PhoneCallRequested{ID: 456, AccessHash: 12, AdminID: 2, ParticipantID: 1}
			if err := c.native.incoming(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			if err := c.AnswerCall(context.Background(), c.CallState().ID); err != nil {
				t.Fatal(err)
			}
			<-started
			confirmation := &tg.UpdatePhoneCall{PhoneCall: &tg.PhoneCall{ID: 456, AccessHash: 12, P2PAllowed: allowed}}
			if err := c.native.handle(context.Background(), tg.Entities{}, confirmation); err != nil {
				t.Fatal(err)
			}
			if allowed {
				if forwarded.Load() != 1 || c.CallState().Status != "connecting" {
					t.Fatal("allowed confirmation was not delivered to transport")
				}
				return
			}
			state := waitCallStatus(t, c, "failed")
			if forwarded.Load() != 0 || audioStarts.Load() != 0 || state.Error != errRelayOnlyCall.Error() {
				t.Fatalf("relay-only call reached transport or hid the restriction: %+v", state)
			}
			if record := nextDiscard(t, discards); record.input.ID != 456 {
				t.Fatalf("wrong call discarded after privacy restriction: %+v", record)
			}
		})
	}
}
