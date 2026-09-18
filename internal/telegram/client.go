package telegram

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	gotd "github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/paramon-tech/tuigram/internal/core"
)

// The initial client shows the most recent 1,000 dialogs and at most 100 messages
// per history or search request. It does not persist message history or contacts.
const resultLimit = 100

type peerRecord struct {
	input     tg.InputPeerClass
	title     string
	kind      string
	username  string
	firstName string
	lastName  string
}

type client struct {
	telegram  *gotd.Client
	api       *tg.Client
	mu        sync.RWMutex
	peers     map[string]peerRecord
	accountID int64 // Authenticated before this client is exposed to the TUI.
}

var _ core.Client = (*client)(nil)

func newClient(transport *gotd.Client) *client {
	return &client{telegram: transport, api: transport.API(), peers: make(map[string]peerRecord)}
}

func peerID(peer tg.PeerClass) string {
	switch p := peer.(type) {
	case *tg.PeerUser:
		return "user:" + strconv.FormatInt(p.UserID, 10)
	case *tg.PeerChat:
		return "group:" + strconv.FormatInt(p.ChatID, 10)
	case *tg.PeerChannel:
		return "channel:" + strconv.FormatInt(p.ChannelID, 10)
	default:
		return ""
	}
}

func (c *client) remember(users []tg.UserClass, chats []tg.ChatClass) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, item := range users {
		u, ok := item.(*tg.User)
		if !ok {
			continue
		}
		id := peerID(&tg.PeerUser{UserID: u.ID})
		input := tg.InputPeerClass(&tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash})
		if u.Self {
			input = &tg.InputPeerSelf{}
		}
		// A minimal entity may omit its access hash. Keep a full entity already
		// learned through dialogs or contact search instead of invalidating it.
		if existing, exists := c.peers[id]; exists && u.Min {
			input = existing.input
		}
		title := strings.TrimSpace(u.FirstName + " " + u.LastName)
		if title == "" {
			title = u.Username
		}
		if title == "" {
			title = "Deleted account"
		}
		c.peers[id] = peerRecord{input: input, title: title, kind: "private", username: u.Username, firstName: u.FirstName, lastName: u.LastName}
	}
	for _, item := range chats {
		var id string
		var record peerRecord
		switch chat := item.(type) {
		case *tg.Chat:
			id = peerID(&tg.PeerChat{ChatID: chat.ID})
			record = peerRecord{input: &tg.InputPeerChat{ChatID: chat.ID}, title: chat.Title, kind: "group"}
		case *tg.ChatForbidden:
			id = peerID(&tg.PeerChat{ChatID: chat.ID})
			record = peerRecord{input: &tg.InputPeerChat{ChatID: chat.ID}, title: chat.Title, kind: "group"}
		case *tg.Channel:
			id = peerID(&tg.PeerChannel{ChannelID: chat.ID})
			kind := "channel"
			if chat.Megagroup {
				kind = "group"
			}
			record = peerRecord{input: &tg.InputPeerChannel{ChannelID: chat.ID, AccessHash: chat.AccessHash}, title: chat.Title, kind: kind}
			if existing, exists := c.peers[id]; exists && chat.Min {
				record.input = existing.input
			}
		case *tg.ChannelForbidden:
			id = peerID(&tg.PeerChannel{ChannelID: chat.ID})
			kind := "channel"
			if chat.Megagroup {
				kind = "group"
			}
			record = peerRecord{input: &tg.InputPeerChannel{ChannelID: chat.ID, AccessHash: chat.AccessHash}, title: chat.Title, kind: kind}
		default:
			continue
		}
		c.peers[id] = record
	}
}

func (c *client) peer(id string) (peerRecord, error) {
	c.mu.RLock()
	record, ok := c.peers[id]
	c.mu.RUnlock()
	if !ok {
		return peerRecord{}, errors.New("chat is unavailable; refresh dialogs or search for the contact")
	}
	return record, nil
}

