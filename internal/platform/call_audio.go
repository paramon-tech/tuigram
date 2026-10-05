package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// CallAudioOptions selects the microphone. Empty values use the OS default.
// Speakers follow the system output device. The capture process is never given
// terminal input, and audio is never written to disk.
type CallAudioOptions struct {
	InputFormat string
	InputDevice string
}

func callInput(opts CallAudioOptions, goos string) (string, string, error) {
	format, device := opts.InputFormat, opts.InputDevice
	if format == "" {
		switch goos {
		case "darwin":
			format = "avfoundation"
		case "linux":
			format = "pulse"
		case "openbsd":
			format = "sndio"
		case "freebsd", "netbsd":
			format = "oss"
		case "windows":
			format = "dshow"
		default:
			return "", "", fmt.Errorf("native call microphone capture is not configured for %s", goos)
		}
	}
	switch format {
	case "avfoundation":
		if device == "" {
			device = "default"
		}
		if strings.Contains(device, ":") {
			return "", "", errors.New("call_input_device must be an audio device name or index, without a video-device prefix")
		}
		device = "none:" + device
	case "pulse", "alsa", "sndio":
		if device == "" {
			device = "default"
		}
	case "oss":
		if device == "" {
			device = "/dev/dsp"
		}
	case "dshow":
		if device == "" {
			return "", "", errors.New("set call_input_device to your DirectShow microphone name; use tuigram audio devices")
		}
		device = "audio=" + device
	default:
		return "", "", fmt.Errorf("unsupported call_input_format %q (use avfoundation, pulse, alsa, oss, sndio, or dshow)", format)
	}
	if strings.ContainsAny(device, "\x00\r\n") {
		return "", "", errors.New("call_input_device contains invalid characters")
	}
	return format, device, nil
}

func callAudioTools() (string, string, error) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", "", errors.New("native calls need FFmpeg with libopus and ffplay installed on PATH (macOS: brew install ffmpeg)")
	}
	ffplay, err := exec.LookPath("ffplay")
	if err != nil {
		return "", "", errors.New("native calls need ffplay installed on PATH; install an FFmpeg build with SDL audio support")
	}
	return ffmpeg, ffplay, nil
}

// CheckCallAudio checks executable, codec, and capture-backend availability.
// It does not open a microphone or a speaker.
func CheckCallAudio(ctx context.Context, opts CallAudioOptions) error {
	format, _, err := callInput(opts, runtime.GOOS)
	if err != nil {
		return err
	}
	ffmpeg, ffplay, err := callAudioTools()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, check := range []struct {
		path string
		args []string
		want string
	}{
		{ffmpeg, []string{"-hide_banner", "-encoders"}, "libopus"},
		{ffmpeg, []string{"-hide_banner", "-devices"}, format},
		{ffplay, []string{"-version"}, "ffplay version"},
	} {
		out, runErr := exec.CommandContext(ctx, check.path, check.args...).CombinedOutput()
		if runErr != nil {
			return fmt.Errorf("check native call audio tools: %w: %s", runErr, strings.TrimSpace(string(out)))
		}
		if !strings.Contains(string(out), check.want) {
			return fmt.Errorf("installed FFmpeg lacks %s support needed for native calls", check.want)
		}
	}
	return nil
}

