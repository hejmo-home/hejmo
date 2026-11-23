package calendar_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/birthday"
	"github.com/hejmo-home/hejmo/internal/calendar"
)

// birthdayEvent is the event the box writes for a contact: an all-day yearly series.
func birthdayEvent(contactID, name string, d, m, y int) calendar.Event {
	year, rule := birthday.Series(d, m, y)
	start := time.Date(year, time.Month(m), d, 0, 0, 0, 0, berlin)
	return calendar.Event{ID: birthday.EventID(contactID), Title: name, AllDay: true, Start: start, End: start.AddDate(0, 0, 1), RRule: rule, Birthday: true}
}

func dated(occs []calendar.Occurrence) []string {
	var out []string
	for _, o := range occs {
		out = append(out, o.Start.In(berlin).Format("02.01.2006")+" "+o.Title)
	}
	return out
}

func TestBirthdayOnTheLeapDayRepeatsOnTheLastDayOfFebruary(t *testing.T) {
	t.Parallel()
	ev := []calendar.Event{birthdayEvent("mia", "Mia", 29, 2, 2020)}
	for _, c := range []struct {
		year int
		want string
	}{{2025, "28.02.2025 Mia (5)"}, {2024, "29.02.2024 Mia (4)"}, {2027, "28.02.2027 Mia (7)"}} {
		occs, err := calendar.Expand(ev, day(c.year, 2, 1), day(c.year, 3, 5))
		if err != nil {
			t.Fatal(err)
		}
		equal(t, dated(occs), []string{c.want})
	}
}

func TestBirthdayShowsAgeOnlyWhenTheYearIsKnown(t *testing.T) {
	t.Parallel()
	ev := []calendar.Event{birthdayEvent("mia", "Mia", 12, 5, 2019), birthdayEvent("oma", "Oma Gerda", 3, 5, 0)}
	occs, err := calendar.Expand(ev, day(2026, 5, 1), day(2026, 6, 1))
	if err != nil {
		t.Fatal(err)
	}
	equal(t, dated(occs), []string{"03.05.2026 Oma Gerda", "12.05.2026 Mia (7)"})
}

func TestBirthYearItselfIsNoBirthday(t *testing.T) {
	t.Parallel()
	ev := []calendar.Event{birthdayEvent("mia", "Mia", 12, 5, 2026)}
	occs, err := calendar.Expand(ev, day(2026, 5, 1), day(2026, 6, 1))
	if err != nil {
		t.Fatal(err)
	}
	equal(t, dated(occs), nil)
	occs, err = calendar.Expand(ev, day(2027, 5, 1), day(2027, 6, 1))
	if err != nil {
		t.Fatal(err)
	}
	equal(t, dated(occs), []string{"12.05.2027 Mia (1)"})
}

func TestOrdinaryYearlyEventGetsNoAge(t *testing.T) {
	t.Parallel()
	ev := []calendar.Event{{ID: "anniv", Title: "Anniversary", AllDay: true, Start: day(2015, 5, 12), End: day(2015, 5, 13), RRule: "FREQ=YEARLY"}}
	occs, err := calendar.Expand(ev, day(2026, 5, 1), day(2026, 6, 1))
	if err != nil {
		t.Fatal(err)
	}
	equal(t, dated(occs), []string{"12.05.2026 Anniversary"})
}

// The age comes from the event being a birthday (the marker the store sets for
// the calendar of kind birthdays), not from what its id looks like.
func TestAgeFollowsTheBirthdayMarkerNotTheID(t *testing.T) {
	t.Parallel()
	marked := calendar.Event{ID: "9b2f0c1e-0000-4000-8000-000000000001", Title: "Mia", AllDay: true, Start: day(2019, 5, 12), End: day(2019, 5, 13), RRule: "FREQ=YEARLY", Birthday: true}
	lookalike := calendar.Event{ID: "birthday-x", Title: "Anniversary", AllDay: true, Start: day(2019, 5, 12), End: day(2019, 5, 13), RRule: "FREQ=YEARLY"}
	occs, err := calendar.Expand([]calendar.Event{marked, lookalike}, day(2026, 5, 1), day(2026, 6, 1))
	if err != nil {
		t.Fatal(err)
	}
	equal(t, dated(occs), []string{"12.05.2026 Anniversary", "12.05.2026 Mia (7)"})
}
