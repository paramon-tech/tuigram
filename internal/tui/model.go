// Package tui implements the keyboard-driven Telegram terminal interface.
package tui

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/paramon-tech/tuigram/internal/core"
)

type Cache interface {
	Get(string) ([]byte, error)
	Put(string, []byte) error
}

type Options struct {
	Theme        string
	PollInterval time.Duration
	Cache        Cache
}

type mode int

const (
	normal mode = iota
	compose
	search
	contactSearch
	contactPicker
	forwardPicker
	imagePreview
	messageReader
	help
)

type tickMsg time.Time
type dialogsMsg struct {
	request uint64
	chats   []core.Chat
	err     error
}
type historyMsg struct {
	request  uint64
	chatID   string
	messages []core.Message
	err      error
}
type contactsMsg struct {
	request  uint64
	contacts []core.Contact
	err      error
}
type imageMsg struct {
	request         uint64
	chatID, preview string
	err             error
}
type operationMsg struct {
	operation, chatID string
	err               error
}

// Model is a Bubble Tea model. All network and cache work runs in commands.
type Model struct {
	ctx                                                           context.Context
	client                                                        core.Client
	opts                                                          Options
	width, height                                                 int
	chats                                                         []core.Chat
	messages                                                      []core.Message
	contacts                                                      []core.Contact
	chatIndex, messageIndex, pickerIndex                          int
	focus                                                         int
	mode                                                          mode
	input, query, status, failure, preview                        string
	failureKind                                                   string
	drafts                                                        map[string]string
	viewOffset                                                    int
	busy, loading, pendingG                                       bool
	dialogsPending                                                bool
	dialogsRequest, historyRequest, contactsRequest, imageRequest uint64
	forwardSource                                                 core.Chat
	forwardMessage                                                int
}

func New(ctx context.Context, client core.Client, opts Options) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = 15 * time.Second
	}
	if opts.Theme != "light" && opts.Theme != "dracula" {
		opts.Theme = "midnight"
	}
	return Model{ctx: ctx, client: client, opts: opts, width: 80, height: 24, loading: true,
		status: "Connecting…", dialogsRequest: 1, dialogsPending: true, drafts: make(map[string]string)}
}

func (m Model) Init() tea.Cmd { return tea.Batch(m.dialogsCmd(), m.tickCmd()) }

func (m Model) tickCmd() tea.Cmd {
	if m.opts.PollInterval < 0 {
		return nil
	}
	return tea.Tick(m.opts.PollInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) dialogsCmd() tea.Cmd {
	client, parent, request := m.client, m.ctx, m.dialogsRequest
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		chats, err := client.Dialogs(ctx)
		return dialogsMsg{request, chats, err}
	}
}

func (m *Model) fetchHistory() tea.Cmd {
	chat, ok := m.currentChat()
	if !ok {
		return nil
	}
	m.historyRequest++
	m.loading = true
	client, parent, query, request := m.client, m.ctx, m.query, m.historyRequest
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		messages, err := client.History(ctx, chat, query)
		return historyMsg{request, chat.ID, messages, err}
	}
}

func (m *Model) selectChat(index int) tea.Cmd {
	if len(m.chats) == 0 {
		return nil
	}
	index = max(0, min(index, len(m.chats)-1))
	if m.chatIndex == index && m.messages != nil {
		return nil
	}
	m.chatIndex = index
	m.messages = nil
	m.messageIndex = 0
	m.query, m.failure, m.preview = "", "", ""
	m.imageRequest++
	return m.fetchHistory()
}

func (m Model) currentChat() (core.Chat, bool) {
	if m.chatIndex < 0 || m.chatIndex >= len(m.chats) {
		return core.Chat{}, false
	}
	return m.chats[m.chatIndex], true
}

func (m Model) currentMessage() (core.Message, bool) {
	if m.messageIndex < 0 || m.messageIndex >= len(m.messages) {
		return core.Message{}, false
	}
	return m.messages[m.messageIndex], true
}

func (m *Model) setFailure(kind, text string) {
	m.failureKind, m.failure = kind, text
}

func (m *Model) clearFailure(kind string) {
	if m.failureKind == kind {
		m.failureKind, m.failure = "", ""
	}
}

func (m *Model) runOperation(operation string, chat core.Chat, call func(context.Context) error) tea.Cmd {
	m.busy = true
	m.failure = ""
	m.status = operation + "…"
	parent := m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		return operationMsg{operation, chat.ID, call(ctx)}
	}
}

