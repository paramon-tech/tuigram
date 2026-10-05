package telegram

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/gotd/td/tg"
	"github.com/paramon-tech/tuigram/internal/core"
)

var _ core.OrganizationClient = (*client)(nil)

func (c *client) SetChatPinned(ctx context.Context, chat core.Chat, value bool) error {
	p, err := c.peer(chat.ID)
	if err != nil {
		return err
	}
	ok, err := c.api.MessagesToggleDialogPin(ctx, &tg.MessagesToggleDialogPinRequest{Pinned: value, Peer: &tg.InputDialogPeer{Peer: p.input}})
	return organizationResult(ok, err)
}
func (c *client) SetChatArchived(ctx context.Context, chat core.Chat, value bool) error {
	p, err := c.peer(chat.ID)
	if err != nil {
		return err
	}
	folder := 0
	if value {
		folder = 1
	}
	updates, err := c.api.FoldersEditPeerFolders(ctx, []tg.InputFolderPeer{{Peer: p.input, FolderID: folder}})
	if err == nil {
		c.rememberUpdates(updates)
	}
	return err
}
func (c *client) SetChatMuted(ctx context.Context, chat core.Chat, value bool) error {
	p, err := c.peer(chat.ID)
	if err != nil {
		return err
	}
	settings := tg.InputPeerNotifySettings{}
	until := 0
	if value {
		until = math.MaxInt32
	}
	settings.SetMuteUntil(until)
	// Send only mute_until; preserve previews, sounds, and story preferences.
	ok, err := c.api.AccountUpdateNotifySettings(ctx, &tg.AccountUpdateNotifySettingsRequest{Peer: &tg.InputNotifyPeer{Peer: p.input}, Settings: settings})
	return organizationResult(ok, err)
}
func organizationResult(ok bool, err error) error {
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("Telegram did not accept the organization change")
	}
	return nil
}

func (c *client) inputPeerID(peer tg.InputPeerClass) string {
	switch p := peer.(type) {
	case *tg.InputPeerUser:
		return peerID(&tg.PeerUser{UserID: p.UserID})
	case *tg.InputPeerChat:
		return peerID(&tg.PeerChat{ChatID: p.ChatID})
	case *tg.InputPeerChannel:
		return peerID(&tg.PeerChannel{ChannelID: p.ChannelID})
	case *tg.InputPeerSelf:
		return peerID(&tg.PeerUser{UserID: c.accountID})
	}
	return ""
}
func (c *client) folderIDs(peers []tg.InputPeerClass) []string {
	result := make([]string, 0, len(peers))
	for _, p := range peers {
		if id := c.inputPeerID(p); id != "" {
			result = append(result, id)
		}
	}
	return result
}
func (c *client) folder(filter tg.DialogFilterClass) (core.ChatFolder, bool) {
	switch f := filter.(type) {
	case *tg.DialogFilter:
		return core.ChatFolder{ID: f.ID, Title: f.Title.Text, PinnedIDs: c.folderIDs(f.PinnedPeers), IncludeIDs: c.folderIDs(f.IncludePeers), ExcludeIDs: c.folderIDs(f.ExcludePeers), Contacts: f.Contacts, NonContacts: f.NonContacts, Groups: f.Groups, Channels: f.Broadcasts, Bots: f.Bots, ExcludeMuted: f.ExcludeMuted, ExcludeRead: f.ExcludeRead, ExcludeArchived: f.ExcludeArchived}, true
	case *tg.DialogFilterChatlist:
		return core.ChatFolder{ID: f.ID, Title: f.Title.Text, Shared: true, PinnedIDs: c.folderIDs(f.PinnedPeers), IncludeIDs: c.folderIDs(f.IncludePeers)}, true
	}
	return core.ChatFolder{}, false
}
func (c *client) Folders(ctx context.Context) ([]core.ChatFolder, error) {
	response, err := c.loadDialogFilters(ctx, false)
	if err != nil {
		return nil, err
	}
	result := make([]core.ChatFolder, 0, len(response.Filters))
	for _, filter := range response.Filters {
		if f, ok := c.folder(filter); ok {
			result = append(result, f)
		}
	}
	return result, nil
}
func (c *client) CreateFolder(ctx context.Context, title string, chat core.Chat) (core.ChatFolder, error) {
	title, err := managementName(title, 12)
	if err != nil {
		return core.ChatFolder{}, err
	}
	peer, err := c.peer(chat.ID)
	if err != nil {
		return core.ChatFolder{}, err
	}
	response, err := c.loadDialogFilters(ctx, false)
	if err != nil {
		return core.ChatFolder{}, err
	}
	used := make(map[int]bool)
	for _, f := range response.Filters {
		if folder, ok := c.folder(f); ok {
			used[folder.ID] = true
		}
	}
	id := 2
	for used[id] {
		id++
	}
	filter := &tg.DialogFilter{ID: id, Title: tg.TextWithEntities{Text: title}, IncludePeers: []tg.InputPeerClass{peer.input}}
	request := &tg.MessagesUpdateDialogFilterRequest{ID: id}
	request.SetFilter(filter)
	defer c.invalidateDialogFilters()
	ok, err := c.api.MessagesUpdateDialogFilter(ctx, request)
	if err = organizationResult(ok, err); err != nil {
		return core.ChatFolder{}, err
	}
	folder, _ := c.folder(filter)
	return folder, nil
}
func (c *client) SetFolderChat(ctx context.Context, id int, chat core.Chat, include bool) error {
	p, err := c.peer(chat.ID)
	if err != nil {
		return err
	}
	response, err := c.loadDialogFilters(ctx, false)
	if err != nil {
		return err
	}
	for _, item := range response.Filters {
		folder, ok := c.folder(item)
		if !ok || folder.ID != id {
			continue
		}
		filter, ok := item.(*tg.DialogFilter)
		if !ok {
			return errors.New("shared folders can be viewed here; edit their membership in an official Telegram client")
		}
		// Refetch and mutate only membership, preserving existing rules/title/entities.
		clone := *filter
		remove := func(peers []tg.InputPeerClass) []tg.InputPeerClass {
			out := make([]tg.InputPeerClass, 0, len(peers))
			for _, peer := range peers {
				if c.inputPeerID(peer) != chat.ID {
					out = append(out, peer)
				}
			}
			return out
		}
		clone.IncludePeers = remove(filter.IncludePeers)
		clone.ExcludePeers = remove(filter.ExcludePeers)
		if include {
			pinned := false
			for _, peer := range clone.PinnedPeers {
				if c.inputPeerID(peer) == chat.ID {
					pinned = true
					break
				}
			}
			if !pinned {
				clone.IncludePeers = append(clone.IncludePeers, p.input)
			}
		} else {
			clone.PinnedPeers = remove(filter.PinnedPeers)
			clone.ExcludePeers = append(clone.ExcludePeers, p.input)
		}
		request := &tg.MessagesUpdateDialogFilterRequest{ID: id}
		request.SetFilter(&clone)
		defer c.invalidateDialogFilters()
		accepted, err := c.api.MessagesUpdateDialogFilter(ctx, request)
		return organizationResult(accepted, err)
	}
	return errors.New("folder no longer exists; reopen organization to refresh")
}

