package tui

import (
	"context"
	"fmt"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/paramon-tech/tuigram/internal/core"
)

type historyAction int

const (
	historyRefresh historyAction = iota
	historyOlder
	historyNewer
	historyOldest
	historyLatest
	historyWindow
)

type historyState struct {
	chatID, query              string
	oldestID, newestID         int
	hasOlder, hasNewer, loaded bool
	readPending, readConfirmed map[string]int
	readRetryAt                map[string]time.Time
	pending                    *historyFetch
}

type historyFetch struct {
	chatID, query string
	action        historyAction
	request       core.HistoryRequest
	cancel        context.CancelFunc
}

func (m *Model) cancelHistory() {
	if m.history.pending != nil {
		m.history.pending.cancel()
		m.history.pending = nil
	}
}

// A held navigation key should share its outstanding request. Changing chat,
// search, or page instead cancels obsolete work rather than merely discarding
// its response after Telegram has already served it.
func (m *Model) beginHistory(chatID string, action historyAction, request core.HistoryRequest) (context.Context, context.CancelFunc) {
	if pending := m.history.pending; pending != nil && pending.chatID == chatID && pending.query == m.query && pending.action == action && pending.request == request {
		return nil, nil
	}
	m.cancelHistory()
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	m.history.pending = &historyFetch{chatID: chatID, query: m.query, action: action, request: request, cancel: cancel}
	return ctx, cancel
}

type historyPageMsg struct {
	request       uint64
	chatID, query string
	action        historyAction
	page          core.HistoryPage
	boundary      int
	err           error
}

type readStateMsg struct {
	chatID string
	maxID  int
	state  core.ReadState
	err    error
}

func (m *Model) requestHistory(action historyAction) tea.Cmd {
	chat, ok := m.currentChat()
	if !ok {
		return nil
	}
	client, paged := m.client.(core.HistoryClient)
	if !paged {
		if action != historyRefresh && action != historyLatest {
			m.status = "This connection does not support history pagination"
			return nil
		}
		ctx, cancel := m.beginHistory(chat.ID, action, core.HistoryRequest{Query: m.query})
		if ctx == nil {
			return nil
		}
		m.historyRequest++
		m.loading = true
		legacy, request, query := m.client, m.historyRequest, m.query
		return func() tea.Msg {
			defer cancel()
			if err := ctx.Err(); err != nil {
				return historyMsg{request: request, chatID: chat.ID, err: err}
			}
			messages, err := legacy.History(ctx, chat, query)
			return historyMsg{request, chat.ID, messages, err}
		}
	}
	same := m.history.loaded && m.history.chatID == chat.ID && m.history.query == m.query
	// Background refresh must not replace a historical window with the newest
	// page. The user can explicitly return to live history with End/G.
	if action == historyRefresh && same && m.history.hasNewer {
		return nil
	}
	request := core.HistoryRequest{Query: m.query, Limit: 100}
	switch action {
	case historyOlder:
		if m.loading || !same || !m.history.hasOlder {
			return nil
		}
		request.BeforeID = m.history.oldestID
	case historyNewer:
		if m.loading || !same || !m.history.hasNewer {
			return nil
		}
		request.AfterID = m.history.newestID
	case historyOldest:
		request.Oldest = true
	case historyWindow:
		if message, ok := m.currentMessage(); ok {
			request.BeforeID = message.ID + 1
		}
	}
	ctx, cancel := m.beginHistory(chat.ID, action, request)
	if ctx == nil {
		return nil
	}
	m.historyRequest++
	m.loading = true
	generation, query := m.historyRequest, m.query
	return func() tea.Msg {
		defer cancel()
		if err := ctx.Err(); err != nil {
			return historyPageMsg{request: generation, chatID: chat.ID, query: query, action: action, err: err}
		}
		page, err := client.HistoryPage(ctx, chat, request)
		return historyPageMsg{request: generation, chatID: chat.ID, query: query, action: action, page: page, boundary: request.BeforeID, err: err}
	}
}

// refreshHistoryWindow updates the selected message after a mutation without
// discarding pages or jumping from an old search result to the newest result.
func (m *Model) refreshHistoryWindow() tea.Cmd {
	if m.history.loaded && (m.history.hasNewer || m.messageIndex < len(m.messages)-1) {
		return m.requestHistory(historyWindow)
	}
	return m.requestHistory(historyRefresh)
}

