package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/paramon-tech/tuigram/internal/core"
)

func TestCreateGroupUsesResolvedMembersAndReportsPartialInvites(t *testing.T) {
	for _, partial := range []bool{false, true} {
		c := testClient(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
			request := input.(*tg.MessagesCreateChatRequest)
			if request.Title != "Weekend plans" || len(request.Users) != 1 {
				t.Fatalf("invalid group request: %+v", request)
			}
			user := request.Users[0].(*tg.InputUser)
			if user.UserID != 1 || user.AccessHash != 123 {
				t.Fatalf("lost member access hash: %+v", user)
			}
			response := output.(*tg.MessagesInvitedUsers)
			response.Updates = &tg.Updates{Chats: []tg.ChatClass{&tg.Chat{ID: 9, Title: request.Title}}}
			if partial {
				response.MissingInvitees = []tg.MissingInvitee{{UserID: 1}}
			}
			return nil
		})
		member := core.Contact{ID: "user:1"}
		chat, err := c.CreateGroup(context.Background(), " Weekend plans ", []core.Contact{member, member})
		if chat.ID != "group:9" || chat.Title != "Weekend plans" || chat.Kind != "group" {
			t.Fatalf("group not returned: %+v, %v", chat, err)
		}
		if partial && (err == nil || !strings.Contains(err.Error(), "group created")) || !partial && err != nil {
			t.Fatalf("partial invite not reported correctly: %v", err)
		}
		if record, err := c.peer(chat.ID); err != nil || record.input.(*tg.InputPeerChat).ChatID != 9 {
			t.Fatalf("new group cannot be used immediately: %+v, %v", record, err)
		}
	}
}

func TestRenameChatUsesPeerTypeAndDoesNotChangePrivateContacts(t *testing.T) {
	calls := 0
	c := testClient(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		calls++
		switch request := input.(type) {
		case *tg.MessagesEditChatTitleRequest:
			if request.ChatID != 3 || request.Title != "Renamed group" {
				t.Fatalf("wrong group rename: %+v", request)
			}
		case *tg.ChannelsEditTitleRequest:
			peer := request.Channel.(*tg.InputChannel)
			if peer.ChannelID != 2 || peer.AccessHash != 456 || request.Title != "Renamed channel" {
				t.Fatalf("wrong channel rename: %+v", request)
			}
		default:
			t.Fatalf("unexpected RPC %T", input)
		}
		output.(*tg.UpdatesBox).Updates = &tg.UpdatesTooLong{}
		return nil
	})
	c.remember(nil, []tg.ChatClass{&tg.Chat{ID: 3, Title: "Group"}})
	ctx := context.Background()
	if err := c.RenameChat(ctx, core.Chat{ID: "group:3"}, "Renamed group"); err != nil {
		t.Fatal(err)
	}
	if err := c.RenameChat(ctx, core.Chat{ID: "channel:2"}, "Renamed channel"); err != nil {
		t.Fatal(err)
	}
	if err := c.RenameChat(ctx, core.Chat{ID: "user:1", Kind: "group"}, "Unsafe override"); err == nil {
		t.Fatal("trusted caller's kind instead of actual peer type")
	}
	if calls != 2 {
		t.Fatalf("unexpected RPC count: %d", calls)
	}
	if record, _ := c.peer("group:3"); record.title != "Renamed group" {
		t.Fatal("successful rename was not remembered")
	}
}

