// Package secretbox encrypts the settings saved in the UI that would
// otherwise sit in .env (platform passwords, tokens, 2FA secrets), so the
// database, its backups and any copy of it don't hold them in the clear.
//
// The key is 32 random bytes in a file of its own in the data directory,
// made on first use and readable by its owner only. It protects copies of
// the database, not someone who has the whole data directory: HuntDeck has
// to read the values back to sign in, so they can't be hashed.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// KeyFile is the key's file name in the data directory.
const KeyFile = "secret.key"

// keySize is AES-256's.
const keySize = 32

// prefix marks a sealed value, and its format's version.
const prefix = "v1:"

// ErrNoKey means the key file doesn't exist (nothing was ever saved).
var ErrNoKey = errors.New("secretbox: no key file")

// ErrOpen means a value can't be decrypted: it was sealed with another key
// (the key file was lost or replaced), under another name, or it's damaged.
var ErrOpen = errors.New("secretbox: value can't be decrypted with this key")

// Box seals and opens values with one key.
type Box struct {
	aead cipher.AEAD
}

// Load reads the key at path. It returns ErrNoKey if there's none.
func Load(path string) (*Box, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoKey
	}
	if err != nil {
		return nil, fmt.Errorf("secretbox: read key: %w", err)
	}
	if len(b) != keySize {
		return nil, fmt.Errorf("secretbox: key file %s is %d bytes, want %d", path, len(b), keySize)
	}
	return newBox(b)
}

// LoadOrCreate reads the key at path, making a new one, owner-only, if
// there's none.
func LoadOrCreate(path string) (*Box, error) {
	box, err := Load(path)
	if !errors.Is(err, ErrNoKey) {
		return box, err
	}
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("secretbox: new key: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return Load(path) // made meanwhile by another process
	}
	if err != nil {
		return nil, fmt.Errorf("secretbox: create key: %w", err)
	}
	_, err = f.Write(key)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("secretbox: write key: %w", err)
	}
	return newBox(key)
}

func newBox(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts value for name: it opens only under the same name, so a
// sealed value can't be moved to another setting.
func (b *Box) Seal(name, value string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secretbox: nonce: %w", err)
	}
	out := b.aead.Seal(nonce, nonce, []byte(value), []byte(name))
	return prefix + base64.RawStdEncoding.EncodeToString(out), nil
}

// Open decrypts what Seal made for name. Its errors never include the value.
func (b *Box) Open(name, sealed string) (string, error) {
	rest, ok := strings.CutPrefix(sealed, prefix)
	if !ok {
		return "", ErrOpen
	}
	raw, err := base64.RawStdEncoding.DecodeString(rest)
	if err != nil || len(raw) < b.aead.NonceSize() {
		return "", ErrOpen
	}
	n := b.aead.NonceSize()
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], []byte(name))
	if err != nil {
		return "", ErrOpen
	}
	return string(plain), nil
}
