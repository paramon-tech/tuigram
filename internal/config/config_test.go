package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func privateTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func environment(t *testing.T) string {
	t.Helper()
	dir := privateTempDir(t)
	for name, value := range map[string]string{"XDG_CONFIG_HOME": filepath.Join(dir, "config"), "XDG_CACHE_HOME": filepath.Join(dir, "cache"), "XDG_STATE_HOME": filepath.Join(dir, "state"), "TUIGRAM_API_ID": "", "TUIGRAM_API_HASH": ""} {
		t.Setenv(name, value)
	}
	return dir
}

func TestDefaultsAndMissingFile(t *testing.T) {
	dir := environment(t)
	c, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if c.Theme != "midnight" || c.CacheMaxBytes != 32<<20 || c.CacheTTLHours != 24 || c.PollSeconds != 15 {
		t.Fatalf("wrong defaults: %+v", c)
	}
	if c.CacheDir != filepath.Join(dir, "cache", "tuigram") || c.StateDir != filepath.Join(dir, "state", "tuigram") {
		t.Fatalf("XDG directories ignored: %+v", c)
	}
	path, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "config", "tuigram", "config.json") {
		t.Fatalf("configuration path: %s", path)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, loaded) {
		t.Fatalf("missing file changed defaults: %+v", loaded)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("loading missing config created files or directories: %v", entries)
	}
}

func TestSaveLoadPrivateAndEnvironmentOverrides(t *testing.T) {
	environment(t)
	c, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	c.AppID = 123
	c.AppHash = "0123456789abcdef0123456789abcdef"
	c.Theme = "dracula"
	c.CacheMaxBytes = 0
	path, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, loaded) {
		t.Fatalf("round trip: %+v != %+v", c, loaded)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions: %o", info.Mode().Perm())
	}
	t.Setenv("TUIGRAM_API_ID", "456")
	t.Setenv("TUIGRAM_API_HASH", "abcdef0123456789abcdef0123456789")
	loaded, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AppID != 456 || loaded.AppHash != "abcdef0123456789abcdef0123456789" {
		t.Fatal("environment did not override saved credentials")
	}
}

func TestPartialConfigurationRetainsDefaults(t *testing.T) {
	environment(t)
	path := filepath.Join(privateTempDir(t), "config.json")
	if err := os.WriteFile(path, []byte(`{"theme":"light"}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Theme != "light" || c.CacheMaxBytes != 32<<20 || c.PollSeconds != 15 {
		t.Fatalf("partial config: %+v", c)
	}
}

func TestRejectsInvalidConfiguration(t *testing.T) {
	environment(t)
	for _, body := range []string{`{"theme":"unknown"}`, `{"cache_max_bytes":-1}`, `{"cache_ttl_hours":0}`, `{"poll_seconds":1}`, `{"cache_dir":"relative"}`, `{"app_id":-1}`, `{"app_hash":"wrong"}`, `{"theme":"light","typo":true}`, `{} {}`, `not json`, `null`} {
		t.Run(body, func(t *testing.T) {
			path := filepath.Join(privateTempDir(t), "config.json")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatalf("invalid config accepted: %s", body)
			}
		})
	}
}

func TestRejectsUnsafeConfigFile(t *testing.T) {
	environment(t)
	path := filepath.Join(privateTempDir(t), "config.json")
	if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("world readable config accepted")
	}
	link := filepath.Join(privateTempDir(t), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link); err == nil {
		t.Fatal("symlink config accepted")
	}
}

func TestRejectsInvalidEnvironment(t *testing.T) {
	environment(t)
	for _, value := range []string{"zero", "0", "-2", "9223372036854775808"} {
		t.Setenv("TUIGRAM_API_ID", value)
		if _, err := Default(); err == nil {
			t.Errorf("invalid API ID accepted: %s", value)
		}
	}
	t.Setenv("TUIGRAM_API_ID", "")
	t.Setenv("XDG_CACHE_HOME", "relative")
	if _, err := Default(); err == nil {
		t.Fatal("relative XDG directory accepted")
	}
}

func TestRejectsSharedDataDirectory(t *testing.T) {
	environment(t)
	c, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	c.CacheDir = c.StateDir
	if err := c.Validate(); err == nil {
		t.Fatal("same directory accepted for cache and sessions")
	}
}

func TestCallAudioConfiguration(t *testing.T) {
	environment(t)
	c, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	c.CallInputFormat = "avfoundation"
	c.CallInputDevice = "External USB Microphone"
	p := filepath.Join(privateTempDir(t), "config.json")
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil || got.CallInputDevice != c.CallInputDevice || got.CallInputFormat != c.CallInputFormat {
		t.Fatalf("audio config round trip: %#v %v", got, err)
	}
	for _, format := range []string{"lavfi", "file", "concat", "; sh"} {
		c.CallInputFormat = format
		if err := c.Validate(); err == nil {
			t.Fatalf("unsafe backend accepted: %q", format)
		}
	}
	c.CallInputFormat = ""
	c.CallInputDevice = "microphone\x00other"
	if err := c.Validate(); err == nil {
		t.Fatal("invalid device accepted")
	}
}
