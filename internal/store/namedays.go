package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/nameday"
)

// Name days in the generated calendar Birthdays (ticket 318): the household
// setting, the choice of a contact, and the events written from both.

// MaxNameDayName is the longest name a contact can be given for its name day.
const MaxNameDayName = 40

// nameDayYearsBack and nameDayYearsAhead are the years a name day that follows
// Easter is written for: the box rewrites the calendar at every start.
const (
	nameDayYearsBack  = 1
	nameDayYearsAhead = 9
)

// NameDayCalendar is the list of the household, while name days are on.
func (h Household) NameDayCalendar() (*nameday.Calendar, bool) {
	if !h.NameDaysOn {
		return nil, false
	}
	return nameday.Get(nameday.List(h.NameDayList))
}

// DroppedNameDay is a contact whose hand-picked name day the new list does not have.
type DroppedNameDay struct{ ID, Name string }

// NameDayChange is what SetNameDays did besides saving the setting.
type NameDayChange struct {
	// Dropped are the contacts whose hand-picked day or name the new church does not have: they are on
	// the usual day of their name again.
	Dropped []DroppedNameDay
}

// ValidateNameDays checks the choice of SetNameDays without writing anything,
// so a caller can refuse a request before it saves any other part of it.
func ValidateNameDays(on bool, list string) error {
	if on && list == "" {
		return &FieldError{Field: "name_day_list", Msg: "name_day_list: choose a church"}
	}
	if list != "" && !nameday.List(list).Valid() {
		return &FieldError{Field: "name_day_list", Msg: "name_day_list: unknown list " + list}
	}
	return nil
}

// SetNameDays switches name days on or off and chooses the church (an empty list
// leaves it as it is). Switching on needs a church (a *FieldError at name_day_list). Changing the church sets the
// choices of contacts that the new list does not have back to auto and reports
// them; switching off keeps the church and every choice. The calendar is made
// when it is missing, and every name-day event is written again.
func (db *DB) SetNameDays(householdID string, on bool, list, calendarName, word string) (NameDayChange, error) {
	var change NameDayChange
	if err := ValidateNameDays(on, list); err != nil {
		return change, err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return change, err
	}
	defer tx.Rollback()
	var curList string
	if err := tx.QueryRow(`SELECT COALESCE(name_day_list, '') FROM households WHERE id = ?`, householdID).Scan(&curList); errors.Is(err, sql.ErrNoRows) {
		return change, ErrNotFound
	} else if err != nil {
		return change, err
	}
	newList := curList
	if list != "" { // switching off leaves the church as it is; a church given while off is kept for later
		newList = list
	}
	if list != "" && list != curList {
		if change.Dropped, err = dropNameDayChoices(tx, householdID, nameday.List(list)); err != nil {
			return change, err
		}
	}
	if err := expectOne(tx.Exec(`UPDATE households SET name_days_on = ?, name_day_list = ?, name_day_word = CASE WHEN ? <> '' THEN ? ELSE name_day_word END, updated_at = ? WHERE id = ?`, on, nullString(newList), word, word, now(), householdID)); err != nil {
		return change, err
	}
	if err := syncBirthdays(tx, householdID, calendarName); err != nil {
		return change, err
	}
	return change, tx.Commit()
}

