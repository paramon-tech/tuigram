package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCacheBoundsAndHashedKeys(t *testing.T) {
	dir := privateTempDir(t)
	c, err := NewCache(dir, 12, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	key := "../../outside?private=chat-secret"
	if err := c.Put(key, []byte("first!")); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(filepath.Join(dir, cacheName(key)), old, old); err != nil {
		t.Fatal(err)
	}
	if err := c.Put("second", []byte("second")); err != nil {
		t.Fatal(err)
	}
	if err := c.Put("third", []byte("third!")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(key); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("oldest entry not evicted: %v", err)
	}
	data, err := c.Get("third")
	if err != nil || string(data) != "third!" {
		t.Fatalf("cached value %q: %v", data, err)
	}
	entries, size, err := c.Stats()
	if err != nil || entries != 2 || size != 12 {
		t.Fatalf("stats: %d, %d, %v", entries, size, err)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if !isCacheName(file.Name()) {
			t.Fatalf("unhashed key on disk: %s", file.Name())
		}
	}
	if err := c.Put("oversize", make([]byte, 13)); !errors.Is(err, ErrEntryTooLarge) {
		t.Fatalf("oversized entry: %v", err)
	}
}

func TestCacheTTLAndStartupPruning(t *testing.T) {
	dir := privateTempDir(t)
	c, err := NewCache(dir, 100, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Put("old", []byte("expired")); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, cacheName("old")), old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get("old"); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("expired cache read: %v", err)
	}
	for _, key := range []string{"one", "two", "three"} {
		if err := c.Put(key, []byte("123456")); err != nil {
			t.Fatal(err)
		}
	}
	smaller, err := NewCache(dir, 6, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	entries, size, err := smaller.Stats()
	if err != nil || entries != 1 || size != 6 {
		t.Fatalf("startup bounds: %d/%d %v", entries, size, err)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if err := os.Chtimes(filepath.Join(dir, file.Name()), old, old); err != nil {
			t.Fatal(err)
		}
	}
	fresh, err := NewCache(dir, 100, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err = fresh.Stats()
	if err != nil || entries != 0 {
		t.Fatalf("startup TTL: %d, %v", entries, err)
	}
}

func TestCacheDisabledAndClearPreservesSession(t *testing.T) {
	dir := privateTempDir(t)
	c, err := NewCache(dir, 100, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Put("media", []byte("image")); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(dir, "session.enc")
	if err := os.WriteFile(sessionPath, []byte("session-preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Clear(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(sessionPath)
	if err != nil || string(data) != "session-preserved" {
		t.Fatalf("session deleted: %q %v", data, err)
	}
	if err := c.Put("media", []byte("image")); err != nil {
		t.Fatal(err)
	}
	disabled, err := NewCache(dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := disabled.Put("new", []byte("ignored")); err != nil {
		t.Fatal(err)
	}
	if _, err := disabled.Get("new"); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("disabled read: %v", err)
	}
	entries, size, err := disabled.Stats()
	if err != nil || entries != 0 || size != 0 {
		t.Fatalf("disabled retained cache: %d %d %v", entries, size, err)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("session deleted: %v", err)
	}
}

func TestCacheRejectsSymlinkEntry(t *testing.T) {
	dir := privateTempDir(t)
	c, err := NewCache(dir, 100, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(privateTempDir(t), "secret")
	if err := os.WriteFile(target, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, cacheName("attack"))); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get("attack"); err == nil {
		t.Fatal("symlink entry read")
	}
	if err := c.Put("attack", []byte("overwrite")); err == nil {
		t.Fatal("symlink entry overwritten")
	}
	if err := c.Clear(); err == nil {
		t.Fatal("unsafe cache directory silently accepted")
	}
	if _, err := NewCache(dir, 100, time.Hour); err == nil {
		t.Fatal("unsafe cache accepted at startup")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "secret" {
		t.Fatal("symlink target modified")
	}
}

func TestCacheEntryCap(t *testing.T) {
	c, err := NewCache(privateTempDir(t), 32<<20, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Put("large", make([]byte, MaxEntryBytes+1)); !errors.Is(err, ErrEntryTooLarge) {
		t.Fatalf("entry cap: %v", err)
	}
}

func TestCacheConcurrentBounds(t *testing.T) {
	c, err := NewCache(privateTempDir(t), 100, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprint(i)
			if err := c.Put(key, []byte("ten bytes!")); err != nil {
				t.Error(err)
			}
			_, err := c.Get(key)
			if err != nil && !errors.Is(err, ErrCacheMiss) {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	_, size, err := c.Stats()
	if err != nil || size > 100 {
		t.Fatalf("concurrent bounds: %d %v", size, err)
	}
}

func TestCacheBoundsEmptyEntryCount(t *testing.T) {
	dir := privateTempDir(t)
	for i := 0; i < maxCacheEntries+1; i++ {
		if err := os.WriteFile(filepath.Join(dir, cacheName(fmt.Sprint(i))), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	c, err := NewCache(dir, 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	entries, size, err := c.Stats()
	if err != nil || entries != maxCacheEntries || size != 0 {
		t.Fatalf("empty file bound: %d/%d %v", entries, size, err)
	}
}
