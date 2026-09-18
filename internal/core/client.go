// Package core defines the transport-independent contract used by the TUI.
package core

import (
	"context"
	"time"
)

type Chat struct {
	ID     string
	Title  string
	Kind   string // private, group, or channel
	Unread int
}

type Reaction struct {
	Emoji string
	Count int
}

type Message struct {
	ID         int
	ChatID     string
	Sender     string
	Text       string
	Time       time.Time
	Outgoing   bool
	Forwarded  bool
	Reactions  []Reaction
	Image      bool
	MediaLabel string
	// MediaKey identifies cacheable media within an authenticated account and
	// revision. An empty key forbids disk caching (unknown or protected media).
	MediaKey string
}

type Contact struct {
	ID       string
	Name     string
	Username string
}

// Client implementations must support concurrent calls and respect cancellation.
// Histories are returned oldest first. Implementations limit results and downloads.
type Client interface {
	Dialogs(context.Context) ([]Chat, error)
	History(context.Context, Chat, string) ([]Message, error)
	Send(context.Context, Chat, string) error
	Forward(context.Context, Chat, int, Chat) error
	React(context.Context, Chat, int, string) error
	SearchContacts(context.Context, string) ([]Contact, error)
	AddContact(context.Context, Contact) error
	DownloadImage(context.Context, Chat, int) ([]byte, error)
}
