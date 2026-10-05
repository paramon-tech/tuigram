package demo

import (
	"context"
	"fmt"
	"testing"

	"github.com/paramon-tech/tuigram/internal/core"
)

func TestHistoryPagesAndSearchReachBeginning(t *testing.T) {
	c := New()
	c.messages["builders"] = nil
	for i := 1; i <= 250; i++ {
		c.messages["builders"] = append(c.messages["builders"], core.Message{ID: i * 2, Text: fmt.Sprintf("needle %d", i)})
	}
	chat := core.Chat{ID: "builders"}
	page, err := c.HistoryPage(context.Background(), chat, core.HistoryRequest{Query: "needle"})
	if err != nil || len(page.Messages) != 100 || page.OldestID != 302 || !page.HasOlder || page.HasNewer {
		t.Fatalf("latest %+v %v", page, err)
	}
	page, err = c.HistoryPage(context.Background(), chat, core.HistoryRequest{Query: "needle", BeforeID: page.OldestID})
	if err != nil || page.OldestID != 102 || len(page.Messages) != 100 || !page.HasOlder {
		t.Fatalf("middle %+v %v", page, err)
	}
	page, err = c.HistoryPage(context.Background(), chat, core.HistoryRequest{Query: "needle", BeforeID: page.OldestID})
	if err != nil || page.OldestID != 2 || len(page.Messages) != 50 || page.HasOlder {
		t.Fatalf("beginning %+v %v", page, err)
	}
	page, err = c.HistoryPage(context.Background(), chat, core.HistoryRequest{Oldest: true, Limit: 1})
	if err != nil || page.OldestID != 2 || page.HasOlder || !page.HasNewer {
		t.Fatalf("oldest %+v %v", page, err)
	}
	page, err = c.HistoryPage(context.Background(), chat, core.HistoryRequest{AfterID: page.NewestID, Limit: 1})
	if err != nil || page.OldestID != 4 {
		t.Fatalf("newer %+v %v", page, err)
	}
}

func TestReadReceiptLeavesMessagesAboveBoundaryUnread(t *testing.T) {
	c := New()
	chat := core.Chat{ID: "builders"}
	state, err := c.MarkRead(context.Background(), chat, 5)
	if err != nil || state.Unread != 1 || state.TopMessageID != 7 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	state, err = c.MarkRead(context.Background(), chat, 4)
	if err != nil || state.MaxID != 5 || state.Unread != 1 {
		t.Fatalf("read boundary regressed: %+v %v", state, err)
	}
	state, err = c.MarkRead(context.Background(), chat, 7)
	if err != nil || state.Unread != 0 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	chats, err := c.Dialogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, chat := range chats {
		if chat.ID == "builders" && chat.Unread != 0 {
			t.Fatalf("dialogs not updated: %+v", chat)
		}
	}
}
