package tui

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
)

const maxImageBytes = 10 << 20
const maxImagePixels = 16_000_000

// renderImage decodes only bounded images and emits our own RGB sequences.
// Telegram bytes are never passed through to the terminal as escape sequences.
func renderImage(data []byte, width, height int) (string, error) {
	if len(data) > maxImageBytes {
		return "", fmt.Errorf("image exceeds the 10 MiB preview limit")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("preview supports PNG, JPEG and GIF images: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > 16384 || config.Height > 16384 || int64(config.Width)*int64(config.Height) > maxImagePixels {
		return "", fmt.Errorf("image exceeds the 16 megapixel preview limit")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("decode image: %w", err)
	}
	width = max(1, min(width, 80))
	height = max(1, min(height, 30))
	w, h := config.Width, config.Height
	if w > width {
		h = max(1, h*width/w)
		w = width
	}
	if h > height*2 {
		w = max(1, w*height*2/h)
		h = height * 2
	}
	var out strings.Builder
	for y := 0; y < h; y += 2 {
		for x := 0; x < w; x++ {
			sx := img.Bounds().Min.X + x*config.Width/w
			sy := img.Bounds().Min.Y + y*config.Height/h
			r1, g1, b1, _ := img.At(sx, sy).RGBA()
			r2, g2, b2, _ := img.At(sx, img.Bounds().Min.Y+min(y+1, h-1)*config.Height/h).RGBA()
			fmt.Fprintf(&out, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", r1>>8, g1>>8, b1>>8, r2>>8, g2>>8, b2>>8)
		}
		out.WriteString("\x1b[0m")
		if y+2 < h {
			out.WriteByte('\n')
		}
	}
	return out.String(), nil
}
