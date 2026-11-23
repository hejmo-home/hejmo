package store_test

import (
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/birthday"
	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 302: the generated calendar Birthdays, written from the contacts.

func birthdayCalendar(t *testing.T, db *store.DB, h store.Household) (store.Calendar, bool) {
	t.Helper()
	cals, err := db.Calendars(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cals {
		if c.Kind == "birthdays" {
			return c, true
		}
	}
	return store.Calendar{}, false
}

// birthdayDays lists the birthdays of a year as "dd.mm. Title".
func birthdayDays(t *testing.T, db *store.DB, h store.Household, year int) []string {
	t.Helper()
	from, to := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(year+1, 1, 1, 0, 0, 0, 0, time.UTC)
	events, err := db.EventsBetween(h.ID, from, to)
	if err != nil {
		t.Fatal(err)
	}
	cal, _ := birthdayCalendar(t, db, h)
	var own []calendar.Event
	for _, e := range events {
		if e.CalendarID == cal.ID {
			own = append(own, e)
		}
	}
	occs, err := calendar.Expand(own, from, to)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, o := range occs {
		out = append(out, o.Start.Format("02.01. ")+o.Title)
	}
	return out
}

func TestNoBirthdayNoCalendar(t *testing.T) {
	t.Parallel()
	db, h, _ := exampleFamily(t)
	if _, err := db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Oma Gerda"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := birthdayCalendar(t, db, h); ok {
		t.Error("a calendar was made although nobody has a birthday")
	}
}

func TestABirthdayIsAnAllDayYearlyEventInTheGeneratedReadOnlyCalendar(t *testing.T) {
	t.Parallel()
	db, h, _ := exampleFamily(t)
	if _, err := db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Oma Gerda", BdayDay: 3, BdayMonth: 5, CalendarName: "Geburtstage"}); err != nil {
		t.Fatal(err)
	}
	cal, ok := birthdayCalendar(t, db, h)
	if !ok || cal.Name != "Geburtstage" || !cal.ReadOnly || !cal.Shared {
		t.Fatalf("calendar = %+v, %v; want Geburtstage, read-only and shared through the box", cal, ok)
	}
	if got := strings.Join(birthdayDays(t, db, h, 2026), ","); got != "03.05. Oma Gerda" {
		t.Errorf("2026 = %s", got)
	}
	if got := strings.Join(birthdayDays(t, db, h, 2031), ","); got != "03.05. Oma Gerda" {
		t.Errorf("2031 = %s (it repeats every year, without an age)", got)
	}
}

// Acceptance: 29 February repeats on the last day of February in other years.
func TestALeapDayBirthdayRepeatsOnTheLastDayOfFebruary(t *testing.T) {
	t.Parallel()
	db, h, _ := exampleFamily(t)
	if _, err := db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Mia B", BdayDay: 29, BdayMonth: 2, BdayYear: 2020}); err != nil {
		t.Fatal(err)
	}
	for year, want := range map[int]string{2024: "29.02. Mia B (4)", 2026: "28.02. Mia B (6)", 2028: "29.02. Mia B (8)"} {
		if got := strings.Join(birthdayDays(t, db, h, year), ","); got != want {
			t.Errorf("%d = %q, want %q", year, got, want)
		}
	}
	if _, err := db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Opa", BdayDay: 29, BdayMonth: 2}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(birthdayDays(t, db, h, 2026), ","); got != "28.02. Mia B (6),28.02. Opa" {
		t.Errorf("2026 = %q: a leap day without a year repeats on the 28th too, without an age", got)
	}
}

// Acceptance: age only when the year is known.
func TestTheAgeShowsOnlyWhenTheYearIsKnown(t *testing.T) {
	t.Parallel()
	db, h, _ := exampleFamily(t)
	db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Oma Gerda", BdayDay: 3, BdayMonth: 5})
	db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Tante Ines", BdayDay: 4, BdayMonth: 5, BdayYear: 1980})
	if got := strings.Join(birthdayDays(t, db, h, 2026), ","); got != "03.05. Oma Gerda,04.05. Tante Ines (46)" {
		t.Errorf("2026 = %q", got)
	}
}

