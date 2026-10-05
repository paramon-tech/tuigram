package demo

import (
	"context"
	"testing"

	"github.com/paramon-tech/tuigram/internal/core"
)

func TestReactionsReplaceOnlyOwnCountAndCanBeRemoved(t *testing.T) {
	c, ctx := New(), context.Background()
	chat := core.Chat{ID: "builders"}
	// Demo message starts with two other people's hearts.
	for _, emoji := range []string{"❤", "❤", "👍", ""} {
		if err := c.React(ctx, chat, 4, emoji); err != nil {
			t.Fatal(err)
		}
		messages, _ := c.History(ctx, chat, "")
		reactions := messages[0].Reactions
		hearts, thumbs, own := 0, 0, 0
		for _, reaction := range reactions {
			if reaction.Emoji == "❤" {
				hearts = reaction.Count
			}
			if reaction.Emoji == "👍" {
				thumbs = reaction.Count
			}
			if reaction.Mine {
				own++
			}
		}
		wantHearts, wantThumbs, wantOwn := 2, 0, 0
		if emoji == "❤" {
			wantHearts, wantOwn = 3, 1
		}
		if emoji == "👍" {
			wantThumbs, wantOwn = 1, 1
		}
		if hearts != wantHearts || thumbs != wantThumbs || own != wantOwn {
			t.Fatalf("reaction %q changed other users or duplicated own: %+v", emoji, reactions)
		}
	}
}
