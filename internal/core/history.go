package core

import "context"

// HistoryRequest selects a bounded window of the complete server-side history.
// BeforeID and AfterID are exclusive cursors; zero requests the newest page.
// Oldest starts at the earliest available message (or search result).
type HistoryRequest struct {
	Query    string
	BeforeID int
	AfterID  int
	Oldest   bool
	Limit    int
}

// HistoryPage keeps raw message cursors even when a page contains service or
// deleted messages that the client does not render. Messages are oldest first.
type HistoryPage struct {
	Messages           []Message
	OldestID, NewestID int
	HasOlder, HasNewer bool
}

type HistoryClient interface {
	HistoryPage(context.Context, Chat, HistoryRequest) (HistoryPage, error)
}

// ReadState is an authoritative dialog snapshot taken after a read receipt.
// TopMessageID lets the UI preserve newer unread arrivals while applying it.
type ReadState struct {
	MaxID, Unread, TopMessageID int
	UnreadMark                  bool
}

type ReadClient interface {
	MarkRead(context.Context, Chat, int) (ReadState, error)
}
