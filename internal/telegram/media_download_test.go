package telegram

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/paramon-tech/tuigram/internal/core"
)

func mediaDocument() *tg.Message {
	return &tg.Message{ID: 9, PeerID: &tg.PeerUser{UserID: 1}, Media: &tg.MessageMediaDocument{Document: &tg.Document{
		ID: 99, AccessHash: 1234, FileReference: []byte("fresh-reference"), MimeType: "video/mp4", Size: 12345,
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "clip.mp4"}},
	}}}
}

func TestMediaMetadataPreservesDocumentAndChoosesLargestPhoto(t *testing.T) {
	location, file, err := mediaLocation(mediaDocument())
	if err != nil {
		t.Fatal(err)
	}
	document := location.(*tg.InputDocumentFileLocation)
	if document.ID != 99 || document.AccessHash != 1234 || string(document.FileReference) != "fresh-reference" || file.Name != "media-9-clip.mp4" || file.MIMEType != "video/mp4" || file.Size != 12345 {
		t.Fatalf("lost document data: %+v %+v", document, file)
	}
	message := &tg.Message{ID: 3, Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 7, Sizes: []tg.PhotoSizeClass{
		&tg.PhotoSize{Type: "s", Size: 1024}, &tg.PhotoSizeProgressive{Type: "w", Sizes: []int{10000, 20 << 20}},
	}}}}
	location, file, err = mediaLocation(message)
	if err != nil || location.(*tg.InputPhotoFileLocation).ThumbSize != "w" || file.Size != 20<<20 || file.Name != "photo-3.jpg" {
		t.Fatalf("photo: %+v %+v %v", location, file, err)
	}
}

func TestMediaRejectsProtectedAndOversizedMetadata(t *testing.T) {
	tests := map[string]struct {
		mutate func(*tg.Message)
		want   error
	}{
		"protected":     {func(m *tg.Message) { m.Noforwards = true }, errMediaProtected},
		"auto deletion": {func(m *tg.Message) { m.TTLPeriod = 60 }, errMediaProtected},
		"disappearing":  {func(m *tg.Message) { m.Media.(*tg.MessageMediaDocument).TTLSeconds = 5 }, errMediaProtected},
		"large video": {func(m *tg.Message) {
			m.Media.(*tg.MessageMediaDocument).Document.(*tg.Document).Size = core.MaxMediaBytes + 1
		}, errMediaTooLarge},
		"negative size": {func(m *tg.Message) { m.Media.(*tg.MessageMediaDocument).Document.(*tg.Document).Size = -1 }, errMediaTooLarge},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			message := mediaDocument()
			test.mutate(message)
			if _, _, err := mediaLocation(message); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestAttachmentNamesCannotChoosePathOrTerminalControls(t *testing.T) {
	for _, remote := range []string{"../../private.txt", `C:\Users\test\clip.mp4`, "\x1b[31m\nattack.mp4", "..", "-v.mp4", "CON", strings.Repeat("音", 300)} {
		name := attachmentName(9, remote, "video/mp4")
		if !strings.HasPrefix(name, "media-9-") || strings.ContainsAny(name, "/\\\x1b\n\r\x00") || len(name) > 180 || !utf8.ValidString(name) {
			t.Fatalf("unsafe name %q from %q", name, remote)
		}
	}
	if name := attachmentName(9, "", "audio/ogg"); !strings.HasSuffix(name, ".ogg") {
		t.Fatalf("missing playable extension: %q", name)
	}
}

func TestMediaFetchUsesCorrectRPCAndRequiresMatchingPeer(t *testing.T) {
	for _, channel := range []bool{false, true} {
		t.Run(map[bool]string{false: "private", true: "channel"}[channel], func(t *testing.T) {
			chat := core.Chat{ID: "user:1"}
			message := mediaDocument()
			if channel {
				chat.ID = "channel:2"
				message.PeerID = &tg.PeerChannel{ChannelID: 2}
			}
			c := testClient(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				switch request := input.(type) {
				case *tg.MessagesGetMessagesRequest:
					if channel || request.ID[0].(*tg.InputMessageID).ID != 9 {
						t.Fatalf("wrong message request: %+v", request)
					}
				case *tg.ChannelsGetMessagesRequest:
					peer := request.Channel.(*tg.InputChannel)
					if !channel || peer.ChannelID != 2 || peer.AccessHash != 456 || request.ID[0].(*tg.InputMessageID).ID != 9 {
						t.Fatalf("wrong channel request: %+v", request)
					}
				default:
					t.Fatalf("unexpected RPC %T", input)
				}
				output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{message}}
				return nil
			})
			got, err := c.mediaMessage(context.Background(), chat, 9)
			if err != nil || got.ID != 9 {
				t.Fatalf("fetch failed: %+v %v", got, err)
			}
			message.PeerID = &tg.PeerUser{UserID: 555}
			if _, err := c.DownloadMedia(context.Background(), chat, 9, io.Discard); err == nil {
				t.Fatal("download accepted a message from a different peer")
			}
		})
	}
}

