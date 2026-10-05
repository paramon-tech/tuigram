package tui

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/paramon-tech/tuigram/internal/core"
)

type testRateLimit struct{ delay time.Duration }

func (e testRateLimit) Error() string             { return "Telegram rate limit" }
func (e testRateLimit) RetryAfter() time.Duration { return e.delay }

func TestReadReceiptHonorsWrappedRateLimit(t *testing.T) {
	m, c := historyFixture()
	m.opts.DisableAutoRead = false
	m.focus = 1
	c.readErr = fmt.Errorf("mark read: %w", testRateLimit{2 * time.Minute})
	m.chats[0].Unread = 3
	cmd := m.markVisibleRead()
	next, again := m.updateReadState(cmd().(readStateMsg))
	m = next.(Model)
	if again != nil || m.markVisibleRead() != nil || m.chats[0].Unread != 3 {
		t.Fatal("rate-limited receipt retried or changed unread count")
	}
	if remaining := time.Until(m.history.readRetryAt["one"]); remaining < 119*time.Second || remaining > 120*time.Second {
		t.Fatalf("receipt ignored server deadline: %s", remaining)
	}
	for range 10 {
		m, cmd = press(m, "j")
		if cmd != nil {
			t.Fatal("navigation retried a rate-limited receipt")
		}
	}
	if len(c.readIDs) != 1 {
		t.Fatalf("sent %d receipts during cooldown", len(c.readIDs))
	}
	m.history.readRetryAt["one"] = time.Now().Add(-time.Second)
	if m.markVisibleRead() == nil {
		t.Fatal("receipt did not become retryable after deadline")
	}
}

func TestBackgroundPollingHonorsRateLimitAndResumes(t *testing.T) {
	for _, source := range []string{"dialogs", "legacy history", "paged history"} {
		t.Run(source, func(t *testing.T) {
			m, _ := fixture()
			err := fmt.Errorf("load: %w", testRateLimit{time.Minute})
			var msg tea.Msg
			switch source {
			case "dialogs":
				msg = dialogsMsg{request: m.dialogsRequest, err: err}
			case "legacy history":
				msg = historyMsg{request: m.historyRequest, chatID: "one", err: err}
			case "paged history":
				msg = historyPageMsg{request: m.historyRequest, chatID: "one", err: err}
			}
			next, _ := m.Update(msg)
			m = next.(Model)
			request := m.dialogsRequest
			next, cmd := m.Update(tickMsg(time.Now()))
			m = next.(Model)
			if cmd != nil || m.dialogsPending || m.dialogsRequest != request {
				t.Fatal("background poll ignored Telegram cooldown")
			}
			if remaining := time.Until(m.pollRetryAt); remaining < 59*time.Second || remaining > time.Minute {
				t.Fatalf("wrong background deadline: %s", remaining)
			}
			m.pollRetryAt = time.Now().Add(-time.Second)
			next, cmd = m.Update(tickMsg(time.Now()))
			m = next.(Model)
			if cmd == nil || !m.dialogsPending || m.dialogsRequest != request+1 {
				t.Fatal("background polling did not resume after cooldown")
			}
		})
	}
}

func TestBackgroundDialogsDoNotRepeatSearchButManualRefreshDoes(t *testing.T) {
	m, c := historyFixture()
	m.query, m.history.query = "needle", "needle"
	next, cmd := m.Update(tickMsg(time.Now()))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("background dialog refresh missing")
	}
	next, cmd = m.Update(cmd())
	m = next.(Model)
	if cmd != nil || len(c.requests) != 0 {
		t.Fatal("background dialogs reran explicit search")
	}
	m, cmd = press(m, "R")
	if cmd == nil {
		t.Fatal("manual refresh missing")
	}
	next, cmd = m.Update(cmd())
	m = next.(Model)
	if cmd == nil {
		t.Fatal("manual refresh did not rerun search")
	}
	cmd()
	if len(c.requests) != 1 || c.requests[0].Query != "needle" {
		t.Fatalf("manual refresh sent wrong query: %+v", c.requests)
	}
	m.cancelHistory()
}