func TestDeleteChatOnlyRemovesOwnHistoryOrLeavesMembership(t *testing.T) {
	deletions, leaves := 0, 0
	c := testClient(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		switch request := input.(type) {
		case *tg.MessagesDeleteHistoryRequest:
			if request.Revoke || request.JustClear || request.MaxID != 0 || request.Peer.(*tg.InputPeerUser).UserID != 1 {
				t.Fatalf("unsafe deletion request: %+v", request)
			}
			deletions++
			if deletions == 1 {
				output.(*tg.MessagesAffectedHistory).Offset = 20
			}
		case *tg.MessagesDeleteChatUserRequest:
			if _, ok := request.UserID.(*tg.InputUserSelf); !ok || request.ChatID != 3 || request.RevokeHistory {
				t.Fatalf("unsafe group leave request: %+v", request)
			}
			leaves++
			output.(*tg.UpdatesBox).Updates = &tg.UpdatesTooLong{}
		case *tg.ChannelsLeaveChannelRequest:
			peer := request.Channel.(*tg.InputChannel)
			if peer.ChannelID != 2 || peer.AccessHash != 456 {
				t.Fatalf("wrong channel leave: %+v", request)
			}
			leaves++
			output.(*tg.UpdatesBox).Updates = &tg.UpdatesTooLong{}
		default:
			t.Fatalf("unexpected RPC %T", input)
		}
		return nil
	})
	c.remember(nil, []tg.ChatClass{&tg.Chat{ID: 3, Title: "Group"}})
	for _, id := range []string{"user:1", "group:3", "channel:2"} {
		if err := c.DeleteChat(context.Background(), core.Chat{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if deletions != 2 || leaves != 2 {
		t.Fatalf("history continuation or membership leave missing: %d, %d", deletions, leaves)
	}
}

func TestListEditAndDeleteContactsPreservePrivacyAndPeer(t *testing.T) {
	calls := 0
	c := testClient(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		calls++
		switch request := input.(type) {
		case *tg.ContactsGetContactsRequest:
			if request.Hash != 0 {
				t.Fatal("contact list requires full response")
			}
			output.(*tg.ContactsContactsBox).Contacts = &tg.ContactsContacts{
				Contacts: []tg.Contact{{UserID: 1}},
				Users:    []tg.UserClass{&tg.User{ID: 1, AccessHash: 123, FirstName: "Alice"}, &tg.User{ID: 8, FirstName: "Unrelated user"}},
			}
		case *tg.ContactsAddContactRequest:
			user := request.ID.(*tg.InputUser)
			if request.FirstName != "Alice Smith" || request.LastName != "" || request.Phone != "" || request.AddPhonePrivacyException || user.UserID != 1 || user.AccessHash != 123 {
				t.Fatalf("unsafe rename: %+v", request)
			}
			output.(*tg.UpdatesBox).Updates = &tg.UpdatesTooLong{}
		case *tg.ContactsDeleteContactsRequest:
			if len(request.ID) != 1 || request.ID[0].(*tg.InputUser).AccessHash != 123 {
				t.Fatalf("incorrect contact deletion: %+v", request)
			}
			output.(*tg.UpdatesBox).Updates = &tg.UpdatesTooLong{}
		default:
			t.Fatalf("unexpected RPC %T", input)
		}
		return nil
	})
	ctx := context.Background()
	contacts, err := c.SearchContacts(ctx, " ")
	if err != nil || len(contacts) != 1 || contacts[0].ID != "user:1" {
		t.Fatalf("saved contacts mixed with noncontacts: %+v, %v", contacts, err)
	}
	if err := c.EditContact(ctx, contacts[0], "Alice Smith"); err != nil {
		t.Fatal(err)
	}
	if record, _ := c.peer("user:1"); record.title != "Alice Smith" || record.firstName != "Alice Smith" || record.lastName != "" {
		t.Fatal("edited contact name not remembered")
	}
	if err := c.DeleteContact(ctx, contacts[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := c.peer("user:1"); err != nil {
		t.Fatal("deleting contact also removed its conversation peer")
	}
	if calls != 3 {
		t.Fatalf("unexpected RPC count: %d", calls)
	}
}

func TestImportContactMatchesClientIDAndPreservesResolvedPeer(t *testing.T) {
	c := testClient(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		request := input.(*tg.ContactsImportContactsRequest)
		if len(request.Contacts) != 1 {
			t.Fatal("import must contain only explicitly supplied contact")
		}
		contact := request.Contacts[0]
		if contact.Phone != "+14155552671" || contact.FirstName != "New Person" || contact.ClientID == 0 {
			t.Fatalf("incorrect import: %+v", contact)
		}
		*output.(*tg.ContactsImportedContacts) = tg.ContactsImportedContacts{
			Imported: []tg.ImportedContact{{ClientID: contact.ClientID, UserID: 44}},
			Users:    []tg.UserClass{&tg.User{ID: 44, AccessHash: 987, FirstName: "New Person"}},
		}
		return nil
	})
	contact, err := c.ImportContact(context.Background(), "+14155552671", "New Person")
	if err != nil || contact.ID != "user:44" || contact.Name != "New Person" {
		t.Fatalf("import result: %+v, %v", contact, err)
	}
	if record, err := c.peer(contact.ID); err != nil || record.input.(*tg.InputPeerUser).AccessHash != 987 {
		t.Fatalf("imported peer unavailable: %+v, %v", record, err)
	}
}

func TestManagementValidationAndFailedMutation(t *testing.T) {
	calls := 0
	c := testClient(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		calls++
		return errors.New("RPC failed")
	})
	ctx := context.Background()
	member := core.Contact{ID: "user:1"}
	for _, title := range []string{"", " ", strings.Repeat("x", 129), string([]byte{0xff})} {
		if _, err := c.CreateGroup(ctx, title, []core.Contact{member}); err == nil {
			t.Fatalf("accepted invalid group title %q", title)
		}
	}
	if _, err := c.CreateGroup(ctx, "Group", nil); err == nil {
		t.Fatal("accepted group without members")
	}
	for _, phone := range []string{"", "123456789", "+1234", "+1415oops123", "+1234567890123456"} {
		if _, err := c.ImportContact(ctx, phone, "Person"); err == nil {
			t.Fatalf("accepted invalid phone %q", phone)
		}
	}
	if calls != 0 {
		t.Fatal("invalid input reached Telegram")
	}
	if err := c.EditContact(ctx, member, "Changed"); err == nil {
		t.Fatal("discarded edit failure")
	}
	if record, _ := c.peer(member.ID); record.title != "Alice" {
		t.Fatal("failed edit changed local record")
	}
}

func TestDeleteHistoryHonorsCancellationBetweenPages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := testClient(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		output.(*tg.MessagesAffectedHistory).Offset = 1
		cancel()
		return nil
	})
	if err := c.DeleteChat(ctx, core.Chat{ID: "user:1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestSavedContactsSortedAndBounded(t *testing.T) {
	c := testClient(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		response := &tg.ContactsContacts{}
		for i := resultLimit + 5; i > 0; i-- {
			id := int64(i + 1000)
			response.Contacts = append(response.Contacts, tg.Contact{UserID: id})
			response.Users = append(response.Users, &tg.User{ID: id, FirstName: fmt.Sprintf("Person %03d", i)})
		}
		output.(*tg.ContactsContactsBox).Contacts = response
		return nil
	})
	contacts, err := c.SearchContacts(context.Background(), "")
	if err != nil || len(contacts) != resultLimit || contacts[0].Name != "Person 001" || contacts[resultLimit-1].Name != "Person 100" {
		t.Fatalf("contact browse order or bound wrong: %d, %v", len(contacts), err)
	}
}
