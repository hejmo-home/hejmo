package store_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/store"
)

var berlin, _ = time.LoadLocation("Europe/Berlin")

func at(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, berlin)
}

type calFixture struct {
	db        *store.DB
	h         store.Household
	mama, mia store.Person
	cal       store.Calendar
}

func newCalFixture(t *testing.T) calFixture {
	t.Helper()
	db, _ := openTemp(t)
	h, mama := seedParent(t, db)
	mia, _ := db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	cal, err := db.FamilyCalendar(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	return calFixture{db, h, mama, mia, cal}
}

func TestFamilyCalendarIsCreatedOnce(t *testing.T) {
	f := newCalFixture(t)
	again, err := f.db.FamilyCalendar(f.h.ID)
	if err != nil || again.ID != f.cal.ID || again.Kind != "local" {
		t.Fatalf("FamilyCalendar = %+v, %v", again, err)
	}
}

func TestEventRoundTrip(t *testing.T) {
	f := newCalFixture(t)
	in := calendar.Event{
		CalendarID: f.cal.ID, Title: "Swimming", Location: "Westbad",
		Start: at(2026, 11, 2, 16, 0), End: at(2026, 11, 2, 17, 0),
		RRule: "FREQ=WEEKLY;BYDAY=MO", ExDates: []time.Time{at(2026, 11, 9, 16, 0)}, People: []string{f.mia.ID},
	}
	saved, err := f.db.CreateEvent(f.h.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	series, err := f.db.Series(f.h.ID, saved.ID)
	if err != nil || len(series) != 1 {
		t.Fatalf("Series = %+v, %v", series, err)
	}
	got := series[0]
	if got.Title != "Swimming" || got.Location != "Westbad" || !got.Start.Equal(in.Start) || got.Start.Location().String() != "Europe/Berlin" ||
		got.RRule != in.RRule || len(got.ExDates) != 1 || !got.ExDates[0].Equal(in.ExDates[0]) || len(got.People) != 1 || got.People[0] != f.mia.ID {
		t.Fatalf("round trip = %+v", got)
	}

	allDay, _ := f.db.CreateEvent(f.h.ID, calendar.Event{CalendarID: f.cal.ID, Title: "Trip", AllDay: true,
		Start: time.Date(2026, 11, 17, 0, 0, 0, 0, berlin), End: time.Date(2026, 11, 20, 0, 0, 0, 0, berlin)})
	s, _ := f.db.Series(f.h.ID, allDay.ID)
	if !s[0].AllDay || s[0].End.Sub(s[0].Start) != 72*time.Hour {
		t.Fatalf("all-day = %+v", s[0])
	}
}

func TestApplyChangeAndEventsBetween(t *testing.T) {
	f := newCalFixture(t)
	master, _ := f.db.CreateEvent(f.h.ID, calendar.Event{CalendarID: f.cal.ID, Title: "Football",
		Start: at(2026, 11, 2, 16, 0), End: at(2026, 11, 2, 17, 0), RRule: "FREQ=WEEKLY"})
	single, _ := f.db.CreateEvent(f.h.ID, calendar.Event{CalendarID: f.cal.ID, Title: "Dentist",
		Start: at(2026, 12, 3, 9, 0), End: at(2026, 12, 3, 10, 0)})

	series, _ := f.db.Series(f.h.ID, master.ID)
	title := "Football (new coach)"
	ch, err := calendar.Editor{NewID: store.NewID}.Edit(series, at(2026, 11, 16, 16, 0), calendar.ScopeFollowing, calendar.Patch{Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.ApplyChange(f.h.ID, ch); err != nil {
		t.Fatal(err)
	}

	events, err := f.db.EventsBetween(f.h.ID, at(2026, 11, 1, 0, 0), at(2026, 12, 1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	occs, _ := calendar.Expand(events, at(2026, 11, 1, 0, 0), at(2026, 12, 1, 0, 0))
	var titles []string
	for _, o := range occs {
		titles = append(titles, o.Title)
	}
	want := []string{"Football", "Football", "Football (new coach)", "Football (new coach)", "Football (new coach)"}
	if len(titles) != len(want) {
		t.Fatalf("titles = %v", titles)
	}
	for i := range want {
		if titles[i] != want[i] {
			t.Fatalf("titles = %v", titles)
		}
	}
	for _, e := range events {
		if e.ID == single.ID {
			t.Fatal("December event loaded for November")
		}
	}
	if err := f.db.ApplyChange(f.h.ID, calendar.Change{Delete: []string{single.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Series(f.h.ID, single.ID); err == nil {
		t.Fatal("deleted event still there")
	}
}

func TestEventsOfOtherHouseholdsAreInvisible(t *testing.T) {
	f := newCalFixture(t)
	e, _ := f.db.CreateEvent(f.h.ID, calendar.Event{CalendarID: f.cal.ID, Title: "Secret", Start: at(2026, 11, 2, 9, 0), End: at(2026, 11, 2, 10, 0)})
	other, _ := f.db.CreateHousehold(store.Household{Name: "Neighbours"})
	if _, err := f.db.Series(other.ID, e.ID); err == nil {
		t.Fatal("event visible to another household")
	}
}

func TestGroupsAndCalendarAudience(t *testing.T) {
	f := newCalFixture(t)
	g, err := f.db.CreateGroup(f.h.ID, "School children", []string{f.mia.ID})
	if err != nil {
		t.Fatal(err)
	}
	if groups, _ := f.db.PersonGroups(f.mia.ID); len(groups) != 1 || groups[0] != g.ID {
		t.Fatalf("PersonGroups = %v", groups)
	}
	school, err := f.db.CreateCalendar(store.Calendar{HouseholdID: f.h.ID, Name: "School holidays", Kind: "ics", Audience: calendar.Audience{Groups: []string{g.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	cals, _ := f.db.Calendars(f.h.ID)
	found := false
	for _, c := range cals {
		if c.ID == school.ID && len(c.Audience.Groups) == 1 && c.Audience.Groups[0] == g.ID {
			found = true
		}
	}
	if !found || len(cals) != 2 {
		t.Fatalf("Calendars = %+v", cals)
	}
	groups, _ := f.db.Groups(f.h.ID)
	if len(groups) != 1 || len(groups[0].Members) != 1 {
		t.Fatalf("Groups = %+v", groups)
	}
}

func TestSearchEvents(t *testing.T) {
	f := newCalFixture(t)
	for _, e := range []calendar.Event{
		{Title: "Schwimmkurs", Location: "Westbad"},
		{Title: "Zahnarzt Mia", Description: "Kontrolle"},
		{Title: "Elternabend", Location: "Grundschule Pasing"},
	} {
		e.CalendarID, e.Start, e.End = f.cal.ID, at(2026, 11, 2, 9, 0), at(2026, 11, 2, 10, 0)
		if _, err := f.db.CreateEvent(f.h.ID, e); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]string{"schwimm": "Schwimmkurs", "ZAHNARZT": "Zahnarzt Mia", "pasing": "Elternabend", "westbad": "Schwimmkurs", "kontrolle": "Zahnarzt Mia"}
	for q, want := range cases {
		got, err := f.db.SearchEvents(f.h.ID, q)
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		if len(got) != 1 || got[0].Title != want {
			t.Errorf("search %q = %v, want %s", q, got, want)
		}
	}
	for _, q := range []string{"", `"`, "nothing", "AND OR"} {
		if got, err := f.db.SearchEvents(f.h.ID, q); err != nil || len(got) != 0 {
			t.Errorf("search %q = %v, %v; want no results, no error", q, got, err)
		}
	}
}
