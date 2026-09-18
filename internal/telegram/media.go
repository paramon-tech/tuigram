package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gotd/td/tg"
	"github.com/paramon-tech/tuigram/internal/core"
)

const maxImageBytes = 8 << 20

var errImageTooLarge = errors.New("image exceeds the 8 MiB download limit")

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > maxImageBytes-b.Len() {
		return 0, errImageTooLarge
	}
	return b.Buffer.Write(p)
}

// photoLocation chooses the largest available photo that fits in the download
// limit. Stripped vector/preview thumbnails are not standalone image files.
func photoLocation(photo *tg.Photo) (tg.InputFileLocationClass, error) {
	var thumb string
	var size int
	for _, item := range photo.Sizes {
		var candidateType string
		var candidateSize int
		switch candidate := item.(type) {
		case *tg.PhotoSize:
			candidateType, candidateSize = candidate.Type, candidate.Size
		case *tg.PhotoSizeProgressive:
			candidateType = candidate.Type
			for _, value := range candidate.Sizes {
				if value > candidateSize {
					candidateSize = value
				}
			}
		default:
			continue
		}
		if candidateSize > size && candidateSize <= maxImageBytes {
			thumb, size = candidateType, candidateSize
		}
	}
	if thumb == "" {
		return nil, errors.New("photo has no downloadable size within the 8 MiB limit")
	}
	return &tg.InputPhotoFileLocation{ID: photo.ID, AccessHash: photo.AccessHash, FileReference: photo.FileReference, ThumbSize: thumb}, nil
}

func imageLocation(message *tg.Message) (tg.InputFileLocationClass, error) {
	switch media := message.Media.(type) {
	case *tg.MessageMediaPhoto:
		photo, ok := media.Photo.(*tg.Photo)
		if !ok {
			return nil, errors.New("photo is no longer available")
		}
		return photoLocation(photo)
	case *tg.MessageMediaDocument:
		document, ok := media.Document.(*tg.Document)
		if !ok || !strings.HasPrefix(document.MimeType, "image/") {
			return nil, errors.New("selected attachment is not an image")
		}
		if document.Size > maxImageBytes || document.Size < 0 {
			return nil, errImageTooLarge
		}
		return &tg.InputDocumentFileLocation{ID: document.ID, AccessHash: document.AccessHash, FileReference: document.FileReference}, nil
	default:
		return nil, errors.New("selected message has no image")
	}
}

// DownloadImage retrieves only the explicitly selected image. The metadata size
// and streaming writer both enforce an 8 MiB ceiling. No media is saved to disk.
func (c *client) DownloadImage(ctx context.Context, chat core.Chat, messageID int) ([]byte, error) {
	if messageID <= 0 {
		return nil, errors.New("select a message containing an image")
	}
	record, err := c.peer(chat.ID)
	if err != nil {
		return nil, err
	}
	ids := []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}}
	var response tg.MessagesMessagesClass
	if channel, ok := record.input.(*tg.InputPeerChannel); ok {
		response, err = c.api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash}, ID: ids})
	} else {
		response, err = c.api.MessagesGetMessages(ctx, ids)
	}
	if err != nil {
		return nil, fmt.Errorf("get image message: %w", err)
	}
	messages, err := c.unpack(response)
	if err != nil {
		return nil, err
	}
	for _, item := range messages {
		message, ok := item.(*tg.Message)
		if !ok || message.ID != messageID || peerID(message.PeerID) != chat.ID {
			continue
		}
		location, err := imageLocation(message)
		if err != nil {
			return nil, err
		}
		var output limitedBuffer
		if _, err := c.telegram.Download(location).Stream(ctx, &output); err != nil {
			return nil, fmt.Errorf("download image: %w", err)
		}
		return output.Bytes(), nil
	}
	return nil, errors.New("message is no longer available")
}
