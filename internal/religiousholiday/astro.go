package religiousholiday

import (
	"math"
	"sort"
	"time"

	"github.com/soniakeys/meeus/v3/julian"
	"github.com/soniakeys/meeus/v3/moonphase"
	"github.com/soniakeys/meeus/v3/moonposition"
	"github.com/soniakeys/meeus/v3/solar"
)

// deltaT is the difference of dynamical time and UTC, in seconds, around 2026.
const deltaT = 69.0

// jdeTime turns a Julian ephemeris day into a UTC time.
func jdeTime(jde float64) time.Time {
	return julian.JDToTime(jde - deltaT/86400)
}

// timeJDE turns a UTC time into a Julian ephemeris day.
func timeJDE(t time.Time) float64 {
	return julian.TimeToJD(t) + deltaT/86400
}

// moonTimes lists the times of new moons (full false) or full moons that fall
// from the beginning of fromYear to the end of toYear, in order.
func moonTimes(full bool, fromYear, toYear int) []time.Time {
	var out []time.Time
	start := time.Date(fromYear, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(toYear+1, 1, 1, 0, 0, 0, 0, time.UTC)
	for k := -2.0; ; k++ {
		year := float64(fromYear) + k/12.3685
		var jde float64
		if full {
			jde = moonphase.Full(year)
		} else {
			jde = moonphase.New(year)
		}
		t := jdeTime(jde)
		if t.After(end.AddDate(0, 0, 40)) {
			break
		}
		if t.Before(start.AddDate(0, 0, -40)) {
			continue
		}
		if n := len(out); n > 0 && math.Abs(t.Sub(out[n-1]).Hours()) < 24 {
			continue // the same moon found twice
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// sunLongitude is the apparent ecliptic longitude of the sun, in degrees, at t.
func sunLongitude(t time.Time) float64 {
	return solar.ApparentLongitude((timeJDE(t) - 2451545) / 36525).Deg()
}

// moonLongitude is the ecliptic longitude of the moon, in degrees, at t.
func moonLongitude(t time.Time) float64 {
	l, _, _ := moonposition.Position(timeJDE(t))
	return l.Deg()
}

func norm360(x float64) float64 {
	x = math.Mod(x, 360)
	if x < 0 {
		x += 360
	}
	return x
}

// tithi is the number of the lunar day at t, 1 to 30: the angle between moon and sun in steps of 12 degrees.
func tithi(t time.Time) int {
	return int(norm360(moonLongitude(t)-sunLongitude(t))/12) + 1
}

// lahiri is the ayanamsa of the Lahiri (Chitrapaksha) convention at t, in degrees: how far the
// sidereal zodiac of Hindu astronomy is shifted against the tropical one (23.853 degrees at 2000.0, 50.29 arc seconds a year).
func lahiri(t time.Time) float64 {
	years := (timeJDE(t) - 2451545) / 365.25
	return 23.853 + years*50.29/3600
}

// siderealSun is the longitude of the sun in the sidereal zodiac, in degrees.
func siderealSun(t time.Time) float64 { return norm360(sunLongitude(t) - lahiri(t)) }
