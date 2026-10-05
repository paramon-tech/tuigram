package telegram

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/paramon-tech/tuigram/internal/core"
)

func TestHistoryPaginationSparseIDsBothDirectionsAndSearch(t *testing.T) {
	items := []tg.MessageClass{&tg.Message{ID: 25, Message: "needle"}, &tg.MessageService{ID: 11}, &tg.Message{ID: 5, Message: "needle"}, &tg.Message{ID: 3, Message: "other"}, &tg.Message{ID: 1, Message: "needle"}}
	c := testClient(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		var offset, additional, limit int
		matches := items
		switch request := input.(type) {
		case *tg.MessagesGetHistoryRequest:
			offset, additional, limit = request.OffsetID, request.AddOffset, request.Limit
		case *tg.MessagesSearchRequest:
			if request.Q != "needle" {
				t.Fatalf("unexpected query %q", request.Q)
			}
			offset, additional, limit = request.OffsetID, request.AddOffset, request.Limit
			matches = []tg.MessageClass{items[0], items[2], items[4]}
		default:
			t.Fatalf("unexpected request %T", input)
		}
		// Telegram defines offsetFromID as the count up to the boundary,
		// inclusive. This exercises real cursor arithmetic, not just fields.
		start := 0
		if offset > 0 {
			for start < len(matches) && matches[start].GetID() >= offset {
				start++
			}
		}
		start = max(0, min(len(matches), start+additional))
		end := min(len(matches), start+limit)
		output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessagesSlice{Messages: matches[start:end], Count: len(matches)}
		return nil
	})
	chat := core.Chat{ID: "user:1"}
	for _, query := range []string{"", "needle"} {
		page, err := c.HistoryPage(context.Background(), chat, core.HistoryRequest{Query: query, Oldest: true, Limit: 1})
		if err != nil || page.OldestID != 1 || page.HasOlder {
			t.Fatalf("beginning: %+v %v", page, err)
		}
		ids := []int{page.NewestID}
		for page.HasNewer {
			page, err = c.HistoryPage(context.Background(), chat, core.HistoryRequest{Query: query, AfterID: page.NewestID, Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			if page.NewestID > 0 {
				ids = append(ids, page.NewestID)
			}
			if len(ids) > 10 {
				t.Fatal("pagination did not terminate")
			}
		}
		want := []int{1, 3, 5, 11, 25}
		if query != "" {
			want = []int{1, 5, 25}
		}
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("query=%q ids=%v want=%v", query, ids, want)
		}
		page, err = c.HistoryPage(context.Background(), chat, core.HistoryRequest{Query: query, Limit: 2})
		if err != nil || page.NewestID != 25 || page.HasNewer {
			t.Fatalf("latest: %+v %v", page, err)
		}
		older, err := c.HistoryPage(context.Background(), chat, core.HistoryRequest{Query: query, BeforeID: page.OldestID, Limit: 2})
		if err != nil || older.NewestID >= page.OldestID {
			t.Fatalf("older: %+v %v", older, err)
		}
	}
}

func TestHistoryServiceOnlyPageRetainsCursor(t *testing.T) {
	c := testClient(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.MessageService{ID: 10}, &tg.MessageEmpty{ID: 9}}}
		return nil
	})
	page, err := c.HistoryPage(context.Background(), core.Chat{ID: "user:1"}, core.HistoryRequest{Limit: 2})
	if err != nil || len(page.Messages) != 0 || page.OldestID != 9 || page.NewestID != 10 || !page.HasOlder {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestReadReceiptsUsePeerTypeAndPreserveLaterArrivals(t *testing.T) {
	for _, id := range []string{"user:1", "group:3", "channel:2"} {
		t.Run(id, func(t *testing.T) {
			calls := 0
			c := testClient(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				calls++
				switch request := input.(type) {
				case *tg.MessagesReadHistoryRequest:
					if id == "channel:2" || request.MaxID != 10 {
						t.Fatalf("wrong receipt %+v", request)
					}
				case *tg.ChannelsReadHistoryRequest:
					if id != "channel:2" || request.MaxID != 10 || request.Channel.(*tg.InputChannel).AccessHash != 456 {
						t.Fatalf("wrong channel receipt %+v", request)
					}
					output.(*tg.BoolBox).Bool = &tg.BoolTrue{}
				case *tg.MessagesGetPeerDialogsRequest:
					peer := tg.PeerClass(&tg.PeerUser{UserID: 1})
					if id == "group:3" {
						peer = &tg.PeerChat{ChatID: 3}
					}
					if id == "channel:2" {
						peer = &tg.PeerChannel{ChannelID: 2}
					}
					*output.(*tg.MessagesPeerDialogs) = tg.MessagesPeerDialogs{Dialogs: []tg.DialogClass{&tg.Dialog{Peer: peer, TopMessage: 12, ReadInboxMaxID: 10, UnreadCount: 2}}}
				default:
					t.Fatalf("unexpected request %T", input)
				}
				return nil
			})
			c.remember(nil, []tg.ChatClass{&tg.Chat{ID: 3, Title: "Basic group"}})
			state, err := c.MarkRead(context.Background(), core.Chat{ID: id}, 10)
			if err != nil || state.Unread != 2 || state.MaxID != 10 || state.TopMessageID != 12 || calls != 2 {
				t.Fatalf("state=%+v calls=%d err=%v", state, calls, err)
			}
		})
	}
}

func TestMarkReadClearsManualUnreadMarkAndValidates(t *testing.T) {
	calls := 0
	c := testClient(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		calls++
		switch request := input.(type) {
		case *tg.MessagesReadHistoryRequest:
		case *tg.MessagesMarkDialogUnreadRequest:
			if request.Unread {
				t.Fatal("marked unread")
			}
			output.(*tg.BoolBox).Bool = &tg.BoolTrue{}
		case *tg.MessagesGetPeerDialogsRequest:
			*output.(*tg.MessagesPeerDialogs) = tg.MessagesPeerDialogs{Dialogs: []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerUser{UserID: 1}, ReadInboxMaxID: 10, TopMessage: 10}}}
		default:
			t.Fatalf("unexpected %T", input)
		}
		return nil
	})
	chat := core.Chat{ID: "user:1", UnreadMark: true}
	state, err := c.MarkRead(context.Background(), chat, 10)
	if err != nil || state.UnreadMark || calls != 3 {
		t.Fatalf("state=%+v calls=%d err=%v", state, calls, err)
	}
	if _, err = c.MarkRead(context.Background(), chat, 0); err == nil || calls != 3 {
		t.Fatal("zero receipt reached server")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.MarkRead(ctx, chat, 10); !errors.Is(err, context.Canceled) || calls != 3 {
		t.Fatalf("cancellation not respected: %v", err)
	}
}

func TestMarkReadFailureNeverClaimsSuccess(t *testing.T) {
	failed := errors.New("offline")
	c := testClient(func(_ context.Context, input bin.Encoder, output bin.Decoder) error { return failed })
	state, err := c.MarkRead(context.Background(), core.Chat{ID: "user:1"}, 10)
	if !errors.Is(err, failed) || state != (core.ReadState{}) {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}
