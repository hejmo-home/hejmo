package server

import (
	"net/http"
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
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "PlacesDate": s.places.Date(), "NameDaySources": s.nameDaySources(s.langOf(r)),
	})
}

// nameDaySources names the source of every name-day list, in the order of the settings page (ticket 318): the line of
// the licences page that credits the calendars of the churches.
func (s *server) nameDaySources(lang string) string {
	var out []string
	for _, l := range nameday.Lists() {
		cal, ok := nameday.Get(l)
		if !ok {
			continue
		}
		var names []string
		seen := map[string]bool{}
		for _, e := range cal.Entries {
			if n := cal.Sources[e.Source]; !seen[e.Source] {
				seen[e.Source] = true
				names = append(names, n)
			}
		}
		out = append(out, s.i18n.T(lang, "nd.list."+string(l))+": "+strings.Join(names, "; ")+".")
	}
	return strings.Join(out, " ")
}
