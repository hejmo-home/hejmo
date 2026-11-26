package server

import (
	"net/http"
	"sort"
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
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "PlacesDate": s.places.Date(), "NameDayLicences": s.nameDayLicences(s.langOf(r)), "RHLines": s.religiousLicences(s.langOf(r)),
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
	slava := nameday.Slavas()
	src := nameDaySource{Name: s.i18n.T(lang, "slava.source")}
	if orig := slava.Sources["spc"]; !strings.HasPrefix(orig, src.Name) { // the edition the list was checked against
		src.Original = orig
	}
	row := nameDayLicence{Name: s.i18n.T(lang, "slava.fact"), Sources: []nameDaySource{src}}
	for _, en := range nameday.Slavas().Entries {
		row.Unchecked = row.Unchecked || en.Unchecked
	}
	return append(out, row)
}

// licenceLine is a line of a licences card; Unchecked marks a table that was not yet checked against its source.
type licenceLine struct {
	Text      string
	Unchecked bool
}

// religiousLicences lists what the religious holidays calendar is made from (ticket 339): the three libraries with
// their licences, the rules that were worked out, and every table with its source, the years it covers and what it was checked against (ticket 354) and how
// much of it was not yet checked.
func (s *server) religiousLicences(lang string) []licenceLine {
	t := func(key string, kv ...string) string { return s.i18n.Format(lang, i18n.Msg(key, kv...)) }
	out := []licenceLine{{Text: t("lic.rh.lib.hebcal")}, {Text: t("lic.rh.lib.hijri")}, {Text: t("lic.rh.lib.meeus")}}
	for _, tb := range rh.Tables() {
		out = append(out, licenceLine{Text: s.rhSourceName(lang, tb.ID)})
		if tb.UncheckedEntries == 0 {
			edition := t("lic.rh.edition." + tb.ID)
			out = append(out, licenceLine{Text: t("lic.rh.checked", "source", edition)})
		}
		if tb.ID == rh.SrcBahai {
			out = append(out, licenceLine{Text: t("lic.rh.bahai.copyright")})
		}
		if tb.UncheckedEntries > 0 {
			out = append(out, licenceLine{Text: t("lic.rh.unchecked", "n", strconv.Itoa(tb.UncheckedEntries), "total", strconv.Itoa(tb.Entries)), Unchecked: true})
		}
	}
	out = append(out, licenceLine{Text: s.rhSourceName(lang, rh.SrcAABF)}, licenceLine{Text: t("lic.rh.rules")})
	return out
}
