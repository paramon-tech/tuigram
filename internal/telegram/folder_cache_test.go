package telegram

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/paramon-tech/tuigram/internal/core"
)

func TestDialogRefreshReusesFolderRulesButPanelRefetches(t *testing.T) {
	filters := 0
	c := testClient(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		switch input.(type) {
		case *tg.AccountGetNotifySettingsRequest, *tg.MessagesGetPinnedDialogsRequest:
		case *tg.MessagesGetDialogsRequest:
			output.(*tg.MessagesDialogsBox).Dialogs = &tg.MessagesDialogs{}
		case *tg.MessagesGetDialogFiltersRequest:
			filters++
		default:
			t.Fatalf("unexpected RPC %T", input)
		}
		return nil
	})
	ctx := context.Background()
	for range 3 {
		if _, err := c.Dialogs(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if filters != 1 {
		t.Fatalf("background refresh repeatedly fetched folder rules: %d", filters)
	}
	if _, err := c.Folders(ctx); err != nil {
		t.Fatal(err)
	}
	if filters != 2 {
		t.Fatal("opening organization did not fetch current rules")
	}
	c.dialogFiltersAt = time.Now().Add(-2 * time.Minute)
	if _, err := c.Dialogs(ctx); err != nil {
		t.Fatal(err)
	}
	if filters != 3 {
		t.Fatal("expired folder rules were reused")
	}
}

func TestFolderEditsUseFreshRulesAndInvalidateBackgroundCache(t *testing.T) {
	for _, action := range []string{"create", "include"} {
		t.Run(action, func(t *testing.T) {
			filters := 0
			c := testClient(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				switch input.(type) {
				case *tg.MessagesGetDialogFiltersRequest:
					filters++
					output.(*tg.MessagesDialogFilters).Filters = []tg.DialogFilterClass{&tg.DialogFilter{ID: 2}}
				case *tg.MessagesUpdateDialogFilterRequest:
					output.(*tg.BoolBox).Bool = &tg.BoolTrue{}
				default:
					t.Fatalf("unexpected RPC %T", input)
				}
				return nil
			})
			ctx := context.Background()
			if _, err := c.loadDialogFilters(ctx, true); err != nil {
				t.Fatal(err)
			}
			chat := core.Chat{ID: "user:1"}
			var err error
			if action == "create" {
				_, err = c.CreateFolder(ctx, "Work", chat)
			} else {
				err = c.SetFolderChat(ctx, 2, chat, true)
			}
			if err != nil {
				t.Fatal(err)
			}
			if filters != 2 {
				t.Fatal("folder edit reused stale rules")
			}
			if _, err := c.loadDialogFilters(ctx, true); err != nil {
				t.Fatal(err)
			}
			if filters != 3 {
				t.Fatal("background refresh reused rules from before the edit")
			}
		})
	}
}

func TestFolderFetchBeforeInvalidationCannotRefillCache(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	c := testClient(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		if _, ok := input.(*tg.MessagesGetDialogFiltersRequest); !ok {
			t.Errorf("unexpected RPC %T", input)
			return nil
		}
		close(started)
		<-release
		return nil
	})
	done := make(chan error, 1)
	go func() { _, err := c.loadDialogFilters(context.Background(), true); done <- err }()
	<-started
	c.invalidateDialogFilters()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if c.dialogFilters != nil {
		t.Fatal("old fetch repopulated cache after a folder edit")
	}
}
