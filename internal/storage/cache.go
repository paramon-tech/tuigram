package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const MaxEntryBytes int64 = 8 << 20
const maxCacheEntries = 1024

var ErrCacheMiss = os.ErrNotExist
var ErrEntryTooLarge = errors.New("cache entry exceeds the configured size limit")

// Cache is a bounded, private disk cache for disposable media. Its clear and
// eviction operations only remove SHA-256-named .cache files, never sessions.
type Cache struct {
	mu       sync.Mutex
	dir      string
	maxBytes int64
	ttl      time.Duration
}

func NewCache(dir string, maxBytes int64, ttl time.Duration) (*Cache, error) {
	if maxBytes < 0 {
		return nil, errors.New("cache size cannot be negative")
	}
	if maxBytes > 0 && ttl <= 0 {
		return nil, errors.New("cache TTL must be positive")
	}
	if err := EnsurePrivateDir(dir); err != nil {
		return nil, err
	}
	c := &Cache{dir: dir, maxBytes: maxBytes, ttl: ttl}
	if err := c.Prune(); err != nil {
		return nil, err
	}
	return c, nil
}

func cacheName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:]) + ".cache"
}

func isCacheName(name string) bool {
	if len(name) != 64+len(".cache") || !strings.HasSuffix(name, ".cache") {
		return false
	}
	for _, r := range name[:64] {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func (c *Cache) open() (*os.Root, error) {
	if err := EnsurePrivateDir(c.dir); err != nil {
		return nil, err
	}
	return os.OpenRoot(c.dir)
}

func (c *Cache) Get(key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxBytes == 0 {
		return nil, ErrCacheMiss
	}
	root, err := c.open()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := c.prune(root); err != nil {
		return nil, err
	}
	data, err := readPrivate(root, cacheName(key), min(c.maxBytes, MaxEntryBytes))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrCacheMiss
	}
	return data, err
}

func (c *Cache) Put(key string, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxBytes == 0 {
		return nil
	}
	if int64(len(data)) > min(c.maxBytes, MaxEntryBytes) {
		return ErrEntryTooLarge
	}
	root, err := c.open()
	if err != nil {
		return err
	}
	defer root.Close()
	if err := c.prune(root); err != nil {
		return err
	}
	if err := writePrivate(root, cacheName(key), data); err != nil {
		return err
	}
	return c.prune(root)
}

type cacheEntry struct {
	name     string
	size     int64
	modified time.Time
}

func cacheEntries(root *os.Root) ([]cacheEntry, error) {
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	files, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	entries := make([]cacheEntry, 0, len(files))
	for _, file := range files {
		if !isCacheName(file.Name()) {
			continue
		}
		info, err := root.Lstat(file.Name())
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := checkPrivateFile(info); err != nil {
			return nil, fmt.Errorf("unsafe cache entry %q: %w", file.Name(), err)
		}
		entries = append(entries, cacheEntry{file.Name(), info.Size(), info.ModTime()})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].modified.Equal(entries[j].modified) {
			return entries[i].name < entries[j].name
		}
		return entries[i].modified.Before(entries[j].modified)
	})
	return entries, nil
}

func removeCacheEntry(root *os.Root, name string) error {
	err := root.Remove(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (c *Cache) prune(root *os.Root) error {
	entries, err := cacheEntries(root)
	if err != nil {
		return err
	}
	now := time.Now()
	var total int64
	kept := make([]cacheEntry, 0, len(entries))
	for _, entry := range entries {
		if c.maxBytes == 0 || now.Sub(entry.modified) >= c.ttl || entry.size > min(c.maxBytes, MaxEntryBytes) {
			if err := removeCacheEntry(root, entry.name); err != nil {
				return err
			}
			continue
		}
		total += entry.size
		kept = append(kept, entry)
	}
	count := len(kept)
	for _, entry := range kept {
		if total <= c.maxBytes && count <= maxCacheEntries {
			break
		}
		if err := removeCacheEntry(root, entry.name); err != nil {
			return err
		}
		total -= entry.size
		count--
	}
	return nil
}

func (c *Cache) Prune() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	root, err := c.open()
	if err != nil {
		return err
	}
	defer root.Close()
	return c.prune(root)
}

func (c *Cache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	root, err := c.open()
	if err != nil {
		return err
	}
	defer root.Close()
	entries, err := cacheEntries(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := removeCacheEntry(root, entry.name); err != nil {
			return err
		}
	}
	return nil
}

func (c *Cache) Stats() (entries int, bytes int64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	root, err := c.open()
	if err != nil {
		return 0, 0, err
	}
	defer root.Close()
	if err := c.prune(root); err != nil {
		return 0, 0, err
	}
	files, err := cacheEntries(root)
	if err != nil {
		return 0, 0, err
	}
	for _, file := range files {
		bytes += file.size
	}
	return len(files), bytes, nil
}
