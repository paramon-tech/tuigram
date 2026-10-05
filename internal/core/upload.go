package core

import (
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxPhotoUploadBytes int64 = 10 << 20
const MaxAttachmentCount = 10

// Attachment is a local file to send. Kind may be empty to detect photos and
// videos from their contents, or "photo", "video", or "document". Document
// always preserves the original file, including for photos and videos.
type Attachment struct {
	Path    string
	Caption string
	Kind    string
}

type AttachmentClient interface {
	SendAttachment(context.Context, Chat, Attachment) error
}

type BatchAttachmentClient interface {
	SendAttachments(context.Context, Chat, []Attachment) error
}

// OpenAttachments validates the complete batch before any upload starts, keeps
// the validated handles open, and bounds both the count and aggregate size.
// Telegram groups documents separately from photo/video albums.
func OpenAttachments(ctx context.Context, attachments []Attachment) (files []*AttachmentFile, err error) {
	if len(attachments) == 0 || len(attachments) > MaxAttachmentCount {
		return nil, fmt.Errorf("choose between 1 and %d attachments", MaxAttachmentCount)
	}
	defer func() {
		if err != nil {
			for _, file := range files {
				file.Close()
			}
			files = nil
		}
	}()
	var size int64
	for i, attachment := range attachments {
		file, openErr := OpenAttachment(ctx, attachment)
		if openErr != nil {
			return files, fmt.Errorf("attachment %d: %w", i+1, openErr)
		}
		files = append(files, file)
		size += file.Info.Size
		if size > MaxMediaBytes {
			return files, errors.New("attachments exceed the 512 MiB total upload limit")
		}
		if i > 0 && (file.Info.Kind == "document") != (files[0].Info.Kind == "document") {
			return files, errors.New("documents cannot share a photo/video album; enable send as files for the whole selection")
		}
	}
	return files, nil
}

type AttachmentInfo struct {
	Name              string
	Kind              string
	MIMEType          string
	Size              int64
	Width             int
	Height            int
	Duration          float64
	SupportsStreaming bool
}

// AttachmentFile keeps inspection and upload on the same open file. Read is
// bounded by the inspected size and observes cancellation between reads.
type AttachmentFile struct {
	Info   AttachmentInfo
	ctx    context.Context
	file   *os.File
	stat   os.FileInfo
	reader *io.SectionReader
	read   int64
}

func (f *AttachmentFile) Close() error { return f.file.Close() }

func (f *AttachmentFile) Read(p []byte) (int, error) {
	if err := f.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := f.reader.Read(p)
	f.read += int64(n)
	return n, err
}

// VerifyComplete must succeed after upload, before sending the message. In
// particular, a truncated file must not become a successfully sent attachment.
func (f *AttachmentFile) VerifyComplete() error {
	if err := f.ctx.Err(); err != nil {
		return err
	}
	stat, err := f.file.Stat()
	if err != nil {
		return err
	}
	if f.read != f.Info.Size || stat.Size() != f.Info.Size || !stat.ModTime().Equal(f.stat.ModTime()) {
		return errors.New("attachment changed while uploading; select it again")
	}
	return nil
}

func InspectAttachment(ctx context.Context, attachment Attachment) (AttachmentInfo, error) {
	file, err := OpenAttachment(ctx, attachment)
	if err != nil {
		return AttachmentInfo{}, err
	}
	defer file.Close()
	return file.Info, nil
}

func OpenAttachment(ctx context.Context, attachment Attachment) (_ *AttachmentFile, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !utf8.ValidString(attachment.Caption) || utf8.RuneCountInString(attachment.Caption) > 1024 {
		return nil, errors.New("caption must be valid UTF-8 and at most 1024 characters")
	}
	if attachment.Kind != "" && attachment.Kind != "photo" && attachment.Kind != "video" && attachment.Kind != "document" {
		return nil, errors.New("attachment kind must be photo, video, or document")
	}
	path := attachment.Path
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, path[2:])
	}
	// Reject pipes and devices before opening them: opening a pipe may block.
	stat, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("open attachment: %w", err)
	}
	if !stat.Mode().IsRegular() {
		return nil, errors.New("attachment must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open attachment: %w", err)
	}
	defer func() {
		if err != nil {
			file.Close()
		}
	}()
	stat, err = file.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() <= 0 {
		return nil, errors.New("attachment must be a nonempty regular file")
	}
	if stat.Size() > MaxMediaBytes {
		return nil, errors.New("attachment exceeds the 512 MiB upload limit")
	}
	info := AttachmentInfo{Name: uploadName(filepath.Base(path)), Size: stat.Size()}
	var signature [512]byte
	n, err := file.ReadAt(signature[:], 0)
	if err != nil && err != io.EOF {
		return nil, err
	}
	switch {
	case attachment.Kind == "document":
		info.Kind, info.MIMEType = "document", http.DetectContentType(signature[:n])
	case n >= 8 && (string(signature[:8]) == "\x89PNG\r\n\x1a\n" || signature[0] == 0xff && signature[1] == 0xd8):
		if stat.Size() > MaxPhotoUploadBytes {
			return nil, errors.New("photo exceeds the 10 MiB upload limit")
		}
		config, format, err := image.DecodeConfig(io.NewSectionReader(file, 0, stat.Size()))
		if err != nil || (format != "jpeg" && format != "png") {
			return nil, errors.New("attachment is not a valid JPEG or PNG photo")
		}
		if config.Width <= 0 || config.Height <= 0 || config.Width+config.Height > 10000 || config.Width > config.Height*20 || config.Height > config.Width*20 {
			return nil, errors.New("photo dimensions must total at most 10000 pixels and aspect ratio must be at most 20:1")
		}
		info.Kind, info.MIMEType = "photo", "image/"+format
		info.Width, info.Height = config.Width, config.Height
	case n >= 12 && string(signature[4:8]) == "ftyp":
		info.Kind, info.MIMEType = "video", "video/mp4"
		if string(signature[8:12]) == "qt  " {
			info.MIMEType = "video/quicktime"
		}
		if err := inspectMP4(ctx, file, &info); err != nil {
			return nil, fmt.Errorf("read video: %w", err)
		}
	default:
		info.Kind, info.MIMEType = "document", http.DetectContentType(signature[:n])
	}
	if attachment.Kind != "" && attachment.Kind != info.Kind {
		return nil, fmt.Errorf("selected file is a %s, not a %s", info.Kind, attachment.Kind)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &AttachmentFile{Info: info, ctx: ctx, file: file, stat: stat, reader: io.NewSectionReader(file, 0, stat.Size())}, nil
}

func uploadName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' || r == utf8.RuneError {
			return '_'
		}
		return r
	}, name)
	for len(name) > 200 {
		_, n := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-n]
	}
	return name
}
