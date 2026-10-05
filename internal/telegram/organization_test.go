package telegram

import (
	"context"
	"math"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/paramon-tech/tuigram/internal/core"
)

func TestOrganizationMutationsPreserveUnrelatedSettings(t *testing.T) {
	calls := 0
	c := testClient(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		calls++
		switch r := input.(type) {
		case *tg.MessagesToggleDialogPinRequest:
			if !r.Pinned || r.Peer.(*tg.InputDialogPeer).Peer.(*tg.InputPeerUser).AccessHash != 123 {
				t.Fatalf("bad pin: %+v", r)
			}
			output.(*tg.BoolBox).Bool = &tg.BoolTrue{}
		case *tg.FoldersEditPeerFoldersRequest:
			if len(r.FolderPeers) != 1 || r.FolderPeers[0].FolderID != 1 {
				t.Fatalf("bad archive: %+v", r)
			}
			output.(*tg.UpdatesBox).Updates = &tg.UpdatesTooLong{}
		case *tg.AccountUpdateNotifySettingsRequest:
			until, ok := r.Settings.GetMuteUntil()
			if !ok || until != math.MaxInt32 && until != 0 {
				t.Fatalf("bad mute: %+v", r.Settings)
			}
			if _, ok := r.Settings.GetShowPreviews(); ok {
				t.Fatal("changed previews")
			}
			if _, ok := r.Settings.GetSound(); ok {
				t.Fatal("changed sound")
			}
			if _, ok := r.Settings.GetSilent(); ok {
				t.Fatal("changed silent sends")
			}
			output.(*tg.BoolBox).Bool = &tg.BoolTrue{}
		default:
			t.Fatalf("unexpected RPC %T", input)
		}
		return nil
	})
	ctx := context.Background()
	chat := core.Chat{ID: "user:1"}
	for _, err := range []error{c.SetChatPinned(ctx, chat, true), c.SetChatArchived(ctx, chat, true), c.SetChatMuted(ctx, chat, true), c.SetChatMuted(ctx, chat, false)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls != 4 {
		t.Fatalf("got %d RPCs", calls)
	}
}

func TestFolderMembershipPreservesRemoteRulesAndRemovesExclusion(t *testing.T) {
	calls := 0
	c := testClient(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		switch r := input.(type) {
		case *tg.MessagesGetDialogFiltersRequest:
			output.(*tg.MessagesDialogFilters).Filters = []tg.DialogFilterClass{&tg.DialogFilter{ID: 5, Title: tg.TextWithEntities{Text: "Work"}, Groups: true, ExcludeMuted: true, ExcludePeers: []tg.InputPeerClass{&tg.InputPeerUser{UserID: 1, AccessHash: 123}}, PinnedPeers: []tg.InputPeerClass{&tg.InputPeerChannel{ChannelID: 2, AccessHash: 456}}}}
		case *tg.MessagesUpdateDialogFilterRequest:
			calls++
			f := r.Filter.(*tg.DialogFilter)
			if f.ID != 5 || f.Title.Text != "Work" || !f.Groups || !f.ExcludeMuted || len(f.PinnedPeers) != 1 || len(f.ExcludePeers) != 0 || len(f.IncludePeers) != 1 {
				t.Fatalf("rules overwritten: %+v", f)
			}
			output.(*tg.BoolBox).Bool = &tg.BoolTrue{}
		default:
			t.Fatalf("unexpected RPC %T", input)
		}
		return nil
	})
	if err := c.SetFolderChat(context.Background(), 5, core.Chat{ID: "user:1"}, true); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("no mutation")
	}
}

func TestCreateFolderUsesUnusedIDAndInitialChat(t *testing.T) {
	c := testClient(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		switch r := input.(type) {
		case *tg.MessagesGetDialogFiltersRequest:
			output.(*tg.MessagesDialogFilters).Filters = []tg.DialogFilterClass{&tg.DialogFilter{ID: 2}, &tg.DialogFilterChatlist{ID: 3}}
		case *tg.MessagesUpdateDialogFilterRequest:
			f := r.Filter.(*tg.DialogFilter)
			if r.ID != 4 || f.ID != 4 || f.Title.Text != "Projects" || len(f.IncludePeers) != 1 {
				t.Fatalf("bad folder: %+v", f)
			}
			output.(*tg.BoolBox).Bool = &tg.BoolTrue{}
		default:
			t.Fatalf("unexpected RPC %T", input)
		}
		return nil
	})
	f, err := c.CreateFolder(context.Background(), " Projects ", core.Chat{ID: "user:1"})
	if err != nil || f.ID != 4 || !f.Contains(core.Chat{ID: "user:1"}) {
		t.Fatalf("folder: %+v %v", f, err)
	}
	if _, err := c.CreateFolder(context.Background(), "long folder name", core.Chat{ID: "user:1"}); err == nil {
		t.Fatal("accepted invalid title")
	}
}

