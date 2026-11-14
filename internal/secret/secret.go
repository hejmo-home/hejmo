// Package secret encrypts credentials at rest with AES-256-GCM (ADR 0012).
//
// The key lives in a file in the data volume. This protects a leaked database
// file or backup on its own, not a leaked data volume.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const keySize = 32 // AES-256

// Box seals and opens short secrets such as app-specific passwords.
type Box struct{ gcm cipher.AEAD }

// NewBox returns a Box for a 32-byte key.
func NewBox(key []byte) (*Box, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("secret: key must be %d bytes, is %d", keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{gcm: gcm}, nil
}

// LoadOrCreate reads the key file at path, or creates it (mode 0600, random
// key) if it does not exist. A key file of the wrong size is an error and is
// never replaced: that would lock out everything sealed with the old key.
func LoadOrCreate(path string) (*Box, error) {
	key, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		key = make([]byte, keySize)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) { // created by someone else meanwhile
			return LoadOrCreate(path)
		}
		if err != nil {
			return nil, err
		}
		_, werr := f.Write(key)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			os.Remove(path)
			return nil, werr
		}
	} else if err != nil {
		return nil, err
	}
	box, err := NewBox(key)
	if err != nil {
		return nil, fmt.Errorf("key file %s: %w", path, err)
	}
	return box, nil
}

// Seal encrypts text. The result is base64 of nonce and ciphertext; every call
// uses a fresh nonce.
func (b *Box) Seal(plain string) (string, error) {
	nonce := make([]byte, b.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b.gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// Open decrypts text produced by Seal. It fails for another key or altered text.
func (b *Box) Open(sealed string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", errors.New("secret: not sealed text")
	}
	n := b.gcm.NonceSize()
	if len(raw) < n+b.gcm.Overhead() {
		return "", errors.New("secret: sealed text too short")
	}
	plain, err := b.gcm.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", errors.New("secret: cannot open (wrong key or altered text)")
	}
	return string(plain), nil
}
