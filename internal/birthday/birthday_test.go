package birthday_test

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/birthday"
	"github.com/hejmo-home/hejmo/internal/contact"
)

func TestSeriesStartsInTheBirthYearOrBeforeAnyRealYear(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name           string
		day, month, yr int
		wantYear       int
		wantRule       string
	}{
		{"year known", 12, 5, 2019, 2019, "FREQ=YEARLY"},
		{"year unknown starts before 1900, so no real year clashes", 12, 5, 0, birthday.UnknownYearStart, "FREQ=YEARLY"},
	} {
		year, rule := birthday.Series(c.day, c.month, c.yr)
		if year != c.wantYear || rule != c.wantRule {
			t.Errorf("%s: Series = %d %q, want %d %q", c.name, year, rule, c.wantYear, c.wantRule)
		}
	}
	if birthday.UnknownYearStart >= contact.MinYear {
		t.Errorf("UnknownYearStart %d must be before the first year a person can be born in (%d)", birthday.UnknownYearStart, contact.MinYear)
	}
}

// 29 February repeats on the last day of February in other years (ticket 302).
func TestLeapDayRepeatsOnTheLastDayOfFebruary(t *testing.T) {
	t.Parallel()
	for _, yr := range []int{2020, 0} {
		year, rule := birthday.Series(29, 2, yr)
		if rule != "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=-1" {
			t.Errorf("year %d: rule = %q, want the last day of February", yr, rule)
		}
		if year%4 != 0 {
			t.Errorf("year %d: the series starts in %d, which has no 29 February", yr, year)
		}
	}
}

func TestAgeOnlyWhenTheYearIsKnown(t *testing.T) {
	t.Parallel()
	if n, ok := birthday.Age(2019, 2026); !ok || n != 7 {
		t.Errorf("Age(2019, 2026) = %d %v, want 7 true", n, ok)
	}
	if _, ok := birthday.Age(birthday.UnknownYearStart, 2026); ok {
		t.Error("Age with an unknown year must not give an age")
	}
	if n, ok := birthday.Age(2026, 2026); !ok || n != 0 {
		t.Errorf("Age in the birth year = %d %v, want 0 true", n, ok)
	}
}

func TestEventIDRoundTrip(t *testing.T) {
	t.Parallel()
	id := birthday.EventID("abc123")
	if !birthday.IsEvent(id) {
		t.Fatalf("%q is not recognised as a birthday event", id)
	}
	if got := birthday.ContactID(id); got != "abc123" {
		t.Errorf("ContactID = %q, want abc123", got)
	}
	if birthday.IsEvent("abc123") {
		t.Error("an ordinary event id is no birthday event")
	}
}
