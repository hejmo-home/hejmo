package religiousholiday

import (
	"time"

	"github.com/soniakeys/meeus/v3/solstice"
)

// tehran is the zone the Bahá'í calendar counts in. Iran has no daylight saving time since 2022.
var tehran = time.FixedZone("Tehran", 3*3600+1800)

// bahaiSunsetHour is the hour of sunset in Tehran around the equinox and the Twin Holy Birthdays.
const bahaiSunsetHour = 18

// The official table of the Bahá'í World Centre covers 2015 to 2064.
const bahaiFrom, bahaiTo = 2015, 2064

// bahaiDay is the date of an instant in Tehran, or of the next day when the instant is after sunset (the Bahá'í day changes at sunset).
func bahaiDay(t time.Time) time.Time {
	l := t.In(tehran)
	d := date(l.Year(), l.Month(), l.Day())
	if l.Hour() >= bahaiSunsetHour {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

// bahai computes the nine holy days of the Bahá'í calendar of 2015 on: Naw-Rúz
// is the day of the March equinox in Tehran, the other days are a fixed
// number of days after it and the Twin Holy Birthdays follow the eighth new
// moon after Naw-Rúz. The official table was not at hand, so the dates are
// Unchecked.
func bahai(from, to int) []Holiday {
	var out []Holiday
	for y := from; y <= to; y++ {
		if y < bahaiFrom || y > bahaiTo {
			continue
		}
		equinox := jdeTime(solstice.March(y))
		nr := bahaiDay(equinox)
		add := func(key string, t time.Time) {
			out = append(out, Holiday{Key: key, Start: t, End: t, Eve: true, Source: SrcBahai, Unchecked: true})
		}
		add("naw_ruz", nr)
		add("ridvan_first", nr.AddDate(0, 0, 31))
		add("ridvan_ninth", nr.AddDate(0, 0, 39))
		add("ridvan_twelfth", nr.AddDate(0, 0, 42))
		add("declaration_bab", nr.AddDate(0, 0, 63))
		add("ascension_bahaullah", nr.AddDate(0, 0, 69))
		add("martyrdom_bab", nr.AddDate(0, 0, 110))
		n := 0
		for _, m := range moonTimes(false, y, y) {
			if m.After(equinox) {
				if n++; n == 8 {
					// The Birth of the Báb is the first day after the eighth new moon; Bahá'u'lláh's birthday follows.
					// bahaiDay is the day the new moon falls in, or the next one after sunset; the birthday is the day after that.
					b := bahaiDay(m).AddDate(0, 0, 1)
					add("birth_bab", b)
					add("birth_bahaullah", b.AddDate(0, 0, 1))
					break
				}
			}
		}
	}
	return out
}

// losar is the Tibetan new year, a table of three days from the first day.
type keyed struct {
	key string
	Entry
}

func losarEntries() []Entry {
	var out []Entry
	for _, k := range losarTable() {
		out = append(out, k.Entry)
	}
	return out
}

func losarTable() []keyed {
	x := func(iso string) keyed { return keyed{"losar", e(SrcLosar, iso, true)} }
	return []keyed{x("2024-02-10"), x("2025-02-28"), x("2026-02-18"), x("2027-02-07"), x("2028-02-26"), x("2029-02-14")}
}

// buddhist lists Losar (a table), Vesak and Asalha Puja (the full moons of May and July as the German Buddhist Union counts them) and Bodhi Day.
func buddhist(o Options, from, to int) []Holiday {
	var out []Holiday
	for _, k := range losarTable() {
		if y := k.Date.Year(); y >= from && y <= to {
			out = append(out, Holiday{Key: k.key, Start: k.Date, End: k.Date.AddDate(0, 0, 2), Source: k.Source, Unchecked: k.Unchecked})
		}
	}
	zone := o.zone()
	full := moonTimes(true, from, to)
	for y := from; y <= to; y++ {
		first := func(m time.Month) (time.Time, bool) {
			for _, f := range full {
				if l := f.In(zone); l.Year() == y && l.Month() == m {
					return date(y, m, l.Day()), true
				}
			}
			return time.Time{}, false
		}
		if t, ok := first(time.May); ok {
			out = append(out, Holiday{Key: "vesak", Start: t, End: t, Source: SrcMoon})
		}
		if t, ok := first(time.July); ok {
			out = append(out, Holiday{Key: "asalha_puja", Start: t, End: t, Source: SrcMoon})
		}
		out = append(out, Holiday{Key: "bodhi_day", Start: date(y, time.December, 8), End: date(y, time.December, 8), Source: SrcFixed})
	}
	return out
}

func sgpcTable() []keyed {
	x := func(key, iso string) keyed { return keyed{key, e(SrcSGPC, iso, true)} }
	return []keyed{
		x("guru_gobind_singh", "2025-01-06"), x("hola_mohalla", "2025-03-14"), x("vaisakhi", "2025-04-14"), x("bandi_chhor", "2025-10-20"), x("guru_nanak", "2025-11-05"), x("guru_gobind_singh", "2025-12-27"),
		x("hola_mohalla", "2026-03-04"), x("vaisakhi", "2026-04-14"), x("bandi_chhor", "2026-11-08"), x("guru_nanak", "2026-11-24"),
		x("guru_gobind_singh", "2027-01-15"), x("hola_mohalla", "2027-03-23"), x("vaisakhi", "2027-04-14"), x("bandi_chhor", "2027-10-29"), x("guru_nanak", "2027-11-14"),
	}
}

func sgpcEntries() []Entry {
	var out []Entry
	for _, k := range sgpcTable() {
		out = append(out, k.Entry)
	}
	return out
}

// sikh lists the days of the SGPC table, as far as it reaches.
func sikh(from, to int) []Holiday {
	var out []Holiday
	for _, k := range sgpcTable() {
		if y := k.Date.Year(); y >= from && y <= to {
			end := k.Date
			if k.key == "hola_mohalla" {
				end = k.Date.AddDate(0, 0, 2) // three days at Anandpur Sahib
			}
			out = append(out, Holiday{Key: k.key, Start: k.Date, End: end, Source: k.Source, Unchecked: k.Unchecked})
		}
	}
	return out
}
