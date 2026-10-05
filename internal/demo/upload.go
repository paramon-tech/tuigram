package demo

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/paramon-tech/tuigram/internal/core"
)

type uploadedMedia struct {
	file core.MediaFile
	data []byte // Immutable after publication under Client.mu.
}

var _ core.AttachmentClient = (*Client)(nil)
var _ core.BatchAttachmentClient = (*Client)(nil)

func (c *Client) SendAttachment(ctx context.Context, chat core.Chat, attachment core.Attachment) error {
	return c.SendAttachments(ctx, chat, []core.Attachment{attachment})
}

func (c *Client) SendAttachments(ctx context.Context, chat core.Chat, attachments []core.Attachment) error {
	c.mu.Lock()
	known := c.hasPeer(chat.ID)
	c.mu.Unlock()
	if !known {
		return errors.New("chat not found")
	}
	files, err := core.OpenAttachments(ctx, attachments)
	if err != nil {
		return err
	}
	defer func() {
		for _, file := range files {
			file.Close()
		}
	}()
	data := make([][]byte, len(files))
	for i, file := range files {
		data[i], err = io.ReadAll(file)
		if err != nil {
			return err
		}
	}
	for _, file := range files {
		if err := file.VerifyComplete(); err != nil {
			return err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !c.hasPeer(chat.ID) {
		return errors.New("chat not found")
	}
	if !c.hasChat(chat.ID) {
		for _, contact := range c.contacts {
			if contact.ID == chat.ID {
				c.chats = append(c.chats, core.Chat{ID: contact.ID, Title: contact.Name, Kind: "private"})
				break
			}
		}
	}
	if c.uploads == nil {
		c.uploads = make(map[int]uploadedMedia)
	}
	for i, file := range files {
		c.uploads[c.nextID] = uploadedMedia{file: core.MediaFile{Name: file.Info.Name, MIMEType: file.Info.MIMEType, Size: file.Info.Size}, data: data[i]}
		c.messages[chat.ID] = append(c.messages[chat.ID], core.Message{ID: c.nextID, ChatID: chat.ID, Sender: "You", Text: attachments[i].Caption, Time: time.Now(), Outgoing: true, Image: file.Info.Kind == "photo", Downloadable: true, MediaLabel: file.Info.Name})
		c.nextID++
	}
	return nil
}
