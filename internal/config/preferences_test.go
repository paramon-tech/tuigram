package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSavePreferencesPreservesDiskCredentialsAndIgnoresEnvironmentSecrets(t *testing.T) {
	environment(t)
	path := filepath.Join(privateTempDir(t), "config.json")
	c, _ := Default()
	c.AppID = 123
	c.AppHash = "0123456789abcdef0123456789abcdef"
	c.CacheTTLHours = 72
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TUIGRAM_API_ID", "456")
	t.Setenv("TUIGRAM_API_HASH", "abcdef0123456789abcdef0123456789")
	live, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p := live.Preferences()
	p.Theme = "dracula"
	p.MarkRead = false
	p.DownloadDir = filepath.Join(filepath.Dir(path), "downloads")
	p.KeyBindings = map[string]string{"settings": ";"}
	if err := SavePreferences(path, p); err != nil {
		t.Fatal(err)
	}
	saved, err := load(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AppID != c.AppID || saved.AppHash != c.AppHash || saved.CacheTTLHours != 72 {
		t.Fatal("preferences altered credentials or unrelated settings")
	}
	if saved.Theme != "dracula" || saved.MarkRead || saved.DownloadDir != p.DownloadDir || saved.KeyBindings["settings"] != ";" {
		t.Fatalf("preferences not saved: %+v", saved.Preferences())
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), live.AppHash) {
		t.Fatal("environment secret was persisted")
	}
}

func TestFirstPreferencesSaveNeverCopiesEnvironmentCredentials(t *testing.T) {
	environment(t)
	t.Setenv("TUIGRAM_API_ID", "456")
	t.Setenv("TUIGRAM_API_HASH", "abcdef0123456789abcdef0123456789")
	c, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(privateTempDir(t), "config.json")
	if err := SavePreferences(path, c.Preferences()); err != nil {
		t.Fatal(err)
	}
	saved, err := load(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AppID != 0 || saved.AppHash != "" {
		t.Fatal("new settings file leaked environment credentials")
	}
}

func TestPreferencesValidateBindingsAndPaths(t *testing.T) {
	environment(t)
	c, _ := Default()
	for _, keys := range []map[string]string{{"settings": "q"}, {"settings": "a"}, {"settings": "ctrl+c"}, {"settings": "\x1b"}, {"unknown": "z"}} {
		p := c.Preferences()
		p.KeyBindings = keys
		if p.Validate() == nil {
			t.Fatalf("invalid bindings accepted: %v", keys)
		}
	}
	p := c.Preferences()
	p.KeyBindings = map[string]string{"settings": "a", "attach": ","}
	if err := p.Validate(); err != nil {
		t.Fatal("valid key swap rejected:", err)
	}
	p.DownloadDir = "relative"
	if p.Validate() == nil {
		t.Fatal("relative downloads accepted")
	}
}
