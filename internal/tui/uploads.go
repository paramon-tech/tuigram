package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/paramon-tech/tuigram/internal/core"
)

type uploadResultMsg struct {
	chatID string
	err    error
}

func (m *Model) openAttachment() {
	chat, ok := m.currentChat()
	if !ok {
		return
	}
	if _, ok := m.client.(core.AttachmentClient); !ok {
		m.setFailure("upload", "Attachments are unavailable")
		return
	}
	m.attachmentAsFile = false
	m.form = actionForm{kind: "attachment", title: "Attach to " + chat.Title, labels: []string{"File path (photo, video, or document)", "Caption (optional)"}, values: []string{"", ""}, chat: chat}
	m.mode, m.failure = manageForm, ""
}

func (m *Model) attachmentFormKey(key tea.KeyMsg) bool {
	if m.form.kind != "attachment" {
		return false
	}
	switch key.String() {
	case "ctrl+n":
		if len(m.form.values)-1 >= core.MaxAttachmentCount {
			m.setFailure("input", "An album supports at most 10 attachments")
			return true
		}
		if _, ok := m.client.(core.BatchAttachmentClient); !ok {
			m.setFailure("upload", "Multiple attachments are unavailable")
			return true
		}
		last := len(m.form.values) - 1
		values := append([]string(nil), m.form.values[:last]...)
		m.form.values = append(values, "", m.form.values[last])
		m.form.index = last
	case "ctrl+d":
		last := len(m.form.values) - 1
		if last > 1 && m.form.index < last {
			values := append([]string(nil), m.form.values[:m.form.index]...)
			m.form.values = append(values, m.form.values[m.form.index+1:]...)
			m.form.index = min(m.form.index, len(m.form.values)-2)
		}
	case "ctrl+f":
		m.attachmentAsFile = !m.attachmentAsFile
	default:
		return false
	}
	m.form.labels = nil
	for i := 0; i < len(m.form.values)-1; i++ {
		m.form.labels = append(m.form.labels, fmt.Sprintf("File %d path", i+1))
	}
	m.form.labels = append(m.form.labels, "Caption (optional; first attachment)")
	mode := "automatic media"
	if m.attachmentAsFile {
		mode = "send as files"
	}
	m.form.title = fmt.Sprintf("Attach to %s · %s · %d/10", m.form.chat.Title, mode, len(m.form.values)-1)
	m.failure = ""
	return true
}

func (m *Model) sendAttachment() tea.Cmd {
	last := len(m.form.values) - 1
	attachments := make([]core.Attachment, last)
	for i, path := range m.form.values[:last] {
		path = strings.TrimSpace(path)
		// Strip a matching quote pair, never evaluate shell syntax.
		if len(path) > 1 && ((path[0] == '"' && path[len(path)-1] == '"') || (path[0] == '\'' && path[len(path)-1] == '\'')) {
			path = path[1 : len(path)-1]
		}
		if path == "" {
			m.form.index = i
			m.setFailure("input", "Enter the file path")
			return nil
		}
		attachments[i] = core.Attachment{Path: path}
		if m.attachmentAsFile {
			attachments[i].Kind = "document"
		}
	}
	attachments[0].Caption = m.form.values[last]
	client, ok := m.client.(core.AttachmentClient)
	if !ok {
		m.setFailure("upload", "Attachments are unavailable")
		return nil
	}
	batch, hasBatch := m.client.(core.BatchAttachmentClient)
	if len(attachments) > 1 && !hasBatch {
		m.setFailure("upload", "Multiple attachments are unavailable")
		return nil
	}
	chat := m.form.chat
	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Minute)
	m.uploadCancel = cancel
	m.busy = true
	m.failure = ""
	m.mediaStatus = ""
	m.status = "Uploading attachment… Esc cancels"
	return func() tea.Msg {
		defer cancel()
		var err error
		if len(attachments) > 1 {
			err = batch.SendAttachments(ctx, chat, attachments)
		} else {
			err = client.SendAttachment(ctx, chat, attachments[0])
		}
		return uploadResultMsg{chatID: chat.ID, err: err}
	}
}

func (m Model) updateUpload(result uploadResultMsg) (tea.Model, tea.Cmd) {
	m.busy = false
	m.uploadCancel = nil
	if result.err != nil {
		message := result.err.Error()
		if errors.Is(result.err, context.Canceled) || errors.Is(result.err, context.DeadlineExceeded) {
			message = "Upload stopped. Check the chat before retrying if it was already sending."
		}
		m.setFailure("upload", message)
		m.status = "Attachment kept in the form"
		return m, nil
	}
	m.failure = ""
	m.status = "Attachment sent"
	if m.mode == callPanel && m.callReturnMode == manageForm {
		m.callReturnMode = normal
	} else if m.mode == manageForm {
		m.mode = normal
	}
	m.form = actionForm{}
	if chat, ok := m.currentChat(); ok && chat.ID == result.chatID {
		m.query = ""
		m.history.loaded = false
		cmd := m.jumpHistory(false)
		return m, cmd
	}
	return m, nil
}
