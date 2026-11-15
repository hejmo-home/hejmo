package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/hejmo-home/hejmo/internal/calendar"
)

// NewID returns a new random id (UUID v4), for callers that create records
// before storing them (series edits).
func NewID() string { return newID() }

// Group is a subgroup of a household, e.g. "School children".
type Group struct {
	ID      string
	Name    string
	Members []string
}

// Calendar is a source of events.
type Calendar struct {
	ID          string
	HouseholdID string
	Name        string
	Kind        string // local, ics, caldav, google
	Color       string
	ReadOnly    bool // ICS calendars are always read-only
	// Shared is "share through the box": phones may subscribe to the calendar
	// over CalDAV. Always true for the family calendar.
	Shared bool
	// Waste marks a read-only calendar as the household's waste collection
	// calendar (ADR 0015): its events feed only the tomorrow tile and the night page.
	Waste bool
	// Family marks the household's family calendar: the first local calendar.
	Family   bool
	Audience calendar.Audience
}

const (
	localLayout = "2006-01-02T15:04:05"
	dateLayout  = "2006-01-02"
	// DayLayout formats a day (YYYY-MM-DD), the key of day notes.
	DayLayout = dateLayout
)

// CreateGroup creates a subgroup with members.
func (db *DB) CreateGroup(householdID, name string, members []string) (Group, error) {
	if strings.TrimSpace(name) == "" {
		return Group{}, errors.New("store: group name required")
	}
	g := Group{ID: newID(), Name: strings.TrimSpace(name), Members: members}
	tx, err := db.sql.Begin()
	if err != nil {
		return Group{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO groups (id, household_id, name, created_at) VALUES (?, ?, ?, ?)`, g.ID, householdID, g.Name, now()); err != nil {
		return Group{}, err
	}
	for _, p := range members {
		if _, err := tx.Exec(`INSERT INTO group_members (group_id, person_id)
			SELECT ?, person_id FROM household_members WHERE household_id = ? AND person_id = ?`, g.ID, householdID, p); err != nil {
			return Group{}, err
		}
	}
	return g, tx.Commit()
}

// Groups lists a household's subgroups with members.
func (db *DB) Groups(householdID string) ([]Group, error) {
	rows, err := db.sql.Query(`SELECT g.id, g.name, COALESCE(m.person_id, '') FROM groups g
		LEFT JOIN group_members m ON m.group_id = g.id WHERE g.household_id = ? ORDER BY g.name, g.id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var id, name, member string
		if err := rows.Scan(&id, &name, &member); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].ID != id {
			out = append(out, Group{ID: id, Name: name})
		}
		if member != "" {
			out[len(out)-1].Members = append(out[len(out)-1].Members, member)
		}
	}
	return out, rows.Err()
}

// PersonGroups returns the ids of the groups a person belongs to.
func (db *DB) PersonGroups(personID string) ([]string, error) {
	return db.strings(`SELECT group_id FROM group_members WHERE person_id = ? ORDER BY group_id`, personID)
}

