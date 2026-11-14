// Package holiday computes public holidays for the DACH regions (ISO 3166-2).
// Only statutory, region-wide holidays count; days observed in single
// municipalities (for example Augsburg's peace festival) are left out.
package holiday

import (
	"sort"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/region"
)

// Holiday is a public holiday on a calendar date (time part is zero, UTC).
type Holiday struct {
	Date   time.Time
	NameDE string
	NameEN string
	// Partial: a holiday in most, but not all, municipalities of the region.
	Partial bool
}

type rule struct {
	nameDE, nameEN string
	date           func(year int) time.Time
	// regions: "DE" = whole country, or region codes. Empty = never.
	regions  []string
	from, to int                  // years the rule applies (0 = open)
	when     func(time.Time) bool // optional condition on the date
	partial  bool
}

func fixed(month time.Month, d int) func(int) time.Time {
	return func(y int) time.Time { return time.Date(y, month, d, 0, 0, 0, 0, time.UTC) }
}

func easterOffset(days int) func(int) time.Time {
	return func(y int) time.Time { return Easter(y).AddDate(0, 0, days) }
}

// Easter returns Easter Sunday (Gregorian calendar, anonymous algorithm).
func Easter(y int) time.Time {
	a, b, c := y%19, y/100, y%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return time.Date(y, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

// repentanceDay is the Wednesday before 23 November (Buß- und Bettag).
func repentanceDay(y int) time.Time {
	d := time.Date(y, time.November, 22, 0, 0, 0, 0, time.UTC)
	for d.Weekday() != time.Wednesday {
		d = d.AddDate(0, 0, -1)
	}
	return d
}

var rules = []rule{
	{nameDE: "Neujahr", nameEN: "New Year's Day", date: fixed(time.January, 1), regions: []string{"DE", "AT"}},
	{nameDE: "Heilige Drei Könige", nameEN: "Epiphany", date: fixed(time.January, 6), regions: []string{"DE-BW", "DE-BY", "DE-ST", "AT"}},
	{nameDE: "Internationaler Frauentag", nameEN: "International Women's Day", date: fixed(time.March, 8), regions: []string{"DE-BE"}, from: 2019},
	{nameDE: "Internationaler Frauentag", nameEN: "International Women's Day", date: fixed(time.March, 8), regions: []string{"DE-MV"}, from: 2023},
	{nameDE: "Karfreitag", nameEN: "Good Friday", date: easterOffset(-2), regions: []string{"DE"}},
	{nameDE: "Ostersonntag", nameEN: "Easter Sunday", date: easterOffset(0), regions: []string{"DE-BB"}},
	{nameDE: "Ostermontag", nameEN: "Easter Monday", date: easterOffset(1), regions: []string{"DE", "AT"}},
	{nameDE: "Tag der Arbeit", nameEN: "Labour Day", date: fixed(time.May, 1), regions: []string{"DE", "AT"}},
	{nameDE: "Christi Himmelfahrt", nameEN: "Ascension Day", date: easterOffset(39), regions: []string{"DE", "AT"}},
	{nameDE: "Pfingstsonntag", nameEN: "Whit Sunday", date: easterOffset(49), regions: []string{"DE-BB"}},
	{nameDE: "Pfingstmontag", nameEN: "Whit Monday", date: easterOffset(50), regions: []string{"DE", "AT"}},
	{nameDE: "Fronleichnam", nameEN: "Corpus Christi", date: easterOffset(60), regions: []string{"DE-BW", "DE-BY", "DE-HE", "DE-NW", "DE-RP", "DE-SL", "AT"}},
	{nameDE: "Mariä Himmelfahrt", nameEN: "Assumption Day", date: fixed(time.August, 15), regions: []string{"DE-SL", "AT"}},
	// Bavaria: only in municipalities with a Catholic majority (1,708 of 2,056).
	{nameDE: "Mariä Himmelfahrt", nameEN: "Assumption Day", date: fixed(time.August, 15), regions: []string{"DE-BY"}, partial: true},
	// Berlin: one-off holidays (law of 10.7.2024).
	{nameDE: "Jahrestag des Volksaufstands vom 17. Juni 1953", nameEN: "75th anniversary of the 1953 uprising", date: fixed(time.June, 17), regions: []string{"DE-BE"}, from: 2028, to: 2028},
	{nameDE: "Weltkindertag", nameEN: "World Children's Day", date: fixed(time.September, 20), regions: []string{"DE-TH"}, from: 2019},
	{nameDE: "Tag der Deutschen Einheit", nameEN: "German Unity Day", date: fixed(time.October, 3), regions: []string{"DE"}},
	{nameDE: "Nationalfeiertag", nameEN: "National Day", date: fixed(time.October, 26), regions: []string{"AT"}},
	{nameDE: "Reformationstag", nameEN: "Reformation Day", date: fixed(time.October, 31), regions: []string{"DE-BB", "DE-MV", "DE-SN", "DE-ST", "DE-TH"}},
	{nameDE: "Reformationstag", nameEN: "Reformation Day", date: fixed(time.October, 31), regions: []string{"DE-HB", "DE-HH", "DE-NI", "DE-SH"}, from: 2018},
	{nameDE: "Allerheiligen", nameEN: "All Saints' Day", date: fixed(time.November, 1), regions: []string{"DE-BW", "DE-BY", "DE-NW", "DE-RP", "DE-SL", "AT"}},
	{nameDE: "Buß- und Bettag", nameEN: "Day of Repentance and Prayer", date: repentanceDay, regions: []string{"DE-SN"}},
	{nameDE: "Mariä Empfängnis", nameEN: "Immaculate Conception", date: fixed(time.December, 8), regions: []string{"AT"}},
	{nameDE: "1. Weihnachtstag", nameEN: "Christmas Day", date: fixed(time.December, 25), regions: []string{"DE"}},
	{nameDE: "Christtag", nameEN: "Christmas Day", date: fixed(time.December, 25), regions: []string{"AT"}},
	{nameDE: "2. Weihnachtstag", nameEN: "Boxing Day", date: fixed(time.December, 26), regions: []string{"DE"}},
	{nameDE: "Stefanitag", nameEN: "St Stephen's Day", date: fixed(time.December, 26), regions: []string{"AT"}},
}

// For returns the holidays of a region in a year, sorted by date. Unknown
// regions return nil.
func For(code string, year int) []Holiday {
	r, ok := region.Lookup(code)
	if !ok {
		return nil
	}
	var out []Holiday
	for _, ru := range append(rules, swissRules...) {
		if year < ru.from || (ru.to != 0 && year > ru.to) || !applies(ru.regions, r) {
			continue
		}
		d := ru.date(year)
		if ru.when != nil && !ru.when(d) {
			continue
		}
		out = append(out, Holiday{Date: d, NameDE: ru.nameDE, NameEN: ru.nameEN, Partial: ru.partial})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.Before(out[j].Date) })
	return out
}

// Between returns holidays with from <= date < to (dates compared by day).
func Between(code string, from, to time.Time) []Holiday {
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	end := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
	var out []Holiday
	for y := start.Year(); y <= end.Year(); y++ {
		for _, h := range For(code, y) {
			if !h.Date.Before(start) && h.Date.Before(end) {
				out = append(out, h)
			}
		}
	}
	return out
}

func applies(regions []string, r region.Region) bool {
	for _, x := range regions {
		if x == r.Code || x == r.Country || (strings.HasSuffix(x, "*") && strings.HasPrefix(r.Code, strings.TrimSuffix(x, "*"))) {
			return true
		}
	}
	return false
}
