package telegram

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gotd/td/tg"
	"github.com/paramon-tech/tuigram/internal/core"
)

var (
	_ core.ManagementClient = (*client)(nil)
	_ core.ContactImporter  = (*client)(nil)
)

func managementName(value string, limit int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit {
		return "", fmt.Errorf("name must contain 1–%d valid characters", limit)
	}
	return value, nil
}

func (c *client) contactUser(contact core.Contact) (*tg.InputUser, error) {
	record, err := c.peer(contact.ID)
	if err != nil {
		return nil, err
	}
	peer, ok := record.input.(*tg.InputPeerUser)
	if !ok {
		return nil, errors.New("select another user account")
	}
	return &tg.InputUser{UserID: peer.UserID, AccessHash: peer.AccessHash}, nil
}

func (c *client) rememberUpdates(updates tg.UpdatesClass) []tg.ChatClass {
	switch result := updates.(type) {
	case *tg.Updates:
		c.remember(result.Users, result.Chats)
		return result.Chats
	case *tg.UpdatesCombined:
		c.remember(result.Users, result.Chats)
		return result.Chats
	}
	return nil
}

func (c *client) CreateGroup(ctx context.Context, title string, members []core.Contact) (core.Chat, error) {
	title, err := managementName(title, 128)
	if err != nil {
		return core.Chat{}, err
	}
	users := make([]tg.InputUserClass, 0, len(members))
	seen := make(map[string]bool)
	for _, member := range members {
		if seen[member.ID] {
			continue
		}
		user, err := c.contactUser(member)
		if err != nil {
			return core.Chat{}, err
		}
		seen[member.ID] = true
		users = append(users, user)
	}
	if len(users) == 0 || len(users) > 199 {
		return core.Chat{}, errors.New("select 1–199 contacts for the group")
	}
	response, err := c.api.MessagesCreateChat(ctx, &tg.MessagesCreateChatRequest{Users: users, Title: title})
	if err != nil {
		return core.Chat{}, err
	}
	chats := c.rememberUpdates(response.Updates)
	for _, item := range chats {
		if group, ok := item.(*tg.Chat); ok {
			chat := core.Chat{ID: peerID(&tg.PeerChat{ChatID: group.ID}), Title: group.Title, Kind: "group"}
			if len(response.MissingInvitees) > 0 {
				return chat, fmt.Errorf("group created, but Telegram could not invite %d selected contact(s)", len(response.MissingInvitees))
			}
			return chat, nil
		}
	}
	return core.Chat{}, errors.New("Telegram accepted group creation; refresh chats before trying again")
}

func (c *client) RenameChat(ctx context.Context, chat core.Chat, title string) error {
	title, err := managementName(title, 128)
	if err != nil {
		return err
	}
	record, err := c.peer(chat.ID)
	if err != nil {
		return err
	}
	var updates tg.UpdatesClass
	switch peer := record.input.(type) {
	case *tg.InputPeerChat:
		updates, err = c.api.MessagesEditChatTitle(ctx, &tg.MessagesEditChatTitleRequest{ChatID: peer.ChatID, Title: title})
	case *tg.InputPeerChannel:
		updates, err = c.api.ChannelsEditTitle(ctx, &tg.ChannelsEditTitleRequest{Channel: &tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash}, Title: title})
	default:
		return errors.New("private chat names come from contacts; edit the contact instead")
	}
	if err != nil {
		return err
	}
	c.rememberUpdates(updates)
	c.mu.Lock()
	record = c.peers[chat.ID]
	record.title = title
	c.peers[chat.ID] = record
	c.mu.Unlock()
	return nil
}

