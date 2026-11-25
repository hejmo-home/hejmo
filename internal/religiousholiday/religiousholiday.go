// Package religiousholiday computes the main holidays of the religions a
// household can choose (ticket 339), offline, from a few libraries and small
// checked-in tables. It is pure: no database, no clock, no network.
//
// Libraries (ADR of the make-or-buy decision of 25/11/2026): hebcal-go
// (Hebrew calendar), go-hijri (Umm al-Qura) and meeus/v3 (sun, moon, Easter).
// Dates that come from a table carry its source and whether they were checked
// against it (Unchecked). Nothing here is ported from Calendrical Calculations.
//
// A holiday is one entry for the whole festival: Ramadan, Pessach (8 days) and
// Opferfest (4 days) are one Holiday with Start and End.
package religiousholiday

import (
	"sort"
	"time"
)

// Religion is a choice of the household. The values are stored and sent by the API.
type Religion string

const (
	Alevi     Religion = "alevi"
	Bahai     Religion = "bahai"
	Buddhist  Religion = "buddhist"
	Christian Religion = "christian" // Catholic and Protestant
	Orthodox  Religion = "orthodox"
	Hindu     Religion = "hindu"
	Muslim    Religion = "muslim"
	Jewish    Religion = "jewish"
	Sikh      Religion = "sikh"
)

// Religions lists every religion, in the order of the code (the page sorts them by their names).
func Religions() []Religion {
	return []Religion{Alevi, Bahai, Buddhist, Christian, Orthodox, Hindu, Muslim, Jewish, Sikh}
}

// Valid reports whether r is a religion of the list.
func (r Religion) Valid() bool {
	for _, x := range Religions() {
		if x == r {
			return true
		}
	}
	return false
}

// IslamicCalendar says which table the Islamic dates follow.
type IslamicCalendar string

const (
	Diyanet   IslamicCalendar = "diyanet"
	UmmAlQura IslamicCalendar = "umm_al_qura"
)

// Valid reports whether c is one of the two.
func (c IslamicCalendar) Valid() bool { return c == Diyanet || c == UmmAlQura }

// OrthodoxCalendar says on which day the Orthodox church celebrates Christmas.
type OrthodoxCalendar string

const (
	RevisedJulian OrthodoxCalendar = "revised_julian" // 25 December
	Julian        OrthodoxCalendar = "julian"         // 7 January
)

// Valid reports whether c is one of the two.
func (c OrthodoxCalendar) Valid() bool { return c == RevisedJulian || c == Julian }

// Place is where the household lives; Hindu dates depend on it.
type Place struct {
	Lat, Lon float64
}

// Options are the choices that change the dates.
type Options struct {
	Islamic  IslamicCalendar  // empty: Diyanet
	Orthodox OrthodoxCalendar // empty: revised Julian
	// Place is the home location; nil means none (Hindu holidays are then not computed).
	Place *Place
	// Zone is the time zone of the household (full and new moon dates, sunrise). nil: UTC.
	Zone *time.Location
}

func (o Options) islamic() IslamicCalendar {
	if o.Islamic == UmmAlQura {
		return UmmAlQura
	}
	return Diyanet
}

func (o Options) orthodox() OrthodoxCalendar {
	if o.Orthodox == Julian {
		return Julian
	}
	return RevisedJulian
}

func (o Options) zone() *time.Location {
	if o.Zone == nil {
		return time.UTC
	}
	return o.Zone
}

// Source ids. Each has a name in the locale files (rh.source.<id>) and, for
// tables, a record in Tables.
const (
	SrcChurch   = "church_year"
	SrcOrthodox = "orthodox_church"
	SrcHebcal   = "hebcal"
	SrcDiyanet  = "diyanet"
	SrcUmmQura  = "umm_al_qura"
	SrcAABF     = "aabf"
	SrcBahai    = "bahai"
	SrcLosar    = "losar"
	SrcSGPC     = "sgpc"
	SrcMoon     = "meeus_moon"
	SrcPanchang = "panchang"
	SrcFixed    = "fixed_date"
)

