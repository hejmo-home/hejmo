package store

import (
	"database/sql"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/nameday"
)

// The Serbian Slava (ticket 338): one yearly all-day event of the whole family in the generated calendar Birthdays,
// made from the choice of the household. It is independent of name days and always follows the Serbian Orthodox
// Church (the Julian calendar), not the calendar style of the household.

// SlavaTitle is the title of the event: "Slava: Nikoljdan".
func SlavaTitle(name string) string { return "Slava: " + name }

// SlavaInput is what a household says about its Slava. Choice is the id of a saint of the list or nameday.OwnID;
// OwnName and OwnDay ("MM-DD" of our calendar) belong to an own Slava.
type SlavaInput struct {
	On                      bool
	Choice, OwnName, OwnDay string
}

// SlavaEntry is the Slava the household chose: an entry of the list or its own, while the choice is complete.
func (h Household) SlavaEntry() (nameday.Entry, bool) {
	switch h.Slava {
	case "":
		return nameday.Entry{}, false
	case nameday.OwnID:
		if h.SlavaOwnName == "" || h.SlavaOwnDay == "" {
			return nameday.Entry{}, false
		}
		return nameday.OwnEntry(h.SlavaOwnName, h.SlavaOwnDay), true
	}
	return nameday.Slavas().Entry(h.Slava)
}

// OwnSlavaDay splits "MM-DD" into month and day; false when it is not a day of the year.
func OwnSlavaDay(s string) (month, day int, ok bool) {
	m, d, found := strings.Cut(s, "-")
	mi, err1 := strconv.Atoi(m)
	di, err2 := strconv.Atoi(d)
	if !found || err1 != nil || err2 != nil {
		return 0, 0, false
	}
	if _, ok := nameday.OwnDay(mi, di); !ok {
		return 0, 0, false
	}
	return mi, di, true
}

// ValidateSlava checks the choice of SetSlava without writing anything, so a caller can refuse a request before it
// saves any other part of it. Switching off needs nothing. A *FieldError names the field: slava (nothing or an unknown
// saint chosen), slava_name (an own Slava without a name or with more than 60 characters) or slava_day.
func ValidateSlava(in SlavaInput) error {
	if !in.On && in.Choice == "" {
		return nil // switching off needs nothing; a saint given while off is checked like any other
	}
	switch in.Choice {
	case "":
		return &FieldError{Field: "slava", Msg: "slava: choose a saint"}
	case nameday.OwnID:
		name := strings.TrimSpace(in.OwnName)
		if name == "" || utf8.RuneCountInString(name) > nameday.MaxOwnSlavaName {
			return &FieldError{Field: "slava_name", Msg: "slava_name: a name of 1 to 60 characters"}
		}
		if _, _, ok := OwnSlavaDay(in.OwnDay); !ok {
			return &FieldError{Field: "slava_day", Msg: "slava_day: a day of the year as MM-DD"}
		}
	default:
		if _, ok := nameday.Slavas().Entry(in.Choice); !ok {
			return &FieldError{Field: "slava", Msg: "slava: unknown saint " + in.Choice}
		}
	}
	return nil
}

// SetSlava switches the Slava on or off and chooses the saint. Switching off without a choice keeps the saint, the own
// name and the own day; a choice given while off is saved for later; switching on needs a valid choice (*FieldError). The event is written, replaced when the saint changed,
// or removed.
func (db *DB) SetSlava(householdID string, in SlavaInput, calendarName string) error {
	if err := ValidateSlava(in); err != nil {
		return err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	switch {
	case in.Choice == "": // switching off: everything stays as it is
		err = expectOne(tx.Exec(`UPDATE households SET slava_on = 0, updated_at = ? WHERE id = ?`, now(), householdID))
	case in.Choice == nameday.OwnID:
		err = expectOne(tx.Exec(`UPDATE households SET slava_on = ?, slava = ?, slava_own_name = ?, slava_own_day = ?, updated_at = ? WHERE id = ?`,
			in.On, nameday.OwnID, strings.TrimSpace(in.OwnName), in.OwnDay, now(), householdID))
	default:
		err = expectOne(tx.Exec(`UPDATE households SET slava_on = ?, slava = ?, updated_at = ? WHERE id = ?`, in.On, in.Choice, now(), householdID))
	}
	if err != nil {
		return err
	}
	if err := syncBirthdays(tx, householdID, calendarName); err != nil {
		return err
	}
	return tx.Commit()
}

// slavaEvent is the event of the Slava, before it is written.
type slavaEvent struct {
	id, title, rrule string
	start            time.Time
}

// slavaEvents lists the events the Slava wants: none while it is off or incomplete; one yearly series for a fixed
// day, one event per year (from last year on) for a feast that follows Easter.
func slavaEvents(tx *sql.Tx, householdID string, loc *time.Location) ([]slavaEvent, error) {
	var on bool
	var saint, ownName, ownDay string
	if err := tx.QueryRow(`SELECT slava_on, COALESCE(slava, ''), slava_own_name, slava_own_day FROM households WHERE id = ?`, householdID).Scan(&on, &saint, &ownName, &ownDay); err != nil {
		return nil, err
	}
	if !on {
		return nil, nil
	}
	h := Household{Slava: saint, SlavaOwnName: ownName, SlavaOwnDay: ownDay}
	en, ok := h.SlavaEntry()
	if !ok {
		return nil, nil
	}
	title := SlavaTitle(en.Name)
	thisYear := time.Now().In(loc).Year()
	occ := func(y int) nameday.Occurrence {
		if saint == nameday.OwnID {
			return en.On(y) // a fixed day of our calendar
		}
		return en.SlavaOn(y)
	}
	if en.Yearly() {
		o := occ(thisYear)
		start := time.Date(2000, o.Date.Month(), o.Date.Day(), 0, 0, 0, 0, loc) // a leap year, so 29 February is possible
		return []slavaEvent{{id: nameday.SlavaEventID(householdID, 0), title: title, rrule: "FREQ=YEARLY", start: start}}, nil
	}
	var out []slavaEvent
	for y := thisYear - nameDayYearsBack; y <= thisYear+nameDayYearsAhead; y++ {
		d := occ(y).Date
		out = append(out, slavaEvent{id: nameday.SlavaEventID(householdID, y), title: title, start: time.Date(y, d.Month(), d.Day(), 0, 0, 0, 0, loc)})
	}
	return out, nil
}
