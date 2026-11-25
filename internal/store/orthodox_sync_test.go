package store_test

import (
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/nameday"
	rh "github.com/hejmo-home/hejmo/internal/religiousholiday"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Tickets 338 and 339 share one calendar style of the Orthodox church (households.orthodox_calendar): a change from
// either page writes both generated calendars again.

// orthodoxTheophany lists the days of the Theophany events (Christmas is a public holiday in the region, so it is not in the calendar) of the religious holidays calendar.
func orthodoxTheophany(t *testing.T, db *store.DB, h store.Household) string {
	t.Helper()
	var days []string
	for _, e := range religiousEvents(t, db, h) {
		if e.Religious == "orthodox" && e.ReligiousKey == "theophany" && !e.ReligiousEve {
			days = append(days, e.Start.Format("2006-01-02"))
		}
	}
	return strings.Join(days, ",")
}

// orthodoxHousehold has the Orthodox holidays and the Russian name days on, with one name day in November.
func orthodoxHousehold(t *testing.T) (*store.DB, store.Household) {
	t.Helper()
	db, h := religiousHousehold(t, "DE-BY", 0, 0)
	nameDayContact(t, db, h, "Mama", "Katharina Maier")
	setNameDays(t, db, h, true, nameday.RussianOrthodox)
	if _, err := db.SaveReligiousHolidays(h.ID, on(rh.Orthodox), "Religiöse Feiertage", "de"); err != nil {
		t.Fatal(err)
	}
	if got := orthodoxTheophany(t, db, h); !strings.Contains(got, "2027-01-06") || strings.Contains(got, "2027-01-19") {
		t.Fatalf("revised Theophany: %s", got)
	}
	if got := strings.Join(birthdayDays(t, db, h, 2026), ","); got != "24.11. Namenstag Mama (Jekaterina)" {
		t.Fatalf("revised name day: %s", got)
	}
	return db, h
}

func TestTheStyleOfTheBirthdaysPageWritesTheReligiousHolidaysAgain(t *testing.T) {
	t.Parallel()
	db, h := orthodoxHousehold(t)
	julian := nameday.Julian
	if _, err := db.SetBirthdays(h.ID, store.BirthdaySettings{Style: &julian}, "Geburtstage"); err != nil {
		t.Fatal(err)
	}
	if got := orthodoxTheophany(t, db, h); !strings.Contains(got, "2027-01-19") || strings.Contains(got, "2027-01-06") {
		t.Fatalf("Theophany after the Birthdays page chose julian: %s", got)
	}
	if err := db.SetOrthodoxCalendar(h.ID, nameday.RevisedJulian, "Geburtstage"); err != nil {
		t.Fatal(err)
	}
	if got := orthodoxTheophany(t, db, h); !strings.Contains(got, "2027-01-06") || strings.Contains(got, "2027-01-19") {
		t.Fatalf("Theophany after SetOrthodoxCalendar chose revised_julian: %s", got)
	}
}

func TestTheStyleOfTheReligiousHolidaysWritesTheNameDaysAgain(t *testing.T) {
	t.Parallel()
	db, h := orthodoxHousehold(t)
	if _, err := db.SaveReligiousHolidays(h.ID, store.ReligiousInput{On: true, Religions: []rh.Religion{rh.Orthodox}, Orthodox: rh.Julian}, "Religiöse Feiertage", "de"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(birthdayDays(t, db, h, 2026), ","); got != "07.12. Namenstag Mama (Jekaterina)" {
		t.Fatalf("name day after the religious holidays chose julian: %s", got)
	}
	// Back, and with the religious holidays off: the choice still moves the name days (the settings API sends it alone).
	if _, err := db.SaveReligiousHolidays(h.ID, store.ReligiousInput{On: false, Orthodox: rh.RevisedJulian}, "Religiöse Feiertage", "de"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(birthdayDays(t, db, h, 2026), ","); got != "24.11. Namenstag Mama (Jekaterina)" {
		t.Fatalf("name day after revised_julian with the holidays off: %s", got)
	}
	if _, err := db.SaveReligiousHolidays(h.ID, store.ReligiousInput{On: false, Orthodox: rh.Julian}, "Religiöse Feiertage", "de"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(birthdayDays(t, db, h, 2026), ","); got != "07.12. Namenstag Mama (Jekaterina)" {
		t.Fatalf("name day after julian with the holidays off: %s", got)
	}
}

func TestAStyleSaveThatChangesNothingLeavesTheOtherCalendarAlone(t *testing.T) {
	t.Parallel()
	db, h := orthodoxHousehold(t)
	before := len(birthdayDays(t, db, h, 2026))
	// Saving the religious holidays without a new style must not need the Birthdays calendar name or touch it.
	if _, err := db.SaveReligiousHolidays(h.ID, on(rh.Orthodox, rh.Jewish), "Religiöse Feiertage", "de"); err != nil {
		t.Fatal(err)
	}
	if got := len(birthdayDays(t, db, h, 2026)); got != before {
		t.Fatalf("birthday events %d, were %d", got, before)
	}
}
