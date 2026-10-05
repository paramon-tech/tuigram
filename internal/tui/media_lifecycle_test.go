package tui

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestShutdownWaitsForActiveAndPreviouslyCancelledPlaybackCleanup(t *testing.T) {
	for _, stopFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "already stopped"}[stopFirst], func(t *testing.T) {
			m, base := fixture()
			m.client = &mediaFake{fakeClient: base, data: []byte("audio"), name: "voice.ogg"}
			m.messages[m.messageIndex].Voice = true
			m.messages[m.messageIndex].Downloadable = true
			started := make(chan string, 1)
			cancelled := make(chan struct{})
			allowFinish := make(chan struct{})
			release := sync.OnceFunc(func() { close(allowFinish) })
			defer release()
			m.opts.PlayAudio = func(ctx context.Context, path string) error {
				started <- path
				<-ctx.Done()
				close(cancelled)
				<-allowFinish
				return ctx.Err()
			}
			m, cmd := press(m, "p")
			if cmd == nil {
				t.Fatal("playback did not start")
			}
			defer m.stopMedia()
			commandDone := make(chan tea.Msg, 1)
			go func() { commandDone <- cmd() }()
			var path string
			select {
			case path = <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("playback did not start")
			}
			if stopFirst {
				m, _ = press(m, "esc")
			}
			shutdownDone := make(chan struct{})
			go func() { m.ShutdownMedia(); close(shutdownDone) }()
			select {
			case <-cancelled:
			case <-time.After(2 * time.Second):
				t.Fatal("shutdown did not cancel audio player")
			}
			select {
			case <-shutdownDone:
				t.Fatal("shutdown returned before audio worker cleanup")
			default:
			}
			release()
			select {
			case <-shutdownDone:
			case <-time.After(2 * time.Second):
				t.Fatal("shutdown did not return after cleanup")
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("shutdown retained private audio file: %v", err)
			}
		})
	}
}
