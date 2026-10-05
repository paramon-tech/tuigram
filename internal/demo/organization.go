package demo

import (
	"context"
	"errors"

	"github.com/paramon-tech/tuigram/internal/core"
)

var _ core.OrganizationClient = (*Client)(nil)

func cloneFolder(f core.ChatFolder) core.ChatFolder {
	f.PinnedIDs = append([]string(nil), f.PinnedIDs...)
	f.IncludeIDs = append([]string(nil), f.IncludeIDs...)
	f.ExcludeIDs = append([]string(nil), f.ExcludeIDs...)
	return f
}
func (c *Client) Folders(ctx context.Context) ([]core.ChatFolder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]core.ChatFolder, len(c.folders))
	for i, f := range c.folders {
		result[i] = cloneFolder(f)
	}
	return result, nil
}
func (c *Client) organize(ctx context.Context, chat core.Chat, change func(*core.Chat)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.chats {
		if c.chats[i].ID == chat.ID {
			change(&c.chats[i])
			return nil
		}
	}
	return errors.New("chat not found")
}
func (c *Client) SetChatPinned(ctx context.Context, chat core.Chat, value bool) error {
	return c.organize(ctx, chat, func(p *core.Chat) { p.Pinned = value })
}
func (c *Client) SetChatArchived(ctx context.Context, chat core.Chat, value bool) error {
	return c.organize(ctx, chat, func(p *core.Chat) { p.Archived = value; p.Pinned = false })
}
func (c *Client) SetChatMuted(ctx context.Context, chat core.Chat, value bool) error {
	return c.organize(ctx, chat, func(p *core.Chat) { p.Muted = value })
}
func (c *Client) CreateFolder(ctx context.Context, title string, chat core.Chat) (core.ChatFolder, error) {
	if err := ctx.Err(); err != nil {
		return core.ChatFolder{}, err
	}
	title, err := managementName(title, 12)
	if err != nil {
		return core.ChatFolder{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.hasChat(chat.ID) {
		return core.ChatFolder{}, errors.New("select a conversation first")
	}
	id := 2
	for _, f := range c.folders {
		if f.ID >= id {
			id = f.ID + 1
		}
	}
	f := core.ChatFolder{ID: id, Title: title, IncludeIDs: []string{chat.ID}}
	c.folders = append(c.folders, f)
	return cloneFolder(f), nil
}
func (c *Client) SetFolderChat(ctx context.Context, id int, chat core.Chat, include bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.hasChat(chat.ID) {
		return errors.New("chat not found")
	}
	for i := range c.folders {
		if c.folders[i].ID != id {
			continue
		}
		f := &c.folders[i]
		if f.Shared {
			return errors.New("shared folder membership cannot be edited here")
		}
		remove := func(ids []string) []string {
			out := make([]string, 0, len(ids))
			for _, item := range ids {
				if item != chat.ID {
					out = append(out, item)
				}
			}
			return out
		}
		f.IncludeIDs = remove(f.IncludeIDs)
		f.ExcludeIDs = remove(f.ExcludeIDs)
		if include {
			f.IncludeIDs = append(f.IncludeIDs, chat.ID)
		} else {
			f.PinnedIDs = remove(f.PinnedIDs)
			f.ExcludeIDs = append(f.ExcludeIDs, chat.ID)
		}
		return nil
	}
	return errors.New("folder not found")
}