// dropNameDayChoices sets the choices the list does not have back to auto.
func dropNameDayChoices(tx *sql.Tx, householdID string, l nameday.List) ([]DroppedNameDay, error) {
	cal, ok := nameday.Get(l)
	if !ok {
		return nil, nil
	}
	rows, err := tx.Query(`SELECT id, hejmo_name, name_day_mode, name_day_entry, name_day_name FROM contacts
		WHERE household_id = ? AND name_day_mode IN ('entry', 'name') ORDER BY hejmo_name COLLATE NOCASE`, householdID)
	if err != nil {
		return nil, err
	}
	var dropped []DroppedNameDay
	for rows.Next() {
		var id, name, mode, entry, nm string
		if err := rows.Scan(&id, &name, &mode, &entry, &nm); err != nil {
			rows.Close()
			return nil, err
		}
		keep := false
		if mode == nameday.ModeEntry {
			_, keep = cal.Entry(entry)
		} else {
			keep = cal.Lookup(nm) != nil
		}
		if !keep {
			dropped = append(dropped, DroppedNameDay{ID: id, Name: name})
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, d := range dropped {
		if _, err := tx.Exec(`UPDATE contacts SET name_day_mode = 'auto', name_day_entry = '', name_day_name = '', name_day_by = NULL WHERE id = ?`, d.ID); err != nil {
			return nil, err
		}
	}
	return dropped, nil
}

// nameDay is the event of a name day, before it is written.
type nameDay struct {
	id, contactID, title, rrule string
	start                       time.Time
}

// nameDayEvents lists the name-day events a household wants: none while name
// days are off; else one yearly series per contact whose day is fixed, and one
// event per year (from last year on) for a day that follows Easter or moves
// with it.
func nameDayEvents(tx *sql.Tx, householdID string, loc *time.Location) ([]nameDay, error) {
	var on bool
	var list, word string
	if err := tx.QueryRow(`SELECT name_days_on, COALESCE(name_day_list, ''), name_day_word FROM households WHERE id = ?`, householdID).Scan(&on, &list, &word); err != nil {
		return nil, err
	}
	if word == "" {
		word = "Name day"
	}
	if !on {
		return nil, nil
	}
	cal, ok := nameday.Get(nameday.List(list))
	if !ok {
		return nil, nil
	}
	rows, err := tx.Query(`SELECT id, hejmo_name, full_name, name_day_mode, name_day_entry, name_day_name FROM contacts WHERE household_id = ?`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	thisYear := time.Now().In(loc).Year()
	var out []nameDay
	for rows.Next() {
		var id, hejmo, full string
		var ch nameday.Choice
		if err := rows.Scan(&id, &hejmo, &full, &ch.Mode, &ch.Entry, &ch.Name); err != nil {
			return nil, err
		}
		res := cal.For(full, hejmo, ch)
		if !res.Found {
			continue
		}
		title := nameday.Title(word, hejmo, res.Entry.Name)
		if res.Entry.Yearly() {
			m, d := res.Entry.On(thisYear).Date.Month(), res.Entry.On(thisYear).Date.Day()
			start := time.Date(2000, m, d, 0, 0, 0, 0, loc) // the series starts in a leap year, so 29 February is possible
			out = append(out, nameDay{id: nameday.EventID(id, 0), contactID: id, title: title, rrule: "FREQ=YEARLY", start: start})
			continue
		}
		for y := thisYear - nameDayYearsBack; y <= thisYear+nameDayYearsAhead; y++ {
			date := res.Entry.On(y).Date
			out = append(out, nameDay{id: nameday.EventID(id, y), contactID: id, title: title, start: time.Date(y, date.Month(), date.Day(), 0, 0, 0, 0, loc)})
		}
	}
	return out, rows.Err()
}

// checkNameDayChoice validates the choice of a contact against the household's
// list while name days are on, and cleans what the mode does not use.
func checkNameDayChoice(tx *sql.Tx, householdID string, in *ContactInput) error {
	in.NameDayName = strings.TrimSpace(in.NameDayName)
	switch in.NameDayMode {
	case "":
		return nil
	case nameday.ModeAuto, nameday.ModeNone:
		in.NameDayEntry, in.NameDayName = "", ""
		return nil
	case nameday.ModeEntry, nameday.ModeName:
	default:
		return &FieldError{Field: "name_day", Msg: "name_day: unknown mode " + in.NameDayMode}
	}
	if in.NameDayMode == nameday.ModeEntry {
		in.NameDayName = ""
	} else {
		in.NameDayEntry = ""
		if in.NameDayName == "" || utf8.RuneCountInString(in.NameDayName) > MaxNameDayName {
			return &FieldError{Field: "name_day_name", Msg: "name_day_name: a name of 1 to 40 characters"}
		}
	}
	var on bool
	var list string
	if err := tx.QueryRow(`SELECT name_days_on, COALESCE(name_day_list, '') FROM households WHERE id = ?`, householdID).Scan(&on, &list); err != nil {
		return err
	}
	cal, ok := nameday.Get(nameday.List(list))
	if !on || !ok {
		return nil // nothing to check against; switching name days on drops what the list does not have
	}
	if in.NameDayMode == nameday.ModeEntry {
		if _, ok := cal.Entry(in.NameDayEntry); !ok {
			return &FieldError{Field: "name_day", Msg: "name_day: unknown entry " + in.NameDayEntry}
		}
	} else if cal.Lookup(in.NameDayName) == nil {
		return &FieldError{Field: "name_day_name", Msg: "name_day_name: the list does not know " + in.NameDayName}
	}
	return nil
}

// writeNameDayChoice stores the choice of a save. An empty mode keeps what the
// contact has. Who chose is the person who saved when the choice changed.
func writeNameDayChoice(tx *sql.Tx, id, by string, in ContactInput, curMode, curEntry, curName string) error {
	if in.NameDayMode == "" {
		return nil
	}
	if in.NameDayMode == curMode && in.NameDayEntry == curEntry && in.NameDayName == curName {
		return nil // nothing changed, nobody chose again
	}
	chooser := nullString(by)
	if in.NameDayMode == nameday.ModeAuto {
		chooser = nil
	}
	_, err := tx.Exec(`UPDATE contacts SET name_day_mode = ?, name_day_entry = ?, name_day_name = ?, name_day_by = ? WHERE id = ?`,
		in.NameDayMode, in.NameDayEntry, in.NameDayName, chooser, id)
	return err
}
