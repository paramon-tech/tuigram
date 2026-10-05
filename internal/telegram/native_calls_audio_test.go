package telegram

import (
	"context"
	"errors"
	"io"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/paramon-tech/tuigram/internal/core"
	"github.com/paramon-tech/tuigram/internal/platform"
	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

type diagnosticCallConnection struct {
	*fakeCallConnection
	fail atomic.Bool
}

func (c *diagnosticCallConnection) WriteRTP(data []byte) error {
	if c.fail.Load() {
		return errors.New("transport write failed")
	}
	return c.fakeCallConnection.WriteRTP(data)
}

type diagnosticCallAudio struct {
	*fakeCallAudio
	writes atomic.Uint64
	fail   bool
}

func (a *diagnosticCallAudio) WriteRTP([]byte) error {
	if a.fail {
		return errors.New("speaker queue unavailable")
	}
	a.writes.Add(1)
	return nil
}

type incomingAudioResult struct {
	packet *rtp.Packet
	err    error
}

type diagnosticIncomingTrack struct {
	ctx     context.Context
	codec   string
	results chan incomingAudioResult
}

func (t *diagnosticIncomingTrack) ReadRTP() (*rtp.Packet, interceptor.Attributes, error) {
	select {
	case <-t.ctx.Done():
		return nil, nil, t.ctx.Err()
	case result := <-t.results:
		return result.packet, nil, result.err
	}
}

func (t *diagnosticIncomingTrack) Codec() webrtc.RTPCodecParameters {
	return webrtc.RTPCodecParameters{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: t.codec}}
}

func diagnosticAudioPacket() *rtp.Packet {
	return &rtp.Packet{
		Header: rtp.Header{Version: 2, PayloadType: 111, SequenceNumber: 1, Timestamp: 960},
		// An Opus silence packet, never played by these mocked tests.
		Payload: []byte{0xf8, 0xff, 0xfe},
	}
}

func activeNativeSession(c *client) *nativeCall {
	c.native.mu.Lock()
	defer c.native.mu.Unlock()
	return c.native.active
}

func TestNativeCallPacketCountersTrackSuccessfulHandoffs(t *testing.T) {
	c, baseConn, baseAudio, _, _ := nativeTestClient(t)
	conn := &diagnosticCallConnection{fakeCallConnection: baseConn}
	audio := &diagnosticCallAudio{fakeCallAudio: baseAudio}
	senders := make(chan func([]byte) error, 2)
	c.native.startAudio = func(_ context.Context, send func([]byte) error) (callAudio, error) {
		senders <- send
		return audio, nil
	}
	c.native.newDriver = func(s *nativeCall) callDriver {
		return callDriver{request: func(context.Context, tg.InputUserClass) (callConnection, error) {
			s.capture(tg.InputPhoneCall{ID: 100, AccessHash: 200})
			return conn, nil
		}}
	}
	if err := c.StartCall(context.Background(), core.Chat{ID: "user:2"}); err != nil {
		t.Fatal(err)
	}
	state := waitCallStatus(t, c, "active")
	send := <-senders
	if err := send([]byte("sent")); err != nil {
		t.Fatal(err)
	}
	conn.fail.Store(true)
	if err := send([]byte("failed")); err == nil {
		t.Fatal("failed transport write was hidden")
	}
	conn.fail.Store(false)
	if err := c.SetCallMuted(state.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := send([]byte("muted")); err != nil {
		t.Fatal(err)
	}
	if got := c.CallState().SentPackets; got != 1 {
		t.Fatalf("sent counter includes failed or muted packets: %d", got)
	}

	s := activeNativeSession(c)
	track := &diagnosticIncomingTrack{ctx: s.ctx, codec: webrtc.MimeTypeOpus, results: make(chan incomingAudioResult, 1)}
	done := make(chan struct{})
	go func() {
		s.receiveAudio(track)
		close(done)
	}()
	track.results <- incomingAudioResult{packet: diagnosticAudioPacket()}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for c.CallState().ReceivedPackets != 1 {
		select {
		case <-deadline.C:
			t.Fatal("incoming audio was not counted")
		case <-time.After(time.Millisecond):
		}
	}
	if audio.writes.Load() != 1 {
		t.Fatal("received count advanced without queuing audio")
	}
	if err := c.EndCall(context.Background(), state.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("incoming reader survived cancellation")
	}
	state = c.CallState()
	if state.Status != "ended" || state.Error != "" || state.SentPackets != 1 || state.ReceivedPackets != 1 {
		t.Fatalf("final diagnostics were lost or normal hangup became a failure: %+v", state)
	}
	if err := send([]byte("after hangup")); err != nil {
		t.Fatal(err)
	}
	if c.CallState().SentPackets != 1 {
		t.Fatal("canceled audio changed the final packet counter")
	}
	if err := c.StartCall(context.Background(), core.Chat{ID: "user:2"}); err != nil {
		t.Fatal(err)
	}
	state = waitCallStatus(t, c, "active")
	if state.SentPackets != 0 || state.ReceivedPackets != 0 {
		t.Fatalf("new call inherited old counters: %+v", state)
	}
}

func TestNativeCallIncomingAudioFailuresAreVisible(t *testing.T) {
	for _, tc := range []struct {
		name      string
		codec     string
		readErr   error
		queueFail bool
		want      string
	}{
		{name: "reader", codec: webrtc.MimeTypeOpus, readErr: io.ErrUnexpectedEOF, want: "receive call audio: unexpected EOF"},
		{name: "queue", codec: webrtc.MimeTypeOpus, queueFail: true, want: "queue incoming call audio: speaker queue unavailable"},
		{name: "codec", codec: "audio/unknown", want: "receive call audio: unsupported codec"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, conn, baseAudio, discards, _ := nativeTestClient(t)
			audio := &diagnosticCallAudio{fakeCallAudio: baseAudio, fail: tc.queueFail}
			c.native.startAudio = func(context.Context, func([]byte) error) (callAudio, error) { return audio, nil }
			if err := c.StartCall(context.Background(), core.Chat{ID: "user:2"}); err != nil {
				t.Fatal(err)
			}
			waitCallStatus(t, c, "active")
			s := activeNativeSession(c)
			track := &diagnosticIncomingTrack{ctx: s.ctx, codec: tc.codec, results: make(chan incomingAudioResult, 1)}
			track.results <- incomingAudioResult{packet: diagnosticAudioPacket(), err: tc.readErr}
			s.receiveAudio(track)
			state := waitCallStatus(t, c, "failed")
			if !strings.Contains(state.Error, tc.want) || state.ReceivedPackets != 0 {
				t.Fatalf("incoming failure was hidden or counted as audio: %+v", state)
			}
			if !conn.closed.Load() || !baseAudio.closed.Load() {
				t.Fatal("receive failure did not close microphone and transport")
			}
			if _, ok := nextDiscard(t, discards).reason.(*tg.PhoneCallDiscardReasonDisconnect); !ok {
				t.Fatal("receive failure did not report a disconnected call")
			}
		})
	}
}

