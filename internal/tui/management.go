package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/paramon-tech/tuigram/internal/core"
)

type actionForm struct {
	kind, title    string
	labels, values []string
	index          int
	chat           core.Chat
	contact        core.Contact
	members        []core.Contact
}

type pendingAction struct {
	kind, prompt string
	chat         core.Chat
	contact      core.Contact
}

type managementMsg struct {
	kind    string
	chat    core.Chat
	contact core.Contact
	err     error
}

func (m *Model) editChat() {
	chat, ok := m.currentChat()
	if !ok {
		return
	}
	if chat.Kind == "private" {
		m.setFailure("management", "Rename a person in contacts: c, search, then e")
		return
	}
	m.form = actionForm{kind: "renameChat", title: "Rename " + chat.Title, labels: []string{"Title"}, values: []string{chat.Title}, chat: chat}
	m.mode, m.failure = manageForm, ""
}

func (m *Model) confirmChatDeletion() {
	chat, ok := m.currentChat()
	if !ok {
		return
	}
	prompt := "Delete your history with " + chat.Title + "? This cannot be undone. The other person's history is kept."
	if chat.Kind != "private" {
		prompt = "Leave " + chat.Title + "? You may need an invitation to rejoin. This does not delete the group/channel for others."
	}
	m.confirmation = pendingAction{kind: "deleteChat", prompt: prompt, chat: chat}
	m.mode, m.failure = confirmAction, ""
}

func (m *Model) managePickerKey(key tea.KeyMsg) (bool, tea.Cmd) {
	if m.mode != contactPicker {
		return false, nil
	}
	k := key.String()
	if m.selectingGroup {
		switch k {
		case " ", "space":
			if len(m.contacts) > 0 {
				ct := m.contacts[m.pickerIndex]
				if _, found := m.groupMembers[ct.ID]; found {
					delete(m.groupMembers, ct.ID)
				} else {
					m.groupMembers[ct.ID] = ct
				}
			}
			return true, nil
		case "enter":
			if len(m.groupMembers) == 0 {
				m.setFailure("input", "Select at least one person with Space")
				return true, nil
			}
			members := make([]core.Contact, 0, len(m.groupMembers))
			for _, ct := range m.groupMembers {
				members = append(members, ct)
			}
			m.form = actionForm{kind: "createGroup", title: "Create group", labels: []string{"Title"}, values: []string{""}, members: members}
			m.mode, m.failure = manageForm, ""
			return true, nil
		case "a", "e", "D", "n":
			return true, nil
		}
		return false, nil
	}
	switch k {
	case "n":
		m.form = actionForm{kind: "importContact", title: "Add contact by phone", labels: []string{"International phone (+country code)", "Name"}, values: []string{"", ""}}
		m.mode, m.failure = manageForm, ""
		return true, nil
	case "e", "D":
		if len(m.contacts) == 0 {
			return true, nil
		}
		ct := m.contacts[m.pickerIndex]
		if k == "e" {
			m.form = actionForm{kind: "editContact", title: "Edit contact", labels: []string{"Name"}, values: []string{ct.Name}, contact: ct}
			m.mode = manageForm
		} else {
			m.confirmation = pendingAction{kind: "deleteContact", prompt: "Remove " + ct.Name + " from your Telegram contacts? Messages will be kept.", contact: ct}
			m.mode = confirmAction
		}
		m.failure = ""
		return true, nil
	}
	return false, nil
}

