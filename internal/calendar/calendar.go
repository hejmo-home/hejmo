// Package calendar holds the calendar's domain logic: series expansion,
// series editing, holiday-free layout of the week and visibility rules. It
// knows nothing about storage or HTTP.
package calendar

import (
	"fmt"
	"sort"
	"time"

	"github.com/teambition/rrule-go"
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
		end = start.AddDate(0, 0, daysBetween(e.Start, e.End))
	}
	return Occurrence{
		EventID: e.ID, SeriesID: seriesID, RecurrenceID: recurrenceID, CalendarID: e.CalendarID,
		Title: e.Title, Description: e.Description, Location: e.Location, AllDay: e.AllDay,
		Start: start, End: end, People: e.People, Groups: e.Groups,
	}
}

func occKey(seriesID string, t time.Time) string { return fmt.Sprintf("%s@%d", seriesID, t.Unix()) }

// daysBetween counts calendar days from a to b (both midnight local).
func daysBetween(a, b time.Time) int {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return int(time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC).Sub(time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)).Hours() / 24)
}