func TestEveryContactCanBeSwitchedOutOfTheCalendar(t *testing.T) {
	t.Parallel()
	db, h, _ := exampleFamily(t)
	c, err := db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Oma Gerda", BdayDay: 3, BdayMonth: 5})
	if err != nil {
		t.Fatal(err)
	}
	if c.HideBirthday {
		t.Fatal("a birthday is in the calendar unless it is switched off")
	}
	in := store.ContactInput{HejmoName: "Oma Gerda", BdayDay: 3, BdayMonth: 5, HideBirthday: true}
	if c, err = db.UpdateContact(h.ID, c.ID, "", in); err != nil || !c.HideBirthday {
		t.Fatalf("switched off: %+v, %v", c, err)
	}
	if got := birthdayDays(t, db, h, 2026); len(got) != 0 {
		t.Errorf("switched off, but still in the calendar: %v", got)
	}
	in.HideBirthday = false
	if _, err = db.UpdateContact(h.ID, c.ID, "", in); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(birthdayDays(t, db, h, 2026), ","); got != "03.05. Oma Gerda" {
		t.Errorf("switched on again: %q", got)
	}
}

func TestTheCalendarFollowsTheContact(t *testing.T) {
	t.Parallel()
	db, h, f := exampleFamily(t)
	mia := f["Mia"]
	c, _ := db.Contact(h.ID, mia.ID)
	if _, err := db.UpdateContact(h.ID, c.ID, "", store.ContactInput{BdayDay: 12, BdayMonth: 5, BdayYear: 2019}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(birthdayDays(t, db, h, 2026), ","); got != "12.05. Mia (7)" {
		t.Fatalf("2026 = %q", got)
	}
	if err := db.UpdatePerson(h.ID, mia.ID, "Mia Maier", "", ""); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(birthdayDays(t, db, h, 2026), ","); got != "12.05. Mia Maier (7)" {
		t.Errorf("after the person was renamed: %q", got)
	}
	if _, err := db.UpdateContact(h.ID, c.ID, "", store.ContactInput{}); err != nil {
		t.Fatal(err)
	}
	if got := birthdayDays(t, db, h, 2026); len(got) != 0 {
		t.Errorf("birthday removed from the contact, still in the calendar: %v", got)
	}
	other, _ := db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Oma Gerda", BdayDay: 3, BdayMonth: 5})
	if err := db.DeleteContact(h.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if got := birthdayDays(t, db, h, 2026); len(got) != 0 {
		t.Errorf("contact deleted, still in the calendar: %v", got)
	}
}

// The API declares event ids as uuids and the age follows the event kind, not
// its id: the store gives the birthday series of a contact the id
// birthday.EventID and marks only the events of the calendar of kind birthdays.
func TestBirthdayEventsHaveAUUIDPerContactAndAreMarkedAsBirthdays(t *testing.T) {
	t.Parallel()
	db, h, _ := exampleFamily(t)
	gerda, err := db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Oma Gerda", BdayDay: 3, BdayMonth: 5, BdayYear: 1950})
	if err != nil {
		t.Fatal(err)
	}
	fam, err := db.FamilyCalendar(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC)
	if _, err := db.CreateEvent(h.ID, calendar.Event{CalendarID: fam.ID, Title: "Anniversary", AllDay: true, Start: day, End: day.AddDate(0, 0, 1), RRule: "FREQ=YEARLY"}); err != nil {
		t.Fatal(err)
	}
	events, err := db.EventsBetween(h.ID, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	cal, _ := birthdayCalendar(t, db, h)
	seen := map[string]bool{}
	for _, e := range events {
		switch e.CalendarID {
		case cal.ID:
			seen["birthday"] = true
			if e.ID != birthday.EventID(gerda.ID) || !e.Birthday {
				t.Errorf("birthday event = id %q marked %v, want id %q marked", e.ID, e.Birthday, birthday.EventID(gerda.ID))
			}
		case fam.ID:
			seen["family"] = true
			if e.Birthday {
				t.Error("an event of the family calendar is marked as a birthday")
			}
		}
	}
	if !seen["birthday"] || !seen["family"] {
		t.Fatalf("events = %+v", events)
	}
	if got := strings.Join(birthdayDays(t, db, h, 2026), ","); got != "03.05. Oma Gerda (76)" {
		t.Errorf("2026 = %s", got)
	}
}

// Contacts that exist before the calendar (the migration makes the calendar,
// the box writes the events) are in it after the next start.
func TestTheBoxWritesTheBirthdaysOfExistingContactsAtStart(t *testing.T) {
	t.Parallel()
	db, path := openTemp(t)
	h, err := db.CreateHousehold(store.Household{Name: "Family"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Oma Gerda", BdayDay: 3, BdayMonth: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`DELETE FROM events`); err != nil {
		t.Fatal(err)
	}
	if got := birthdayDays(t, db, h, 2026); len(got) != 0 {
		t.Fatalf("setup: events left: %v", got)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if got := strings.Join(birthdayDays(t, again, h, 2026), ","); got != "03.05. Oma Gerda" {
		t.Errorf("2026 after the start = %s", got)
	}
}
