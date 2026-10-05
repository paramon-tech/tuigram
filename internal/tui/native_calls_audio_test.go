package tui

import (
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/paramon-tech/tuigram/internal/core"
)

func TestCallPanelShowsAudioPacketActivity(t *testing.T) {
	m := Model{call: core.CallState{ID: 1, Status: "active", Chat: core.Chat{ID: "user:2", Title: "Alice"}}}
	text := m.callText()
	for _, want := range []string{"Outgoing audio: waiting for microphone packets", "Incoming audio: waiting for remote packets"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in call panel:\n%s", want, text)
		}
	}
	m.call.SentPackets = 100
	m.call.ReceivedPackets = 200
	text = m.callText()
	for _, want := range []string{"Outgoing audio: sent 100 packets", "Incoming audio: received 200 packets"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in call panel:\n%s", want, text)
		}
	}
	if strings.Contains(text, "waiting for") {
		t.Fatalf("active streams still described as waiting:\n%s", text)
	}
	m.call.Status = "ringing"
	m.call.Incoming = true
	if text := m.callText(); strings.Contains(text, "Incoming audio:") || strings.Contains(text, "Outgoing audio:") {
		t.Fatalf("ringing call unexpectedly claims audio activity:\n%s", text)
	}
}

func TestCallPanelShowsDecodedSilenceSignalAndStall(t *testing.T) {
	for _, tc := range []struct {
		name   string
		frames uint64
		level  float64
		age    time.Duration
		want   string
	}{
		{name: "waiting", want: "Decoded audio: waiting"},
		{name: "silence", frames: 1, level: math.Inf(-1), want: "Decoded audio: silence (-Inf dBFS)"},
		{name: "quiet", frames: 1, level: -75.2, want: "Decoded audio: very quiet (-75.2 dBFS)"},
		{name: "signal", frames: 1, level: -20.1, want: "Decoded audio: signal (-20.1 dBFS)"},
		{name: "stalled", frames: 1, level: -20.1, age: 3 * time.Second, want: "Decoded audio: signal (-20.1 dBFS) · stalled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := Model{call: core.CallState{
				ID: 1, Status: "active", Chat: core.Chat{ID: "user:2", Title: "Alice"},
				AudioDecodedFrames: tc.frames, AudioLevelDB: tc.level, AudioLastDecodedAt: time.Now().Add(-tc.age),
			}}
			if text := m.callText(); !strings.Contains(text, tc.want) {
				t.Fatalf("missing decoder status %q:\n%s", tc.want, text)
			}
		})
	}
}

func TestCallPanelBoundsAndSanitizesDecoderError(t *testing.T) {
	m := Model{width: 64, call: core.CallState{
		ID: 1, Status: "active", Chat: core.Chat{ID: "user:2", Title: "Alice"},
		AudioOutputError: "old diagnostic\n\x1b[31mlast decoder error\x1b[0m\u202e " + strings.Repeat("details ", 100) + "\n\n",
	}}
	text := m.callText()
	if strings.Contains(text, "old diagnostic") || strings.ContainsAny(text, "\x1b\u202e") {
		t.Fatalf("decoder log retained old lines or control characters:\n%s", text)
	}
	lines := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "Audio output:") {
			lines++
			if !strings.Contains(line, "last decoder error") || utf8.RuneCountInString(line) > 60 {
				t.Fatalf("decoder diagnostic not clipped to one panel line: %q", line)
			}
		}
	}
	if lines != 1 {
		t.Fatalf("expected exactly one decoder error line, got %d:\n%s", lines, text)
	}
}
