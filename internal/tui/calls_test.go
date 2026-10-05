package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/paramon-tech/tuigram/internal/core"
)

type nativeCallFake struct {
	*fakeClient
	state           core.CallState
	started         []core.Chat
	answered, ended []uint64
	muted           []bool
	callErr         error
}

func (f *nativeCallFake) CallState() core.CallState { return f.state }
func (f *nativeCallFake) StartCall(_ context.Context, chat core.Chat) error {
	if f.callErr != nil {
		return f.callErr
	}
	f.started = append(f.started, chat)
	f.state = core.CallState{ID: f.state.ID + 1, Chat: chat, Status: "connecting"}
	return nil
}
func (f *nativeCallFake) AnswerCall(_ context.Context, id uint64) error {
	if id != f.state.ID {
		return errors.New("call changed")
	}
	f.answered = append(f.answered, id)
	f.state.Status = "active"
	return f.callErr
}
func (f *nativeCallFake) EndCall(_ context.Context, id uint64) error {
	if id != 0 && id != f.state.ID {
		return errors.New("call changed")
	}
	f.ended = append(f.ended, id)
	f.state.Status = "ended"
	return f.callErr
}
func (f *nativeCallFake) SetCallMuted(id uint64, muted bool) error {
	if id != f.state.ID {
		return errors.New("call changed")
	}
	f.muted = append(f.muted, muted)
	f.state.Muted = muted
	return f.callErr
}
func callFixture() (Model, *nativeCallFake) {
	m, base := fixture()
	f := &nativeCallFake{fakeClient: base, state: core.CallState{Status: "idle"}}
	m.client = f
	return m, f
}
func callKey(m Model, key tea.KeyType) (Model, tea.Cmd) {
	next, cmd := m.Update(tea.KeyMsg{Type: key})
	return next.(Model), cmd
}

func TestNativeCallStartsSelectedPersonOnlyOnExplicitAction(t *testing.T) {
	m, f := callFixture()
	m, cmd := press(m, "C")
	if cmd != nil || m.mode != callPanel || len(f.started) != 0 {
		t.Fatal("opening call controls placed a call")
	}
	// Selection can change underneath a modal after a dialog refresh.
	m.chatIndex = 1
	m, cmd = press(m, "enter")
	if cmd == nil {
		t.Fatal("call was not scheduled")
	}
	m, duplicate := press(m, "enter")
	if duplicate != nil {
		t.Fatal("duplicate call scheduled")
	}
	m = complete(m, cmd)
	if len(f.started) != 1 || f.started[0].ID != "one" || m.call.Status != "connecting" {
		t.Fatalf("wrong native call: %#v %s", f.started, m.failure)
	}
	m, _ = press(m, "esc")
	if m.mode != normal || len(f.ended) != 0 {
		t.Fatal("returning to chat ended call")
	}
}

func TestIncomingCallControlsPreserveComposerAndTargetCallID(t *testing.T) {
	m, f := callFixture()
	m, _ = press(m, "i")
	m, _ = press(m, "draft with spaces")
	f.state = core.CallState{ID: 17, Chat: core.Chat{ID: "caller", Title: "Caller", Kind: "private"}, Status: "ringing", Incoming: true}
	next, _ := m.Update(callStateMsg{state: f.state})
	m = next.(Model)
	if m.mode != compose || !strings.Contains(m.View(), "Incoming call") {
		t.Fatal("incoming call disrupted composer or was invisible")
	}
	m, _ = callKey(m, tea.KeyCtrlG)
	m, cmd := press(m, "a")
	m = complete(m, cmd)
	if len(f.answered) != 1 || f.answered[0] != 17 || m.call.Status != "active" {
		t.Fatal("wrong call answered")
	}
	m, cmd = press(m, "m")
	m = complete(m, cmd)
	if !m.call.Muted || len(f.muted) != 1 {
		t.Fatal("microphone not muted")
	}
	m, _ = press(m, "esc")
	if m.mode != compose || m.input != "draft with spaces" {
		t.Fatal("call controls lost composer draft")
	}
	m, cmd = callKey(m, tea.KeyCtrlX)
	m = complete(m, cmd)
	if len(f.ended) != 1 || f.ended[0] != 17 || m.mode != compose {
		t.Fatal("global hangup failed or changed compose mode")
	}
}

func TestCallFailureAndStaleIncomingAction(t *testing.T) {
	m, f := callFixture()
	f.callErr = errors.New("microphone permission denied")
	m, _ = press(m, "C")
	m, cmd := press(m, "enter")
	m = complete(m, cmd)
	if m.failure != "microphone permission denied" || m.callPending {
		t.Fatal("call failure not visible")
	}
	f.callErr = nil
	f.state = core.CallState{ID: 12, Status: "ringing", Incoming: true}
	m.call = f.state
	m, cmd = press(m, "a")
	f.state = core.CallState{ID: 13, Status: "ringing", Incoming: true}
	m = complete(m, cmd)
	if len(f.answered) != 0 || m.failure != "call changed" {
		t.Fatal("stale action answered different caller")
	}
}

