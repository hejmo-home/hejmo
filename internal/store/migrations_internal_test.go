package store

import (
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
