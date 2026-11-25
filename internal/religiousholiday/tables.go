package religiousholiday

import (
	"sort"
	"time"
)

// Embedded date tables. Every entry names its source (Src...) and says whether
// it was checked against it: an entry that was typed from memory or from a
// secondary source is Unchecked and the box says so next to the date (ticket
// 339). TestEveryTableEntryHasASource fails for an entry without a source.

// Entry is one date of a table.
type Entry struct {
	Date      time.Time
	Source    string
	Unchecked bool
}

// TableInfo describes a table for the Licences page.
type TableInfo struct {
	ID       string // a source id
	From, To int    // years the table covers
	Entries  int
	// UncheckedEntries is how many entries were not yet checked against the source.
	UncheckedEntries int
}

// allTables lists every table with its entries.
func allTables() map[string][]Entry {
	return map[string][]Entry{
		SrcDiyanet: diyanetEntries(),
		SrcAABF:    nil, // fixed days, see alevi
		SrcLosar:   losarEntries(),
		SrcSGPC:    sgpcEntries(),
	}
}

// Tables lists the embedded tables, for the Licences page, sorted by id.
func Tables() []TableInfo {
	var out []TableInfo
	for id, es := range allTables() {
		if len(es) == 0 {
			continue
		}
		ti := TableInfo{ID: id, Entries: len(es), From: es[0].Date.Year(), To: es[0].Date.Year()}
		for _, e := range es {
			if y := e.Date.Year(); y < ti.From {
				ti.From = y
			} else if y > ti.To {
				ti.To = y
			}
			if e.Unchecked {
				ti.UncheckedEntries++
			}
		}
		out = append(out, ti)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// e makes a table entry from a date written as 2006-01-02.
func e(src, iso string, unchecked bool) Entry {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		panic("religiousholiday: bad date " + iso)
	}
	return Entry{Date: t, Source: src, Unchecked: unchecked}
}

// TableEnd is the last year a table of the religion covers, or 0 when the
// dates are computed without a table that ends. The page says so when it ends
// before the end of the calendar.
func TableEnd(r Religion, o Options) int {
	switch r {
	case Muslim, Alevi:
		if o.islamic() == Diyanet {
			return tableLastYear(diyanetEntries())
		}
	case Buddhist:
		return tableLastYear(losarEntries())
	case Sikh:
		return tableLastYear(sgpcEntries())
	}
	return 0
}

func tableLastYear(es []Entry) int {
	last := 0
	for _, x := range es {
		if y := x.Date.Year(); y > last {
			last = y
		}
	}
	return last
}
