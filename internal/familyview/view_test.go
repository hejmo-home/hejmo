package familyview_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/wholine"
)

var berlin = func() *time.Location {
	l, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		panic(err)
	}
	return l
}()

func at(day, hour, min int) time.Time { return time.Date(2026, 11, day, hour, min, 0, 0, berlin) }

// family is the example family in display order.
func family() []familyview.Person {
	return []familyview.Person{
		{ID: "mama", Name: "Mama", Slot: 1, Shape: "circle-filled"},
		{ID: "papa", Name: "Papa", Slot: 2, Shape: "circle-empty"},
		{ID: "seb", Name: "Sebastian", Slot: 3, Shape: "square-filled"},
		{ID: "mia", Name: "Mia", Slot: 4, Shape: "square-empty"},
	}
}

func timed(title string, start, end time.Time, people ...string) calendar.Occurrence {
	return calendar.Occurrence{EventID: title, CalendarID: "cal", Title: title, Start: start, End: end, People: people}
}

// build lays out the view with the display's "show tomorrow block" setting on,
// so the 18:00 rule is what the tests exercise.
func build(now time.Time, occs ...calendar.Occurrence) familyview.View {
	return familyview.Build(familyview.Input{Now: now, Lang: "en", Region: "DE-BY", People: family(), Occurrences: occs, ShowTomorrow: true})
}

func column(t *testing.T, v familyview.View, name string) familyview.Column {
	t.Helper()
	for _, c := range v.Columns {
		if c.Person.Name == name {
			return c
		}
	}
	t.Fatalf("no column for %s", name)
	return familyview.Column{}
}

func titles(evs []familyview.Event) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Title)
	}
	return out
}

