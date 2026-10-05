package core

import (
	"context"
	"io"
)

// MaxMediaBytes bounds explicit attachment downloads independently of previews.
const MaxMediaBytes int64 = 512 << 20

// MediaFile describes a completely downloaded attachment. Name is a safe base
// name, never a path; callers choose the destination and must remove partial
// output after an error. Size is the number of bytes written.
type MediaFile struct {
	Name     string
	MIMEType string
	Size     int64
}

// MediaClient optionally extends Client with explicit, streaming downloads.
// Implementations respect cancellation and do not write protected or ephemeral
// attachments. The destination must remain valid until DownloadMedia returns.
type MediaClient interface {
	DownloadMedia(context.Context, Chat, int, io.Writer) (MediaFile, error)
}
