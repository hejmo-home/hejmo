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