func same(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// 5.3: Given events for Mama and Mia, each appears in the right column in chronological order.
func TestEventsAppearInTheRightColumnInChronologicalOrder(t *testing.T) {
	v := build(at(14, 8, 0),
		timed("Mia swimming", at(14, 16, 0), at(14, 17, 0), "mia"),
		timed("Mama dentist", at(14, 9, 0), at(14, 10, 0), "mama"),
		timed("Mia school play", at(14, 10, 30), at(14, 11, 30), "mia"),
	)
	same(t, titles(column(t, v, "Mama").Events), []string{"Mama dentist"})
	same(t, titles(column(t, v, "Mia").Events), []string{"Mia school play", "Mia swimming"})
	same(t, titles(column(t, v, "Papa").Events), nil)
	if got := column(t, v, "Mama").Events[0].Time; got != "09:00–10:00" {
		t.Errorf("time = %q, want 24-hour range", got)
	}
}

// 5.3: Given an event with audience Sebastian and Mia, it appears in both their columns.
func TestEventWithTwoPeopleAppearsInBothColumns(t *testing.T) {
	v := build(at(14, 8, 0), timed("Football", at(14, 15, 0), at(14, 16, 0), "seb", "mia"))
	same(t, titles(column(t, v, "Sebastian").Events), []string{"Football"})
	same(t, titles(column(t, v, "Mia").Events), []string{"Football"})
	same(t, titles(column(t, v, "Mama").Events), nil)
	if len(v.Strip) != 0 {
		t.Errorf("strip = %v, want empty", v.Strip)
	}
}

// ADR 0011: the calendar's audience narrows an event's audience (both must include the person).
func TestCalendarAudienceNarrowsTheColumns(t *testing.T) {
	o := timed("Football", at(14, 15, 0), at(14, 16, 0), "seb", "mia")
	o.CalendarID = "kids"
	v := familyview.Build(familyview.Input{Now: at(14, 8, 0), Lang: "en", Region: "DE-BY", People: family(),
		Calendars:   map[string]calendar.Audience{"kids": {People: []string{"seb"}}},
		Occurrences: []calendar.Occurrence{o}})
	same(t, titles(column(t, v, "Sebastian").Events), []string{"Football"})
	same(t, titles(column(t, v, "Mia").Events), nil)
}

func TestSubgroupAudienceReachesItsMembers(t *testing.T) {
	o := calendar.Occurrence{EventID: "x", CalendarID: "cal", Title: "School", Start: at(14, 8, 0), End: at(14, 12, 0), Groups: []string{"school"}}
	ps := family()
	ps[2].Groups = []string{"school"}
	v := familyview.Build(familyview.Input{Now: at(14, 7, 0), Lang: "en", Region: "DE-BY", People: ps, Occurrences: []calendar.Occurrence{o}})
	same(t, titles(column(t, v, "Sebastian").Events), []string{"School"})
	same(t, titles(column(t, v, "Mia").Events), nil)
}

func TestOnlyTodaysEventsAreInTheColumns(t *testing.T) {
	v := build(at(14, 8, 0),
		timed("Yesterday", at(13, 9, 0), at(13, 10, 0), "mama"),
		timed("Today", at(14, 9, 0), at(14, 10, 0), "mama"),
		timed("Tomorrow", at(15, 9, 0), at(15, 10, 0), "mama"),
	)
	same(t, titles(column(t, v, "Mama").Events), []string{"Today"})
}

func stripTitles(v familyview.View) []string {
	var out []string
	for _, s := range v.Strip {
		out = append(out, s.Title)
	}
	return out
}

func allDay(title string, from, to time.Time, people ...string) calendar.Occurrence {
	return calendar.Occurrence{EventID: title, CalendarID: "cal", Title: title, AllDay: true, Start: from, End: to, People: people}
}

func day(d int) time.Time { return at(d, 0, 0) }

// ADR 0015: events without audience and all-day events appear once in the family strip, not in columns.
func TestWholeHouseholdAndAllDayEventsGoToTheStripOnly(t *testing.T) {
	v := build(at(14, 8, 0),
		timed("Family lunch", at(14, 12, 0), at(14, 13, 0)),
		allDay("Book fair", day(14), day(15), "seb"),
		allDay("Mama birthday", day(14), day(15)),
	)
	same(t, stripTitles(v), []string{"Book fair", "Mama birthday", "Family lunch"})
	for _, c := range v.Columns {
		if len(c.Events) != 0 {
			t.Errorf("%s column has %v, want none", c.Person.Name, titles(c.Events))
		}
	}
	// An all-day event with an audience says in brackets who it is for; the whole family needs none.
	if w := v.Strip[0].Who; w != "Sebastian" {
		t.Errorf("who-line = %q", w)
	}
	if v.Strip[2].Time != "12:00–13:00" || !v.Strip[1].AllDay || v.Strip[1].Who != "" {
		t.Errorf("strip = %+v", v.Strip)
	}
}

// 5.3: Given an event spanning three days, it appears on each day as part of one bar.
func TestThreeDayEventIsOneBarAcrossItsDays(t *testing.T) {
	trip := allDay("Grandparents visit", day(13), day(16), "mama", "papa")
	var got []string
	for _, d := range []int{12, 13, 14, 15, 16} {
		v := build(at(d, 8, 0), trip)
		if len(v.Strip) == 0 {
			got = append(got, fmt.Sprintf("%d:-", d))
			continue
		}
		s := v.Strip[0]
		got = append(got, fmt.Sprintf("%d:%d/%d<%t>%t", d, s.DayIndex, s.DayCount, s.ContinuesBefore, s.ContinuesAfter))
		if len(v.Columns[0].Events) != 0 {
			t.Errorf("multi-day event appears in a column")
		}
	}
	same(t, got, []string{"12:-", "13:1/3<false>true", "14:2/3<true>true", "15:3/3<true>false", "16:-"})
}

func TestTimedEventOverMidnightIsAMultiDayBar(t *testing.T) {
	v := build(at(14, 8, 0), timed("Sleepover", at(13, 19, 0), at(14, 10, 0), "mia"))
	same(t, stripTitles(v), []string{"Sleepover"})
	same(t, titles(column(t, v, "Mia").Events), nil)
}

// 5.3: Given a public holiday in Bavaria, it is shown; none from other states.
func TestHolidaysOfTheHomeRegionOnly(t *testing.T) {
	// 2026-11-01 Allerheiligen: Bavaria yes, Berlin no.
	by := familyview.Build(familyview.Input{Now: time.Date(2026, 11, 1, 8, 0, 0, 0, berlin), Lang: "en", Region: "DE-BY", People: family()})
	same(t, stripTitles(by), []string{"All Saints' Day"})
	if by.Strip[0].Kind != familyview.KindHoliday {
		t.Errorf("kind = %s", by.Strip[0].Kind)
	}
	be := familyview.Build(familyview.Input{Now: time.Date(2026, 11, 1, 8, 0, 0, 0, berlin), Lang: "en", Region: "DE-BE", People: family()})
	same(t, stripTitles(be), nil)
	de := familyview.Build(familyview.Input{Now: time.Date(2026, 11, 1, 8, 0, 0, 0, berlin), Lang: "de", Region: "DE-BY", People: family()})
	same(t, stripTitles(de), []string{"Allerheiligen"})
}

func TestStripListsHolidaysFirst(t *testing.T) {
	now := time.Date(2026, 11, 1, 8, 0, 0, 0, berlin)
	v := familyview.Build(familyview.Input{Now: now, Lang: "en", Region: "DE-BY", People: family(),
		Occurrences: []calendar.Occurrence{timed("Brunch", at(1, 10, 0), at(1, 11, 0))}})
	same(t, stripTitles(v), []string{"All Saints' Day", "Brunch"})
}

// ADR 0015: from 18:00 each column ends with the person's first three timed events of tomorrow.
func TestTomorrowBlockFrom1800(t *testing.T) {
	occs := []calendar.Occurrence{
		timed("Today", at(14, 9, 0), at(14, 10, 0), "mia"),
		timed("T4", at(15, 15, 0), at(15, 16, 0), "mia"),
		timed("T2", at(15, 9, 0), at(15, 10, 0), "mia"),
		timed("T1", at(15, 7, 30), at(15, 8, 0), "mia"),
		timed("T3", at(15, 12, 0), at(15, 13, 0), "mia"),
	}
	before := build(at(14, 17, 59), occs...)
	if before.ShowTomorrow || len(column(t, before, "Mia").Tomorrow) != 0 {
		t.Fatal("tomorrow block shown before 18:00")
	}
	v := build(at(14, 18, 0), occs...)
	if !v.ShowTomorrow {
		t.Fatal("tomorrow block not shown at 18:00")
	}
	same(t, titles(column(t, v, "Mia").Tomorrow), []string{"T1", "T2", "T3"})
	same(t, titles(column(t, v, "Mia").Events), []string{"Today"})
	same(t, titles(column(t, v, "Mama").Tomorrow), nil)
}

// ADR 0016: the tomorrow block is a per-display setting, off by default; with
// it off nothing of tomorrow is shown, even in the evening.
func TestTomorrowBlockNeedsTheDisplaySetting(t *testing.T) {
	occs := []calendar.Occurrence{
		timed("Piano", at(15, 15, 0), at(15, 16, 0), "mia"),
		allDay("Tomorrow bar", day(15), day(16)),
	}
	v := familyview.Build(familyview.Input{Now: at(14, 19, 0), Lang: "en", Region: "DE-BY", People: family(), Occurrences: occs})
	if v.ShowTomorrow || len(column(t, v, "Mia").Tomorrow) != 0 || len(v.StripTomorrow) != 0 {
		t.Fatalf("tomorrow shown with the setting off: %+v", v)
	}
	if on := build(at(14, 19, 0), occs...); !on.ShowTomorrow || len(column(t, on, "Mia").Tomorrow) != 1 {
		t.Fatal("tomorrow not shown with the setting on")
	}
}

// ADR 0015: whole-household events of tomorrow appear once at the end of the strip.
func TestTomorrowsWholeHouseholdEventsEndTheStrip(t *testing.T) {
	occs := []calendar.Occurrence{
		timed("Family brunch", at(15, 10, 0), at(15, 12, 0)),
		allDay("Today bar", day(14), day(15)),
		allDay("Tomorrow bar", day(15), day(16)),
		timed("Mia only", at(15, 9, 0), at(15, 10, 0), "mia"),
		allDay("Trip", day(14), day(17)), // already shown today as a bar
	}
	if v := build(at(14, 17, 0), occs...); len(v.StripTomorrow) != 0 {
		t.Fatalf("strip tomorrow before 18:00: %+v", v.StripTomorrow)
	}
	v := build(at(14, 19, 0), occs...)
	var got []string
	for _, s := range v.StripTomorrow {
		got = append(got, s.Title)
	}
	same(t, got, []string{"Tomorrow bar", "Family brunch"})
	same(t, stripTitles(v), []string{"Today bar", "Trip"})
}

func TestHeaderShowsWeekdayWeekDateAndClock(t *testing.T) {
	v := build(at(14, 7, 58))
	if v.Weekday != time.Saturday || v.Week != 46 || v.Clock != "07:58" || v.Date.Day() != 14 {
		t.Errorf("header = %v CW%d %v %s", v.Weekday, v.Week, v.Date, v.Clock)
	}
	if v.Weather != nil || v.Meal != nil || v.TomorrowTile != nil {
		t.Error("weather needs a reading and footer tiles are empty until package 6")
	}
}

func TestTimesAreShownInTheHouseholdZoneWhateverZoneTheEventIsStoredIn(t *testing.T) {
	utc := time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC) // 09:00 in Berlin
	auckland, err := time.LoadLocation("Pacific/Auckland")
	if err != nil {
		t.Fatal(err)
	}
	nz := time.Date(2026, 11, 14, 21, 0, 0, 0, auckland) // 09:00 in Berlin (UTC+13)
	occs := []calendar.Occurrence{
		timed("UTC", utc, utc.Add(time.Hour), "mia"),
		timed("NZ", nz, nz.Add(time.Hour), "mia"),
		timed("Family UTC", utc, utc.Add(time.Hour)),
		timed("Tomorrow UTC", utc.AddDate(0, 0, 1), utc.AddDate(0, 0, 1).Add(time.Hour), "mia"),
		timed("Family tomorrow UTC", utc.AddDate(0, 0, 1), utc.AddDate(0, 0, 1).Add(time.Hour)),
	}
	v := build(at(14, 18, 0), occs...)
	for _, e := range column(t, v, "Mia").Events {
		if e.Time != "09:00–10:00" {
			t.Errorf("%s column time = %q, want 09:00–10:00", e.Title, e.Time)
		}
	}
	if got := column(t, v, "Mia").Tomorrow; len(got) != 1 || got[0].Time != "09:00–10:00" {
		t.Errorf("tomorrow card = %+v", got)
	}
	for _, s := range append(append([]familyview.StripItem(nil), v.Strip...), v.StripTomorrow...) {
		if s.Time != "09:00–10:00" {
			t.Errorf("strip %q time = %q, want 09:00–10:00", s.Title, s.Time)
		}
	}
}

