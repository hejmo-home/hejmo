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

// Issue #108: sessions made before the migration get a public id of their own
// (not derived from the token hash), no device and no last use.
func TestBrowserSessionsMigrationBackfillsExistingRows(t *testing.T) {
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "legacy.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	old := &DB{sql: conn}
	defer old.Close()
	if _, err := conn.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	ms, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	var before, target []migration
	for _, m := range ms {
		if strings.HasSuffix(m.name, "_browser_sessions.sql") {
			target = append(target, m)
		} else if len(target) == 0 {
			before = append(before, m)
		}
	}
	if len(target) != 1 {
		t.Fatalf("found %d browser_sessions migrations, want 1", len(target))
	}
	if err := old.apply(before); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO people (id, name, created_at, updated_at) VALUES ('p1', 'Mama', 'x', 'x')`); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"tok-a", "tok-b"} {
		if _, err := conn.Exec(`INSERT INTO sessions (token_hash, person_id, created_at, expires_at) VALUES (?, 'p1', '2026-11-01T00:00:00Z', '2026-12-01T00:00:00Z')`, h); err != nil {
			t.Fatal(err)
		}
	}
	if err := old.apply(target); err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(`SELECT token_hash, id, device, last_seen_at FROM sessions ORDER BY token_hash`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var hash, id, device string
		var seen sql.NullString
		if err := rows.Scan(&hash, &id, &device, &seen); err != nil {
			t.Fatal(err)
		}
		if id == "" || strings.Contains(id, hash) || device != "" || seen.Valid {
			t.Errorf("row %s: id %q device %q last_seen %v", hash, id, device, seen)
		}
		ids = append(ids, id)
	}
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Errorf("ids = %v, want two distinct", ids)
	}
}
