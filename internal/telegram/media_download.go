package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gotd/td/tg"
	"github.com/paramon-tech/tuigram/internal/core"
)

var (
	errMediaTooLarge  = errors.New("attachment exceeds the 512 MiB download limit")
	errMediaProtected = errors.New("protected or disappearing media cannot be saved or played externally")
)

var _ core.MediaClient = (*client)(nil)

// mediaOutput checks actual bytes as well as metadata, without buffering the
// attachment. It also prevents a short destination write from looking complete.
type mediaOutput struct {
	ctx     context.Context
	dst     io.Writer
	limit   int64
	written int64
}

func (w *mediaOutput) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(p)) > w.limit-w.written {
		return 0, errMediaTooLarge
	}
	n, err := w.dst.Write(p)
	w.written += int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

func downloadablePhoto(photo *tg.Photo) (tg.InputFileLocationClass, int64, error) {
	var thumb string
	var size int64
	for _, item := range photo.Sizes {
		var candidateType string
		var candidateSize int64
		switch candidate := item.(type) {
		case *tg.PhotoSize:
			candidateType, candidateSize = candidate.Type, int64(candidate.Size)
		case *tg.PhotoSizeProgressive:
			candidateType = candidate.Type
			for _, n := range candidate.Sizes {
				if int64(n) > candidateSize {
					candidateSize = int64(n)
				}
			}
		default:
			continue
		}
		if candidateSize > size && candidateSize <= core.MaxMediaBytes {
			thumb, size = candidateType, candidateSize
		}
	}
	if thumb == "" || photo.ID == 0 {
		return nil, 0, errors.New("photo has no available size within the 512 MiB download limit")
	}
	return &tg.InputPhotoFileLocation{ID: photo.ID, AccessHash: photo.AccessHash, FileReference: photo.FileReference, ThumbSize: thumb}, size, nil
}

// attachmentName deliberately prefixes every remote name, replaces separators
// and controls, and caps length. A server supplied name can never choose a path,
// a hidden file, an option for a media player, or a reserved Windows device name.
func attachmentName(id int, name, mime string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._- ", r) {
			return r
		}
		return '_'
	}, name)
	name = strings.Trim(name, ". -_")
	runes := []rune(name)
	if len(runes) > 100 {
		name = string(runes[:100])
	}
	for len(name) > 160 {
		_, n := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-n]
	}
	if name == "" {
		ext := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/gif": ".gif", "video/mp4": ".mp4", "video/webm": ".webm", "audio/ogg": ".ogg", "audio/opus": ".opus", "audio/mpeg": ".mp3", "audio/mp4": ".m4a", "audio/wav": ".wav"}[mime]
		name = "attachment" + ext
	}
	return fmt.Sprintf("media-%d-%s", id, name)
}

func mediaLocation(message *tg.Message) (tg.InputFileLocationClass, core.MediaFile, error) {
	if message.Noforwards || message.TTLPeriod != 0 {
		return nil, core.MediaFile{}, errMediaProtected
	}
	switch media := message.Media.(type) {
	case *tg.MessageMediaPhoto:
		if media.TTLSeconds != 0 {
			return nil, core.MediaFile{}, errMediaProtected
		}
		photo, ok := media.Photo.(*tg.Photo)
		if !ok {
			return nil, core.MediaFile{}, errors.New("photo is no longer available")
		}
		location, size, err := downloadablePhoto(photo)
		return location, core.MediaFile{Name: fmt.Sprintf("photo-%d.jpg", message.ID), MIMEType: "image/jpeg", Size: size}, err
	case *tg.MessageMediaDocument:
		if media.TTLSeconds != 0 {
			return nil, core.MediaFile{}, errMediaProtected
		}
		document, ok := media.Document.(*tg.Document)
		if !ok || document.ID == 0 {
			return nil, core.MediaFile{}, errors.New("attachment is no longer available")
		}
		if document.Size > core.MaxMediaBytes || document.Size < 0 {
			return nil, core.MediaFile{}, errMediaTooLarge
		}
		name := ""
		for _, attr := range document.Attributes {
			if filename, ok := attr.(*tg.DocumentAttributeFilename); ok {
				name = filename.FileName
				break
			}
		}
		file := core.MediaFile{Name: attachmentName(message.ID, name, document.MimeType), MIMEType: document.MimeType, Size: document.Size}
		return &tg.InputDocumentFileLocation{ID: document.ID, AccessHash: document.AccessHash, FileReference: document.FileReference}, file, nil
	default:
		return nil, core.MediaFile{}, errors.New("selected message has no downloadable attachment")
	}
}

func (c *client) mediaMessage(ctx context.Context, chat core.Chat, messageID int) (*tg.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if messageID <= 0 {
		return nil, errors.New("select a message containing media")
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
		return nil, fmt.Errorf("get media message: %w", err)
	}
	messages, err := c.unpack(response)
	if err != nil {
		return nil, err
	}
	if fresh, err := c.peer(chat.ID); err == nil && fresh.protected {
		return nil, errMediaProtected
	}
	for _, item := range messages {
		if message, ok := item.(*tg.Message); ok && message.ID == messageID && peerID(message.PeerID) == chat.ID {
			return message, nil
		}
	}
	return nil, errors.New("message is no longer available")
}

func streamMedia(ctx context.Context, file core.MediaFile, dst io.Writer, stream func(io.Writer) error) (core.MediaFile, error) {
	if dst == nil {
		return core.MediaFile{}, errors.New("download destination is required")
	}
	if err := ctx.Err(); err != nil {
		return core.MediaFile{}, err
	}
	output := &mediaOutput{ctx: ctx, dst: dst, limit: core.MaxMediaBytes}
	if err := stream(output); err != nil {
		return core.MediaFile{}, fmt.Errorf("download media: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return core.MediaFile{}, err
	}
	if output.written != file.Size {
		return core.MediaFile{}, errors.New("downloaded attachment size does not match Telegram metadata")
	}
	return file, nil
}

func (c *client) DownloadMedia(ctx context.Context, chat core.Chat, messageID int, dst io.Writer) (core.MediaFile, error) {
	if dst == nil {
		return core.MediaFile{}, errors.New("download destination is required")
	}
	message, err := c.mediaMessage(ctx, chat, messageID)
	if err != nil {
		return core.MediaFile{}, err
	}
	location, file, err := mediaLocation(message)
	if err != nil {
		return core.MediaFile{}, err
	}
	return streamMedia(ctx, file, dst, func(output io.Writer) error {
		_, err := c.telegram.Download(location).Stream(ctx, output)
		return err
	})
}
