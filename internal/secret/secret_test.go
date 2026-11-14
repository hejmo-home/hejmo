package secret_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/secret"
)

func TestSealedTextIsNotReadableAndOpensWithTheSameKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	box, err := secret.LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.Seal("app-specific-password")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "app-specific-password") {
		t.Fatalf("sealed text leaks the secret: %q", sealed)
	}
	again, _ := box.Seal("app-specific-password")
	if again == sealed {
		t.Fatal("two seals of the same text are identical: nonce is reused")
	}
	got, err := box.Open(sealed)
	if err != nil || got != "app-specific-password" {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

func TestOtherKeyOrTamperedTextDoesNotOpen(t *testing.T) {
	dir := t.TempDir()
	a, _ := secret.LoadOrCreate(filepath.Join(dir, "a.key"))
	b, _ := secret.LoadOrCreate(filepath.Join(dir, "b.key"))
	sealed, _ := a.Seal("pw")
	if _, err := b.Open(sealed); err == nil {
		t.Fatal("opened with another key")
	}
	if _, err := a.Open(sealed[:len(sealed)-6] + "AAAAAA"); err == nil {
		t.Fatal("opened tampered text")
	}
	if _, err := a.Open("not base64 !"); err == nil {
		t.Fatal("opened garbage")
	}
}

func TestFreshVolumeGetsKeyFileWithMode0600AndRestartReusesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "secret.key")
	first, err := secret.LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("no key file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	sealed, _ := first.Seal("pw")

	second, err := secret.LoadOrCreate(path) // restart
	if err != nil {
		t.Fatal(err)
	}
	if got, err := second.Open(sealed); err != nil || got != "pw" {
		t.Fatalf("restart did not reuse the key: %q, %v", got, err)
	}
	if again, _ := os.ReadFile(path); string(again) != string(raw) {
		t.Fatal("key file was rewritten on restart")
	}
}

func TestBrokenKeyFileIsRefusedNotReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	if err := os.WriteFile(path, []byte("too short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := secret.LoadOrCreate(path); err == nil {
		t.Fatal("accepted a broken key file")
	}
	if got, _ := os.ReadFile(path); string(got) != "too short" {
		t.Fatal("broken key file was overwritten, which would lock out stored credentials")
	}
}
