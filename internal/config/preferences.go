package config

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Preferences contains editable user preferences, never API credentials or
// session locations. Saving it reloads the on-disk config without environment
// overrides so environment-only secrets cannot accidentally be persisted.
type Preferences struct {
	Theme, DownloadDir, CallInputFormat, CallInputDevice string
	PollSeconds                                          int
	MarkRead                                             bool
	KeyBindings                                          map[string]string
}

type Shortcut struct{ Action, Label, Key string }

func Shortcuts() []Shortcut {
	return []Shortcut{
		{"compose", "Compose", "i"}, {"attach", "Attach files", "a"},
		{"react", "Reactions", "r"}, {"organization", "Chat organization", "o"},
		{"settings", "Settings", ","}, {"search", "Search messages", "/"},
		{"theme", "Cycle theme", "t"}, {"refresh", "Refresh", "R"},
	}
}

func (p Preferences) Key(action string) string {
	if key := p.KeyBindings[action]; key != "" {
		return key
	}
	for _, shortcut := range Shortcuts() {
		if shortcut.Action == action {
			return shortcut.Key
		}
	}
	return ""
}

func (c Config) Preferences() Preferences {
	return Preferences{Theme: c.Theme, DownloadDir: c.DownloadDir, PollSeconds: c.PollSeconds,
		CallInputFormat: c.CallInputFormat, CallInputDevice: c.CallInputDevice,
		MarkRead: c.MarkRead, KeyBindings: maps.Clone(c.KeyBindings)}
}

func (p Preferences) Validate() error {
	switch p.Theme {
	case "midnight", "light", "dracula":
	default:
		return errors.New("theme must be midnight, light, or dracula")
	}
	if p.DownloadDir != "" && (!filepath.IsAbs(p.DownloadDir) || filepath.Clean(p.DownloadDir) == string(filepath.Separator) || strings.ContainsAny(p.DownloadDir, "\x00\r\n")) {
		return errors.New("download directory must be an absolute directory path")
	}
	if p.PollSeconds < 2 || p.PollSeconds > 86400 {
		return errors.New("refresh seconds must be between 2 and 86400")
	}
	switch p.CallInputFormat {
	case "", "avfoundation", "pulse", "alsa", "oss", "sndio", "dshow":
	default:
		return errors.New("unsupported microphone backend")
	}
	if len(p.CallInputDevice) > 256 || strings.ContainsAny(p.CallInputDevice, "\x00\r\n") {
		return errors.New("microphone device must be at most 256 bytes without control separators")
	}
	known, used := map[string]bool{}, map[string]string{}
	// All fixed normal-mode shortcuts remain reachable. Modifiers, navigation,
	// and global call/quit keys cannot be replaced by an editable shortcut.
	const reserved = "q?hjlkgGcnNeDCdpfvyu[]BLO"
	for _, s := range Shortcuts() {
		known[s.Action] = true
		key := p.Key(s.Action)
		r, _ := utf8.DecodeRuneInString(key)
		if !utf8.ValidString(key) || utf8.RuneCountInString(key) != 1 || !unicode.IsPrint(r) || unicode.IsSpace(r) || strings.ContainsRune(reserved, r) {
			return fmt.Errorf("%s shortcut must be one printable, unreserved character", s.Label)
		}
		if other := used[key]; other != "" {
			return fmt.Errorf("shortcut %q is assigned to both %s and %s", key, other, s.Label)
		}
		used[key] = s.Label
	}
	for action := range p.KeyBindings {
		if !known[action] {
			return fmt.Errorf("unknown shortcut action %q", action)
		}
	}
	return nil
}

func SavePreferences(path string, p Preferences) error {
	if err := p.Validate(); err != nil {
		return err
	}
	c, err := load(path, false)
	if err != nil {
		return err
	}
	c.Theme, c.DownloadDir, c.PollSeconds = p.Theme, p.DownloadDir, p.PollSeconds
	c.CallInputFormat, c.CallInputDevice = p.CallInputFormat, p.CallInputDevice
	c.MarkRead, c.KeyBindings = p.MarkRead, maps.Clone(p.KeyBindings)
	return Save(path, c)
}
