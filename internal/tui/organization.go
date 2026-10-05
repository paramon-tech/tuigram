package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/paramon-tech/tuigram/internal/core"
)

type organizationState struct {
	folders                     []core.ChatFolder
	index, folderID             int // 0 inbox, -1 archive, -2 all, positive Telegram folder ID.
	unreadOnly, naming, pending bool
	name                        string
	target                      core.Chat
	request                     uint64
}
type organizationMsg struct {
	request  uint64
	action   string
	chat     core.Chat
	value    bool
	folderID int
	folder   core.ChatFolder
	folders  []core.ChatFolder
	err      error
}

func (m *Model) openOrganization() tea.Cmd {
	manager, ok := m.client.(core.OrganizationClient)
	if !ok {
		m.setFailure("organization", "Chat organization is unavailable")
		return nil
	}
	m.mode = organizationPanel
	m.failure = ""
	m.organization.naming = false
	m.organization.name = ""
	m.organization.target, _ = m.currentChat()
	m.organization.index = 0
	switch m.organization.folderID {
	case -1:
		m.organization.index = 1
	case -2:
		m.organization.index = 2
	default:
		for i, f := range m.organization.folders {
			if f.ID == m.organization.folderID {
				m.organization.index = i + 3
			}
		}
	}
	m.organization.request++
	m.organization.pending = true
	request, parent := m.organization.request, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		folders, err := manager.Folders(ctx)
		return organizationMsg{request: request, action: "folders", folders: folders, err: err}
	}
}

// installDialogs stores the full dialog set and derives the visible list. A chat
// that stays selected keeps its loaded history and current search.
func (m *Model) installDialogs(chats []core.Chat) {
	m.installOrganizationDialogs(chats, true)
}

func (m *Model) installOrganizationDialogs(chats []core.Chat, preserveReadSelection bool) {
	old, hadOld := m.currentChat()
	m.allChats = append([]core.Chat(nil), chats...)
	m.chats = m.organizationFilter(chats)
	// Do not turn passive unread refreshes into a chain that automatically opens
	// and marks every next conversation read. Keep the just-read selection until
	// the user explicitly changes the filter or switches away.
	if preserveReadSelection && hadOld {
		present := false
		for _, chat := range m.chats {
			if chat.ID == old.ID {
				present = true
				break
			}
		}
		if !present {
			for _, chat := range chats {
				if chat.ID == old.ID && chat.Unread == 0 && !chat.UnreadMark && m.matchesWithoutUnread(chat) {
					m.chats = append(m.chats, chat)
					break
				}
			}
		}
	}
	m.chatIndex = 0
	if hadOld {
		for i, chat := range m.chats {
			if chat.ID == old.ID {
				m.chatIndex = i
				return
			}
		}
	}
	m.messages = nil
	m.history.loaded = false
	m.messageIndex = 0
	m.query = ""
	m.preview = ""
	m.historyRequest++
	m.cancelHistory()
	m.imageRequest++
	m.loading = false
}
func (m Model) matchesWithoutUnread(chat core.Chat) bool {
	switch m.organization.folderID {
	case 0:
		return !chat.Archived
	case -1:
		return chat.Archived
	case -2:
		return true
	}
	for _, folder := range m.organization.folders {
		if folder.ID == m.organization.folderID {
			folder.ExcludeRead = false
			return folder.Contains(chat)
		}
	}
	return false
}

func (m Model) organizationFilter(chats []core.Chat) []core.Chat {
	var folder *core.ChatFolder
	for i := range m.organization.folders {
		if m.organization.folders[i].ID == m.organization.folderID {
			folder = &m.organization.folders[i]
			break
		}
	}
	result := make([]core.Chat, 0, len(chats))
	for _, chat := range chats {
		if m.organization.unreadOnly && chat.Unread == 0 && !chat.UnreadMark {
			continue
		}
		include := false
		switch m.organization.folderID {
		case 0:
			include = !chat.Archived
		case -1:
			include = chat.Archived
		case -2:
			include = true
		default:
			include = folder != nil && folder.Contains(chat)
		}
		if include {
			result = append(result, chat)
		}
	}
	pinned := func(c core.Chat) int {
		if folder != nil {
			for i, id := range folder.PinnedIDs {
				if id == c.ID {
					return i
				}
			}
			return len(folder.PinnedIDs)
		}
		if c.Pinned {
			return 0
		}
		return 1
	}
	sort.SliceStable(result, func(i, j int) bool { return pinned(result[i]) < pinned(result[j]) })
	return result
}
func (m *Model) applyOrganizationFilter() tea.Cmd {
	// A model constructed directly in tests may not have received dialogs yet.
	if m.allChats == nil {
		m.allChats = append([]core.Chat(nil), m.chats...)
	}
	m.installOrganizationDialogs(m.allChats, false)
	m.status = m.organizationLabel()
	if len(m.chats) == 0 {
		m.status += " · no matching conversations"
		return nil
	}
	if m.messages == nil {
		return m.fetchHistory()
	}
	return nil
}
func (m Model) organizationLabel() string {
	label := "Inbox"
	switch m.organization.folderID {
	case -1:
		label = "Archive"
	case -2:
		label = "All chats"
	default:
		for _, f := range m.organization.folders {
			if f.ID == m.organization.folderID {
				label = f.Title
			}
		}
	}
	if m.organization.unreadOnly {
		label += " · unread"
	}
	return label
}
func (m *Model) toggleUnreadFilter() tea.Cmd {
	m.organization.unreadOnly = !m.organization.unreadOnly
	return m.applyOrganizationFilter()
}