func (c *client) Dialogs(ctx context.Context) ([]core.Chat, error) {
	request := &tg.MessagesGetDialogsRequest{
		OffsetPeer: &tg.InputPeerEmpty{}, Limit: resultLimit,
	}
	chats := make([]core.Chat, 0, resultLimit)
	seen := make(map[string]bool)
	for page := 0; page < 10; page++ {
		response, err := c.api.MessagesGetDialogs(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("load dialogs: %w", err)
		}
		var dialogs []tg.DialogClass
		var messages []tg.MessageClass
		complete := false
		switch result := response.(type) {
		case *tg.MessagesDialogs:
			c.remember(result.Users, result.Chats)
			dialogs, messages, complete = result.Dialogs, result.Messages, true
		case *tg.MessagesDialogsSlice:
			c.remember(result.Users, result.Chats)
			dialogs, messages = result.Dialogs, result.Messages
		default:
			return nil, errors.New("Telegram returned an unexpected dialogs response")
		}
		var last *tg.Dialog
		added := 0
		for _, item := range dialogs {
			dialog, ok := item.(*tg.Dialog)
			if !ok {
				continue
			}
			id := peerID(dialog.Peer)
			record, err := c.peer(id)
			if err != nil {
				continue
			}
			last = dialog
			if seen[id] {
				continue
			}
			seen[id] = true
			chats = append(chats, core.Chat{ID: id, Title: record.title, Kind: record.kind, Unread: dialog.UnreadCount})
			added++
		}
		if complete || last == nil || added == 0 || len(dialogs) == 0 {
			break
		}
		record, _ := c.peer(peerID(last.Peer))
		request.OffsetID, request.OffsetPeer = last.TopMessage, record.input
		request.OffsetDate = 0
		for _, item := range messages {
			switch message := item.(type) {
			case *tg.Message:
				if message.ID == last.TopMessage && peerID(message.PeerID) == peerID(last.Peer) {
					request.OffsetDate = message.Date
				}
			case *tg.MessageService:
				if message.ID == last.TopMessage && peerID(message.PeerID) == peerID(last.Peer) {
					request.OffsetDate = message.Date
				}
			}
		}
		request.ExcludePinned = true
	}
	return chats, nil
}

func (c *client) unpack(response tg.MessagesMessagesClass) ([]tg.MessageClass, error) {
	switch result := response.(type) {
	case *tg.MessagesMessages:
		c.remember(result.Users, result.Chats)
		return result.Messages, nil
	case *tg.MessagesMessagesSlice:
		c.remember(result.Users, result.Chats)
		return result.Messages, nil
	case *tg.MessagesChannelMessages:
		c.remember(result.Users, result.Chats)
		return result.Messages, nil
	default:
		return nil, errors.New("Telegram returned an unexpected messages response")
	}
}

func (c *client) History(ctx context.Context, chat core.Chat, query string) ([]core.Message, error) {
	record, err := c.peer(chat.ID)
	if err != nil {
		return nil, err
	}
	var response tg.MessagesMessagesClass
	if strings.TrimSpace(query) == "" {
		response, err = c.api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: record.input, Limit: resultLimit})
	} else {
		response, err = c.api.MessagesSearch(ctx, &tg.MessagesSearchRequest{Peer: record.input, Q: query, Filter: &tg.InputMessagesFilterEmpty{}, Limit: resultLimit})
	}
	if err != nil {
		return nil, fmt.Errorf("load messages: %w", err)
	}
	messages, err := c.unpack(response)
	if err != nil {
		return nil, err
	}
	result := make([]core.Message, 0, len(messages))
	for _, item := range messages {
		if message, ok := item.(*tg.Message); ok {
			result = append(result, c.message(chat, message))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (c *client) message(chat core.Chat, message *tg.Message) core.Message {
	sender := chat.Title
	if record, err := c.peer(peerID(message.FromID)); err == nil {
		sender = record.title
	}
	_, forwarded := message.GetFwdFrom()
	result := core.Message{ID: message.ID, ChatID: chat.ID, Sender: sender, Text: message.Message,
		Time: time.Unix(int64(message.Date), 0), Outgoing: message.Out, Forwarded: forwarded}
	for _, count := range message.Reactions.Results {
		if emoji, ok := count.Reaction.(*tg.ReactionEmoji); ok {
			result.Reactions = append(result.Reactions, core.Reaction{Emoji: emoji.Emoticon, Count: count.Count})
		}
	}
	switch media := message.Media.(type) {
	case *tg.MessageMediaPhoto:
		result.Image = true
		result.MediaLabel = "Photo"
		if photo, ok := media.Photo.(*tg.Photo); ok && media.TTLSeconds == 0 {
			result.MediaKey = c.mediaKey(message, "photo", photo.ID)
		}
	case *tg.MessageMediaDocument:
		result.MediaLabel = "Attachment"
		if document, ok := media.Document.(*tg.Document); ok {
			result.Image = strings.HasPrefix(document.MimeType, "image/")
			if result.Image && media.TTLSeconds == 0 {
				result.MediaKey = c.mediaKey(message, "document", document.ID)
			}
			result.MediaLabel = document.MimeType
			for _, attr := range document.Attributes {
				if name, ok := attr.(*tg.DocumentAttributeFilename); ok {
					result.MediaLabel = name.FileName
				}
			}
		}
	case *tg.MessageMediaWebPage:
		if page, ok := media.Webpage.(*tg.WebPage); ok && !strings.Contains(result.Text, page.URL) {
			result.Text += "\n" + page.URL
		}
	}
	// Telegram text-url entities may hide a link behind a label. Preserve the
	// destination as plain text so terminal users can inspect it before opening.
	for _, entity := range message.Entities {
		if link, ok := entity.(*tg.MessageEntityTextURL); ok && !strings.Contains(result.Text, link.URL) {
			result.Text += "\n" + link.URL
		}
	}
	return result
}

func (c *client) mediaKey(message *tg.Message, kind string, mediaID int64) string {
	if c.accountID <= 0 || mediaID == 0 || message.Noforwards || message.TTLPeriod != 0 {
		return ""
	}
	return fmt.Sprintf("account:%d:%s:%d:edit:%d", c.accountID, kind, mediaID, message.EditDate)
}

func randomID() (int64, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, err
	}
	value := int64(binary.LittleEndian.Uint64(raw[:]))
	if value == 0 {
		value = 1
	}
	return value, nil
}

func (c *client) Send(ctx context.Context, chat core.Chat, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("message must not be empty")
	}
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > 4096 {
		return errors.New("message must be valid UTF-8 and at most 4096 characters")
	}
	record, err := c.peer(chat.ID)
	if err != nil {
		return err
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	_, err = c.api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{Peer: record.input, Message: text, RandomID: id})
	return err
}

