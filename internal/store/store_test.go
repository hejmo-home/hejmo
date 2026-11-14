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

// Until the timestamp convention, migrations were numbered 0001 to 0010.
// Development databases hold those names; they must map onto the renamed
// files instead of running every migration a second time.
func TestDevelopmentDatabaseWithOldMigrationNamesMigratesWithoutRerunning(t *testing.T) {
	db, path := openTemp(t)
	if _, err := db.SQL().Exec(`INSERT INTO people (id, name, created_at, updated_at) VALUES ('p1', 'Mama', '2026-11-14T10:00:00Z', '2026-11-14T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	// Put schema_migrations back into the state the old runner left it in.
	old := []string{
		"0001_people_and_access.sql", "0002_calendar.sql", "0003_sync.sql", "0004_share_through_box.sql",
		"0005_public_https.sql", "0006_displays.sql", "0007_tasks.sql", "0008_display_show_tomorrow.sql",
		"0009_household_settings.sql", "0010_caldav.sql",
	}
	// Only the rows of the first ten migrations go back to counter names; later
	// migrations (recovery codes, ...) stay recorded, as on a real dev database.
	if _, err := db.SQL().Exec(`DELETE FROM schema_migrations WHERE version <= 202611142200`); err != nil {
		t.Fatal(err)
	}
	for i, name := range old {
		if _, err := db.SQL().Exec(`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, i+1, name); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	again, err := store.Open(path) // re-running a CREATE TABLE would fail here
	if err != nil {
		t.Fatalf("opening a database with old migration names: %v", err)
	}
	defer again.Close()
	if v, _ := again.SchemaVersion(); v != store.LatestVersion() {
		t.Errorf("schema version = %d, want %d", v, store.LatestVersion())
	}
	var n int
	if err := again.SQL().QueryRow(`SELECT COUNT(*) FROM people WHERE name = 'Mama'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("data after the upgrade: %d rows, %v", n, err)
	}
	var names int
	if err := again.SQL().QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name LIKE '0%'`).Scan(&names); err != nil || names != 0 {
		t.Errorf("%d rows still carry an old name, %v", names, err)
	}
}
