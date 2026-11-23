package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/hejmo-home/hejmo/internal/birthday"
	"github.com/hejmo-home/hejmo/internal/calendar"
)

// birthdayCalendarColor is the colour of the generated calendar in the web
// interface's calendar views.
const birthdayCalendarColor = "#A0526A"

// defaultBirthdayCalendarName names the calendar when the caller gave no name.
const defaultBirthdayCalendarName = "Birthdays"

// SyncAllBirthdays rewrites the generated calendar Birthdays of every
// household that has one. The box runs it at its start: the migration makes the
// calendar for contacts that exist before it, but cannot make the events (their
// ids are UUIDv5, see birthday.EventID). Nothing is touched that is right
// already.
func (db *DB) SyncAllBirthdays() error {
	ids, err := db.strings(`SELECT DISTINCT household_id FROM calendars WHERE kind = 'birthdays'`)
	if err != nil {
		return err
	}
	for _, id := range ids {
		tx, err := db.sql.Begin()
		if err != nil {
			return err
		}
		// The calendar exists, so its name is never used here.
		if err := syncBirthdays(tx, id, ""); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// syncBirthdays makes the calendar of a household say what its contacts say:
// one all-day yearly series per contact that has a birthday and is not switched
// off, titled with the Hejmo name (the age comes with each occurrence, see
// calendar.Expand). The calendar is generated on first use, read-only and
// shared through the box like the meal plan. An event that is right already is
// not touched, so CalDAV clients do not fetch it again.
func syncBirthdays(tx *sql.Tx, householdID, calendarName string) error {
	type want struct {
		title                 string
		day, month, birthYear int
	}
	rows, err := tx.Query(`SELECT id, hejmo_name, bday_day, bday_month, COALESCE(bday_year, 0) FROM contacts
		WHERE household_id = ? AND bday_day IS NOT NULL AND hide_birthday = 0`, householdID)
	if err != nil {
		return err
	}
	wanted := map[string]want{}
	for rows.Next() {
		var id string
		var w want
		if err := rows.Scan(&id, &w.title, &w.day, &w.month, &w.birthYear); err != nil {
			rows.Close()
			return err
		}
		wanted[birthday.EventID(id)] = w
	}
	if err := rows.Close(); err != nil {
		return err
	}
	type have struct{ title, start, rrule string }
	haveEvents := map[string]have{}
	rows, err = tx.Query(`SELECT e.id, e.title, e.start_local, e.rrule FROM events e JOIN calendars c ON c.id = e.calendar_id
		WHERE c.household_id = ? AND c.kind = 'birthdays'`, householdID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var h have
		if err := rows.Scan(&id, &h.title, &h.start, &h.rrule); err != nil {
			rows.Close()
			return err
		}
		haveEvents[id] = h
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(wanted) == 0 && len(haveEvents) == 0 {
		return nil
	}
	for id := range haveEvents {
		if _, ok := wanted[id]; !ok {
			if _, err := tx.Exec(`DELETE FROM events WHERE id = ? AND household_id = ?`, id, householdID); err != nil {
				return err
			}
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	calID, err := birthdayCalendarID(tx, householdID, calendarName)
	if err != nil {
		return err
	}
	loc := time.UTC
	var tz string
	if err := tx.QueryRow(`SELECT timezone FROM households WHERE id = ?`, householdID).Scan(&tz); err == nil {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	for id, w := range wanted {
		year, rule := birthday.Series(w.day, w.month, w.birthYear)
		start := time.Date(year, time.Month(w.month), w.day, 0, 0, 0, 0, loc)
		ev := calendar.Event{ID: id, CalendarID: calID, Title: w.title, AllDay: true, Start: start, End: start.AddDate(0, 0, 1), RRule: rule, Birthday: true}
		old, exists := haveEvents[id]
		switch {
		case !exists:
			err = insertEventRow(tx, householdID, ev)
		case old.title != ev.Title || old.start != start.Format(dateLayout) || old.rrule != rule:
			err = updateEventRow(tx, householdID, ev)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// birthdayCalendarID returns the id of the household's Birthdays calendar,
// creating it (read-only, shared through the box) if the household has none.
func birthdayCalendarID(tx *sql.Tx, householdID, name string) (string, error) {
	var id string
	err := tx.QueryRow(`SELECT id FROM calendars WHERE household_id = ? AND kind = 'birthdays' ORDER BY created_at, rowid LIMIT 1`, householdID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if name == "" {
		name = defaultBirthdayCalendarName
	}
	id, ts := newID(), now()
	_, err = tx.Exec(`INSERT INTO calendars (id, household_id, name, kind, color, read_only, shared, created_at, updated_at)
		VALUES (?, ?, ?, 'birthdays', ?, 1, 1, ?, ?)`, id, householdID, name, birthdayCalendarColor, ts, ts)
	return id, err
}
