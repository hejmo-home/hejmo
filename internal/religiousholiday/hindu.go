package religiousholiday

import (
	"sort"
	"time"

	"github.com/hejmo-home/hejmo/internal/sun"
)

// Hindu holidays follow the lunar day (tithi) that prevails at a time of the
// day that the festival names, in a lunar month named after the zodiac sign of
// the sun at the new moon before it (the amanta scheme; a month that begins
// with the same sign as the next one is the extra month, adhika, and has no
// festivals). Both depend on the place: sunrise, sunset and the date change.
// The rules are the published conventions of the Panchang (the almanac), the
// computation is ours on the moon and sun positions of meeus/v3; nothing is
// taken from other code.

type lunarMonth struct {
	start, end time.Time // new moons
	index      int       // 0 Chaitra .. 11 Phalguna
	adhika     bool
}

// lunarMonths lists the lunar months from the year before fromYear to the year after toYear.
func lunarMonths(fromYear, toYear int) []lunarMonth {
	nm := moonTimes(false, fromYear-1, toYear+1)
	var out []lunarMonth
	for i := 0; i+1 < len(nm); i++ {
		r := int(siderealSun(nm[i]) / 30)
		r2 := int(siderealSun(nm[i+1]) / 30)
		out = append(out, lunarMonth{start: nm[i], end: nm[i+1], index: (r + 1) % 12, adhika: r == r2})
	}
	return out
}

// Times of the day a festival names, after sunrise and sunset.
type dayPoint int

const (
	atSunrise   dayPoint = iota
	atMidday             // madhyahna
	atAfternoon          // aparahna
	atEvening            // pradosha
	atMidnight           // nishita
)

// pointOn is the time of the day point on a civil day at the place.
func pointOn(d time.Time, p dayPoint, o Options) time.Time {
	zone := o.zone()
	local := time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, zone)
	t := sun.On(local, o.Place.Lat, o.Place.Lon)
	if t.Kind != sun.Normal {
		t.Rise, t.Set = time.Date(d.Year(), d.Month(), d.Day(), 6, 0, 0, 0, zone), time.Date(d.Year(), d.Month(), d.Day(), 18, 0, 0, 0, zone)
	}
	dayLen := t.Set.Sub(t.Rise)
	switch p {
	case atSunrise:
		return t.Rise
	case atMidday:
		return t.Rise.Add(dayLen / 2)
	case atAfternoon:
		return t.Rise.Add(dayLen * 7 / 10)
	case atEvening:
		return t.Set.Add(45 * time.Minute)
	default:
		return t.Set.Add((24*time.Hour - dayLen) / 2)
	}
}

// tithiDay finds the civil day within a lunar month on which the tithi prevails at the day point.
// If it is skipped at that time (kshaya), the civil day it ends on is used, so
// a festival is never dropped.
func tithiDay(m lunarMonth, tithiNo int, p dayPoint, o Options) (time.Time, bool) {
	zone := o.zone()
	first := m.start.In(zone)
	d := date(first.Year(), first.Month(), first.Day()).AddDate(0, 0, -1)
	last := m.end.In(zone)
	stop := date(last.Year(), last.Month(), last.Day()).AddDate(0, 0, 1)
	for ; !d.After(stop); d = d.AddDate(0, 0, 1) {
		at := pointOn(d, p, o)
		if at.Before(m.start) || at.After(m.end) {
			continue
		}
		if tithi(at) == tithiNo {
			return d, true
		}
	}
	end := tithiEnd(m, tithiNo)
	l := end.In(zone)
	return date(l.Year(), l.Month(), l.Day()), true
}

// tithiEnd is the moment the tithi ends within the lunar month: the first
// moment a later one is at hand, or the end of the month for the last tithi.
func tithiEnd(m lunarMonth, tithiNo int) time.Time {
	if tithiNo >= 30 {
		return m.end
	}
	lo, hi := m.start, m.end
	for hi.Sub(lo) > time.Second {
		mid := lo.Add(hi.Sub(lo) / 2)
		if tithi(mid) > tithiNo {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi
}

// festival describes a Hindu festival by month, lunar day and day point.
type festival struct {
	key   string
	month int // 0 Chaitra .. 11 Phalguna
	tithi int // 1..30
	at    dayPoint
	days  int // 0 or 1: one day; more: from the day found
	next  int // days added to the day found (Holi follows Holika Dahan)
}

var hinduFestivals = []festival{
	{"maha_shivaratri", 10, 29, atMidnight, 1, 0},
	{"holi", 11, 15, atEvening, 1, 1},
	{"rama_navami", 0, 9, atMidday, 1, 0},
	{"raksha_bandhan", 4, 15, atSunrise, 1, 0},
	{"janmashtami", 4, 23, atMidnight, 1, 0},
	{"ganesh_chaturthi", 5, 4, atMidday, 1, 0},
	{"navaratri", 6, 1, atSunrise, 9, 0},
	{"dussehra", 6, 10, atAfternoon, 1, 0},
	{"diwali", 6, 30, atEvening, 1, 0},
}

// hindu lists the festivals for the place of the household.
func hindu(o Options, from, to int) []Holiday {
	if o.Place == nil {
		return nil
	}
	var out []Holiday
	for _, m := range lunarMonths(from, to) {
		if m.adhika {
			continue
		}
		for _, f := range hinduFestivals {
			if f.month != m.index {
				continue
			}
			d, ok := tithiDay(m, f.tithi, f.at, o)
			if !ok {
				continue
			}
			d = d.AddDate(0, 0, f.next)
			end := d.AddDate(0, 0, f.days-1)
			out = append(out, Holiday{Key: f.key, Start: d, End: end, Source: SrcPanchang})
		}
	}
	for y := from; y <= to; y++ {
		if d, ok := makarSankranti(y, o); ok {
			out = append(out, Holiday{Key: "makar_sankranti", Start: d, End: d, Source: SrcPanchang})
		}
	}
	out = inYears(out, from, to)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// makarSankranti is the day the sun enters Capricorn in the sidereal zodiac; after sunset it counts for the next day.
func makarSankranti(y int, o Options) (time.Time, bool) {
	lo, hi := time.Date(y, 1, 10, 0, 0, 0, 0, time.UTC), time.Date(y, 1, 20, 0, 0, 0, 0, time.UTC)
	for hi.Sub(lo) > time.Minute {
		mid := lo.Add(hi.Sub(lo) / 2)
		if siderealSun(mid) >= 270 && siderealSun(mid) < 300 {
			hi = mid
		} else {
			lo = mid
		}
	}
	zone := o.zone()
	l := hi.In(zone)
	d := date(l.Year(), l.Month(), l.Day())
	set := sun.On(time.Date(l.Year(), l.Month(), l.Day(), 12, 0, 0, 0, zone), o.Place.Lat, o.Place.Lon)
	if set.Kind == sun.Normal && !l.Before(set.Set) {
		d = d.AddDate(0, 0, 1)
	}
	return d, true
}
