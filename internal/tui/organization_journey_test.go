package tui

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/paramon-tech/tuigram/internal/core"
	"github.com/paramon-tech/tuigram/internal/demo"
)

// Drain finite action commands, including history/read batches. Periodic polling
// is disabled in these journeys and no live account or external files are used.
func finishOrganizationAction(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	pending := []tea.Cmd{cmd}
	for steps := 0; len(pending) > 0; steps++ {
		if steps > 100 {
			t.Fatal("action commands did not settle")
		}
		nextCmd := pending[0]
		pending = pending[1:]
		if nextCmd == nil {
			continue
		}
		msg := nextCmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			pending = append(pending, batch...)
			continue
		}
		next, follow := m.Update(msg)
		m = next.(Model)
		if follow != nil {
			pending = append(pending, follow)
		}
	}
	return m
}
func organizationJourney(t *testing.T, c *demo.Client) Model {
	t.Helper()
	m := New(context.Background(), c, Options{PollInterval: -1, DisableAutoRead: true})
	chats, err := c.Dialogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	next, cmd := m.Update(dialogsMsg{request: m.dialogsRequest, chats: chats})
	return finishOrganizationAction(t, next.(Model), cmd)
}
func TestOrganizationJourneyForwardToArchivedDestination(t *testing.T) {
	c := demo.New()
	ctx := context.Background()
	destination := core.Chat{ID: "news"}
	if err := c.SetChatArchived(ctx, destination, true); err != nil {
		t.Fatal(err)
	}
	m := organizationJourney(t, c)
	m.messageIndex = 0
	var cmd tea.Cmd
	m, cmd = press(m, "f")
	if cmd != nil || m.mode != forwardPicker {
		t.Fatal("forward picker did not open")
	}
	target := -1
	for i, chat := range m.forwardChats() {
		if chat.ID == destination.ID {
			target = i
		}
	}
	if target < 0 {
		t.Fatal("archived destination hidden by inbox filter")
	}
	m.pickerIndex = target
	m, cmd = press(m, "enter")
	m = finishOrganizationAction(t, m, cmd)
	messages, err := c.History(ctx, destination, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || !messages[1].Forwarded || messages[1].Text != "Welcome to tuigram 👋\nYour conversations, at keyboard speed." {
		t.Fatalf("forward did not reach archived chat: %+v", messages)
	}
	if current, _ := m.currentChat(); current.ID != "saved" {
		t.Fatal("forward unexpectedly changed current conversation")
	}
}
func TestOrganizationJourneyOpenArchivedContactPreservesHistory(t *testing.T) {
	c := demo.New()
	ctx := context.Background()
	contacts, err := c.SearchContacts(ctx, "ada")
	if err != nil || len(contacts) != 1 {
		t.Fatal("demo contact missing")
	}
	contact := contacts[0]
	chat := core.Chat{ID: contact.ID}
	if err := c.AddContact(ctx, contact); err != nil {
		t.Fatal(err)
	}
	if err := c.Send(ctx, chat, "Keep this archived message"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetChatArchived(ctx, chat, true); err != nil {
		t.Fatal(err)
	}
	m := organizationJourney(t, c)
	m.contacts = contacts
	m.pickerIndex = 0
	m.mode = contactPicker
	m, cmd := press(m, "enter")
	m = finishOrganizationAction(t, m, cmd)
	if current, _ := m.currentChat(); current.ID != contact.ID || !current.Archived {
		t.Fatalf("hidden contact not restored or unexpectedly unarchived: %+v", current)
	}
	if len(m.messages) != 1 || m.messages[0].Text != "Keep this archived message" {
		t.Fatalf("wrong conversation history: %+v", m.messages)
	}
	if m.organization.folderID != -2 || m.organization.unreadOnly {
		t.Fatal("contact opening did not reveal all chats")
	}
	// Change a setting while viewing that chat; history remains attached to it.
	m, cmd = press(m, ",")
	if cmd != nil || m.mode != settingsPanel {
		t.Fatal("settings did not open")
	}
	m, cmd = press(m, "ctrl+s")
	m = finishOrganizationAction(t, m, cmd)
	if current, _ := m.currentChat(); current.ID != contact.ID || len(m.messages) != 1 {
		t.Fatal("settings save changed selected chat/history")
	}
}
func TestOrganizationContactEditsChangeAutomaticFolderMembership(t *testing.T) {
	m := organizationFixture(t)
	m.installDialogs([]core.Chat{{ID: "person", Title: "Old", Kind: "private", Contact: true}})
	m.organization.folders = []core.ChatFolder{{ID: 2, Title: "Contacts", Contacts: true}, {ID: 3, Title: "Others", NonContacts: true}}
	m.organization.folderID = 2
	m.applyOrganizationFilter()
	next, _ := m.updateManagement(managementMsg{kind: "deleteContact", contact: core.Contact{ID: "person"}})
	m = next.(Model)
	m.applyOrganizationFilter()
	if len(m.chats) != 0 {
		t.Fatal("removed contact retained automatic Contacts membership")
	}
	m.organization.folderID = 3
	m.applyOrganizationFilter()
	if len(m.chats) != 1 || m.chats[0].Contact {
		t.Fatal("removed contact absent from NonContacts folder")
	}
	next, _ = m.updateManagement(managementMsg{kind: "importContact", contact: core.Contact{ID: "person", Name: "Restored"}})
	m = next.(Model)
	m.organization.folderID = 2
	m.applyOrganizationFilter()
	if len(m.chats) != 1 || !m.chats[0].Contact || m.chats[0].Title != "Restored" {
		t.Fatal("restored contact missing from Contacts folder")
	}
}

func TestOrganizationRefreshPreservesForwardDestination(t *testing.T) {
	c := demo.New()
	m := organizationJourney(t, c)
	m.messageIndex = 0
	m, _ = press(m, "f")
	for i, chat := range m.forwardChats() {
		if chat.ID == "builders" {
			m.pickerIndex = i
		}
	}
	chats, err := c.Dialogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	chats = []core.Chat{chats[2], chats[0], chats[1]}
	next, _ := m.Update(dialogsMsg{request: m.dialogsRequest, chats: chats})
	m = next.(Model)
	if m.mode != forwardPicker || m.forwardChats()[m.pickerIndex].ID != "builders" {
		t.Fatalf("refresh changed forward target to %+v", m.forwardChats()[m.pickerIndex])
	}
}

func TestOrganizationRestoresHistoricalChatAfterEmptyFilter(t *testing.T) {
	c := demo.New()
	m := organizationJourney(t, c)
	m.history.loaded = true
	m.history.chatID = "saved"
	m.history.hasNewer = true
	m.organization.folderID = -1
	m.applyOrganizationFilter()
	if len(m.chats) != 0 {
		t.Fatal("expected empty archive")
	}
	if m.history.loaded {
		t.Fatal("history cursor survived cleared selection")
	}
	m.organization.folderID = 0
	cmd := m.applyOrganizationFilter()
	if cmd == nil {
		t.Fatal("restored chat did not reload history")
	}
	m = finishOrganizationAction(t, m, cmd)
	if len(m.messages) == 0 {
		t.Fatal("restored chat was left with empty history")
	}
}

func TestOrganizationDialogRefreshKeepsExplicitHistoryRequest(t *testing.T) {
	m, c := historyFixture()
	m.loading = false
	m.dialogsPending = true
	c.page = core.HistoryPage{Messages: []core.Message{{ID: 1, ChatID: "one"}}, OldestID: 1, NewestID: 1, HasNewer: true}
	cmd := m.jumpHistory(true)
	request := m.historyRequest
	if cmd == nil {
		t.Fatal("oldest request did not start")
	}
	next, background := m.Update(dialogsMsg{request: m.dialogsRequest, chats: append([]core.Chat(nil), m.chats...)})
	m = next.(Model)
	if background != nil || m.historyRequest != request || !m.loading {
		t.Fatal("background dialog refresh superseded explicit history request")
	}
	m = finishOrganizationAction(t, m, cmd)
	if len(m.messages) != 1 || m.messages[0].ID != 1 || m.messageIndex != 0 {
		t.Fatal("explicit oldest response lost")
	}
}

func TestOrganizationFailedSearchClearCannotAcknowledgeSearchResults(t *testing.T) {
	m, c := historyFixture()
	m.opts.DisableAutoRead = false
	m.query = "needle"
	m.history.query = "needle"
	m.messages = []core.Message{{ID: 150, ChatID: "one", Text: "Matching result"}}
	m.messageIndex = 0
	m, cmd := press(m, "esc")
	if cmd == nil {
		t.Fatal("search clear did not request unfiltered history")
	}
	next, _ := m.Update(historyPageMsg{request: m.historyRequest, chatID: "one", query: "", action: historyRefresh, err: errors.New("offline")})
	m = next.(Model)
	if read := m.markVisibleRead(); read != nil {
		t.Fatal("failed search refresh scheduled a receipt for stale search result")
	}
	if len(c.readIDs) != 0 {
		t.Fatal("search result acknowledged unviewed messages")
	}
}
