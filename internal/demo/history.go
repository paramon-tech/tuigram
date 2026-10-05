package demo

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/paramon-tech/tuigram/internal/core"
)

var _ core.HistoryClient = (*Client)(nil)
var _ core.ReadClient = (*Client)(nil)

func (c *Client) HistoryPage(ctx context.Context, chat core.Chat, request core.HistoryRequest) (core.HistoryPage, error) {
	var page core.HistoryPage
	if request.BeforeID < 0 || request.AfterID < 0 || (request.BeforeID != 0 && request.AfterID != 0) || (request.Oldest && (request.BeforeID != 0 || request.AfterID != 0)) {
		return page, errors.New("invalid history cursor")
	}
	messages, err := c.History(ctx, chat, strings.TrimSpace(request.Query))
	if err != nil {
		return page, err
	}
	sort.Slice(messages, func(i, j int) bool { return messages[i].ID < messages[j].ID })
	limit := request.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	start, end := 0, len(messages)
	if request.BeforeID > 0 {
		end = sort.Search(len(messages), func(i int) bool { return messages[i].ID >= request.BeforeID })
	}
	if request.AfterID > 0 {
		start = sort.Search(len(messages), func(i int) bool { return messages[i].ID > request.AfterID })
	}
	if request.Oldest || request.AfterID > 0 {
		end = min(end, start+limit)
	} else {
		start = max(start, end-limit)
	}
	page.Messages = messages[start:end]
	page.HasOlder, page.HasNewer = start > 0, end < len(messages)
	if len(page.Messages) > 0 {
		page.OldestID, page.NewestID = page.Messages[0].ID, page.Messages[len(page.Messages)-1].ID
	}
	return page, nil
}

func (c *Client) MarkRead(ctx context.Context, chat core.Chat, maxID int) (core.ReadState, error) {
	var state core.ReadState
	if err := ctx.Err(); err != nil {
		return state, err
	}
	if maxID <= 0 {
		return state, errors.New("read receipt requires a positive message ID")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.chats {
		if c.chats[i].ID != chat.ID {
			continue
		}
		state.MaxID = max(maxID, c.chats[i].ReadInboxMaxID)
		for _, message := range c.messages[chat.ID] {
			state.TopMessageID = max(state.TopMessageID, message.ID)
			if !message.Outgoing && message.ID > state.MaxID {
				state.Unread++
			}
		}
		c.chats[i].ReadInboxMaxID = state.MaxID
		c.chats[i].TopMessageID = state.TopMessageID
		c.chats[i].Unread = state.Unread
		c.chats[i].UnreadMark = false
		return state, nil
	}
	return state, errors.New("chat not found")
}
