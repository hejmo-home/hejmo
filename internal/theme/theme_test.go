package theme_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/sun"
	"github.com/hejmo-home/hejmo/internal/theme"
)

const lat, lon = 48.1371, 11.5754 // Munich

var berlin = func() *time.Location { l, _ := time.LoadLocation("Europe/Berlin"); return l }()

// Midsummer 2026 in Munich: sunrise 03:13 UTC, sunset 19:17 UTC (see internal/sun).
func solar(t *testing.T) sun.Times {
	t.Helper()
	s := sun.On(time.Date(2026, 6, 21, 12, 0, 0, 0, berlin), lat, lon)
	if s.Kind != sun.Normal {
		t.Fatal("no sun times")
	}
	return s
}

func auto(now time.Time, rise, set int) theme.Input {
	return theme.Input{Setting: theme.Auto, SunriseOffset: rise, SunsetOffset: set, Now: now, Lat: lat, Lon: lon}
}

// 5.15: offset -30 at sunset, 30 minutes before sunset: dark.
func TestAutomaticTurnsDarkAtSunsetShiftedByItsOffset(t *testing.T) {
	s := solar(t)
	in := auto(s.Set.Add(-30*time.Minute), 0, -30)
	if got := theme.Resolve(in); got != theme.Dark {
		t.Fatalf("at sunset-30 with offset -30 = %s, want dark", got)
	}
	in.Now = s.Set.Add(-31 * time.Minute)
	if got := theme.Resolve(in); got != theme.Light {
		t.Fatalf("one minute earlier = %s, want light", got)
	}
}

// 5.15: offset +45 at sunrise: light 45 minutes after sunrise.
func TestAutomaticTurnsLightAtSunriseShiftedByItsOffset(t *testing.T) {
	s := solar(t)
	in := auto(s.Rise.Add(45*time.Minute), 45, 0)
	if got := theme.Resolve(in); got != theme.Light {
		t.Fatalf("at sunrise+45 with offset +45 = %s, want light", got)
	}
	in.Now = s.Rise.Add(44 * time.Minute)
	if got := theme.Resolve(in); got != theme.Dark {
		t.Fatalf("one minute earlier = %s, want dark", got)
	}
}

func TestAutomaticIsDarkAtNightAndLightByDay(t *testing.T) {
	s := solar(t)
	for name, c := range map[string]struct {
		at   time.Time
		want theme.Theme
	}{
		"after midnight":  {time.Date(2026, 6, 21, 1, 0, 0, 0, berlin), theme.Dark},
		"noon":            {s.Rise.Add(7 * time.Hour), theme.Light},
		"late evening":    {time.Date(2026, 6, 21, 23, 0, 0, 0, berlin), theme.Dark},
		"after midnight2": {time.Date(2026, 6, 22, 0, 30, 0, 0, berlin), theme.Dark},
	} {
		if got := theme.Resolve(auto(c.at, 0, 0)); got != c.want {
			t.Errorf("%s = %s, want %s", name, got, c.want)
		}
	}
}

// A sunset offset can push the switch past midnight; the evening before still counts.
func TestAutomaticUsesTheSwitchOfThePreviousDay(t *testing.T) {
	s := solar(t)
	// Dark since sunset+180 on the 21st, i.e. 00:17 on the 22nd; at 00:30 on the 22nd it is dark
	// even though the 22nd's own sunset is far away.
	now := s.Set.Add(180*time.Minute + 13*time.Minute)
	if got := theme.Resolve(auto(now, 0, 180)); got != theme.Dark {
		t.Fatalf("after the shifted sunset = %s, want dark", got)
	}
	if got := theme.Resolve(auto(now.Add(-14*time.Minute), 0, 180)); got != theme.Light {
		t.Fatalf("before the shifted sunset = %s, want light", got)
	}
}

func TestFixedThemesIgnoreTheSun(t *testing.T) {
	noon := time.Date(2026, 6, 21, 12, 0, 0, 0, berlin)
	if got := theme.Resolve(theme.Input{Setting: theme.Dark, Now: noon, Lat: lat, Lon: lon}); got != theme.Dark {
		t.Errorf("dark = %s", got)
	}
	night := time.Date(2026, 6, 21, 2, 0, 0, 0, berlin)
	if got := theme.Resolve(theme.Input{Setting: theme.Light, Now: night, Lat: lat, Lon: lon}); got != theme.Light {
		t.Errorf("light = %s", got)
	}
}

// Far north the sunrise switch (sunrise minus 180 minutes) can fall on the
// evening before: tomorrow's switches count too.
func TestAutomaticSeesTomorrowsEarlySunriseSwitch(t *testing.T) {
	utc := time.UTC
	rvk := func(now time.Time) theme.Input {
		return theme.Input{Setting: theme.Auto, SunriseOffset: -180, SunsetOffset: -180, Now: now, Lat: 64.15, Lon: -21.94}
	}
	tom := sun.On(time.Date(2026, 6, 22, 12, 0, 0, 0, utc), 64.15, -21.94)
	sw := tom.Rise.Add(-180 * time.Minute)
	if sw.Day() != 21 {
		t.Fatalf("switch falls on %v, not the evening before", sw)
	}
	if got := theme.Resolve(rvk(sw.Add(time.Minute))); got != theme.Light {
		t.Errorf("after tomorrow's early light switch = %s, want light", got)
	}
}

func TestPolarDayAndNightAndUnknownLocation(t *testing.T) {
	noon := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	if got := theme.Resolve(theme.Input{Setting: theme.Auto, Now: noon, Lat: 69.65, Lon: 18.96}); got != theme.Light {
		t.Errorf("midnight sun = %s, want light", got)
	}
	dec := time.Date(2026, 12, 21, 12, 0, 0, 0, time.UTC)
	if got := theme.Resolve(theme.Input{Setting: theme.Auto, Now: dec, Lat: 69.65, Lon: 18.96}); got != theme.Dark {
		t.Errorf("polar night = %s, want dark", got)
	}
	// No home location stored (0, 0): nothing to compute, stay light.
	if got := theme.Resolve(theme.Input{Setting: theme.Auto, Now: time.Date(2026, 6, 21, 2, 0, 0, 0, time.UTC)}); got != theme.Light {
		t.Errorf("unknown location = %s, want light", got)
	}
}

func TestOffsetsAreLimitedTo180Minutes(t *testing.T) {
	for _, c := range []struct {
		m  int
		ok bool
	}{{-180, true}, {180, true}, {0, true}, {-181, false}, {181, false}} {
		if theme.ValidOffset(c.m) != c.ok {
			t.Errorf("ValidOffset(%d) = %v", c.m, !c.ok)
		}
	}
}

func TestDefaultsByDisplayType(t *testing.T) {
	if theme.DefaultFor("eink_bw") != theme.Light || theme.DefaultFor("eink_color") != theme.Light || theme.DefaultFor("screen") != theme.Auto {
		t.Fatal("defaults: E-Ink light, normal screen automatic")
	}
}
