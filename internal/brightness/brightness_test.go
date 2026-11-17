package brightness_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/brightness"
	"github.com/hejmo-home/hejmo/internal/quiet"
	"github.com/hejmo-home/hejmo/internal/sun"
)

const lat, lon = 48.1371, 11.5754 // Munich

var berlin = func() *time.Location { l, _ := time.LoadLocation("Europe/Berlin"); return l }()

func input(now time.Time) brightness.Input {
	return brightness.Input{Settings: brightness.Defaults(), Now: now, Lat: lat, Lon: lon}
}

func noon(t *testing.T) (time.Time, sun.Times) {
	t.Helper()
	day := time.Date(2026, 6, 21, 12, 0, 0, 0, berlin)
	s := sun.On(day, lat, lon)
	if s.Kind != sun.Normal {
		t.Fatal("no sun times")
	}
	return day, s
}

func TestDefaultsAreFullByDayFortyPercentInTheEveningAndFiveWhenDimmed(t *testing.T) {
	d := brightness.Defaults()
	if d.Day != 100 || d.Evening != 40 || d.QuietMode != brightness.QuietOff || d.QuietLevel != 5 || d.WakeMinutes != 1 || d.Source != brightness.SourceSun {
		t.Fatalf("defaults = %+v", d)
	}
}

// Q76: Day and Evening switch at the sun times of the display's automatic theme.
func TestDayAndEveningFollowTheSunWithTheThemeOffsets(t *testing.T) {
	_, s := noon(t)
	in := input(s.Set.Add(-time.Minute))
	got := brightness.Plan(in)
	if got.Phase != brightness.PhaseDay || got.Percent != 100 || !got.Since.Equal(s.Rise) {
		t.Fatalf("before sunset = %+v", got)
	}
	in.Now = s.Set
	got = brightness.Plan(in)
	if got.Phase != brightness.PhaseEvening || got.Percent != 40 || !got.Since.Equal(s.Set) {
		t.Fatalf("at sunset = %+v", got)
	}
	in.SunsetOffset = 30 // shifted like the theme
	if got := brightness.Plan(in); got.Phase != brightness.PhaseDay {
		t.Fatalf("at sunset with offset +30 = %+v, want day", got)
	}
}

func TestWithoutAHomeLocationItIsDayAllDay(t *testing.T) {
	_, s := noon(t)
	in := input(s.Set.Add(3 * time.Hour))
	in.Lat, in.Lon = 0, 0
	if got := brightness.Plan(in); got.Phase != brightness.PhaseDay || got.Percent != 100 {
		t.Fatalf("no location = %+v", got)
	}
}

// Q77: quiet mode is off (front light off, or the normal screen off) or strongly dimmed.
func TestQuietModeSwitchesOffOrDimsAndSaysSince(t *testing.T) {
	now := time.Date(2026, 6, 21, 23, 30, 0, 0, berlin)
	in := input(now)
	in.Quiet = quiet.Schedule{Enabled: true, From: 22 * 60, To: 6 * 60}
	got := brightness.Plan(in)
	if got.Phase != brightness.PhaseQuietOff || !got.Off || got.ScreenOff || got.Percent != 0 {
		t.Fatalf("quiet off on a front light display = %+v", got) // E-Ink: front light to 0
	}
	if want := time.Date(2026, 6, 21, 22, 0, 0, 0, berlin); !got.Since.Equal(want) {
		t.Fatalf("since = %s, want %s", got.Since, want)
	}
	in.Screen = true
	if got := brightness.Plan(in); !got.Off || !got.ScreenOff {
		t.Fatalf("quiet off on a normal screen = %+v, want screen off", got)
	}
	in.Settings.QuietMode, in.Settings.QuietLevel = brightness.QuietDim, 7
	if got := brightness.Plan(in); got.Phase != brightness.PhaseQuietDim || got.Percent != 7 || got.Off || got.ScreenOff {
		t.Fatalf("dimmed = %+v", got)
	}
	// After 06:00 the sun rules again.
	in.Now = time.Date(2026, 6, 22, 6, 0, 0, 0, berlin)
	if got := brightness.Plan(in); got.Phase != brightness.PhaseDay {
		t.Fatalf("after quiet mode = %+v", got)
	}
}

func TestQuietModeSwitchedOffLeavesTheNightToTheEveningValue(t *testing.T) {
	in := input(time.Date(2026, 6, 21, 23, 30, 0, 0, berlin))
	in.Quiet = quiet.Schedule{Enabled: false, From: 22 * 60, To: 6 * 60}
	if got := brightness.Plan(in); got.Phase != brightness.PhaseEvening || got.Percent != 40 {
		t.Fatalf("night without quiet mode = %+v", got)
	}
}

// Q78: room light lets the device's sensor regulate; quiet mode applies either way.
func TestRoomLightHandsOverToTheSensorOnlyWithOne(t *testing.T) {
	in := input(time.Date(2026, 6, 21, 12, 0, 0, 0, berlin))
	in.Settings.Source = brightness.SourceRoom
	if got := brightness.Plan(in); got.Auto || got.Percent != 100 {
		t.Fatalf("room light without a sensor = %+v, want the sun values", got)
	}
	in.HasSensor = true
	if got := brightness.Plan(in); !got.Auto || got.Phase != brightness.PhaseRoom {
		t.Fatalf("room light with a sensor = %+v", got)
	}
	in.Now = time.Date(2026, 6, 21, 23, 0, 0, 0, berlin)
	in.Quiet = quiet.Schedule{Enabled: true, From: 22 * 60, To: 6 * 60}
	if got := brightness.Plan(in); got.Auto || !got.Off {
		t.Fatalf("quiet mode under room light = %+v, want off", got)
	}
}

// Q79: a touch in quiet mode wakes the display at evening brightness, then it goes off again.
func TestAWokenDisplayShowsAtEveningBrightness(t *testing.T) {
	in := input(time.Date(2026, 6, 21, 23, 30, 0, 0, berlin))
	in.Quiet = quiet.Schedule{Enabled: true, From: 22 * 60, To: 6 * 60}
	in.Screen, in.Woken = true, true
	got := brightness.Plan(in)
	if got.Phase != brightness.PhaseWake || got.Percent != 40 || got.Off || got.ScreenOff {
		t.Fatalf("woken = %+v", got)
	}
	in.Settings.Source, in.HasSensor = brightness.SourceRoom, true
	if got := brightness.Plan(in); !got.Auto || got.Phase != brightness.PhaseWake {
		t.Fatalf("woken under room light = %+v", got)
	}
	in.Woken, in.Quiet.Enabled = true, false // not in quiet mode: a wake changes nothing
	if got := brightness.Plan(in); got.Phase == brightness.PhaseWake {
		t.Fatalf("wake outside quiet mode = %+v", got)
	}
}

func TestValidation(t *testing.T) {
	for n, ok := range map[int]bool{0: false, 1: true, 100: true, 101: false, -5: false} {
		if brightness.ValidPercent(n) != ok {
			t.Errorf("ValidPercent(%d) = %v", n, !ok)
		}
	}
	for n, ok := range map[int]bool{0: false, 1: true, 30: true, 31: false} {
		if brightness.ValidWake(n) != ok {
			t.Errorf("ValidWake(%d) = %v", n, !ok)
		}
	}
	if !brightness.QuietDim.Valid() || !brightness.QuietOff.Valid() || brightness.QuietMode("x").Valid() || !brightness.SourceRoom.Valid() || brightness.Source("moon").Valid() {
		t.Error("modes and sources are not validated")
	}
}
