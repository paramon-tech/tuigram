package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/paramon-tech/tuigram/internal/core"
)

type palette struct{ text, muted, accent, border, selected, danger, background lipgloss.Color }

func colors(theme string) palette {
	switch theme {
	case "light":
		return palette{"#253044", "#526078", "#006B77", "#BBC5D5", "#DDEAF1", "#A32939", "#F6F8FC"}
	case "dracula":
		return palette{"#F8F8F2", "#A7A8C2", "#BD93F9", "#6272A4", "#44475A", "#FF5555", "#282A36"}
	default:
		return palette{"#DBE5F4", "#91A1B9", "#62D4CB", "#344257", "#243D50", "#FF8991", "#101823"}
	}
}

// Snapshot renders one bounded, synchronous read for screenshots and smoke tests.
// It does not start a terminal or schedule background polling.
func Snapshot(ctx context.Context, client core.Client, opts Options, width, height int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	m := New(ctx, client, opts)
	m.width, m.height = max(1, width), max(1, height)
	chats, err := client.Dialogs(ctx)
	if err != nil {
		return "", err
	}
	m.chats = chats
	if len(chats) > 0 {
		m.messages, err = client.History(ctx, chats[0], "")
		if err != nil {
			return "", err
		}
		m.messageIndex = max(0, len(m.messages)-1)
	}
	m.loading = false
	m.dialogsPending = false
	m.status = fmt.Sprintf("%d conversations", len(chats))
	return m.View(), nil
}

func (m Model) View() string {
	p := colors(m.opts.Theme)
	if m.width < 24 || m.height < 8 {
		return paintCanvas("tuigram\nResize to at least 24 × 8\nctrl+c quit", m.width, m.height, p)
	}
	header := lipgloss.NewStyle().Foreground(p.accent).Bold(true).Render(" TUIGRAM") +
		lipgloss.NewStyle().Foreground(p.muted).Render("  Telegram, at your fingertips")
	if m.width >= 76 {
		header += lipgloss.NewStyle().Foreground(p.muted).Render("   " + m.opts.Theme + " · ? help")
	}
	header = fitLine(header, m.width)
	bodyHeight := m.height - 5
	var body string
	switch m.mode {
	case help:
		body = m.helpView(m.width, bodyHeight, p)
	case imagePreview:
		body = m.panel("Image preview · esc close", m.preview, m.width, bodyHeight, true, p)
	case messageReader:
		message, _ := m.currentMessage()
		lines := m.messageLines(message, max(1, m.width-4), true, p)
		offset := min(m.viewOffset, max(0, len(lines)-1))
		body = m.panel("Read message · j/k scroll · esc close", strings.Join(lines[offset:], "\n"), m.width, bodyHeight, true, p)
	case contactSearch:
		body = m.panel("Find a person", "Search existing contacts or a public @username.\n\n"+Sanitize(m.input)+"▌\n\nenter search · esc cancel", m.width, bodyHeight, true, p)
	case contactPicker, forwardPicker:
		body = m.pickerView(m.width, bodyHeight, p)
	case compose, search:
		body = m.inputView(m.width, bodyHeight, p)
	default:
		if m.width < 72 {
			if m.focus == 0 {
				body = m.chatsView(m.width, bodyHeight, p)
			} else {
				body = m.messagesView(m.width, bodyHeight, p)
			}
		} else {
			left := min(34, max(24, m.width/3))
			body = lipgloss.JoinHorizontal(lipgloss.Top, m.chatsView(left, bodyHeight, p), m.messagesView(m.width-left, bodyHeight, p))
		}
	}
	status := m.status
	style := lipgloss.NewStyle().Foreground(p.muted)
	if (m.loading || m.dialogsPending) && m.mode == normal {
		status = "Loading…  " + status
	}
	if m.failure != "" {
		status = "! " + singleLine(m.failure)
		style = style.Foreground(p.danger)
	}
	if m.query != "" && m.failure == "" {
		status += " · search: " + singleLine(m.query) + " · esc clears"
	}
	status = style.Render(fitLine(" "+status, m.width))
	footer := m.footer()
	return paintCanvas(header+"\n"+body+"\n"+status+"\n"+lipgloss.NewStyle().Foreground(p.accent).Render(fitLine(" "+footer, m.width))+"\n", m.width, m.height, p)
}

// paintCanvas restores the theme after nested styles reset their attributes.
// Explicit image RGB and selected-row backgrounds remain intact until reset.
func paintCanvas(content string, width, height int, p palette) string {
	base := lipgloss.NewStyle().Foreground(p.text).Background(p.background)
	restore := strings.TrimSuffix(base.Render(""), "\x1b[0m")
	content = fitBlock(content, width, height)
	content = strings.ReplaceAll(content, "\x1b[0m", "\x1b[0m"+restore)
	return base.Render(content)
}

