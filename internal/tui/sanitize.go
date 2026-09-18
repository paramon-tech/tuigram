package tui

import (
	"strings"
	"unicode"
)

// Sanitize removes terminal commands and invisible direction overrides from
// untrusted Telegram text while retaining newlines and emoji joiners.
func Sanitize(s string) string {
	runes := []rune(s)
	var out strings.Builder
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '\x1b' || r == '\u009b' || r == '\u009d' {
			kind := r
			if r == '\x1b' {
				i++
				if i >= len(runes) {
					break
				}
				kind = runes[i]
			}
			switch kind {
			case '[', '\u009b':
				for i++; i < len(runes); i++ {
					if runes[i] >= 0x40 && runes[i] <= 0x7e {
						break
					}
				}
			case ']', 'P', '_', '^', 'X', '\u009d':
				for i++; i < len(runes); i++ {
					if runes[i] == '\a' || runes[i] == '\u009c' {
						break
					}
					if runes[i] == '\x1b' && i+1 < len(runes) && runes[i+1] == '\\' {
						i++
						break
					}
				}
			}
			continue
		}
		if r == '\t' {
			out.WriteString("    ")
			continue
		}
		if r != '\n' && unicode.IsControl(r) {
			continue
		}
		if unicode.Is(unicode.Cf, r) && r != '\u200c' && r != '\u200d' {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func singleLine(s string) string { return strings.ReplaceAll(Sanitize(s), "\n", " ") }
