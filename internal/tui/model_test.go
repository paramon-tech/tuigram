package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/paramon-tech/tuigram/internal/core"
)

type fakeClient struct {
	chats     []core.Chat
	histories map[string][]core.Message
	contacts  []core.Contact
	image     []byte
	calls     []string
	fail      error
}

func (f *fakeClient) Dialogs(ctx context.Context) ([]core.Chat, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return f.chats, f.fail
}
func (f *fakeClient) History(_ context.Context, c core.Chat, q string) ([]core.Message, error) {
	f.calls = append(f.calls, "history:"+c.ID+":"+q)
	return f.histories[c.ID], f.fail
}
func (f *fakeClient) Send(_ context.Context, c core.Chat, s string) error {
	f.calls = append(f.calls, "send:"+c.ID+":"+s)
	return f.fail
}
func (f *fakeClient) Forward(_ context.Context, c core.Chat, id int, to core.Chat) error {
	f.calls = append(f.calls, fmt.Sprintf("forward:%s:%d:%s", c.ID, id, to.ID))
	return f.fail
}
func (f *fakeClient) React(_ context.Context, c core.Chat, id int, emoji string) error {
	f.calls = append(f.calls, fmt.Sprintf("react:%s:%d:%s", c.ID, id, emoji))
	return f.fail
}
func (f *fakeClient) SearchContacts(_ context.Context, q string) ([]core.Contact, error) {
	f.calls = append(f.calls, "contacts:"+q)
	return f.contacts, f.fail
}
func (f *fakeClient) AddContact(_ context.Context, c core.Contact) error {
	f.calls = append(f.calls, "add:"+c.ID)
	return f.fail
}
func (f *fakeClient) DownloadImage(_ context.Context, c core.Chat, id int) ([]byte, error) {
	f.calls = append(f.calls, fmt.Sprintf("image:%s:%d", c.ID, id))
	return f.image, f.fail
}

func fixture() (Model, *fakeClient) {
	f := &fakeClient{chats: []core.Chat{{ID: "one", Title: "First chat", Kind: "private"}, {ID: "two", Title: "Developers", Kind: "group"}}, histories: map[string][]core.Message{
		"one": {{ID: 1, ChatID: "one", Sender: "Alice", Text: "Hello 👋 https://example.com", Time: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}, {ID: 2, ChatID: "one", Sender: "Bob", Text: "A second message", Reactions: []core.Reaction{{Emoji: "👍", Count: 3}}}},
		"two": {{ID: 3, ChatID: "two", Sender: "Charlie", Text: "Go team"}},
	}, contacts: []core.Contact{{ID: "three", Name: "Dana", Username: "dana"}}}
	m := New(context.Background(), f, Options{PollInterval: -1})
	next, cmd := m.Update(dialogsMsg{request: 1, chats: f.chats})
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	f.calls = nil
	return m, f
}

