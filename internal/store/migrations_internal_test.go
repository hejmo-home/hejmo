package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestShippedMigrationsHaveUniqueAscendingTimestampPrefixes(t *testing.T) {
	ms, err := migrations()
	if err != nil {
		t.Fatalf("shipped migrations: %v", err)
	}
	if len(ms) == 0 {
		t.Fatal("no migrations shipped")
	}
}

func TestCheckMigrationNamesRejectsDuplicateAndMalformedPrefixes(t *testing.T) {
	for name, tc := range map[string]struct {
		names   []string
		wantErr string // empty: valid
	}{
		"ascending":          {names: []string{"202611141227_a.sql", "202611141408_b.sql"}},
		"duplicate prefix":   {names: []string{"202611141227_a.sql", "202611141227_b.sql"}, wantErr: "duplicate"},
		"not ascending":      {names: []string{"202611141408_b.sql", "202611141227_a.sql"}, wantErr: "ascending"},
		"old counter":        {names: []string{"0011_x.sql"}, wantErr: "YYYYMMDDhhmm"},
		"date only":          {names: []string{"20261114_x.sql"}, wantErr: "YYYYMMDDhhmm"},
		"no name":            {names: []string{"202611141227.sql"}, wantErr: "YYYYMMDDhhmm_name.sql"},
		"impossible month":   {names: []string{"202613141227_x.sql"}, wantErr: "not a valid time"},
		"impossible minute":  {names: []string{"202611141260_x.sql"}, wantErr: "not a valid time"},
		"non-ascii name":     {names: []string{"202611141227_käse.sql"}, wantErr: "YYYYMMDDhhmm_name.sql"},
		"wrong extension":    {names: []string{"202611141227_a.txt"}, wantErr: "YYYYMMDDhhmm_name.sql"},
		"upper case in name": {names: []string{"202611141227_A.sql"}, wantErr: "YYYYMMDDhhmm_name.sql"},
	} {
		t.Run(name, func(t *testing.T) {
			err := checkMigrationNames(tc.names)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("err = %v, want none", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// A migration from a branch merged later may carry an older timestamp than one
// a development database has already applied. It must still run.
func TestAMigrationOlderThanTheLatestAppliedStillRuns(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "hejmo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	late := migration{version: 200001010000, name: "200001010000_late.sql", body: `CREATE TABLE late_probe (x INTEGER)`}
	if err := db.apply([]migration{late}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`INSERT INTO late_probe (x) VALUES (1)`); err != nil {
		t.Fatalf("late migration did not run: %v", err)
	}
	if err := db.apply([]migration{late}); err != nil {
		t.Fatalf("applying again: %v", err)
	}
}

// SQLite cannot change a CHECK constraint in place; the table is rebuilt. With
// foreign keys on, dropping the old table would cascade into its children, so
// a migration that starts with the marker runs with foreign keys off.
func TestRebuildMigrationKeepsChildRowsWhenMarkedForeignKeysOff(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "hejmo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	setup := migration{version: 200001010000, name: "200001010000_setup.sql", body: `
CREATE TABLE parent_probe (id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('a')));
CREATE TABLE child_probe (id TEXT PRIMARY KEY, parent_id TEXT NOT NULL REFERENCES parent_probe(id) ON DELETE CASCADE);
INSERT INTO parent_probe VALUES ('p', 'a');
INSERT INTO child_probe VALUES ('c', 'p');`}
	rebuild := migration{version: 200001010001, name: "200001010001_rebuild.sql", body: foreignKeysOffMarker + `
CREATE TABLE parent_probe_new (id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('a', 'b')));
INSERT INTO parent_probe_new SELECT id, kind FROM parent_probe;
DROP TABLE parent_probe;
ALTER TABLE parent_probe_new RENAME TO parent_probe;`}
	if err := db.apply([]migration{setup, rebuild}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM child_probe`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("child rows after the rebuild = %d, %v (the drop cascaded)", n, err)
	}
	if _, err := db.sql.Exec(`INSERT INTO parent_probe VALUES ('q', 'b')`); err != nil {
		t.Fatalf("new constraint not in effect: %v", err)
	}
	var on int
	if err := db.sql.QueryRow(`PRAGMA foreign_keys`).Scan(&on); err != nil || on != 1 {
		t.Fatalf("foreign keys are %d after the migration, want on (%v)", on, err)
	}
}

// Foreign keys are off only while the migration runs; a rebuild that leaves a
// child row without its parent must not be committed.
func TestMigrationWithForeignKeysOffIsRefusedIfItBreaksAReference(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "hejmo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	setup := migration{version: 200001010000, name: "200001010000_setup.sql", body: `
CREATE TABLE parent_probe (id TEXT PRIMARY KEY);
CREATE TABLE child_probe (id TEXT PRIMARY KEY, parent_id TEXT NOT NULL REFERENCES parent_probe(id));
INSERT INTO parent_probe VALUES ('p');
INSERT INTO child_probe VALUES ('c', 'p');`}
	bad := migration{version: 200001010001, name: "200001010001_bad.sql", body: foreignKeysOffMarker + `DELETE FROM parent_probe;`}
	if err := db.apply([]migration{setup}); err != nil {
		t.Fatal(err)
	}
	if err := db.apply([]migration{bad}); err == nil {
		t.Fatal("a migration that orphans a child row was accepted")
	}
	var n int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM parent_probe`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("parent rows = %d, %v: the failed migration was not rolled back", n, err)
	}
	var on int
	if err := db.sql.QueryRow(`PRAGMA foreign_keys`).Scan(&on); err != nil || on != 1 {
		t.Fatalf("foreign keys are %d after a failed migration, want on", on)
	}
}

// The meal-plan migration rebuilds the calendars table: calendars keep their
// events, sync state, audience and place in the list.
func TestMealPlanMigrationKeepsExistingCalendarsAndTheirData(t *testing.T) {
	ms, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	const name = "202611150930_meal_plan.sql"
	var at = -1
	for i, m := range ms {
		if m.name == name {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("%s not shipped", name)
	}
	// A database as it was before the migration.
	raw, err := openRaw(filepath.Join(t.TempDir(), "before.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.sql.Close()
	if _, err := raw.sql.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if err := raw.apply(ms[:at]); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO households (id, name, created_at, updated_at) VALUES ('h', 'Family', 't', 't')`,
		`INSERT INTO calendar_connections (id, household_id, kind, url, created_at) VALUES ('n', 'h', 'ics', 'https://example.org/a.ics', 't')`,
		`INSERT INTO calendars (id, household_id, name, kind, color, created_at, updated_at) VALUES ('c1', 'h', 'Family', 'local', '', 't', 't')`,
		`INSERT INTO calendars (id, household_id, name, kind, color, connection_id, read_only, shared, created_at, updated_at) VALUES ('c2', 'h', 'School', 'ics', '#112233', 'n', 1, 1, 't', 't')`,
		`INSERT INTO calendar_sync (calendar_id, interval_minutes, started_at, next_attempt_at) VALUES ('c2', 60, 't', 't')`,
		`INSERT INTO events (id, household_id, calendar_id, title, all_day, start_local, end_local, tzid, created_at, updated_at) VALUES ('e1', 'h', 'c1', 'Swim', 0, '2026-11-17T16:00:00', '2026-11-17T17:00:00', 'Europe/Berlin', 't', 't')`,
		`INSERT INTO events (id, household_id, calendar_id, title, all_day, start_local, end_local, tzid, created_at, updated_at) VALUES ('e2', 'h', 'c2', 'Holiday', 1, '2026-11-18', '2026-11-19', 'Europe/Berlin', 't', 't')`,
	} {
		if _, err := raw.sql.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := raw.apply(ms[at:]); err != nil {
		t.Fatalf("migrating a database with data: %v", err)
	}
	for q, want := range map[string]int{
		`SELECT COUNT(*) FROM events`: 2, `SELECT COUNT(*) FROM calendar_sync`: 1, `SELECT COUNT(*) FROM calendar_connections`: 1,
		`SELECT COUNT(*) FROM calendars WHERE id = 'c2' AND read_only = 1 AND shared = 1 AND color = '#112233' AND connection_id = 'n'`: 1,
	} {
		var n int
		if err := raw.sql.QueryRow(q).Scan(&n); err != nil || n != want {
			t.Fatalf("%s = %d, %v, want %d", q, n, err, want)
		}
	}
	var first string
	if err := raw.sql.QueryRow(`SELECT id FROM calendars ORDER BY created_at, rowid LIMIT 1`).Scan(&first); err != nil || first != "c1" {
		t.Fatalf("calendar order changed: first = %q, %v", first, err)
	}
	var cutoffs string
	if err := raw.sql.QueryRow(`SELECT cutoff_breakfast || ' ' || cutoff_lunch || ' ' || cutoff_snack || ' [' || cutoff_dinner || ']' FROM households`).Scan(&cutoffs); err != nil || cutoffs != "10:00 13:30 16:30 []" {
		t.Fatalf("cut-offs of an existing household = %q, %v", cutoffs, err)
	}
	if _, err := raw.sql.Exec(`INSERT INTO calendars (id, household_id, name, kind, created_at, updated_at) VALUES ('c3', 'h', 'Meal plan', 'meals', 't', 't')`); err != nil {
		t.Fatalf("meals calendar refused: %v", err)
	}
}

// openRaw opens a database without applying the shipped migrations.
func openRaw(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	return &DB{sql: conn}, nil
}
