// Package theme decides whether a display or the web interface is light or
// dark right now (ADR 0015): fixed, or automatic by sunrise and sunset at the
// home location, each shifted by its own offset. Pure: no clock, no storage.
package theme

import (
	"time"

	"github.com/hejmo-home/hejmo/internal/sun"
)

// Theme is a setting (Light, Dark, Auto) or the result of resolving one
// (Light, Dark, Night).
type Theme string

// Setting is a stored choice; it uses the same values as Theme.
type Setting = Theme

// Themes.
const (
	Light Theme = "light"
	Dark  Theme = "dark"
	Auto  Theme = "auto"
	// Night is the quiet-mode night page, which wins over every theme.
	Night Theme = "night"
)

// Valid reports whether s can be stored as a display's theme.
func (s Theme) Valid() bool { return s == Light || s == Dark || s == Auto }

// MaxOffset is the largest shift of a sun switch, in minutes either way.
const MaxOffset = 180

// ValidOffset reports whether m minutes is an allowed offset.
func ValidOffset(m int) bool { return m >= -MaxOffset && m <= MaxOffset }

// DefaultFor is the theme of a newly paired display of the given type
// (ADR 0015): E-Ink light, normal screen automatic.
func DefaultFor(displayType string) Theme {
	if displayType == "screen" {
		return Auto
	}
	return Light
}

// Input is everything Resolve needs.
type Input struct {
	Setting                     Setting
	SunriseOffset, SunsetOffset int       // minutes, each -180..180
	Now                         time.Time // in the household's zone
	Lat, Lon                    float64   // home location; 0, 0 means not set
	Quiet                       bool
}

// Resolve returns what to show now: Night in quiet mode, otherwise Light or Dark.
func Resolve(in Input) Theme {
	switch {
	case in.Quiet:
		return Night
	case in.Setting == Dark:
		return Dark
	case in.Setting != Auto:
		return Light
	case in.Lat == 0 && in.Lon == 0:
		return Light // no home location to compute from
	}
	today := sun.On(in.Now, in.Lat, in.Lon)
	switch today.Kind {
	case sun.AlwaysUp:
		return Light
	case sun.AlwaysDown:
		return Dark
	}
	// The latest switch at or before now decides. Offsets reach 3 hours, so
	// yesterday's sunset can still be the latest one.
	var latest time.Time
	result := Light
	for _, shift := range []int{-1, 0} {
		t := sun.On(in.Now.AddDate(0, 0, shift), in.Lat, in.Lon)
		if t.Kind != sun.Normal {
			continue
		}
		for _, sw := range []struct {
			at time.Time
			to Theme
		}{
			{t.Rise.Add(time.Duration(in.SunriseOffset) * time.Minute), Light},
			{t.Set.Add(time.Duration(in.SunsetOffset) * time.Minute), Dark},
		} {
			if !sw.at.After(in.Now) && sw.at.After(latest) {
				latest, result = sw.at, sw.to
			}
		}
	}
	return result
}
