package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gotd/td/session"
)

var testPassphrase = []byte("correct horse battery staple")

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

func newTestSession(t *testing.T) (*Session, string) {
	t.Helper()
	path := filepath.Join(privateTempDir(t), "state", "session.enc")
	s, err := NewSession(path, testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestSessionEncryptedRoundTrip(t *testing.T) {
	s, path := newTestSession(t)
	ctx := context.Background()
	if _, err := s.LoadSession(ctx); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("missing session: %v", err)
	}
	plain := []byte(`{"auth_key":"highly secret Telegram session"}`)
	if err := s.StoreSession(ctx, plain); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(first, plain) || bytes.Contains(first, testPassphrase) {
		t.Fatal("secret stored in plaintext")
	}
	got, err := s.LoadSession(ctx)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("round trip: %q, %v", got, err)
	}
	if err := s.StoreSession(ctx, plain); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("session encryption reused salt and nonce")
	}
	for name, want := range map[string]os.FileMode{path: 0600, filepath.Dir(path): 0700} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s: permissions %o, want %o", name, info.Mode().Perm(), want)
		}
	}
}

func TestSessionRejectsWrongPassphraseAndTampering(t *testing.T) {
	s, path := newTestSession(t)
	ctx := context.Background()
	if err := s.StoreSession(ctx, []byte("secret")); err != nil {
		t.Fatal(err)
	}
	other, err := NewSession(path, []byte("a different strong passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	if data, err := other.LoadSession(ctx); err == nil || data != nil {
		t.Fatal("wrong passphrase decrypted a session")
	}
	encrypted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{0, len(sessionMagic), len(encrypted) - 1} {
		altered := bytes.Clone(encrypted)
		altered[index] ^= 1
		if err := os.WriteFile(path, altered, 0600); err != nil {
			t.Fatal(err)
		}
		if data, err := s.LoadSession(ctx); err == nil || data != nil {
			t.Fatalf("tampered byte %d accepted", index)
		}
	}
	if err := os.WriteFile(path, []byte("plaintext session"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadSession(ctx); err == nil {
		t.Fatal("plaintext session accepted")
	}
}

func TestSessionRejectsWeakPassphrase(t *testing.T) {
	for _, pass := range []string{"", "short", "aaaaaaaaaaaaaaaa", "                  ", "aabbccccaabbccc", string([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})} {
		if _, err := NewSession(filepath.Join(privateTempDir(t), "session"), []byte(pass)); err == nil {
			t.Errorf("weak passphrase accepted: %q", pass)
		}
	}
}

func TestSessionCancellationAndSizeLimit(t *testing.T) {
	s, _ := newTestSession(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.StoreSession(ctx, []byte("secret")); !errors.Is(err, context.Canceled) {
		t.Fatalf("store: %v", err)
	}
	if _, err := s.LoadSession(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("load: %v", err)
	}
	if err := s.StoreSession(context.Background(), make([]byte, maxSessionBytes+1)); err == nil {
		t.Fatal("oversized session accepted")
	}
}

func TestSessionConcurrentAccess(t *testing.T) {
	s, _ := newTestSession(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.StoreSession(ctx, []byte("concurrent session")); err != nil {
				t.Error(err)
			}
			if _, err := s.LoadSession(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestPrivateFilesRejectUnsafePaths(t *testing.T) {
	t.Run("symlink file", func(t *testing.T) {
		dir := privateTempDir(t)
		target := filepath.Join(dir, "target")
		link := filepath.Join(dir, "session")
		if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := NewSession(link, testPassphrase); err == nil {
			t.Fatal("symlink session accepted")
		}
		if _, err := ReadPrivateFile(link, 100); err == nil {
			t.Fatal("symlink read accepted")
		}
		if err := WritePrivateFile(link, []byte("overwrite")); err == nil {
			t.Fatal("symlink write accepted")
		}
		data, _ := os.ReadFile(target)
		if string(data) != "original" {
			t.Fatal("symlink target modified")
		}
	})
	t.Run("symlink directory", func(t *testing.T) {
		dir := privateTempDir(t)
		target := filepath.Join(dir, "target")
		link := filepath.Join(dir, "link")
		if err := os.Mkdir(target, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := NewSession(filepath.Join(link, "nested", "session"), testPassphrase); err == nil {
			t.Fatal("symlink directory accepted")
		}
	})
	t.Run("permissive directory", func(t *testing.T) {
		dir := privateTempDir(t)
		if err := os.Chmod(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if _, err := NewSession(filepath.Join(dir, "session"), testPassphrase); err == nil {
			t.Fatal("permissive directory accepted")
		}
	})
	t.Run("permissive file", func(t *testing.T) {
		path := filepath.Join(privateTempDir(t), "session")
		if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := NewSession(path, testPassphrase); err == nil {
			t.Fatal("permissive file accepted")
		}
		if _, err := ReadPrivateFile(path, 100); err == nil {
			t.Fatal("permissive file read")
		}
		if err := WritePrivateFile(path, []byte("new")); err == nil {
			t.Fatal("permissive file overwritten")
		}
	})
	t.Run("hard linked file", func(t *testing.T) {
		dir := privateTempDir(t)
		path := filepath.Join(dir, "session")
		if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(path, filepath.Join(dir, "linked")); err != nil {
			t.Fatal(err)
		}
		if _, err := NewSession(path, testPassphrase); err == nil {
			t.Fatal("hard linked file accepted")
		}
	})
}

func TestPrivateReadSizeLimit(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "large")
	if err := os.WriteFile(path, make([]byte, 128), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivateFile(path, 127); err == nil {
		t.Fatal("oversized file read")
	}
}

func TestSessionCloseClearsPassphraseAndRejectsUse(t *testing.T) {
	s, _ := newTestSession(t)
	retained := s.passphrase
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, b := range retained {
		if b != 0 {
			t.Fatal("passphrase memory was not cleared")
		}
	}
	if err := s.StoreSession(context.Background(), []byte("secret")); err == nil {
		t.Fatal("closed session store accepted writes")
	}
	if _, err := s.LoadSession(context.Background()); err == nil {
		t.Fatal("closed session store accepted reads")
	}
}
