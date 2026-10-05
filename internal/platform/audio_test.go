package platform

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAudioPlayerMissingAndFallback(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := CheckAudioPlayer(); err == nil || !strings.Contains(err.Error(), "ffplay") {
		t.Fatalf("missing player must give installation guidance: %v", err)
	}
	p, err := findAudioPlayer(func(name string) (string, error) {
		if name == "mpv" {
			return "/usr/bin/mpv", nil
		}
		return "", os.ErrNotExist
	})
	if err != nil || p.path != "/usr/bin/mpv" {
		t.Fatalf("did not find fallback player: %+v, %v", p, err)
	}
}

func TestAudioPathIsOneArgumentAndInputDisconnected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "-voice ; $(touch injected) with spaces.ogg")
	if err := os.WriteFile(path, []byte("voice"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TUIGRAM_TEST_AUDIO_HELPER", "check")
	t.Setenv("TUIGRAM_TEST_AUDIO_PATH", path)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := audioPlayer{path: executable, args: []string{"-test.run=^TestAudioHelperProcess$", "--"}}
	if err := p.play(context.Background(), path); err != nil {
		t.Fatal(err)
	}
}

func TestCancelAudioStopsProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "voice.ogg")
	if err := os.WriteFile(path, []byte("voice"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TUIGRAM_TEST_AUDIO_HELPER", "wait")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := audioPlayer{path: executable, args: []string{"-test.run=^TestAudioHelperProcess$", "--"}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := p.play(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("playback did not stop on cancellation: %v", err)
	}
}

func TestAudioRejectsDirectoriesAndEmptyFiles(t *testing.T) {
	p := audioPlayer{path: "must-not-run"}
	dir := t.TempDir()
	if err := p.play(context.Background(), dir); err == nil {
		t.Fatal("accepted directory")
	}
	path := filepath.Join(dir, "empty.ogg")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := p.play(context.Background(), path); err == nil {
		t.Fatal("accepted empty voice message")
	}
}

func TestAudioHelperProcess(t *testing.T) {
	switch os.Getenv("TUIGRAM_TEST_AUDIO_HELPER") {
	case "wait":
		time.Sleep(time.Minute)
		os.Exit(1)
	case "check":
		args := os.Args
		if len(args) != 4 || args[2] != "--" || args[3] != os.Getenv("TUIGRAM_TEST_AUDIO_PATH") {
			os.Exit(2)
		}
		var buf [1]byte
		if n, err := os.Stdin.Read(buf[:]); n != 0 || err != io.EOF {
			os.Exit(3)
		}
		os.Exit(0)
	}
}
