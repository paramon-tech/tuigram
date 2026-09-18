package tui

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func TestSanitizeTerminalAndBidiSequences(t *testing.T) {
	tests := map[string]string{
		"hello 👨‍👩‍👧‍👦":                                  "hello 👨‍👩‍👧‍👦",
		"before\x1b[2Jafter":                             "beforeafter",
		"\x1b[31mred\x1b[0m":                             "red",
		"a\x1b]52;c;c2VjcmV0\ab":                         "ab",
		"a\x1b]8;;https://evil\x1b\\link\x1b]8;;\x1b\\b": "alinkb",
		"a\x1bPmalicious\x1b\\b":                         "ab",
		"a\u009b2Jb":                                     "ab",
		"a\u009d52;c;secret\u009cb":                      "ab",
		"x\u202ey\u2066z\u2069":                          "xyz",
		"a\r\x00\x07b\n\tc":                              "ab\n    c",
		"https://example.com?q=hello":                    "https://example.com?q=hello",
		"safe\x1b":                                       "safe",
		"safe\x1b]unterminated":                          "safe",
	}
	for input, want := range tests {
		if got := Sanitize(input); got != want {
			t.Errorf("Sanitize(%q)=%q; want %q", input, got, want)
		}
	}
}

func FuzzSanitize(f *testing.F) {
	f.Add("Hello 👋\x1b]52;c;secret\a\u202e")
	f.Fuzz(func(t *testing.T, s string) {
		got := Sanitize(s)
		if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\u202e') || strings.ContainsRune(got, '\u009b') {
			t.Fatalf("unsafe sanitized result: %q", got)
		}
		if got != Sanitize(got) {
			t.Fatal("sanitizing twice changed output")
		}
	})
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 1, color.RGBA{B: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestImagePreviewAndResourceLimits(t *testing.T) {
	data := tinyPNG(t)
	preview, err := renderImage(data, 20, 10)
	if err != nil || !strings.Contains(preview, "▀") || !strings.Contains(preview, "38;2;255;0;0") {
		t.Fatalf("image not rendered: %q, %v", preview, err)
	}
	if _, err := renderImage(make([]byte, maxImageBytes+1), 20, 10); err == nil {
		t.Fatal("oversized encoded image accepted")
	}
	if _, err := renderImage([]byte("\x1b]52;c;bad\a"), 20, 10); err == nil {
		t.Fatal("terminal bytes accepted as image")
	}
	// Modify the PNG IHDR before decoding; the decoder must reject dimensions
	// before allocating a pixel buffer.
	huge := append([]byte{}, data...)
	binary.BigEndian.PutUint32(huge[16:20], 100000)
	binary.BigEndian.PutUint32(huge[29:33], crc32.ChecksumIEEE(huge[12:29]))
	if _, err := renderImage(huge, 20, 10); err == nil || !strings.Contains(err.Error(), "megapixel") {
		t.Fatalf("dimension limit not applied: %v", err)
	}
}

type memoryCache struct {
	values     map[string][]byte
	gets, puts int
}

func (c *memoryCache) Get(k string) ([]byte, error) { c.gets++; return c.values[k], nil }
func (c *memoryCache) Put(k string, v []byte) error {
	c.puts++
	c.values[k] = append([]byte{}, v...)
	return nil
}

func TestImageCacheAndStalePreview(t *testing.T) {
	m, f := fixture()
	f.image = tinyPNG(t)
	m.opts.Cache = &memoryCache{values: map[string][]byte{}}
	m.messages[m.messageIndex].Image = true
	m.messages[m.messageIndex].MediaKey = "account:1:photo:2:edit:0"
	m, cmd := press(m, "v")
	m = complete(m, cmd)
	if !strings.Contains(m.preview, "▀") || len(f.calls) != 1 {
		t.Fatal("first preview did not download")
	}
	m, _ = press(m, "esc")
	m, cmd = press(m, "v")
	m = complete(m, cmd)
	if len(f.calls) != 1 {
		t.Fatal("cached preview downloaded again")
	}
	m, _ = press(m, "esc")
	m, cmd = press(m, "v")
	m, _ = press(m, "esc")
	m = complete(m, cmd)
	if m.preview != "" || m.mode != normal {
		t.Fatal("stale preview reopened closed image")
	}
}

func TestImageCacheIsolatedAcrossAccountMediaAndEdit(t *testing.T) {
	cache := &memoryCache{values: map[string][]byte{}}
	for _, key := range []string{"account:1:photo:2:edit:0", "account:2:photo:2:edit:0", "account:1:photo:3:edit:0", "account:1:photo:2:edit:10"} {
		m, f := fixture()
		f.image = tinyPNG(t)
		m.opts.Cache = cache
		m.messages[m.messageIndex].Image = true
		m.messages[m.messageIndex].MediaKey = key
		m, cmd := press(m, "v")
		m = complete(m, cmd)
		if len(f.calls) != 1 || !strings.Contains(m.preview, "▀") {
			t.Fatalf("media %q reused another account or revision's cache", key)
		}
	}
	if len(cache.values) != 4 {
		t.Fatalf("expected four distinct cache entries, got %d", len(cache.values))
	}
}

func TestImageWithNoIdentityBypassesCache(t *testing.T) {
	m, f := fixture()
	f.image = tinyPNG(t)
	cache := &memoryCache{values: map[string][]byte{}}
	m.opts.Cache = cache
	m.messages[m.messageIndex].Image = true
	for i := 0; i < 2; i++ {
		next, command := press(m, "v")
		m = complete(next, command)
		m, _ = press(m, "esc")
	}
	if len(f.calls) != 2 || cache.gets != 0 || cache.puts != 0 {
		t.Fatalf("unknown identity used disk cache: downloads=%d reads=%d writes=%d", len(f.calls), cache.gets, cache.puts)
	}
}
