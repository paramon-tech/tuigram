package storage

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/gotd/td/session"
	"golang.org/x/crypto/scrypt"
)

const sessionMagic = "TGSES001"
const maxSessionBytes = 1 << 20
const saltSize = 16

// Session stores Telegram authorization data in an authenticated encrypted file.
// The passphrase is only held in process memory and is never written to disk.
type Session struct {
	mu         sync.Mutex
	path       string
	passphrase []byte
	closed     bool
}

func NewSession(path string, passphrase []byte) (*Session, error) {
	if !utf8.Valid(passphrase) || utf8.RuneCount(passphrase) < 12 {
		return nil, errors.New("session passphrase must contain at least 12 characters")
	}
	distinct := make(map[rune]bool)
	for _, r := range string(passphrase) {
		if !unicode.IsSpace(r) {
			distinct[r] = true
		}
	}
	if len(distinct) < 4 {
		return nil, errors.New("session passphrase must contain at least four distinct non-space characters")
	}
	if filepath.Base(path) == "." || filepath.Base(path) == string(filepath.Separator) {
		return nil, errors.New("session file path is required")
	}
	if err := EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if err := checkPrivateFile(info); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return &Session{path: path, passphrase: bytes.Clone(passphrase)}, nil
}

func sessionCipher(passphrase, salt []byte) (cipher.AEAD, error) {
	key, err := scrypt.Key(passphrase, salt, 32768, 8, 1, 32)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *Session) LoadSession(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("session store is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := ReadPrivateFile(s.path, maxSessionBytes+128)
	if errors.Is(err, os.ErrNotExist) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	headerLen := len(sessionMagic) + saltSize
	if len(data) < headerLen+12+16 || string(data[:len(sessionMagic)]) != sessionMagic {
		return nil, errors.New("invalid encrypted session format")
	}
	aead, err := sessionCipher(s.passphrase, data[len(sessionMagic):headerLen])
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nonce := data[headerLen : headerLen+aead.NonceSize()]
	plain, err := aead.Open(nil, nonce, data[headerLen+aead.NonceSize():], data[:headerLen])
	if err != nil {
		return nil, errors.New("cannot decrypt session: incorrect passphrase or damaged file")
	}
	if len(plain) > maxSessionBytes {
		clear(plain)
		return nil, errors.New("session exceeds the size limit")
	}
	return plain, nil
}

func (s *Session) StoreSession(ctx context.Context, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("session store is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(data) > maxSessionBytes {
		return fmt.Errorf("session exceeds %d byte limit", maxSessionBytes)
	}
	header := make([]byte, len(sessionMagic)+saltSize)
	copy(header, sessionMagic)
	if _, err := rand.Read(header[len(sessionMagic):]); err != nil {
		return err
	}
	aead, err := sessionCipher(s.passphrase, header[len(sessionMagic):])
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	sealed := aead.Seal(nil, nonce, data, header)
	if err := ctx.Err(); err != nil {
		return err
	}
	encrypted := append(append(header, nonce...), sealed...)
	return WritePrivateFile(s.path, encrypted)
}

// Close clears the retained passphrase. The store cannot be used after closing.
// This is best-effort memory hygiene; Go does not guarantee removal of all copies.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.passphrase)
	s.passphrase = nil
	s.closed = true
	return nil
}
