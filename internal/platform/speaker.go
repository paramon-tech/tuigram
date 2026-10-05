package platform

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// PlaySpeakerTest plays a short tone through the same player and system output
// used by native calls. It never opens the microphone or connects to Telegram.
func PlaySpeakerTest(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	player, err := exec.LookPath("ffplay")
	if err != nil {
		return errors.New("speaker test needs ffplay (FFmpeg) installed on PATH")
	}
	return runSpeakerTest(ctx, player, []string{
		"-hide_banner", "-loglevel", "error", "-nostats", "-nodisp", "-autoexit",
		"-volume", "25", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2",
	})
}

func runSpeakerTest(ctx context.Context, player string, args []string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var stderr audioErrorTail
	cmd := exec.CommandContext(ctx, player, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("speaker test failed: %w: %s", err, stderr.String())
	}
	// ffplay sometimes returns success after failing to open an audio device.
	// At our explicit error log level, diagnostic output still means failure.
	if diagnostic := stderr.String(); diagnostic != "" {
		return fmt.Errorf("speaker test failed: %s", diagnostic)
	}
	return nil
}
