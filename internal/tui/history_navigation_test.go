package tui

import (
	"context"
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/paramon-tech/tuigram/internal/core"
	"github.com/paramon-tech/tuigram/internal/demo"
)

func navigateHistory(t *testing.T, m Model, key string) Model {
	t.Helper()
	next, cmd := press(m, key)
	return finishOrganizationAction(t, next, cmd)
}

func TestHistoryNavigationSearchesBeyondFirstPageAndRefreshesOldReactions(t *testing.T) {
	c := demo.New()
	chat := core.Chat{ID: "saved"}
	for i := 0; i < 240; i++ {
		if err := c.Send(context.Background(), chat, fmt.Sprintf("needle %03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	m := organizationJourney(t, c)
	m = navigateHistory(t, m, "/")
	m = navigateHistory(t, m, "needle")
	m = navigateHistory(t, m, "enter")
	if len(m.messages) != 100 || !m.history.hasOlder {
		t.Fatal("initial whole-chat search did not return bounded page")
	}
	m = navigateHistory(t, m, "B")
	if msg, _ := m.currentMessage(); msg.Text != "needle 000" || !m.history.hasNewer {
		t.Fatalf("beginning search result not reached: %+v", msg)
	}
	m = navigateHistory(t, m, "r")
	m = navigateHistory(t, m, "enter")
	if msg, _ := m.currentMessage(); msg.Text != "needle 000" || len(msg.Reactions) != 1 || !msg.Reactions[0].Mine {
		t.Fatalf("old search reaction not refreshed in place: %+v", msg)
	}
	m = navigateHistory(t, m, "]")
	if len(m.messages) != 200 {
		t.Fatalf("newer page not merged: %d", len(m.messages))
	}
	// Submitting the same query after browsing oldest must restart the query,
	// even though its previously loaded window had newer pages.
	m = navigateHistory(t, m, "/")
	m = navigateHistory(t, m, "enter")
	if len(m.messages) != 100 || m.history.hasNewer {
		t.Fatal("same query left an empty or historical window")
	}
	m = navigateHistory(t, m, "[")
	m = navigateHistory(t, m, "[")
	if len(m.messages) != 240 || m.history.hasOlder {
		t.Fatalf("older search pages did not reach beginning: %d", len(m.messages))
	}
	m = navigateHistory(t, m, "G")
	if msg, _ := m.currentMessage(); msg.Text != "needle 239" {
		t.Fatalf("latest jump did not reach final match: %+v", msg)
	}
	m = navigateHistory(t, m, "g")
	m = navigateHistory(t, m, "g")
	if msg, _ := m.currentMessage(); msg.Text != "needle 000" {
		t.Fatal("gg did not jump to chat search beginning")
	}
}

func TestReadReceiptsWaitForVisibleHistoryAndKeepUnreadCountsSynchronized(t *testing.T) {
	c := demo.New()
	m := New(context.Background(), c, Options{PollInterval: -1})
	m.width, m.height = 50, 24
	m = finishOrganizationAction(t, m, m.Init())
	m = navigateHistory(t, m, "j")
	if chat, _ := m.currentChat(); chat.ID != "builders" || chat.Unread == 0 {
		t.Fatal("narrow hidden history was marked read")
	}
	m = navigateHistory(t, m, "enter")
	if chat, _ := m.currentChat(); chat.Unread != 0 || chat.ReadInboxMaxID != 7 {
		t.Fatalf("viewed channel/chat stayed unread: %+v", chat)
	}
	for _, chat := range m.allChats {
		if chat.ID == "builders" && chat.Unread != 0 {
			t.Fatal("canonical unread count stayed stale")
		}
	}
}

func TestCallOverlayDoesNotAcknowledgeHiddenHistory(t *testing.T) {
	c := demo.New()
	m := organizationJourney(t, c)
	m.opts.DisableAutoRead = false
	m.chatIndex = 1
	m.messages = nil
	m.history.loaded = false
	cmd := m.fetchHistory()
	m.openCallPanel()
	next, read := m.Update(cmd())
	m = next.(Model)
	if read != nil || m.history.readPending["builders"] != 0 {
		t.Fatal("history hidden behind call panel generated a read receipt")
	}
	m = navigateHistory(t, m, "esc")
	if chat, _ := m.currentChat(); chat.Unread != 0 {
		t.Fatal("returning from call panel did not acknowledge visible history")
	}
}

func TestExpandedPanelsStayWithinTerminalBounds(t *testing.T) {
	c := demo.New()
	m := organizationJourney(t, c)
	for _, md := range []mode{settingsPanel, organizationPanel, reactionPicker} {
		m.openSettings()
		m.openReactions()
		m.mode = md
		for _, size := range [][2]int{{24, 8}, {40, 15}, {80, 24}} {
			next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m = next.(Model)
			if view := m.View(); view == "" || lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
				t.Fatalf("panel %d exceeds %dx%d", md, size[0], size[1])
			}
		}
	}
}
