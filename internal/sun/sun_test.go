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

// Munich (48.1371 N, 11.5754 E). Reference: Open-Meteo (archive and
// historical forecast) and sunrise-sunset.org, which agree to the minute,
// in UTC.
var munich = struct{ lat, lon float64 }{48.1371, 11.5754}

func TestMunichSunTimesMatchTheReferenceTableWithinTwoMinutes(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	for _, c := range []struct {
		day             time.Time
		sunrise, sunset string
	}{
		{time.Date(2026, 6, 21, 0, 0, 0, 0, berlin), "2026-06-21T03:13:00Z", "2026-06-21T19:17:00Z"},
		{time.Date(2026, 12, 21, 0, 0, 0, 0, berlin), "2026-12-21T07:01:00Z", "2026-12-21T15:22:00Z"},
	} {
		got := sun.On(c.day, munich.lat, munich.lon)
		if got.Kind != sun.Normal {
			t.Fatalf("%s: kind = %v", c.day.Format("02/01"), got.Kind)
		}
		for name, pair := range map[string][2]string{"sunrise": {c.sunrise, got.Rise.UTC().Format(time.RFC3339)}, "sunset": {c.sunset, got.Set.UTC().Format(time.RFC3339)}} {
			want, _ := time.Parse(time.RFC3339, pair[0])
			have, _ := time.Parse(time.RFC3339, pair[1])
			if d := have.Sub(want); d < -2*time.Minute || d > 2*time.Minute {
				t.Errorf("%s %s = %s, want %s +-2 min", c.day.Format("02/01"), name, pair[1], pair[0])
			}
		}
	}
}

func TestSunTimesAreInTheZoneOfTheGivenDay(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	got := sun.On(time.Date(2026, 6, 21, 12, 0, 0, 0, berlin), munich.lat, munich.lon)
	if got.Rise.Location() != berlin || got.Rise.Hour() != 5 {
		t.Fatalf("sunrise = %v, want about 05:13 Berlin time", got.Rise)
	}
}

func TestOnPolarDayAndPolarNight(t *testing.T) {
	// Tromso, 69.65 N: midnight sun in June, polar night in December.
	if k := sun.On(time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC), 69.65, 18.96).Kind; k != sun.AlwaysUp {
		t.Errorf("June = %v, want AlwaysUp", k)
	}
	if k := sun.On(time.Date(2026, 12, 21, 12, 0, 0, 0, time.UTC), 69.65, 18.96).Kind; k != sun.AlwaysDown {
		t.Errorf("December = %v, want AlwaysDown", k)
	}
}