func TestMediaFetchRefreshesAndRejectsChatProtection(t *testing.T) {
	c := testClient(func(_ context.Context, _ bin.Encoder, output bin.Decoder) error {
		message := mediaDocument()
		message.PeerID = &tg.PeerChannel{ChannelID: 2}
		output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesChannelMessages{Messages: []tg.MessageClass{message}, Chats: []tg.ChatClass{&tg.Channel{ID: 2, Noforwards: true}}}
		return nil
	})
	if _, err := c.DownloadMedia(context.Background(), core.Chat{ID: "channel:2"}, 9, io.Discard); !errors.Is(err, errMediaProtected) {
		t.Fatalf("protected chat download: %v", err)
	}
	c.remember(nil, []tg.ChatClass{&tg.Channel{ID: 2, Min: true}})
	record, _ := c.peer("channel:2")
	if !record.protected {
		t.Fatal("minimal entity dropped protection")
	}
}

type shortMediaWriter struct{}

func (shortMediaWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

func TestStreamingMediaBoundsCancellationAndPartialWrites(t *testing.T) {
	var dst bytes.Buffer
	w := &mediaOutput{ctx: context.Background(), dst: &dst, limit: 4}
	if _, err := w.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("5")); !errors.Is(err, errMediaTooLarge) || dst.Len() != 4 {
		t.Fatalf("stream exceeded actual-byte bound: %v, size %d", err, dst.Len())
	}
	ctx, cancel := context.WithCancel(context.Background())
	w = &mediaOutput{ctx: ctx, dst: &dst, limit: 10}
	cancel()
	if _, err := w.Write([]byte("5")); !errors.Is(err, context.Canceled) || dst.Len() != 4 {
		t.Fatalf("cancelled write: %v, size %d", err, dst.Len())
	}
	write := func(w io.Writer) error { _, err := w.Write([]byte("1234")); return err }
	file := core.MediaFile{Name: "clip.mp4", Size: 4}
	if _, err := streamMedia(context.Background(), file, shortMediaWriter{}, write); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write: %v", err)
	}
	if _, err := streamMedia(context.Background(), file, io.Discard, write); err != nil {
		t.Fatalf("complete stream: %v", err)
	}
	file.Size++
	if _, err := streamMedia(context.Background(), file, io.Discard, write); err == nil {
		t.Fatal("truncated attachment reported success")
	}
}

func TestVoiceMessageFlagsRespectProtection(t *testing.T) {
	c := testClient(nil)
	message := mediaDocument()
	media := message.Media.(*tg.MessageMediaDocument)
	media.Document.(*tg.Document).MimeType = "audio/ogg"
	media.Document.(*tg.Document).Attributes = []tg.DocumentAttributeClass{&tg.DocumentAttributeAudio{Voice: true}}
	result := c.message(core.Chat{ID: "user:1"}, message)
	if !result.Voice || !result.Downloadable || result.MediaLabel != "Voice message" {
		t.Fatalf("missing voice capability: %+v", result)
	}
	message.Noforwards = true
	if result := c.message(core.Chat{ID: "user:1"}, message); result.Downloadable {
		t.Fatal("protected voice is downloadable")
	}
}
