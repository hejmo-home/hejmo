package store_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/nameday"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 338: the calendar style and the Serbian Slava.

func slavaEvents(t *testing.T, db *store.DB, h store.Household, year int) []calendar.Occurrence {
	t.Helper()
	from, to := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(year+1, 1, 1, 0, 0, 0, 0, time.UTC)
	events, err := db.EventsBetween(h.ID, from, to)
	if err != nil {
		t.Fatal(err)
	}
	var own []calendar.Event
	for _, e := range events {
		if e.Slava {
			own = append(own, e)
		}
	}
	occs, err := calendar.Expand(own, from, to)
	if err != nil {
		t.Fatal(err)
	}
	return occs
}

func TestOrthodoxCalendarDefaultsToRevisedJulianAndRejectsOthers(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	got, _ := db.Household(h.ID)
	if got.OrthodoxCalendar != nameday.RevisedJulian || got.SlavaOn || got.Slava != "" {
		t.Fatalf("defaults: %+v", got)
	}
	var fe *store.FieldError
	if err := db.SetOrthodoxCalendar(h.ID, "old", "Geburtstage"); !errors.As(err, &fe) || fe.Field != "orthodox_calendar" {
		t.Fatalf("err = %v", err)
	}
}

func TestChangingTheCalendarStyleWritesTheNameDaysAgain(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	nameDayContact(t, db, h, "Mama", "Katharina Maier")
	setNameDays(t, db, h, true, nameday.RussianOrthodox)
	if got := strings.Join(birthdayDays(t, db, h, 2026), ","); got != "24.11. Namenstag Mama (Jekaterina)" {
		t.Fatalf("revised: %s", got)
	}
	if err := db.SetOrthodoxCalendar(h.ID, nameday.Julian, "Geburtstage"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(birthdayDays(t, db, h, 2026), ","); got != "07.12. Namenstag Mama (Jekaterina)" {
		t.Fatalf("julian: %s", got)
	}
	// A list that is not Orthodox does not follow the style.
	setNameDays(t, db, h, true, nameday.Catholic)
	if got := strings.Join(birthdayDays(t, db, h, 2026), ","); !strings.HasPrefix(got, "25.11. Namenstag Mama") {
		t.Fatalf("catholic with julian set: %s", got)
	}
	// Switching the list back, the style that was set is still there.
	setNameDays(t, db, h, true, nameday.RussianOrthodox)
	if got := strings.Join(birthdayDays(t, db, h, 2026), ","); got != "07.12. Namenstag Mama (Jekaterina)" {
		t.Fatalf("back to russian: %s", got)
	}
}

func TestEasterNameDaysAreTheSameInBothStyles(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	nameDayContact(t, db, h, "Papa", "Thomas Maier")
	setNameDays(t, db, h, true, nameday.RussianOrthodox)
	// The usual day of Thomas is the Sunday after Easter (Foma), the same on both styles.
	a := strings.Join(birthdayDays(t, db, h, 2027), ",")
	if err := db.SetOrthodoxCalendar(h.ID, nameday.Julian, "Geburtstage"); err != nil {
		t.Fatal(err)
	}
	b := strings.Join(birthdayDays(t, db, h, 2027), ",")
	if a != b || a != "09.05. Namenstag Papa (Foma)" {
		t.Fatalf("revised %s, julian %s", a, b)
	}
}

func TestSlavaValidation(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	cases := []struct {
		name  string
		in    store.SlavaInput
		field string
	}{
		{"on without a choice", store.SlavaInput{On: true}, "slava"},
		{"unknown saint", store.SlavaInput{On: true, Choice: "nope"}, "slava"},
		{"own without a name", store.SlavaInput{On: true, Choice: "own", OwnDay: "06-30"}, "slava_name"},
		{"own with a name over 60", store.SlavaInput{On: true, Choice: "own", OwnName: strings.Repeat("x", 61), OwnDay: "06-30"}, "slava_name"},
		{"own without a day", store.SlavaInput{On: true, Choice: "own", OwnName: "Sveti X"}, "slava_day"},
		{"own with an impossible day", store.SlavaInput{On: true, Choice: "own", OwnName: "Sveti X", OwnDay: "06-31"}, "slava_day"},
	}
	for _, tc := range cases {
		var fe *store.FieldError
		if err := db.SetSlava(h.ID, tc.in, "Geburtstage"); !errors.As(err, &fe) || fe.Field != tc.field {
			t.Errorf("%s: err = %v, want a field error at %s", tc.name, err, tc.field)
		}
		if err := store.ValidateSlava(tc.in); !errors.As(err, &fe) || fe.Field != tc.field {
			t.Errorf("%s: ValidateSlava = %v", tc.name, err)
		}
	}
	if got, _ := db.Household(h.ID); got.SlavaOn {
		t.Fatal("a wrong choice was saved")
	}
	if _, ok := birthdayCalendar(t, db, h); ok {
		t.Fatal("no calendar for nothing")
	}
}

func TestSlavaIsJulianRegardlessOfTheHouseholdStyle(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	if err := db.SetSlava(h.ID, store.SlavaInput{On: true, Choice: "nikola"}, "Geburtstage"); err != nil {
		t.Fatal(err)
	}
	for _, style := range []nameday.Style{nameday.RevisedJulian, nameday.Julian} {
		if err := db.SetOrthodoxCalendar(h.ID, style, "Geburtstage"); err != nil {
			t.Fatal(err)
		}
		occs := slavaEvents(t, db, h, 2026)
		if len(occs) != 1 || occs[0].Title != "Slava: Nikoljdan" || occs[0].Start.Format("02.01.") != "19.12." || !occs[0].AllDay {
			t.Fatalf("%s: %+v", style, occs)
		}
	}
	got, _ := db.Household(h.ID)
	if !got.SlavaOn || got.Slava != "nikola" {
		t.Fatalf("household: %+v", got)
	}
	cal, _ := birthdayCalendar(t, db, h)
	if !cal.ReadOnly {
		t.Error("the Slava lives in the read-only Birthdays calendar")
	}
	if len(birthdayDays(t, db, h, 2026)) != 1 {
		t.Error("only the Slava is in the calendar")
	}
}

func TestSlavaOnEasterIsWrittenYearByYear(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	if err := db.SetSlava(h.ID, store.SlavaInput{On: true, Choice: "spas"}, "Geburtstage"); err != nil {
		t.Fatal(err)
	}
	for y := time.Now().Year(); y <= time.Now().Year()+3; y++ {
		occs := slavaEvents(t, db, h, y)
		want := nameday.OrthodoxEaster(y).AddDate(0, 0, 39)
		if len(occs) != 1 || !occs[0].Start.Equal(time.Date(y, want.Month(), want.Day(), 0, 0, 0, 0, occs[0].Start.Location())) {
			t.Fatalf("%d: %+v, want %s", y, occs, want.Format("02.01."))
		}
	}
}

func TestOwnSlava(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	in := store.SlavaInput{On: true, Choice: "own", OwnName: "Sveti Vasilije Ostroški", OwnDay: "05-25"}
	if err := db.SetSlava(h.ID, in, "Geburtstage"); err != nil {
		t.Fatal(err)
	}
	occs := slavaEvents(t, db, h, 2026)
	if len(occs) != 1 || occs[0].Title != "Slava: Sveti Vasilije Ostroški" || occs[0].Start.Format("02.01.") != "25.05." {
		t.Fatalf("%+v", occs)
	}
	// The own day is a fixed day of our calendar, whatever the style.
	if err := db.SetOrthodoxCalendar(h.ID, nameday.Julian, "Geburtstage"); err != nil {
		t.Fatal(err)
	}
	if occs := slavaEvents(t, db, h, 2026); len(occs) != 1 || occs[0].Start.Format("02.01.") != "25.05." {
		t.Fatalf("julian: %+v", occs)
	}
}

func TestSwitchingTheSlavaOffRemovesTheEventAndKeepsTheChoice(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	if err := db.SetSlava(h.ID, store.SlavaInput{On: true, Choice: "nikola"}, "Geburtstage"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSlava(h.ID, store.SlavaInput{On: false}, "Geburtstage"); err != nil {
		t.Fatal(err)
	}
	if occs := slavaEvents(t, db, h, 2026); len(occs) != 0 {
		t.Fatalf("still there: %+v", occs)
	}
	got, _ := db.Household(h.ID)
	if got.SlavaOn || got.Slava != "nikola" {
		t.Fatalf("the choice is kept: %+v", got)
	}
	// On again without a new choice: the kept one is used.
	if err := db.SetSlava(h.ID, store.SlavaInput{On: true, Choice: "nikola"}, "Geburtstage"); err != nil {
		t.Fatal(err)
	}
	if len(slavaEvents(t, db, h, 2026)) != 1 {
		t.Fatal("back on")
	}
	// Switching the saint replaces the event.
	if err := db.SetSlava(h.ID, store.SlavaInput{On: true, Choice: "djordje"}, "Geburtstage"); err != nil {
		t.Fatal(err)
	}
	occs := slavaEvents(t, db, h, 2026)
	if len(occs) != 1 || occs[0].Title != "Slava: Đurđevdan" || occs[0].Start.Format("02.01.") != "06.05." {
		t.Fatalf("switched: %+v", occs)
	}
}

func TestSlavaAndNameDaysAreIndependent(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	nameDayContact(t, db, h, "Mama", "Katharina Maier")
	setNameDays(t, db, h, true, nameday.RomanianOrthodox)
	if err := db.SetSlava(h.ID, store.SlavaInput{On: true, Choice: "nikola"}, "Geburtstage"); err != nil {
		t.Fatal(err)
	}
	setNameDays(t, db, h, false, "")
	if occs := slavaEvents(t, db, h, 2026); len(occs) != 1 {
		t.Fatal("name days off must not remove the Slava")
	}
	if got := strings.Join(birthdayDays(t, db, h, 2026), ","); got != "19.12. Slava: Nikoljdan" {
		t.Fatalf("%s", got)
	}
}
