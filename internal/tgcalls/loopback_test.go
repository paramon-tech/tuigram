package calls

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/pion/logging"
	"github.com/pion/rtp"
	"github.com/pion/transport/v4/vnet"
	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/gotd/log"
	"github.com/gotd/log/logzap"
)

// TestConnLoopback runs a full 1:1 call handshake between two Conn instances
// over an in-memory pion virtual network: ICE gathering, the InitialSetup /
// Candidates / NegotiateChannels signaling exchange, the DTLS handshake and the
// SRTP media path — with no real network and no Telegram server.
func TestConnLoopback(t *testing.T) {
	if testing.Short() {
		t.Skip("loopback handshake is slow")
	}

	// Two virtual NICs on a shared subnet: direct host-candidate connectivity.
	router, err := vnet.NewRouter(&vnet.RouterConfig{
		CIDR:          "10.0.0.0/24",
		LoggerFactory: logging.NewDefaultLoggerFactory(),
	})
	require.NoError(t, err)
	callerNet, err := vnet.NewNet(&vnet.NetConfig{StaticIPs: []string{"10.0.0.1"}})
	require.NoError(t, err)
	calleeNet, err := vnet.NewNet(&vnet.NetConfig{StaticIPs: []string{"10.0.0.2"}})
	require.NoError(t, err)
	require.NoError(t, router.AddNet(callerNet))
	require.NoError(t, router.AddNet(calleeNet))
	require.NoError(t, router.Start())
	defer func() { _ = router.Stop() }()

	zapLog := zaptest.NewLogger(t)
	caller := newConn(true, log.For(logzap.New(zapLog.Named("caller"))))
	caller.net = callerNet
	callee := newConn(false, log.For(logzap.New(zapLog.Named("callee"))))
	callee.net = calleeNet
	defer func() { _ = caller.Close(); _ = callee.Close() }()

	// Preserve each sender's signaling order, as the app's serialized update
	// handler and sequential sendSignalingData calls do. In particular the
	// callee sends the caller's answer before proposing its own send channel.
	signalsDone := make(chan struct{})
	var signalsWG sync.WaitGroup
	defer func() { close(signalsDone); signalsWG.Wait() }()
	bridge := func(from, to *Conn) {
		queue := make(chan []byte, 64)
		signalsWG.Add(1)
		go func() {
			defer signalsWG.Done()
			for {
				select {
				case data := <-queue:
					_ = to.onSignal(data)
				case <-signalsDone:
					return
				}
			}
		}()
		from.emit = func(payload []byte) {
			data := append([]byte(nil), payload...)
			select {
			case queue <- data:
			case <-signalsDone:
			}
		}
	}
	bridge(caller, callee)
	bridge(callee, caller)

	callerUp := make(chan struct{})
	calleeUp := make(chan struct{})
	caller.OnConnected(func() { close(callerUp) })
	callee.OnConnected(func() { close(calleeUp) })

	// Both directions must deliver actual decrypted RTP; a successful write
	// alone can silently drop media when its sender has not been negotiated.
	callerRTP, calleeRTP := make(chan struct{}), make(chan struct{})
	registerReceive := func(conn, peer *Conn, gotRTP chan struct{}, marker byte) {
		var once sync.Once
		conn.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
			if track.Kind() != webrtc.RTPCodecTypeAudio {
				_ = receiver.Stop()
				return
			}
			go func() {
				defer receiver.Stop()
				if packet, _, err := track.ReadRTP(); err == nil {
					if packet.SSRC != peer.AudioSSRC() || !bytes.Equal(packet.Payload, bytes.Repeat([]byte{marker}, 60)) {
						t.Errorf("received RTP from wrong source: SSRC=%d want=%d payload=%x", packet.SSRC, peer.AudioSSRC(), packet.Payload)
					}
					once.Do(func() { close(gotRTP) })
				}
			}()
		})
	}
	registerReceive(caller, callee, callerRTP, 0x42)
	registerReceive(callee, caller, calleeRTP, 0x41)

	require.NoError(t, caller.open(nil))
	require.NoError(t, callee.open(nil))
	require.NoError(t, caller.start()) // caller offers first
	require.NoError(t, callee.start()) // callee waits

	waitClosed(t, callerUp, 30*time.Second, "caller never connected")
	waitClosed(t, calleeUp, 30*time.Second, "callee never connected")

	// Push distinct audio in each direction until both receivers get packets.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		var seq uint16
		var ts uint32
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			seq++
			ts += 960
			_ = caller.AudioTrack().WriteRTP(&rtp.Packet{
				Header:  rtp.Header{Version: 2, PayloadType: 111, SequenceNumber: seq, Timestamp: ts},
				Payload: bytes.Repeat([]byte{0x41}, 60),
			})
			_ = callee.AudioTrack().WriteRTP(&rtp.Packet{
				Header:  rtp.Header{Version: 2, PayloadType: 111, SequenceNumber: seq, Timestamp: ts},
				Payload: bytes.Repeat([]byte{0x42}, 60),
			})
		}
	}()

	waitClosed(t, calleeRTP, 30*time.Second, "callee never received caller audio RTP")
	waitClosed(t, callerRTP, 30*time.Second, "caller never received callee audio RTP")
}

func waitClosed(t *testing.T, ch <-chan struct{}, d time.Duration, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(d):
		t.Fatal(msg)
	}
}
