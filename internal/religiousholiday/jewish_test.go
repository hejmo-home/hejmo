package religiousholiday_test

import (
	"testing"
	"time"

	rh "github.com/hejmo-home/hejmo/internal/religiousholiday"
)

func TestJewishFestivalsAreOneEventWithTheirEveAndTheirHebrewDates(t *testing.T) {
	t.Parallel()
	hs := rh.Compute(rh.Jewish, rh.Options{}, 2026, 2027)
	type want struct {
		key      string
		year     int
		from, to time.Time
		own      rh.CalDate
	}
	for _, c := range []want{
		{"rosh_hashanah", 2026, d(2026, 9, 12), d(2026, 9, 13), rh.CalDate{Cal: "hebrew", Day: 1, Month: 7, Year: 5787, EndDay: 2, EndMonth: 7, EndYear: 5787}},
		{"yom_kippur", 2026, d(2026, 9, 21), d(2026, 9, 21), rh.CalDate{Cal: "hebrew", Day: 10, Month: 7, Year: 5787, EndDay: 10, EndMonth: 7, EndYear: 5787}},
		{"sukkot", 2026, d(2026, 9, 26), d(2026, 10, 2), rh.CalDate{Cal: "hebrew", Day: 15, Month: 7, Year: 5787, EndDay: 21, EndMonth: 7, EndYear: 5787}},
		{"simchat_torah", 2026, d(2026, 10, 3), d(2026, 10, 4), rh.CalDate{Cal: "hebrew", Day: 22, Month: 7, Year: 5787, EndDay: 23, EndMonth: 7, EndYear: 5787}},
		{"chanukkah", 2026, d(2026, 12, 5), d(2026, 12, 12), rh.CalDate{Cal: "hebrew", Day: 25, Month: 9, Year: 5787, EndDay: 2, EndMonth: 10, EndYear: 5787}},
		{"purim", 2027, d(2027, 3, 23), d(2027, 3, 23), rh.CalDate{Cal: "hebrew", Day: 14, Month: 13, Year: 5787, EndDay: 14, EndMonth: 13, EndYear: 5787}},
		{"pessach", 2027, d(2027, 4, 22), d(2027, 4, 29), rh.CalDate{Cal: "hebrew", Day: 15, Month: 1, Year: 5787, EndDay: 22, EndMonth: 1, EndYear: 5787}},
		{"shavuot", 2027, d(2027, 6, 11), d(2027, 6, 12), rh.CalDate{Cal: "hebrew", Day: 6, Month: 3, Year: 5787, EndDay: 7, EndMonth: 3, EndYear: 5787}},
	} {
		h := find(t, hs, c.key, c.year)
		if !h.Start.Equal(c.from) || !h.End.Equal(c.to) {
			t.Errorf("%s %d: %s to %s, want %s to %s", c.key, c.year, h.Start.Format("2006-01-02"), h.End.Format("2006-01-02"), c.from.Format("2006-01-02"), c.to.Format("2006-01-02"))
		}
		if h.Own != c.own {
			t.Errorf("%s: own date %+v, want %+v", c.key, h.Own, c.own)
		}
		if !h.Eve && c.key != "chanukkah" {
			t.Errorf("%s has no eve", c.key)
		}
		if h.Vary || h.Source != rh.SrcHebcal || h.Unchecked {
			t.Errorf("%s: vary %v source %q unchecked %v", c.key, h.Vary, h.Source, h.Unchecked)
		}
	}
}

func TestChanukkahHasItsEveBeforeTheFirstDay(t *testing.T) {
	t.Parallel()
	h := find(t, rh.Compute(rh.Jewish, rh.Options{}, 2027, 2027), "chanukkah", 2027)
	if !h.Eve || !h.Start.Equal(d(2027, 12, 25)) || !h.End.Equal(d(2028, 1, 1)) || h.Days() != 8 {
		t.Errorf("Chanukkah 2027: %v to %v eve %v days %d", h.Start, h.End, h.Eve, h.Days())
	}
}
