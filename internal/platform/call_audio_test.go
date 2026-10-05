package platform

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"net"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// An alive ffplay process does not prove that incoming audio is decoded: a
// player waiting for EOF or buffering forever can pass the duplex lifetime
// test below. Measure real non-silent decoded PCM while the RTP producer and
// Ogg input remain open, using exactly the live call playback arguments.
func TestCallAudioDecodesBeforeStreamCloses(t *testing.T) {
	if os.Getenv("TUIGRAM_TEST_CALL_AUDIO") != "1" {
		t.Skip("set TUIGRAM_TEST_CALL_AUDIO=1 with FFmpeg installed for synthetic playback test")
	}
	ffmpeg, ffplay, err := callAudioTools()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SDL_AUDIODRIVER", "dummy")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	player := exec.CommandContext(ctx, ffplay, playbackCallArgs()...)
	var playerStderr bytes.Buffer
	player.Stderr = &playerStderr
	input, err := player.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := player.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := player.Start(); err != nil {
		t.Fatal(err)
	}
	playerDone := make(chan struct{})
	var playerErr error
	go func() {
		playerErr = player.Wait()
		close(playerDone)
	}()
	defer func() { cancel(); <-playerDone }()

	decoded := make(chan float64, 16)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			level, ok := strings.CutPrefix(scanner.Text(), "lavfi.astats.Overall.RMS_level=")
			if !ok {
				continue
			}
			rms, err := strconv.ParseFloat(level, 64)
			if err == nil && !math.IsNaN(rms) && !math.IsInf(rms, 0) && rms > -60 {
				select {
				case decoded <- rms:
				default:
				}
			}
		}
	}()
	defer func() { cancel(); <-readerDone }()

	a := &CallAudio{ctx: ctx, cancel: cancel, socket: socket, input: input,
		incoming: make(chan opusRTP, 16), errors: make(chan error, 1)}
	bridgeDone := make(chan struct{})
	go func() {
		defer close(bridgeDone)
		a.playback()
	}()
	captureDone := make(chan struct{})
	go func() {
		defer close(captureDone)
		a.capture(a.WriteRTP)
	}()
	defer func() {
		cancel()
		input.Close()
		socket.Close()
		<-bridgeDone
		<-captureDone
	}()

	producer := exec.CommandContext(ctx, ffmpeg, captureCallArgs(
		[]string{"-re", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000"},
		socket.LocalAddr().(*net.UDPAddr).Port)...)
	var producerStderr bytes.Buffer
	producer.Stderr = &producerStderr
	if err := producer.Start(); err != nil {
		t.Fatal(err)
	}
	producerDone := make(chan struct{})
	var producerErr error
	go func() {
		producerErr = producer.Wait()
		close(producerDone)
	}()
	defer func() { cancel(); <-producerDone }()

	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for frame := 0; frame < 10; {
		select {
		case <-decoded:
			frame++
		case err := <-a.Errors():
			t.Fatalf("audio bridge failed: %v", err)
		case <-playerDone:
			t.Fatalf("player exited before live playback: %v: %s", playerErr, playerStderr.String())
		case <-producerDone:
			t.Fatalf("tone producer exited before live playback: %v: %s", producerErr, producerStderr.String())
		case <-deadline.C:
			t.Fatalf("only %d non-silent frames decoded while call stream remained open", frame)
		}
	}
}

func TestCallInputDefaultsAndValidation(t *testing.T) {
	for _, tc := range []struct{ os, format, device string }{
		{"darwin", "avfoundation", "none:default"},
		{"linux", "pulse", "default"},
		{"freebsd", "oss", "/dev/dsp"},
	} {
		format, device, err := callInput(CallAudioOptions{}, tc.os)
		if err != nil || format != tc.format || device != tc.device {
			t.Errorf("%s defaults: %q %q %v", tc.os, format, device, err)
		}
	}
	for _, opts := range []CallAudioOptions{
		{InputFormat: "lavfi", InputDevice: "movie=/private/file"},
		{InputFormat: "avfoundation", InputDevice: "0:0"},
		{InputFormat: "pulse", InputDevice: "bad\nname"},
	} {
		if _, _, err := callInput(opts, "darwin"); err == nil {
			t.Errorf("accepted invalid options: %+v", opts)
		}
	}
	format, device, err := callInput(CallAudioOptions{InputDevice: "USB Microphone ; $(touch injected)"}, "darwin")
	if err != nil || format != "avfoundation" || device != "none:USB Microphone ; $(touch injected)" {
		t.Fatalf("device names must remain a single literal argument: %q %q %v", format, device, err)
	}
}

