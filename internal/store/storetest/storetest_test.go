package storetest_test

import (
	"database/sql"
	"os"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/store/storetest"
)

// A copied template is a fully migrated database, like store.Open makes.
func TestPathHoldsAFullyMigratedDatabase(t *testing.T) {
	db, err := store.Open(storetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	v, err := db.SchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if v != store.LatestVersion() {
		t.Fatalf("schema version %d, want %d", v, store.LatestVersion())
	}
}

// Every test gets its own file: writing to one copy leaves the next untouched.
func TestEveryPathIsAnIndependentDatabase(t *testing.T) {
	a, b := storetest.Path(t), storetest.Path(t)
	if a == b {
		t.Fatal("two calls returned the same path")
	}
	dbA, err := store.Open(a)
	if err != nil {
		t.Fatal(err)
	}
	defer dbA.Close()
	if _, err := dbA.SQL().Exec(`CREATE TABLE scratch (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	dbB, err := store.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	defer dbB.Close()
	if _, err := dbB.SQL().Exec(`SELECT x FROM scratch`); err == nil {
		t.Fatal("the second database sees the first one's table")
	}
	if _, err := os.Stat(a); err != nil {
		t.Fatal(err)
	}
}

// The copy is migrated before store.Open sees it: read it with a plain
// connection that migrates nothing. (Through store.Open alone a stub that
// returns an empty path would pass, as Open migrates it.)
func TestPathIsMigratedBeforeOpen(t *testing.T) {
	conn, err := sql.Open("sqlite", "file:"+storetest.Path(t)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("the file holds no migrated schema: %v", err)
	}
	if n == 0 {
		t.Fatal("no migration recorded in the file")
	}
}
