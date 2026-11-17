// Package brightness decides how bright a display should be now (ADR 0020,
// ticket 179): Day or Evening by the sun at the home location, switching at
// the same times and offsets as the display's automatic theme (ADR 0015); in
// quiet mode off or strongly dimmed; optionally the device's own light sensor.
// Pure: time and settings in, target out. Sending it is the kiosk adapter's
// and the runner's job.
package brightness

import (
	"time"

	"github.com/hejmo-home/hejmo/internal/quiet"
	"github.com/hejmo-home/hejmo/internal/theme"
)

// QuietMode is what quiet mode does to the display's light.
type QuietMode string

// Quiet modes. QuietOff switches the front light (E-Ink) or the screen off.
const (
	QuietOff QuietMode = "off"
	QuietDim QuietMode = "dim"
)

// Valid reports whether m can be stored.
func (m QuietMode) Valid() bool { return m == QuietOff || m == QuietDim }

// Source says what the brightness follows.
type Source string

// Sources: the sun at the home location, or the device's light sensor.
const (
	SourceSun  Source = "sun"
	SourceRoom Source = "room"
)

// Valid reports whether s can be stored.
func (s Source) Valid() bool { return s == SourceSun || s == SourceRoom }

// Limits of the adjustable values.
const (
	MinPercent, MaxPercent = 1, 100 // 0 would be "off", which quiet mode says
	MinWake, MaxWake       = 1, 30  // minutes
)

// ValidPercent reports whether n is a brightness in percent.
func ValidPercent(n int) bool { return n >= MinPercent && n <= MaxPercent }

// ValidWake reports whether n is a wake duration in minutes.
func ValidWake(n int) bool { return n >= MinWake && n <= MaxWake }

// Settings are a display's brightness values.
type Settings struct {
	Day, Evening int // percent
	Source       Source
	QuietMode    QuietMode
	QuietLevel   int // percent, for QuietDim
	WakeMinutes  int // how long a touch wakes a display that quiet mode switched off
}

// Defaults are the values of a display nobody has adjusted (Q76, Q77, Q79).
func Defaults() Settings {
	return Settings{Day: 100, Evening: 40, Source: SourceSun, QuietMode: QuietOff, QuietLevel: 5, WakeMinutes: 1}
}

// Phase names why a target applies.
type Phase string

// Phases.
const (
	PhaseDay      Phase = "day"
	PhaseEvening  Phase = "evening"
	PhaseRoom     Phase = "room" // the light sensor regulates
	PhaseQuietOff Phase = "quiet_off"
	PhaseQuietDim Phase = "quiet_dim"
	PhaseWake     Phase = "wake" // touched in quiet mode
)

// Input is everything Plan needs.
type Input struct {
	Settings Settings
	// Quiet is the display's quiet mode schedule; Woken says a touch woke the
	// display during it.
	Quiet quiet.Schedule
	Woken bool
	// Screen is a normal screen: "off" switches the screen off. Without it
	// (E-Ink) "off" is the front light at 0 %.
	Screen bool
	// HasSensor is whether the device reports a light sensor.
	HasSensor                   bool
	SunriseOffset, SunsetOffset int
	Now                         time.Time // in the household's zone
	Lat, Lon                    float64   // home location; 0, 0 means not set
}

// Target is what the display should do now.
type Target struct {
	Phase Phase
	// Percent is the brightness to set (1 to 100); 0 when Off or Auto.
	Percent int
	// Off: switched off. ScreenOff says this is a normal screen, switched off
	// as a whole; otherwise the front light is set to 0 %.
	Off, ScreenOff bool
	// Auto: the device's light sensor regulates.
	Auto bool
	// Since is when this phase began; zero when no moment decides it.
	Since time.Time
}

// Plan returns what the display should do at in.Now.
func Plan(in Input) Target {
	s := in.Settings
	if in.Quiet.Active(in.Now) {
		since := in.Quiet.Since(in.Now)
		switch {
		case in.Woken:
			t := Target{Phase: PhaseWake, Percent: s.Evening, Since: in.Now}
			if s.Source == SourceRoom && in.HasSensor {
				t.Auto, t.Percent = true, 0
			}
			return t
		case s.QuietMode == QuietDim:
			return Target{Phase: PhaseQuietDim, Percent: s.QuietLevel, Since: since}
		default:
			return Target{Phase: PhaseQuietOff, Off: true, ScreenOff: in.Screen, Since: since}
		}
	}
	th, since := theme.ResolveSince(theme.Input{Setting: theme.Auto, SunriseOffset: in.SunriseOffset, SunsetOffset: in.SunsetOffset, Now: in.Now, Lat: in.Lat, Lon: in.Lon})
	if s.Source == SourceRoom && in.HasSensor {
		return Target{Phase: PhaseRoom, Auto: true, Since: since}
	}
	if th == theme.Dark {
		return Target{Phase: PhaseEvening, Percent: s.Evening, Since: since}
	}
	return Target{Phase: PhaseDay, Percent: s.Day, Since: since}
}
