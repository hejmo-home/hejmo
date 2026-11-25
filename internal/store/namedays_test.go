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

// Ticket 318: name days in the generated calendar Birthdays.

func nameDayContact(t *testing.T, db *store.DB, h store.Household, hejmo, full string, mod ...func(*store.ContactInput)) store.Contact {
	t.Helper()
	in := store.ContactInput{HejmoName: hejmo, FullName: full, CalendarName: "Geburtstage"}
	for _, m := range mod {
		m(&in)
	}
	c, err := db.CreateContact(h.ID, "", in)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func setNameDays(t *testing.T, db *store.DB, h store.Household, on bool, list nameday.List) store.NameDayChange {
	t.Helper()
	ch, err := db.SetNameDays(h.ID, on, string(list), "Geburtstage", "Namenstag")
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

// bareHousehold is a household without people, so that only the contacts of a test have name days.
func bareHousehold(t *testing.T) (*store.DB, store.Household) {
	t.Helper()
	db, _ := openTemp(t)
	h, err := db.CreateHousehold(store.Household{Name: "Family"})
	if err != nil {
		t.Fatal(err)
	}
	return db, h
}

func TestNameDaysAreOffByDefault(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	got, err := db.Household(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.NameDaysOn || got.NameDayList != "" {
		t.Fatalf("name days = %v %q, want off with no church", got.NameDaysOn, got.NameDayList)
	}
	nameDayContact(t, db, h, "Mama", "Katharina Maier", func(in *store.ContactInput) { in.BdayDay, in.BdayMonth = 3, 5 })
	if got := strings.Join(birthdayDays(t, db, h, 2026), ","); got != "03.05. Mama" {
		t.Errorf("2026 = %s: only the birthday, no name day", got)
	}
}

func TestSwitchingOnWithoutAChurchIsAFieldError(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	_, err := db.SetNameDays(h.ID, true, "", "Geburtstage", "Namenstag")
	var fe *store.FieldError
	if !errors.As(err, &fe) || fe.Field != "name_day_list" {
		t.Fatalf("err = %v, want a field error at name_day_list", err)
	}
	_, err = db.SetNameDays(h.ID, true, "anglican", "Geburtstage", "Namenstag")
	if !errors.As(err, &fe) || fe.Field != "name_day_list" {
		t.Fatalf("unknown list: err = %v", err)
	}
	if got, _ := db.Household(h.ID); got.NameDaysOn {
		t.Error("the setting was saved although it was wrong")
	}
}

func TestSwitchingOnMakesTheNameDaysAndTheCalendar(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	if _, ok := birthdayCalendar(t, db, h); ok {
		t.Fatal("no calendar yet")
	}
	nameDayContact(t, db, h, "Mama", "Katharina Maier")
	nameDayContact(t, db, h, "Opa", "Thomas Maier", func(in *store.ContactInput) { in.BdayDay, in.BdayMonth = 1, 2 })
	nameDayContact(t, db, h, "Frau Yilmaz", "")
	setNameDays(t, db, h, true, nameday.Catholic)
	cal, ok := birthdayCalendar(t, db, h)
	if !ok || cal.Name != "Geburtstage" || !cal.ReadOnly {
		t.Fatalf("turning on makes the calendar: %+v %v", cal, ok)
	}
	got := strings.Join(birthdayDays(t, db, h, 2026), ",")
	if got != "01.02. Opa,03.07. Namenstag Opa (Thomas),25.11. Namenstag Mama (Katharina)" {
		t.Errorf("2026 = %s", got)
	}
	// Yearly and without an age, like every fixed day.
	if got := strings.Join(birthdayDays(t, db, h, 2033), ","); !strings.Contains(got, "25.11. Namenstag Mama (Katharina)") || strings.Contains(got, "(7") {
		t.Errorf("2033 = %s", got)
	}
	h2, _ := db.Household(h.ID)
	if !h2.NameDaysOn || h2.NameDayList != "catholic" {
		t.Errorf("household = %v %q", h2.NameDaysOn, h2.NameDayList)
	}
}

func TestANameDayIsAllDayWholeFamilyAndNamesItsContact(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	c := nameDayContact(t, db, h, "Mama", "Katharina Maier")
	setNameDays(t, db, h, true, nameday.Catholic)
	cal, _ := birthdayCalendar(t, db, h)
	events, err := db.CalendarEvents(h.ID, cal.ID)
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %v, %v", events, err)
	}
	e := events[0]
	if !e.AllDay || len(e.People) != 0 || len(e.Groups) != 0 || e.Birthday || e.NameDayContact != c.ID || e.RRule != "FREQ=YEARLY" || e.ID != nameday.EventID(c.ID, 0) {
		t.Errorf("event = %+v", e)
	}
}

func TestNameDayOfAnEasterMovedDayIsWrittenYearByYear(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	nameDayContact(t, db, h, "Onkel Giorgos", "Georg Papadopoulos")
	setNameDays(t, db, h, true, nameday.GreekOrthodox)
	year := time.Now().Year()
	// Every year in the window shows the day on its own date, moved when it is before Easter.
	for y := year; y <= year+3; y++ {
		easter := nameday.OrthodoxEaster(y)
		want := time.Date(y, 4, 23, 0, 0, 0, 0, time.UTC)
		if want.Before(easter) {
			want = easter.AddDate(0, 0, 1)
		}
		got := strings.Join(birthdayDays(t, db, h, y), ",")
		if got != want.Format("02.01.")+" Namenstag Onkel Giorgos (Georgios)" {
			t.Errorf("%d = %s, want %s", y, got, want.Format("02.01."))
		}
	}
}

func TestSwitchingOffRemovesTheNameDaysAndKeepsEverythingElse(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	c := nameDayContact(t, db, h, "Mama", "Katharina Maier", func(in *store.ContactInput) { in.BdayDay, in.BdayMonth = 3, 5 })
	other := nameDayContact(t, db, h, "Ben", "Ben Maier", func(in *store.ContactInput) { in.NameDayMode, in.NameDayEntry = nameday.ModeEntry, "benedikt-07-11" })
	setNameDays(t, db, h, true, nameday.Catholic)
	if got := birthdayDays(t, db, h, 2026); len(got) != 3 {
		t.Fatalf("on: %v", got)
	}
	setNameDays(t, db, h, false, nameday.Catholic)
	if got := strings.Join(birthdayDays(t, db, h, 2026), ","); got != "03.05. Mama" {
		t.Errorf("off: %s, want the birthday only", got)
	}
	got, _ := db.Household(h.ID)
	if got.NameDaysOn || got.NameDayList != "catholic" {
		t.Errorf("the church is kept while off: %v %q", got.NameDaysOn, got.NameDayList)
	}
	kept, _ := db.Contact(h.ID, other.ID)
	if kept.NameDayMode != nameday.ModeEntry || kept.NameDayEntry != "benedikt-07-11" {
		t.Errorf("switching off keeps the choices: %+v", kept)
	}
	_ = c
	// And on again with the same church: the choice counts again.
	setNameDays(t, db, h, true, nameday.Catholic)
	if got := strings.Join(birthdayDays(t, db, h, 2026), ","); !strings.Contains(got, "11.07. Namenstag Ben (Benedikt)") {
		t.Errorf("on again: %s", got)
	}
}

func TestChangingTheChurchDropsChoicesTheNewListDoesNotHave(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	ben := nameDayContact(t, db, h, "Ben", "Ben Maier", func(in *store.ContactInput) { in.NameDayMode, in.NameDayEntry = nameday.ModeEntry, "benedikt-07-11" })
	gerda := nameDayContact(t, db, h, "Oma Gerda", "Gerda Müller", func(in *store.ContactInput) { in.NameDayMode, in.NameDayName = nameday.ModeName, "Gertrud" })
	mama := nameDayContact(t, db, h, "Mama", "Katharina Maier", func(in *store.ContactInput) { in.NameDayMode, in.NameDayName = nameday.ModeName, "Katharina" })
	none := nameDayContact(t, db, h, "Papa", "Thomas Maier", func(in *store.ContactInput) { in.NameDayMode = nameday.ModeNone })
	ch := setNameDays(t, db, h, true, nameday.Catholic)
	if len(ch.Dropped) != 0 {
		t.Fatalf("first time: %v", ch.Dropped)
	}
	ch = setNameDays(t, db, h, true, nameday.GreekOrthodox)
	var names []string
	for _, d := range ch.Dropped {
		names = append(names, d.Name)
	}
	if strings.Join(names, ",") != "Ben,Oma Gerda" && strings.Join(names, ",") != "Oma Gerda,Ben" {
		t.Errorf("dropped = %v, want Ben and Oma Gerda (the Greek list has neither the day nor Gertrud)", names)
	}
	for _, id := range []string{ben.ID, gerda.ID} {
		c, _ := db.Contact(h.ID, id)
		if c.NameDayMode != nameday.ModeAuto || c.NameDayEntry != "" || c.NameDayName != "" {
			t.Errorf("%s is auto again: %+v", c.HejmoName, c)
		}
	}
	if c, _ := db.Contact(h.ID, mama.ID); c.NameDayMode != nameday.ModeName || c.NameDayName != "Katharina" {
		t.Errorf("Katharina is in the Greek list too: %+v", c)
	}
	if c, _ := db.Contact(h.ID, none.ID); c.NameDayMode != nameday.ModeNone {
		t.Errorf("no name day stays: %+v", c)
	}
	// The same church again drops nothing.
	if ch := setNameDays(t, db, h, true, nameday.GreekOrthodox); len(ch.Dropped) != 0 {
		t.Errorf("same list: %v", ch.Dropped)
	}
}

func TestChangingAContactChangesItsEvent(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	c := nameDayContact(t, db, h, "Opa", "Thomas Maier")
	setNameDays(t, db, h, true, nameday.Catholic)
	days := func() string { return strings.Join(birthdayDays(t, db, h, 2026), ",") }
	if got := days(); got != "03.07. Namenstag Opa (Thomas)" {
		t.Fatalf("start: %s", got)
	}
	update := func(mod func(*store.ContactInput)) {
		t.Helper()
		in := store.ContactInput{HejmoName: c.HejmoName, FullName: c.FullName, CalendarName: "Geburtstage"}
		mod(&in)
		if _, err := db.UpdateContact(h.ID, c.ID, "", in); err != nil {
			t.Fatal(err)
		}
	}
	update(func(in *store.ContactInput) { in.NameDayMode, in.NameDayEntry = nameday.ModeEntry, "thomas-01-28" })
	if got := days(); got != "28.01. Namenstag Opa (Thomas)" {
		t.Errorf("another day: %s", got)
	}
	update(func(in *store.ContactInput) { in.NameDayMode = nameday.ModeNone })
	if got := days(); got != "" {
		t.Errorf("none: %s", got)
	}
	update(func(in *store.ContactInput) { in.FullName, in.NameDayMode = "Sebastian Maier", nameday.ModeAuto })
	if got := days(); got != "20.01. Namenstag Opa (Sebastian)" {
		t.Errorf("the first given name of the full name counts again: %s", got)
	}
}

func TestAnUpdateWithoutAChoiceKeepsTheChoice(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	c := nameDayContact(t, db, h, "Ben", "Ben Maier", func(in *store.ContactInput) { in.NameDayMode, in.NameDayEntry = nameday.ModeEntry, "benedikt-07-11" })
	if _, err := db.UpdateContact(h.ID, c.ID, "", store.ContactInput{HejmoName: "Ben", FullName: "Ben Maier", Note: "x"}); err != nil {
		t.Fatal(err)
	}
	got, _ := db.Contact(h.ID, c.ID)
	if got.NameDayMode != nameday.ModeEntry || got.NameDayEntry != "benedikt-07-11" {
		t.Errorf("a save that says nothing about the name day keeps it (the contact import does that): %+v", got)
	}
}

func TestTheChoiceOfANameDayIsChecked(t *testing.T) {
	t.Parallel()
	db, h, ppl := exampleFamily(t)
	setNameDays(t, db, h, true, nameday.Catholic)
	var fe *store.FieldError
	_, err := db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Mia2", FullName: "Mia Maier", NameDayMode: nameday.ModeName, NameDayName: "Mariella"})
	if !errors.As(err, &fe) || fe.Field != "name_day_name" {
		t.Errorf("an unknown name: %v", err)
	}
	_, err = db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Mia3", NameDayMode: nameday.ModeName, NameDayName: ""})
	if !errors.As(err, &fe) || fe.Field != "name_day_name" {
		t.Errorf("an empty name: %v", err)
	}
	_, err = db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Mia4", NameDayMode: nameday.ModeName, NameDayName: strings.Repeat("a", 41)})
	if !errors.As(err, &fe) || fe.Field != "name_day_name" {
		t.Errorf("a long name: %v", err)
	}
	_, err = db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Mia5", NameDayMode: nameday.ModeEntry, NameDayEntry: "nope"})
	if !errors.As(err, &fe) || fe.Field != "name_day" {
		t.Errorf("an unknown day: %v", err)
	}
	_, err = db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Mia6", NameDayMode: "other"})
	if !errors.As(err, &fe) || fe.Field != "name_day" {
		t.Errorf("an unknown mode: %v", err)
	}
	c, err := db.CreateContact(h.ID, ppl["Papa"].ID, store.ContactInput{HejmoName: "Maria", NameDayMode: nameday.ModeName, NameDayName: "Sophie"})
	if err != nil || c.NameDayBy != ppl["Papa"].ID || c.NameDayByName != "Papa" || c.NameDayMode != nameday.ModeName {
		t.Errorf("a good choice names who chose: %+v %v", c, err)
	}
}

func TestDeletingAContactTakesItsNameDayAway(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	c := nameDayContact(t, db, h, "Opa", "Thomas Maier")
	setNameDays(t, db, h, true, nameday.Catholic)
	if err := db.DeleteContact(h.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	if got := birthdayDays(t, db, h, 2026); len(got) != 0 {
		t.Errorf("after delete: %v", got)
	}
}

func TestBirthdaysStillCarryTheirAgeWithNameDaysOn(t *testing.T) {
	t.Parallel()
	db, h := bareHousehold(t)
	nameDayContact(t, db, h, "Mia", "Mia Maier", func(in *store.ContactInput) { in.BdayDay, in.BdayMonth, in.BdayYear = 2, 9, 2019 })
	setNameDays(t, db, h, true, nameday.Catholic)
	from, to := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	events, _ := db.EventsBetween(h.ID, from, to)
	occs, _ := calendar.Expand(events, from, to)
	var titles []string
	for _, o := range occs {
		titles = append(titles, o.Title)
	}
	if got := strings.Join(titles, ","); got != "Mia (7)" {
		t.Errorf("titles = %s", got)
	}
}
