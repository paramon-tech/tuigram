package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/paramon-tech/tuigram/internal/core"
	"github.com/paramon-tech/tuigram/internal/demo"
)

func organizationFixture(t *testing.T) Model {
	t.Helper()
	c := demo.New()
	chats, err := c.Dialogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m := New(context.Background(), c, Options{})
	m.loading = false
	m.dialogsPending = false
	m.installDialogs(chats)
	return m
}
func TestOrganizationArchiveRecoveryAndPinnedOrdering(t *testing.T) {
	m := organizationFixture(t)
	m.installDialogs([]core.Chat{{ID: "one"}, {ID: "archived", Archived: true}, {ID: "pin", Pinned: true}})
	if len(m.chats) != 2 || m.chats[0].ID != "pin" {
		t.Fatalf("inbox filtering/sort: %+v", m.chats)
	}
	m.organization.folderID = -1
	m.applyOrganizationFilter()
	if len(m.chats) != 1 || m.chats[0].ID != "archived" {
		t.Fatalf("archive missing: %+v", m.chats)
	}
	m.organization.folderID = -2
	m.applyOrganizationFilter()
	if len(m.chats) != 3 {
		t.Fatal("all chats did not restore archived")
	}
}
func TestUnreadFilterRetainsReadSelectionOnPassiveRefresh(t *testing.T) {
	m := organizationFixture(t)
	chats := []core.Chat{{ID: "one", Unread: 2}, {ID: "two", Unread: 4}, {ID: "three", Unread: 0}}
	m.installDialogs(chats)
	m.organization.unreadOnly = true
	m.applyOrganizationFilter()
	m.messages = []core.Message{{ID: 1, ChatID: "one"}}
	chats[0].Unread = 0
	m.installDialogs(chats)
	current, _ := m.currentChat()
	if current.ID != "one" || len(m.messages) != 1 {
		t.Fatal("passive unread refresh advanced selection and could mark next chat read")
	}
	m.applyOrganizationFilter()
	current, _ = m.currentChat()
	if current.ID != "two" || len(m.chats) != 1 {
		t.Fatal("explicit filter did not hide read chats")
	}
	// Retention must not leak an archived chat back into Inbox.
	chats[1].Archived = true
	m.installDialogs(chats)
	if len(m.chats) != 0 {
		t.Fatalf("archive leaked into unread inbox: %+v", m.chats)
	}
}
func TestFolderRulesAndSelectionPreservation(t *testing.T) {
	m := organizationFixture(t)
	m.organization.folders = []core.ChatFolder{{ID: 3, Title: "Groups", Groups: true, ExcludeRead: true, PinnedIDs: []string{"second", "first"}}}
	m.organization.folderID = 3
	m.installDialogs([]core.Chat{{ID: "first", Kind: "group"}, {ID: "second", Kind: "private"}, {ID: "other", Kind: "private", Unread: 1}})
	if len(m.chats) != 2 || m.chats[0].ID != "second" {
		t.Fatalf("folder rules/order lost: %+v", m.chats)
	}
	m.chatIndex = 1
	m.messages = []core.Message{{ID: 7}}
	m.query = "keep"
	m.installDialogs(m.allChats)
	if current, _ := m.currentChat(); current.ID != "first" || m.query != "keep" || len(m.messages) != 1 {
		t.Fatal("refresh lost selection/search")
	}
}
func TestOrganizationMenuCreatesFolderWithSelectedChat(t *testing.T) {
	m := organizationFixture(t)
	cmd := m.openOrganization()
	next, _ := m.Update(cmd())
	m = next.(Model)
	if m.organization.pending || m.mode != organizationPanel {
		t.Fatal("menu did not load")
	}
	next, _ = m.updateOrganizationKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = next.(Model)
	next, _ = m.updateOrganizationKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("My")})
	m = next.(Model)
	next, _ = m.updateOrganizationKey(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(Model)
	next, _ = m.updateOrganizationKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("chats")})
	m = next.(Model)
	next, cmd = m.updateOrganizationKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil || !m.busy {
		t.Fatal("folder not submitted")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.busy || len(m.organization.folders) != 1 || m.organization.folders[0].Title != "My chats" || !m.organization.folders[0].Contains(m.organization.target) {
		t.Fatalf("folder failed: %+v", m.organization)
	}
	if !strings.Contains(m.organizationText(), "My chats") {
		t.Fatal("folder not listed")
	}
}
func TestStaleOrganizationResultCannotOverwriteNewPanel(t *testing.T) {
	m := organizationFixture(t)
	m.organization.request = 3
	m.organization.pending = true
	next, _ := m.updateOrganization(organizationMsg{request: 2, action: "folders", folders: []core.ChatFolder{{ID: 9}}})
	m = next.(Model)
	if len(m.organization.folders) != 0 || !m.organization.pending {
		t.Fatal("stale folders applied")
	}
}

func TestOrganizationFiltersKeepManagementChanges(t *testing.T) {
	m := organizationFixture(t)
	m.installDialogs([]core.Chat{{ID: "one", Title: "Old", Kind: "group"}, {ID: "person", Title: "Person", Kind: "private", Archived: true}})
	next, _ := m.updateManagement(managementMsg{kind: "renameChat", chat: core.Chat{ID: "one", Title: "New"}})
	m = next.(Model)
	m.organization.folderID = -2
	m.applyOrganizationFilter()
	if m.chats[0].Title != "New" {
		t.Fatal("filter switch restored old chat title")
	}
	next, _ = m.updateManagement(managementMsg{kind: "editContact", contact: core.Contact{ID: "person", Name: "New name"}})
	m = next.(Model)
	m.applyOrganizationFilter()
	if m.chats[1].Title != "New name" {
		t.Fatal("filter switch restored old contact name")
	}
	next, _ = m.updateManagement(managementMsg{kind: "deleteChat", chat: core.Chat{ID: "one"}})
	m = next.(Model)
	m.applyOrganizationFilter()
	if len(m.chats) != 1 || m.chats[0].ID != "person" {
		t.Fatalf("deleted chat resurrected: %+v", m.chats)
	}
	next, _ = m.updateManagement(managementMsg{kind: "createGroup", chat: core.Chat{ID: "new", Title: "New group", Kind: "group"}})
	m = next.(Model)
	m.organization.folderID = -2
	m.applyOrganizationFilter()
	if len(m.chats) != 2 {
		t.Fatal("created group lost after changing filters")
	}
}