func TestDialogsIncludesArchivePinnedAndOldFolderMembers(t *testing.T) {
	c := testClient(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		switch r := input.(type) {
		case *tg.AccountGetNotifySettingsRequest:
			return nil
		case *tg.MessagesGetPinnedDialogsRequest:
			if r.FolderID == 0 {
				output.(*tg.MessagesPeerDialogs).Dialogs = []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerUser{UserID: 1}, Pinned: true}}
			}
		case *tg.MessagesGetDialogsRequest:
			if !r.ExcludePinned {
				t.Fatal("pinned dialogs mixed into date pagination")
			}
			if folder, ok := r.GetFolderID(); !ok || folder != r.FolderID {
				t.Fatal("folder not explicitly set")
			}
			d := &tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 2}, UnreadCount: 5, TopMessage: 99, ReadInboxMaxID: 90}
			d.NotifySettings.SetMuteUntil(math.MaxInt32)
			if r.FolderID == 1 {
				output.(*tg.MessagesDialogsBox).Dialogs = &tg.MessagesDialogs{Dialogs: []tg.DialogClass{d}}
			} else {
				output.(*tg.MessagesDialogsBox).Dialogs = &tg.MessagesDialogs{Dialogs: []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerUser{UserID: 1}}}}
			}
		case *tg.MessagesGetDialogFiltersRequest:
			output.(*tg.MessagesDialogFilters).Filters = []tg.DialogFilterClass{&tg.DialogFilter{ID: 2, Title: tg.TextWithEntities{Text: "Old"}, IncludePeers: []tg.InputPeerClass{&tg.InputPeerUser{UserID: 9, AccessHash: 999}}}}
		case *tg.MessagesGetPeerDialogsRequest:
			if len(r.Peers) != 1 || r.Peers[0].(*tg.InputDialogPeer).Peer.(*tg.InputPeerUser).UserID != 9 {
				t.Fatal("wrong folder peer")
			}
			response := output.(*tg.MessagesPeerDialogs)
			response.Users = []tg.UserClass{&tg.User{ID: 9, AccessHash: 999, FirstName: "Old contact", Contact: true}}
			response.Dialogs = []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerUser{UserID: 9}}}
		default:
			t.Fatalf("unexpected RPC %T", input)
		}
		return nil
	})
	chats, err := c.Dialogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 3 || !chats[0].Pinned || !chats[1].Archived || !chats[1].Muted || chats[1].TopMessageID != 99 || chats[1].ReadInboxMaxID != 90 || !chats[2].Contact {
		t.Fatalf("lost dialog metadata: %+v", chats)
	}
}

func TestOrganizationMinimalPeerRetainsContactMembership(t *testing.T) {
	c := testClient(func(context.Context, bin.Encoder, bin.Decoder) error { return nil })
	c.remember([]tg.UserClass{&tg.User{ID: 1, AccessHash: 123, Contact: true, FirstName: "Alice"}}, nil)
	c.remember([]tg.UserClass{&tg.User{ID: 1, Min: true, FirstName: "Alice"}}, nil)
	chat, ok := c.dialogChat(&tg.Dialog{Peer: &tg.PeerUser{UserID: 1}}, false)
	if !ok || !chat.Contact {
		t.Fatal("minimal history entity removed Contacts folder membership")
	}
	// A later full entity may deliberately remove the contact.
	c.remember([]tg.UserClass{&tg.User{ID: 1, AccessHash: 123, FirstName: "Alice"}}, nil)
	chat, _ = c.dialogChat(&tg.Dialog{Peer: &tg.PeerUser{UserID: 1}}, false)
	if chat.Contact {
		t.Fatal("full contact removal was ignored")
	}
}

func TestOrganizationMuteDefaultsAndExplicitOverride(t *testing.T) {
	calls := 0
	c := testClient(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		r, ok := input.(*tg.AccountGetNotifySettingsRequest)
		if !ok {
			t.Fatalf("unexpected RPC %T", input)
		}
		calls++
		if _, ok := r.Peer.(*tg.InputNotifyUsers); ok {
			output.(*tg.PeerNotifySettings).SetMuteUntil(math.MaxInt32)
		}
		return nil
	})
	if err := c.loadNotificationDefaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.loadNotificationDefaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("global defaults not cached")
	}
	d := &tg.Dialog{Peer: &tg.PeerUser{UserID: 1}}
	chat, _ := c.dialogChat(d, false)
	if !chat.Muted {
		t.Fatal("missing inherited mute")
	}
	d.NotifySettings.SetMuteUntil(0)
	chat, _ = c.dialogChat(d, false)
	if chat.Muted {
		t.Fatal("explicit unmute did not override default")
	}
}
