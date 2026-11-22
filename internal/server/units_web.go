package server

import (
	"errors"
	"html"
	"html/template"
	"net/http"
	"strings"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// Settings, Lists, Units (ticket 165, preview-165 frame 10): which units can be
// chosen when adding to a list. The home's country presets them; a parent
// switches more on or off. Switching off changes no entry on a list.

type unitRow struct {
	Key, Name, Desc string
	On, Always      bool
}

type unitGroup struct {
	Key, Title string
	Rows       []unitRow
	Error      string
}

type unitsForm struct {
	Groups []unitGroup
	Hint   template.HTML // carries a link
	Reset  string
	Custom bool
}

// unitOrder is how the settings page lists the units of a group: the units
// with the same name of the UK come before the US ones.
var unitOrder = map[string][]string{
	unit.GroupCount:  {"piece", "dozen", "pack", "bunch"},
	unit.GroupWeight: {"g", "kg", "oz", "lb"},
	unit.GroupVolume: {"ml", "l", "floz_uk", "floz_us", "cup_us", "pint_uk", "pint_us", "quart_us", "gallon_uk", "gallon_us"},
}

// countryName is the name of a country in a sentence, "" when Hejmo has none.
func (s *server) countryName(lang, code string) string {
	if code == "" {
		return ""
	}
	if n := s.i18n.T(lang, "lq.country."+code); n != "lq.country."+code {
		return n
	}
	return ""
}

func (s *server) unitsForm(p principal, lang string, on []string, errGroup string) unitsForm {
	_, custom, _ := s.db.ActiveUnits(p.household.ID)
	if on == nil {
		on = s.activeUnits(p.household)
	}
	f := unitsForm{Custom: custom}
	for _, g := range []string{unit.GroupCount, unit.GroupWeight, unit.GroupVolume} {
		grp := unitGroup{Key: g, Title: s.i18n.T(lang, "lq.units.group."+g)}
		if g == errGroup {
			grp.Error = s.i18n.T(lang, "lq.units.err."+g)
		}
		for _, k := range unitOrder[g] {
			name := s.i18n.T(lang, "unit."+k+".one")
			switch {
			case strings.HasSuffix(k, "_us"):
				name += " " + s.i18n.T(lang, "unit.suffix.us")
			case strings.HasSuffix(k, "_uk"):
				name += " " + s.i18n.T(lang, "unit.suffix.uk")
			}
			row := unitRow{Key: k, Name: name, On: strings.Contains(","+strings.Join(on, ",")+",", ","+k+","), Always: k == unit.Piece}
			if d := s.i18n.T(lang, "unit."+k+".desc"); d != "unit."+k+".desc" {
				row.Desc = d
			}
			grp.Rows = append(grp.Rows, row)
		}
		f.Groups = append(f.Groups, grp)
	}
	country := s.countryName(lang, homeCountry(p.household))
	link := `<a href="/settings/household">` + s.i18n.T(lang, "lq.units.household") + `</a>`
	switch {
	case custom:
		f.Hint = template.HTML(html.EscapeString(s.i18n.T(lang, "lq.units.hint.custom")))
	case country != "":
		f.Hint = template.HTML(repl(html.EscapeString(s.i18n.T(lang, "lq.units.hint")), "{country}", "<b>"+html.EscapeString(country)+"</b>", "{link}", link))
	default:
		f.Hint = template.HTML(html.EscapeString(s.i18n.T(lang, "lq.units.hint.nocountry")))
	}
	f.Reset = s.i18n.T(lang, "lq.units.reset.nocountry")
	if country != "" {
		f.Reset = repl(s.i18n.T(lang, "lq.units.reset"), "{country}", country)
	}
	return f
}

// handleUnitsSave stores the units a parent switched on, or goes back to the preset.
func (s *server) handleUnitsSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	if r.PostFormValue("reset") != "" {
		if err := s.db.ResetActiveUnits(p.household.ID); err != nil {
			s.fail(w, "reset units", err)
			return
		}
		s.redirectSaved(w, r, listsSettingsPath+"#units", "flash.units_reset")
		return
	}
	var on []string
	for _, k := range unit.All {
		if r.PostFormValue("unit_"+k) != "" {
			on = append(on, k)
		}
	}
	err := s.db.SetActiveUnits(p.household.ID, on)
	var fe *store.FieldError
	switch {
	case errors.As(err, &fe):
		s.renderListsSettingsUnits(w, r, p, http.StatusUnprocessableEntity, nil, "", unit.Normalize(on), fe.Field)
	case err != nil:
		s.fail(w, "save units", err)
	default:
		s.redirectSaved(w, r, listsSettingsPath+"#units", "flash.units_saved")
	}
}
