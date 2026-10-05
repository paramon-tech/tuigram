package telegram

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/gotd/td/tg"
	"github.com/paramon-tech/tuigram/internal/core"
)

var _ core.HistoryClient = (*client)(nil)
var _ core.ReadClient = (*client)(nil)

func (c *client) HistoryPage(ctx context.Context, chat core.Chat, request core.HistoryRequest) (core.HistoryPage, error) {
	var page core.HistoryPage
	if err := ctx.Err(); err != nil {
		return page, err
	}
	if request.BeforeID < 0 || request.AfterID < 0 || request.BeforeID > math.MaxInt32 || request.AfterID > math.MaxInt32 || (request.BeforeID != 0 && request.AfterID != 0) || (request.Oldest && (request.BeforeID != 0 || request.AfterID != 0)) {
		return page, errors.New("invalid history cursor")
	}
	if request.AfterID == math.MaxInt32 {
		page.HasOlder = true
		return page, nil
	}
	record, err := c.peer(chat.ID)
	if err != nil {
		return page, err
	}
	limit := request.Limit
	if limit <= 0 || limit > resultLimit {
		limit = resultLimit
	}
	offset, addOffset := request.BeforeID, 0
	if request.Oldest {
		offset, addOffset = 1, -limit
	}
	if request.AfterID > 0 {
		// offsetFromID includes the boundary message. Move past it before
		// applying the negative offset, including for one-message pages.
		offset, addOffset = request.AfterID+1, -limit
	}
	var response tg.MessagesMessagesClass
	query := strings.TrimSpace(request.Query)
	if query == "" {
		response, err = c.api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: record.input, OffsetID: offset, AddOffset: addOffset, Limit: limit})
	} else {
		response, err = c.api.MessagesSearch(ctx, &tg.MessagesSearchRequest{Peer: record.input, Q: query, Filter: &tg.InputMessagesFilterEmpty{}, OffsetID: offset, AddOffset: addOffset, Limit: limit})
	}
	if err != nil {
		return page, fmt.Errorf("load messages: %w", err)
	}
	messages, err := c.unpack(response)
	if err != nil {
		return page, err
	}
	seen := make(map[int]bool, len(messages))
	for _, item := range messages {
		id := item.GetID()
		if id <= 0 || seen[id] || (request.BeforeID > 0 && id >= request.BeforeID) || (request.AfterID > 0 && id <= request.AfterID) {
			continue
		}
		seen[id] = true
		if page.OldestID == 0 || id < page.OldestID {
			page.OldestID = id
		}
		if id > page.NewestID {
			page.NewestID = id
		}
		if message, ok := item.(*tg.Message); ok {
			page.Messages = append(page.Messages, c.message(chat, message))
		}
	}
	sort.Slice(page.Messages, func(i, j int) bool { return page.Messages[i].ID < page.Messages[j].ID })
	more := len(messages) >= limit && page.OldestID > 0
	if request.Oldest || request.AfterID > 0 {
		page.HasOlder, page.HasNewer = request.AfterID > 0, more
	} else {
		page.HasOlder, page.HasNewer = more, request.BeforeID > 0
	}
	return page, nil
}

func (c *client) MarkRead(ctx context.Context, chat core.Chat, maxID int) (core.ReadState, error) {
	var state core.ReadState
	if err := ctx.Err(); err != nil {
		return state, err
	}
	// Zero means "all" in some Telegram methods; never allow it as a receipt.
	if maxID <= 0 || maxID > math.MaxInt32 {
		return state, errors.New("read receipt requires a positive message ID")
	}
	record, err := c.peer(chat.ID)
	if err != nil {
		return state, err
	}
	if peer, ok := record.input.(*tg.InputPeerChannel); ok {
		var accepted bool
		accepted, err = c.api.ChannelsReadHistory(ctx, &tg.ChannelsReadHistoryRequest{Channel: &tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash}, MaxID: maxID})
		if err == nil && !accepted {
			err = errors.New("Telegram did not accept the read receipt")
		}
	} else {
		_, err = c.api.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{Peer: record.input, MaxID: maxID})
	}
	if err != nil {
		return state, fmt.Errorf("mark messages read: %w", err)
	}
	if chat.UnreadMark {
		accepted, err := c.api.MessagesMarkDialogUnread(ctx, &tg.MessagesMarkDialogUnreadRequest{Peer: &tg.InputDialogPeer{Peer: record.input}, Unread: false})
		if err != nil {
			return state, fmt.Errorf("clear unread mark: %w", err)
		}
		if !accepted {
			return state, errors.New("Telegram did not clear the unread mark")
		}
	}
	// Fetch the remaining count rather than assuming the whole chat is read:
	// a newer message may have arrived since the displayed page was loaded.
	response, err := c.api.MessagesGetPeerDialogs(ctx, []tg.InputDialogPeerClass{&tg.InputDialogPeer{Peer: record.input}})
	if err != nil {
		return state, fmt.Errorf("refresh read state: %w", err)
	}
	c.remember(response.Users, response.Chats)
	for _, item := range response.Dialogs {
		if dialog, ok := item.(*tg.Dialog); ok && peerID(dialog.Peer) == chat.ID {
			return core.ReadState{MaxID: dialog.ReadInboxMaxID, Unread: dialog.UnreadCount, TopMessageID: dialog.TopMessage, UnreadMark: dialog.UnreadMark}, nil
		}
	}
	return state, errors.New("Telegram omitted the chat from the read-state response")
}