func (m *Model) searchContacts() tea.Cmd {
	query := strings.TrimSpace(m.input)
	if query == "" {
		m.setFailure("input", "Enter a contact name or public username")
		return nil
	}
	m.contactsRequest++
	request, client, parent := m.contactsRequest, m.client, m.ctx
	m.contacts = nil
	m.pickerIndex = 0
	m.mode = contactPicker
	m.loading = true
	m.status = "Searching contacts…"
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		contacts, err := client.SearchContacts(ctx, query)
		return contactsMsg{request, contacts, err}
	}
}

func (m *Model) loadImage() tea.Cmd {
	chat, ok := m.currentChat()
	if !ok {
		return nil
	}
	message, ok := m.currentMessage()
	if !ok || !message.Image {
		m.failure = "Select a message with an image first"
		return nil
	}
	m.imageRequest++
	m.mode = imagePreview
	m.preview = "Loading image…"
	request, client, parent, cache := m.imageRequest, m.client, m.ctx, m.opts.Cache
	// A backend must explicitly provide an account- and revision-scoped key.
	// Unknown identities and protected/ephemeral media stay in memory only.
	if message.MediaKey == "" {
		cache = nil
	}
	width, height := max(1, m.width-8), max(1, m.height-10)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		key := fmt.Sprintf("image-v2-%x", sha256.Sum256([]byte(fmt.Sprintf("%q:%d:%q", chat.ID, message.ID, message.MediaKey))))
		if cache != nil {
			if data, err := cache.Get(key); err == nil {
				if preview, err := renderImage(data, width, height); err == nil {
					return imageMsg{request, chat.ID, preview, nil}
				}
			}
		}
		data, err := client.DownloadImage(ctx, chat, message.ID)
		if err != nil {
			return imageMsg{request: request, chatID: chat.ID, err: err}
		}
		preview, err := renderImage(data, width, height)
		if err == nil && cache != nil {
			_ = cache.Put(key, data)
		}
		return imageMsg{request, chat.ID, preview, err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
		return m, nil
	case tickMsg:
		if m.ctx.Err() != nil {
			return m, nil
		}
		if m.mode != normal || m.busy || m.loading || m.dialogsPending {
			return m, m.tickCmd()
		}
		m.dialogsRequest++
		m.dialogsPending = true
		return m, tea.Batch(m.dialogsCmd(), m.tickCmd())
	case dialogsMsg:
		if msg.request != m.dialogsRequest {
			return m, nil
		}
		m.dialogsPending = false
		if msg.err != nil {
			m.loading = false
			m.setFailure("dialogs", msg.err.Error())
			return m, nil
		}
		m.clearFailure("dialogs")
		old, hadOld := m.currentChat()
		// Keep search-opened conversations until the server includes them.
		if hadOld {
			found := false
			for _, chat := range msg.chats {
				if chat.ID == old.ID {
					found = true
					break
				}
			}
			if !found {
				msg.chats = append([]core.Chat{old}, msg.chats...)
			}
		}
		m.chats = msg.chats
		m.chatIndex = 0
		if hadOld {
			for i, chat := range m.chats {
				if chat.ID == old.ID {
					m.chatIndex = i
					break
				}
			}
		}
		if len(m.chats) == 0 {
			m.loading = false
			m.status = "No conversations yet · c searches contacts"
			return m, nil
		}
		m.status = fmt.Sprintf("%d conversations", len(m.chats))
		cmd := m.fetchHistory()
		return m, cmd
	case historyMsg:
		chat, ok := m.currentChat()
		if !ok || msg.chatID != chat.ID || msg.request != m.historyRequest {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.setFailure("history", msg.err.Error())
			return m, nil
		}
		m.clearFailure("history")
		selected, hadSelected := m.currentMessage()
		atEnd := m.messageIndex >= len(m.messages)-1
		m.messages = msg.messages
		m.messageIndex = max(0, len(m.messages)-1)
		if hadSelected && !atEnd {
			for i, item := range m.messages {
				if item.ID == selected.ID {
					m.messageIndex = i
					break
				}
			}
		}
		return m, nil
	case contactsMsg:
		if msg.request != m.contactsRequest || m.mode != contactPicker {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.setFailure("contacts", msg.err.Error())
			return m, nil
		}
		m.clearFailure("contacts")
		m.contacts, m.pickerIndex = msg.contacts, 0
		m.status = fmt.Sprintf("%d contacts found", len(msg.contacts))
		return m, nil
	case imageMsg:
		chat, ok := m.currentChat()
		if !ok || msg.chatID != chat.ID || msg.request != m.imageRequest || m.mode != imagePreview {
			return m, nil
		}
		if msg.err != nil {
			m.preview = "Preview unavailable"
			m.setFailure("image", msg.err.Error())
			return m, nil
		}
		m.clearFailure("image")
		m.preview = msg.preview
		return m, nil
	case operationMsg:
		m.busy = false
		if msg.err != nil {
			m.setFailure("operation", msg.err.Error())
			m.status = "Action failed; retry when ready"
			return m, nil
		}
		m.failure = ""
		m.status = msg.operation + " complete"
		if msg.operation == "Sending message" {
			m.input = ""
			delete(m.drafts, msg.chatID)
			m.mode = normal
		}
		if chat, ok := m.currentChat(); ok && chat.ID == msg.chatID {
			cmd := m.fetchHistory()
			return m, cmd
		}
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m Model) updateKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := key.String()
	if k == "ctrl+c" {
		return m, tea.Quit
	}
	if k == "esc" {
		if m.busy && m.mode == compose {
			m.status = "Sending message; wait for the result"
			return m, nil
		}
		m.failure, m.preview = "", ""
		m.contactsRequest++
		m.imageRequest++
		m.pendingG = false
		if m.mode != normal {
			m.mode = normal
			m.loading = false
			return m, nil
		}
		if m.query != "" {
			m.query = ""
			cmd := m.fetchHistory()
			return m, cmd
		}
		return m, nil
	}
	if m.mode == help || m.mode == imagePreview {
		if k == "q" || k == "enter" || k == "?" {
			m.mode = normal
			m.preview = ""
			m.imageRequest++
		}
		return m, nil
	}
	if m.mode == messageReader {
		message, _ := m.currentMessage()
		lineCount := len(m.messageLines(message, max(1, m.width-4), true, colors(m.opts.Theme)))
		switch k {
		case "j", "down":
			m.viewOffset++
		case "k", "up":
			m.viewOffset--
		case "ctrl+d", "pgdown":
			m.viewOffset += max(1, m.height-9)
		case "ctrl+u", "pgup":
			m.viewOffset -= max(1, m.height-9)
		case "g", "home":
			m.viewOffset = 0
		case "G", "end":
			m.viewOffset = lineCount
		case "q", "enter":
			m.mode = normal
		}
		m.viewOffset = max(0, min(m.viewOffset, max(0, lineCount-max(1, m.height-8))))
		return m, nil
	}
	if m.mode == compose || m.mode == search || m.mode == contactSearch {
		return m.updateInput(key)
	}
	if m.mode == contactPicker || m.mode == forwardPicker {
		return m.updatePicker(key)
	}
	if k != "g" {
		m.pendingG = false
	}
	switch k {
	case "q":
		return m, tea.Quit
	case "?":
		m.mode = help
	case "tab":
		m.focus = 1 - m.focus
	case "h", "left":
		m.focus = 0
	case "l", "right", "enter":
		m.focus = 1
	case "j", "down", "k", "up", "g", "G", "home", "end", "pgup", "pgdown", "ctrl+d", "ctrl+u":
		index, count := m.messageIndex, len(m.messages)
		if m.focus == 0 {
			index, count = m.chatIndex, len(m.chats)
		}
		switch k {
		case "j", "down":
			index++
		case "k", "up":
			index--
		case "G", "end":
			index = count - 1
		case "home":
			index = 0
		case "g":
			if !m.pendingG {
				m.pendingG = true
				return m, nil
			}
			m.pendingG = false
			index = 0
		case "pgup", "ctrl+u":
			index -= max(1, (m.height-8)/4)
		case "pgdown", "ctrl+d":
			index += max(1, (m.height-8)/4)
		}
		index = max(0, min(index, count-1))
		if m.focus == 0 {
			cmd := m.selectChat(index)
			return m, cmd
		}
		m.messageIndex = index
	case "i":
		if chat, ok := m.currentChat(); ok {
			m.mode = compose
			m.input = m.drafts[chat.ID]
			m.failure = ""
		}
	case "/":
		if _, ok := m.currentChat(); ok {
			m.mode = search
			m.input = m.query
			m.failure = ""
		}
	case "c":
		m.mode = contactSearch
		m.input, m.failure = "", ""
	case "f":
		if message, ok := m.currentMessage(); ok && !m.busy {
			m.forwardSource, _ = m.currentChat()
			m.forwardMessage = message.ID
			m.mode, m.pickerIndex = forwardPicker, 0
		}
	case "r":
		if message, ok := m.currentMessage(); ok && !m.busy {
			chat, _ := m.currentChat()
			client := m.client
			cmd := m.runOperation("Adding reaction", chat, func(ctx context.Context) error { return client.React(ctx, chat, message.ID, "👍") })
			return m, cmd
		}
	case "v":
		cmd := m.loadImage()
		return m, cmd
	case "y":
		if _, ok := m.currentMessage(); ok {
			m.mode = messageReader
			m.viewOffset = 0
		}
	case "t":
		switch m.opts.Theme {
		case "midnight":
			m.opts.Theme = "light"
		case "light":
			m.opts.Theme = "dracula"
		default:
			m.opts.Theme = "midnight"
		}
		m.status = "Theme: " + m.opts.Theme
	case "R":
		if m.dialogsPending {
			return m, nil
		}
		m.failure = ""
		m.dialogsRequest++
		m.dialogsPending = true
		return m, m.dialogsCmd()
	}
	return m, nil
}

