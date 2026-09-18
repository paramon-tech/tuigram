// Package config loads private JSON configuration with environment overrides.
package config

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/paramon-tech/tuigram/internal/storage"
)

type Config struct {
	AppID         int    `json:"app_id"`
	AppHash       string `json:"app_hash"`
	Theme         string `json:"theme"`
	CacheDir      string `json:"cache_dir"`
	StateDir      string `json:"state_dir"`
	CacheMaxBytes int64  `json:"cache_max_bytes"`
	CacheTTLHours int    `json:"cache_ttl_hours"`
	PollSeconds   int    `json:"poll_seconds"`
}

func userDirectory(env string, fallback func() (string, error)) (string, error) {
	if value := os.Getenv(env); value != "" {
		if !filepath.IsAbs(value) {
			return "", fmt.Errorf("%s must be an absolute path", env)
		}
		return filepath.Join(value, "tuigram"), nil
	}
	base, err := fallback()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "tuigram"), nil
}

func DefaultPath() (string, error) {
	dir, err := userDirectory("XDG_CONFIG_HOME", os.UserConfigDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

func Default() (Config, error) {
	cache, err := userDirectory("XDG_CACHE_HOME", os.UserCacheDir)
	if err != nil {
		return Config{}, err
	}
	state, err := userDirectory("XDG_STATE_HOME", func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "state"), nil
	})
	if err != nil {
		return Config{}, err
	}
	c := Config{Theme: "midnight", CacheDir: cache, StateDir: state, CacheMaxBytes: 32 << 20, CacheTTLHours: 24, PollSeconds: 5}
	if err := c.applyEnvironment(); err != nil {
		return Config{}, err
	}
	return c, c.Validate()
}

func (c *Config) applyEnvironment() error {
	if value, exists := os.LookupEnv("TUIGRAM_API_ID"); exists && value != "" {
		id, err := strconv.Atoi(value)
		if err != nil || id <= 0 {
			return errors.New("TUIGRAM_API_ID must be a positive integer")
		}
		c.AppID = id
	}
	if value := os.Getenv("TUIGRAM_API_HASH"); value != "" {
		c.AppHash = value
	}
	return nil
}

func (c Config) Validate() error {
	if c.AppID < 0 || c.AppID > 2147483647 {
		return errors.New("app_id must fit a positive 32-bit integer, or be zero when unset")
	}
	if c.AppHash != "" {
		if len(c.AppHash) != 32 {
			return errors.New("app_hash must be 32 hexadecimal characters")
		}
		if _, err := hex.DecodeString(c.AppHash); err != nil {
			return errors.New("app_hash must be 32 hexadecimal characters")
		}
	}
	switch c.Theme {
	case "midnight", "light", "dracula":
	default:
		return errors.New("theme must be midnight, light, or dracula")
	}
	if !filepath.IsAbs(c.CacheDir) || !filepath.IsAbs(c.StateDir) {
		return errors.New("cache_dir and state_dir must be absolute paths")
	}
	if filepath.Clean(c.CacheDir) == filepath.Clean(c.StateDir) {
		return errors.New("cache_dir and state_dir must be separate directories")
	}
	if filepath.Clean(c.CacheDir) == string(filepath.Separator) || filepath.Clean(c.StateDir) == string(filepath.Separator) {
		return errors.New("data directories cannot be the filesystem root")
	}
	if c.CacheMaxBytes < 0 {
		return errors.New("cache_max_bytes cannot be negative; use zero to disable the cache")
	}
	if c.CacheTTLHours < 1 || c.CacheTTLHours > 87600 {
		return errors.New("cache_ttl_hours must be between 1 and 87600")
	}
	if c.PollSeconds < 2 || c.PollSeconds > 86400 {
		return errors.New("poll_seconds must be between 2 and 86400")
	}
	return nil
}

func Load(path string) (Config, error) {
	c, err := Default()
	if err != nil {
		return Config{}, err
	}
	if path == "" {
		path, err = DefaultPath()
		if err != nil {
			return Config{}, err
		}
	}
	data, err := storage.ReadPrivateFile(path, 64<<10)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read configuration: %w", err)
	}
	if trimmed := bytes.TrimSpace(data); len(trimmed) == 0 || trimmed[0] != '{' {
		return Config{}, errors.New("configuration must contain exactly one JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return Config{}, errors.New("configuration must contain exactly one JSON object")
	}
	if err := c.applyEnvironment(); err != nil {
		return Config{}, err
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func Save(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return storage.WritePrivateFile(path, append(data, '\n'))
}
