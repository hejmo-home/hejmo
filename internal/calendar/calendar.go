// Package calendar holds the calendar's domain logic: series expansion,
// series editing, holiday-free layout of the week and visibility rules. It
// knows nothing about storage or HTTP.
package calendar

import (
	"fmt"
	"sort"
	"time"

	"github.com/teambition/rrule-go"

	"github.com/hejmo-home/hejmo/internal/birthday"
)

// Event is a single event, a series master, or a changed occurrence of a
// series (SeriesID and RecurrenceID set). Times carry their time zone.
type Event struct {
	ID          string
	CalendarID  string
	Title       string
	Description string
	Location    string
	AllDay      bool
	Start, End  time.Time // all-day: midnight local, End exclusive
	RRule       string    // RFC 5545 RRULE value without "RRULE:", "" = no series
	ExDates     []time.Time
	// For a changed occurrence: the series it belongs to and the original start.
	SeriesID     string
	RecurrenceID time.Time
	// Audience: people and groups; both empty = whole family.
	People []string
	Groups []string
	// UpdatedAt is when the box last stored the event. Zero for events not read from the store.
	UpdatedAt time.Time
	// Invitation is set for an event other people are invited to (Outlook):
	// the box shows it but never changes it, since a change would send a
	// message to every attendee.
	Invitation *Invitation
	// Birthday is set by the store for the events of the calendar of kind
	// birthdays: their occurrences carry the age (see Expand). It is not stored
	// on the event.
	Birthday bool
	// NameDayContact is the id of the contact a generated name-day event is made from (ticket 318); empty for
	// every other event. It is set by the store, not stored from the event.
	NameDayContact string
}

// Invitation says who invited and what the family's account answered, with a
// link to the event at its source.
type Invitation struct {
	Organizer string
	Response  string // Graph's answer: accepted, tentativelyAccepted, declined, notResponded, organizer
	WebLink   string
}

// Recurring reports whether e is a series master.
func (e Event) Recurring() bool { return e.RRule != "" && e.SeriesID == "" }

// Occurrence is one concrete appearance of an event in time.
type Occurrence struct {
	EventID      string // event that produced it (master or changed occurrence)
	SeriesID     string // master id when part of a series
	RecurrenceID time.Time
	CalendarID   string
	Title        string
	Description  string
	Location     string
	AllDay       bool
	Start, End   time.Time
	People       []string
	Groups       []string
	Invitation   *Invitation // set when other people are invited: the event changes only at its source
}

// ValidateRRule checks an RRULE value.
func ValidateRRule(s string) error {
	_, err := rrule.StrToROption(s)
	return err
}

// Expand returns all occurrences overlapping [from, to), sorted by start.
func Expand(events []Event, from, to time.Time) ([]Occurrence, error) {
	overridden := map[string]bool{}
	for _, e := range events {
		if e.SeriesID != "" {
			overridden[occKey(e.SeriesID, e.RecurrenceID)] = true
		}
	}
	var out []Occurrence
	for _, e := range events {
		if !e.Recurring() {
			if e.Start.Before(to) && e.End.After(from) {
				out = append(out, occurrence(e, e.Start, e.RecurrenceID, e.SeriesID))
			}
			continue
		}
		starts, err := seriesStarts(e, from.Add(-e.End.Sub(e.Start)), to)
		if err != nil {
			return nil, fmt.Errorf("calendar: event %s: %w", e.ID, err)
		}
		excluded := map[int64]bool{}
		for _, x := range e.ExDates {
			excluded[x.Unix()] = true
		}
		for _, s := range starts {
			if excluded[s.Unix()] || overridden[occKey(e.ID, s)] {
				continue
			}
			o := occurrence(e, s, s, e.ID)
			if e.Birthday && !withAge(&o, e.Start.Year()) {
				continue
			}
			if o.Start.Before(to) && o.End.After(from) {
				out = append(out, o)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].Title < out[j].Title
	})
	return out, nil
}

// withAge puts the age into the title of a birthday ("Mia (7)") when the year
// of birth is known. It reports false for the birth year itself: being born is
// no birthday.
func withAge(o *Occurrence, startYear int) bool {
	age, ok := birthday.Age(startYear, o.Start.Year())
	if !ok {
		return true
	}
	if age < 1 {
		return false
	}
	o.Title = fmt.Sprintf("%s (%d)", o.Title, age)
	return true
}

func seriesStarts(e Event, after, before time.Time) ([]time.Time, error) {
	opt, err := rrule.StrToROption(e.RRule)
	if err != nil {
		return nil, err
	}
	opt.Dtstart = e.Start
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return nil, err
	}
	loc := e.Start.Location()
	starts := r.Between(after.In(loc), before.In(loc), true)
	return starts, nil
}

func occurrence(e Event, start, recurrenceID time.Time, seriesID string) Occurrence {
	end := start.Add(e.End.Sub(e.Start))
	if e.AllDay {
		end = start.AddDate(0, 0, DaysBetween(e.Start, e.End))
	}
	return Occurrence{
		EventID: e.ID, SeriesID: seriesID, RecurrenceID: recurrenceID, CalendarID: e.CalendarID,
		Title: e.Title, Description: e.Description, Location: e.Location, AllDay: e.AllDay,
		Start: start, End: end, People: e.People, Groups: e.Groups, Invitation: e.Invitation,
	}
}

func occKey(seriesID string, t time.Time) string { return fmt.Sprintf("%s@%d", seriesID, t.Unix()) }

// DaysBetween counts calendar days from a to b (both midnight local).
func DaysBetween(a, b time.Time) int {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return int(time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC).Sub(time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)).Hours() / 24)
}
