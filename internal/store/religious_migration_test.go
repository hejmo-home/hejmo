package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// #339: the migration rebuilds the calendars table to allow the new kind. The calendars and events a household
// has already are all there after it, and the household's settings start off.
func TestReligiousHolidaysMigrationKeepsCalendarsAndEventsAndStartsOff(t *testing.T) {
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
		case m.name == "202611251630_religious_holidays.sql":
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
		`INSERT INTO households (id, name, timezone, created_at, updated_at) VALUES ('h1', 'Familie', 'Europe/Berlin', 'x', 'x')`,
		`INSERT INTO calendars (id, household_id, name, kind, color, read_only, shared, created_at, updated_at) VALUES ('c1', 'h1', 'Familie', 'local', '#111111', 0, 1, 'x', 'x'), ('c2', 'h1', 'Geburtstage', 'birthdays', '#A0526A', 1, 1, 'x', 'x')`,
		`INSERT INTO events (id, household_id, calendar_id, title, all_day, start_local, end_local, tzid, created_at, updated_at) VALUES ('e1', 'h1', 'c1', 'Zahnarzt', 1, '2026-12-01', '2026-12-02', 'Europe/Berlin', 'x', 'x')`,
	} {
		if _, err := conn.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.apply([]migration{mine}); err != nil {
		t.Fatal(err)
	}
	if err := db.apply(after); err != nil {
		t.Fatal(err)
	}
	var cals, events int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM calendars`).Scan(&cals); err != nil || cals != 2 {
		t.Errorf("%d calendars after the migration, want 2 (%v)", cals, err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM events WHERE id = 'e1'`).Scan(&events); err != nil || events != 1 {
		t.Errorf("the event went with the table: %d (%v)", events, err)
	}
	if _, err := conn.Exec(`INSERT INTO calendars (id, household_id, name, kind, read_only, shared, created_at, updated_at) VALUES ('c3', 'h1', 'Religiöse Feiertage', 'religious', 1, 1, 'x', 'x')`); err != nil {
		t.Errorf("the kind religious is refused: %v", err)
	}
	h, err := db.Household("h1")
	if err != nil {
		t.Fatal(err)
	}
	if h.ReligiousHolidaysOn || len(h.Religions) != 0 || h.IslamicCalendar != "diyanet" || h.OrthodoxCalendar != "revised_julian" {
		t.Errorf("settings do not start off: %+v", h)
	}
	if _, err := conn.Exec(`UPDATE households SET islamic_calendar = 'moon' WHERE id = 'h1'`); err == nil {
		t.Error("an unknown Islamic calendar was accepted by the database")
	}
}
