package demo

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/paramon-tech/tuigram/internal/core"
)

var _ core.MediaClient = (*Client)(nil)

func (c *Client) DownloadMedia(ctx context.Context, chat core.Chat, id int, dst io.Writer) (core.MediaFile, error) {
	if err := ctx.Err(); err != nil {
		return core.MediaFile{}, err
	}
	if dst == nil {
		return core.MediaFile{}, errors.New("download destination is required")
	}
	c.mu.Lock()
	var message core.Message
	for _, m := range c.messages[chat.ID] {
		if m.ID == id {
			message = m
			break
		}
	}
	upload, uploaded := c.uploads[id]
	c.mu.Unlock()
	if !message.Downloadable {
		return core.MediaFile{}, errors.New("message has no downloadable media")
	}
	var data []byte
	var file core.MediaFile
	switch {
	case uploaded:
		data, file = upload.data, upload.file
	case message.Image:
		var err error
		data, err = c.DownloadImage(ctx, chat, id)
		if err != nil {
			return core.MediaFile{}, err
		}
		file = core.MediaFile{Name: fmt.Sprintf("demo-photo-%d.png", id), MIMEType: "image/png"}
	case message.Voice:
		data = demoVoice()
		file = core.MediaFile{Name: fmt.Sprintf("demo-voice-%d.wav", id), MIMEType: "audio/wav"}
	default:
		return core.MediaFile{}, errors.New("message has no downloadable media")
	}
	if err := ctx.Err(); err != nil {
		return core.MediaFile{}, err
	}
	n, err := dst.Write(data)
	if err != nil {
		return core.MediaFile{}, err
	}
	if n != len(data) {
		return core.MediaFile{}, io.ErrShortWrite
	}
	if err := ctx.Err(); err != nil {
		return core.MediaFile{}, err
	}
	file.Size = int64(n)
	return file, nil
}

// demoVoice is an audible, account-free PCM sample, labeled as a tone in the
// fixture so it is not confused with a recorded spoken message.
func demoVoice() []byte {
	const sampleRate = 16000
	const samples = sampleRate
	data := make([]byte, 44+samples*2)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], sampleRate)
	binary.LittleEndian.PutUint32(data[28:32], sampleRate*2)
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:44], samples*2)
	for n := 0; n < samples; n++ {
		// Fade in and out to avoid clicks at the sample boundaries.
		envelope := math.Sin(math.Pi * float64(n) / samples)
		value := int16(6000 * envelope * math.Sin(2*math.Pi*440*float64(n)/sampleRate))
		binary.LittleEndian.PutUint16(data[44+2*n:], uint16(value))
	}
	return data
}
