package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// #294: people that exist before the migration get the switch by their role:
// on for children, off for adults.
func TestStreaksMigrationBackfillsTheSwitchByRole(t *testing.T) {
	t.Parallel()
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "hejmo.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	db := &DB{sql: conn}
	defer db.Close()
	if _, err := conn.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	all, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	var before []migration
	var mine migration
	for _, m := range all {
		if m.name == "202611231912_pauses.sql" {
			mine = m
			break
		}
		before = append(before, m)
	}
	if mine.name == "" {
		t.Fatal("the migration is not shipped")
	}
	if err := db.apply(before); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO households (id, name, created_at, updated_at) VALUES ('h1', 'Familie', 'x', 'x')`,
		`INSERT INTO people (id, name, created_at, updated_at) VALUES ('p1', 'Mia', 'x', 'x'), ('p2', 'Mama', 'x', 'x')`,
		`INSERT INTO household_members (household_id, person_id, role) VALUES ('h1', 'p1', 'child'), ('h1', 'p2', 'parent')`,
	} {
		if _, err := conn.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.apply([]migration{mine}); err != nil {
		t.Fatal(err)
	}
	if err := db.apply(all); err != nil { // the later migrations too: People reads their columns
		t.Fatal(err)
	}
	people, err := db.People("h1")
	if err != nil || len(people) != 2 {
		t.Fatalf("People = %v, %v", people, err)
	}
	for _, p := range people {
		if want := p.Role == RoleChild; p.StreaksShown != want {
			t.Errorf("%s (%s): StreaksShown = %v, want %v", p.Name, p.Role, p.StreaksShown, want)
		}
	}
}
