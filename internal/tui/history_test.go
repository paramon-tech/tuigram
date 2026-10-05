package tui

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/paramon-tech/tuigram/internal/core"
)

type historyTestClient struct {
	*fakeClient
	requests  []core.HistoryRequest
	page      core.HistoryPage
	readIDs   []int
	readState core.ReadState
	readErr   error
}

func (c *historyTestClient) HistoryPage(_ context.Context, _ core.Chat, request core.HistoryRequest) (core.HistoryPage, error) {
	c.requests = append(c.requests, request)
	return c.page, nil
}
func (c *historyTestClient) MarkRead(_ context.Context, _ core.Chat, maxID int) (core.ReadState, error) {
	c.readIDs = append(c.readIDs, maxID)
	return c.readState, c.readErr
}

func historyFixture() (Model, *historyTestClient) {
	m, f := fixture()
	c := &historyTestClient{fakeClient: f}
	m.client = c
	m.opts.DisableAutoRead = true
	m.history = historyState{chatID: "one", loaded: true, oldestID: 100, newestID: 200, hasOlder: true}
	m.messages = []core.Message{{ID: 100}, {ID: 150}, {ID: 200}}
	m.messageIndex = 1
	return m, c
}

func TestHistoryMergePreservesSelectionAndRejectsStalePages(t *testing.T) {
	m, _ := historyFixture()
	m.historyRequest = 10
	msg := historyPageMsg{request: 10, chatID: "one", action: historyOlder, page: core.HistoryPage{Messages: []core.Message{{ID: 50}, {ID: 100, Text: "updated"}}, OldestID: 50, NewestID: 100, HasOlder: true}}
	next, _ := m.updateHistoryPage(msg)
	m = next.(Model)
	if len(m.messages) != 4 || m.messages[m.messageIndex].ID != 150 || m.messages[1].Text != "updated" || m.history.oldestID != 50 {
		t.Fatalf("incorrect merge: %+v selected=%d", m.messages, m.messageIndex)
	}
	msg.request = 9
	msg.page.Messages = []core.Message{{ID: 1}}
	next, _ = m.updateHistoryPage(msg)
	if len(next.(Model).messages) != 4 {
		t.Fatal("stale request applied")
	}
	msg.request = 10
	msg.query = "other"
	next, _ = m.updateHistoryPage(msg)
	if len(next.(Model).messages) != 4 {
		t.Fatal("stale query applied")
	}
	msg.chatID = "two"
	msg.query = ""
	next, _ = m.updateHistoryPage(msg)
	if len(next.(Model).messages) != 4 {
		t.Fatal("stale chat applied")
	}
}

func TestHistoryRefreshKeepsOlderPagesAndRemovesDeletedRecentMessage(t *testing.T) {
	m, _ := historyFixture()
	m.historyRequest = 2
	msg := historyPageMsg{request: 2, chatID: "one", action: historyRefresh, page: core.HistoryPage{Messages: []core.Message{{ID: 150, Text: "edited"}, {ID: 201}}, OldestID: 150, NewestID: 201, HasOlder: true}}
	next, _ := m.updateHistoryPage(msg)
	m = next.(Model)
	if len(m.messages) != 3 || m.messages[0].ID != 100 || m.messages[1].Text != "edited" || m.messages[2].ID != 201 || m.messages[m.messageIndex].ID != 150 {
		t.Fatalf("refresh discarded history or selection: %+v", m.messages)
	}
}

func TestOldHistorySkipsPollingButRefreshesSelectedMutation(t *testing.T) {
	m, c := historyFixture()
	m.loading = false
	m.history.hasNewer = true
	if m.fetchHistory() != nil {
		t.Fatal("polling replaced historical window")
	}
	c.page = core.HistoryPage{Messages: []core.Message{{ID: 100}, {ID: 150, Text: "edited"}}, OldestID: 100, NewestID: 150, HasOlder: true, HasNewer: true}
	cmd := m.refreshHistoryWindow()
	if cmd == nil {
		t.Fatal("mutation did not refresh historical window")
	}
	response := cmd().(historyPageMsg)
	if len(c.requests) != 1 || c.requests[0].BeforeID != 151 {
		t.Fatalf("wrong refresh boundary: %+v", c.requests)
	}
	next, _ := m.updateHistoryPage(response)
	m = next.(Model)
	if len(m.messages) != 3 || m.messages[1].Text != "edited" || m.messages[2].ID != 200 || m.messages[m.messageIndex].ID != 150 || !m.history.hasNewer {
		t.Fatalf("window refresh incorrect %+v", m.messages)
	}
}

