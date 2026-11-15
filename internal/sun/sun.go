// Package sun computes sunrise and sunset locally and tells whether the sun is
// below the horizon (ADR 0015: themes and quiet mode by sun position, no
// network). It uses the NOAA solar calculator equations (Meeus, Astronomical
// Algorithms), accurate to about a minute.
package sun

import (
	"math"
	"time"
)

// Kind says whether the sun rises and sets on a day.
type Kind int

// Kinds of day.
const (
	Normal     Kind = iota // the sun rises and sets
	AlwaysUp               // midnight sun
	AlwaysDown             // polar night
)

// Times are the sun times of one day.
type Times struct {
	Kind Kind
	// Rise and Set are in the zone of the day passed to On; zero unless Kind is Normal.
	Rise, Set time.Time
}

// On returns sunrise and sunset on the calendar day of day (in day's own zone)
// at latitude lat and longitude lon in degrees (north and east positive).
func On(day time.Time, lat, lon float64) Times {
	loc := day.Location()
	y, m, d := day.Date()
	noon := time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
	jd := float64(noon.Unix())/86400 + 2440587.5
	t := (jd - 2451545) / 36525

	rad := math.Pi / 180
	l0 := math.Mod(280.46646+t*(36000.76983+t*0.0003032), 360)
	mean := 357.52911 + t*(35999.05029-0.0001537*t)
	ecc := 0.016708634 - t*(0.000042037+0.0000001267*t)
	c := math.Sin(mean*rad)*(1.914602-t*(0.004817+0.000014*t)) +
		math.Sin(2*mean*rad)*(0.019993-0.000101*t) + math.Sin(3*mean*rad)*0.000289
	trueLong := l0 + c
	omega := 125.04 - 1934.136*t
	appLong := trueLong - 0.00569 - 0.00478*math.Sin(omega*rad)
	obliq := 23 + (26+(21.448-t*(46.815+t*(0.00059-t*0.001813)))/60)/60 +
		0.00256*math.Cos(omega*rad)
	decl := math.Asin(math.Sin(obliq*rad)*math.Sin(appLong*rad)) / rad
	v := math.Tan(obliq/2*rad) * math.Tan(obliq/2*rad)
	eqTime := 4 / rad * (v*math.Sin(2*l0*rad) - 2*ecc*math.Sin(mean*rad) +
		4*ecc*v*math.Sin(mean*rad)*math.Cos(2*l0*rad) -
		0.5*v*v*math.Sin(4*l0*rad) - 1.25*ecc*ecc*math.Sin(2*mean*rad))

	// Hour angle at the standard sunrise elevation (-0.833 degrees: refraction and the sun's radius).
	cosH := (math.Cos(90.833*rad) - math.Sin(lat*rad)*math.Sin(decl*rad)) / (math.Cos(lat*rad) * math.Cos(decl*rad))
	switch {
	case cosH < -1:
		return Times{Kind: AlwaysUp}
	case cosH > 1:
		return Times{Kind: AlwaysDown}
	}
	h := math.Acos(cosH) / rad
	noonMin := 720 - 4*lon - eqTime // solar noon, minutes after 00:00 UTC
	at := func(minutes float64) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Add(time.Duration(minutes * float64(time.Minute))).In(loc)
	}
	return Times{Kind: Normal, Rise: at(noonMin - 4*h), Set: at(noonMin + 4*h)}
}

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
