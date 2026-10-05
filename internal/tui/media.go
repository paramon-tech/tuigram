package tui

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/paramon-tech/tuigram/internal/core"
	"github.com/paramon-tech/tuigram/internal/platform"
	"github.com/paramon-tech/tuigram/internal/storage"
)

type mediaResultMsg struct {
	request  uint64
	path     string
	playback bool
	err      error
}

type mediaTask struct {
	cancel context.CancelFunc
	done   <-chan struct{}
}

// ShutdownMedia stops downloads and audio players and lets their cleanup finish
// before the application exits. Keep cancelled tasks until they finish, since a
// user can stop playback and immediately quit. The total wait is bounded in case
// the program exits before Bubble Tea starts a queued command.
func (m Model) ShutdownMedia() {
	for _, task := range m.mediaTasks {
		task.cancel()
	}
	if m.mediaCancel != nil {
		m.mediaCancel()
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for _, task := range m.mediaTasks {
		select {
		case <-task.done:
		case <-deadline.C:
			return
		}
	}
}

func (m *Model) stopMedia() {
	if m.mediaCancel != nil {
		m.mediaCancel()
		m.mediaCancel = nil
		m.mediaRequest++
	}
	m.mediaStatus = ""
}

func (m *Model) startMedia(playback bool) tea.Cmd {
	if m.mediaCancel != nil {
		m.stopMedia()
		m.status = "Media action stopped"
		return nil
	}
	message, ok := m.currentMessage()
	if !ok || (!message.Downloadable && !message.Image) {
		m.setFailure("media", "Select a downloadable image, video, or audio message")
		return nil
	}
	if playback && !message.Voice {
		m.setFailure("media", "Select a voice message to play")
		return nil
	}
	client, ok := m.client.(core.MediaClient)
	if !ok {
		m.setFailure("media", "Media downloads are unavailable")
		return nil
	}
	play := m.opts.PlayAudio
	if playback && play == nil {
		if err := platform.CheckAudioPlayer(); err != nil {
			m.setFailure("media", err.Error())
			return nil
		}
		play = platform.PlayAudio
	}
	chat, _ := m.currentChat()
	m.mediaRequest++
	request := m.mediaRequest
	ctx, cancel := context.WithCancel(m.ctx)
	m.mediaCancel = cancel
	// Prune finished tasks only on the UI goroutine. The workers communicate
	// completion through closed channels, so this needs no shared mutable state.
	active := m.mediaTasks[:0]
	for _, task := range m.mediaTasks {
		select {
		case <-task.done:
		default:
			active = append(active, task)
		}
	}
	done := make(chan struct{})
	m.mediaTasks = append(active, mediaTask{cancel: cancel, done: done})
	m.failure = ""
	m.mediaStatus = "Downloading media… Esc stops"
	if playback {
		m.mediaStatus = "Voice playback — downloading / playing · p or Esc stops"
	}
	downloadDir := m.opts.DownloadDir
	return func() tea.Msg {
		defer close(done)
		defer cancel()
		result := mediaResultMsg{request: request, playback: playback}
		dir := downloadDir
		if playback {
			var err error
			dir, err = os.MkdirTemp("", "tuigram-voice-")
			if err != nil {
				result.err = err
				return result
			}
			defer os.RemoveAll(dir)
			// On macOS TMPDIR can contain a system symlink; use its canonical path.
			dir, err = filepath.EvalSymlinks(dir)
			if err != nil {
				result.err = err
				return result
			}
		} else if dir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				result.err = err
				return result
			}
			dir = filepath.Join(home, "Downloads", "tuigram")
		}
		downloadCtx, downloadCancel := context.WithTimeout(ctx, 5*time.Minute)
		result.path, result.err = saveMedia(downloadCtx, client, chat, message.ID, dir)
		downloadCancel()
		if result.err == nil && playback {
			playCtx, playCancel := context.WithTimeout(ctx, 2*time.Hour)
			result.err = play(playCtx, result.path)
			playCancel()
			result.path = ""
		}
		return result
	}
}

func (m Model) updateMedia(result mediaResultMsg) (tea.Model, tea.Cmd) {
	if result.request != m.mediaRequest {
		return m, nil
	}
	m.mediaCancel = nil
	if result.err != nil {
		m.mediaStatus = ""
		m.setFailure("media", result.err.Error())
	} else if result.playback {
		m.mediaStatus = "Voice playback finished"
	} else {
		m.mediaStatus = "Saved: " + result.path
	}
	return m, nil
}

// saveMedia publishes a completed private file only. Failed or cancelled downloads
// are removed, names supplied by Telegram cannot escape the destination directory.
func saveMedia(ctx context.Context, client core.MediaClient, chat core.Chat, id int, dir string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !filepath.IsAbs(dir) {
		return "", errors.New("download directory must be an absolute path")
	}
	if err := storage.EnsurePrivateDir(dir); err != nil {
		return "", fmt.Errorf("download directory: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	token := rand.Text()
	partial := "." + token + ".part"
	file, err := root.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer root.Remove(partial)
	metadata, downloadErr := client.DownloadMedia(ctx, chat, id, file)
	if downloadErr == nil {
		downloadErr = ctx.Err()
	}
	if downloadErr == nil {
		downloadErr = file.Sync()
	}
	closeErr := file.Close()
	if downloadErr != nil {
		return "", downloadErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name := token + "-" + downloadName(metadata.Name)
	// Link refuses an existing destination, unlike Rename; then remove the partial.
	if err := root.Link(partial, name); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		_ = root.Remove(name)
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func downloadName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._-", r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
		if b.Len() > 120 {
			break
		}
	}
	name = strings.Trim(b.String(), ".")
	if name == "" {
		return "media.bin"
	}
	return name
}