func TestReadReceiptOnlyMarksDisplayedUnfilteredSelection(t *testing.T) {
	m, c := historyFixture()
	m.opts.DisableAutoRead = false
	m.loading = false
	m.width = 60
	m.focus = 0
	if m.markVisibleRead() != nil {
		t.Fatal("marked hidden narrow history read")
	}
	m.focus = 1
	m.query = "needle"
	if m.markVisibleRead() != nil {
		t.Fatal("search result marked intervening messages read")
	}
	m.query = ""
	m.mode = compose
	if m.markVisibleRead() != nil {
		t.Fatal("marked history hidden behind composer")
	}
	m.mode = normal
	c.readState = core.ReadState{MaxID: 150, TopMessageID: 200, Unread: 1}
	cmd := m.markVisibleRead()
	if cmd == nil || len(c.readIDs) != 0 {
		t.Fatal("receipt must be deferred")
	}
	if m.markVisibleRead() != nil {
		t.Fatal("duplicate concurrent read receipt")
	}
	next, _ := m.updateReadState(cmd().(readStateMsg))
	m = next.(Model)
	if len(c.readIDs) != 1 || c.readIDs[0] != 150 || m.chats[0].Unread != 1 || m.chats[0].ReadInboxMaxID != 150 {
		t.Fatalf("read beyond selected boundary: ids=%v chat=%+v", c.readIDs, m.chats[0])
	}
	if m.markVisibleRead() != nil {
		t.Fatal("acknowledged boundary sent twice")
	}
}

func TestReadSnapshotPreservesNewerArrivalsAndInvalidatesOldDialogs(t *testing.T) {
	m, _ := historyFixture()
	m.opts.DisableAutoRead = false
	m.chats[0].TopMessageID = 210
	m.chats[0].Unread = 4
	m.allChats = append([]core.Chat(nil), m.chats...)
	m.history.readPending = map[string]int{"one": 150}
	m.dialogsPending = true
	m.dialogsRequest = 10
	next, _ := m.updateReadState(readStateMsg{chatID: "one", maxID: 150, state: core.ReadState{MaxID: 150, TopMessageID: 200, Unread: 1}})
	m = next.(Model)
	if m.chats[0].Unread != 4 || m.allChats[0].Unread != 4 || m.chats[0].ReadInboxMaxID != 150 {
		t.Fatalf("new unread arrivals erased: %+v", m.chats[0])
	}
	if m.dialogsPending || m.dialogsRequest != 11 {
		t.Fatal("stale dialogs generation not invalidated")
	}
}

func TestFailedReadKeepsUnreadAndCanRetry(t *testing.T) {
	m, c := historyFixture()
	m.opts.DisableAutoRead = false
	m.loading = false
	m.chats[0].Unread = 3
	c.readErr = errors.New("offline")
	cmd := m.markVisibleRead()
	next, again := m.updateReadState(cmd().(readStateMsg))
	m = next.(Model)
	if m.chats[0].Unread != 3 || again != nil || m.failureKind != "read" {
		t.Fatal("failed read hid unread count or retried in a loop")
	}
	if m.markVisibleRead() != nil {
		t.Fatal("failed read retried before its backoff elapsed")
	}
	m.history.readRetryAt["one"] = time.Now().Add(-time.Second)
	if m.markVisibleRead() == nil {
		t.Fatal("failed read cannot retry after its backoff")
	}
}

func TestManualUnreadCanBeClearedAtAlreadyReadBoundary(t *testing.T) {
	m, c := historyFixture()
	m.opts.DisableAutoRead = false
	m.loading = false
	m.chats[0].UnreadMark = true
	m.chats[0].ReadInboxMaxID = 200
	m.messageIndex = 2
	c.readState = core.ReadState{MaxID: 200, TopMessageID: 200}
	cmd := m.markVisibleRead()
	if cmd == nil {
		t.Fatal("manual unread flag prevented read receipt")
	}
	next, again := m.updateReadState(cmd().(readStateMsg))
	m = next.(Model)
	if m.chats[0].UnreadMark || again != nil {
		t.Fatal("manual unread flag not cleared")
	}
}

func TestHistoryPageSchedulesReceiptWithPendingState(t *testing.T) {
	m, _ := historyFixture()
	m.opts.DisableAutoRead = false
	m.historyRequest = 10
	next, cmd := m.updateHistoryPage(historyPageMsg{request: 10, chatID: "one", action: historyLatest, page: core.HistoryPage{Messages: []core.Message{{ID: 200}}, OldestID: 200, NewestID: 200}})
	m = next.(Model)
	if cmd == nil || m.history.readPending["one"] != 200 {
		t.Fatal("receipt command lost pending state in returned model")
	}
}

func TestServiceOnlyHistoryCanBeReadButCallOverlayCannot(t *testing.T) {
	m, c := historyFixture()
	m.opts.DisableAutoRead = false
	m.loading = false
	m.messages = nil
	m.history.newestID = 300
	m.historyObscured = true
	if m.markVisibleRead() != nil {
		t.Fatal("call overlay marked hidden history read")
	}
	m.historyObscured = false
	cmd := m.markVisibleRead()
	if cmd == nil {
		t.Fatal("service-only latest history cannot be marked read")
	}
	cmd()
	if len(c.readIDs) != 1 || c.readIDs[0] != 300 {
		t.Fatalf("wrong service receipt: %v", c.readIDs)
	}
}