// CreateCalendar stores a calendar with its audience.
func (db *DB) CreateCalendar(c Calendar) (Calendar, error) {
	c.ID = newID()
	tx, err := db.sql.Begin()
	if err != nil {
		return Calendar{}, err
	}
	defer tx.Rollback()
	ts := now()
	if _, err := tx.Exec(`INSERT INTO calendars (id, household_id, name, kind, color, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.HouseholdID, c.Name, c.Kind, c.Color, ts, ts); err != nil {
		return Calendar{}, err
	}
	if err := writeAudience(tx, "calendar_audience", "calendar_id", c.ID, c.Audience); err != nil {
		return Calendar{}, err
	}
	return c, tx.Commit()
}

// Calendars lists a household's calendars with their audiences.
func (db *DB) Calendars(householdID string) ([]Calendar, error) {
	rows, err := db.sql.Query(`SELECT id, household_id, name, kind, color, read_only, shared, waste FROM calendars WHERE household_id = ? ORDER BY created_at, rowid`, householdID)
	if err != nil {
		return nil, err
	}
	var out []Calendar
	for rows.Next() {
		var c Calendar
		if err := rows.Scan(&c.ID, &c.HouseholdID, &c.Name, &c.Kind, &c.Color, &c.ReadOnly, &c.Shared, &c.Waste); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	rows.Close()
	for i := range out {
		if out[i].Kind == "local" && !hasFamily(out[:i]) {
			out[i].Family, out[i].Shared = true, true
		}
		if out[i].Audience, err = db.audience("calendar_audience", "calendar_id", out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, rows.Err()
}

// hasFamily reports whether the family calendar is among cals.
func hasFamily(cals []Calendar) bool {
	for _, c := range cals {
		if c.Family {
			return true
		}
	}
	return false
}

// Calendar returns one calendar of the household.
func (db *DB) Calendar(householdID, calendarID string) (Calendar, error) {
	cals, err := db.Calendars(householdID)
	if err != nil {
		return Calendar{}, err
	}
	for _, c := range cals {
		if c.ID == calendarID {
			return c, nil
		}
	}
	return Calendar{}, ErrNotFound
}

// SetCalendarShared switches "share through the box" for a calendar. The
// family calendar is always served: asking to change it is invalid.
func (db *DB) SetCalendarShared(householdID, calendarID string, shared bool) error {
	c, err := db.Calendar(householdID, calendarID)
	if err != nil {
		return err
	}
	if c.Family {
		return fmt.Errorf("%w: the family calendar is always shared", ErrInvalid)
	}
	return expectOne(db.sql.Exec(`UPDATE calendars SET shared = ?, updated_at = ? WHERE id = ? AND household_id = ?`, shared, now(), calendarID, householdID))
}

// CalendarEvents returns every event of one calendar: single events, series
// masters and changed occurrences, masters first.
func (db *DB) CalendarEvents(householdID, calendarID string) ([]calendar.Event, error) {
	return db.queryEvents(eventSelect+` WHERE household_id = ? AND calendar_id = ? ORDER BY series_id IS NOT NULL, start_local, id`, householdID, calendarID)
}

// FamilyCalendar returns the household's local calendar, creating it once.
func (db *DB) FamilyCalendar(householdID string) (Calendar, error) {
	cals, err := db.Calendars(householdID)
	if err != nil {
		return Calendar{}, err
	}
	for _, c := range cals {
		if c.Family {
			return c, nil
		}
	}
	return db.CreateCalendar(Calendar{HouseholdID: householdID, Name: "Family", Kind: "local"})
}

// EnsureFamilyCalendar creates the family calendar if the household has none yet.
func (db *DB) EnsureFamilyCalendar(householdID string) error {
	_, err := db.FamilyCalendar(householdID)
	return err
}

// CreateEvent stores a new event (keeps e.ID if set).
func (db *DB) CreateEvent(householdID string, e calendar.Event) (calendar.Event, error) {
	if e.ID == "" {
		e.ID = newID()
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return calendar.Event{}, err
	}
	defer tx.Rollback()
	if err := insertEvent(tx, householdID, e); err != nil {
		return calendar.Event{}, err
	}
	return e, tx.Commit()
}

// ApplyChange applies a series edit atomically.
func (db *DB) ApplyChange(householdID string, ch calendar.Change) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Create first: updated overrides may point at a newly created series.
	for _, e := range ch.Create {
		if err := insertEvent(tx, householdID, e); err != nil {
			return err
		}
	}
	for _, e := range ch.Update {
		if err := updateEvent(tx, householdID, e); err != nil {
			return err
		}
	}
	for _, id := range ch.Delete {
		if err := expectOne(tx.Exec(`DELETE FROM events WHERE id = ? AND household_id = ?`, id, householdID)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ErrInvalid marks input the caller can fix (maps to HTTP 422).
var ErrInvalid = errors.New("invalid")

// FieldError says which field of a form was refused. It matches ErrInvalid
// with errors.Is.
type FieldError struct{ Field, Msg string }

func (e *FieldError) Error() string { return ErrInvalid.Error() + ": " + e.Msg }
func (e *FieldError) Unwrap() error { return ErrInvalid }

func validateEvent(e calendar.Event) error {
	if strings.TrimSpace(e.Title) == "" {
		return fmt.Errorf("%w: event title required", ErrInvalid)
	}
	return validateTimes(e)
}

// validateTimes is validateEvent without the title, for synced events: a feed
// may hold untitled events, and the placeholder is localised when shown.
func validateTimes(e calendar.Event) error {
	if !e.End.After(e.Start) {
		return fmt.Errorf("%w: event must end after it starts", ErrInvalid)
	}
	return nil
}

func eventColumns(e calendar.Event) (start, end, tz string, recurrenceID any, seriesID any) {
	tz = e.Start.Location().String()
	layout := localLayout
	if e.AllDay {
		layout = dateLayout
	}
	start, end = e.Start.Format(layout), e.End.In(e.Start.Location()).Format(layout)
	if e.SeriesID != "" {
		seriesID, recurrenceID = e.SeriesID, e.RecurrenceID.UTC().Format(time.RFC3339)
	}
	return
}

func insertEvent(tx *sql.Tx, householdID string, e calendar.Event) error {
	if err := validateEvent(e); err != nil {
		return err
	}
	return insertEventRow(tx, householdID, e)
}

func insertEventRow(tx *sql.Tx, householdID string, e calendar.Event) error {
	if err := validateTimes(e); err != nil {
		return err
	}
	start, end, tz, recID, seriesID := eventColumns(e)
	ts := now()
	if _, err := tx.Exec(`INSERT INTO events (id, household_id, calendar_id, title, description, location, all_day,
		start_local, end_local, tzid, rrule, series_id, recurrence_id, created_at, updated_at)
		SELECT ?, ?, id, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ? FROM calendars WHERE id = ? AND household_id = ?`,
		e.ID, householdID, e.Title, e.Description, e.Location, e.AllDay, start, end, tz, e.RRule, seriesID, recID, ts, ts,
		e.CalendarID, householdID); err != nil {
		return err
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE id = ?`, e.ID).Scan(&n); err != nil || n == 0 {
		return fmt.Errorf("%w: unknown calendar", ErrInvalid)
	}
	return writeEventDetails(tx, e)
}

