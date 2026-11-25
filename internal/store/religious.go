package store

import (
	"crypto/sha1" //nolint:gosec // UUIDv5 is defined with SHA-1; no security use
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	rh "github.com/hejmo-home/hejmo/internal/religiousholiday"
)

// The religious holidays calendar (ticket 339): the household setting and the
// events written from it. The box writes the events from last year to nine
// years ahead, as far as the tables reach, and again when the choice, the
// region or the home location changes, and at every start.

// religiousCalendarColor is the colour of the generated calendar in the web interface's calendar views.
const religiousCalendarColor = "#7A5C8C"

// defaultReligiousCalendarName names the calendar when the caller gave no name.
const defaultReligiousCalendarName = "Religious holidays"

const (
	religiousYearsBack  = 1
	religiousYearsAhead = 9
)

// ErrNoReligion is the error at the choice of religions: the switch is on and none is chosen. It is a *FieldError of the field religions.
var ErrNoReligion error = &FieldError{Field: "religions", Msg: "religions: choose at least one religion"}

// ErrHinduNeedsLocation is the error at the choice of religions: Hinduism is chosen but the household has no home location,
// and the dates depend on sunrise and moon there. It is a *FieldError of the field religions.
var ErrHinduNeedsLocation error = &FieldError{Field: "religions", Msg: "religions: hindu needs the home location"}

// ReligiousTitler makes the title of an event of the calendar: the name of the holiday (key of the religion) in the
// language, or the line "<name> beginnt am Abend" for the eve.
type ReligiousTitler func(lang, religion, key string, eve bool) string

// SetReligiousTitler sets how events are titled. The server sets it from the locale files before the box syncs.
func (db *DB) SetReligiousTitler(f ReligiousTitler) { db.religiousTitle.Store(&f) }

func (db *DB) titleReligious(lang, religion, key string, eve bool) string {
	if f := db.religiousTitle.Load(); f != nil {
		return (*f)(lang, religion, key, eve)
	}
	if eve {
		return key + " (evening)"
	}
	return key
}

func splitReligions(list string) []rh.Religion {
	if list == "" {
		return nil
	}
	var out []rh.Religion
	for _, r := range strings.Split(list, ",") {
		out = append(out, rh.Religion(r))
	}
	return out
}