func (c *client) Forward(ctx context.Context, source core.Chat, messageID int, destination core.Chat) error {
	if messageID <= 0 {
		return errors.New("select a message to forward")
	}
	from, err := c.peer(source.ID)
	if err != nil {
		return err
	}
	to, err := c.peer(destination.ID)
	if err != nil {
		return err
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	_, err = c.api.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{FromPeer: from.input, ToPeer: to.input, ID: []int{messageID}, RandomID: []int64{id}})
	return err
}

func (c *client) React(ctx context.Context, chat core.Chat, messageID int, emoji string) error {
	if messageID <= 0 {
		return errors.New("select a message to react to")
	}
	record, err := c.peer(chat.ID)
	if err != nil {
		return err
	}
	request := &tg.MessagesSendReactionRequest{Peer: record.input, MsgID: messageID}
	if emoji != "" {
		request.Reaction = []tg.ReactionClass{&tg.ReactionEmoji{Emoticon: emoji}}
	}
	_, err = c.api.MessagesSendReaction(ctx, request)
	return err
}

func (c *client) SearchContacts(ctx context.Context, query string) ([]core.Contact, error) {
	query = strings.TrimPrefix(strings.TrimSpace(query), "@")
	if query == "" {
		return nil, errors.New("enter a contact name or Telegram username")
	}
	response, err := c.api.ContactsSearch(ctx, &tg.ContactsSearchRequest{Q: query, Limit: resultLimit})
	if err != nil {
		return nil, err
	}
	c.remember(response.Users, response.Chats)
	result := make([]core.Contact, 0, len(response.Users))
	seen := make(map[string]bool)
	for _, peer := range append(response.MyResults, response.Results...) {
		if _, ok := peer.(*tg.PeerUser); !ok {
			continue
		}
		id := peerID(peer)
		record, err := c.peer(id)
		if err != nil || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, core.Contact{ID: id, Name: record.title, Username: record.username})
	}
	return result, nil
}

func (c *client) AddContact(ctx context.Context, contact core.Contact) error {
	record, err := c.peer(contact.ID)
	if err != nil {
		return err
	}
	peer, ok := record.input.(*tg.InputPeerUser)
	if !ok {
		return errors.New("only a user account can be added as a contact")
	}
	firstName := record.firstName
	if firstName == "" {
		firstName = record.title
	}
	_, err = c.api.ContactsAddContact(ctx, &tg.ContactsAddContactRequest{ID: &tg.InputUser{UserID: peer.UserID, AccessHash: peer.AccessHash}, FirstName: firstName, LastName: record.lastName})
	return err
}
