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
}

type rule struct {
	nameDE, nameEN string
	date           func(year int) time.Time
	// regions: "DE" = whole country, or region codes. Empty = never.
	regions []string
	from    int // first year the rule applies (0 = always)
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
	{"Neujahr", "New Year's Day", fixed(time.January, 1), []string{"DE", "AT"}, 0},
	{"Heilige Drei Könige", "Epiphany", fixed(time.January, 6), []string{"DE-BW", "DE-BY", "DE-ST", "AT"}, 0},
	{"Internationaler Frauentag", "International Women's Day", fixed(time.March, 8), []string{"DE-BE"}, 2019},
	{"Internationaler Frauentag", "International Women's Day", fixed(time.March, 8), []string{"DE-MV"}, 2023},
	{"Karfreitag", "Good Friday", easterOffset(-2), []string{"DE"}, 0},
	{"Ostersonntag", "Easter Sunday", easterOffset(0), []string{"DE-BB"}, 0},
	{"Ostermontag", "Easter Monday", easterOffset(1), []string{"DE", "AT"}, 0},
	{"Tag der Arbeit", "Labour Day", fixed(time.May, 1), []string{"DE", "AT"}, 0},
	{"Christi Himmelfahrt", "Ascension Day", easterOffset(39), []string{"DE", "AT"}, 0},
	{"Pfingstsonntag", "Whit Sunday", easterOffset(49), []string{"DE-BB"}, 0},
	{"Pfingstmontag", "Whit Monday", easterOffset(50), []string{"DE", "AT"}, 0},
	{"Fronleichnam", "Corpus Christi", easterOffset(60), []string{"DE-BW", "DE-BY", "DE-HE", "DE-NW", "DE-RP", "DE-SL", "AT"}, 0},
	{"Mariä Himmelfahrt", "Assumption Day", fixed(time.August, 15), []string{"DE-BY", "DE-SL", "AT"}, 0},
	{"Weltkindertag", "World Children's Day", fixed(time.September, 20), []string{"DE-TH"}, 2019},
	{"Tag der Deutschen Einheit", "German Unity Day", fixed(time.October, 3), []string{"DE"}, 0},
	{"Nationalfeiertag", "National Day", fixed(time.October, 26), []string{"AT"}, 0},
	{"Reformationstag", "Reformation Day", fixed(time.October, 31), []string{"DE-BB", "DE-MV", "DE-SN", "DE-ST", "DE-TH"}, 0},
	{"Reformationstag", "Reformation Day", fixed(time.October, 31), []string{"DE-HB", "DE-HH", "DE-NI", "DE-SH"}, 2018},
	{"Allerheiligen", "All Saints' Day", fixed(time.November, 1), []string{"DE-BW", "DE-BY", "DE-NW", "DE-RP", "DE-SL", "AT"}, 0},
	{"Buß- und Bettag", "Day of Repentance and Prayer", repentanceDay, []string{"DE-SN"}, 0},
	{"Mariä Empfängnis", "Immaculate Conception", fixed(time.December, 8), []string{"AT"}, 0},
	{"1. Weihnachtstag", "Christmas Day", fixed(time.December, 25), []string{"DE"}, 0},
	{"Christtag", "Christmas Day", fixed(time.December, 25), []string{"AT"}, 0},
	{"2. Weihnachtstag", "Boxing Day", fixed(time.December, 26), []string{"DE"}, 0},
	{"Stefanitag", "St Stephen's Day", fixed(time.December, 26), []string{"AT"}, 0},
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
		if year >= ru.from && applies(ru.regions, r) {
			out = append(out, Holiday{Date: ru.date(year), NameDE: ru.nameDE, NameEN: ru.nameEN})
		}
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
