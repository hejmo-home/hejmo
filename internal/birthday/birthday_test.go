package birthday_test

import (
	"regexp"
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

var uuidV5 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// The API declares every event id a uuid, so the id of a birthday series must
// be one: a UUIDv5 of the contact id, the same on every box start.
func TestEventIDIsAStableUUIDPerContact(t *testing.T) {
	t.Parallel()
	id := birthday.EventID("abc123")
	if !uuidV5.MatchString(id) {
		t.Fatalf("EventID = %q, want a version 5 UUID", id)
	}
	// Computed with Python's uuid.uuid5(uuid.NAMESPACE_URL, "abc123").
	if id != "616d42ca-cddb-57f6-a86d-ed5fbcd0ed3d" {
		t.Errorf("EventID(abc123) = %q, want 616d42ca-cddb-57f6-a86d-ed5fbcd0ed3d", id)
	}
	if again := birthday.EventID("abc123"); again != id {
		t.Errorf("EventID is not stable: %q, then %q", id, again)
	}
	if other := birthday.EventID("abc124"); other == id {
		t.Error("two contacts share an event id")
	}
}
