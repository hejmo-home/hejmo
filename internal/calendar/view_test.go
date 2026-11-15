package calendar_test

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/calendar"
)

func titles(occs []calendar.Occurrence) []string {
	var out []string
	for _, o := range occs {
		out = append(out, o.Title)
	}
	return out
}

// Acceptance (Calendar and sync): Given an external calendar assigned to the
// subgroup "school children", when the nursery child's view is shown, then
// its events do not appear.
func TestCalendarAssignedToSubgroupIsHiddenFromOthers(t *testing.T) {
	occs := []calendar.Occurrence{
		{Title: "School holidays", CalendarID: "school"},
		{Title: "Family dinner", CalendarID: "family"},
		{Title: "Nursery party", CalendarID: "family", People: []string{"mia"}},
		{Title: "Parents' evening", CalendarID: "family", Groups: []string{"parents"}},
	}
	audiences := map[string]calendar.Audience{"school": {Groups: []string{"schoolkids"}}}

	sebastian := calendar.Viewer{PersonID: "sebastian", Groups: []string{"schoolkids"}}
	mia := calendar.Viewer{PersonID: "mia"}
	mama := calendar.Viewer{PersonID: "mama", Groups: []string{"parents"}}

	equal(t, titles(calendar.Visible(occs, audiences, mia)), []string{"Family dinner", "Nursery party"})
	equal(t, titles(calendar.Visible(occs, audiences, sebastian)), []string{"School holidays", "Family dinner"})
	equal(t, titles(calendar.Visible(occs, audiences, mama)), []string{"Family dinner", "Parents' evening"})
}

func TestCalendarAssignedToPerson(t *testing.T) {
	occs := []calendar.Occurrence{{Title: "Football club", CalendarID: "club"}}
	audiences := map[string]calendar.Audience{"club": {People: []string{"sebastian"}}}
	if got := calendar.Visible(occs, audiences, calendar.Viewer{PersonID: "sebastian"}); len(got) != 1 {
		t.Fatal("assigned person does not see the calendar")
	}
	if got := calendar.Visible(occs, audiences, calendar.Viewer{PersonID: "mia"}); len(got) != 0 {
		t.Fatal("other person sees the calendar")
	}
}

// Acceptance (Calendar and sync): Given an event over three days, when the
// week view is shown, then it appears as one continuous bar.
func TestThreeDayEventIsOneBar(t *testing.T) {
	trip, _ := calendar.Expand([]calendar.Event{{ID: "trip", Title: "Grandma", AllDay: true, Start: day(2026, 11, 17), End: day(2026, 11, 20)}},
		day(2026, 11, 16), day(2026, 11, 23))
	bars := calendar.WeekBars(trip, day(2026, 11, 16))
	if len(bars) != 1 {
		t.Fatalf("bars = %d, want 1", len(bars))
	}
	b := bars[0]
	if b.Col != 1 || b.Span != 3 || b.Lane != 0 || b.ContinuesBefore || b.ContinuesAfter {
		t.Fatalf("bar = col %d span %d lane %d before %v after %v, want Tue + 3 days", b.Col, b.Span, b.Lane, b.ContinuesBefore, b.ContinuesAfter)
	}
}

func TestBarsAreClippedToTheWeekAndStacked(t *testing.T) {
	events := []calendar.Event{
		{ID: "holiday", Title: "Autumn break", AllDay: true, Start: day(2026, 11, 12), End: day(2026, 11, 18)},
		{ID: "fair", Title: "Book fair", AllDay: true, Start: day(2026, 11, 16), End: day(2026, 11, 18)},
		{ID: "trip", Title: "Hut weekend", Start: at(2026, 11, 21, 10, 0), End: at(2026, 11, 23, 15, 0)},
		{ID: "short", Title: "Dentist", Start: at(2026, 11, 18, 9, 0), End: at(2026, 11, 18, 10, 0)},
	}
	occs, _ := calendar.Expand(events, day(2026, 11, 16), day(2026, 11, 23))
	bars := calendar.WeekBars(occs, day(2026, 11, 16))
	got := map[string]calendar.Bar{}
	for _, b := range bars {
		got[b.Title] = b
	}
	if _, ok := got["Dentist"]; ok || len(bars) != 3 {
		t.Fatalf("bars = %v, want 3 multi-day bars without the one-hour event", titlesOfBars(bars))
	}
	if b := got["Autumn break"]; b.Col != 0 || b.Span != 2 || !b.ContinuesBefore || b.ContinuesAfter {
		t.Errorf("Autumn break = %+v", b)
	}
	if b := got["Book fair"]; b.Col != 0 || b.Span != 2 || b.Lane == got["Autumn break"].Lane {
		t.Errorf("Book fair must share the days but sit in another lane: %+v", b)
	}
	if b := got["Hut weekend"]; b.Col != 5 || b.Span != 2 || !b.ContinuesAfter || b.Lane != 0 {
		t.Errorf("Hut weekend = %+v", b)
	}
}

func titlesOfBars(bs []calendar.Bar) []string {
	var out []string
	for _, b := range bs {
		out = append(out, b.Title)
	}
	return out
}

// 5.22: the people an occurrence is for are those whose view shows it.
func TestViewersOfAnOccurrence(t *testing.T) {
	viewers := []calendar.Viewer{
		{PersonID: "mama", Groups: []string{"parents"}},
		{PersonID: "papa", Groups: []string{"parents"}},
		{PersonID: "sebastian", Groups: []string{"schoolkids"}},
		{PersonID: "mia"},
	}
	audiences := map[string]calendar.Audience{"school": {Groups: []string{"schoolkids"}}}
	for _, c := range []struct {
		name string
		o    calendar.Occurrence
		want []string
	}{
		{"no audience: everyone", calendar.Occurrence{CalendarID: "family"}, []string{"mama", "papa", "sebastian", "mia"}},
		{"event people", calendar.Occurrence{CalendarID: "family", People: []string{"mia", "papa"}}, []string{"papa", "mia"}},
		{"event group", calendar.Occurrence{CalendarID: "family", Groups: []string{"parents"}}, []string{"mama", "papa"}},
		{"calendar audience", calendar.Occurrence{CalendarID: "school"}, []string{"sebastian"}},
		{"calendar and event audience", calendar.Occurrence{CalendarID: "school", People: []string{"mia", "sebastian"}}, []string{"sebastian"}},
	} {
		equal(t, calendar.ViewersOf(c.o, audiences, viewers), c.want)
	}
}