func TestHistoricalWindowRetriesReadAfterCooldownWithoutRefetching(t *testing.T) {
	m, c := historyFixture()
	m.opts.DisableAutoRead = false
	m.history.hasNewer = true
	c.readErr = fmt.Errorf("mark read: %w", testRateLimit{time.Minute})
	first := m.markVisibleRead()
	next, cmd := m.Update(first())
	m = next.(Model)
	if cmd != nil || len(c.readIDs) != 1 {
		t.Fatal("failed receipt retried immediately")
	}
	// A normal dialog refresh during the cooldown must remain quiet.
	next, cmd = m.Update(dialogsMsg{request: m.dialogsRequest, chats: c.chats})
	m = next.(Model)
	if cmd != nil || len(c.requests) != 0 {
		t.Fatal("historical window fetched messages or retried before cooldown expired")
	}
	m.history.readRetryAt["one"] = time.Now().Add(-time.Second)
	c.readErr = nil
	c.readState = core.ReadState{MaxID: 150, TopMessageID: 200, Unread: 1}
	next, cmd = m.Update(tickMsg(time.Now()))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("background refresh missing")
	}
	next, cmd = m.Update(cmd())
	m = next.(Model)
	if cmd == nil || len(c.requests) != 0 {
		t.Fatal("expired receipt did not retry independently of historical pagination")
	}
	next, cmd = m.Update(cmd())
	m = next.(Model)
	if cmd != nil || len(c.readIDs) != 2 || c.readIDs[1] != 150 || m.chats[0].Unread != 1 || len(c.requests) != 0 {
		t.Fatal("historical receipt retry did not preserve visible boundary or unread state")
	}
}

type blockingHistoryClient struct {
	*fakeClient
	started chan context.Context
}

func (c *blockingHistoryClient) HistoryPage(ctx context.Context, _ core.Chat, _ core.HistoryRequest) (core.HistoryPage, error) {
	c.started <- ctx
	<-ctx.Done()
	return core.HistoryPage{}, ctx.Err()
}

func TestHistoryJumpDeduplicatesAndCancelsSupersededRequest(t *testing.T) {
	m, f := fixture()
	c := &blockingHistoryClient{fakeClient: f, started: make(chan context.Context, 1)}
	m.client = c
	first := m.jumpHistory(true)
	request := m.historyRequest
	if m.jumpHistory(true) != nil || m.historyRequest != request {
		t.Fatal("held beginning key started duplicate history request")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- first() }()
	var firstContext context.Context
	select {
	case firstContext = <-c.started:
	case <-time.After(time.Second):
		t.Fatal("history request did not start")
	}
	second := m.jumpHistory(false)
	defer m.cancelHistory()
	if second == nil || !errors.Is(firstContext.Err(), context.Canceled) {
		t.Fatal("changed history destination did not cancel obsolete RPC")
	}
	select {
	case msg := <-done:
		next, cmd := m.Update(msg)
		m = next.(Model)
		if cmd != nil || !m.loading || m.history.pending == nil || m.history.pending.action != historyLatest {
			t.Fatal("cancelled result overwrote newer history request")
		}
	case <-time.After(time.Second):
		t.Fatal("obsolete history request did not stop")
	}
}

func TestObsoleteQueuedHistoryNeverInvokesClient(t *testing.T) {
	m, c := historyFixture()
	first := m.jumpHistory(true)
	second := m.jumpHistory(false)
	defer m.cancelHistory()
	msg := first().(historyPageMsg)
	if !errors.Is(msg.err, context.Canceled) || len(c.requests) != 0 {
		t.Fatal("superseded queued request still reached Telegram")
	}
	second()
	if len(c.requests) != 1 || c.requests[0].Oldest {
		t.Fatalf("new history destination not sent: %+v", c.requests)
	}
}

func TestReplacingDialogSelectionCancelsQueuedHistory(t *testing.T) {
	m, c := historyFixture()
	cmd := m.jumpHistory(true)
	m.installDialogs(nil)
	msg := cmd().(historyPageMsg)
	if !errors.Is(msg.err, context.Canceled) || m.history.pending != nil || len(c.requests) != 0 {
		t.Fatal("removed conversation kept its pending history request")
	}
}