func (m *Model) loadOlderHistory() tea.Cmd { return m.requestHistory(historyOlder) }
func (m *Model) loadNewerHistory() tea.Cmd { return m.requestHistory(historyNewer) }
func (m *Model) jumpHistory(oldest bool) tea.Cmd {
	if oldest {
		return m.requestHistory(historyOldest)
	}
	return m.requestHistory(historyLatest)
}

func (m Model) updateHistoryPage(msg historyPageMsg) (tea.Model, tea.Cmd) {
	chat, ok := m.currentChat()
	if !ok || msg.chatID != chat.ID || msg.query != m.query || msg.request != m.historyRequest {
		return m, nil
	}
	m.cancelHistory()
	m.loading = false
	if msg.err != nil {
		m.deferPolling(msg.err)
		m.setFailure("history", msg.err.Error())
		return m, nil
	}
	m.clearFailure("history")
	selected, selectedOK := m.currentMessage()
	selectedIndex := m.messageIndex
	atEnd := m.messageIndex >= len(m.messages)-1
	same := m.history.loaded && m.history.chatID == chat.ID && m.history.query == m.query
	page := msg.page
	if same && msg.action == historyWindow && msg.boundary > 0 {
		kept := make([]core.Message, 0, len(m.messages))
		for _, message := range m.messages {
			if message.ID >= msg.boundary || (page.HasOlder && page.OldestID > 0 && message.ID < page.OldestID) {
				kept = append(kept, message)
			}
		}
		m.messages = mergeHistory(kept, page.Messages)
		if !page.HasOlder || (page.OldestID > 0 && page.OldestID <= m.history.oldestID) {
			m.history.oldestID, m.history.hasOlder = page.OldestID, page.HasOlder
		}
	} else if same && (msg.action == historyOlder || msg.action == historyNewer) {
		m.messages = mergeHistory(m.messages, page.Messages)
		if msg.action == historyOlder {
			if page.OldestID > 0 {
				m.history.oldestID = page.OldestID
			}
			m.history.hasOlder = page.HasOlder
		} else {
			if page.NewestID > 0 {
				m.history.newestID = page.NewestID
			}
			m.history.hasNewer = page.HasNewer
		}
	} else if same && msg.action == historyRefresh && !m.history.hasNewer && page.OldestID > 0 && page.HasOlder {
		// Refresh the recent window (including deletions) while retaining loaded
		// older pages and the user's selected message.
		older := make([]core.Message, 0)
		for _, message := range m.messages {
			if message.ID < page.OldestID {
				older = append(older, message)
			}
		}
		m.messages = mergeHistory(older, page.Messages)
		if m.history.oldestID == 0 || page.OldestID < m.history.oldestID {
			m.history.oldestID = page.OldestID
			m.history.hasOlder = page.HasOlder
		}
		m.history.newestID, m.history.hasNewer = page.NewestID, page.HasNewer
	} else {
		m.messages = page.Messages
		m.history.oldestID, m.history.newestID = page.OldestID, page.NewestID
		m.history.hasOlder, m.history.hasNewer = page.HasOlder, page.HasNewer
	}
	m.history.chatID, m.history.query, m.history.loaded = chat.ID, m.query, true
	m.messageIndex = max(0, len(m.messages)-1)
	if msg.action == historyOldest {
		m.messageIndex = 0
	} else if selectedOK && msg.action != historyLatest && (msg.action == historyOlder || msg.action == historyNewer || msg.action == historyWindow || !atEnd) {
		m.messageIndex = min(selectedIndex, max(0, len(m.messages)-1))
		for i, message := range m.messages {
			if message.ID == selected.ID {
				m.messageIndex = i
				break
			}
		}
	}
	m.status = m.historySummary()
	cmd := m.markVisibleRead()
	return m, cmd
}

