// Package storetest gives tests a migrated database without migrating it each
// time: the migrations run once per test binary into a template file, and every
// test gets a copy. Migrating under the race detector costs about 0.7 s, which
// added up to most of the test time (ticket 228).
package storetest

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

var (
	once     sync.Once
	template []byte
	failure  error
)

func load() ([]byte, error) {
	once.Do(func() {
		dir, err := os.MkdirTemp("", "hejmo-template-*")
		if err != nil {
			failure = err
			return
		}
		defer os.RemoveAll(dir)
		path := filepath.Join(dir, "template.db")
		db, err := store.Open(path)
		if err != nil {
			failure = err
			return
		}
		// Fold the write-ahead log into the main file so one file is the whole database.
		if _, err := db.SQL().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			db.Close()
			failure = err
			return
		}
		if err := db.Close(); err != nil {
			failure = err
			return
		}
		template, failure = os.ReadFile(path)
	})
	return template, failure
}

// Path returns the path of a fresh, fully migrated database file in the test's
// temporary directory. Open it with store.Open, which finds nothing to migrate.
func Path(t testing.TB) string {
	t.Helper()
	body, err := load()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "hejmo.db")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
