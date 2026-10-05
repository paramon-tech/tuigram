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

type callStateMsg struct{ state core.CallState }
type callOperationMsg struct {
	request uint64
	state   core.CallState
	err     error
}

func (m Model) callTickCmd() tea.Cmd {
	client, ok := m.client.(core.CallClient)
	if !ok || m.opts.PollInterval < 0 || m.ctx.Err() != nil {
		return nil
	}
	return tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return callStateMsg{state: client.CallState()} })
}

func (m *Model) applyCallState(state core.CallState) {
	if state.ID < m.call.ID {
		return
	}
	// A completed operation result may arrive after a newer poll. A terminal
	// state for this call cannot regress to ringing/connected.
	if state.ID == m.call.ID && (m.call.Status == "ended" || m.call.Status == "failed") && state.Active() {
		return
	}
	m.call = state
}

func (m *Model) openCallPanel() {
	if _, ok := m.client.(core.CallClient); !ok {
		m.setFailure("call", "Native calls are unavailable with this backend")
		return
	}
	if m.mode == callPanel {
		return
	}
	m.callReturnMode = m.mode
	if !m.callPending {
		m.callTarget, _ = m.currentChat()
	}
	m.mode = callPanel
	m.clearFailure("call")
}

func (m Model) updateCallKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc", "q", "C":
		m.mode = m.callReturnMode
		cmd := m.markVisibleRead()
		return m, cmd
	case "enter", "a":
		action := "start"
		if m.call.Active() {
			if m.call.Status != "ringing" || !m.call.Incoming {
				return m, nil
			}
			action = "answer"
		}
		cmd := m.callCommand(action)
		return m, cmd
	case "x":
		cmd := m.callCommand("end")
		return m, cmd
	case "m":
		cmd := m.callCommand("mute")
		return m, cmd
	}
	return m, nil
}

func (m *Model) callCommand(action string) tea.Cmd {
	client, ok := m.client.(core.CallClient)
	if !ok {
		m.setFailure("call", "Native calls are unavailable with this backend")
		return nil
	}
	pendingStart := false
	if m.callPending {
		if action != "end" || m.callPendingAction == "end" {
			return nil
		}
		pendingStart = m.callPendingAction == "start"
		if m.callCancel != nil {
			m.callCancel()
		}
	}
	target := m.callTarget
	if action == "start" {
		if target.ID == "" || target.Kind != "private" {
			m.setFailure("call", "Select a person's private chat to call")
			return nil
		}
		if m.call.Active() {
			return nil
		}
		m.stopMedia()
	} else if !m.call.Active() && !pendingStart {
		return nil
	}
	if action == "answer" {
		m.stopMedia()
	}
	id, muted := m.call.ID, !m.call.Muted
	m.callRequest++
	request := m.callRequest
	m.callPending = true
	m.callPendingAction = action
	m.clearFailure("call")
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	m.callCancel = cancel
	return func() tea.Msg {
		defer cancel()
		var err error
		switch action {
		case "start":
			err = client.StartCall(ctx, target)
		case "answer":
			err = client.AnswerCall(ctx, id)
		case "end":
			if pendingStart {
				// Start may have created a session just before cancellation. Only
				// end that outgoing call, never a newly arrived incoming caller.
				state := client.CallState()
				if state.Active() && !state.Incoming && state.Chat.ID == target.ID {
					err = client.EndCall(ctx, state.ID)
				}
			} else {
				err = client.EndCall(ctx, id)
			}
		case "mute":
			err = client.SetCallMuted(id, muted)
		default:
			err = errors.New("unknown call action")
		}
		return callOperationMsg{request: request, state: client.CallState(), err: err}
	}
}

func (m Model) updateCallOperation(result callOperationMsg) (tea.Model, tea.Cmd) {
	if result.request != m.callRequest {
		return m, nil
	}
	m.callPending = false
	m.callCancel = nil
	m.callPendingAction = ""
	m.applyCallState(result.state)
	if result.err != nil {
		m.setFailure("call", result.err.Error())
	} else {
		m.clearFailure("call")
	}
	return m, nil
}

func (m Model) callBanner() string {
	title := singleLine(m.call.Chat.Title)
	if m.call.Status == "ringing" && m.call.Incoming {
		return "Incoming call: " + title + " · ctrl+g controls"
	}
	status := m.call.Status
	if m.call.Muted {
		status += " · mic muted"
	}
	return "Call " + status + ": " + title + " · ctrl+g controls · ctrl+x end"
}

func (m Model) callText() string {
	chat := m.callTarget
	if m.call.Active() {
		chat = m.call.Chat
	}
	status := m.call.Status
	if !m.call.Active() && chat.ID != m.call.Chat.ID {
		status = "idle"
	}
	if status == "" || status == "idle" {
		status = "Ready"
	}
	lines := []string{"Person: " + singleLine(chat.Title), "Status: " + status}
	if m.call.Active() {
		microphone := "on when connected"
		if m.call.Muted {
			microphone = "muted"
		}
		if m.call.Incoming && m.call.Status == "ringing" {
			microphone = "off — answer to enable"
		}
		lines = append(lines, "Microphone: "+microphone)
		if !m.call.StartedAt.IsZero() {
			seconds := max(0, int(time.Since(m.call.StartedAt).Seconds()))
			lines = append(lines, fmt.Sprintf("Duration: %02d:%02d", seconds/60, seconds%60))
		}
	}
	if m.call.Error != "" && chat.ID == m.call.Chat.ID {
		lines = append(lines, "", Sanitize(m.call.Error))
	}
	lines = append(lines, "")
	if m.callPending {
		lines = append(lines, "Updating call…")
	} else {
		switch {
		case m.call.Incoming && m.call.Status == "ringing":
			lines = append(lines, "Enter / a answer · x decline")
		case m.call.Active():
			lines = append(lines, "m mute / unmute · x hang up")
		default:
			lines = append(lines, "Enter starts a native voice call using your microphone.", "Headphones are recommended to avoid echo.")
		}
	}
	lines = append(lines, "", "Esc returns to chat; an active call continues.", "Ctrl+g opens these controls · Ctrl+x ends the call.")
	return strings.Join(lines, "\n")
}

// ShutdownCalls stops the microphone and sends hang-up before transport teardown.
func (m Model) ShutdownCalls() {
	if m.callCancel != nil {
		m.callCancel()
	}
	if client, ok := m.client.(core.CallClient); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.EndCall(ctx, 0)
	}
}
