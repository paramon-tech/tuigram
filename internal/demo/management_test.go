package demo

import (
	"context"
	"errors"
	"testing"

	"github.com/paramon-tech/tuigram/internal/core"
)

func TestManagementJourney(t *testing.T) {
	ctx := context.Background()
	c := New()
	contacts, err := c.SearchContacts(ctx, "")
	if err != nil || len(contacts) != 2 {
		t.Fatalf("browse contacts: %+v, %v", contacts, err)
	}
	group, err := c.CreateGroup(ctx, "Weekend plans", contacts)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Send(ctx, group, "Meet at noon"); err != nil {
		t.Fatal(err)
	}
	if err := c.RenameChat(ctx, group, "Next weekend"); err != nil {
		t.Fatal(err)
	}
	chats, _ := c.Dialogs(ctx)
	if chats[len(chats)-1].Title != "Next weekend" {
		t.Fatal("group title not updated")
	}
	if err := c.DeleteChat(ctx, group); err != nil {
		t.Fatal(err)
	}
	if _, err := c.History(ctx, group, ""); err == nil {
		t.Fatal("deleted group remained accessible")
	}
	contact := contacts[0]
	if err := c.AddContact(ctx, contact); err != nil {
		t.Fatal(err)
	}
	private := core.Chat{ID: contact.ID, Kind: "private"}
	if err := c.Send(ctx, private, "Hello Ada"); err != nil {
		t.Fatal(err)
	}
	if err := c.EditContact(ctx, contact, "Ada Byron"); err != nil {
		t.Fatal(err)
	}
	contacts, _ = c.SearchContacts(ctx, "Byron")
	if len(contacts) != 1 || contacts[0].Name != "Ada Byron" {
		t.Fatal("contact rename not searchable")
	}
	if err := c.DeleteContact(ctx, contact); err != nil {
		t.Fatal(err)
	}
	if messages, err := c.History(ctx, private, ""); err != nil || len(messages) != 1 {
		t.Fatalf("contact deletion removed messages: %+v, %v", messages, err)
	}
	if contacts, _ := c.SearchContacts(ctx, "ada"); len(contacts) != 0 {
		t.Fatal("contact not deleted")
	}
	imported, err := c.ImportContact(ctx, "+14155552671", "New Person")
	if err != nil || imported.ID == "" || imported.Name != "New Person" {
		t.Fatalf("import: %+v, %v", imported, err)
	}
	if contacts, _ := c.SearchContacts(ctx, "New Person"); len(contacts) != 1 {
		t.Fatal("imported contact not searchable")
	}
}

func TestManagementCancellationAndPrivateNames(t *testing.T) {
	c := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	contact := core.Contact{ID: "ada"}
	if _, err := c.CreateGroup(ctx, "Group", []core.Contact{contact}); !errors.Is(err, context.Canceled) {
		t.Fatal("group ignored cancellation")
	}
	if err := c.DeleteContact(ctx, contact); !errors.Is(err, context.Canceled) {
		t.Fatal("contact delete ignored cancellation")
	}
	if err := c.RenameChat(context.Background(), core.Chat{ID: "saved", Kind: "group"}, "Renamed"); err == nil {
		t.Fatal("private chat renamed by spoofing its kind")
	}
	if _, err := c.ImportContact(context.Background(), "+badphone", "Name"); err == nil {
		t.Fatal("invalid phone accepted")
	}
}
