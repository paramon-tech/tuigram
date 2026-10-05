package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/paramon-tech/tuigram/internal/core"
)

type managementClient struct {
	*fakeClient
	deletedChat string
	group       core.Chat
}

func (c *managementClient) CreateGroup(context.Context, string, []core.Contact) (core.Chat, error) {
	return c.group, c.fail
}
func (c *managementClient) RenameChat(context.Context, core.Chat, string) error { return c.fail }
func (c *managementClient) DeleteChat(_ context.Context, chat core.Chat) error {
	c.deletedChat = chat.ID
	return c.fail
}
func (c *managementClient) EditContact(context.Context, core.Contact, string) error { return c.fail }
func (c *managementClient) DeleteContact(context.Context, core.Contact) error       { return c.fail }

type importOnlyClient struct {
	*fakeClient
	phone, name string
}

func (c *importOnlyClient) ImportContact(_ context.Context, phone, name string) (core.Contact, error) {
	c.phone, c.name = phone, name
	return core.Contact{ID: "new-contact", Name: name}, nil
}

func TestGroupPartialInviteWarningSurvivesSelectionAndHistory(t *testing.T) {
	m, _ := fixture()
	warning := errors.New("group created, but Telegram could not invite 1 selected contact(s)")
	group := core.Chat{ID: "new-group", Title: "Weekend", Kind: "group"}
	next, command := m.Update(managementMsg{kind: "createGroup", chat: group, err: warning})
	m = next.(Model)
	if current, _ := m.currentChat(); current.ID != group.ID || m.failure != warning.Error() {
		t.Fatalf("group or partial invite warning lost: %+v, %q", current, m.failure)
	}
	if m.mode != normal || command == nil {
		t.Fatal("created group did not open its history")
	}
	next, _ = m.Update(command())
	m = next.(Model)
	if m.failure != warning.Error() {
		t.Fatal("successful history response hid partial group creation")
	}
}

func TestConfirmedDeleteCapturesTargetAndRejectsStaleReads(t *testing.T) {
	m, f := fixture()
	manager := &managementClient{fakeClient: f}
	m.client = manager
	m.drafts["one"] = "unsent draft"
	oldDialogs, oldHistory := m.dialogsRequest, m.historyRequest
	m, command := press(m, "D")
	if m.mode != confirmAction || command != nil || manager.deletedChat != "" {
		t.Fatal("delete ran before confirmation")
	}
	// The confirmation owns its target even if the background selection changes.
	m.chatIndex = 1
	m, command = press(m, "y")
	if command == nil || !m.busy {
		t.Fatal("confirmed delete did not start")
	}
	next, _ := m.Update(dialogsMsg{request: oldDialogs, chats: []core.Chat{{ID: "stale"}}})
	m = next.(Model)
	next, _ = m.Update(historyMsg{request: oldHistory, chatID: "two", messages: []core.Message{{ID: 999}}})
	m = next.(Model)
	if len(m.chats) != 2 || m.chats[0].ID != "one" || m.messages[0].ID == 999 {
		t.Fatal("stale read changed state during deletion")
	}
	busy, duplicate := press(m, "y")
	if duplicate != nil || !busy.busy {
		t.Fatal("repeated confirmation started duplicate mutation")
	}
	next, _ = m.Update(command())
	m = next.(Model)
	if manager.deletedChat != "one" || len(m.chats) != 1 || m.chats[0].ID != "two" {
		t.Fatalf("deleted wrong chat: %q, %+v", manager.deletedChat, m.chats)
	}
	if _, exists := m.drafts["one"]; exists {
		t.Fatal("deleted chat's draft retained")
	}
	next, _ = m.Update(dialogsMsg{request: oldDialogs, chats: f.chats})
	if len(next.(Model).chats) != 1 {
		t.Fatal("late dialogs resurrected deleted chat")
	}
}

func TestDeleteCancellationAndFailureKeepConversation(t *testing.T) {
	m, f := fixture()
	manager := &managementClient{fakeClient: f}
	m.client = manager
	m, _ = press(m, "D")
	m, cmd := press(m, "n")
	if cmd != nil || m.mode != normal || manager.deletedChat != "" {
		t.Fatal("cancelled delete mutated state")
	}
	manager.fail = errors.New("permission denied")
	m, _ = press(m, "D")
	m, cmd = press(m, "y")
	next, _ := m.Update(cmd())
	m = next.(Model)
	if len(m.chats) != 2 || m.busy || !strings.Contains(m.failure, "permission denied") || m.mode != confirmAction {
		t.Fatal("failed deletion removed conversation or lost retry state")
	}
}

func TestPhoneImportWorksWithoutOtherManagementCapabilities(t *testing.T) {
	m, f := fixture()
	importer := &importOnlyClient{fakeClient: f}
	m.client = importer
	m.mode = contactPicker
	m, _ = press(m, "n")
	if m.mode != manageForm || len(m.form.values) != 2 {
		t.Fatal("phone import form did not open")
	}
	m, _ = press(m, "+14155552671")
	m, _ = press(m, "enter")
	m, _ = press(m, "New")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(Model)
	m, _ = press(m, "Person")
	m, cmd := press(m, "enter")
	if cmd == nil {
		t.Fatalf("independent ContactImporter rejected: %s", m.failure)
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if importer.phone != "+14155552671" || importer.name != "New Person" {
		t.Fatalf("import form lost values or spaces: %q, %q", importer.phone, importer.name)
	}
	if m.mode != contactPicker || len(m.contacts) != 1 || m.contacts[0].ID != "new-contact" {
		t.Fatal("imported contact unavailable for opening a chat")
	}
}