func TestCallAudioMissingTools(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := CheckCallAudio(context.Background(), CallAudioOptions{InputFormat: "pulse"})
	if err == nil || !strings.Contains(err.Error(), "FFmpeg") {
		t.Fatalf("missing tools did not give installation guidance: %v", err)
	}
}

func rtpTestPacket(seq uint16) []byte {
	b := []byte{0x80, 111, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0xf8, 0xff, 0xfe}
	binary.BigEndian.PutUint16(b[2:4], seq)
	binary.BigEndian.PutUint32(b[4:8], uint32(seq)*960)
	return b
}

func TestCallOpusRTPParsing(t *testing.T) {
	b := rtpTestPacket(65535)
	p, err := parseOpusRTP(b)
	if err != nil || p.sequence != 65535 || p.samples != 960 || !bytes.Equal(p.payload, b[12:]) {
		t.Fatalf("valid packet: %+v %v", p, err)
	}
	extended := append([]byte(nil), b[:12]...)
	extended[0] = 0xb1 // extension, padding, one CSRC
	extended = append(extended, 0, 0, 0, 1, 0xbe, 0xde, 0, 1, 0, 0, 0, 0)
	extended = append(extended, b[12:]...)
	extended = append(extended, 0, 2)
	p, err = parseOpusRTP(extended)
	if err != nil || !bytes.Equal(p.payload, b[12:]) {
		t.Fatalf("RTP extensions or padding leaked into Opus payload: %+v %v", p, err)
	}
	for i := 0; i < 12; i++ {
		if _, err := parseOpusRTP(b[:i]); err == nil {
			t.Errorf("accepted truncated RTP header of %d bytes", i)
		}
	}
	for _, invalid := range [][]byte{
		append([]byte{0x90}, b[1:]...),                     // truncated extension
		append([]byte{0xaf}, b[1:]...),                     // truncated CSRCs and bad padding
		append(append([]byte(nil), b[:12]...), 0xfb, 0x3f), // >120 ms
	} {
		if _, err := parseOpusRTP(invalid); err == nil {
			t.Fatalf("accepted malformed packet: %x", invalid)
		}
	}
}

func TestCallOpusDurations(t *testing.T) {
	for _, tc := range []struct {
		toc     byte
		samples uint32
	}{
		{0x00, 480}, {0x08, 960}, {0x10, 1920}, {0x18, 2880},
		{0x60, 480}, {0x68, 960}, {0x80, 120}, {0x88, 240}, {0x90, 480}, {0x98, 960}, {0x99, 1920},
	} {
		got, err := opusSamples([]byte{tc.toc, 0xff})
		if err != nil || got != tc.samples {
			t.Errorf("TOC %x: got %d, want %d: %v", tc.toc, got, tc.samples, err)
		}
	}
}

func TestCallRTPReorderLossAndWraparound(t *testing.T) {
	var r reorderOpus
	var seen []uint16
	write := func(p opusRTP) error { seen = append(seen, p.sequence); return nil }
	now := time.Now()
	for _, seq := range []uint16{65534, 0, 65535, 0, 3, 4} {
		if err := r.push(opusRTP{sequence: seq}, now, write); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(seen, []uint16{65534, 65535, 0}) {
		t.Fatalf("reorder/duplicates failed: %v", seen)
	}
	if err := r.drain(now.Add(61*time.Millisecond), write); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(seen, []uint16{65534, 65535, 0, 3, 4}) {
		t.Fatalf("loss timeout failed: %v", seen)
	}
	if len(r.pending) != 0 {
		t.Fatal("buffer did not drain")
	}
}

func TestCallReceiveQueueBoundedAndCopiesPayload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &CallAudio{ctx: ctx, incoming: make(chan opusRTP, 16)}
	for i := range 100 {
		b := rtpTestPacket(uint16(i))
		if err := a.WriteRTP(b); err != nil {
			t.Fatal(err)
		}
		b[12] = 0
	}
	if len(a.incoming) != 16 {
		t.Fatalf("queue length %d", len(a.incoming))
	}
	first := <-a.incoming
	if first.sequence != 84 || first.payload[0] != 0xf8 {
		t.Fatalf("queue retained stale or caller-owned data: %+v", first)
	}
	cancel()
	if err := a.WriteRTP(rtpTestPacket(100)); !errors.Is(err, context.Canceled) {
		t.Fatalf("accepted audio after close: %v", err)
	}
}

