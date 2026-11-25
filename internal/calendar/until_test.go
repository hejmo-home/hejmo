package calendar_test

import (
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
)

// Acceptance (#307): an all-day series ends with a date-only UNTIL naming its
// last day; a timed one keeps the UTC date-time form.
func TestRepeatRuleWritesUntilInTheValueTypeOfDtstart(t *testing.T) {
	until := time.Date(2026, 12, 31, 23, 59, 59, 0, berlin)
	got, err := calendar.RepeatRule(calendar.RepeatWeekly, until, true)
	if err != nil || got != "FREQ=WEEKLY;UNTIL=20261231" {
		t.Errorf("all-day = %q, %v; want FREQ=WEEKLY;UNTIL=20261231", got, err)
	}
	got, err = calendar.RepeatRule(calendar.RepeatWeekly, until, false)
	if err != nil || got != "FREQ=WEEKLY;UNTIL=20261231T225959Z" {
		t.Errorf("timed = %q, %v; want FREQ=WEEKLY;UNTIL=20261231T225959Z", got, err)
	}
}

// Acceptance (#307): reading back shows the same end date, and the last
// occurrence is the end day itself.
func TestDateOnlyUntilReadsBackAsTheLastDay(t *testing.T) {
	for _, zone := range []string{"Europe/Berlin", "America/New_York", "Pacific/Auckland"} {
		loc := mustLoc(zone)
		until := time.Date(2026, 12, 31, 23, 59, 59, 0, loc)
		rule, err := calendar.RepeatRule(calendar.RepeatDaily, until, true)
		if err != nil {
			t.Fatal(err)
		}
		r, u := calendar.RepeatOf(rule, loc)
		if r != calendar.RepeatDaily || u.In(loc).Format("2006-01-02") != "2026-12-31" {
			t.Errorf("%s: RepeatOf(%q) = %s, %v", zone, rule, r, u)
		}
		start := time.Date(2026, 12, 29, 0, 0, 0, 0, loc)
		ev := calendar.Event{ID: "s", Title: "Holiday", AllDay: true, Start: start, End: start.AddDate(0, 0, 1), RRule: rule}
		occs, err := calendar.Expand([]calendar.Event{ev}, start.AddDate(0, 0, -1), start.AddDate(0, 0, 10))
		if err != nil {
			t.Fatal(err)
		}
		if len(occs) != 3 || !occs[2].Start.Equal(time.Date(2026, 12, 31, 0, 0, 0, 0, loc)) {
			t.Errorf("%s: occurrences %v, want 29., 30. and 31.12.", zone, occs)
		}
	}
}

// Acceptance (#307): an existing all-day series with a date-time UNTIL still
// expands, and a series from outside with a date UNTIL works.
func TestAllDaySeriesWithEitherUntilFormExpands(t *testing.T) {
	for _, rule := range []string{"FREQ=DAILY;UNTIL=20261231T225959Z", "FREQ=DAILY;UNTIL=20261231"} {
		ev := calendar.Event{ID: "s", Title: "Holiday", AllDay: true, Start: day(2026, 12, 30), End: day(2026, 12, 31), RRule: rule}
		occs, err := calendar.Expand([]calendar.Event{ev}, day(2026, 12, 29), day(2027, 1, 10))
		if err != nil {
			t.Fatal(err)
		}
		equal(t, starts(occs), []string{"Wed 30.12. 00:00", "Thu 31.12. 00:00"})
	}
}

// Acceptance (#307): splitting an all-day series ends the old part with a
// date-only UNTIL on the day before the split.
func TestSplittingAnAllDaySeriesEndsTheOldPartWithADate(t *testing.T) {
	master := calendar.Event{ID: "m", Title: "Camp", AllDay: true, Start: day(2026, 11, 2), End: day(2026, 11, 3), RRule: "FREQ=WEEKLY"}
	ch, err := newEditor().Edit([]calendar.Event{master}, day(2026, 11, 16), calendar.ScopeFollowing, calendar.Patch{Title: str("Camp 2")})
	if err != nil {
		t.Fatal(err)
	}
	if got := ch.Update[0].RRule; got != "FREQ=WEEKLY;UNTIL=20261115" {
		t.Errorf("old part = %q, want FREQ=WEEKLY;UNTIL=20261115", got)
	}
	ch, err = newEditor().Delete([]calendar.Event{master}, day(2026, 11, 16), calendar.ScopeFollowing)
	if err != nil {
		t.Fatal(err)
	}
	if got := ch.Update[0].RRule; got != "FREQ=WEEKLY;UNTIL=20261115" {
		t.Errorf("after delete = %q, want FREQ=WEEKLY;UNTIL=20261115", got)
	}
}

func TestSplittingATimedSeriesKeepsTheUTCDateTime(t *testing.T) {
	master := calendar.Event{ID: "m", Title: "Choir", Start: at(2026, 11, 2, 19, 0), End: at(2026, 11, 2, 20, 0), RRule: "FREQ=WEEKLY"}
	ch, err := newEditor().Edit([]calendar.Event{master}, at(2026, 11, 16, 19, 0), calendar.ScopeFollowing, calendar.Patch{Title: str("Choir 2")})
	if err != nil {
		t.Fatal(err)
	}
	if got := ch.Update[0].RRule; !strings.HasSuffix(got, "UNTIL=20261116T175959Z") {
		t.Errorf("old part = %q, want a UTC date-time UNTIL", got)
	}
}

// Regression guards (#307): occurrences cancelled or moved with UTC
// identifiers keep working on both sides of the daylight saving changes.
func TestExdateAndRecurrenceIDInUTCAcrossDaylightSaving(t *testing.T) {
	// Berlin: summer time starts 29/03/2026 and ends 25/10/2026.
	master := calendar.Event{ID: "m", Title: "Choir", Start: at(2026, 3, 2, 9, 0), End: at(2026, 3, 2, 10, 0), RRule: "FREQ=WEEKLY;UNTIL=20261231T225959Z",
		ExDates: []time.Time{
			time.Date(2026, 4, 6, 7, 0, 0, 0, time.UTC),  // 09:00 CEST
			time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC), // 09:00 CET
		}}
	moved := calendar.Event{ID: "o1", Title: "Choir (moved)", SeriesID: "m", Start: at(2026, 4, 14, 11, 0), End: at(2026, 4, 14, 12, 0),
		RecurrenceID: time.Date(2026, 4, 13, 7, 0, 0, 0, time.UTC)}
	moved2 := calendar.Event{ID: "o2", Title: "Choir (moved)", SeriesID: "m", Start: at(2026, 11, 10, 11, 0), End: at(2026, 11, 10, 12, 0),
		RecurrenceID: time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)}
	occs, err := calendar.Expand([]calendar.Event{master, moved, moved2}, day(2026, 4, 1), day(2026, 11, 20))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, o := range occs {
		got[o.Start.In(berlin).Format("02.01. 15:04 ")+o.Title] = true
	}
	for _, absent := range []string{"06.04. 09:00 Choir", "02.11. 09:00 Choir", "13.04. 09:00 Choir", "09.11. 09:00 Choir"} {
		if got[absent] {
			t.Errorf("%s is still there", absent)
		}
	}
	for _, present := range []string{"14.04. 11:00 Choir (moved)", "10.11. 11:00 Choir (moved)", "20.04. 09:00 Choir", "26.10. 09:00 Choir", "16.11. 09:00 Choir"} {
		if !got[present] {
			t.Errorf("%s is missing in %v", present, got)
		}
	}
}
