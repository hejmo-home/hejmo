// Package sun computes sunrise and sunset locally (ADR 0015: themes by sun
// position, no network). It uses the NOAA solar calculator equations
// (Meeus, Astronomical Algorithms), accurate to about a minute outside the
// polar circles.
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
