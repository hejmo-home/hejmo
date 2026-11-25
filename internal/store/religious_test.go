package store_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	rh "github.com/hejmo-home/hejmo/internal/religiousholiday"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 339: the generated calendar Religious holidays.

func religiousHousehold(t *testing.T, region string, lat, lon float64) (*store.DB, store.Household) {
	t.Helper()
	db, _ := openTemp(t)
	db.SetReligiousTitler(func(lang, religion, key string, eve bool) string {
		if eve {
			return key + " beginnt am Abend"
		}
		return key
	})
	h, err := db.CreateHousehold(store.Household{Name: "Family", Region: region, Latitude: lat, Longitude: lon, HomeName: "Munich", Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatal(err)
	}
	return db, h
}

func religiousCalendar(t *testing.T, db *store.DB, h store.Household) (store.Calendar, bool) {
	t.Helper()
	cals, err := db.Calendars(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cals {
		if c.Kind == "religious" {
			return c, true
		}
	}
	return store.Calendar{}, false
}

// religiousEvents lists the events of the calendar, by start.
func religiousEvents(t *testing.T, db *store.DB, h store.Household) []calendar.Event {
	t.Helper()
	cal, ok := religiousCalendar(t, db, h)
	if !ok {
		return nil
	}
	all, err := db.EventsBetween(h.ID, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var out []calendar.Event
	for _, e := range all {
		if e.CalendarID == cal.ID {
			out = append(out, e)
		}
	}
	return out
}

func on(religions ...rh.Religion) store.ReligiousInput {
	return store.ReligiousInput{On: true, Religions: religions}
}

func TestReligiousHolidaysAreOffByDefaultWithoutACalendar(t *testing.T) {
	t.Parallel()
	db, h := religiousHousehold(t, "DE-BY", 0, 0)
	got, err := db.Household(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ReligiousHolidaysOn || len(got.Religions) != 0 || got.IslamicCalendar != rh.Diyanet || got.OrthodoxCalendar != rh.RevisedJulian {
		t.Errorf("defaults: %+v", got)
	}
	if _, ok := religiousCalendar(t, db, got); ok {
		t.Error("a calendar before anyone chose a religion")
	}
}

func TestSwitchingOnWritesAReadOnlyCalendarWithFestivalsAsOneEventAndTheEve(t *testing.T) {
	t.Parallel()
	db, h := religiousHousehold(t, "DE-BY", 0, 0)
	ch, err := db.SaveReligiousHolidays(h.ID, on(rh.Jewish), "Religiöse Feiertage", "de")
	if err != nil {
		t.Fatal(err)
	}
	cal, ok := religiousCalendar(t, db, h)
	if !ok || !cal.ReadOnly || !cal.Shared || cal.Name != "Religiöse Feiertage" {
		t.Fatalf("calendar %+v", cal)
	}
	year := time.Now().Year()
	want := rh.Compute(rh.Jewish, rh.Options{Zone: time.UTC}, year-1, year+9)
	var pessach rh.Holiday
	for _, w := range want {
		if w.Key == "pessach" && w.Start.Year() == year+1 {
			pessach = w
		}
	}
	var festival, eve *calendar.Event
	evs := religiousEvents(t, db, h)
	for i := range evs {
		e := evs[i]
		if e.Religious != "jewish" || e.ReligiousKey != "pessach" || e.Start.Year() != year+1 {
			continue
		}
		if e.ReligiousEve {
			eve = &evs[i]
		} else {
			festival = &evs[i]
		}
	}
	if festival == nil || eve == nil {
		t.Fatalf("Pessach %d: festival %v, eve %v among %d events", year+1, festival, eve, len(evs))
	}
	if !festival.AllDay || festival.Title != "pessach" || festival.Start.Format("2006-01-02") != pessach.Start.Format("2006-01-02") || festival.End.Format("2006-01-02") != pessach.End.AddDate(0, 0, 1).Format("2006-01-02") {
		t.Errorf("festival: %+v, want %v to %v", festival, pessach.Start, pessach.End)
	}
	if !eve.AllDay || eve.Title != "pessach beginnt am Abend" || eve.Start.Format("2006-01-02") != pessach.Start.AddDate(0, 0, -1).Format("2006-01-02") || eve.End.Sub(eve.Start) != 24*time.Hour {
		t.Errorf("eve: %+v", eve)
	}
	if festival.VarySighting || festival.Birthday {
		t.Errorf("flags: %+v", festival)
	}
	if ch.Count == 0 {
		t.Error("the change says no holiday stands in the calendar until the end of next year")
	}
	h2, _ := db.Household(h.ID)
	if !h2.ReligiousHolidaysOn || len(h2.Religions) != 1 || h2.Religions[0] != rh.Jewish {
		t.Errorf("household %+v", h2)
	}
}

func TestMoonSightingDaysCarryTheFlag(t *testing.T) {
	t.Parallel()
	db, h := religiousHousehold(t, "DE-BY", 0, 0)
	if _, err := db.SaveReligiousHolidays(h.ID, on(rh.Muslim, rh.Buddhist), "Religiöse Feiertage", "de"); err != nil {
		t.Fatal(err)
	}
	var vary, plain int
	for _, e := range religiousEvents(t, db, h) {
		switch {
		case e.Religious == "muslim" && !e.VarySighting:
			t.Errorf("%s: Islamic day without the flag", e.Title)
		case e.Religious == "muslim":
			vary++
		case e.VarySighting:
			t.Errorf("%s: flag on a day that does not depend on sighting", e.Title)
		default:
			plain++
		}
	}
	if vary == 0 || plain == 0 {
		t.Errorf("%d moon-sighting events and %d others", vary, plain)
	}
}

func TestSwitchingOnNeedsAReligionAndHinduismNeedsALocationAndSavesNothing(t *testing.T) {
	t.Parallel()
	db, h := religiousHousehold(t, "DE-BY", 0, 0)
	_, err := db.SaveReligiousHolidays(h.ID, on(), "Religiöse Feiertage", "de")
	var fe *store.FieldError
	if !errors.As(err, &fe) || fe.Field != "religions" || !errors.Is(err, store.ErrNoReligion) {
		t.Errorf("no religion: %v", err)
	}
	_, err = db.SaveReligiousHolidays(h.ID, on(rh.Muslim, rh.Hindu), "Religiöse Feiertage", "de")
	if !errors.As(err, &fe) || fe.Field != "religions" || !errors.Is(err, store.ErrHinduNeedsLocation) {
		t.Errorf("Hindu without a location: %v", err)
	}
	_, err = db.SaveReligiousHolidays(h.ID, on(rh.Religion("pastafarian")), "Religiöse Feiertage", "de")
	if !errors.As(err, &fe) || fe.Field != "religions" {
		t.Errorf("unknown religion: %v", err)
	}
	got, _ := db.Household(h.ID)
	if got.ReligiousHolidaysOn || len(got.Religions) != 0 {
		t.Errorf("something was saved: %+v", got)
	}
	if _, ok := religiousCalendar(t, db, got); ok {
		t.Error("a calendar was made")
	}
	// With a location the same choice works.
	db2, h2 := religiousHousehold(t, "DE-BY", 48.137, 11.575)
	if _, err := db2.SaveReligiousHolidays(h2.ID, on(rh.Hindu), "Religiöse Feiertage", "de"); err != nil {
		t.Errorf("Hindu with a location: %v", err)
	}
	var diwali int
	for _, e := range religiousEvents(t, db2, h2) {
		if e.ReligiousKey == "diwali" {
			diwali++
		}
	}
	if diwali == 0 {
		t.Error("no Diwali in the calendar")
	}
}

func TestSwitchingOffKeepsTheChoiceAndRemovesTheEvents(t *testing.T) {
	t.Parallel()
	db, h := religiousHousehold(t, "DE-BY", 0, 0)
	if _, err := db.SaveReligiousHolidays(h.ID, store.ReligiousInput{On: true, Religions: []rh.Religion{rh.Muslim, rh.Christian}, Islamic: rh.UmmAlQura, Orthodox: rh.Julian}, "Religiöse Feiertage", "de"); err != nil {
		t.Fatal(err)
	}
	if len(religiousEvents(t, db, h)) == 0 {
		t.Fatal("no events")
	}
	if _, err := db.SaveReligiousHolidays(h.ID, store.ReligiousInput{On: false}, "Religiöse Feiertage", "de"); err != nil {
		t.Fatal(err)
	}
	if n := len(religiousEvents(t, db, h)); n != 0 {
		t.Errorf("%d events after switching off", n)
	}
	got, _ := db.Household(h.ID)
	if got.ReligiousHolidaysOn || len(got.Religions) != 2 || got.IslamicCalendar != rh.UmmAlQura || got.OrthodoxCalendar != rh.Julian {
		t.Errorf("the choice was not kept: %+v", got)
	}
	// Switching on again with the stored choice (the page posts the ticks again).
	if _, err := db.SaveReligiousHolidays(h.ID, store.ReligiousInput{On: true, Religions: got.Religions, Islamic: got.IslamicCalendar, Orthodox: got.OrthodoxCalendar}, "Religiöse Feiertage", "de"); err != nil {
		t.Fatal(err)
	}
	if len(religiousEvents(t, db, h)) == 0 {
		t.Error("no events after switching on again")
	}
}

func TestPublicHolidaysOfTheRegionAreNotAddedTwice(t *testing.T) {
	t.Parallel()
	db, h := religiousHousehold(t, "DE-BY", 0, 0)
	if _, err := db.SaveReligiousHolidays(h.ID, on(rh.Christian), "Religiöse Feiertage", "de"); err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, e := range religiousEvents(t, db, h) {
		keys[e.ReligiousKey] = true
	}
	for _, k := range []string{"good_friday", "easter_monday", "ascension", "all_saints", "assumption", "christmas_days"} {
		if keys[k] {
			t.Errorf("%s is a public holiday in Bavaria and stands in the calendar twice", k)
		}
	}
	for _, k := range []string{"easter", "advent1", "christmas_eve"} {
		if !keys[k] {
			t.Errorf("%s is missing", k)
		}
	}
	// Berlin does not have All Saints' Day.
	db2, h2 := religiousHousehold(t, "DE-BE", 0, 0)
	if _, err := db2.SaveReligiousHolidays(h2.ID, on(rh.Christian), "Religiöse Feiertage", "de"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range religiousEvents(t, db2, h2) {
		found = found || e.ReligiousKey == "all_saints"
	}
	if !found {
		t.Error("All Saints' Day is missing in Berlin")
	}
}

func TestChangingTheIslamicCalendarMovesDatesAndSaysHowMany(t *testing.T) {
	t.Parallel()
	db, h := religiousHousehold(t, "DE-BY", 0, 0)
	if _, err := db.SaveReligiousHolidays(h.ID, on(rh.Muslim), "Religiöse Feiertage", "de"); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{}
	for _, e := range religiousEvents(t, db, h) {
		before[e.ID] = e.Start.Format("2006-01-02")
	}
	ch, err := db.SaveReligiousHolidays(h.ID, store.ReligiousInput{On: true, Religions: []rh.Religion{rh.Muslim}, Islamic: rh.UmmAlQura}, "Religiöse Feiertage", "de")
	if err != nil {
		t.Fatal(err)
	}
	moved := 0
	for _, e := range religiousEvents(t, db, h) {
		if old, ok := before[e.ID]; ok && old != e.Start.Format("2006-01-02") {
			moved++
		}
	}
	if moved == 0 || ch.Moved != moved {
		t.Errorf("%d events moved, the change says %d", moved, ch.Moved)
	}
}

func TestHomeLocationChangeRegeneratesHinduDays(t *testing.T) {
	t.Parallel()
	db, h := religiousHousehold(t, "DE-BY", 48.137, 11.575)
	if _, err := db.SaveReligiousHolidays(h.ID, on(rh.Hindu), "Religiöse Feiertage", "de"); err != nil {
		t.Fatal(err)
	}
	dates := func() string {
		var s []string
		for _, e := range religiousEvents(t, db, h) {
			s = append(s, e.ID+e.Start.Format("2006-01-02"))
		}
		return strings.Join(s, ",")
	}
	munich := dates()
	got, _ := db.Household(h.ID)
	if err := db.SaveHouseholdSettings(h.ID, store.HouseholdSettings{DateFormat: got.DateFormat, TimeFormat: got.TimeFormat, TemperatureUnit: got.TemperatureUnit, AutoUpdate: got.AutoUpdate, MealCutoffs: got.MealCutoffs,
		Home: &store.Home{Name: "Delhi", Latitude: 28.6139, Longitude: 77.2090}}); err != nil {
		t.Fatal(err)
	}
	if after := dates(); after == munich {
		t.Error("the Hindu days are the same for Delhi as for Munich: the calendar was not written again")
	}
}

func TestSyncAllReligiousLeavesRightEventsUntouched(t *testing.T) {
	t.Parallel()
	db, h := religiousHousehold(t, "DE-BY", 0, 0)
	if _, err := db.SaveReligiousHolidays(h.ID, on(rh.Jewish, rh.Orthodox), "Religiöse Feiertage", "de"); err != nil {
		t.Fatal(err)
	}
	stamp := func() string {
		var s string
		if err := db.SQL().QueryRow(`SELECT group_concat(updated_at) FROM events WHERE religious IS NOT NULL`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	n := len(religiousEvents(t, db, h))
	before := stamp()
	time.Sleep(1100 * time.Millisecond)
	if err := db.SyncAllReligious(); err != nil {
		t.Fatal(err)
	}
	if stamp() != before || len(religiousEvents(t, db, h)) != n {
		t.Error("the sync touched events that were right")
	}
}

func TestReligiousEventIDsAreValidAndStable(t *testing.T) {
	t.Parallel()
	db, h := religiousHousehold(t, "DE-BY", 0, 0)
	if _, err := db.SaveReligiousHolidays(h.ID, on(rh.Jewish), "Religiöse Feiertage", "de"); err != nil {
		t.Fatal(err)
	}
	first := map[string]bool{}
	for _, e := range religiousEvents(t, db, h) {
		if len(e.ID) != 36 || e.ID[14] != '5' {
			t.Errorf("not a UUIDv5: %s", e.ID)
		}
		first[e.ID] = true
	}
	if _, err := db.SaveReligiousHolidays(h.ID, on(rh.Jewish), "Religiöse Feiertage", "de"); err != nil {
		t.Fatal(err)
	}
	for _, e := range religiousEvents(t, db, h) {
		if !first[e.ID] {
			t.Errorf("event id %s changed on a second save", e.ID)
		}
	}
}