// ADR 0016: the clock and every event time follow the household's time format.
func TestTimesFollowTheTimeFormat(t *testing.T) {
	v := familyview.Build(familyview.Input{Now: at(14, 13, 5), Lang: "en", People: family(), TimeFormat: "12h",
		Occurrences: []calendar.Occurrence{timed("Dentist", at(14, 9, 0), at(14, 14, 30), "mama"), timed("Brunch", at(14, 10, 0), at(14, 11, 0))}})
	if v.Clock != "1:05 PM" {
		t.Errorf("clock = %q", v.Clock)
	}
	if got := column(t, v, "Mama").Events[0].Time; got != "9:00 AM–2:30 PM" {
		t.Errorf("event = %q", got)
	}
	if got := v.Strip[0].Time; got != "10:00 AM–11:00 AM" {
		t.Errorf("strip = %q", got)
	}
	v = familyview.Build(familyview.Input{Now: at(14, 13, 5), Lang: "en", People: family(), TimeFormat: "24h"})
	if v.Clock != "13:05" {
		t.Errorf("24h clock = %q", v.Clock)
	}
}

// ADR 0016: the weather chip shows the temperature in the household's unit.
func TestWeatherChipFollowsTheTemperatureUnit(t *testing.T) {
	w := &familyview.Weather{TempC: 18.4, Icon: "clear"}
	for unit, want := range map[store.TemperatureUnit]string{"": "18°C", store.Celsius: "18°C", store.Fahrenheit: "65°F"} {
		v := familyview.Build(familyview.Input{Now: at(14, 8, 0), People: family(), Weather: w, TemperatureUnit: unit})
		if v.Weather == nil || v.Weather.Text != want || v.Weather.Icon != "clear" {
			t.Errorf("unit %q: %+v, want %q", unit, v.Weather, want)
		}
	}
	for c, want := range map[float64]string{-0.4: "0°C", -2.6: "-3°C", 0: "32°F"} {
		unit := store.Celsius
		if want == "32°F" {
			unit = store.Fahrenheit
		}
		v := familyview.Build(familyview.Input{Now: at(14, 8, 0), Weather: &familyview.Weather{TempC: c}, TemperatureUnit: unit})
		if v.Weather.Text != want {
			t.Errorf("%v = %q, want %q", c, v.Weather.Text, want)
		}
	}
	if v := familyview.Build(familyview.Input{Now: at(14, 8, 0), People: family()}); v.Weather != nil {
		t.Error("weather chip without a reading")
	}
}