func (m Model) updateInput(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.busy {
		return m, nil
	}
	switch key.String() {
	case "ctrl+s":
		if m.mode != compose {
			return m, nil
		}
		text := strings.TrimSpace(m.input)
		if text == "" {
			m.setFailure("input", "Write a message first")
			return m, nil
		}
		if len([]rune(text)) > 4096 {
			m.setFailure("input", "Messages are limited to 4096 characters")
			return m, nil
		}
		chat, ok := m.currentChat()
		if !ok {
			return m, nil
		}
		client := m.client
		cmd := m.runOperation("Sending message", chat, func(ctx context.Context) error { return client.Send(ctx, chat, text) })
		return m, cmd
	case "enter":
		if m.mode == search {
			m.query = strings.TrimSpace(m.input)
			m.mode = normal
			m.messages = nil
			cmd := m.fetchHistory()
			return m, cmd
		}
		if m.mode == contactSearch {
			cmd := m.searchContacts()
			return m, cmd
		}
		if len([]rune(m.input)) < 4096 {
			m.input += "\n"
		}
	case "backspace", "ctrl+h":
		if r := []rune(m.input); len(r) > 0 {
			m.input = string(r[:len(r)-1])
		}
	case "ctrl+u":
		m.input = ""
	case "space":
		if len([]rune(m.input)) < 4096 {
			m.input += " "
		}
	default:
		if key.Type == tea.KeyRunes {
			value := Sanitize(string(key.Runes))
			if m.mode != compose {
				value = strings.ReplaceAll(value, "\n", " ")
			}
			room := max(0, 4096-len([]rune(m.input)))
			r := []rune(value)
			if len(r) > room {
				r = r[:room]
			}
			m.input += string(r)
		}
	}
	if m.mode == compose {
		if chat, ok := m.currentChat(); ok {
			m.drafts[chat.ID] = m.input
		}
	}
	return m, nil
}

