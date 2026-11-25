package religiousholiday_test

import (
	"testing"
	"time"

	rh "github.com/hejmo-home/hejmo/internal/religiousholiday"
)

func d(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }

// find returns the holiday with this key that starts in this year.
func find(t *testing.T, hs []rh.Holiday, key string, year int) rh.Holiday {
	t.Helper()
	for _, h := range hs {
		if h.Key == key && h.Start.Year() == year {
			return h
		}
	}
	t.Fatalf("no %s in %d", key, year)
	return rh.Holiday{}
}

func TestChristianDays(t *testing.T) {
	t.Parallel()
	hs := rh.Compute(rh.Christian, rh.Options{}, 2026, 2027)
	for _, c := range []struct {
		key  string
		year int
		want time.Time
	}{
		{"advent1", 2026, d(2026, 11, 29)},
		{"advent4", 2026, d(2026, 12, 20)},
		{"christmas_eve", 2026, d(2026, 12, 24)},
		{"ash_wednesday", 2027, d(2027, 2, 10)},
		{"palm_sunday", 2027, d(2027, 3, 21)},
		{"maundy_thursday", 2027, d(2027, 3, 25)},
		{"good_friday", 2027, d(2027, 3, 26)},
		{"easter", 2027, d(2027, 3, 28)},
		{"easter_monday", 2027, d(2027, 3, 29)},
		{"ascension", 2027, d(2027, 5, 6)},
		{"pentecost", 2027, d(2027, 5, 16)},
		{"corpus_christi", 2027, d(2027, 5, 27)},
		{"harvest_thanks", 2026, d(2026, 10, 4)},
		{"reformation", 2026, d(2026, 10, 31)},
		{"st_martin", 2026, d(2026, 11, 11)},
		{"repentance_day", 2026, d(2026, 11, 18)},
		{"saint_nicholas", 2026, d(2026, 12, 6)},
	} {
		h := find(t, hs, c.key, c.year)
		if !h.Start.Equal(c.want) || !h.End.Equal(c.want) {
			t.Errorf("%s %d: %v to %v, want %v", c.key, c.year, h.Start, h.End, c.want)
		}
		if h.Religion != rh.Christian || h.Source == "" || h.Vary {
			t.Errorf("%s: religion %q source %q vary %v", c.key, h.Religion, h.Source, h.Vary)
		}
	}
	xmas := find(t, hs, "christmas_days", 2026)
	if !xmas.Start.Equal(d(2026, 12, 25)) || !xmas.End.Equal(d(2026, 12, 26)) {
		t.Errorf("christmas days: %v to %v", xmas.Start, xmas.End)
	}
}

func TestOrthodoxFollowsTheChosenCalendarForChristmasAndAlwaysEasterByTheOldRule(t *testing.T) {
	t.Parallel()
	revised := rh.Compute(rh.Orthodox, rh.Options{Orthodox: rh.RevisedJulian}, 2026, 2027)
	old := rh.Compute(rh.Orthodox, rh.Options{Orthodox: rh.Julian}, 2026, 2027)
	if got := find(t, revised, "christmas", 2026).Start; !got.Equal(d(2026, 12, 25)) {
		t.Errorf("revised Julian Christmas: %v", got)
	}
	if got := find(t, old, "christmas", 2027).Start; !got.Equal(d(2027, 1, 7)) {
		t.Errorf("Julian Christmas: %v", got)
	}
	if got := find(t, old, "theophany", 2027).Start; !got.Equal(d(2027, 1, 19)) {
		t.Errorf("Julian Theophany: %v", got)
	}
	if got := find(t, revised, "theophany", 2027).Start; !got.Equal(d(2027, 1, 6)) {
		t.Errorf("revised Julian Theophany: %v", got)
	}
	for _, hs := range [][]rh.Holiday{revised, old} {
		// Orthodox Easter: 2026 on 12 April, 2027 on 2 May (both calendars).
		if got := find(t, hs, "easter", 2026).Start; !got.Equal(d(2026, 4, 12)) {
			t.Errorf("Easter 2026: %v", got)
		}
		if got := find(t, hs, "easter", 2027).Start; !got.Equal(d(2027, 5, 2)) {
			t.Errorf("Easter 2027: %v", got)
		}
		if got := find(t, hs, "good_friday", 2027).Start; !got.Equal(d(2027, 4, 30)) {
			t.Errorf("Good Friday 2027: %v", got)
		}
		if got := find(t, hs, "pentecost", 2027).Start; !got.Equal(d(2027, 6, 20)) {
			t.Errorf("Pentecost 2027: %v", got)
		}
	}
}

func TestDefaultOrthodoxCalendarIsTheRevisedJulianOne(t *testing.T) {
	t.Parallel()
	hs := rh.Compute(rh.Orthodox, rh.Options{}, 2026, 2026)
	if got := find(t, hs, "christmas", 2026).Start; !got.Equal(d(2026, 12, 25)) {
		t.Errorf("Christmas without a choice: %v", got)
	}
}