// 5.22: Given an event, then its card says in words who it is for; the
// column's own person is left out, and nobody is named on a card for one person.
func TestEventCardsSayInWordsWhoTheEventIsFor(t *testing.T) {
	v := build(at(14, 8, 0),
		timed("Football", at(14, 15, 0), at(14, 16, 0), "seb", "mia"),
		timed("Dentist", at(14, 9, 0), at(14, 10, 0), "mama"),
		timed("Parents' evening", at(14, 19, 0), at(14, 20, 0), "mama", "papa", "mia"),
		timed("Cinema", at(14, 17, 0), at(14, 18, 0), "mama", "papa", "seb", "mia"),
	)
	who := func(name, title string) string {
		t.Helper()
		for _, e := range column(t, v, name).Events {
			if e.Title == title {
				return e.Who
			}
		}
		t.Fatalf("%s has no %q", name, title)
		return ""
	}
	for _, c := range []struct{ column, title, want string }{
		{"Sebastian", "Football", "everyone except Mama and Papa"},
		{"Mia", "Football", "everyone except Mama and Papa"},
		{"Mama", "Dentist", ""},
		{"Mia", "Parents' evening", "everyone except Sebastian"},
		{"Papa", "Cinema", "Whole family"},
	} {
		if got := who(c.column, c.title); got != c.want {
			t.Errorf("%s column, %s: who-line = %q, want %q", c.column, c.title, got, c.want)
		}
	}
}

