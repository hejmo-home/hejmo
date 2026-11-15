// Package sun tells whether the sun is below the horizon at a place and time.
// It is computed locally from the home location, no service involved (ADR 0015).
package sun

import (
	"math"
	"time"
)

// horizon is the sun's centre elevation at sunrise and sunset in degrees:
// atmospheric refraction and the sun's radius.
const horizon = -0.833

// IsDown reports whether the sun is below the horizon at t, at the given
// latitude and longitude (degrees, east positive). It follows the NOAA
// approximation of the solar position, accurate to about a minute, and works in
// polar day and polar night too.
func IsDown(t time.Time, lat, lon float64) bool {
	return elevation(t, lat, lon) < horizon
}

func rad(d float64) float64 { return d * math.Pi / 180 }

// elevation is the sun's height above the horizon in degrees.
func elevation(t time.Time, lat, lon float64) float64 {
	u := t.UTC()
	// Julian century from J2000.0.
	jd := float64(u.Unix())/86400 + 2440587.5
	c := (jd - 2451545) / 36525
	l0 := math.Mod(280.46646+c*(36000.76983+c*0.0003032), 360)
	m := 357.52911 + c*(35999.05029-0.0001537*c)
	ecc := 0.016708634 - c*(0.000042037+0.0000001267*c)
	center := math.Sin(rad(m))*(1.914602-c*(0.004817+0.000014*c)) +
		math.Sin(rad(2*m))*(0.019993-0.000101*c) + math.Sin(rad(3*m))*0.000289
	trueLong := l0 + center
	omega := 125.04 - 1934.136*c
	appLong := trueLong - 0.00569 - 0.00478*math.Sin(rad(omega))
	meanObliq := 23 + (26+(21.448-c*(46.815+c*(0.00059-c*0.001813)))/60)/60
	obliq := meanObliq + 0.00256*math.Cos(rad(omega))
	decl := math.Asin(math.Sin(rad(obliq)) * math.Sin(rad(appLong)))
	y := math.Tan(rad(obliq/2)) * math.Tan(rad(obliq/2))
	eqTime := 4 * (y*math.Sin(2*rad(l0)) - 2*ecc*math.Sin(rad(m)) + 4*ecc*y*math.Sin(rad(m))*math.Cos(2*rad(l0)) -
		0.5*y*y*math.Sin(4*rad(l0)) - 1.25*ecc*ecc*math.Sin(2*rad(m))) * 180 / math.Pi // minutes
	minutes := float64(u.Hour()*60+u.Minute()) + float64(u.Second())/60
	trueSolar := math.Mod(minutes+eqTime+4*lon, 1440)
	hourAngle := trueSolar/4 - 180
	if hourAngle < -180 {
		hourAngle += 360
	}
	cosZ := math.Sin(rad(lat))*math.Sin(decl) + math.Cos(rad(lat))*math.Cos(decl)*math.Cos(rad(hourAngle))
	return 90 - math.Acos(math.Max(-1, math.Min(1, cosZ)))*180/math.Pi
}