func mergeHistory(existing, incoming []core.Message) []core.Message {
	byID := make(map[int]core.Message, len(existing)+len(incoming))
	for _, message := range existing {
		byID[message.ID] = message
	}
	for _, message := range incoming {
		byID[message.ID] = message
	}
	result := make([]core.Message, 0, len(byID))
	for _, message := range byID {
		result = append(result, message)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (m Model) historySummary() string {
	text := fmt.Sprintf("%d messages loaded", len(m.messages))
	if m.query != "" {
		text = fmt.Sprintf("%d matches loaded · searching entire chat", len(m.messages))
	}
	if m.history.hasOlder {
		text += " · [ older"
	} else {
		text += " · beginning reached"
	}
	if m.history.hasNewer {
		text += " · ] newer"
	}
	return text
}

func (m *Model) markVisibleRead() tea.Cmd {
	if m.opts.DisableAutoRead || m.historyObscured || m.query != "" || m.loading || m.width < 24 || m.height < 8 {
		return nil
	}
	if m.mode != normal && m.mode != messageReader {
		return nil
	}
	if m.mode == normal && m.width < 72 && m.focus == 0 {
		return nil
	}
	client, supported := m.client.(core.ReadClient)
	if !supported {
		return nil
	}
	chat, ok := m.currentChat()
	if !ok {
		return nil
	}
	if time.Now().Before(m.history.readRetryAt[chat.ID]) {
		return nil
	}
	if _, paged := m.client.(core.HistoryClient); paged && (!m.history.loaded || m.history.chatID != chat.ID || m.history.query != "") {
		return nil
	}
	message, ok := m.currentMessage()
	if !ok && (len(m.messages) > 0 || !m.history.loaded || m.history.hasNewer || m.history.chatID != chat.ID) {
		return nil
	}
	maxID := message.ID
	// Account for a final service message omitted from rendered history, only
	// when the displayed selection is the end of the current unfiltered page.
	if (len(m.messages) == 0 || m.messageIndex == len(m.messages)-1) && m.history.chatID == chat.ID && m.history.query == "" {
		maxID = max(maxID, m.history.newestID)
	}
	if maxID <= 0 || (!chat.UnreadMark && (maxID <= chat.ReadInboxMaxID || maxID <= m.history.readConfirmed[chat.ID])) || m.history.readPending[chat.ID] > 0 {
		return nil
	}
	if m.history.readPending == nil {
		m.history.readPending = make(map[string]int)
	}
	m.history.readPending[chat.ID] = maxID
	parent := m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		state, err := client.MarkRead(ctx, chat, maxID)
		return readStateMsg{chat.ID, maxID, state, err}
	}
}

func (m Model) updateReadState(msg readStateMsg) (tea.Model, tea.Cmd) {
	if m.history.readPending[msg.chatID] != msg.maxID {
		return m, nil
	}
	delete(m.history.readPending, msg.chatID)
	if msg.err != nil {
		if m.history.readRetryAt == nil {
			m.history.readRetryAt = make(map[string]time.Time)
		}
		delay := retryAfter(msg.err)
		if delay <= 0 {
			delay = 30 * time.Second
		}
		m.history.readRetryAt[msg.chatID] = time.Now().Add(delay)
		if chat, ok := m.currentChat(); ok && chat.ID == msg.chatID {
			m.setFailure("read", msg.err.Error())
		}
		return m, nil
	}
	delete(m.history.readRetryAt, msg.chatID)
	m.clearFailure("read")
	// A dialogs request started before the receipt may return its old unread
	// count afterward. Its response cannot overwrite this newer snapshot.
	if m.dialogsPending {
		m.dialogsRequest++
		m.dialogsPending = false
	}
	if m.history.readConfirmed == nil {
		m.history.readConfirmed = make(map[string]int)
	}
	m.history.readConfirmed[msg.chatID] = max(m.history.readConfirmed[msg.chatID], msg.state.MaxID, msg.maxID)
	apply := func(chats []core.Chat) {
		for i := range chats {
			chat := &chats[i]
			if chat.ID != msg.chatID || chat.ReadInboxMaxID > msg.state.MaxID {
				continue
			}
			chat.ReadInboxMaxID = msg.state.MaxID
			// A dialog refresh may already know about a message that arrived
			// after this receipt snapshot. Preserve that newer unread count.
			if chat.TopMessageID <= msg.state.TopMessageID {
				chat.TopMessageID, chat.Unread = msg.state.TopMessageID, msg.state.Unread
				chat.UnreadMark = msg.state.UnreadMark
			}
		}
	}
	apply(m.chats)
	apply(m.allChats)
	if msg.state.UnreadMark {
		return m, nil
	}
	cmd := m.markVisibleRead()
	return m, cmd
}
