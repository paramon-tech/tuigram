package core

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
)

// mp4Box only records ranges; even a large mdat is skipped without allocation.
type mp4Box struct {
	kind       string
	start, end int64
}

type mp4Reader struct {
	ctx   context.Context
	file  io.ReaderAt
	boxes int
}

func (r *mp4Reader) walk(start, end int64, visit func(mp4Box) error) error {
	for start < end {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		r.boxes++
		if r.boxes > 10000 || end-start < 8 {
			return errors.New("invalid or excessively complex MP4 container")
		}
		var header [16]byte
		if _, err := r.file.ReadAt(header[:8], start); err != nil {
			return err
		}
		size, headerSize := int64(binary.BigEndian.Uint32(header[:4])), int64(8)
		if size == 1 {
			if end-start < 16 {
				return io.ErrUnexpectedEOF
			}
			if _, err := r.file.ReadAt(header[8:], start+8); err != nil {
				return err
			}
			size, headerSize = int64(binary.BigEndian.Uint64(header[8:])), 16
		} else if size == 0 {
			size = end - start
		}
		if size < headerSize || size > end-start {
			return errors.New("invalid MP4 box length")
		}
		if err := visit(mp4Box{kind: string(header[4:8]), start: start + headerSize, end: start + size}); err != nil {
			return err
		}
		start += size
	}
	return nil
}

func (r *mp4Reader) prefix(box mp4Box, dst []byte) error {
	if box.end-box.start < int64(len(dst)) {
		return io.ErrUnexpectedEOF
	}
	_, err := r.file.ReadAt(dst, box.start)
	return err
}

func inspectMP4(ctx context.Context, file io.ReaderAt, info *AttachmentInfo) error {
	r := mp4Reader{ctx: ctx, file: file}
	var movie, data bool
	err := r.walk(0, info.Size, func(box mp4Box) error {
		switch box.kind {
		case "mdat":
			data = box.end > box.start
		case "moov":
			movie = true
			info.SupportsStreaming = !data
			return r.walk(box.start, box.end, func(child mp4Box) error {
				if child.kind == "trak" {
					return r.track(child, info)
				}
				return nil
			})
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !movie || !data || info.Width <= 0 || info.Height <= 0 || info.Duration <= 0 {
		return errors.New("MP4/MOV requires a video track with dimensions, duration, and media data")
	}
	return nil
}

func (r *mp4Reader) track(box mp4Box, info *AttachmentInfo) error {
	var width, height int
	var duration float64
	var video bool
	err := r.walk(box.start, box.end, func(child mp4Box) error {
		switch child.kind {
		case "tkhd":
			var version [1]byte
			if err := r.prefix(child, version[:]); err != nil {
				return err
			}
			offset := 76
			if version[0] == 1 {
				offset = 88
			} else if version[0] != 0 {
				return errors.New("unsupported MP4 track header version")
			}
			var data [96]byte
			if err := r.prefix(child, data[:offset+8]); err != nil {
				return err
			}
			width = int(binary.BigEndian.Uint32(data[offset:]) >> 16)
			height = int(binary.BigEndian.Uint32(data[offset+4:]) >> 16)
			// A quarter-turn matrix changes the displayed dimensions.
			matrix := offset - 36
			if binary.BigEndian.Uint32(data[matrix:]) == 0 && binary.BigEndian.Uint32(data[matrix+16:]) == 0 {
				width, height = height, width
			}
		case "mdia":
			return r.walk(child.start, child.end, func(media mp4Box) error {
				switch media.kind {
				case "hdlr":
					var data [12]byte
					if err := r.prefix(media, data[:]); err != nil {
						return err
					}
					video = string(data[8:12]) == "vide"
				case "mdhd":
					var data [32]byte
					if err := r.prefix(media, data[:20]); err != nil {
						return err
					}
					scale, ticks := uint32(0), uint64(0)
					switch data[0] {
					case 0:
						scale = binary.BigEndian.Uint32(data[12:16])
						ticks = uint64(binary.BigEndian.Uint32(data[16:20]))
					case 1:
						if err := r.prefix(media, data[:]); err != nil {
							return err
						}
						scale = binary.BigEndian.Uint32(data[20:24])
						ticks = binary.BigEndian.Uint64(data[24:32])
					default:
						return errors.New("unsupported MP4 media header version")
					}
					if scale > 0 {
						duration = float64(ticks) / float64(scale)
					}
				}
				return nil
			})
		}
		return nil
	})
	if err == nil && video && width > 0 && height > 0 && duration > 0 {
		info.Width, info.Height, info.Duration = width, height, duration
	}
	return err
}