// ListCallAudioDevices returns FFmpeg's microphone discovery output. It may
// prompt macOS for permission to enumerate audio devices but does not record.
func ListCallAudioDevices(ctx context.Context, opts CallAudioOptions) (string, error) {
	// DirectShow requires a device when recording but not when enumerating.
	if opts.InputFormat == "dshow" || (opts.InputFormat == "" && runtime.GOOS == "windows") {
		opts.InputDevice = "unused"
	}
	format, _, err := callInput(opts, runtime.GOOS)
	if err != nil {
		return "", err
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", errors.New("install FFmpeg to list microphone devices")
	}
	args := []string{"-hide_banner", "-sources", format}
	if format == "avfoundation" || format == "dshow" {
		args = []string{"-hide_banner", "-f", format, "-list_devices", "true", "-i", ""}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	text := strings.TrimSpace(string(out))
	// AVFoundation and DirectShow intentionally exit nonzero after listing.
	listed := strings.Contains(text, "AVFoundation audio devices:") || strings.Contains(text, "(audio)")
	if err != nil && !listed {
		return "", fmt.Errorf("list %s audio devices: %w: %s", format, err, text)
	}
	if format == "avfoundation" && listed {
		// FFmpeg lists both camera and microphone devices, then intentionally
		// prints an input-open error. Show only its successful audio listing.
		var audioLines []string
		inAudio := false
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, "AVFoundation audio devices:") {
				inAudio = true
			}
			if inAudio && strings.HasPrefix(line, "[AVFoundation indev @") {
				if end := strings.Index(line, "]"); end >= 0 {
					audioLines = append(audioLines, strings.TrimSpace(line[end+1:]))
				}
			}
		}
		text = strings.Join(audioLines, "\n")
	}
	return text + "\nSpeaker output follows the system default audio device.", nil
}

// CallAudio is a duplex Opus/RTP bridge. Its receive queue is bounded so a slow
// speaker cannot stall WebRTC. Close stops and reaps both child processes.
type CallAudio struct {
	ctx      context.Context
	cancel   context.CancelFunc
	socket   *net.UDPConn
	input    io.WriteCloser
	incoming chan opusRTP
	errors   chan error
	muted    atomic.Bool
	wg       sync.WaitGroup
	done     chan struct{}
}

// StartCallAudio opens the microphone and speaker until ctx ends or Close is
// called. send receives owned RTP packets and must return promptly (normally a
// WebRTC TrackLocalStaticRTP.Write call). CheckCallAudio should run before call
// signaling so missing dependencies do not ring the remote user.
func StartCallAudio(ctx context.Context, opts CallAudioOptions, send func([]byte) error) (*CallAudio, error) {
	format, device, err := callInput(opts, runtime.GOOS)
	if err != nil {
		return nil, err
	}
	ffmpeg, ffplay, err := callAudioTools()
	if err != nil {
		return nil, err
	}
	return startCallAudio(ctx, ffmpeg, ffplay, []string{"-f", format, "-i", device}, send)
}

func captureCallArgs(inputArgs []string, port int) []string {
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error"}
	args = append(args, inputArgs...)
	return append(args, "-map", "0:a:0", "-vn", "-ac", "1", "-ar", "48000",
		"-c:a", "libopus", "-application", "voip", "-frame_duration", "20", "-b:a", "32k",
		"-flush_packets", "1", "-payload_type", "111", "-rtpflags", "skip_rtcp", "-f", "rtp",
		"rtp://127.0.0.1:"+strconv.Itoa(port)+"?pkt_size=1200&connect=1")
}

