package store_test

import (
	"path/filepath"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

func openTemp(t *testing.T) (*store.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hejmo.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func TestOpenAppliesAllMigrations(t *testing.T) {
	db, _ := openTemp(t)
	got, err := db.SchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if want := store.LatestVersion(); got != want || want < 1 {
		t.Fatalf("schema version = %d, want %d (>= 1)", got, want)
	}
}

func TestReopenIsIdempotent(t *testing.T) {
	db, path := openTemp(t)
	db.Close()
	again, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer again.Close()
	if v, _ := again.SchemaVersion(); v != store.LatestVersion() {
		t.Fatalf("version after reopen = %d", v)
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	db, _ := openTemp(t)
	_, err := db.SQL().Exec(`INSERT INTO household_members (household_id, person_id, role) VALUES ('nope', 'nope', 'parent')`)
	if err == nil {
		t.Fatal("insert with dangling foreign keys succeeded")
	}
}

// Full-text search (FTS5) is a guardrail for the calendar in package 3.
func TestFTS5IsAvailable(t *testing.T) {
	db, _ := openTemp(t)
	if _, err := db.SQL().Exec(`CREATE VIRTUAL TABLE fts_probe USING fts5(body)`); err != nil {
		t.Fatalf("FTS5 not available: %v", err)
	}
}
