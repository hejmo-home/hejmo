package server

import (
	"net/http"
	"sort"
	"strings"

	"github.com/hejmo-home/hejmo/internal/nameday"
)

// handleLicences shows the third-party content the box ships with and the
// licences it comes under (the place list is CC BY 4.0 and must be credited).
func (s *server) handleLicences(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.render(w, r, http.StatusOK, "licences.html", map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "PlacesDate": s.places.Date(), "NameDayLicences": s.nameDayLicences(s.langOf(r)),
	})
}

// nameDayLicence is a row of the card "Namenstage und Slava": a list, the sources its entries come from and whether an
// entry of it is not yet checked against them.
type nameDayLicence struct {
	Name      string
	Sources   []nameDaySource
	Unchecked bool
}

// nameDaySource is a source in the reader's language and, when that does not already say it, the name recorded in the
// data file (the original-language name of a church calendar).
type nameDaySource struct{ Name, Original string }

// nameDayLicences names every source of every name-day list, in the order of the settings page (tickets 318 and 338),
// and of the Slava list: the rows of the licences page that credits the calendars of the churches.
func (s *server) nameDayLicences(lang string) []nameDayLicence {
	e := ctEnv{s: s, lang: lang}
	var out []nameDayLicence
	for _, l := range nameday.Lists() {
		cal, ok := nameday.Get(l)
		if !ok {
			continue
		}
		row := nameDayLicence{Name: s.i18n.T(lang, "nd.list."+string(l))}
		used := map[string]bool{}
		for _, en := range cal.Entries {
			row.Unchecked = row.Unchecked || en.Unchecked
			used[en.Source] = true
		}
		keys := make([]string, 0, len(used))
		for k := range used {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			src := nameDaySource{Name: e.sourceName(cal, k)}
			if !strings.HasPrefix(cal.Sources[k], src.Name) {
				src.Original = cal.Sources[k]
			}
			row.Sources = append(row.Sources, src)
		}
		out = append(out, row)
	}
	row := nameDayLicence{Name: s.i18n.T(lang, "slava.fact"), Sources: []nameDaySource{{Name: s.i18n.T(lang, "slava.source")}}}
	for _, en := range nameday.Slavas().Entries {
		row.Unchecked = row.Unchecked || en.Unchecked
	}
	return append(out, row)
}