func (m Model) footer() string {
	switch m.mode {
	case compose:
		return "ctrl+s send · enter newline · esc cancel"
	case search, contactSearch:
		return "enter search · ctrl+u clear · esc cancel"
	case contactPicker:
		return "j/k move · enter chat · a add · / search · esc close"
	case forwardPicker:
		return "j/k move · enter forward · esc cancel"
	case imagePreview, help:
		return "esc close · ctrl+c quit"
	case messageReader:
		return "j/k scroll · ctrl+u/d page · g/G edges · esc close"
	default:
		if m.width < 72 {
			return "tab panes · j/k move · i write · ? help"
		}
		return "j/k move · tab panes · i write · / search · c contacts · f forward · ? help"
	}
}

func (m Model) panel(title, content string, width, height int, active bool, p palette) string {
	border := p.border
	if active {
		border = p.accent
	}
	innerWidth, innerHeight := max(1, width-4), max(1, height-3)
	titleStyle := lipgloss.NewStyle().Foreground(border).Bold(true)
	inside := titleStyle.Render(fitLine(singleLine(title), innerWidth)) + "\n" + fitBlock(content, innerWidth, innerHeight)
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(0, 1).Width(max(1, width-2)).Height(max(1, height-2)).MaxWidth(width).MaxHeight(height).Render(inside)
}

func (m Model) chatsView(width, height int, p palette) string {
	available := max(1, height-3)
	innerWidth := max(1, width-4)
	start := max(0, m.chatIndex-available/2)
	start = min(start, max(0, len(m.chats)-available))
	var lines []string
	for i := start; i < len(m.chats) && len(lines) < available; i++ {
		chat := m.chats[i]
		prefix := "  "
		if i == m.chatIndex {
			prefix = "› "
		}
		kind := "·"
		switch chat.Kind {
		case "group":
			kind = "#"
		case "channel":
			kind = "◈"
		}
		label := prefix + kind + " " + singleLine(chat.Title)
		if chat.Unread > 0 {
			label += fmt.Sprintf(" (%d)", chat.Unread)
		}
		line := fitLine(label, innerWidth)
		style := lipgloss.NewStyle().Foreground(p.text)
		if i == m.chatIndex {
			style = style.Foreground(p.accent).Background(p.selected).Bold(true)
		}
		lines = append(lines, style.Render(line))
	}
	if len(lines) == 0 {
		lines = []string{"No conversations yet.", "Press c to find contacts."}
		if m.loading {
			lines = []string{"Connecting to Telegram…"}
		}
	}
	return m.panel(fmt.Sprintf("Conversations · %d", len(m.chats)), strings.Join(lines, "\n"), width, height, m.focus == 0, p)
}

func (m Model) messagesView(width, height int, p palette) string {
	chat, ok := m.currentChat()
	if !ok {
		return m.panel("Messages", "Welcome to tuigram.\n\nSelect a conversation or press c to find someone.\nPress ? for keyboard shortcuts.", width, height, m.focus == 1, p)
	}
	available, innerWidth := max(1, height-3), max(1, width-4)
	if len(m.messages) == 0 {
		text := "No messages yet. Press i to write."
		if m.loading {
			text = "Loading messages…"
		} else if m.query != "" {
			text = "No messages match this search. Esc clears it."
		}
		return m.panel(chat.Title, text, width, height, m.focus == 1, p)
	}
	// Anchor the viewport on the selected message, then use remaining space
	// for context. Even a very long message cannot push selection offscreen.
	blocks := make([][]string, len(m.messages))
	for i, message := range m.messages {
		blocks[i] = m.messageLines(message, innerWidth, i == m.messageIndex, p)
	}
	selected := max(0, min(m.messageIndex, len(blocks)-1))
	start, end, used := selected, selected+1, len(blocks[selected])
	for start > 0 && used+len(blocks[start-1]) <= available {
		start--
		used += len(blocks[start])
	}
	for end < len(blocks) && used+len(blocks[end]) <= available {
		used += len(blocks[end])
		end++
	}
	var lines []string
	for i := start; i < end; i++ {
		lines = append(lines, blocks[i]...)
	}
	if len(lines) > available {
		lines = lines[:available]
		if available > 1 {
			lines[available-1] = lipgloss.NewStyle().Foreground(p.muted).Render("… press y to read full message")
		}
	}
	return m.panel(chat.Title+fmt.Sprintf(" · %d/%d", selected+1, len(m.messages)), strings.Join(lines, "\n"), width, height, m.focus == 1, p)
}