func TestNativeCallIncomingReadCancellationIsQuiet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &nativeCall{ctx: ctx, cancel: cancel}
	s.receiveAudio(&diagnosticIncomingTrack{ctx: ctx, codec: webrtc.MimeTypeOpus})
	if s.stopError != nil {
		t.Fatalf("normal hangup became a receive failure: %v", s.stopError)
	}
}

func TestNativeCallCancellationStopsBufferedReceiver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &nativeCall{ctx: ctx, cancel: cancel}
	// Like Pion's per-stream buffers, this reader is independent of the call
	// context and unblocks only when its RTP receiver is explicitly stopped.
	streamCtx, stopStream := context.WithCancel(context.Background())
	defer stopStream()
	track := &diagnosticIncomingTrack{ctx: streamCtx, codec: webrtc.MimeTypeOpus}
	var once sync.Once
	finished := make(chan struct{})
	go func() {
		s.receiveAudioUntilClosed(track, func() { once.Do(stopStream) })
		close(finished)
	}()
	cancel()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("call cancellation left the receiver blocked")
	}
	if streamCtx.Err() == nil || s.stopError != nil {
		t.Fatalf("receiver cleanup failed or normal hangup became an error: %v", s.stopError)
	}
}

type telemetryCallAudio struct {
	*fakeCallAudio
	mu         sync.Mutex
	state      platform.CallOutputState
	closeState platform.CallOutputState
}

func (a *telemetryCallAudio) OutputState() platform.CallOutputState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *telemetryCallAudio) Close() error {
	a.mu.Lock()
	a.state = a.closeState
	a.mu.Unlock()
	return a.fakeCallAudio.Close()
}

func TestNativeCallCopiesDecoderTelemetryAndKeepsFinalSnapshot(t *testing.T) {
	c, _, baseAudio, _, _ := nativeTestClient(t)
	decodedAt := time.Now().Add(-time.Second)
	audio := &telemetryCallAudio{
		fakeCallAudio: baseAudio,
		state: platform.CallOutputState{
			DecodedFrames: 10, LevelDB: math.Inf(-1), LastDecodedAt: decodedAt, Error: "decoder warning",
		},
		closeState: platform.CallOutputState{
			DecodedFrames: 12, LevelDB: -24, LastDecodedAt: decodedAt.Add(time.Second), Error: "final decoder warning",
		},
	}
	c.native.startAudio = func(context.Context, func([]byte) error) (callAudio, error) { return audio, nil }
	if err := c.StartCall(context.Background(), core.Chat{ID: "user:2"}); err != nil {
		t.Fatal(err)
	}
	state := waitCallStatus(t, c, "active")
	if state.AudioDecodedFrames != 10 || !math.IsInf(state.AudioLevelDB, -1) || !state.AudioLastDecodedAt.Equal(decodedAt) || state.AudioOutputError != "decoder warning" {
		t.Fatalf("missing live decoder snapshot: %+v", state)
	}
	if state.Error != "" {
		t.Fatalf("nonfatal decoder diagnostic became a call failure: %v", state.Error)
	}
	if err := c.EndCall(context.Background(), state.ID); err != nil {
		t.Fatal(err)
	}
	state = c.CallState()
	if state.Status != "ended" || state.AudioDecodedFrames != 12 || state.AudioLevelDB != -24 || !state.AudioLastDecodedAt.Equal(decodedAt.Add(time.Second)) || state.AudioOutputError != "final decoder warning" {
		t.Fatalf("decoder state after player shutdown was lost: %+v", state)
	}
}

func TestNativeCallWithoutDecoderTelemetryStillWorks(t *testing.T) {
	c, _, _, _, _ := nativeTestClient(t)
	if err := c.StartCall(context.Background(), core.Chat{ID: "user:2"}); err != nil {
		t.Fatal(err)
	}
	state := waitCallStatus(t, c, "active")
	if state.AudioDecodedFrames != 0 || !state.AudioLastDecodedAt.IsZero() || state.AudioOutputError != "" {
		t.Fatalf("bridge without diagnostics reported decoder activity: %+v", state)
	}
}
