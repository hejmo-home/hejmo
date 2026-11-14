package calendar_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
)

var berlin = mustLoc("Europe/Berlin")

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

func at(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, berlin)
}
func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, berlin) }

func starts(occs []calendar.Occurrence) []string {
	var out []string
	for _, o := range occs {
		out = append(out, o.Start.In(berlin).Format("Mon 02.01. 15:04"))
	}
	return out
}

func equal(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d occurrences %v, want %v", len(got), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("occurrence %d = %s, want %s (all: %v)", i, got[i], want[i], got)
		}
	}
}

func TestSingleEventsInRange(t *testing.T) {
	events := []calendar.Event{
		{ID: "in", Title: "Doctor", Start: at(2026, 11, 16, 9, 0), End: at(2026, 11, 16, 10, 0)},
		{ID: "before", Title: "Old", Start: at(2026, 11, 1, 9, 0), End: at(2026, 11, 1, 10, 0)},
		{ID: "overlap", Title: "Night", Start: at(2026, 11, 15, 23, 0), End: at(2026, 11, 16, 1, 0)},
	}
	occs, err := calendar.Expand(events, day(2026, 11, 16), day(2026, 11, 17))
	if err != nil {
		t.Fatal(err)
	}
	equal(t, starts(occs), []string{"Sun 15.11. 23:00", "Mon 16.11. 09:00"})
}

func TestWeeklySeriesKeepsLocalTimeAcrossDST(t *testing.T) {
	// Daylight saving time ends on Sunday 25.10.2026.
	swim := calendar.Event{ID: "swim", Title: "Swimming", Start: at(2026, 10, 12, 8, 0), End: at(2026, 10, 12, 9, 0), RRule: "FREQ=WEEKLY;BYDAY=MO,WE"}
	occs, err := calendar.Expand([]calendar.Event{swim}, day(2026, 10, 19), day(2026, 11, 2))
	if err != nil {
		t.Fatal(err)
	}
	equal(t, starts(occs), []string{"Mon 19.10. 08:00", "Wed 21.10. 08:00", "Mon 26.10. 08:00", "Wed 28.10. 08:00"})
	for _, o := range occs {
		if o.End.Sub(o.Start) != time.Hour || o.SeriesID != "swim" || o.Title != "Swimming" {
			t.Errorf("occurrence %+v", o)
		}
	}
}

func TestExdateCancelsOneOccurrence(t *testing.T) {
	ev := calendar.Event{ID: "s", Title: "Choir", Start: at(2026, 11, 2, 17, 0), End: at(2026, 11, 2, 18, 0),
		RRule: "FREQ=WEEKLY", ExDates: []time.Time{at(2026, 11, 9, 17, 0)}}
	occs, _ := calendar.Expand([]calendar.Event{ev}, day(2026, 11, 1), day(2026, 11, 20))
	equal(t, starts(occs), []string{"Mon 02.11. 17:00", "Mon 16.11. 17:00"})
}

func TestOverrideReplacesOccurrence(t *testing.T) {
	master := calendar.Event{ID: "m", Title: "Football", Start: at(2026, 11, 3, 16, 0), End: at(2026, 11, 3, 17, 30), RRule: "FREQ=WEEKLY;COUNT=3"}
	moved := calendar.Event{ID: "o", Title: "Football (away)", Start: at(2026, 11, 11, 15, 0), End: at(2026, 11, 11, 17, 0),
		SeriesID: "m", RecurrenceID: at(2026, 11, 10, 16, 0)}
	occs, err := calendar.Expand([]calendar.Event{master, moved}, day(2026, 11, 1), day(2026, 11, 30))
	if err != nil {
		t.Fatal(err)
	}
	equal(t, starts(occs), []string{"Tue 03.11. 16:00", "Wed 11.11. 15:00", "Tue 17.11. 16:00"})
	if occs[1].Title != "Football (away)" || occs[1].SeriesID != "m" || !occs[1].RecurrenceID.Equal(at(2026, 11, 10, 16, 0)) {
		t.Errorf("override occurrence = %+v", occs[1])
	}
}

func TestAllDayEventsAndOtherFrequencies(t *testing.T) {
	events := []calendar.Event{
		{ID: "trip", Title: "Grandma", AllDay: true, Start: day(2026, 11, 14), End: day(2026, 11, 17)},
		{ID: "bday", Title: "Mia's birthday", AllDay: true, Start: day(2019, 11, 16), End: day(2019, 11, 17), RRule: "FREQ=YEARLY"},
		{ID: "bin", Title: "Paper bin", AllDay: true, Start: day(2026, 1, 16), End: day(2026, 1, 17), RRule: "FREQ=MONTHLY;BYMONTHDAY=16"},
	}
	occs, err := calendar.Expand(events, day(2026, 11, 16), day(2026, 11, 17))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, o := range occs {
		got[o.EventID] = true
		if !o.AllDay {
			t.Errorf("%s lost all-day flag", o.EventID)
		}
	}
	if len(occs) != 3 || !got["trip"] || !got["bday"] || !got["bin"] {
		t.Fatalf("occurrences = %+v", occs)
	}
}

func TestInvalidRRule(t *testing.T) {
	ev := calendar.Event{ID: "x", Start: at(2026, 11, 2, 8, 0), End: at(2026, 11, 2, 9, 0), RRule: "FREQ=SOMETIMES"}
	if _, err := calendar.Expand([]calendar.Event{ev}, day(2026, 11, 1), day(2026, 12, 1)); err == nil {
		t.Fatal("invalid RRULE accepted")
	}
	if err := calendar.ValidateRRule("FREQ=WEEKLY;BYDAY=MO"); err != nil {
		t.Fatalf("valid RRULE rejected: %v", err)
	}
}