func press(m Model, key string) (Model, tea.Cmd) {
	var msg tea.KeyMsg
	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "ctrl+s":
		msg = tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+c":
		msg = tea.KeyMsg{Type: tea.KeyCtrlC}
	case "backspace":
		msg = tea.KeyMsg{Type: tea.KeyBackspace}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func complete(m Model, cmd tea.Cmd) Model { next, _ := m.Update(cmd()); return next.(Model) }

func TestChatNavigationRejectsStaleHistory(t *testing.T) {
	m, f := fixture()
	oldRequest := m.historyRequest
	m, cmd := press(m, "j")
	if m.chatIndex != 1 || !m.loading || len(m.messages) != 0 || cmd == nil {
		t.Fatalf("chat switch did not schedule clean state: %#v", m)
	}
	next, _ := m.Update(historyMsg{request: oldRequest, chatID: "one", messages: f.histories["one"]})
	m = next.(Model)
	if len(m.messages) != 0 {
		t.Fatal("stale history crossed into another chat")
	}
	m = complete(m, cmd)
	if len(m.messages) != 1 || m.messages[0].ID != 3 || m.loading {
		t.Fatalf("new history not applied: %#v", m.messages)
	}
	m, _ = press(m, "g")
	m, _ = press(m, "g")
	if m.chatIndex != 0 {
		t.Fatal("gg did not select first chat")
	}
	m, _ = press(m, "G")
	if m.chatIndex != 1 {
		t.Fatal("G did not select final chat")
	}
}

func TestComposeDefersNetworkPreservesFailureAndClearsSuccess(t *testing.T) {
	m, f := fixture()
	m, _ = press(m, "i")
	m, _ = press(m, "Hello 👋")
	m, _ = press(m, "esc")
	m, _ = press(m, "/")
	m, _ = press(m, "query")
	m, _ = press(m, "esc")
	m, _ = press(m, "i")
	if m.input != "Hello 👋" {
		t.Fatalf("draft lost across search: %q", m.input)
	}
	m, cmd := press(m, "ctrl+s")
	if !m.busy || cmd == nil || len(f.calls) != 0 {
		t.Fatal("sending must be deferred and mark pending")
	}
	m, duplicate := press(m, "ctrl+s")
	if duplicate != nil {
		t.Fatal("duplicate send while pending")
	}
	f.fail = errors.New("offline")
	m = complete(m, cmd)
	if m.busy || m.mode != compose || m.input != "Hello 👋" || m.failure != "offline" {
		t.Fatal("failed send did not preserve retryable draft")
	}
	f.fail = nil
	m, cmd = press(m, "ctrl+s")
	m = complete(m, cmd)
	if m.busy || m.mode != normal || m.input != "" || len(m.drafts) != 0 {
		t.Fatal("successful send did not clear draft")
	}
	if got := f.calls[len(f.calls)-1]; got != "send:one:Hello 👋" {
		t.Fatalf("wrong send: %q", got)
	}
}

func TestContactSearchAddAndOpenPrivateChat(t *testing.T) {
	m, f := fixture()
	m, _ = press(m, "c")
	m, _ = press(m, "@dana")
	m, cmd := press(m, "enter")
	if m.mode != contactPicker || cmd == nil || len(f.calls) != 0 {
		t.Fatal("contact search must run asynchronously")
	}
	m = complete(m, cmd)
	if len(m.contacts) != 1 {
		t.Fatal("search results missing")
	}
	m, cmd = press(m, "a")
	m = complete(m, cmd)
	if f.calls[len(f.calls)-1] != "add:three" {
		t.Fatal("contact not added")
	}
	m, cmd = press(m, "enter")
	chat, ok := m.currentChat()
	if !ok || chat.ID != "three" || chat.Kind != "private" || m.mode != normal || cmd == nil {
		t.Fatalf("contact did not open private chat: %#v", chat)
	}
	m = complete(m, cmd)
	m, _ = press(m, "i")
	if m.mode != compose || m.input != "" {
		t.Fatal("new private chat cannot compose a clean message")
	}
}

func TestForwardAndReactionUseSelectedMessage(t *testing.T) {
	m, f := fixture()
	m, _ = press(m, "f")
	m, _ = press(m, "j")
	m, cmd := press(m, "enter")
	if m.mode != normal || !m.busy || len(f.calls) != 0 {
		t.Fatal("forward not deferred")
	}
	m = complete(m, cmd)
	if f.calls[len(f.calls)-1] != "forward:one:2:two" {
		t.Fatalf("wrong forwarding route: %v", f.calls)
	}
	m, cmd = press(m, "r")
	if m.mode != reactionPicker || cmd != nil {
		t.Fatal("reaction picker did not open")
	}
	m, cmd = press(m, "enter")
	m = complete(m, cmd)
	if f.calls[len(f.calls)-1] != "react:one:2:👍" {
		t.Fatalf("wrong reaction: %v", f.calls)
	}
}

func TestSearchIsChatScopedAndEscapeClears(t *testing.T) {
	m, f := fixture()
	m, _ = press(m, "/")
	m, _ = press(m, "release")
	m, cmd := press(m, "enter")
	m = complete(m, cmd)
	if m.query != "release" || f.calls[len(f.calls)-1] != "history:one:release" {
		t.Fatal("message search query not sent")
	}
	m, cmd = press(m, "esc")
	m = complete(m, cmd)
	if m.query != "" || f.calls[len(f.calls)-1] != "history:one:" {
		t.Fatal("escape did not clear search")
	}
}

func TestCancelledContactSearchCannotReopenPicker(t *testing.T) {
	m, _ := fixture()
	m, _ = press(m, "c")
	m, _ = press(m, "Dana")
	m, cmd := press(m, "enter")
	m, _ = press(m, "esc")
	m = complete(m, cmd)
	if m.mode != normal || len(m.contacts) != 0 {
		t.Fatal("stale contacts result replaced current view")
	}
}

func TestHistoryRefreshPreservesSelectionAndFollowsTail(t *testing.T) {
	m, f := fixture()
	m.focus = 1
	m, _ = press(m, "k")
	newMessages := append(append([]core.Message{}, f.histories["one"]...), core.Message{ID: 9, Text: "new"})
	next, _ := m.Update(historyMsg{request: m.historyRequest, chatID: "one", messages: newMessages})
	m = next.(Model)
	if m.messages[m.messageIndex].ID != 1 {
		t.Fatal("refresh moved selection away from older message")
	}
	m, _ = press(m, "G")
	newMessages = append(newMessages, core.Message{ID: 10, Text: "newer"})
	next, _ = m.Update(historyMsg{request: m.historyRequest, chatID: "one", messages: newMessages})
	m = next.(Model)
	if m.messages[m.messageIndex].ID != 10 {
		t.Fatal("tail did not follow incoming message")
	}
}

func TestFullMessageReaderScrolls(t *testing.T) {
	m, _ := fixture()
	m.height = 12
	m.width = 50
	m.messages[m.messageIndex].Text = strings.Repeat("A long message on multiple lines.\n", 40)
	m, _ = press(m, "y")
	m, _ = press(m, "j")
	if m.mode != messageReader || m.viewOffset != 1 {
		t.Fatal("full message reader did not scroll")
	}
	m, _ = press(m, "G")
	if m.viewOffset < 20 {
		t.Fatal("end did not reach long message tail")
	}
	m, _ = press(m, "esc")
	if m.mode != normal {
		t.Fatal("reader did not close")
	}
}

func TestViewFitsTerminalsAndSanitizesRemoteText(t *testing.T) {
	m, _ := fixture()
	m.chats[0].Title = "Chat\x1b]52;c;clipboard-payload\a\u202eevil"
	m.messages[1].Text = "Emoji 👋 URL https://example.com\n\x1b[2Jkeep"
	for _, width := range []int{20, 24, 40, 71, 72, 80, 120} {
		for _, height := range []int{7, 8, 15, 24} {
			m.width, m.height = width, height
			for _, md := range []mode{normal, compose, help, contactPicker, messageReader} {
				m.mode = md
				view := m.View()
				if lipgloss.Width(view) > width || lipgloss.Height(view) > height {
					t.Fatalf("view overflow at %dx%d mode %d: %dx%d\n%s", width, height, md, lipgloss.Width(view), lipgloss.Height(view), view)
				}
				if strings.Contains(view, "clipboard-payload") || strings.Contains(view, "\u202e") || strings.Contains(view, "\x1b[2J") {
					t.Fatal("remote terminal command leaked")
				}
			}
		}
	}
}

func TestSnapshotAndCancellation(t *testing.T) {
	_, f := fixture()
	view, err := Snapshot(context.Background(), f, Options{}, 100, 25)
	if err != nil || !strings.Contains(view, "TUIGRAM") || !strings.Contains(view, "First chat") || !strings.Contains(view, "👍") {
		t.Fatalf("bad snapshot: %v\n%s", err, view)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Snapshot(ctx, f, Options{}, 80, 24); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not propagated: %v", err)
	}
}

func TestThemeCycleAndQuit(t *testing.T) {
	m, _ := fixture()
	for _, theme := range []string{"light", "dracula", "midnight"} {
		m, _ = press(m, "t")
		if m.opts.Theme != theme {
			t.Fatal("theme cycle failed")
		}
	}
	_, cmd := press(m, "ctrl+c")
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("quit command missing")
	}
}

