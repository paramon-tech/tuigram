package demo

import (
	"context"
	"testing"

	"github.com/paramon-tech/tuigram/internal/core"
)

func TestOrganizationAndFoldersAreIsolatedAndRecoverArchivedChats(t *testing.T) {
	c := New()
	ctx := context.Background()
	chat := core.Chat{ID: "builders"}
	for _, err := range []error{c.SetChatPinned(ctx, chat, true), c.SetChatMuted(ctx, chat, true), c.SetChatArchived(ctx, chat, true)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	chats, err := c.Dialogs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range chats {
		if item.ID == chat.ID {
			found = true
			if !item.Archived || !item.Muted || item.Pinned {
				t.Fatalf("organization lost: %+v", item)
			}
		}
	}
	if !found {
		t.Fatal("archived chat lost from dialog set")
	}
	folder, err := c.CreateFolder(ctx, "Projects", chat)
	if err != nil {
		t.Fatal(err)
	}
	folder.IncludeIDs[0] = "modified"
	folders, err := c.Folders(ctx)
	if err != nil || len(folders) != 1 || !folders[0].Contains(chat) {
		t.Fatalf("folder aliased to caller: %+v %v", folders, err)
	}
	if err := c.SetFolderChat(ctx, folder.ID, chat, false); err != nil {
		t.Fatal(err)
	}
	folders, _ = c.Folders(ctx)
	if folders[0].Contains(chat) {
		t.Fatal("removed chat still in folder")
	}
	if err := c.SetFolderChat(ctx, folder.ID, chat, true); err != nil {
		t.Fatal(err)
	}
	folders, _ = c.Folders(ctx)
	if !folders[0].Contains(chat) {
		t.Fatal("explicit include was not restored")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.SetChatMuted(canceled, chat, false); err == nil {
		t.Fatal("ignored cancellation")
	}
	other := New()
	folders, _ = other.Folders(ctx)
	if len(folders) != 0 {
		t.Fatal("demo client state leaked")
	}
}
