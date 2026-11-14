// Package secret seals credentials the box must be able to read again (the
// DNS token, ADR 0014; calendar passwords, ADR 0012) so that they are not
// readable in a dump of the database. The key lives in its own file next to
// the database, never in it. Standard library only (AES-256-GCM).
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

// Sealer seals and opens values with the box's key.
type Sealer struct{ aead cipher.AEAD }

// Open loads the key at path, creating it (mode 0600) on first use.
func Open(path string) (*Sealer, error) {
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		// O_EXCL: never overwrite a key another start created meanwhile.
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			return Open(path)
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
	if len(key) != 32 {
		return nil, errors.New("secret: key file has the wrong size")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal encrypts plain. The result starts with a fresh random nonce.
func (s *Sealer) Seal(plain []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plain, nil), nil
}

// Open decrypts a value from Seal. It fails for foreign or altered values.
func (s *Sealer) Open(sealed []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n+s.aead.Overhead() {
		return nil, errors.New("secret: sealed value too short")
	}
	return s.aead.Open(nil, sealed[:n], sealed[n:], nil)
}