func TestSuccessfulPollingClearsRecoveredNetworkErrors(t *testing.T) {
	m, f := fixture()
	next, _ := m.Update(historyMsg{request: m.historyRequest, chatID: "one", err: errors.New("temporarily offline")})
	m = next.(Model)
	if m.failure == "" {
		t.Fatal("history failure was not shown")
	}
	next, _ = m.Update(historyMsg{request: m.historyRequest, chatID: "one", messages: f.histories["one"]})
	m = next.(Model)
	if m.failure != "" {
		t.Fatal("successful history refresh left an obsolete error")
	}
	next, _ = m.Update(dialogsMsg{request: m.dialogsRequest, err: errors.New("connection interrupted")})
	m = next.(Model)
	next, command := m.Update(dialogsMsg{request: m.dialogsRequest, chats: f.chats})
	m = complete(next.(Model), command)
	if m.failure != "" {
		t.Fatal("successful dialogs refresh left an obsolete error")
	}

	// A failed write still needs attention after an unrelated successful read.
	next, _ = m.Update(operationMsg{operation: "Sending message", chatID: "one", err: errors.New("posting is restricted")})
	m = next.(Model)
	next, _ = m.Update(historyMsg{request: m.historyRequest, chatID: "one", messages: f.histories["one"]})
	m = next.(Model)
	if m.failure != "posting is restricted" {
		t.Fatal("history refresh hid an unresolved write failure")
	}
}

func TestSlowDialogPollingDoesNotOverlapRequests(t *testing.T) {
	m, _ := fixture()
	before := m.dialogsRequest
	next, command := m.Update(tickMsg(time.Now()))
	m = next.(Model)
	if command == nil || !m.dialogsPending || m.dialogsRequest != before+1 {
		t.Fatal("first poll did not start a request")
	}
	next, command = m.Update(tickMsg(time.Now()))
	m = next.(Model)
	if command != nil || m.dialogsRequest != before+1 {
		t.Fatal("timer overlapped a slow request")
	}
}
