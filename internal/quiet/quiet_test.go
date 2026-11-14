package quiet_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/quiet"
)

var berlin = func() *time.Location {
	l, _ := time.LoadLocation("Europe/Berlin")
	return l
}()

func at(day, h, m int) time.Time { return time.Date(2026, 11, day, h, m, 0, 0, berlin) }

func night(t *testing.T) quiet.Schedule {
	t.Helper()
	s, err := quiet.Parse(true, "22:00", "06:00")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestActiveFrom2200To0600(t *testing.T) {
	s := night(t)
	for _, c := range []struct {
		t      time.Time
		active bool
	}{
		{at(14, 21, 59), false}, {at(14, 22, 0), true}, {at(14, 23, 59), true},
		{at(15, 0, 0), true}, {at(15, 5, 59), true}, {at(15, 6, 0), false}, {at(15, 12, 0), false},
	} {
		if got := s.Active(c.t); got != c.active {
			t.Errorf("Active(%s) = %v, want %v", c.t.Format("02 15:04"), got, c.active)
		}
	}
}

func TestDisabledScheduleIsNeverActive(t *testing.T) {
	s, _ := quiet.Parse(false, "22:00", "06:00")
	if s.Active(at(14, 23, 0)) {
		t.Fatal("disabled schedule is active")
	}
}

func TestSameDayWindow(t *testing.T) {
	s, _ := quiet.Parse(true, "13:00", "15:00")
	if !s.Active(at(14, 14, 0)) || s.Active(at(14, 15, 0)) || s.Active(at(14, 12, 59)) {
		t.Fatal("same-day window wrong")
	}
}

// ADR 0015: the night page is about the coming morning, the day after the
// evening quiet mode started: at 02:00 on Tuesday it still shows Tuesday.
func TestMorningIsTheDayTheWindowEnds(t *testing.T) {
	s := night(t)
	for _, c := range []struct {
		now  time.Time
		want int
	}{{at(14, 22, 0), 15}, {at(14, 23, 30), 15}, {at(15, 2, 0), 15}, {at(15, 5, 59), 15}} {
		y, m, d := s.Morning(c.now).Date()
		if y != 2026 || m != 11 || d != c.want {
			t.Errorf("Morning(%s) = %d.%d.%d, want day %d", c.now.Format("02 15:04"), d, m, y, c.want)
		}
	}
}

// A window that ends before midnight has no morning of its own: the night page
// is about the day after, not about today, which is already over.
func TestMorningOfAWindowEndingBeforeMidnightIsTomorrow(t *testing.T) {
	s, _ := quiet.Parse(true, "20:00", "23:30")
	for _, now := range []time.Time{at(14, 20, 0), at(14, 22, 0), at(14, 23, 29)} {
		if _, _, d := s.Morning(now).Date(); d != 15 {
			t.Errorf("Morning(%s) = day %d, want 15", now.Format("15:04"), d)
		}
	}
}

func TestParseRefusesBadTimes(t *testing.T) {
	for _, bad := range [][2]string{{"24:00", "06:00"}, {"22:00", "6"}, {"", "06:00"}, {"22:00", "06:60"}, {"22:00", "22:00"}} {
		if _, err := quiet.Parse(true, bad[0], bad[1]); err == nil {
			t.Errorf("Parse(%v) accepted", bad)
		}
	}
}
