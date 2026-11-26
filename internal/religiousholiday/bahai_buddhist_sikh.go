package religiousholiday

import "time"

// bahaiTable is the table of the Universal House of Justice of the Bahá'í World Centre for 172-221 B.E. (21 March
// 2015 to 20 March 2065; copy at bahai-library.com), every entry compared with it and its 2026 row with the
// official calendar 2026 of bahai.org (ticket 354). Each year lists, in the order of the calendar year, Naw-Rúz,
// the first, ninth and twelfth day of Riḍván, the Declaration of the Báb, the Ascension of Bahá'u'lláh, the
// Martyrdom of the Báb and the two Twin Holy Birthdays (Báb, Bahá'u'lláh). Dates are the Gregorian days the
// holy days fall on; each begins at sunset of the evening before.
func bahaiTable() []keyed {
	x := func(key, iso string) keyed { return keyed{key, e(SrcBahai, iso, false)} }
	return []keyed{
		x("naw_ruz", "2015-03-21"), x("ridvan_first", "2015-04-21"), x("ridvan_ninth", "2015-04-29"), x("ridvan_twelfth", "2015-05-02"), x("declaration_bab", "2015-05-24"), x("ascension_bahaullah", "2015-05-29"), x("martyrdom_bab", "2015-07-10"), x("birth_bab", "2015-11-13"), x("birth_bahaullah", "2015-11-14"),
		x("naw_ruz", "2016-03-20"), x("ridvan_first", "2016-04-20"), x("ridvan_ninth", "2016-04-28"), x("ridvan_twelfth", "2016-05-01"), x("declaration_bab", "2016-05-23"), x("ascension_bahaullah", "2016-05-28"), x("martyrdom_bab", "2016-07-09"), x("birth_bab", "2016-11-01"), x("birth_bahaullah", "2016-11-02"),
		x("naw_ruz", "2017-03-20"), x("ridvan_first", "2017-04-20"), x("ridvan_ninth", "2017-04-28"), x("ridvan_twelfth", "2017-05-01"), x("declaration_bab", "2017-05-23"), x("ascension_bahaullah", "2017-05-28"), x("martyrdom_bab", "2017-07-09"), x("birth_bab", "2017-10-21"), x("birth_bahaullah", "2017-10-22"),
		x("naw_ruz", "2018-03-21"), x("ridvan_first", "2018-04-21"), x("ridvan_ninth", "2018-04-29"), x("ridvan_twelfth", "2018-05-02"), x("declaration_bab", "2018-05-24"), x("ascension_bahaullah", "2018-05-29"), x("martyrdom_bab", "2018-07-10"), x("birth_bab", "2018-11-09"), x("birth_bahaullah", "2018-11-10"),
		x("naw_ruz", "2019-03-21"), x("ridvan_first", "2019-04-21"), x("ridvan_ninth", "2019-04-29"), x("ridvan_twelfth", "2019-05-02"), x("declaration_bab", "2019-05-24"), x("ascension_bahaullah", "2019-05-29"), x("martyrdom_bab", "2019-07-10"), x("birth_bab", "2019-10-29"), x("birth_bahaullah", "2019-10-30"),
		x("naw_ruz", "2020-03-20"), x("ridvan_first", "2020-04-20"), x("ridvan_ninth", "2020-04-28"), x("ridvan_twelfth", "2020-05-01"), x("declaration_bab", "2020-05-23"), x("ascension_bahaullah", "2020-05-28"), x("martyrdom_bab", "2020-07-09"), x("birth_bab", "2020-10-18"), x("birth_bahaullah", "2020-10-19"),
		x("naw_ruz", "2021-03-20"), x("ridvan_first", "2021-04-20"), x("ridvan_ninth", "2021-04-28"), x("ridvan_twelfth", "2021-05-01"), x("declaration_bab", "2021-05-23"), x("ascension_bahaullah", "2021-05-28"), x("martyrdom_bab", "2021-07-09"), x("birth_bab", "2021-11-06"), x("birth_bahaullah", "2021-11-07"),
		x("naw_ruz", "2022-03-21"), x("ridvan_first", "2022-04-21"), x("ridvan_ninth", "2022-04-29"), x("ridvan_twelfth", "2022-05-02"), x("declaration_bab", "2022-05-24"), x("ascension_bahaullah", "2022-05-29"), x("martyrdom_bab", "2022-07-10"), x("birth_bab", "2022-10-26"), x("birth_bahaullah", "2022-10-27"),
		x("naw_ruz", "2023-03-21"), x("ridvan_first", "2023-04-21"), x("ridvan_ninth", "2023-04-29"), x("ridvan_twelfth", "2023-05-02"), x("declaration_bab", "2023-05-24"), x("ascension_bahaullah", "2023-05-29"), x("martyrdom_bab", "2023-07-10"), x("birth_bab", "2023-10-16"), x("birth_bahaullah", "2023-10-17"),
		x("naw_ruz", "2024-03-20"), x("ridvan_first", "2024-04-20"), x("ridvan_ninth", "2024-04-28"), x("ridvan_twelfth", "2024-05-01"), x("declaration_bab", "2024-05-23"), x("ascension_bahaullah", "2024-05-28"), x("martyrdom_bab", "2024-07-09"), x("birth_bab", "2024-11-02"), x("birth_bahaullah", "2024-11-03"),
		x("naw_ruz", "2025-03-20"), x("ridvan_first", "2025-04-20"), x("ridvan_ninth", "2025-04-28"), x("ridvan_twelfth", "2025-05-01"), x("declaration_bab", "2025-05-23"), x("ascension_bahaullah", "2025-05-28"), x("martyrdom_bab", "2025-07-09"), x("birth_bab", "2025-10-22"), x("birth_bahaullah", "2025-10-23"),
		x("naw_ruz", "2026-03-21"), x("ridvan_first", "2026-04-21"), x("ridvan_ninth", "2026-04-29"), x("ridvan_twelfth", "2026-05-02"), x("declaration_bab", "2026-05-24"), x("ascension_bahaullah", "2026-05-29"), x("martyrdom_bab", "2026-07-10"), x("birth_bab", "2026-11-10"), x("birth_bahaullah", "2026-11-11"),
		x("naw_ruz", "2027-03-21"), x("ridvan_first", "2027-04-21"), x("ridvan_ninth", "2027-04-29"), x("ridvan_twelfth", "2027-05-02"), x("declaration_bab", "2027-05-24"), x("ascension_bahaullah", "2027-05-29"), x("martyrdom_bab", "2027-07-10"), x("birth_bab", "2027-10-30"), x("birth_bahaullah", "2027-10-31"),
		x("naw_ruz", "2028-03-20"), x("ridvan_first", "2028-04-20"), x("ridvan_ninth", "2028-04-28"), x("ridvan_twelfth", "2028-05-01"), x("declaration_bab", "2028-05-23"), x("ascension_bahaullah", "2028-05-28"), x("martyrdom_bab", "2028-07-09"), x("birth_bab", "2028-10-19"), x("birth_bahaullah", "2028-10-20"),
		x("naw_ruz", "2029-03-20"), x("ridvan_first", "2029-04-20"), x("ridvan_ninth", "2029-04-28"), x("ridvan_twelfth", "2029-05-01"), x("declaration_bab", "2029-05-23"), x("ascension_bahaullah", "2029-05-28"), x("martyrdom_bab", "2029-07-09"), x("birth_bab", "2029-11-07"), x("birth_bahaullah", "2029-11-08"),
		x("naw_ruz", "2030-03-20"), x("ridvan_first", "2030-04-20"), x("ridvan_ninth", "2030-04-28"), x("ridvan_twelfth", "2030-05-01"), x("declaration_bab", "2030-05-23"), x("ascension_bahaullah", "2030-05-28"), x("martyrdom_bab", "2030-07-09"), x("birth_bab", "2030-10-28"), x("birth_bahaullah", "2030-10-29"),
		x("naw_ruz", "2031-03-21"), x("ridvan_first", "2031-04-21"), x("ridvan_ninth", "2031-04-29"), x("ridvan_twelfth", "2031-05-02"), x("declaration_bab", "2031-05-24"), x("ascension_bahaullah", "2031-05-29"), x("martyrdom_bab", "2031-07-10"), x("birth_bab", "2031-10-17"), x("birth_bahaullah", "2031-10-18"),
		x("naw_ruz", "2032-03-20"), x("ridvan_first", "2032-04-20"), x("ridvan_ninth", "2032-04-28"), x("ridvan_twelfth", "2032-05-01"), x("declaration_bab", "2032-05-23"), x("ascension_bahaullah", "2032-05-28"), x("martyrdom_bab", "2032-07-09"), x("birth_bab", "2032-11-04"), x("birth_bahaullah", "2032-11-05"),
		x("naw_ruz", "2033-03-20"), x("ridvan_first", "2033-04-20"), x("ridvan_ninth", "2033-04-28"), x("ridvan_twelfth", "2033-05-01"), x("declaration_bab", "2033-05-23"), x("ascension_bahaullah", "2033-05-28"), x("martyrdom_bab", "2033-07-09"), x("birth_bab", "2033-10-24"), x("birth_bahaullah", "2033-10-25"),
		x("naw_ruz", "2034-03-20"), x("ridvan_first", "2034-04-20"), x("ridvan_ninth", "2034-04-28"), x("ridvan_twelfth", "2034-05-01"), x("declaration_bab", "2034-05-23"), x("ascension_bahaullah", "2034-05-28"), x("martyrdom_bab", "2034-07-09"), x("birth_bab", "2034-11-12"), x("birth_bahaullah", "2034-11-13"),
		x("naw_ruz", "2035-03-21"), x("ridvan_first", "2035-04-21"), x("ridvan_ninth", "2035-04-29"), x("ridvan_twelfth", "2035-05-02"), x("declaration_bab", "2035-05-24"), x("ascension_bahaullah", "2035-05-29"), x("martyrdom_bab", "2035-07-10"), x("birth_bab", "2035-11-01"), x("birth_bahaullah", "2035-11-02"),
		x("naw_ruz", "2036-03-20"), x("ridvan_first", "2036-04-20"), x("ridvan_ninth", "2036-04-28"), x("ridvan_twelfth", "2036-05-01"), x("declaration_bab", "2036-05-23"), x("ascension_bahaullah", "2036-05-28"), x("martyrdom_bab", "2036-07-09"), x("birth_bab", "2036-10-20"), x("birth_bahaullah", "2036-10-21"),
		x("naw_ruz", "2037-03-20"), x("ridvan_first", "2037-04-20"), x("ridvan_ninth", "2037-04-28"), x("ridvan_twelfth", "2037-05-01"), x("declaration_bab", "2037-05-23"), x("ascension_bahaullah", "2037-05-28"), x("martyrdom_bab", "2037-07-09"), x("birth_bab", "2037-11-08"), x("birth_bahaullah", "2037-11-09"),
		x("naw_ruz", "2038-03-20"), x("ridvan_first", "2038-04-20"), x("ridvan_ninth", "2038-04-28"), x("ridvan_twelfth", "2038-05-01"), x("declaration_bab", "2038-05-23"), x("ascension_bahaullah", "2038-05-28"), x("martyrdom_bab", "2038-07-09"), x("birth_bab", "2038-10-29"), x("birth_bahaullah", "2038-10-30"),
		x("naw_ruz", "2039-03-21"), x("ridvan_first", "2039-04-21"), x("ridvan_ninth", "2039-04-29"), x("ridvan_twelfth", "2039-05-02"), x("declaration_bab", "2039-05-24"), x("ascension_bahaullah", "2039-05-29"), x("martyrdom_bab", "2039-07-10"), x("birth_bab", "2039-10-19"), x("birth_bahaullah", "2039-10-20"),
		x("naw_ruz", "2040-03-20"), x("ridvan_first", "2040-04-20"), x("ridvan_ninth", "2040-04-28"), x("ridvan_twelfth", "2040-05-01"), x("declaration_bab", "2040-05-23"), x("ascension_bahaullah", "2040-05-28"), x("martyrdom_bab", "2040-07-09"), x("birth_bab", "2040-11-06"), x("birth_bahaullah", "2040-11-07"),
		x("naw_ruz", "2041-03-20"), x("ridvan_first", "2041-04-20"), x("ridvan_ninth", "2041-04-28"), x("ridvan_twelfth", "2041-05-01"), x("declaration_bab", "2041-05-23"), x("ascension_bahaullah", "2041-05-28"), x("martyrdom_bab", "2041-07-09"), x("birth_bab", "2041-10-26"), x("birth_bahaullah", "2041-10-27"),
		x("naw_ruz", "2042-03-20"), x("ridvan_first", "2042-04-20"), x("ridvan_ninth", "2042-04-28"), x("ridvan_twelfth", "2042-05-01"), x("declaration_bab", "2042-05-23"), x("ascension_bahaullah", "2042-05-28"), x("martyrdom_bab", "2042-07-09"), x("birth_bab", "2042-10-15"), x("birth_bahaullah", "2042-10-16"),
		x("naw_ruz", "2043-03-21"), x("ridvan_first", "2043-04-21"), x("ridvan_ninth", "2043-04-29"), x("ridvan_twelfth", "2043-05-02"), x("declaration_bab", "2043-05-24"), x("ascension_bahaullah", "2043-05-29"), x("martyrdom_bab", "2043-07-10"), x("birth_bab", "2043-11-03"), x("birth_bahaullah", "2043-11-04"),
		x("naw_ruz", "2044-03-20"), x("ridvan_first", "2044-04-20"), x("ridvan_ninth", "2044-04-28"), x("ridvan_twelfth", "2044-05-01"), x("declaration_bab", "2044-05-23"), x("ascension_bahaullah", "2044-05-28"), x("martyrdom_bab", "2044-07-09"), x("birth_bab", "2044-10-22"), x("birth_bahaullah", "2044-10-23"),
		x("naw_ruz", "2045-03-20"), x("ridvan_first", "2045-04-20"), x("ridvan_ninth", "2045-04-28"), x("ridvan_twelfth", "2045-05-01"), x("declaration_bab", "2045-05-23"), x("ascension_bahaullah", "2045-05-28"), x("martyrdom_bab", "2045-07-09"), x("birth_bab", "2045-11-10"), x("birth_bahaullah", "2045-11-11"),
		x("naw_ruz", "2046-03-20"), x("ridvan_first", "2046-04-20"), x("ridvan_ninth", "2046-04-28"), x("ridvan_twelfth", "2046-05-01"), x("declaration_bab", "2046-05-23"), x("ascension_bahaullah", "2046-05-28"), x("martyrdom_bab", "2046-07-09"), x("birth_bab", "2046-10-30"), x("birth_bahaullah", "2046-10-31"),
		x("naw_ruz", "2047-03-21"), x("ridvan_first", "2047-04-21"), x("ridvan_ninth", "2047-04-29"), x("ridvan_twelfth", "2047-05-02"), x("declaration_bab", "2047-05-24"), x("ascension_bahaullah", "2047-05-29"), x("martyrdom_bab", "2047-07-10"), x("birth_bab", "2047-10-20"), x("birth_bahaullah", "2047-10-21"),
		x("naw_ruz", "2048-03-20"), x("ridvan_first", "2048-04-20"), x("ridvan_ninth", "2048-04-28"), x("ridvan_twelfth", "2048-05-01"), x("declaration_bab", "2048-05-23"), x("ascension_bahaullah", "2048-05-28"), x("martyrdom_bab", "2048-07-09"), x("birth_bab", "2048-11-07"), x("birth_bahaullah", "2048-11-08"),
		x("naw_ruz", "2049-03-20"), x("ridvan_first", "2049-04-20"), x("ridvan_ninth", "2049-04-28"), x("ridvan_twelfth", "2049-05-01"), x("declaration_bab", "2049-05-23"), x("ascension_bahaullah", "2049-05-28"), x("martyrdom_bab", "2049-07-09"), x("birth_bab", "2049-10-28"), x("birth_bahaullah", "2049-10-29"),
		x("naw_ruz", "2050-03-20"), x("ridvan_first", "2050-04-20"), x("ridvan_ninth", "2050-04-28"), x("ridvan_twelfth", "2050-05-01"), x("declaration_bab", "2050-05-23"), x("ascension_bahaullah", "2050-05-28"), x("martyrdom_bab", "2050-07-09"), x("birth_bab", "2050-10-17"), x("birth_bahaullah", "2050-10-18"),
		x("naw_ruz", "2051-03-21"), x("ridvan_first", "2051-04-21"), x("ridvan_ninth", "2051-04-29"), x("ridvan_twelfth", "2051-05-02"), x("declaration_bab", "2051-05-24"), x("ascension_bahaullah", "2051-05-29"), x("martyrdom_bab", "2051-07-10"), x("birth_bab", "2051-11-05"), x("birth_bahaullah", "2051-11-06"),
		x("naw_ruz", "2052-03-20"), x("ridvan_first", "2052-04-20"), x("ridvan_ninth", "2052-04-28"), x("ridvan_twelfth", "2052-05-01"), x("declaration_bab", "2052-05-23"), x("ascension_bahaullah", "2052-05-28"), x("martyrdom_bab", "2052-07-09"), x("birth_bab", "2052-10-24"), x("birth_bahaullah", "2052-10-25"),
		x("naw_ruz", "2053-03-20"), x("ridvan_first", "2053-04-20"), x("ridvan_ninth", "2053-04-28"), x("ridvan_twelfth", "2053-05-01"), x("declaration_bab", "2053-05-23"), x("ascension_bahaullah", "2053-05-28"), x("martyrdom_bab", "2053-07-09"), x("birth_bab", "2053-11-11"), x("birth_bahaullah", "2053-11-12"),
		x("naw_ruz", "2054-03-20"), x("ridvan_first", "2054-04-20"), x("ridvan_ninth", "2054-04-28"), x("ridvan_twelfth", "2054-05-01"), x("declaration_bab", "2054-05-23"), x("ascension_bahaullah", "2054-05-28"), x("martyrdom_bab", "2054-07-09"), x("birth_bab", "2054-11-01"), x("birth_bahaullah", "2054-11-02"),
		x("naw_ruz", "2055-03-21"), x("ridvan_first", "2055-04-21"), x("ridvan_ninth", "2055-04-29"), x("ridvan_twelfth", "2055-05-02"), x("declaration_bab", "2055-05-24"), x("ascension_bahaullah", "2055-05-29"), x("martyrdom_bab", "2055-07-10"), x("birth_bab", "2055-10-21"), x("birth_bahaullah", "2055-10-22"),
		x("naw_ruz", "2056-03-20"), x("ridvan_first", "2056-04-20"), x("ridvan_ninth", "2056-04-28"), x("ridvan_twelfth", "2056-05-01"), x("declaration_bab", "2056-05-23"), x("ascension_bahaullah", "2056-05-28"), x("martyrdom_bab", "2056-07-09"), x("birth_bab", "2056-11-08"), x("birth_bahaullah", "2056-11-09"),
		x("naw_ruz", "2057-03-20"), x("ridvan_first", "2057-04-20"), x("ridvan_ninth", "2057-04-28"), x("ridvan_twelfth", "2057-05-01"), x("declaration_bab", "2057-05-23"), x("ascension_bahaullah", "2057-05-28"), x("martyrdom_bab", "2057-07-09"), x("birth_bab", "2057-10-29"), x("birth_bahaullah", "2057-10-30"),
		x("naw_ruz", "2058-03-20"), x("ridvan_first", "2058-04-20"), x("ridvan_ninth", "2058-04-28"), x("ridvan_twelfth", "2058-05-01"), x("declaration_bab", "2058-05-23"), x("ascension_bahaullah", "2058-05-28"), x("martyrdom_bab", "2058-07-09"), x("birth_bab", "2058-10-18"), x("birth_bahaullah", "2058-10-19"),
		x("naw_ruz", "2059-03-20"), x("ridvan_first", "2059-04-20"), x("ridvan_ninth", "2059-04-28"), x("ridvan_twelfth", "2059-05-01"), x("declaration_bab", "2059-05-23"), x("ascension_bahaullah", "2059-05-28"), x("martyrdom_bab", "2059-07-09"), x("birth_bab", "2059-11-06"), x("birth_bahaullah", "2059-11-07"),
		x("naw_ruz", "2060-03-20"), x("ridvan_first", "2060-04-20"), x("ridvan_ninth", "2060-04-28"), x("ridvan_twelfth", "2060-05-01"), x("declaration_bab", "2060-05-23"), x("ascension_bahaullah", "2060-05-28"), x("martyrdom_bab", "2060-07-09"), x("birth_bab", "2060-10-25"), x("birth_bahaullah", "2060-10-26"),
		x("naw_ruz", "2061-03-20"), x("ridvan_first", "2061-04-20"), x("ridvan_ninth", "2061-04-28"), x("ridvan_twelfth", "2061-05-01"), x("declaration_bab", "2061-05-23"), x("ascension_bahaullah", "2061-05-28"), x("martyrdom_bab", "2061-07-09"), x("birth_bab", "2061-10-14"), x("birth_bahaullah", "2061-10-15"),
		x("naw_ruz", "2062-03-20"), x("ridvan_first", "2062-04-20"), x("ridvan_ninth", "2062-04-28"), x("ridvan_twelfth", "2062-05-01"), x("declaration_bab", "2062-05-23"), x("ascension_bahaullah", "2062-05-28"), x("martyrdom_bab", "2062-07-09"), x("birth_bab", "2062-11-02"), x("birth_bahaullah", "2062-11-03"),
		x("naw_ruz", "2063-03-20"), x("ridvan_first", "2063-04-20"), x("ridvan_ninth", "2063-04-28"), x("ridvan_twelfth", "2063-05-01"), x("declaration_bab", "2063-05-23"), x("ascension_bahaullah", "2063-05-28"), x("martyrdom_bab", "2063-07-09"), x("birth_bab", "2063-10-23"), x("birth_bahaullah", "2063-10-24"),
		x("naw_ruz", "2064-03-20"), x("ridvan_first", "2064-04-20"), x("ridvan_ninth", "2064-04-28"), x("ridvan_twelfth", "2064-05-01"), x("declaration_bab", "2064-05-23"), x("ascension_bahaullah", "2064-05-28"), x("martyrdom_bab", "2064-07-09"), x("birth_bab", "2064-11-10"), x("birth_bahaullah", "2064-11-11"),
	}
}

func bahaiEntries() []Entry {
	var out []Entry
	for _, k := range bahaiTable() {
		out = append(out, k.Entry)
	}
	return out
}

// bahai lists the nine holy days of each year of the table, 2015 to 2064.
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