// cleanReligions removes doubles, refuses unknown religions and sorts by code.
func cleanReligions(in []rh.Religion) ([]rh.Religion, error) {
	var out []rh.Religion
	for _, r := range in {
		if !r.Valid() {
			return nil, &FieldError{Field: "religions", Msg: "religions: unknown religion " + string(r)}
		}
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// ReligiousInput is the choice of a parent. Empty calendar choices keep what is stored.
type ReligiousInput struct {
	On        bool
	Religions []rh.Religion
	Islamic   rh.IslamicCalendar
	Orthodox  rh.OrthodoxCalendar
}

// ReligiousChange is what SaveReligiousHolidays did besides saving the choice.
type ReligiousChange struct {
	// Count is the number of holidays in the calendar from today to the end of next year.
	Count int
	// Moved is the number of events that stand on another day than before.
	Moved int
}

// HasLocation says the household has a confirmed home location.
func (h Household) HasLocation() bool { return h.Latitude != 0 || h.Longitude != 0 }

// ReligiousOptions are the choices that change the dates, from the household.
func (h Household) ReligiousOptions() rh.Options {
	o := rh.Options{Islamic: h.IslamicCalendar, Orthodox: h.OrthodoxCalendar, Zone: time.UTC}
	if l, err := time.LoadLocation(h.Timezone); err == nil {
		o.Zone = l
	}
	if h.HasLocation() {
		o.Place = &rh.Place{Lat: h.Latitude, Lon: h.Longitude}
	}
	return o
}

// ValidateReligious checks the choice of SaveReligiousHolidays without writing anything, so a caller can
// refuse a request before it saves any other part of it.
func ValidateReligious(in ReligiousInput, hasLocation bool) error {
	rs, err := cleanReligions(in.Religions)
	if err != nil {
		return err
	}
	if in.Islamic != "" && !in.Islamic.Valid() {
		return &FieldError{Field: "islamic_calendar", Msg: "islamic_calendar: unknown calendar " + string(in.Islamic)}
	}
	if in.Orthodox != "" && !in.Orthodox.Valid() {
		return &FieldError{Field: "orthodox_calendar", Msg: "orthodox_calendar: unknown calendar " + string(in.Orthodox)}
	}
	if !in.On {
		return nil
	}
	if len(rs) == 0 {
		return ErrNoReligion
	}
	if slices.Contains(rs, rh.Hindu) && !hasLocation {
		return ErrHinduNeedsLocation
	}
	return nil
}

// SaveReligiousHolidays switches the calendar on or off and stores the choice.
// Switching on needs a religion and, for Hinduism, a home location (a *FieldError
// at religions; nothing is saved). Switching off keeps the religions and the
// calendar choices. The calendar is made when it is missing and every event is
// written again; an event that is right already is not touched.
func (db *DB) SaveReligiousHolidays(householdID string, in ReligiousInput, calendarName, lang string) (ReligiousChange, error) {
	var change ReligiousChange
	tx, err := db.sql.Begin()
	if err != nil {
		return change, err
	}
	defer tx.Rollback()
	var lat, lon float64
	var curIslamic, curOrthodox, curReligions string
	if err := tx.QueryRow(`SELECT COALESCE(latitude, 0), COALESCE(longitude, 0), islamic_calendar, orthodox_calendar, religious_holidays FROM households WHERE id = ?`, householdID).
		Scan(&lat, &lon, &curIslamic, &curOrthodox, &curReligions); errors.Is(err, sql.ErrNoRows) {
		return change, ErrNotFound
	} else if err != nil {
		return change, err
	}
	if err := ValidateReligious(in, lat != 0 || lon != 0); err != nil {
		return change, err
	}
	religions := curReligions
	if rs, _ := cleanReligions(in.Religions); len(rs) > 0 { // a choice given while off is kept for later; none given keeps the stored one
		list := make([]string, len(rs))
		for i, r := range rs {
			list[i] = string(r)
		}
		religions = strings.Join(list, ",")
	}
	islamic, orthodox := curIslamic, curOrthodox
	if in.Islamic != "" {
		islamic = string(in.Islamic)
	}
	if in.Orthodox != "" {
		orthodox = string(in.Orthodox)
	}
	if lang == "" {
		lang = "de"
	}
	if err := expectOne(tx.Exec(`UPDATE households SET religious_holidays_on = ?, religious_holidays = ?, islamic_calendar = ?, orthodox_calendar = ?,
		religious_holidays_lang = CASE WHEN ? THEN ? ELSE religious_holidays_lang END, updated_at = ? WHERE id = ?`,
		in.On, religions, islamic, orthodox, in.On, lang, now(), householdID)); err != nil {
		return change, err
	}
	if change, err = db.syncReligious(tx, householdID, calendarName); err != nil {
		return change, err
	}
	return change, tx.Commit()
}

// SyncAllReligious writes the generated calendar again for every household that has it switched on or still has its events. The
// box runs it at its start, so the window of years moves on.
func (db *DB) SyncAllReligious() error {
	ids, err := db.strings(`SELECT id FROM households WHERE religious_holidays_on = 1
		UNION SELECT DISTINCT household_id FROM events WHERE religious IS NOT NULL`)
	if err != nil {
		return err
	}
	for _, id := range ids {
		tx, err := db.sql.Begin()
		if err != nil {
			return err
		}
		if _, err := db.syncReligious(tx, id, ""); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// religiousNS is the UUID namespace of the event ids (the URL namespace of RFC 4122).
var religiousNS = [16]byte{0x6b, 0xa7, 0xb8, 0x11, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}

// religiousEventID is a UUIDv5 of household, religion, holiday, the year the holiday belongs to (the year of its
// own calendar when it has one) and whether it is the eve, so the same holiday has the same id on every start and
// moves its event when the date changes.
func religiousEventID(householdID string, h rh.Holiday, eve bool) string {
	year := h.Start.Year()
	if h.Own.Cal != "" {
		year = h.Own.Year
	}
	hash := sha1.New() //nolint:gosec // UUIDv5 is defined with SHA-1; no security use
	hash.Write(religiousNS[:])
	fmt.Fprintf(hash, "religious/%s/%s/%s/%d/%t", householdID, h.Religion, h.Key, year, eve)
	var b [16]byte
	copy(b[:], hash.Sum(nil))
	b[6] = b[6]&0x0f | 0x50
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

type religiousWant struct {
	religion, key, title string
	start, end           time.Time // all-day, end exclusive
	eve, vary            bool
}

// syncReligious makes the calendar say what the household chose.
func (db *DB) syncReligious(tx *sql.Tx, householdID, calendarName string) (ReligiousChange, error) {
	var change ReligiousChange
	var on bool
	var list, lang, region, tz string
	h := Household{}
	if err := tx.QueryRow(`SELECT religious_holidays_on, religious_holidays, islamic_calendar, orthodox_calendar, religious_holidays_lang, region, timezone,
		COALESCE(latitude, 0), COALESCE(longitude, 0) FROM households WHERE id = ?`, householdID).
		Scan(&on, &list, &h.IslamicCalendar, &h.OrthodoxCalendar, &lang, &region, &tz, &h.Latitude, &h.Longitude); err != nil {
		return change, err
	}
	h.Timezone = tz
	loc := time.UTC
	if l, err := time.LoadLocation(tz); err == nil {
		loc = l
	}
	wanted := map[string]religiousWant{}
	if on {
		today := db.today().In(loc)
		year := today.Year()
		start := date0(today)
		endOfNext := time.Date(year+1, 12, 31, 0, 0, 0, 0, time.UTC)
		for _, plan := range rh.PlanFor(splitReligions(list), h.ReligiousOptions(), region, year-religiousYearsBack, year+religiousYearsAhead) {
			for _, hol := range plan.Kept {
				rel := string(plan.Religion)
				s := time.Date(hol.Start.Year(), hol.Start.Month(), hol.Start.Day(), 0, 0, 0, 0, loc)
				e := time.Date(hol.End.Year(), hol.End.Month(), hol.End.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
				wanted[religiousEventID(householdID, hol, false)] = religiousWant{religion: rel, key: hol.Key, title: db.titleReligious(lang, rel, hol.Key, false), start: s, end: e, vary: hol.Vary}
				if hol.Eve {
					wanted[religiousEventID(householdID, hol, true)] = religiousWant{religion: rel, key: hol.Key, title: db.titleReligious(lang, rel, hol.Key, true), start: s.AddDate(0, 0, -1), end: s, eve: true}
				}
				if !hol.Start.Before(start) && !hol.Start.After(endOfNext) {
					change.Count++
				}
			}
		}
	}
	type have struct {
		title, start, end string
		vary              bool
	}
	haveEvents := map[string]have{}
	rows, err := tx.Query(`SELECT e.id, e.title, e.start_local, e.end_local, e.religious_vary FROM events e JOIN calendars c ON c.id = e.calendar_id
		WHERE c.household_id = ? AND c.kind = 'religious'`, householdID)
	if err != nil {
		return change, err
	}
	for rows.Next() {
		var id string
		var x have
		if err := rows.Scan(&id, &x.title, &x.start, &x.end, &x.vary); err != nil {
			rows.Close()
			return change, err
		}
		haveEvents[id] = x
	}
	if err := rows.Close(); err != nil {
		return change, err
	}
	if len(wanted) == 0 && len(haveEvents) == 0 {
		return change, nil
	}
	for id := range haveEvents {
		if _, ok := wanted[id]; !ok {
			if _, err := tx.Exec(`DELETE FROM events WHERE id = ? AND household_id = ?`, id, householdID); err != nil {
				return change, err
			}
		}
	}
	if len(wanted) == 0 {
		return change, nil
	}
	calID, err := religiousCalendarID(tx, householdID, calendarName)
	if err != nil {
		return change, err
	}
	for id, w := range wanted {
		ev := calendar.Event{ID: id, CalendarID: calID, Title: w.title, AllDay: true, Start: w.start, End: w.end}
		old, exists := haveEvents[id]
		switch {
		case !exists:
			err = insertEventRow(tx, householdID, ev)
			if err == nil {
				_, err = tx.Exec(`UPDATE events SET religious = ?, religious_key = ?, religious_eve = ?, religious_vary = ? WHERE id = ?`, w.religion, w.key, w.eve, w.vary, id)
			}
		case old.title != w.title || old.start != w.start.Format(dateLayout) || old.end != w.end.Format(dateLayout) || old.vary != w.vary:
			if !w.eve && old.start != w.start.Format(dateLayout) {
				change.Moved++
			}
			err = updateEventRow(tx, householdID, ev)
			if err == nil {
				_, err = tx.Exec(`UPDATE events SET religious_vary = ? WHERE id = ?`, w.vary, id)
			}
		}
		if err != nil {
			return change, err
		}
	}
	return change, nil
}

func date0(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// religiousCalendarID returns the id of the household's religious holidays calendar, creating it (read-only, shared through the box) if the household has none.
func religiousCalendarID(tx *sql.Tx, householdID, name string) (string, error) {
	var id string
	err := tx.QueryRow(`SELECT id FROM calendars WHERE household_id = ? AND kind = 'religious' ORDER BY created_at, rowid LIMIT 1`, householdID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if name == "" {
		name = defaultReligiousCalendarName
	}
	id, ts := newID(), now()
	_, err = tx.Exec(`INSERT INTO calendars (id, household_id, name, kind, color, read_only, shared, created_at, updated_at)
		VALUES (?, ?, ?, 'religious', ?, 1, 1, ?, ?)`, id, householdID, name, religiousCalendarColor, ts, ts)
	return id, err
}
