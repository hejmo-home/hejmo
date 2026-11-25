package server

import (
	"net/http"

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

// nameDayLicence is a row of the card "Namenstage und Slava": a list, its source and whether an entry of it is not yet
// checked against that source.
type nameDayLicence struct {
	Name, Source string
	Unchecked    bool
}

// nameDayLicences names the source of every name-day list, in the order of the settings page (tickets 318 and 338),
// and of the Slava list: the rows of the licences page that credits the calendars of the churches.
func (s *server) nameDayLicences(lang string) []nameDayLicence {
	var out []nameDayLicence
	for _, l := range nameday.Lists() {
		cal, ok := nameday.Get(l)
		if !ok {
			continue
		}
		row := nameDayLicence{Name: s.i18n.T(lang, "nd.list."+string(l)), Source: s.i18n.T(lang, "nd.list."+string(l)+".source")}
		for _, e := range cal.Entries {
			row.Unchecked = row.Unchecked || e.Unchecked
		}
		out = append(out, row)
	}
	row := nameDayLicence{Name: s.i18n.T(lang, "slava.fact"), Source: s.i18n.T(lang, "slava.source")}
	for _, e := range nameday.Slavas().Entries {
		row.Unchecked = row.Unchecked || e.Unchecked
	}
	return append(out, row)
}
