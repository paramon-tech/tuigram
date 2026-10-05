package telegram

import (
	"context"
	"errors"
	"fmt"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/paramon-tech/tuigram/internal/core"
)

var _ core.AttachmentClient = (*client)(nil)
var _ core.BatchAttachmentClient = (*client)(nil)

func (c *client) SendAttachment(ctx context.Context, chat core.Chat, attachment core.Attachment) error {
	return c.SendAttachments(ctx, chat, []core.Attachment{attachment})
}

func (c *client) SendAttachments(ctx context.Context, chat core.Chat, attachments []core.Attachment) error {
	record, err := c.peer(chat.ID)
	if err != nil {
		return err
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
	media := make([]tg.InputSingleMedia, 0, len(files))
	for i, file := range files {
		uploaded, err := uploader.NewUploader(c.api).Upload(ctx, uploader.NewUpload(file.Info.Name, file, file.Info.Size))
		if err != nil {
			return fmt.Errorf("upload attachment %d: %w", i+1, err)
		}
		if err := file.VerifyComplete(); err != nil {
			return err
		}
		input := uploadedAttachment(file.Info, uploaded)
		if len(files) > 1 {
			// sendMultiMedia requires server references, not uploaded constructors.
			// uploadMedia prepares media without publishing any messages.
			prepared, err := c.api.MessagesUploadMedia(ctx, &tg.MessagesUploadMediaRequest{Peer: record.input, Media: input})
			if err != nil {
				return fmt.Errorf("prepare attachment %d: %w", i+1, err)
			}
			input, err = preparedAttachment(prepared)
			if err != nil {
				return err
			}
		}
		id, err := randomID()
		if err != nil {
			return err
		}
		media = append(media, tg.InputSingleMedia{Media: input, Message: attachments[i].Caption, RandomID: id})
	}
	// Recheck earlier files too: one might change while a later file uploads.
	for _, file := range files {
		if err := file.VerifyComplete(); err != nil {
			return err
		}
	}
	if len(media) == 1 {
		_, err = c.api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{Peer: record.input, Media: media[0].Media, Message: media[0].Message, RandomID: media[0].RandomID})
	} else {
		_, err = c.api.MessagesSendMultiMedia(ctx, &tg.MessagesSendMultiMediaRequest{Peer: record.input, MultiMedia: media})
	}
	if err != nil {
		// Never automatically repeat an ambiguous publishing request.
		return fmt.Errorf("send attachment(s): %w", err)
	}
	return nil
}

func uploadedAttachment(info core.AttachmentInfo, file tg.InputFileClass) tg.InputMediaClass {
	if info.Kind == "photo" {
		return &tg.InputMediaUploadedPhoto{File: file}
	}
	attributes := []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: info.Name}}
	if info.Kind == "video" {
		attributes = append(attributes, &tg.DocumentAttributeVideo{W: info.Width, H: info.Height, Duration: info.Duration, SupportsStreaming: info.SupportsStreaming})
	}
	return &tg.InputMediaUploadedDocument{File: file, MimeType: info.MIMEType, Attributes: attributes, ForceFile: info.Kind == "document"}
}

func preparedAttachment(media tg.MessageMediaClass) (tg.InputMediaClass, error) {
	switch value := media.(type) {
	case *tg.MessageMediaPhoto:
		if photo, ok := value.Photo.(*tg.Photo); ok && photo.ID != 0 {
			return &tg.InputMediaPhoto{ID: &tg.InputPhoto{ID: photo.ID, AccessHash: photo.AccessHash, FileReference: photo.FileReference}}, nil
		}
	case *tg.MessageMediaDocument:
		if document, ok := value.Document.(*tg.Document); ok && document.ID != 0 {
			return &tg.InputMediaDocument{ID: &tg.InputDocument{ID: document.ID, AccessHash: document.AccessHash, FileReference: document.FileReference}}, nil
		}
	}
	return nil, errors.New("Telegram did not return a reusable attachment; nothing was sent")
}
