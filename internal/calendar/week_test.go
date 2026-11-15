package calendar_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
)

func TestStartOfWeekIsMondayMidnightLocal(t *testing.T) {
	for _, d := range []time.Time{at(2026, 11, 16, 0, 0), at(2026, 11, 18, 13, 30), at(2026, 11, 22, 23, 59)} {
		got := calendar.StartOfWeek(d)
		if !got.Equal(day(2026, 11, 16)) {
			t.Errorf("StartOfWeek(%v) = %v, want Monday 16/11/2026 00:00", d, got)
		}
	}
	// Across the end of daylight-saving time (25/10/2026) the week still starts at local midnight.
	if got := calendar.StartOfWeek(at(2026, 10, 28, 9, 0)); !got.Equal(day(2026, 10, 26)) {
		t.Errorf("StartOfWeek = %v, want 26/10/2026 00:00", got)
	}
}

func TestWeekDaysHoldTimedSingleDayEventsOnly(t *testing.T) {
	events := []calendar.Event{
		{ID: "a", Title: "Swimming", Start: at(2026, 11, 17, 16, 0), End: at(2026, 11, 17, 17, 0)},
		{ID: "b", Title: "Piano", Start: at(2026, 11, 17, 9, 0), End: at(2026, 11, 17, 10, 0)},
		{ID: "c", Title: "Hut weekend", Start: at(2026, 11, 21, 10, 0), End: at(2026, 11, 23, 15, 0)},
		{ID: "d", Title: "Birthday", AllDay: true, Start: day(2026, 11, 18), End: day(2026, 11, 19)},
		{ID: "e", Title: "Next week", Start: at(2026, 11, 24, 9, 0), End: at(2026, 11, 24, 10, 0)},
	}
	occs, _ := calendar.Expand(events, day(2026, 11, 16), day(2026, 11, 23))
	days := calendar.WeekDays(occs, day(2026, 11, 16))
	if got := titles(days[1]); len(got) != 2 || got[0] != "Piano" || got[1] != "Swimming" {
		t.Errorf("Tuesday = %v, want Piano, Swimming in time order", got)
	}
	for i, d := range days {
		if i != 1 && len(d) != 0 {
			t.Errorf("day %d = %v, want empty (bars and other weeks are not day entries)", i, titles(d))
		}
	}
}

func TestOccurrenceOfFindsGeneratedAndChangedOccurrences(t *testing.T) {
	master := calendar.Event{ID: "m", Title: "Football", Start: at(2026, 11, 2, 16, 0), End: at(2026, 11, 2, 17, 0), RRule: "FREQ=WEEKLY"}
	moved := calendar.Event{ID: "o", SeriesID: "m", RecurrenceID: at(2026, 11, 9, 16, 0), Title: "Football (hall)", Start: at(2026, 11, 9, 18, 0), End: at(2026, 11, 9, 19, 0)}
	events := []calendar.Event{master, moved}

	o, ok := calendar.OccurrenceOf(events, at(2026, 11, 16, 16, 0))
	if !ok || o.Title != "Football" || !o.Start.Equal(at(2026, 11, 16, 16, 0)) || !o.End.Equal(at(2026, 11, 16, 17, 0)) {
		t.Fatalf("generated = %+v %v", o, ok)
	}
	o, ok = calendar.OccurrenceOf(events, at(2026, 11, 9, 16, 0))
	if !ok || o.Title != "Football (hall)" || !o.Start.Equal(at(2026, 11, 9, 18, 0)) {
		t.Fatalf("changed = %+v %v", o, ok)
	}
	if _, ok := calendar.OccurrenceOf(events, at(2026, 11, 10, 16, 0)); ok {
		t.Error("a day outside the series is an occurrence")
	}
	single := []calendar.Event{{ID: "s", Title: "Dentist", Start: at(2026, 11, 18, 9, 0), End: at(2026, 11, 18, 10, 0)}}
	if o, ok := calendar.OccurrenceOf(single, at(2026, 11, 18, 9, 0)); !ok || o.Title != "Dentist" {
		t.Fatalf("single = %+v %v", o, ok)
	}
}

func TestRepeatPresetsBecomeRulesAndBack(t *testing.T) {
	until := at(2026, 12, 20, 23, 59)
	cases := []struct {
		repeat calendar.Repeat
		rule   string
	}{
		{calendar.RepeatNone, ""},
		{calendar.RepeatDaily, "FREQ=DAILY"},
		{calendar.RepeatWeekdays, "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"},
		{calendar.RepeatWeekly, "FREQ=WEEKLY"},
		{calendar.RepeatBiweekly, "FREQ=WEEKLY;INTERVAL=2"},
		{calendar.RepeatMonthly, "FREQ=MONTHLY"},
		{calendar.RepeatYearly, "FREQ=YEARLY"},
	}
	for _, c := range cases {
		got, err := calendar.RepeatRule(c.repeat, time.Time{})
		if err != nil || got != c.rule {
			t.Errorf("RepeatRule(%s) = %q, %v; want %q", c.repeat, got, err, c.rule)
		}
		if r, u := calendar.RepeatOf(c.rule); r != c.repeat || !u.IsZero() {
			t.Errorf("RepeatOf(%q) = %s, %v; want %s without end", c.rule, r, u, c.repeat)
		}
		if c.rule == "" {
			continue
		}
		withEnd, err := calendar.RepeatRule(c.repeat, until)
		if err != nil {
			t.Fatal(err)
		}
		if err := calendar.ValidateRRule(withEnd); err != nil {
			t.Errorf("%q is not a valid rule: %v", withEnd, err)
		}
		if r, u := calendar.RepeatOf(withEnd); r != c.repeat || !u.Equal(until) {
			t.Errorf("RepeatOf(%q) = %s, %v; want %s until %v", withEnd, r, u, c.repeat, until)
		}
	}
	if r, _ := calendar.RepeatOf("FREQ=WEEKLY;COUNT=5"); r != calendar.RepeatCustom {
		t.Errorf("a counted series is %s, want custom", r)
	}
	if r, _ := calendar.RepeatOf("FREQ=MONTHLY;BYMONTHDAY=-1"); r != calendar.RepeatCustom {
		t.Errorf("last day of month is %s, want custom", r)
	}
	if _, err := calendar.RepeatRule("sometimes", time.Time{}); err == nil {
		t.Error("an unknown preset is accepted")
	}
}