func (c *client) dialogChat(dialog *tg.Dialog, archived bool) (core.Chat, bool) {
	id := peerID(dialog.Peer)
	record, err := c.peer(id)
	if err != nil {
		return core.Chat{}, false
	}
	folder, hasFolder := dialog.GetFolderID()
	if hasFolder {
		archived = folder == 1
	}
	muted := dialog.NotifySettings.MuteUntil > int(time.Now().Unix())
	if _, explicit := dialog.NotifySettings.GetMuteUntil(); !explicit {
		c.mu.RLock()
		muted = c.notifyDefaults[record.kind]
		c.mu.RUnlock()
	}
	return core.Chat{ID: id, Title: record.title, Kind: record.kind, Unread: dialog.UnreadCount, UnreadMark: dialog.UnreadMark, Pinned: dialog.Pinned, Archived: archived, Muted: muted, Contact: record.contact, Bot: record.bot, TopMessageID: dialog.TopMessage, ReadInboxMaxID: dialog.ReadInboxMaxID}, true
}

func (c *client) organizedDialogs(ctx context.Context) ([]core.Chat, error) {
	if err := c.loadNotificationDefaults(ctx); err != nil {
		return nil, err
	}
	chats := make([]core.Chat, 0, resultLimit)
	seen := make(map[string]bool)
	add := func(items []tg.DialogClass, archived bool) {
		for _, item := range items {
			if d, ok := item.(*tg.Dialog); ok {
				if chat, ok := c.dialogChat(d, archived); ok && !seen[chat.ID] {
					seen[chat.ID] = true
					chats = append(chats, chat)
				}
			}
		}
	}
	for folder := 0; folder <= 1; folder++ {
		// Pinned dialogs have independent ordering and must never choose the date cursor.
		pinned, err := c.api.MessagesGetPinnedDialogs(ctx, folder)
		if err != nil {
			return nil, fmt.Errorf("load pinned chats: %w", err)
		}
		c.remember(pinned.Users, pinned.Chats)
		add(pinned.Dialogs, folder == 1)
		request := &tg.MessagesGetDialogsRequest{OffsetPeer: &tg.InputPeerEmpty{}, Limit: resultLimit, ExcludePinned: true}
		request.SetFolderID(folder)
		for page := 0; page < 10; page++ {
			response, err := c.api.MessagesGetDialogs(ctx, request)
			if err != nil {
				return nil, fmt.Errorf("load chats: %w", err)
			}
			var dialogs []tg.DialogClass
			var messages []tg.MessageClass
			complete := false
			switch r := response.(type) {
			case *tg.MessagesDialogs:
				c.remember(r.Users, r.Chats)
				dialogs, messages, complete = r.Dialogs, r.Messages, true
			case *tg.MessagesDialogsSlice:
				c.remember(r.Users, r.Chats)
				dialogs, messages = r.Dialogs, r.Messages
			default:
				return nil, errors.New("Telegram returned an unexpected dialogs response")
			}
			add(dialogs, folder == 1)
			var last *tg.Dialog
			for _, item := range dialogs {
				if d, ok := item.(*tg.Dialog); ok && !d.Pinned {
					last = d
				}
			}
			if complete || last == nil {
				break
			}
			record, err := c.peer(peerID(last.Peer))
			if err != nil {
				break
			}
			date := 0
			for _, item := range messages {
				switch msg := item.(type) {
				case *tg.Message:
					if msg.ID == last.TopMessage && peerID(msg.PeerID) == peerID(last.Peer) {
						date = msg.Date
					}
				case *tg.MessageService:
					if msg.ID == last.TopMessage && peerID(msg.PeerID) == peerID(last.Peer) {
						date = msg.Date
					}
				}
			}
			if request.OffsetID == last.TopMessage && request.OffsetDate == date && c.inputPeerID(request.OffsetPeer) == peerID(last.Peer) {
				break
			}
			request.OffsetID, request.OffsetDate, request.OffsetPeer = last.TopMessage, date, record.input
		}
	}
	// Explicit folder members may fall outside the recent-dialog window. Fetch their
	// current dialog metadata rather than displaying an empty or incomplete folder.
	filters, err := c.loadDialogFilters(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("load chat folders: %w", err)
	}
	var peers []tg.InputDialogPeerClass
	pending := make(map[string]bool)
	for _, filter := range filters.Filters {
		var members []tg.InputPeerClass
		switch f := filter.(type) {
		case *tg.DialogFilter:
			members = append(append([]tg.InputPeerClass(nil), f.PinnedPeers...), f.IncludePeers...)
		case *tg.DialogFilterChatlist:
			members = append(append([]tg.InputPeerClass(nil), f.PinnedPeers...), f.IncludePeers...)
		}
		for _, peer := range members {
			id := c.inputPeerID(peer)
			if id != "" && !seen[id] && !pending[id] {
				pending[id] = true
				peers = append(peers, &tg.InputDialogPeer{Peer: peer})
			}
		}
	}
	for len(peers) > 0 {
		n := min(resultLimit, len(peers))
		response, err := c.api.MessagesGetPeerDialogs(ctx, peers[:n])
		if err != nil {
			return nil, fmt.Errorf("load folder chats: %w", err)
		}
		c.remember(response.Users, response.Chats)
		add(response.Dialogs, false)
		peers = peers[n:]
	}
	return chats, nil
}

// Per-peer mute settings are optional; Telegram falls back to the chat type's
// notification settings. Cache them briefly so ordinary polling stays cheap.
func (c *client) loadNotificationDefaults(ctx context.Context) error {
	c.mu.RLock()
	fresh := c.notifyDefaults != nil && time.Since(c.notifyDefaultsAt) < time.Minute
	c.mu.RUnlock()
	if fresh {
		return nil
	}
	defaults := make(map[string]bool)
	for _, item := range []struct {
		kind string
		peer tg.InputNotifyPeerClass
	}{
		{"private", &tg.InputNotifyUsers{}}, {"group", &tg.InputNotifyChats{}}, {"channel", &tg.InputNotifyBroadcasts{}},
	} {
		settings, err := c.api.AccountGetNotifySettings(ctx, item.peer)
		if err != nil {
			return fmt.Errorf("load notification defaults: %w", err)
		}
		defaults[item.kind] = settings.MuteUntil > int(time.Now().Unix())
	}
	c.mu.Lock()
	c.notifyDefaults, c.notifyDefaultsAt = defaults, time.Now()
	c.mu.Unlock()
	return nil
}