func (m Model) updateOrganizationKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.organization.pending {
		return m, nil
	}
	k := key.String()
	if m.organization.naming {
		switch k {
		case "enter":
			cmd := m.organizationCommand("create", true)
			return m, cmd
		case "ctrl+u":
			m.organization.name = ""
		case "backspace", "ctrl+h":
			r := []rune(m.organization.name)
			if len(r) > 0 {
				m.organization.name = string(r[:len(r)-1])
			}
		default:
			var value string
			if key.Type == tea.KeySpace && !key.Alt {
				value = " "
			} else if key.Type == tea.KeyRunes && !key.Alt {
				value = strings.ReplaceAll(Sanitize(string(key.Runes)), "\n", " ")
			}
			r := []rune(value)
			room := max(0, 12-len([]rune(m.organization.name)))
			if len(r) > room {
				r = r[:room]
			}
			m.organization.name += string(r)
		}
		return m, nil
	}
	switch k {
	case "j", "down", "tab":
		m.organization.index = (m.organization.index + 1) % (len(m.organization.folders) + 3)
	case "k", "up", "shift+tab":
		m.organization.index = (m.organization.index + len(m.organization.folders) + 2) % (len(m.organization.folders) + 3)
	case "enter":
		switch m.organization.index {
		case 0:
			m.organization.folderID = 0
		case 1:
			m.organization.folderID = -1
		case 2:
			m.organization.folderID = -2
		default:
			m.organization.folderID = m.organization.folders[m.organization.index-3].ID
		}
		m.mode = normal
		m.focus = 0
		cmd := m.applyOrganizationFilter()
		return m, cmd
	case "u":
		cmd := m.toggleUnreadFilter()
		return m, cmd
	case "p":
		cmd := m.organizationCommand("pin", !m.organization.target.Pinned)
		return m, cmd
	case "a":
		cmd := m.organizationCommand("archive", !m.organization.target.Archived)
		return m, cmd
	case "m":
		cmd := m.organizationCommand("mute", !m.organization.target.Muted)
		return m, cmd
	case "n":
		if m.organization.target.ID == "" {
			m.setFailure("organization", "Select a conversation to include in the new folder")
		} else {
			m.organization.naming = true
			m.organization.name = ""
		}
	case "+", "=":
		cmd := m.organizationCommand("membership", true)
		return m, cmd
	case "-":
		cmd := m.organizationCommand("membership", false)
		return m, cmd
	}
	return m, nil
}
func (m *Model) organizationCommand(action string, value bool) tea.Cmd {
	manager, ok := m.client.(core.OrganizationClient)
	if !ok {
		return nil
	}
	target := m.organization.target
	if target.ID == "" {
		m.setFailure("organization", "Select a conversation first")
		return nil
	}
	folderID := 0
	if action == "membership" {
		if m.organization.index < 3 {
			m.setFailure("organization", "Select a custom folder first")
			return nil
		}
		folder := m.organization.folders[m.organization.index-3]
		if folder.Shared {
			m.setFailure("organization", "Shared folders are view-only here")
			return nil
		}
		folderID = folder.ID
	}
	name := strings.TrimSpace(m.organization.name)
	if action == "create" && name == "" {
		m.setFailure("organization", "Enter a folder name")
		return nil
	}
	m.organization.request++
	m.organization.pending = true
	m.busy = true
	m.failure = ""
	m.status = "Updating chat organization…"
	// An older dialog refresh must not overwrite a successful mutation.
	m.dialogsRequest++
	m.dialogsPending = false
	request, parent := m.organization.request, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		result := organizationMsg{request: request, action: action, chat: target, value: value, folderID: folderID}
		switch action {
		case "pin":
			result.err = manager.SetChatPinned(ctx, target, value)
		case "archive":
			result.err = manager.SetChatArchived(ctx, target, value)
		case "mute":
			result.err = manager.SetChatMuted(ctx, target, value)
		case "create":
			result.folder, result.err = manager.CreateFolder(ctx, name, target)
		case "membership":
			result.err = manager.SetFolderChat(ctx, folderID, target, value)
		}
		return result
	}
}
func (m Model) updateOrganization(result organizationMsg) (tea.Model, tea.Cmd) {
	if result.request != m.organization.request {
		return m, nil
	}
	m.organization.pending = false
	if result.action != "folders" {
		m.busy = false
	}
	if result.err != nil {
		m.setFailure("organization", result.err.Error())
		return m, nil
	}
	m.clearFailure("organization")
	m.organization.folders = append([]core.ChatFolder(nil), m.organization.folders...)
	switch result.action {
	case "folders":
		m.organization.folders = result.folders
		m.organization.index = min(m.organization.index, len(result.folders)+2)
		if m.organization.folderID > 0 {
			found := false
			for _, f := range result.folders {
				if f.ID == m.organization.folderID {
					found = true
				}
			}
			if !found {
				m.organization.folderID = 0
			}
		}
	case "create":
		m.organization.folders = append(m.organization.folders, result.folder)
		m.organization.index = len(m.organization.folders) + 2
		m.organization.naming = false
		m.organization.name = ""
		m.status = "Folder created with " + result.chat.Title
	case "membership":
		for i := range m.organization.folders {
			f := &m.organization.folders[i]
			if f.ID != result.folderID {
				continue
			}
			remove := func(ids []string) []string {
				out := make([]string, 0, len(ids))
				for _, id := range ids {
					if id != result.chat.ID {
						out = append(out, id)
					}
				}
				return out
			}
			f.IncludeIDs = remove(f.IncludeIDs)
			f.ExcludeIDs = remove(f.ExcludeIDs)
			if result.value {
				f.IncludeIDs = append(f.IncludeIDs, result.chat.ID)
			} else {
				f.PinnedIDs = remove(f.PinnedIDs)
				f.ExcludeIDs = append(f.ExcludeIDs, result.chat.ID)
			}
		}
		m.status = "Folder updated"
	default:
		patch := func(chat *core.Chat) {
			if chat.ID != result.chat.ID {
				return
			}
			switch result.action {
			case "pin":
				chat.Pinned = result.value
			case "archive":
				chat.Archived = result.value
				chat.Pinned = false
			case "mute":
				chat.Muted = result.value
			}
		}
		if m.allChats == nil {
			m.allChats = append([]core.Chat(nil), m.chats...)
		}
		for i := range m.allChats {
			patch(&m.allChats[i])
		}
		for i := range m.chats {
			patch(&m.chats[i])
		}
		patch(&m.organization.target)
		m.status = "Chat organization updated"
	}
	if result.action == "folders" {
		if m.allChats != nil {
			m.installDialogs(m.allChats)
		}
		return m, nil
	}
	cmd := m.applyOrganizationFilter()
	return m, cmd
}
func (m Model) organizationText() string {
	o := m.organization
	if o.naming {
		return "New folder (up to 12 characters)\nIncludes: " + Sanitize(o.target.Title) + "\n\n" + Sanitize(o.name) + "▌\n\nEnter create · Esc cancel"
	}
	var lines []string
	if o.target.ID != "" {
		label := Sanitize(o.target.Title)
		var flags []string
		if o.target.Pinned {
			flags = append(flags, "pinned")
		}
		if o.target.Archived {
			flags = append(flags, "archived")
		}
		if o.target.Muted {
			flags = append(flags, "muted")
		}
		if len(flags) > 0 {
			label += " (" + strings.Join(flags, ", ") + ")"
		}
		lines = append(lines, "Selected: "+label)
	}
	lines = append(lines, "p pin/unpin · a archive/unarchive · m mute/unmute", "u unread only: "+fmt.Sprint(o.unreadOnly), "")
	choices := []string{"Inbox", "Archive", "All chats"}
	for _, f := range o.folders {
		label := Sanitize(f.Title)
		if f.Shared {
			label += " (shared)"
		}
		if o.target.ID != "" && f.Contains(o.target) {
			label += " ✓"
		}
		choices = append(choices, label)
	}
	start := max(0, o.index-3)
	end := min(len(choices), start+max(3, m.height-15))
	for i := start; i < end; i++ {
		prefix := "  "
		if i == o.index {
			prefix = "> "
		}
		lines = append(lines, prefix+choices[i])
	}
	if o.pending {
		lines = append(lines, "", "Loading…")
	} else {
		lines = append(lines, "", "↑/↓ select · Enter show · n new folder", "+ add selected chat · - remove · Esc close")
	}
	return strings.Join(lines, "\n")
}
