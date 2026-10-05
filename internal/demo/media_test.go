package demo

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image/png"
	"io"
	"testing"

	"github.com/paramon-tech/tuigram/internal/core"
)

func TestDownloadDemoPhotoAndVoice(t *testing.T) {
	c := New()
	ctx := context.Background()
	var photo bytes.Buffer
	file, err := c.DownloadMedia(ctx, core.Chat{ID: "saved"}, 3, &photo)
	if err != nil || file.MIMEType != "image/png" || file.Size != int64(photo.Len()) {
		t.Fatalf("photo download: %+v %v", file, err)
	}
	if _, err := png.Decode(bytes.NewReader(photo.Bytes())); err != nil {
		t.Fatal(err)
	}
	var voice bytes.Buffer
	file, err = c.DownloadMedia(ctx, core.Chat{ID: "builders"}, 7, &voice)
	if err != nil || file.MIMEType != "audio/wav" || file.Size != int64(voice.Len()) {
		t.Fatalf("voice download: %+v %v", file, err)
	}
	data := voice.Bytes()
	if len(data) < 44 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" || binary.LittleEndian.Uint32(data[40:44]) != uint32(len(data)-44) {
		t.Fatal("demo audio is not a complete WAV sample")
	}
	if err := c.Forward(ctx, core.Chat{ID: "builders"}, 7, core.Chat{ID: "saved"}); err != nil {
		t.Fatal(err)
	}
	forwarded, _ := c.History(ctx, core.Chat{ID: "saved"}, "audio playback")
	if len(forwarded) != 1 || !forwarded[0].Voice {
		t.Fatal("forward lost playable media")
	}
	if _, err := c.DownloadMedia(ctx, core.Chat{ID: "saved"}, forwarded[0].ID, io.Discard); err != nil {
		t.Fatalf("forwarded voice: %v", err)
	}
}

func TestDownloadDemoRejectsWrongChatAndCancellation(t *testing.T) {
	c := New()
	if _, err := c.DownloadMedia(context.Background(), core.Chat{ID: "saved"}, 7, io.Discard); err == nil {
		t.Fatal("voice downloaded from wrong chat")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.DownloadMedia(ctx, core.Chat{ID: "saved"}, 3, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
