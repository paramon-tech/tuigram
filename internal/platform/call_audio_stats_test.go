package platform

import (
	"context"
	"math"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCallOutputStateParsesFragmentedLevelsAndSilentFrames(t *testing.T) {
	a := &CallAudio{}
	for _, fragment := range []string{
		"frame:0 pts:0\nlavfi.astats.Overall.RMS_",
		"level=-24.5\nlavfi.astats.Overall.RMS_level=NaN\n",
		"lavfi.astats.Overall.RMS_level=+Inf\nlavfi.astats.Overall.RMS_level=invalid\n",
	} {
		if n, err := a.output.Write([]byte(fragment)); err != nil || n != len(fragment) {
			t.Fatalf("metadata write failed: %d %v", n, err)
		}
	}
	state := a.OutputState()
	if state.DecodedFrames != 1 || state.LevelDB != -24.5 || state.LastDecodedAt.IsZero() {
		t.Fatalf("invalid metadata treated as decoded sound: %+v", state)
	}
	a.output.Write([]byte("lavfi.astats.Overall.RMS_level=-inf\n"))
	state = a.OutputState()
	if state.DecodedFrames != 2 || !math.IsInf(state.LevelDB, -1) {
		t.Fatalf("silence was not counted as valid decoded audio: %+v", state)
	}
}

func TestCallOutputStateBoundsAndResynchronizesMetadata(t *testing.T) {
	a := &CallAudio{}
	a.output.Write([]byte(strings.Repeat("x", 1024)))
	if len(a.output.pending) > 512 {
		t.Fatal("unterminated metadata grew without a bound")
	}
	// A plausible suffix inside an overlong line must not become a new frame.
	a.output.Write([]byte("lavfi.astats.Overall.RMS_level=-10\n"))
	a.output.Write([]byte("lavfi.astats.Overall.RMS_level=-30\n"))
	if state := a.OutputState(); state.DecodedFrames != 1 || state.LevelDB != -30 {
		t.Fatalf("metadata parser did not recover at line boundary: %+v", state)
	}
}

func TestCallOutputSnapshotsAreSafeDuringDecoderDiagnostics(t *testing.T) {
	a := &CallAudio{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			a.output.Write([]byte("lavfi.astats.Overall.RMS_level=-20\n"))
			a.output.stderr.Write([]byte("decoder diagnostic\n"))
		}
	}()
	for range 1000 {
		if state := a.OutputState(); len(state.Error) > 4096 {
			t.Fatal("live decoder diagnostics grew without a bound")
		}
	}
	<-done
	if state := a.OutputState(); state.DecodedFrames != 1000 || !strings.Contains(state.Error, "decoder diagnostic") {
		t.Fatalf("live decoder telemetry missing: %+v", state)
	}
}

// Exercise silent Opus and malformed Opus against the actual live player. Both
// can arrive as valid RTP and keep ffplay alive, but require different guidance.
// The output driver is dummy, and every input is generated without a microphone.
func TestCallAudioSyntheticSilenceAndDecoderError(t *testing.T) {
	if os.Getenv("TUIGRAM_TEST_CALL_AUDIO") != "1" {
		t.Skip("set TUIGRAM_TEST_CALL_AUDIO=1 with FFmpeg installed for synthetic output-state tests")
	}
	ffmpeg, ffplay, err := callAudioTools()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SDL_AUDIODRIVER", "dummy")
	for _, corrupt := range []bool{false, true} {
		name := "silence"
		if corrupt {
			name = "decoder error while process remains alive"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var session atomic.Pointer[CallAudio]
			a, err := startCallAudio(ctx, ffmpeg, ffplay,
				[]string{"-re", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=mono"}, func(packet []byte) error {
					if a := session.Load(); a != nil {
						if corrupt {
							// This is a valid 40ms Opus TOC, but its VBR length
							// table is truncated. RTP parsing accepts the packet;
							// the actual decoder must expose its failure.
							packet = append(append([]byte(nil), packet[:12]...), 0xfa, 0xff, 0xff)
						}
						return a.WriteRTP(packet)
					}
					return nil
				})
			if err != nil {
				t.Fatal(err)
			}
			session.Store(a)
			defer a.Close()
			deadline := time.NewTimer(3 * time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			for {
				state := a.OutputState()
				if corrupt && state.Error != "" {
					if state.DecodedFrames != 0 || a.ctx.Err() != nil || !strings.Contains(state.Error, "packet") {
						t.Fatalf("nonfatal decoder error not distinguished: %+v", state)
					}
					return
				}
				if !corrupt && state.DecodedFrames >= 5 {
					// Opus may add inaudible quantization noise to encoded zeros.
					if state.LevelDB > -80 || state.Error != "" || state.LastDecodedAt.IsZero() {
						t.Fatalf("silent remote audio not distinguished: %+v", state)
					}
					return
				}
				select {
				case err := <-a.Errors():
					t.Fatalf("audio process stopped unexpectedly: %v", err)
				case <-deadline.C:
					t.Fatalf("player did not report live output state: %+v", a.OutputState())
				case <-tick.C:
				}
			}
		})
	}
}
