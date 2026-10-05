package demo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/paramon-tech/tuigram/internal/core"
)

var (
	_ core.ManagementClient = (*Client)(nil)
	_ core.ContactImporter  = (*Client)(nil)
)

func managementName(value string, limit int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit {
		return "", fmt.Errorf("name must contain 1–%d valid characters", limit)
	}
	return value, nil
}

func (c *Client) CreateGroup(ctx context.Context, title string, members []core.Contact) (core.Chat, error) {
	if err := ctx.Err(); err != nil {
		return core.Chat{}, err
	}
	title, err := managementName(title, 128)
	if err != nil {
		return core.Chat{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := make(map[string]bool)
	for _, member := range members {
		known := false
		for _, contact := range c.contacts {
			if contact.ID == member.ID {
				known = true
				break
			}
		}
		if !known {
			return core.Chat{}, errors.New("contact not found")
		}
		seen[member.ID] = true
	}
	if len(seen) == 0 || len(seen) > 199 {
		return core.Chat{}, errors.New("select 1–199 contacts for the group")
	}
	chat := core.Chat{ID: fmt.Sprintf("demo-group:%d", c.nextID), Title: title, Kind: "group"}
	c.nextID++
	c.chats = append(c.chats, chat)
	c.messages[chat.ID] = nil
	return chat, nil
}

func (c *Client) RenameChat(ctx context.Context, chat core.Chat, title string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	title, err := managementName(title, 128)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, current := range c.chats {
		if current.ID != chat.ID {
			continue
		}
		if current.Kind == "private" {
			return errors.New("private chat names come from contacts; edit the contact instead")
		}
		c.chats[i].Title = title
		return nil
	}
	return errors.New("chat not found")
}

func (c *Client) DeleteChat(ctx context.Context, chat core.Chat) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, current := range c.chats {
		if current.ID == chat.ID {
			c.chats = append(c.chats[:i], c.chats[i+1:]...)
			for _, message := range c.messages[chat.ID] {
				delete(c.uploads, message.ID)
			}
			delete(c.messages, chat.ID)
			return nil
		}
	}
	// A private conversation can be opened from contacts before sending its
	// first message, so deleting an empty one should also succeed.
	if c.hasPeer(chat.ID) {
		delete(c.messages, chat.ID)
		return nil
	}
	return errors.New("chat not found")
}

func (c *Client) EditContact(ctx context.Context, contact core.Contact, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name, err := managementName(name, 64)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, current := range c.contacts {
		if current.ID != contact.ID {
			continue
		}
		c.contacts[i].Name = name
		for j, chat := range c.chats {
			if chat.ID == contact.ID {
				c.chats[j].Title = name
			}
		}
		return nil
	}
	return errors.New("contact not found")
}

func (c *Client) DeleteContact(ctx context.Context, contact core.Contact) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, current := range c.contacts {
		if current.ID == contact.ID {
			c.contacts = append(c.contacts[:i], c.contacts[i+1:]...)
			return nil
		}
	}
	return errors.New("contact not found")
}

func (c *Client) ImportContact(ctx context.Context, phone, name string) (core.Contact, error) {
	if err := ctx.Err(); err != nil {
		return core.Contact{}, err
	}
	name, err := managementName(name, 64)
	if err != nil {
		return core.Contact{}, err
	}
	phone = strings.TrimSpace(phone)
	if len(phone) < 8 || len(phone) > 16 || phone[0] != '+' || strings.Trim(phone[1:], "0123456789") != "" {
		return core.Contact{}, errors.New("enter an international phone number, such as +14155552671")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	contact := core.Contact{ID: "demo-phone:" + phone, Name: name}
	for i, current := range c.contacts {
		if current.ID == contact.ID {
			c.contacts[i] = contact
			return contact, nil
		}
	}
	c.contacts = append(c.contacts, contact)
	return contact, nil
}
