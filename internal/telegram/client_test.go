package telegram

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/paramon-tech/tuigram/internal/core"
)

type invokeFunc func(context.Context, bin.Encoder, bin.Decoder) error

func (f invokeFunc) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	return f(ctx, input, output)
}

func testClient(fn invokeFunc) *client {
	c := &client{api: tg.NewClient(fn), peers: make(map[string]peerRecord)}
	c.remember([]tg.UserClass{&tg.User{ID: 1, AccessHash: 123, FirstName: "Alice"}}, []tg.ChatClass{&tg.Channel{ID: 2, AccessHash: 456, Title: "Group", Megagroup: true}})
	return c
}

func TestHistoryConvertsMessagesAndSearchUsesCorrectPeer(t *testing.T) {
	c := testClient(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		request := input.(*tg.MessagesSearchRequest)
		peer := request.Peer.(*tg.InputPeerChannel)
		if peer.ChannelID != 2 || peer.AccessHash != 456 || request.Q != "hello" || request.Limit != 100 {
			t.Fatalf("unexpected search: %+v", request)
		}
		forwarded := &tg.Message{ID: 4, PeerID: &tg.PeerChannel{ChannelID: 2}, FromID: &tg.PeerUser{UserID: 1}, Message: "hello", Date: 1700000000,
			Entities:  []tg.MessageEntityClass{&tg.MessageEntityTextURL{URL: "https://example.org"}},
			Reactions: tg.MessageReactions{Results: []tg.ReactionCount{{Reaction: &tg.ReactionEmoji{Emoticon: "👍"}, Count: 3}}},
			Media:     &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 7}}}
		forwarded.SetFwdFrom(tg.MessageFwdHeader{Date: 1699999999})
		output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesChannelMessages{Messages: []tg.MessageClass{forwarded, &tg.Message{ID: 3, Message: "older"}}}
		return nil
	})
	messages, err := c.History(context.Background(), core.Chat{ID: "channel:2", Title: "Group"}, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].ID != 3 {
		t.Fatalf("history must be oldest first: %+v", messages)
	}
	m := messages[1]
	if m.Sender != "Alice" || !m.Forwarded || !m.Image || m.Reactions[0].Count != 3 || !strings.Contains(m.Text, "https://example.org") {
		t.Fatalf("missing message data: %+v", m)
	}
}

func TestDialogsPaginatesAndDeduplicates(t *testing.T) {
	calls := 0
	c := testClient(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		request := input.(*tg.MessagesGetDialogsRequest)
		calls++
		switch calls {
		case 1:
			output.(*tg.MessagesDialogsBox).Dialogs = &tg.MessagesDialogsSlice{
				Dialogs:  []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerUser{UserID: 1}, TopMessage: 10}},
				Messages: []tg.MessageClass{&tg.Message{ID: 10, Date: 100, PeerID: &tg.PeerUser{UserID: 1}}},
			}
		case 2:
			if request.OffsetID != 10 || request.OffsetDate != 100 || !request.ExcludePinned {
				t.Fatalf("incorrect pagination: %+v", request)
			}
			output.(*tg.MessagesDialogsBox).Dialogs = &tg.MessagesDialogs{Dialogs: []tg.DialogClass{
				&tg.Dialog{Peer: &tg.PeerUser{UserID: 1}}, &tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 2}, UnreadCount: 3},
			}}
		default:
			t.Fatal("unexpected extra page")
		}
		return nil
	})
	chats, err := c.Dialogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 2 || chats[1].Kind != "group" || chats[1].Unread != 3 {
		t.Fatalf("unexpected dialogs: %+v", chats)
	}
}