// 5.22: Given a subgroup and a big household, then the subgroup is named and
// the others are listed in full: no count, no ellipsis.
func TestWhoLineNamesTheSubgroupAndListsEveryoneElseInFull(t *testing.T) {
	var people []familyview.Person
	for i, n := range []string{"Mama", "Papa", "Sebastian", "Mia", "Jonas", "Lotta", "Ben", "Emma"} {
		people = append(people, familyview.Person{ID: n, Name: n, Slot: i + 1, Shape: "circle-filled"})
	}
	in := familyview.Input{Now: at(14, 8, 0), Lang: "de", People: people,
		Groups: []wholine.Group{{Name: "Kinder", Members: []string{"Sebastian", "Mia", "Jonas", "Lotta", "Ben", "Emma"}}},
		Occurrences: []calendar.Occurrence{
			timed("Schwimmen", at(14, 15, 0), at(14, 16, 0), "Sebastian", "Mia", "Jonas", "Lotta", "Ben", "Emma"),
			timed("Training", at(14, 17, 0), at(14, 18, 0), "Mia", "Jonas", "Ben", "Mama", "Papa"),
			allDay("Besuch bei Oma", day(14), day(15), "Mama", "Papa"),
			allDay("Ausflug", day(14), day(15), "Mia", "Jonas", "Ben", "Emma", "Lotta"),
			allDay("Familientag", day(14), day(15)),
		}}
	v := familyview.Build(in)
	if got := column(t, v, "Emma").Events[0].Who; got != "mit Kinder" {
		t.Errorf("subgroup: %q", got)
	}
	if got := column(t, v, "Mia").Events[1].Who; got != "mit Mama, Papa, Jonas, Ben" {
		t.Errorf("names: %q", got)
	}
	got := map[string]string{}
	for _, it := range v.Strip {
		got[it.Title] = it.Who
	}
	want := map[string]string{"Besuch bei Oma": "Mama, Papa", "Ausflug": "Mia, Jonas, Lotta, Ben, Emma", "Familientag": ""}
	for title, w := range want {
		if got[title] != w {
			t.Errorf("all-day row, %s: %q, want %q", title, got[title], w)
		}
	}
}
