package storage

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// EnsurePrivateDir creates a private directory and rejects symlink components.
// Existing data directories must already be private; permissions are never
// silently changed on a directory supplied by the user.
func EnsurePrivateDir(path string) error {
	return privateDir(path, true)
}

// privateDir validates every path component. Reads must not create missing
// directories: callers use os.ErrNotExist to distinguish absent configuration.
func privateDir(path string, create bool) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if abs == string(filepath.Separator) {
		return errors.New("refusing to use a filesystem root for private data")
	}
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(abs, current), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if !create {
				return err
			}
			if err = os.Mkdir(current, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("unsafe private directory %q: symlinks and non-directories are forbidden", current)
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Uid != 0 && stat.Uid != uint32(os.Geteuid()) {
			return fmt.Errorf("unsafe ancestor directory %q: owned by another user", current)
		}
		if current == abs {
			if info.Mode().Perm()&0077 != 0 {
				return fmt.Errorf("private directory %q must have mode 0700", current)
			}
			if err := owned(info); err != nil {
				return fmt.Errorf("directory %q: %w", current, err)
			}
		} else if info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0 {
			return fmt.Errorf("unsafe writable ancestor directory %q", current)
		}
	}
	return nil
}

func owned(info os.FileInfo) error {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Uid != uint32(os.Geteuid()) {
		return errors.New("private data must be owned by the current user")
	}
	return nil
}

func checkPrivateFile(info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return errors.New("private data must be a regular file, not a symlink")
	}
	if info.Mode().Perm()&0077 != 0 {
		return errors.New("private data file must have mode 0600")
	}
	if err := owned(info); err != nil {
		return err
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink != 1 {
		return errors.New("private data file must not have hard links")
	}
	return nil
}

// ReadPrivateFile bounds reads and refuses files with unsafe permissions or links.
func ReadPrivateFile(path string, maxBytes int64) ([]byte, error) {
	if err := privateDir(filepath.Dir(path), false); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return readPrivate(root, filepath.Base(path), maxBytes)
}

func readPrivate(root *os.Root, name string, maxBytes int64) ([]byte, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if err := checkPrivateFile(info); err != nil {
		return nil, err
	}
	if info.Size() > maxBytes {
		return nil, errors.New("private data exceeds the size limit")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("private data exceeds the size limit")
	}
	return data, nil
}

// WritePrivateFile atomically replaces a private file, syncing it before rename.
func WritePrivateFile(path string, data []byte) error {
	if err := EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	return writePrivate(root, filepath.Base(path), data)
}

func writePrivate(root *os.Root, name string, data []byte) error {
	if info, err := root.Lstat(name); err == nil {
		if err := checkPrivateFile(info); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	tmp := ".tuigram-" + hex.EncodeToString(random[:]) + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = root.Rename(tmp, name); err != nil {
		return err
	}
	// Sync the containing directory so the rename survives a sudden shutdown.
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