func TestSendForwardAndReactionsUseNativeRequests(t *testing.T) {
	calls := 0
	randomIDs := make(map[int64]bool)
	c := testClient(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		calls++
		switch request := input.(type) {
		case *tg.MessagesSendMessageRequest:
			if request.Message != "hello 👋" || request.Peer.(*tg.InputPeerUser).AccessHash != 123 {
				t.Fatalf("invalid send: %+v", request)
			}
			randomIDs[request.RandomID] = true
		case *tg.MessagesForwardMessagesRequest:
			if request.FromPeer.(*tg.InputPeerUser).UserID != 1 || request.ToPeer.(*tg.InputPeerChannel).ChannelID != 2 || request.ID[0] != 9 {
				t.Fatalf("invalid forward: %+v", request)
			}
			randomIDs[request.RandomID[0]] = true
		case *tg.MessagesSendReactionRequest:
			if request.MsgID != 9 || request.Reaction[0].(*tg.ReactionEmoji).Emoticon != "👍" {
				t.Fatalf("invalid reaction: %+v", request)
			}
		default:
			t.Fatalf("unexpected RPC %T", input)
		}
		output.(*tg.UpdatesBox).Updates = &tg.UpdatesTooLong{}
		return nil
	})
	ctx := context.Background()
	alice, group := core.Chat{ID: "user:1"}, core.Chat{ID: "channel:2"}
	if err := c.Send(ctx, alice, "hello 👋"); err != nil {
		t.Fatal(err)
	}
	if err := c.Forward(ctx, alice, 9, group); err != nil {
		t.Fatal(err)
	}
	if err := c.React(ctx, group, 9, "👍"); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(randomIDs) != 2 || randomIDs[0] {
		t.Fatal("RPC count or message deduplication IDs are invalid")
	}
}

