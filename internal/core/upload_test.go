package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func photoFile(t *testing.T) string {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 20, 10))); err != nil {
		t.Fatal(err)
	}
	return attachmentFile(t, "a photo.png", data.Bytes())
}

func attachmentFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mp4TestBox(kind string, children ...[]byte) []byte {
	data := bytes.Join(children, nil)
	box := make([]byte, 8+len(data))
	binary.BigEndian.PutUint32(box, uint32(len(box)))
	copy(box[4:8], kind)
	copy(box[8:], data)
	return box
}

// metadataVideo constructs only the container headers read by the inspector.
// It is not a playable fixture and is never transmitted to Telegram.
func metadataVideo(version byte, rotated bool, handler string, faststart bool) []byte {
	tkhd, mdhd := make([]byte, 84), make([]byte, 24)
	if version == 1 {
		tkhd, mdhd = make([]byte, 96), make([]byte, 36)
		binary.BigEndian.PutUint32(mdhd[20:], 1000)
		binary.BigEndian.PutUint64(mdhd[24:], 3500)
	} else {
		binary.BigEndian.PutUint32(mdhd[12:], 1000)
		binary.BigEndian.PutUint32(mdhd[16:], 3500)
	}
	tkhd[0], mdhd[0] = version, version
	offset := len(tkhd) - 8
	binary.BigEndian.PutUint32(tkhd[offset:], 640<<16)
	binary.BigEndian.PutUint32(tkhd[offset+4:], 480<<16)
	if !rotated {
		binary.BigEndian.PutUint32(tkhd[offset-36:], 1<<16)
		binary.BigEndian.PutUint32(tkhd[offset-20:], 1<<16)
	}
	hdlr := make([]byte, 12)
	copy(hdlr[8:], handler)
	ftyp := mp4TestBox("ftyp", []byte("isom\x00\x00\x00\x00isom"))
	moov := mp4TestBox("moov", mp4TestBox("trak", mp4TestBox("tkhd", tkhd), mp4TestBox("mdia", mp4TestBox("mdhd", mdhd), mp4TestBox("hdlr", hdlr))))
	mdat := mp4TestBox("mdat", []byte("fixture media bytes"))
	if faststart {
		return bytes.Join([][]byte{ftyp, moov, mdat}, nil)
	}
	return bytes.Join([][]byte{ftyp, mdat, moov}, nil)
}

func TestAttachmentInspection(t *testing.T) {
	info, err := InspectAttachment(context.Background(), Attachment{Path: photoFile(t), Caption: "A photo with spaces"})
	if err != nil || info.Kind != "photo" || info.MIMEType != "image/png" || info.Name != "a photo.png" || info.Width != 20 || info.Height != 10 {
		t.Fatalf("photo: %+v, %v", info, err)
	}
	for _, version := range []byte{0, 1} {
		for _, rotated := range []bool{false, true} {
			for _, faststart := range []bool{false, true} {
				path := attachmentFile(t, "a video.mp4", metadataVideo(version, rotated, "vide", faststart))
				info, err := InspectAttachment(context.Background(), Attachment{Path: path})
				width, height := 640, 480
				if rotated {
					width, height = height, width
				}
				if err != nil || info.Kind != "video" || info.MIMEType != "video/mp4" || info.Width != width || info.Height != height || info.Duration != 3.5 || info.SupportsStreaming != faststart {
					t.Fatalf("v%d rotated %t faststart %t: %+v, %v", version, rotated, faststart, info, err)
				}
			}
		}
	}
}

func TestAttachmentInspectsRealVideoFixture(t *testing.T) {
	// Generated with FFmpeg's blue color source, libx264, 320x240, 2 fps,
	// 2.5 seconds, yuv420p, and +faststart. No FFmpeg dependency at test time.
	info, err := InspectAttachment(context.Background(), Attachment{Path: "testdata/video.mp4"})
	if err != nil || info.Kind != "video" || info.MIMEType != "video/mp4" || info.Width != 320 || info.Height != 240 || info.Duration != 2.5 || !info.SupportsStreaming {
		t.Fatalf("real video: %+v %v", info, err)
	}
}