func updateEvent(tx *sql.Tx, householdID string, e calendar.Event) error {
	if err := validateEvent(e); err != nil {
		return err
	}
	return updateEventRow(tx, householdID, e)
}

func updateEventRow(tx *sql.Tx, householdID string, e calendar.Event) error {
	if err := validateTimes(e); err != nil {
		return err
	}
	start, end, tz, recID, seriesID := eventColumns(e)
	if err := expectOne(tx.Exec(`UPDATE events SET title = ?, description = ?, location = ?, all_day = ?, start_local = ?, end_local = ?,
		tzid = ?, rrule = ?, series_id = ?, recurrence_id = ?, updated_at = ? WHERE id = ? AND household_id = ?`,
		e.Title, e.Description, e.Location, e.AllDay, start, end, tz, e.RRule, seriesID, recID, now(), e.ID, householdID)); err != nil {
		return err
	}
	for _, q := range []string{`DELETE FROM event_exdates WHERE event_id = ?`, `DELETE FROM event_audience WHERE event_id = ?`} {
		if _, err := tx.Exec(q, e.ID); err != nil {
			return err
		}
	}
	return writeEventDetails(tx, e)
}

func writeEventDetails(tx *sql.Tx, e calendar.Event) error {
	for _, x := range e.ExDates {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO event_exdates (event_id, occurrence) VALUES (?, ?)`, e.ID, x.UTC().Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return writeAudience(tx, "event_audience", "event_id", e.ID, calendar.Audience{People: e.People, Groups: e.Groups})
}

func writeAudience(tx *sql.Tx, table, key, id string, a calendar.Audience) error {
	for _, p := range a.People {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO `+table+` (`+key+`, person_id) VALUES (?, ?)`, id, p); err != nil {
			return err
		}
	}
	for _, g := range a.Groups {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO `+table+` (`+key+`, group_id) VALUES (?, ?)`, id, g); err != nil {
			return err
		}
	}
	return nil
}

const eventSelect = `SELECT id, calendar_id, title, description, location, all_day, start_local, end_local, tzid, rrule,
	COALESCE(series_id, ''), COALESCE(recurrence_id, ''), updated_at FROM events`

// Series returns the event with this id together with its series: the
// master and all changed occurrences.
func (db *DB) Series(householdID, eventID string) ([]calendar.Event, error) {
	var master string
	err := db.sql.QueryRow(`SELECT COALESCE(series_id, id) FROM events WHERE id = ? AND household_id = ?`, eventID, householdID).Scan(&master)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return db.queryEvents(eventSelect+` WHERE household_id = ? AND (id = ? OR series_id = ?) ORDER BY series_id IS NOT NULL, start_local`,
		householdID, master, master)
}

// EventsBetween loads everything needed to expand [from, to): single events
// near the range, all series masters and all changed occurrences.
func (db *DB) EventsBetween(householdID string, from, to time.Time) ([]calendar.Event, error) {
	lo := from.AddDate(0, 0, -1).Format(localLayout)
	hi := to.AddDate(0, 0, 1).Format(localLayout)
	return db.queryEvents(eventSelect+` WHERE household_id = ? AND (rrule != '' OR series_id IS NOT NULL OR (end_local > ? AND start_local < ?))
		ORDER BY start_local`, householdID, lo, hi)
}

// SearchEvents finds events whose title, description or location contain
// all words of q (prefix match, case- and accent-insensitive).
func (db *DB) SearchEvents(householdID, q string) ([]calendar.Event, error) {
	var terms []string
	for _, w := range strings.FieldsFunc(q, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		terms = append(terms, `"`+w+`"*`)
	}
	if len(terms) == 0 {
		return nil, nil
	}
	return db.queryEvents(eventSelect+` WHERE household_id = ? AND rowid IN (SELECT rowid FROM events_fts WHERE events_fts MATCH ?)
		ORDER BY start_local LIMIT 100`, householdID, strings.Join(terms, " "))
}

func (db *DB) queryEvents(query string, args ...any) ([]calendar.Event, error) {
	rows, err := db.sql.Query(query, args...)
	if err != nil {
		return nil, err
	}
	var out []calendar.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := db.loadEventDetails(&out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func scanEvent(rows *sql.Rows) (calendar.Event, error) {
	var e calendar.Event
	var start, end, tz, recID, updated string
	if err := rows.Scan(&e.ID, &e.CalendarID, &e.Title, &e.Description, &e.Location, &e.AllDay, &start, &end, &tz, &e.RRule, &e.SeriesID, &recID, &updated); err != nil {
		return e, err
	}
	e.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return e, err
	}
	layout := localLayout
	if e.AllDay {
		layout = dateLayout
	}
	if e.Start, err = time.ParseInLocation(layout, start, loc); err != nil {
		return e, err
	}
	if e.End, err = time.ParseInLocation(layout, end, loc); err != nil {
		return e, err
	}
	if recID != "" {
		r, err := time.Parse(time.RFC3339, recID)
		if err != nil {
			return e, err
		}
		e.RecurrenceID = r.In(loc)
	}
	return e, nil
}

func (db *DB) loadEventDetails(e *calendar.Event) error {
	ex, err := db.strings(`SELECT occurrence FROM event_exdates WHERE event_id = ? ORDER BY occurrence`, e.ID)
	if err != nil {
		return err
	}
	for _, s := range ex {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return err
		}
		e.ExDates = append(e.ExDates, t.In(e.Start.Location()))
	}
	a, err := db.audience("event_audience", "event_id", e.ID)
	e.People, e.Groups = a.People, a.Groups
	return err
}

func (db *DB) audience(table, key, id string) (calendar.Audience, error) {
	var a calendar.Audience
	var err error
	if a.People, err = db.strings(`SELECT person_id FROM `+table+` WHERE `+key+` = ? AND person_id IS NOT NULL ORDER BY person_id`, id); err != nil {
		return a, err
	}
	a.Groups, err = db.strings(`SELECT group_id FROM `+table+` WHERE `+key+` = ? AND group_id IS NOT NULL ORDER BY group_id`, id)
	return a, err
}

func (db *DB) strings(query string, args ...any) ([]string, error) {
	rows, err := db.sql.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetCalendarWaste marks a calendar as waste collection or unmarks it. Only
// read-only calendars can be marked (ADR 0015).
func (db *DB) SetCalendarWaste(householdID, calendarID string, waste bool) error {
	c, err := db.Calendar(householdID, calendarID)
	if err != nil {
		return err
	}
	if !c.ReadOnly {
		return fmt.Errorf("%w: only a read-only calendar can be a waste collection calendar", ErrInvalid)
	}
	return expectOne(db.sql.Exec(`UPDATE calendars SET waste = ?, updated_at = ? WHERE id = ? AND household_id = ?`, waste, now(), calendarID, householdID))
}

// DayNote is a parent's free-text note for a day.
type DayNote struct {
	Day  string // YYYY-MM-DD
	Text string
}

// SetDayNote sets the note of a day (YYYY-MM-DD); empty text deletes it.
func (db *DB) SetDayNote(householdID, day, text string) error {
	if _, err := time.Parse(dateLayout, day); err != nil {
		return fmt.Errorf("%w: day must be YYYY-MM-DD", ErrInvalid)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		_, err := db.sql.Exec(`DELETE FROM day_notes WHERE household_id = ? AND day = ?`, householdID, day)
		return err
	}
	_, err := db.sql.Exec(`INSERT INTO day_notes (household_id, day, text) VALUES (?, ?, ?)
		ON CONFLICT(household_id, day) DO UPDATE SET text = excluded.text`, householdID, day, text)
	return err
}

// DayNote returns the note of a day, if there is one.
func (db *DB) DayNote(householdID, day string) (string, bool, error) {
	var text string
	err := db.sql.QueryRow(`SELECT text FROM day_notes WHERE household_id = ? AND day = ?`, householdID, day).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return text, err == nil, err
}

// DayNotes lists the household's notes by day.
func (db *DB) DayNotes(householdID string) ([]DayNote, error) {
	rows, err := db.sql.Query(`SELECT day, text FROM day_notes WHERE household_id = ? ORDER BY day`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DayNote
	for rows.Next() {
		var n DayNote
		if err := rows.Scan(&n.Day, &n.Text); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// PruneDayNotes deletes the notes of days before the given day (ADR 0015: a
// note is deleted once its day is over).
func (db *DB) PruneDayNotes(householdID, before string) error {
	_, err := db.sql.Exec(`DELETE FROM day_notes WHERE household_id = ? AND day < ?`, householdID, before)
	return err
}