func (m Model) updatePicker(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	count := len(m.contacts)
	if m.mode == forwardPicker {
		count = len(m.chats)
	}
	switch key.String() {
	case "j", "down":
		m.pickerIndex = min(max(0, count-1), m.pickerIndex+1)
	case "k", "up":
		m.pickerIndex = max(0, m.pickerIndex-1)
	case "G", "end":
		m.pickerIndex = max(0, count-1)
	case "g", "home":
		m.pickerIndex = 0
	case "/", "c":
		if m.mode == contactPicker {
			m.mode = contactSearch
			m.input = ""
			m.contactsRequest++
			m.loading = false
		}
	case "q":
		m.mode = normal
		m.contactsRequest++
		m.loading = false
	case "a":
		if m.mode != contactPicker || count == 0 || m.busy {
			return m, nil
		}
		contact := m.contacts[m.pickerIndex]
		client := m.client
		cmd := m.runOperation("Adding contact", core.Chat{}, func(ctx context.Context) error { return client.AddContact(ctx, contact) })
		return m, cmd
	case "enter":
		if count == 0 || m.busy {
			return m, nil
		}
		if m.mode == forwardPicker {
			target := m.chats[m.pickerIndex]
			source, id, client := m.forwardSource, m.forwardMessage, m.client
			m.mode = normal
			cmd := m.runOperation("Forwarding message", source, func(ctx context.Context) error { return client.Forward(ctx, source, id, target) })
			return m, cmd
		}
		contact := m.contacts[m.pickerIndex]
		index := -1
		for i, chat := range m.chats {
			if chat.ID == contact.ID {
				index = i
				break
			}
		}
		if index < 0 {
			m.chats = append(m.chats, core.Chat{ID: contact.ID, Title: contact.Name, Kind: "private"})
			index = len(m.chats) - 1
		}
		m.mode, m.focus = normal, 1
		cmd := m.selectChat(index)
		return m, cmd
	}
	return m, nil
}
