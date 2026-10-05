package telegram

import (
	"context"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/paramon-tech/tuigram/internal/core"
)

func TestReactionRemovalAndOwnReactionConversion(t *testing.T) {
	removed := false
	c := testClient(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		switch request := input.(type) {
		case *tg.MessagesSendReactionRequest:
			if request.MsgID != 9 || request.Peer.(*tg.InputPeerChannel).ChannelID != 2 || len(request.Reaction) != 0 {
				t.Fatalf("invalid removal request: %+v", request)
			}
			removed = true
			output.(*tg.UpdatesBox).Updates = &tg.Updates{}
		case *tg.MessagesGetHistoryRequest:
			own := tg.ReactionCount{Reaction: &tg.ReactionEmoji{Emoticon: "❤"}, Count: 2}
			own.SetChosenOrder(0) // Zero is a valid chosen order; check the flag.
			output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.Message{ID: 9, Reactions: tg.MessageReactions{Results: []tg.ReactionCount{own, {Reaction: &tg.ReactionEmoji{Emoticon: "👍"}, Count: 1}}}}}}
		default:
			t.Fatalf("unexpected request %T", input)
		}
		return nil
	})
	chat := core.Chat{ID: "channel:2"}
	if err := c.React(context.Background(), chat, 9, ""); err != nil || !removed {
		t.Fatalf("reaction removal: %v", err)
	}
	messages, err := c.History(context.Background(), chat, "")
	if err != nil || len(messages) != 1 || !messages[0].Reactions[0].Mine || messages[0].Reactions[1].Mine {
		t.Fatalf("lost own-reaction flags: %+v %v", messages, err)
	}
}
