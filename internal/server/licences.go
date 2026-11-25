package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/nameday"
	rh "github.com/hejmo-home/hejmo/internal/religiousholiday"
)

// handleLicences shows the third-party content the box ships with and the
// licences it comes under (the place list is CC BY 4.0 and must be credited).
func (s *server) handleLicences(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.render(w, r, http.StatusOK, "licences.html", map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "PlacesDate": s.places.Date(), "NameDaySources": s.nameDaySources(s.langOf(r)), "RHLines": s.religiousLicences(s.langOf(r)),
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

// licenceLine is a line of a licences card; Unchecked marks a table that was not yet checked against its source.
type licenceLine struct {
	Text      string
	Unchecked bool
}

// religiousLicences lists what the religious holidays calendar is made from (ticket 339): the three libraries with
// their licences, the rules that were worked out, and every table with its source, the years it covers and how
// much of it was not yet checked against the source.
func (s *server) religiousLicences(lang string) []licenceLine {
	t := func(key string, kv ...string) string { return s.i18n.Format(lang, i18n.Msg(key, kv...)) }
	out := []licenceLine{{Text: t("lic.rh.lib.hebcal")}, {Text: t("lic.rh.lib.hijri")}, {Text: t("lic.rh.lib.meeus")}}
	for _, tb := range rh.Tables() {
		out = append(out, licenceLine{Text: s.rhSourceName(lang, tb.ID)})
		if tb.UncheckedEntries > 0 {
			out = append(out, licenceLine{Text: t("lic.rh.unchecked", "n", strconv.Itoa(tb.UncheckedEntries), "total", strconv.Itoa(tb.Entries)), Unchecked: true})
		}
	}
	out = append(out, licenceLine{Text: s.rhSourceName(lang, rh.SrcAABF)}, licenceLine{Text: t("lic.rh.rules")})
	return out
}