func (c *client) DeleteChat(ctx context.Context, chat core.Chat) error {
	record, err := c.peer(chat.ID)
	if err != nil {
		return err
	}
	switch peer := record.input.(type) {
	case *tg.InputPeerUser, *tg.InputPeerSelf:
		// Revoke stays false: the other person's copy is never removed. Leaving
		// JustClear false also removes the dialog, rather than only emptying it.
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			result, err := c.api.MessagesDeleteHistory(ctx, &tg.MessagesDeleteHistoryRequest{Peer: record.input, MaxID: 0})
			if err != nil {
				return err
			}
			if result.Offset <= 0 {
				return nil
			}
		}
	case *tg.InputPeerChat:
		_, err = c.api.MessagesDeleteChatUser(ctx, &tg.MessagesDeleteChatUserRequest{ChatID: peer.ChatID, UserID: &tg.InputUserSelf{}})
	case *tg.InputPeerChannel:
		_, err = c.api.ChannelsLeaveChannel(ctx, &tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash})
	default:
		return errors.New("this chat cannot be deleted")
	}
	return err
}

func (c *client) EditContact(ctx context.Context, contact core.Contact, name string) error {
	name, err := managementName(name, 64)
	if err != nil {
		return err
	}
	user, err := c.contactUser(contact)
	if err != nil {
		return err
	}
	// Re-adding an existing contact changes its local contact name. Do not
	// enable AddPhonePrivacyException: editing must not reveal our number.
	updates, err := c.api.ContactsAddContact(ctx, &tg.ContactsAddContactRequest{ID: user, FirstName: name})
	if err != nil {
		return err
	}
	c.rememberUpdates(updates)
	c.mu.Lock()
	record := c.peers[contact.ID]
	record.title, record.firstName, record.lastName = name, name, ""
	c.peers[contact.ID] = record
	c.mu.Unlock()
	return nil
}

func (c *client) DeleteContact(ctx context.Context, contact core.Contact) error {
	user, err := c.contactUser(contact)
	if err != nil {
		return err
	}
	updates, err := c.api.ContactsDeleteContacts(ctx, []tg.InputUserClass{user})
	if err != nil {
		return err
	}
	// Keep the peer: deleting an address-book entry does not delete messages.
	c.rememberUpdates(updates)
	return nil
}

func (c *client) savedContacts(ctx context.Context) ([]core.Contact, error) {
	response, err := c.api.ContactsGetContacts(ctx, 0)
	if err != nil {
		return nil, err
	}
	contacts, ok := response.(*tg.ContactsContacts)
	if !ok {
		return nil, errors.New("Telegram returned an unexpected contacts response")
	}
	c.remember(contacts.Users, nil)
	result := make([]core.Contact, 0, len(contacts.Contacts))
	for _, contact := range contacts.Contacts {
		id := peerID(&tg.PeerUser{UserID: contact.UserID})
		record, err := c.peer(id)
		if err != nil {
			continue
		}
		result = append(result, core.Contact{ID: id, Name: record.title, Username: record.username})
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name) })
	if len(result) > resultLimit {
		result = result[:resultLimit]
	}
	return result, nil
}

func (c *client) ImportContact(ctx context.Context, phone, name string) (core.Contact, error) {
	name, err := managementName(name, 64)
	if err != nil {
		return core.Contact{}, err
	}
	phone = strings.TrimSpace(phone)
	if len(phone) < 8 || len(phone) > 16 || phone[0] != '+' || strings.Trim(phone[1:], "0123456789") != "" {
		return core.Contact{}, errors.New("enter an international phone number, such as +14155552671")
	}
	id, err := randomID()
	if err != nil {
		return core.Contact{}, err
	}
	response, err := c.api.ContactsImportContacts(ctx, []tg.InputPhoneContact{{ClientID: id, Phone: phone, FirstName: name}})
	if err != nil {
		return core.Contact{}, err
	}
	c.remember(response.Users, nil)
	for _, imported := range response.Imported {
		if imported.ClientID != id {
			continue
		}
		peer := peerID(&tg.PeerUser{UserID: imported.UserID})
		record, err := c.peer(peer)
		if err != nil {
			return core.Contact{}, err
		}
		return core.Contact{ID: peer, Name: record.title, Username: record.username}, nil
	}
	if len(response.RetryContacts) > 0 {
		return core.Contact{}, errors.New("Telegram could not import this contact yet; try again later")
	}
	return core.Contact{}, errors.New("Telegram did not return a user for this number; the account may not exist or may restrict phone discovery")
}
