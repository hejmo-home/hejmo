package religiousholiday

import (
	"sort"

	"github.com/hebcal/hdate"
	"github.com/hebcal/hebcal-go/event"
	"github.com/hebcal/hebcal-go/hebcal"
)

// jewishFestivals maps the base name hebcal-go gives an event to our key. The
// Diaspora schedule is used (DACH): Pessach has 8 days, Schawuot 2, and
// Schemini Azeret and Simchat Tora are two days. A festival begins on the
// evening before its first day.
var jewishFestivals = map[string]string{
	"Rosh Hashana":   "rosh_hashanah",
	"Yom Kippur":     "yom_kippur",
	"Sukkot":         "sukkot",
	"Shmini Atzeret": "simchat_torah",
	"Simchat Torah":  "simchat_torah",
	"Purim":          "purim",
	"Pesach":         "pessach",
	"Shavuot":        "shavuot",
}

// hebrewOn is the date of a day in the Hebrew calendar.
func hebrewOn(first, last Holiday) CalDate {
	a, b := hdate.FromGregorian(first.Start.Year(), first.Start.Month(), first.Start.Day()), hdate.FromGregorian(last.End.Year(), last.End.Month(), last.End.Day())
	return CalDate{Cal: "hebrew", Day: a.Day(), Month: int(a.Month()), Year: a.Year(), EndDay: b.Day(), EndMonth: int(b.Month()), EndYear: b.Year()}
}

// jewish lists the festivals with hebcal-go and joins the days of one festival.
func jewish(from, to int) []Holiday {
	var out []Holiday
	for y := from; y <= to; y++ {
		evs, err := hebcal.HebrewCalendar(&hebcal.Options{Year: y, NoMinorFast: true, NoRoshChodesh: true, NoSpecialShabbat: true, NoModern: true})
		if err != nil {
			continue
		}
		days := map[string][]Holiday{}
		for _, e := range evs {
			t := day(e.Date().Gregorian())
			base, erev := e.Basename(), e.Flags().Has(event.Erev)
			switch {
			case base == "Chanukah" && erev:
				// The first candle is lit on the evening of the 24th of Kislew; the eight days run from the 25th.
				c := Holiday{Key: "chanukkah", Start: t.AddDate(0, 0, 1), End: t.AddDate(0, 0, 8), Source: SrcHebcal, Eve: true}
				c.Own = hebrewOn(c, c)
				if c.Start.Year() == y {
					out = append(out, c)
				}
			case !erev:
				if key, ok := jewishFestivals[base]; ok {
					days[key] = append(days[key], Holiday{Key: key, Start: t, End: t, Source: SrcHebcal, Eve: true})
				}
			}
		}
		for _, hs := range days {
			sort.Slice(hs, func(i, j int) bool { return hs[i].Start.Before(hs[j].Start) })
			cur := hs[0]
			flush := func() {
				cur.Own = hebrewOn(cur, cur)
				if cur.Start.Year() == y {
					out = append(out, cur)
				}
			}
			for _, h := range hs[1:] {
				if h.Start.Sub(cur.End).Hours() <= 24 { // the next day of the same festival
					cur.End = h.Start
					continue
				}
				flush()
				cur = h
			}
			flush()
		}
	}
	return out
}
