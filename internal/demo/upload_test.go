package demo

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/paramon-tech/tuigram/internal/core"
)

func demoUpload(t *testing.T) (core.Attachment, []byte) {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 30, 20))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "my photo.png")
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return core.Attachment{Path: path, Caption: "a photo with spaces"}, data.Bytes()
}

func TestDemoAttachmentSendPreviewDownloadAndForward(t *testing.T) {
	attachment, original := demoUpload(t)
	c, ctx := New(), context.Background()
	chat := core.Chat{ID: "ada", Title: "Ada", Kind: "private"}
	if err := c.SendAttachment(ctx, chat, attachment); err != nil {
		t.Fatal(err)
	}
	// The source is no longer needed after sending.
	if err := os.Remove(attachment.Path); err != nil {
		t.Fatal(err)
	}
	messages, err := c.History(ctx, chat, "photo with spaces")
	if err != nil || len(messages) != 1 {
		t.Fatalf("history: %+v %v", messages, err)
	}
	message := messages[0]
	if !message.Image || !message.Downloadable || !message.Outgoing || message.MediaLabel != "my photo.png" {
		t.Fatalf("lost upload metadata: %+v", message)
	}
	if preview, err := c.DownloadImage(ctx, chat, message.ID); err != nil || !bytes.Equal(preview, original) {
		t.Fatalf("preview changed image: %v", err)
	}
	var data bytes.Buffer
	file, err := c.DownloadMedia(ctx, chat, message.ID, &data)
	if err != nil || !bytes.Equal(data.Bytes(), original) || file.MIMEType != "image/png" || file.Size != int64(len(original)) {
		t.Fatalf("download changed image: %+v %v", file, err)
	}
	if _, err := c.DownloadMedia(ctx, core.Chat{ID: "saved"}, message.ID, io.Discard); err == nil {
		t.Fatal("downloaded from wrong chat")
	}
	if err := c.Forward(ctx, chat, message.ID, core.Chat{ID: "saved"}); err != nil {
		t.Fatal(err)
	}
	forwarded, _ := c.History(ctx, core.Chat{ID: "saved"}, "photo with spaces")
	if len(forwarded) != 1 {
		t.Fatal("forward missing")
	}
	if err := c.DeleteChat(ctx, chat); err != nil {
		t.Fatal(err)
	}
	data.Reset()
	if _, err := c.DownloadMedia(ctx, core.Chat{ID: "saved"}, forwarded[0].ID, &data); err != nil || !bytes.Equal(data.Bytes(), original) {
		t.Fatalf("forward lost media when source chat deleted: %v", err)
	}
	if _, ok := c.uploads[message.ID]; ok {
		t.Fatal("delete retained original attachment")
	}
}

func TestDemoAttachmentCanceledOrInvalidSendDoesNotMutateHistory(t *testing.T) {
	c, ctx := New(), context.Background()
	chat := core.Chat{ID: "saved"}
	before, _ := c.History(ctx, chat, "")
	attachment, _ := demoUpload(t)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.SendAttachment(canceled, chat, attachment); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	attachment.Path = t.TempDir()
	if err := c.SendAttachment(ctx, chat, attachment); err == nil {
		t.Fatal("accepted invalid attachment")
	}
	after, _ := c.History(ctx, chat, "")
	if len(before) != len(after) || len(c.uploads) != 0 {
		t.Fatal("failed send changed history")
	}
}

func TestDemoVideoCanBeDownloadedAfterSend(t *testing.T) {
	data, err := os.ReadFile("../core/testdata/video.mp4")
	if err != nil {
		t.Fatal(err)
	}
	c, ctx := New(), context.Background()
	chat := core.Chat{ID: "saved"}
	attachment := core.Attachment{Path: "../core/testdata/video.mp4", Caption: "my video", Kind: "video"}
	if err := c.SendAttachment(ctx, chat, attachment); err != nil {
		t.Fatal(err)
	}
	messages, _ := c.History(ctx, chat, "my video")
	if len(messages) != 1 || messages[0].Image || !messages[0].Downloadable {
		t.Fatalf("invalid video: %+v", messages)
	}
	var result bytes.Buffer
	file, err := c.DownloadMedia(ctx, chat, messages[0].ID, &result)
	if err != nil || !bytes.Equal(data, result.Bytes()) || file.MIMEType != "video/mp4" {
		t.Fatalf("video changed: %+v %v", file, err)
	}
}

func TestDemoDocumentAlbumPublishesAllOrNone(t *testing.T) {
	c, ctx := New(), context.Background()
	chat := core.Chat{ID: "saved"}
	photo, original := demoUpload(t)
	photo.Kind, photo.Caption = "document", "document album"
	before, _ := c.History(ctx, chat, "")
	if err := c.SendAttachments(ctx, chat, []core.Attachment{photo, {Path: t.TempDir()}}); err == nil {
		t.Fatal("invalid album reported success")
	}
	after, _ := c.History(ctx, chat, "")
	if len(after) != len(before) || len(c.uploads) != 0 {
		t.Fatal("invalid later file partially published album")
	}
	if err := c.SendAttachments(ctx, chat, []core.Attachment{photo, photo}); err != nil {
		t.Fatal(err)
	}
	messages, _ := c.History(ctx, chat, "document album")
	if len(messages) != 2 {
		t.Fatalf("album missing: %+v", messages)
	}
	for _, message := range messages {
		var downloaded bytes.Buffer
		if message.Image || !message.Downloadable {
			t.Fatal("document shown as compressed photo")
		}
		if _, err := c.DownloadMedia(ctx, chat, message.ID, &downloaded); err != nil || !bytes.Equal(downloaded.Bytes(), original) {
			t.Fatal("document original not downloadable")
		}
	}
}
