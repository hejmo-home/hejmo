package religiousholiday

import "time"

// bahaiTable is the official Bahá'í calendar of the Bahá'í World Centre (bahai.org), edition 2026 (182-183 B.E.),
// the only year of it the box has checked (ticket 354). Dates are the Gregorian days the holy days fall on in
// that calendar; each begins at sunset of the evening before.
func bahaiTable() []keyed {
	x := func(key, iso string) keyed { return keyed{key, e(SrcBahai, iso, false)} }
	return []keyed{
		x("naw_ruz", "2026-03-21"), x("ridvan_first", "2026-04-21"), x("ridvan_ninth", "2026-04-29"), x("ridvan_twelfth", "2026-05-02"),
		x("declaration_bab", "2026-05-24"), x("ascension_bahaullah", "2026-05-29"), x("martyrdom_bab", "2026-07-10"),
		x("birth_bab", "2026-11-10"), x("birth_bahaullah", "2026-11-11"),
	}
}

func bahaiEntries() []Entry {
	var out []Entry
	for _, k := range bahaiTable() {
		out = append(out, k.Entry)
	}
	return out
}

// bahai lists the nine holy days of the official table, as far as it reaches.
func bahai(from, to int) []Holiday {
	var out []Holiday
	for _, k := range bahaiTable() {
		if y := k.Date.Year(); y >= from && y <= to {
			out = append(out, Holiday{Key: k.key, Start: k.Date, End: k.Date, Eve: true, Source: k.Source, Unchecked: k.Unchecked})
		}
	}
	return out
}

// keyed is a table entry with the key of the holiday it is the date of.
type keyed struct {
	key string
	Entry
}

// losarTable is empty: the dates entered from memory in ticket 339 could not be checked against the publisher
// (Men-Tsee-Khang) in ticket 354, and an entry that no source confirms is removed. Losar (three days from the
// first day) comes back with a checked table.
func losarTable() []keyed { return nil }

func losarEntries() []Entry {
	var out []Entry
	for _, k := range losarTable() {
		out = append(out, k.Entry)
	}
	return out
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

// sgpcTable is the Nanakshahi Jantri of the SGPC, Nanakshahi years 557 and 558 (14 March 2025 to 13 March 2027).
// Entries of 2027 after 13 March and of Guru Gobind Singh's Prakash Purab of 6 January 2025 are not in the two
// Jantris and are not in the table; Hola Mohalla 2026 is not listed in either (ticket 354).
func sgpcTable() []keyed {
	x := func(key, iso string) keyed { return keyed{key, e(SrcSGPC, iso, false)} }
	return []keyed{
		x("hola_mohalla", "2025-03-15"), x("vaisakhi", "2025-04-13"), x("bandi_chhor", "2025-10-21"), x("guru_nanak", "2025-11-05"), x("guru_gobind_singh", "2025-12-27"),
		x("vaisakhi", "2026-04-14"), x("bandi_chhor", "2026-11-08"), x("guru_nanak", "2026-11-24"),
		x("guru_gobind_singh", "2027-01-15"),
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
