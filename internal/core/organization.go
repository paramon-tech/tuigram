package core

import "context"

// ChatFolder preserves Telegram's explicit membership and automatic rules.
type ChatFolder struct {
	ID                                            int
	Title                                         string
	Shared                                        bool
	PinnedIDs, IncludeIDs, ExcludeIDs             []string
	Contacts, NonContacts, Groups, Channels, Bots bool
	ExcludeMuted, ExcludeRead, ExcludeArchived    bool
}

func (f ChatFolder) Contains(chat Chat) bool {
	contains := func(ids []string) bool {
		for _, id := range ids {
			if id == chat.ID {
				return true
			}
		}
		return false
	}
	if contains(f.ExcludeIDs) {
		return false
	}
	// Explicit inclusions override exclusion rules, as in Telegram clients.
	if contains(f.PinnedIDs) || contains(f.IncludeIDs) {
		return true
	}
	if f.ExcludeMuted && chat.Muted || f.ExcludeRead && chat.Unread == 0 && !chat.UnreadMark || f.ExcludeArchived && chat.Archived {
		return false
	}
	switch {
	case chat.Bot:
		return f.Bots
	case chat.Kind == "private":
		return chat.Contact && f.Contacts || !chat.Contact && f.NonContacts
	case chat.Kind == "group":
		return f.Groups
	case chat.Kind == "channel":
		return f.Channels
	}
	return false
}

type OrganizationClient interface {
	Folders(context.Context) ([]ChatFolder, error)
	SetChatPinned(context.Context, Chat, bool) error
	SetChatArchived(context.Context, Chat, bool) error
	SetChatMuted(context.Context, Chat, bool) error
	CreateFolder(context.Context, string, Chat) (ChatFolder, error)
	SetFolderChat(context.Context, int, Chat, bool) error
}