func TestAttachmentRejectsInvalidInputBeforeUpload(t *testing.T) {
	photo := photoFile(t)
	for name, attachment := range map[string]Attachment{
		"empty path":       {},
		"directory":        {Path: t.TempDir()},
		"empty file":       {Path: attachmentFile(t, "empty.png", nil)},
		"wrong type":       {Path: photo, Kind: "video"},
		"wrong kind":       {Path: photo, Kind: "unknown"},
		"invalid caption":  {Path: photo, Caption: "\xff"},
		"long caption":     {Path: photo, Caption: strings.Repeat("🙂", 1025)},
		"extension spoof":  {Path: attachmentFile(t, "fake.mp4", []byte("hello world")), Kind: "video"},
		"audio only":       {Path: attachmentFile(t, "audio.mp4", metadataVideo(0, false, "soun", true))},
		"truncated video":  {Path: attachmentFile(t, "clip.mp4", metadataVideo(0, false, "vide", true)[:36])},
		"truncated header": {Path: attachmentFile(t, "broken.png", []byte("\x89PNG\r\n\x1a\n"))},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := OpenAttachment(context.Background(), attachment); err == nil {
				t.Fatal("accepted invalid attachment")
			}
		})
	}
	large := attachmentFile(t, "large.png", nil)
	if err := os.Truncate(large, MaxMediaBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectAttachment(context.Background(), Attachment{Path: large}); err == nil {
		t.Fatal("accepted oversized file")
	}
	if err := os.Truncate(photo, MaxPhotoUploadBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectAttachment(context.Background(), Attachment{Path: photo}); err == nil {
		t.Fatal("accepted oversized photo")
	}
}

func TestAttachmentStreamingChecksSameHandleSizeAndCancellation(t *testing.T) {
	for _, action := range []string{"unchanged", "truncate", "append", "cancel", "replace path"} {
		t.Run(action, func(t *testing.T) {
			path := photoFile(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			file, err := OpenAttachment(ctx, Attachment{Path: path})
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			switch action {
			case "truncate":
				err = os.Truncate(path, 1)
			case "append":
				err = os.Truncate(path, file.Info.Size+10)
			case "cancel":
				cancel()
			case "replace path":
				err = os.Rename(path, path+".original")
				if err == nil {
					err = os.WriteFile(path, []byte("replacement"), 0600)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			n, readErr := io.Copy(io.Discard, file)
			if n > file.Info.Size {
				t.Fatal("read exceeded validated size")
			}
			err = file.VerifyComplete()
			if action == "unchanged" || action == "replace path" {
				if err != nil || readErr != nil || n != file.Info.Size {
					t.Fatalf("valid stream: %d %v %v", n, readErr, err)
				}
			} else if err == nil {
				t.Fatal("changed or canceled upload reported complete")
			}
			if action == "cancel" && (!errors.Is(readErr, context.Canceled) || !errors.Is(err, context.Canceled)) {
				t.Fatalf("cancellation lost: %v %v", readErr, err)
			}
		})
	}
}

func TestMP4MalformedBoxesAreBounded(t *testing.T) {
	for _, data := range [][]byte{
		{0, 0, 0, 7, 'm', 'o', 'o', 'v'},
		{0xff, 0xff, 0xff, 0xff, 'm', 'o', 'o', 'v'},
		{0, 0, 0, 1, 'm', 'o', 'o', 'v', 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		bytes.Repeat(mp4TestBox("free"), 10001),
	} {
		reader := mp4Reader{ctx: context.Background(), file: bytes.NewReader(data)}
		if err := reader.walk(0, int64(len(data)), func(mp4Box) error { return nil }); err == nil {
			t.Fatal("accepted invalid or excessively complex container")
		}
	}
}

func TestDocumentsAndSendAsFilePreserveOriginalContents(t *testing.T) {
	for _, path := range []string{photoFile(t), attachmentFile(t, "report.pdf", []byte("%PDF-1.7\nexample document")), attachmentFile(t, "notes.txt", []byte("notes with spaces"))} {
		file, err := OpenAttachment(context.Background(), Attachment{Path: path, Kind: "document"})
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(file)
		original, readErr := os.ReadFile(path)
		if err != nil || readErr != nil || file.Info.Kind != "document" || !bytes.Equal(data, original) || file.VerifyComplete() != nil {
			t.Fatalf("file changed: %+v %v", file.Info, err)
		}
		file.Close()
	}
	info, err := InspectAttachment(context.Background(), Attachment{Path: attachmentFile(t, "notes.txt", []byte("plain document"))})
	if err != nil || info.Kind != "document" || !strings.HasPrefix(info.MIMEType, "text/plain") {
		t.Fatalf("document autodetection: %+v %v", info, err)
	}
}

func TestAttachmentBatchValidationAndBounds(t *testing.T) {
	photo := photoFile(t)
	document := attachmentFile(t, "notes.txt", []byte("notes"))
	for _, attachments := range [][]Attachment{
		nil,
		make([]Attachment, MaxAttachmentCount+1),
		{{Path: photo}, {Path: "/does/not/exist"}},
		{{Path: photo}, {Path: document}},
	} {
		if files, err := OpenAttachments(context.Background(), attachments); err == nil || files != nil {
			t.Fatalf("accepted invalid batch: %+v %v", files, err)
		}
	}
	files, err := OpenAttachments(context.Background(), []Attachment{{Path: photo, Kind: "document"}, {Path: document}})
	if err != nil || len(files) != 2 || files[0].Info.Kind != "document" {
		t.Fatalf("document album: %+v %v", files, err)
	}
	for _, file := range files {
		file.Close()
	}
	large := attachmentFile(t, "large.bin", []byte("data"))
	if err := os.Truncate(large, MaxMediaBytes/2+1); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenAttachments(context.Background(), []Attachment{{Path: large}, {Path: large}}); err == nil {
		t.Fatal("accepted batch over total size limit")
	}
}