func (m Model) messageLines(message core.Message, width int, selected bool, p palette) []string {
	prefix := "  "
	if selected {
		prefix = "› "
	}
	sender := singleLine(message.Sender)
	if message.Outgoing {
		sender = "You"
	}
	stamp := message.Time.Format("15:04")
	if message.Time.IsZero() {
		stamp = "--:--"
	}
	heading := prefix + sender + " · " + stamp
	if message.Forwarded {
		heading += " · forwarded"
	}
	color := p.muted
	if selected {
		color = p.accent
	}
	lines := []string{lipgloss.NewStyle().Foreground(color).Bold(selected).Render(fitLine(heading, width))}
	text := Sanitize(message.Text)
	if message.Image {
		text += "\n▧ " + singleLine(message.MediaLabel) + " · v preview"
	} else if message.MediaLabel != "" {
		text += "\n[" + singleLine(message.MediaLabel) + "]"
	}
	if text != "" {
		lines = append(lines, wrapText(text, width)...)
	}
	if len(message.Reactions) > 0 {
		var reactions []string
		for _, reaction := range message.Reactions {
			reactions = append(reactions, fmt.Sprintf("%s %d", singleLine(reaction.Emoji), reaction.Count))
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(p.accent).Render(fitLine(strings.Join(reactions, "  "), width)))
	}
	return append(lines, "")
}

func (m Model) pickerView(width, height int, p palette) string {
	title := "Contacts · enter opens chat · a adds contact"
	count := len(m.contacts)
	if m.mode == forwardPicker {
		title = "Forward message · select destination"
		count = len(m.chats)
	}
	available := max(1, height-3)
	start := min(max(0, m.pickerIndex-available/2), max(0, count-available))
	var lines []string
	for i := start; i < count && len(lines) < available; i++ {
		var label string
		if m.mode == forwardPicker {
			label = singleLine(m.chats[i].Title)
		} else {
			contact := m.contacts[i]
			label = singleLine(contact.Name)
			if contact.Username != "" {
				label += "  @" + singleLine(strings.TrimPrefix(contact.Username, "@"))
			}
		}
		prefix := "  "
		style := lipgloss.NewStyle().Foreground(p.text)
		if i == m.pickerIndex {
			prefix = "› "
			style = style.Foreground(p.accent).Background(p.selected)
		}
		lines = append(lines, style.Render(fitLine(prefix+label, max(1, width-4))))
	}
	if len(lines) == 0 {
		if m.loading {
			lines = []string{"Searching…"}
		} else {
			lines = []string{"No results. Press / to try another name or @username."}
		}
	}
	return m.panel(title, strings.Join(lines, "\n"), width, height, true, p)
}

func (m Model) inputView(width, height int, p palette) string {
	chat, _ := m.currentChat()
	title := "Write to " + singleLine(chat.Title)
	if m.mode == search {
		title = "Search messages in " + singleLine(chat.Title)
	}
	lines := wrapText(Sanitize(m.input)+"▌", max(1, width-4))
	available := max(1, height-4)
	if len(lines) > available {
		lines = lines[len(lines)-available:]
	}
	if m.mode == compose {
		lines = append(lines, lipgloss.NewStyle().Foreground(p.muted).Render(fmt.Sprintf("%d / 4096 characters", len([]rune(m.input)))))
	}
	return m.panel(title, strings.Join(lines, "\n"), width, height, true, p)
}

func (m Model) helpView(width, height int, p palette) string {
	text := "NAVIGATE  j/k or ↑/↓ move · h/l or tab switch panes\n" +
		"          gg first · G last · ctrl+u/d page · enter open\n" +
		"WRITE     i compose · ctrl+s send · enter newline\n" +
		"SEARCH    / messages · c contacts / public @username\n" +
		"CONTACTS  enter open private chat · a add contact\n" +
		"MESSAGE   y read full text · f forward · r react 👍 · v image\n" +
		"DISPLAY   t theme · R refresh · ? help · esc back\n" +
		"EXIT      q or ctrl+c\n\n" +
		"Emoji and URLs appear as text. Image previews support PNG, JPEG\n" +
		"and GIF (first frame) in true color. URLs never open automatically.\n" +
		"Search and chat histories are bounded by the Telegram backend."
	return m.panel("Keyboard shortcuts", strings.Join(wrapText(text, max(1, width-4)), "\n"), width, height, true, p)
}

func fitLine(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return lipgloss.NewStyle().Width(width).MaxWidth(width).MaxHeight(1).Render(s)
}

func fitBlock(s string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	return lipgloss.NewStyle().Width(width).Height(height).MaxWidth(width).MaxHeight(height).Render(s)
}

func wrapText(s string, width int) []string {
	return strings.Split(lipgloss.NewStyle().Width(max(1, width)).Render(s), "\n")
}
