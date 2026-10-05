package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/paramon-tech/tuigram/internal/core"
)

type reactionPickerState struct {
	chat      core.Chat
	messageID int
	index     int
}

// Telegram enforces each chat's allowed reactions when submitting the choice.
var reactionChoices = []string{"👍", "❤", "🔥", "🥰", "👏", "😁", "🤔", "🤯", "😢", "🎉", "🤩", "👎", ""}

func (m *Model) openReactions() {
	chat, ok := m.currentChat()
	if !ok {
		return
	}
	message, ok := m.currentMessage()
	if !ok {
		return
	}
	m.reaction = reactionPickerState{chat: chat, messageID: message.ID}
	for _, reaction := range message.Reactions {
		if !reaction.Mine {
			continue
		}
		for i, emoji := range reactionChoices {
			if emoji == reaction.Emoji {
				m.reaction.index = i
			}
		}
	}
	m.mode, m.failure = reactionPicker, ""
}

func (m Model) updateReactions(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.busy {
		return m, nil
	}
	switch key.String() {
	case "up", "left", "k", "h", "shift+tab":
		m.reaction.index = (m.reaction.index + len(reactionChoices) - 1) % len(reactionChoices)
	case "down", "right", "j", "l", "tab":
		m.reaction.index = (m.reaction.index + 1) % len(reactionChoices)
	case "backspace", "delete", "0":
		m.reaction.index = len(reactionChoices) - 1
	case "enter":
		choice := reactionChoices[m.reaction.index]
		chat, messageID, client := m.reaction.chat, m.reaction.messageID, m.client
		operation := "Updating reaction"
		if choice == "" {
			operation = "Removing reaction"
		}
		m.mode = normal
		cmd := m.runOperation(operation, chat, func(ctx context.Context) error {
			return client.React(ctx, chat, messageID, choice)
		})
		return m, cmd
	}
	return m, nil
}

func (m Model) reactionsText() string {
	available := max(1, m.height-8)
	var lines []string
	if available >= 4 {
		lines = append(lines, fitLine(fmt.Sprintf("%s · %d/%d", singleLine(m.reaction.chat.Title), m.reaction.index+1, len(reactionChoices)), max(1, m.width-4)))
		available--
	}
	start := min(max(0, m.reaction.index-available/2), max(0, len(reactionChoices)-available))
	for i := start; i < len(reactionChoices) && i < start+available; i++ {
		emoji := reactionChoices[i]
		label := emoji
		if emoji == "" {
			label = "Remove my reaction"
		}
		prefix := "  "
		if i == m.reaction.index {
			prefix = "> "
		}
		lines = append(lines, prefix+label)
	}
	return strings.Join(lines, "\n")
}
