package secret_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/hejmo-home/hejmo/internal/secret"
)

func TestSealedTextIsNotPlainAndOpensAgain(t *testing.T) {
	s, err := secret.Open(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := s.Seal([]byte("dns-token-1234"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("dns-token")) {
		t.Fatal("sealed value contains the plain text")
	}
	got, err := s.Open(sealed)
	if err != nil || string(got) != "dns-token-1234" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	again, _ := s.Seal([]byte("dns-token-1234"))
	if bytes.Equal(sealed, again) {
		t.Error("sealing twice gave the same bytes; nonce is reused")
	}
}

func TestKeySurvivesRestartAndStaysPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	first, _ := secret.Open(path)
	sealed, _ := first.Seal([]byte("x"))

	second, err := secret.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := second.Open(sealed); err != nil || string(got) != "x" {
		t.Fatalf("after restart: %q, %v", got, err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("key file mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestTamperedOrForeignValuesAreRejected(t *testing.T) {
	a, _ := secret.Open(filepath.Join(t.TempDir(), "a.key"))
	b, _ := secret.Open(filepath.Join(t.TempDir(), "b.key"))
	sealed, _ := a.Seal([]byte("x"))
	if _, err := b.Open(sealed); err == nil {
		t.Error("a value sealed with another key opened")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := a.Open(sealed); err == nil {
		t.Error("a tampered value opened")
	}
	if _, err := a.Open([]byte("short")); err == nil {
		t.Error("a too short value opened")
	}
}
