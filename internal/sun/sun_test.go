package sun_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/sun"
)

var berlin = func() *time.Location {
	l, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		panic(err)
	}
	return l
}()

// Pasing, Munich. Reference times (almanac): on 14/11/2026 the sun rises at
// about 07:13 and sets at about 16:36 local time.
const lat, lon = 48.15, 11.46

func TestIsDownAroundSunsetAndSunriseInMunichInNovember(t *testing.T) {
	cases := []struct {
		h, m int
		down bool
	}{
		{6, 30, true}, {7, 5, true}, {7, 25, false}, {12, 0, false},
		{16, 25, false}, {16, 50, true}, {23, 0, true},
	}
	for _, c := range cases {
		at := time.Date(2026, 11, 14, c.h, c.m, 0, 0, berlin)
		if got := sun.IsDown(at, lat, lon); got != c.down {
			t.Errorf("IsDown(%02d:%02d) = %v, want %v", c.h, c.m, got, c.down)
		}
	}
}

func TestIsDownInSummerEveningInMunich(t *testing.T) {
	// Sunset on 21/06/2026 is about 21:25 local time.
	if sun.IsDown(time.Date(2026, 6, 21, 21, 0, 0, 0, berlin), lat, lon) {
		t.Error("down at 21:00 in June")
	}
	if !sun.IsDown(time.Date(2026, 6, 21, 21, 50, 0, 0, berlin), lat, lon) {
		t.Error("up at 21:50 in June")
	}
}

func TestPolarDayAndPolarNight(t *testing.T) {
	tromso := func(m time.Month, h int) time.Time { return time.Date(2026, m, 21, h, 0, 0, 0, time.UTC) }
	if sun.IsDown(tromso(time.June, 23), 69.65, 18.96) {
		t.Error("midnight sun: down")
	}
	if !sun.IsDown(tromso(time.December, 11), 69.65, 18.96) {
		t.Error("polar night: up")
	}
}