func TestCallCaptureMuteAndCancel(t *testing.T) {
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	sender, err := net.DialUDP("udp4", nil, listener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &CallAudio{ctx: ctx, cancel: cancel, socket: listener, errors: make(chan error, 1)}
	seen := make(chan uint16, 10)
	done := make(chan struct{})
	go func() {
		a.capture(func(b []byte) error { seen <- binary.BigEndian.Uint16(b[2:4]); return nil })
		close(done)
	}()
	a.SetMuted(true)
	sender.Write(rtpTestPacket(1))
	select {
	case <-seen:
		t.Fatal("muted microphone sent audio")
	case <-time.After(40 * time.Millisecond):
	}
	a.SetMuted(false)
	sender.Write(rtpTestPacket(2))
	select {
	case seq := <-seen:
		if seq != 2 {
			t.Fatalf("unexpected sequence: %d", seq)
		}
	case <-time.After(time.Second):
		t.Fatal("unmuted microphone did not resume")
	}
	cancel()
	listener.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("capture did not stop")
	}
}

// This opt-in integration test never opens a microphone or real output device.
// It checks FFmpeg RTP encoding, ffplay Ogg decoding, duplex process lifetime,
// mute gating, and cancellation against the actual installed binaries.
func TestCallAudioSyntheticLoopback(t *testing.T) {
	if os.Getenv("TUIGRAM_TEST_CALL_AUDIO") != "1" {
		t.Skip("set TUIGRAM_TEST_CALL_AUDIO=1 with FFmpeg installed for synthetic duplex audio test")
	}
	ffmpeg, ffplay, err := callAudioTools()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SDL_AUDIODRIVER", "dummy")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var session atomic.Pointer[CallAudio]
	var sent atomic.Int64
	a, err := startCallAudio(ctx, ffmpeg, ffplay, []string{"-re", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000"}, func(b []byte) error {
		sent.Add(1)
		if a := session.Load(); a != nil {
			return a.WriteRTP(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	session.Store(a)
	defer a.Close()
	timer := time.NewTimer(time.Second)
	select {
	case err := <-a.Errors():
		t.Fatalf("audio process failed: %v", err)
	case <-timer.C:
	}
	if sent.Load() < 10 {
		t.Fatalf("microphone simulator produced only %d packets", sent.Load())
	}
	if state := a.OutputState(); state.DecodedFrames < 10 || state.LevelDB < -60 || state.LastDecodedAt.IsZero() || state.Error != "" {
		t.Fatalf("live speaker did not decode non-silent audio: %+v", state)
	}
	a.SetMuted(true)
	time.Sleep(40 * time.Millisecond) // allow any in-flight callback to finish
	before := sent.Load()
	time.Sleep(80 * time.Millisecond)
	if sent.Load() != before {
		t.Fatal("muted bridge sent audio")
	}
	a.SetMuted(false)
	time.Sleep(80 * time.Millisecond)
	if sent.Load() <= before {
		t.Fatal("unmuted bridge did not resume audio")
	}
	start := time.Now()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("closing audio took more than a second")
	}
	for err := range a.Errors() {
		t.Fatalf("audio failed before shutdown: %v", err)
	}
	// Verify that an independent decoder accepts headers, lacing, and CRCs.
	var ogg bytes.Buffer
	pages := &opusPages{writer: &ogg}
	if err := pages.headers(); err != nil {
		t.Fatal(err)
	}
	p, _ := parseOpusRTP(rtpTestPacket(1))
	for range 5 {
		if err := pages.packet(p); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "ogg", "-i", "pipe:0", "-f", "s16le", "pipe:1")
	cmd.Stdin = &ogg
	pcm, err := cmd.Output()
	if err != nil {
		t.Fatalf("decode generated Ogg: %v", err)
	}
	if len(pcm) != 5*960*2*2 {
		t.Fatalf("decoded PCM length %d", len(pcm))
	}
}
