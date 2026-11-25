package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
)

// #302: birthdays of contacts that exist before the migration are in the
// calendar after it and the start of the box, and so are the leap day and a missing year.
func TestBirthdaysMigrationWritesTheCalendarForExistingContacts(t *testing.T) {
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
	var before, after []migration
	var mine migration
	for _, m := range all {
		switch {
		case mine.name != "":
			after = append(after, m)
		case m.name == "202611232200_birthdays.sql":
			mine = m
		default:
			before = append(before, m)
		}
	}
	if mine.name == "" {
		t.Fatal("the migration is not shipped")
	}
	if err := db.apply(before); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO households (id, name, timezone, created_at, updated_at) VALUES ('h1', 'Familie', 'Europe/Berlin', 'x', 'x'), ('h2', 'Leer', 'Europe/Berlin', 'x', 'x')`,
		`INSERT INTO contacts (id, household_id, hejmo_name, bday_day, bday_month, bday_year, created_at, updated_at) VALUES
			('c1', 'h1', 'Mia', 12, 5, 2019, 'x', 'x'), ('c2', 'h1', 'Oma Gerda', 3, 5, NULL, 'x', 'x'),
			('c3', 'h1', 'Leap', 29, 2, 2020, 'x', 'x'), ('c4', 'h1', 'No birthday', NULL, NULL, NULL, 'x', 'x')`,
	} {
		if _, err := conn.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.apply([]migration{mine}); err != nil {
		t.Fatal(err)
	}
	cals, err := db.Calendars("h1")
	if err != nil || len(cals) != 1 || cals[0].Kind != "birthdays" || !cals[0].ReadOnly || !cals[0].Shared {
		t.Fatalf("calendars of h1 = %+v, %v", cals, err)
	}
	if cals, _ := db.Calendars("h2"); len(cals) != 0 {
		t.Errorf("a household without birthdays got a calendar: %+v", cals)
	}
	// The migration makes the calendar; the events are written by the box at
	// its next start (an SQL migration cannot make the UUIDv5 ids).
	if err := db.apply(after); err != nil { // the sync reads the columns of later migrations (name days, ticket 318)
		t.Fatal(err)
	}
	if err := db.SyncAllBirthdays(); err != nil {
		t.Fatal(err)
	}
	from, to := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	events, err := db.EventsBetween("h1", from, to)
	if err != nil {
		t.Fatal(err)
	}
	occs, err := calendar.Expand(events, from, to)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range occs {
		got = append(got, o.Start.Format("02.01. ")+o.Title)
	}
	want := []string{"28.02. Leap (6)", "03.05. Oma Gerda", "12.05. Mia (7)"}
	if len(got) != len(want) {
		t.Fatalf("2026 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("2026 = %v, want %v", got, want)
		}
	}
}