func (m Model) updateForm(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.busy {
		return m, nil
	}
	if handled := m.attachmentFormKey(key); handled {
		return m, nil
	}
	// Copy the slice before editing a value, keeping earlier model snapshots intact.
	m.form.values = append([]string(nil), m.form.values...)
	switch key.String() {
	case "tab", "down":
		m.form.index = (m.form.index + 1) % len(m.form.values)
	case "shift+tab", "up":
		m.form.index = (m.form.index + len(m.form.values) - 1) % len(m.form.values)
	case "enter":
		if m.form.index < len(m.form.values)-1 {
			m.form.index++
			return m, nil
		}
		cmd := m.submitForm()
		return m, cmd
	case "ctrl+s":
		cmd := m.submitForm()
		return m, cmd
	case "ctrl+u":
		m.form.values[m.form.index] = ""
	case "backspace", "ctrl+h":
		r := []rune(m.form.values[m.form.index])
		if len(r) > 0 {
			m.form.values[m.form.index] = string(r[:len(r)-1])
		}
	default:
		value := ""
		if key.Type == tea.KeySpace && !key.Alt {
			value = " "
		} else if key.Type == tea.KeyRunes && !key.Alt {
			value = strings.ReplaceAll(Sanitize(string(key.Runes)), "\n", " ")
		}
		r := []rune(value)
		limit := 256
		if m.form.kind == "attachment" {
			limit = 4096
			if m.form.index == len(m.form.values)-1 {
				limit = 1024
			}
		}
		room := max(0, limit-len([]rune(m.form.values[m.form.index])))
		if len(r) > room {
			r = r[:room]
		}
		m.form.values[m.form.index] += string(r)
	}
	return m, nil
}

func (m *Model) submitForm() tea.Cmd {
	f := m.form
	if f.kind == "attachment" {
		return m.sendAttachment()
	}
	for i, v := range f.values {
		if strings.TrimSpace(v) == "" {
			m.form.index = i
			m.setFailure("input", "Enter "+strings.ToLower(f.labels[i]))
			return nil
		}
	}
	manager, ok := m.client.(core.ManagementClient)
	if !ok && f.kind != "importContact" {
		m.setFailure("management", "Chat and contact management is unavailable")
		return nil
	}
	return m.managementCommand(f.kind, func(ctx context.Context) managementMsg {
		result := managementMsg{kind: f.kind, chat: f.chat, contact: f.contact}
		name := strings.TrimSpace(f.values[0])
		switch f.kind {
		case "renameChat":
			result.err = manager.RenameChat(ctx, f.chat, name)
			result.chat.Title = name
		case "createGroup":
			result.chat, result.err = manager.CreateGroup(ctx, name, f.members)
		case "editContact":
			result.err = manager.EditContact(ctx, f.contact, name)
			result.contact.Name = name
		case "importContact":
			importer, ok := m.client.(core.ContactImporter)
			if !ok {
				result.err = errors.New("adding contacts by phone is unavailable")
				break
			}
			result.contact, result.err = importer.ImportContact(ctx, name, strings.TrimSpace(f.values[1]))
		}
		return result
	})
}

func (m Model) updateConfirmation(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.busy {
		return m, nil
	}
	if key.String() == "n" || key.String() == "q" {
		m.mode = normal
		return m, nil
	}
	if key.String() != "y" {
		return m, nil
	}
	action := m.confirmation
	manager, ok := m.client.(core.ManagementClient)
	if !ok {
		m.setFailure("management", "Chat and contact management is unavailable")
		return m, nil
	}
	cmd := m.managementCommand(action.kind, func(ctx context.Context) managementMsg {
		result := managementMsg{kind: action.kind, chat: action.chat, contact: action.contact}
		if action.kind == "deleteChat" {
			result.err = manager.DeleteChat(ctx, action.chat)
		} else {
			result.err = manager.DeleteContact(ctx, action.contact)
		}
		return result
	})
	return m, cmd
}

func (m *Model) managementCommand(kind string, call func(context.Context) managementMsg) tea.Cmd {
	m.busy = true
	m.loading = false
	m.dialogsRequest++
	m.dialogsPending = false
	m.historyRequest++
	m.contactsRequest++
	m.failure = ""
	m.status = "Updating Telegram…"
	parent := m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		return call(ctx)
	}
}

