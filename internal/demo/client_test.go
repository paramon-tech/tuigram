package demo

import (
	"bytes"
	"context"
	"image/png"
	"sync"
	"testing"

	"github.com/paramon-tech/tuigram/internal/core"
)

// This account-free functional scenario also defines the backend contract.
func TestMessagingJourney(t *testing.T) {
	ctx := context.Background()
	c := New()
	chats, err := c.Dialogs(ctx)
	if err != nil || len(chats) != 3 {
		t.Fatalf("dialogs: %v %v", chats, err)
	}
	if err := c.Send(ctx, chats[1], "a searchable message 🦊"); err != nil {
		t.Fatal(err)
	}
	ms, err := c.History(ctx, chats[1], "SEARCHABLE")
	if err != nil || len(ms) != 1 {
		t.Fatalf("search: %v %v", ms, err)
	}
	if err := c.React(ctx, chats[1], ms[0].ID, "👍"); err != nil {
		t.Fatal(err)
	}
	if err := c.Forward(ctx, chats[1], ms[0].ID, chats[0]); err != nil {
		t.Fatal(err)
	}
	forwarded, _ := c.History(ctx, chats[0], "searchable")
	if len(forwarded) != 1 || !forwarded[0].Forwarded {
		t.Fatalf("forward: %+v", forwarded)
	}
	contacts, err := c.SearchContacts(ctx, "@ada")
	if err != nil || len(contacts) != 1 {
		t.Fatalf("contacts: %v %v", contacts, err)
	}
	if err := c.AddContact(ctx, contacts[0]); err != nil {
		t.Fatal(err)
	}
	if err := c.Send(ctx, core.Chat{ID: contacts[0].ID}, "Hello Ada"); err != nil {
		t.Fatal(err)
	}
	data, err := c.DownloadImage(ctx, chats[0], 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentClientAndSnapshotIsolation(t *testing.T) {
	c := New()
	ctx := context.Background()
	chat := core.Chat{ID: "saved"}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			if err := c.Send(ctx, chat, "hi"); err != nil {
				t.Error(err)
			}
			_, _ = c.History(ctx, chat, "")
		})
	}
	wg.Wait()
	ms, _ := c.History(ctx, chat, "")
	if len(ms) != 23 {
		t.Fatalf("lost writes: %d", len(ms))
	}
	ms[1].Reactions[0].Count = 999
	next, _ := c.History(ctx, chat, "")
	if next[1].Reactions[0].Count == 999 {
		t.Fatal("returned shared mutable reaction")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.Dialogs(canceled); err == nil {
		t.Fatal("ignored cancellation")
	}
}
