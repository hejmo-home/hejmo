package sun_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/sun"
)

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

func TestPolarDayAndPolarNight(t *testing.T) {
	// Tromso, 69.65 N: midnight sun in June, polar night in December.
	if k := sun.On(time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC), 69.65, 18.96).Kind; k != sun.AlwaysUp {
		t.Errorf("June = %v, want AlwaysUp", k)
	}
	if k := sun.On(time.Date(2026, 12, 21, 12, 0, 0, 0, time.UTC), 69.65, 18.96).Kind; k != sun.AlwaysDown {
		t.Errorf("December = %v, want AlwaysDown", k)
	}
}
