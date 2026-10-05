// Package platform integrates optional desktop tools without invoking a shell.
package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type audioPlayer struct {
	path string
	args []string
}

func findAudioPlayer(lookPath func(string) (string, error)) (audioPlayer, error) {
	for _, candidate := range []struct {
		name string
		args []string
	}{
		{"ffplay", []string{"-nodisp", "-autoexit", "-loglevel", "error", "-nostats", "-protocol_whitelist", "file,pipe", "-i"}},
		{"mpv", []string{"--no-config", "--load-scripts=no", "--ytdl=no", "--no-terminal", "--no-video", "--really-quiet", "--"}},
		{"play", []string{"-q", "--"}},
	} {
		if path, err := lookPath(candidate.name); err == nil {
			return audioPlayer{path: path, args: candidate.args}, nil
		}
	}
	return audioPlayer{}, errors.New("voice playback needs ffplay (FFmpeg), mpv, or play (SoX) installed on PATH")
}

// CheckAudioPlayer reports whether a supported audio player is installed. Call
// it before downloading a voice message to avoid an unnecessary download.
func CheckAudioPlayer() error {
	_, err := findAudioPlayer(exec.LookPath)
	return err
}

// PlayAudio plays a local media file and waits until playback completes. The
// caller should run it outside the UI update loop and cancel ctx to stop audio.
// Stdin and output are disconnected so the player cannot consume TUI keys or
// write into its terminal. The media path is always passed as a single argument.
func PlayAudio(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	player, err := findAudioPlayer(exec.LookPath)
	if err != nil {
		return err
	}
	return player.play(ctx, path)
}

func (p audioPlayer) play(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("locate voice message: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("read voice message: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("voice message must be a non-empty local file")
	}
	args := append(append([]string(nil), p.args...), abs)
	if err := exec.CommandContext(ctx, p.path, args...).Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("voice playback failed with %s (check its audio device and codec support): %w", filepath.Base(p.path), err)
	}
	return nil
}