func startCallAudio(ctx context.Context, ffmpeg, ffplay string, inputArgs []string, send func([]byte) error) (*CallAudio, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if send == nil {
		return nil, errors.New("native call audio requires an RTP sender")
	}
	ctx, cancel := context.WithCancel(ctx)
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open local call audio socket: %w", err)
	}
	a := &CallAudio{ctx: ctx, cancel: cancel, socket: socket, incoming: make(chan opusRTP, 16), errors: make(chan error, 1), done: make(chan struct{})}
	player := exec.CommandContext(ctx, ffplay, "-hide_banner", "-loglevel", "error", "-nostats", "-nodisp", "-autoexit",
		"-probesize", "32", "-analyzeduration", "0", "-protocol_whitelist", "pipe", "-f", "ogg", "-i", "pipe:0")
	var playerErr, captureErr audioErrorTail
	player.Stderr = &playerErr
	a.input, err = player.StdinPipe()
	if err != nil {
		cancel()
		socket.Close()
		return nil, err
	}
	if err = player.Start(); err != nil {
		cancel()
		a.input.Close()
		socket.Close()
		return nil, fmt.Errorf("start call speaker: %w", err)
	}
	capture := exec.CommandContext(ctx, ffmpeg, captureCallArgs(inputArgs, socket.LocalAddr().(*net.UDPAddr).Port)...)
	capture.Stderr = &captureErr
	if err = capture.Start(); err != nil {
		cancel()
		a.input.Close()
		socket.Close()
		player.Wait()
		return nil, fmt.Errorf("start call microphone: %w", err)
	}
	a.wg.Add(4)
	go func() { defer a.wg.Done(); a.capture(send) }()
	go func() { defer a.wg.Done(); a.playback() }()
	go func() {
		defer a.wg.Done()
		a.waitProcess(capture, &captureErr, "microphone (check microphone permissions and call_input_device)")
	}()
	go func() {
		defer a.wg.Done()
		a.waitProcess(player, &playerErr, "speaker (check your system output device)")
	}()
	go func() {
		<-ctx.Done()
		socket.Close()
		a.input.Close()
		a.wg.Wait()
		close(a.errors)
		close(a.done)
	}()
	return a, nil
}

func (a *CallAudio) waitProcess(cmd *exec.Cmd, stderr *audioErrorTail, device string) {
	err := cmd.Wait()
	if a.ctx.Err() == nil {
		if err == nil {
			err = errors.New("audio process exited")
		}
		a.fail(fmt.Errorf("call %s failed: %w: %s", device, err, stderr.String()))
	}
}

func (a *CallAudio) fail(err error) {
	select {
	case a.errors <- err:
	default:
	}
	a.cancel()
}

func (a *CallAudio) capture(send func([]byte) error) {
	buf := make([]byte, 2048)
	for {
		n, from, err := a.socket.ReadFromUDP(buf)
		if err != nil {
			if a.ctx.Err() == nil {
				a.fail(fmt.Errorf("read call microphone: %w", err))
			}
			return
		}
		if !from.IP.IsLoopback() || a.muted.Load() {
			continue
		}
		if _, err := parseOpusRTP(buf[:n]); err != nil || buf[1]&127 != 111 {
			continue
		}
		if err := send(append([]byte(nil), buf[:n]...)); err != nil {
			if a.ctx.Err() == nil {
				a.fail(fmt.Errorf("send call microphone audio: %w", err))
			}
			return
		}
	}
}

// WriteRTP queues an Opus RTP packet for the speaker. It copies the payload;
// callers may reuse their packet memory immediately. Overload drops the oldest
// packet to keep the conversation live instead of accumulating audio delay.
func (a *CallAudio) WriteRTP(packet []byte) error {
	if err := a.ctx.Err(); err != nil {
		return err
	}
	p, err := parseOpusRTP(packet)
	if err != nil {
		return err
	}
	p.payload = append([]byte(nil), p.payload...)
	select {
	case a.incoming <- p:
	default:
		select {
		case <-a.incoming:
		default:
		}
		select {
		case a.incoming <- p:
		default:
		}
	}
	return nil
}

// SetMuted gates all outgoing audio while continuing microphone capture and
// speaker playback. Muting is local and never depends on a signaling response.
func (a *CallAudio) SetMuted(muted bool) { a.muted.Store(muted) }

func (a *CallAudio) Errors() <-chan error { return a.errors }

func (a *CallAudio) Close() error {
	a.cancel()
	<-a.done
	return nil
}

// Keep only the tail; a malfunctioning child must not exhaust application RAM.
type audioErrorTail struct{ data []byte }

func (b *audioErrorTail) Write(p []byte) (int, error) {
	n := len(p)
	const limit = 4096
	if n >= limit {
		b.data = append(b.data[:0], p[n-limit:]...)
	} else {
		if len(b.data)+n > limit {
			b.data = b.data[len(b.data)+n-limit:]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}

func (b *audioErrorTail) String() string { return strings.TrimSpace(string(b.data)) }