// CalDate is a date in the calendar of the religion itself, for the event page
// ("15. bis 22. Nisan 5787"). Month is the number in that calendar.
type CalDate struct {
	Cal                       string // "hebrew" or "hijri"; empty: none
	Day, Month, Year          int
	EndDay, EndMonth, EndYear int // equal to the start for a single day
}

// Holiday is one holiday or festival.
type Holiday struct {
	Religion Religion
	// Key names the holiday: its name is rh.<religion>.<key> in the locale files.
	Key string
	// Start and End are the first and last day (dates at 00:00 UTC; End is the same day for a single day).
	Start, End time.Time
	// Eve: the festival begins the evening before Start; the calendar gets an extra event on that day.
	Eve bool
	// Evening: the day named is the one on whose evening the night begins (Lailat al-Qadr, Mevlid).
	Evening bool
	// Vary: the day depends on moon sighting; some communities celebrate one day later.
	Vary bool
	// ShowOwn: the list shows Own as the detail.
	ShowOwn bool
	// Own is the date in the calendar of the religion itself, when it has one the page names.
	Own CalDate
	// Source is a source id (Src...). Unchecked: the entry of a table was not yet checked against it.
	Source    string
	Unchecked bool
}

// Days is the number of days of the festival.
func (h Holiday) Days() int { return int(h.End.Sub(h.Start).Hours()/24+0.5) + 1 }

// Compute lists the holidays of one religion whose first day lies in
// fromYear to toYear (both included), by date. A Hindu holiday needs a Place;
// without it none is returned. Dates after the end of a table are left out.
func Compute(r Religion, o Options, fromYear, toYear int) []Holiday {
	var out []Holiday
	switch r {
	case Christian:
		out = christian(fromYear, toYear)
	case Orthodox:
		out = orthodox(o, fromYear, toYear)
	case Jewish:
		out = jewish(fromYear, toYear)
	case Muslim:
		out = muslim(o, fromYear, toYear)
	case Alevi:
		out = alevi(o, fromYear, toYear)
	case Bahai:
		out = bahai(fromYear, toYear)
	case Buddhist:
		out = buddhist(o, fromYear, toYear)
	case Sikh:
		out = sikh(fromYear, toYear)
	case Hindu:
		out = hindu(o, fromYear, toYear)
	}
	for i := range out {
		out[i].Religion = r
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func day(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC) }

// one makes a single-day holiday.
func one(key string, t time.Time, src string) Holiday {
	return Holiday{Key: key, Start: t, End: t, Source: src}
}

// span makes a festival of several days.
func span(key string, from, to time.Time, src string) Holiday {
	return Holiday{Key: key, Start: from, End: to, Source: src}
}

// inYears keeps the holidays whose first day lies in the years.
func inYears(hs []Holiday, from, to int) []Holiday {
	var out []Holiday
	for _, h := range hs {
		if y := h.Start.Year(); y >= from && y <= to {
			out = append(out, h)
		}
	}
	return out
}

// Skipped is a holiday left out because it is a public holiday of the household's region already.
type Skipped struct {
	Holiday
	// Partial: a public holiday in most, but not all, places of the region.
	Partial bool
}

// Skip splits holidays into the ones the calendar gets and the ones that every
// day of which is the public holiday of the region itself: those are in the
// calendar already and are never added twice. A festival that merely falls on
// the date of a public holiday (Vesak on 1 May) is kept. public says, for a
// holiday key and a day, whether the region has that holiday on that day and
// whether it is a partial holiday there.
func Skip(hs []Holiday, public func(key string, day time.Time) (partial, ok bool)) (kept []Holiday, skipped []Skipped) {
	for _, h := range hs {
		all, partial := true, false
		for t := h.Start; !t.After(h.End); t = t.AddDate(0, 0, 1) {
			p, ok := public(h.Key, t)
			if !ok {
				all = false
				break
			}
			partial = partial || p
		}
		if all {
			skipped = append(skipped, Skipped{h, partial})
		} else {
			kept = append(kept, h)
		}
	}
	return kept, skipped
}