func (m Model) updateManagement(result managementMsg) (tea.Model, tea.Cmd) {
	m.busy = false
	if result.err != nil && !(result.kind == "createGroup" && result.chat.ID != "") {
		m.setFailure("management", result.err.Error())
		m.status = "Action failed; retry when ready"
		return m, nil
	}
	m.failure = ""
	m.mode = normal
	m.selectingGroup = false
	if m.allChats == nil {
		m.allChats = append([]core.Chat(nil), m.chats...)
	}
	switch result.kind {
	case "renameChat":
		for i := range m.allChats {
			if m.allChats[i].ID == result.chat.ID {
				m.allChats[i].Title = result.chat.Title
			}
		}
		for i, c := range m.chats {
			if c.ID == result.chat.ID {
				m.chats[i].Title = result.chat.Title
			}
		}
		m.status = "Chat renamed"
	case "createGroup":
		m.allChats = append(m.allChats, result.chat)
		m.organization.folderID, m.organization.unreadOnly = 0, false
		m.chats = m.organizationFilter(m.allChats)
		m.messages = nil
		m.history.loaded = false
		m.focus = 1
		m.status = "Group created"
		index := 0
		for i, chat := range m.chats {
			if chat.ID == result.chat.ID {
				index = i
				break
			}
		}
		cmd := m.selectChat(index)
		if result.err != nil {
			m.setFailure("management", result.err.Error())
		}
		return m, cmd
	case "deleteChat":
		all := make([]core.Chat, 0, len(m.allChats))
		for _, chat := range m.allChats {
			if chat.ID != result.chat.ID {
				all = append(all, chat)
			}
		}
		m.allChats = all
		chats := make([]core.Chat, 0, len(m.chats))
		for _, c := range m.chats {
			if c.ID != result.chat.ID {
				chats = append(chats, c)
			}
		}
		m.chats = chats
		delete(m.drafts, result.chat.ID)
		m.chatIndex = min(m.chatIndex, max(0, len(chats)-1))
		m.messages = nil
		m.history.loaded = false
		m.messageIndex = 0
		m.query = ""
		m.status = "Chat deleted / left"
		cmd := m.fetchHistory()
		return m, cmd
	case "editContact":
		for i := range m.allChats {
			if m.allChats[i].ID == result.contact.ID {
				m.allChats[i].Title = result.contact.Name
				m.allChats[i].Contact = true
			}
		}
		for i, c := range m.contacts {
			if c.ID == result.contact.ID {
				m.contacts[i] = result.contact
			}
		}
		for i, c := range m.chats {
			if c.ID == result.contact.ID {
				m.chats[i].Title = result.contact.Name
				m.chats[i].Contact = true
			}
		}
		m.mode = contactPicker
		m.status = "Contact updated"
	case "deleteContact":
		for i := range m.allChats {
			if m.allChats[i].ID == result.contact.ID {
				m.allChats[i].Contact = false
			}
		}
		for i := range m.chats {
			if m.chats[i].ID == result.contact.ID {
				m.chats[i].Contact = false
			}
		}
		contacts := make([]core.Contact, 0, len(m.contacts))
		for _, c := range m.contacts {
			if c.ID != result.contact.ID {
				contacts = append(contacts, c)
			}
		}
		m.contacts = contacts
		m.pickerIndex = min(m.pickerIndex, max(0, len(contacts)-1))
		m.mode = contactPicker
		m.status = "Contact removed; chat history kept"
	case "importContact":
		for i := range m.allChats {
			if m.allChats[i].ID == result.contact.ID {
				m.allChats[i].Contact = true
				m.allChats[i].Title = result.contact.Name
			}
		}
		for i := range m.chats {
			if m.chats[i].ID == result.contact.ID {
				m.chats[i].Contact = true
				m.chats[i].Title = result.contact.Name
			}
		}
		m.contacts = []core.Contact{result.contact}
		m.pickerIndex = 0
		m.mode = contactPicker
		m.status = "Contact added"
	}
	return m, nil
}

func (m Model) formText() string {
	var lines []string
	selectedEnd := 0
	for i, label := range m.form.labels {
		value := Sanitize(m.form.values[i])
		if i == m.form.index {
			value += "▌"
		}
		if m.form.kind == "attachment" {
			lines = append(lines, wrapText(label+":\n"+value, max(1, m.width-4))...)
			if i == m.form.index {
				selectedEnd = len(lines) - 1
			}
			lines = append(lines, "")
		} else {
			lines = append(lines, label+":\n"+value)
		}
	}
	if m.form.kind == "attachment" {
		available := max(1, m.height-11)
		start := max(0, selectedEnd-available+1)
		return strings.Join(lines[start:min(len(lines), start+available)], "\n") + "\n\nTab next field · Ctrl+s send · Esc cancel"
	}
	return strings.Join(lines, "\n\n") + "\n\nTab next field · Ctrl+u clear · Enter save · Esc cancel"
}
