package religiousholiday

import (
	"time"

	"github.com/soniakeys/meeus/v3/easter"
)

// gregorianEaster is Easter Sunday of the Western churches.
func gregorianEaster(y int) time.Time {
	m, d := easter.Gregorian(y)
	return date(y, time.Month(m), d)
}

// orthodoxEaster is Easter Sunday of the Orthodox churches: the Julian
// computus, written in the Gregorian calendar (the two differ by 13 days from
// 1900 to 2099).
func orthodoxEaster(y int) time.Time {
	m, d := easter.Julian(y)
	return date(y, time.Month(m), d).AddDate(0, 0, 13)
}

// sundayOnOrAfter is the first Sunday on or after t.
func sundayOnOrAfter(t time.Time) time.Time {
	return t.AddDate(0, 0, (7-int(t.Weekday()))%7)
}

// christian lists the days of the church year of the Catholic and Protestant churches. Days
// that are public holidays are in the list too; the caller drops the ones the
// household's region has already (see Skip).
func christian(from, to int) []Holiday {
	var out []Holiday
	for y := from; y <= to; y++ {
		e := gregorianEaster(y)
		add := func(key string, t time.Time) { out = append(out, one(key, t, SrcChurch)) }
		// Advent: four Sundays before Christmas, the first one between 27 November and 3 December.
		advent1 := sundayOnOrAfter(date(y, time.November, 27))
		for i := 0; i < 4; i++ {
			add("advent"+string(rune('1'+i)), advent1.AddDate(0, 0, 7*i))
		}
		add("epiphany", date(y, time.January, 6))
		add("ash_wednesday", e.AddDate(0, 0, -46))
		add("palm_sunday", e.AddDate(0, 0, -7))
		add("maundy_thursday", e.AddDate(0, 0, -3))
		add("good_friday", e.AddDate(0, 0, -2))
		add("easter", e)
		add("easter_monday", e.AddDate(0, 0, 1))
		add("ascension", e.AddDate(0, 0, 39))
		add("pentecost", e.AddDate(0, 0, 49))
		add("whit_monday", e.AddDate(0, 0, 50))
		add("corpus_christi", e.AddDate(0, 0, 60))
		add("assumption", date(y, time.August, 15))
		add("harvest_thanks", sundayOnOrAfter(date(y, time.October, 1)))
		add("reformation", date(y, time.October, 31))
		add("all_saints", date(y, time.November, 1))
		add("st_martin", date(y, time.November, 11))
		rep := date(y, time.November, 22) // Wednesday before 23 November
		for rep.Weekday() != time.Wednesday {
			rep = rep.AddDate(0, 0, -1)
		}
		add("repentance_day", rep)
		add("saint_nicholas", date(y, time.December, 6))
		add("christmas_eve", date(y, time.December, 24))
		out = append(out, span("christmas_days", date(y, time.December, 25), date(y, time.December, 26), SrcChurch))
	}
	return out
}

// orthodox lists the main days of the Orthodox churches. Christmas and
// Theophany follow the chosen calendar; Easter and what depends on it always
// follow the Orthodox Easter.
func orthodox(o Options, from, to int) []Holiday {
	var out []Holiday
	for y := from; y <= to; y++ {
		e := orthodoxEaster(y)
		xmas, theo := date(y, time.December, 25), date(y, time.January, 6)
		if o.orthodox() == Julian {
			xmas, theo = date(y, time.January, 7), date(y, time.January, 19)
		}
		out = append(out,
			one("christmas", xmas, SrcOrthodox),
			one("theophany", theo, SrcOrthodox),
			one("good_friday", e.AddDate(0, 0, -2), SrcOrthodox),
			one("easter", e, SrcOrthodox),
			one("pentecost", e.AddDate(0, 0, 49), SrcOrthodox),
		)
	}
	return out
}
