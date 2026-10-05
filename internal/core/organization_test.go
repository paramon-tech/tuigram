package core

import "testing"

func TestFolderMembershipPrecedenceAndKinds(t *testing.T) {
	f := ChatFolder{Groups: true, Contacts: true, ExcludeMuted: true, ExcludeRead: true, ExcludeArchived: true, IncludeIDs: []string{"explicit"}, PinnedIDs: []string{"pinned"}, ExcludeIDs: []string{"excluded"}}
	for _, tt := range []struct {
		chat Chat
		want bool
	}{
		{Chat{ID: "group", Kind: "group", Unread: 1}, true},
		{Chat{ID: "group", Kind: "group", UnreadMark: true}, true},
		{Chat{ID: "muted", Kind: "group", Unread: 1, Muted: true}, false},
		{Chat{ID: "read", Kind: "group"}, false},
		{Chat{ID: "archive", Kind: "group", Unread: 1, Archived: true}, false},
		{Chat{ID: "contact", Kind: "private", Contact: true, Unread: 1}, true},
		{Chat{ID: "bot", Kind: "private", Contact: true, Bot: true, Unread: 1}, false},
		{Chat{ID: "stranger", Kind: "private", Unread: 1}, false},
		{Chat{ID: "explicit", Muted: true, Archived: true}, true},
		{Chat{ID: "pinned", Muted: true}, true},
		{Chat{ID: "excluded", Kind: "group", Unread: 1}, false},
	} {
		if got := f.Contains(tt.chat); got != tt.want {
			t.Errorf("%s: got %v want %v", tt.chat.ID, got, tt.want)
		}
	}
}
