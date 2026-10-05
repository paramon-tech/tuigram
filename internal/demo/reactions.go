package demo

import (
	"context"
	"errors"

	"github.com/paramon-tech/tuigram/internal/core"
)

func (c *Client) react(ctx context.Context, chat core.Chat, id int, emoji string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, message := range c.messages[chat.ID] {
		if message.ID != id {
			continue
		}
		// Replace only this user's reaction; preserve everyone else's counts.
		reactions := make([]core.Reaction, 0, len(message.Reactions)+1)
		for _, reaction := range message.Reactions {
			if reaction.Mine {
				reaction.Count--
				reaction.Mine = false
			}
			if reaction.Count > 0 {
				reactions = append(reactions, reaction)
			}
		}
		if emoji != "" {
			found := false
			for j := range reactions {
				if reactions[j].Emoji == emoji {
					reactions[j].Count++
					reactions[j].Mine = true
					found = true
					break
				}
			}
			if !found {
				reactions = append(reactions, core.Reaction{Emoji: emoji, Count: 1, Mine: true})
			}
		}
		c.messages[chat.ID][i].Reactions = reactions
		return nil
	}
	return errors.New("message not found")
}