func TestEndedCallDoesNotRegressFromStalePoll(t *testing.T) {
	m, _ := callFixture()
	m.call = core.CallState{ID: 5, Status: "ended"}
	next, _ := m.Update(callStateMsg{state: core.CallState{ID: 5, Status: "active"}})
	m = next.(Model)
	if m.call.Status != "ended" {
		t.Fatal("stale active poll resurrected call")
	}
	next, _ = m.Update(callStateMsg{state: core.CallState{ID: 6, Status: "ringing", Incoming: true}})
	m = next.(Model)
	if m.call.ID != 6 || m.call.Status != "ringing" {
		t.Fatal("new incoming call was ignored")
	}
}

func TestShutdownEndsNativeCall(t *testing.T) {
	m, f := callFixture()
	f.state = core.CallState{ID: 9, Status: "active"}
	m.ShutdownCalls()
	if len(f.ended) != 1 || f.ended[0] != 0 {
		t.Fatal("shutdown left native call running")
	}
}

func TestNewCallPanelShowsNewRecipientAfterPreviousCall(t *testing.T) {
	m, _ := callFixture()
	m.call = core.CallState{ID: 1, Status: "ended", Chat: core.Chat{ID: "past", Title: "Previous caller", Kind: "private"}}
	m, _ = press(m, "C")
	if !strings.Contains(m.callText(), "First chat") || strings.Contains(m.callText(), "Previous caller") {
		t.Fatal("call panel displayed a different recipient than the next call target")
	}
}

func TestCallOverlayPreservesBackgroundResults(t *testing.T) {
	t.Run("send", func(t *testing.T) {
		m, _ := callFixture()
		m.mode = compose
		m.input = "sending draft"
		m.busy = true
		m.openCallPanel()
		next, _ := m.Update(operationMsg{operation: "Sending message", chatID: "one"})
		m = next.(Model)
		if m.mode != callPanel || m.callReturnMode != normal || m.input != "" {
			t.Fatal("send result closed call panel or kept sent draft")
		}
	})
	t.Run("contacts", func(t *testing.T) {
		m, _ := callFixture()
		m.mode = contactPicker
		m.contactsRequest = 5
		m.loading = true
		m.openCallPanel()
		next, _ := m.Update(contactsMsg{request: 5, contacts: []core.Contact{{ID: "x", Name: "Visible"}}})
		m = next.(Model)
		if m.mode != callPanel || m.callReturnMode != contactPicker || m.loading || len(m.contacts) != 1 {
			t.Fatal("contacts response was lost beneath call panel")
		}
	})
	t.Run("management", func(t *testing.T) {
		m, _ := callFixture()
		m.mode = manageForm
		m.openCallPanel()
		next, _ := m.Update(managementMsg{kind: "importContact", contact: core.Contact{ID: "x", Name: "Imported"}})
		m = next.(Model)
		if m.mode != callPanel || m.callReturnMode != contactPicker || len(m.contacts) != 1 {
			t.Fatal("management result closed call panel or lost result")
		}
	})
}

type pendingCallFake struct {
	*nativeCallFake
	ctx context.Context
}

func (f *pendingCallFake) StartCall(ctx context.Context, _ core.Chat) error {
	f.ctx = ctx
	return ctx.Err()
}

func TestHangupCancelsPendingStartBeforeItRuns(t *testing.T) {
	m, base := callFixture()
	f := &pendingCallFake{nativeCallFake: base}
	m.client = f
	m, _ = press(m, "C")
	m, start := press(m, "enter")
	m, end := callKey(m, tea.KeyCtrlX)
	if end == nil {
		t.Fatal("hangup ignored while call was pending")
	}
	m = complete(m, end)
	m = complete(m, start)
	if f.ctx == nil || !errors.Is(f.ctx.Err(), context.Canceled) || len(f.started) != 0 || m.callPending || m.failure != "" {
		t.Fatal("cancelled start escaped or stale result replaced hangup")
	}
}

func TestReopeningPendingCallKeepsItsRecipient(t *testing.T) {
	m, _ := callFixture()
	m, _ = press(m, "C")
	m, start := press(m, "enter")
	m, _ = press(m, "esc")
	m.chatIndex = 1
	m, _ = press(m, "C")
	if m.callTarget.ID != "one" {
		t.Fatal("reopening controls changed a pending call's target")
	}
	m, _ = callKey(m, tea.KeyCtrlX)
	// Finish the cancelled queued command to release its timeout.
	_ = start()
	if m.callCancel != nil {
		m.callCancel()
	}
}