func TestSearchAndAddContactPreservesAccessHash(t *testing.T) {
	c := testClient(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		switch request := input.(type) {
		case *tg.ContactsSearchRequest:
			if request.Q != "newperson" {
				t.Fatalf("unexpected query %q", request.Q)
			}
			*output.(*tg.ContactsFound) = tg.ContactsFound{Results: []tg.PeerClass{&tg.PeerUser{UserID: 44}}, Users: []tg.UserClass{&tg.User{ID: 44, AccessHash: 987, FirstName: "New", LastName: "Person", Username: "newperson"}}}
		case *tg.ContactsAddContactRequest:
			peer := request.ID.(*tg.InputUser)
			if peer.UserID != 44 || peer.AccessHash != 987 || request.FirstName != "New" || request.LastName != "Person" {
				t.Fatalf("invalid contact: %+v", request)
			}
			output.(*tg.UpdatesBox).Updates = &tg.UpdatesTooLong{}
		default:
			t.Fatalf("unexpected RPC %T", input)
		}
		return nil
	})
	contacts, err := c.SearchContacts(context.Background(), "@newperson")
	if err != nil || len(contacts) != 1 {
		t.Fatalf("contacts=%+v, error=%v", contacts, err)
	}
	if err := c.AddContact(context.Background(), contacts[0]); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidSendAndCancelledRPC(t *testing.T) {
	c := testClient(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error { return ctx.Err() })
	chat := core.Chat{ID: "user:1"}
	for _, value := range []string{"", "  ", strings.Repeat("x", 4097), string([]byte{0xff})} {
		if err := c.Send(context.Background(), chat, value); err == nil {
			t.Fatalf("accepted invalid input of length %d", len(value))
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Send(ctx, chat, "hello"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestMinimalPeerDoesNotOverwriteAccessHash(t *testing.T) {
	c := testClient(nil)
	c.remember([]tg.UserClass{&tg.User{ID: 1, Min: true, FirstName: "Alice"}}, nil)
	record, err := c.peer("user:1")
	if err != nil || record.input.(*tg.InputPeerUser).AccessHash != 123 {
		t.Fatal("minimal peer overwrote known access hash")
	}
}

func TestImageSizeIsBoundedFromMetadataAndStream(t *testing.T) {
	photo := &tg.Photo{ID: 1, Sizes: []tg.PhotoSizeClass{
		&tg.PhotoSize{Type: "s", Size: 1024}, &tg.PhotoSizeProgressive{Type: "x", Sizes: []int{1000, 4096}}, &tg.PhotoSize{Type: "w", Size: maxImageBytes + 1},
	}}
	location, err := photoLocation(photo)
	if err != nil || location.(*tg.InputPhotoFileLocation).ThumbSize != "x" {
		t.Fatalf("photo selection: %v %v", location, err)
	}
	_, err = imageLocation(&tg.Message{Media: &tg.MessageMediaDocument{Document: &tg.Document{MimeType: "image/png", Size: maxImageBytes + 1}}})
	if !errors.Is(err, errImageTooLarge) {
		t.Fatalf("oversized metadata: %v", err)
	}
	var output limitedBuffer
	if _, err := output.Write(make([]byte, maxImageBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte{1}); !errors.Is(err, errImageTooLarge) {
		t.Fatalf("unbounded download: %v", err)
	}
	if output.Len() != maxImageBytes {
		t.Fatal("buffer exceeded download ceiling")
	}
}

func TestPromptsDoNotEchoSecretsAndPreservePasswordSpaces(t *testing.T) {
	var output bytes.Buffer
	p := newPrompter(strings.NewReader("+12345\n56789\n secret phrase \n"), &output)
	if value, err := p.Phone(context.Background()); err != nil || value != "+12345" {
		t.Fatalf("phone: %q %v", value, err)
	}
	if value, err := p.Code(context.Background(), nil); err != nil || value != "56789" {
		t.Fatalf("code: %q %v", value, err)
	}
	if value, err := p.Password(context.Background()); err != nil || value != " secret phrase " {
		t.Fatalf("password: %q %v", value, err)
	}
	if strings.Contains(output.String(), "56789") || strings.Contains(output.String(), "secret phrase") {
		t.Fatal("authentication secrets leaked to output")
	}
}

func TestPromptCancellationAndInputBound(t *testing.T) {
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := newPrompter(input, io.Discard).Password(ctx); finished <- err }()
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("prompt did not stop on cancellation")
	}
	_, err := newPrompter(strings.NewReader(strings.Repeat("x", maxPromptBytes+1)), io.Discard).Password(context.Background())
	if err == nil {
		t.Fatal("unbounded authentication input")
	}
}

func TestMediaKeyIsolatesAccountMediaAndRevision(t *testing.T) {
	c := &client{accountID: 100}
	chat := core.Chat{ID: "channel:2"}
	message := &tg.Message{ID: 3, Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 4}}}
	original := c.message(chat, message).MediaKey
	if original == "" {
		t.Fatal("ordinary media has no cache identity")
	}
	if c.message(chat, message).MediaKey != original {
		t.Fatal("media cache identity is unstable")
	}
	c.accountID = 200
	if c.message(chat, message).MediaKey == original {
		t.Fatal("media key crossed account boundary")
	}
	c.accountID = 100
	message.EditDate = 10
	if c.message(chat, message).MediaKey == original {
		t.Fatal("media key did not change after edit")
	}
	message.EditDate = 0
	message.Media = &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 5}}
	if c.message(chat, message).MediaKey == original {
		t.Fatal("replacement media reused cache identity")
	}
	message.Media = &tg.MessageMediaDocument{Document: &tg.Document{ID: 4, MimeType: "image/png"}}
	if key := c.message(chat, message).MediaKey; key == "" || key == original {
		t.Fatal("photo/document IDs share cache identity")
	}
}

func TestProtectedEphemeralAndUnknownMediaAreNotCacheable(t *testing.T) {
	photo := func() *tg.Message { return &tg.Message{Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 4}}} }
	tests := map[string]func(*client, *tg.Message){
		"unknown account":       func(c *client, m *tg.Message) { c.accountID = 0 },
		"protected":             func(c *client, m *tg.Message) { m.Noforwards = true },
		"auto deleting message": func(c *client, m *tg.Message) { m.TTLPeriod = 60 },
		"ephemeral photo":       func(c *client, m *tg.Message) { m.Media.(*tg.MessageMediaPhoto).TTLSeconds = 10 },
		"ephemeral document": func(c *client, m *tg.Message) {
			m.Media = &tg.MessageMediaDocument{Document: &tg.Document{ID: 4, MimeType: "image/png"}, TTLSeconds: 10}
		},
		"unknown media ID": func(c *client, m *tg.Message) { m.Media.(*tg.MessageMediaPhoto).Photo = &tg.Photo{} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			c := &client{accountID: 100}
			message := photo()
			mutate(c, message)
			if key := c.message(core.Chat{ID: "channel:2"}, message).MediaKey; key != "" {
				t.Fatalf("noncacheable media has disk key %q", key)
			}
		})
	}
}
