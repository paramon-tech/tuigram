package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/paramon-tech/tuigram/internal/core"
)

func TestPhysicalSpaceKeyInComposerAndSearch(t *testing.T) {
	for _, md := range []mode{compose, search, contactSearch} {
		t.Run(map[mode]string{compose: "compose", search: "search", contactSearch: "contacts"}[md], func(t *testing.T) {
			m, f := fixture()
			m.mode = md
			m, _ = press(m, "hello")
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
			m = next.(Model)
			m, _ = press(m, "world")
			if m.input != "hello world" {
				t.Fatalf("physical space lost: %q", m.input)
			}
			if md == compose {
				if m.drafts["one"] != "hello world" {
					t.Fatal("draft lost space")
				}
				next, cmd := press(m, "ctrl+s")
				complete(next, cmd)
				if f.calls[len(f.calls)-1] != "send:one:hello world" {
					t.Fatalf("wrong send: %v", f.calls)
				}
			}
		})
	}
	// A literal word "space" pasted into the input must remain text.
	m, _ := fixture()
	m.mode = compose
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("space"), Paste: true})
	if next.(Model).input != "space" {
		t.Fatal("paste invoked a space shortcut")
	}
}

type mediaFake struct {
	*fakeClient
	data []byte
	err  error
	name string
}

func (f *mediaFake) DownloadMedia(ctx context.Context, _ core.Chat, _ int, w io.Writer) (core.MediaFile, error) {
	if err := ctx.Err(); err != nil {
		return core.MediaFile{}, err
	}
	if _, err := w.Write(f.data); err != nil {
		return core.MediaFile{}, err
	}
	return core.MediaFile{Name: f.name, Size: int64(len(f.data))}, f.err
}

func TestDownloadPublishesPrivateFileAndRemovesFailures(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f := &mediaFake{data: []byte("media payload"), name: "../../outside.mp4"}
	saved, err := saveMedia(context.Background(), f, core.Chat{}, 1, dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(saved) != dir || !strings.HasSuffix(saved, "-outside.mp4") {
		t.Fatalf("unsafe filename: %q", saved)
	}
	data, err := os.ReadFile(saved)
	if err != nil || string(data) != "media payload" {
		t.Fatalf("bad saved data: %q %v", data, err)
	}
	info, _ := os.Stat(saved)
	if info.Mode().Perm() != 0600 {
		t.Fatal("download exposed to other users")
	}
	f.err = errors.New("network interrupted")
	if _, err := saveMedia(context.Background(), f, core.Chat{}, 1, dir); err == nil {
		t.Fatal("failed download was published")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("failed download left files: %v", entries)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := saveMedia(ctx, f, core.Chat{}, 1, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel ignored: %v", err)
	}
}

func TestDownloadRejectsSymlinkDestination(t *testing.T) {
	dir := t.TempDir()
	target := t.TempDir()
	link := filepath.Join(dir, "downloads")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	f := &mediaFake{data: []byte("media"), name: "voice.ogg"}
	if _, err := saveMedia(context.Background(), f, core.Chat{}, 1, link); err == nil {
		t.Fatal("symlink accepted")
	}
	entries, _ := os.ReadDir(target)
	if len(entries) != 0 {
		t.Fatal("download wrote through symlink")
	}
}

func TestVoicePlaybackCleansTemporaryFile(t *testing.T) {
	m, base := fixture()
	m.client = &mediaFake{fakeClient: base, data: []byte("audio"), name: "voice.ogg"}
	m.messages[m.messageIndex].Voice = true
	m.messages[m.messageIndex].Downloadable = true
	var played string
	m.opts.PlayAudio = func(ctx context.Context, name string) error {
		played = name
		data, err := os.ReadFile(name)
		if err != nil || string(data) != "audio" {
			t.Fatalf("player received bad file: %q %v", data, err)
		}
		return nil
	}
	m, cmd := press(m, "p")
	if cmd == nil {
		t.Fatal("playback did not start")
	}
	m = complete(m, cmd)
	if m.mediaStatus != "Voice playback finished" || played == "" {
		t.Fatalf("playback failed: %s %s", m.mediaStatus, m.failure)
	}
	if _, err := os.Stat(played); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("voice file was retained: %v", err)
	}
}

func TestCancelledMediaResultDoesNotReplaceNewState(t *testing.T) {
	m, base := fixture()
	m.client = &mediaFake{fakeClient: base, data: []byte("media"), name: "photo.jpg"}
	m.opts.DownloadDir = t.TempDir()
	m.messages[m.messageIndex].Downloadable = true
	m, cmd := press(m, "d")
	request := m.mediaRequest
	m, _ = press(m, "esc")
	if m.mediaCancel != nil {
		t.Fatal("escape did not cancel")
	}
	m = complete(m, cmd)
	if m.failure != "" || m.mediaStatus != "" || m.mediaRequest == request {
		t.Fatal("stale cancelled result affected UI")
	}
	entries, _ := os.ReadDir(m.opts.DownloadDir)
	if len(entries) != 0 {
		t.Fatal("cancelled media persisted")
	}
}

func TestCancellingActivePlaybackRemovesTemporaryAudio(t *testing.T) {
	m, base := fixture()
	m.client = &mediaFake{fakeClient: base, data: []byte("audio"), name: "voice.ogg"}
	m.messages[m.messageIndex].Voice = true
	m.messages[m.messageIndex].Downloadable = true
	started := make(chan string, 1)
	m.opts.PlayAudio = func(ctx context.Context, name string) error {
		started <- name
		<-ctx.Done()
		return ctx.Err()
	}
	m, cmd := press(m, "p")
	if cmd == nil {
		t.Fatal("playback did not start")
	}
	defer m.stopMedia()
	finished := make(chan tea.Msg, 1)
	go func() { finished <- cmd() }()
	var path string
	select {
	case path = <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("playback did not receive downloaded audio")
	}
	m, _ = press(m, "esc")
	select {
	case result := <-finished:
		next, _ := m.Update(result)
		m = next.(Model)
	case <-time.After(2 * time.Second):
		t.Fatal("playback did not stop on cancellation")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled playback retained audio: %v", err)
	}
	if m.failure != "" || m.mediaStatus != "" {
		t.Fatal("cancelled playback result replaced current state")
	}
}

func TestManagementViewsFitSmallTerminal(t *testing.T) {
	m, _ := fixture()
	m.width = 50
	m.height = 18
	m.form = actionForm{title: "Contact", labels: []string{"Phone", "Name"}, values: []string{"+123456789", "A Person"}}
	for _, md := range []mode{manageForm, confirmAction} {
		m.mode = md
		m.confirmation.prompt = "Delete " + strings.Repeat("long chat title ", 20)
		view := m.View()
		if !strings.Contains(view, "TUIGRAM") {
			t.Fatal("management view failed to render")
		}
	}
}
