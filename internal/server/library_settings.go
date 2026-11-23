package server

import (
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/food"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Settings, Lists (ticket 164, preview-164): the article library as one row,
// then how Hejmo warns about allergies and the optional, experimental hiding.

const (
	listsSettingsPath = "/settings/lists"
	libraryPath       = "/settings/lists/library"
	hejmoSymbol       = "🧺"
)

// allergyPerson is a person with allergies entered, for "Eingetragene Allergien".
type allergyPerson struct {
	Chip      personChip
	Allergens string // the ticked allergens in words, "" when only free text
	Unchecked string // the note for the free text, "" when none
}

// freeTextPerson is a person with free-text allergies, which Hejmo cannot check.
type freeTextPerson struct {
	Chip personChip
	Text string
}

// freeText lists the people with free-text allergies, in household order.
func (s *server) freeText(st libState) []freeTextPerson {
	var out []freeTextPerson
	for _, x := range st.people {
		if items, _ := food.ParseOther(st.foods[x.ID].Other); len(items) > 0 {
			out = append(out, freeTextPerson{st.chips[x.ID], strings.Join(items, ", ")})
		}
	}
	return out
}

// libraryRowStates is the library's state in words: how many shipped articles
// and how many of them were hidden by hand, how many own ones.
func (s *server) libraryRowStates(st libState) (shipped, own string) {
	n, h := len(library.Shipped()), 0
	for range st.hidden {
		h++
	}
	if h > 0 {
		shipped = s.i18n.Format(st.lang, i18n.Msg("lib.state.shipped_hidden", "n", strconv.Itoa(n), "h", strconv.Itoa(h)))
	} else {
		shipped = s.i18n.Format(st.lang, i18n.Msg("lib.state.shipped", "n", strconv.Itoa(n)))
	}
	switch len(st.own) {
	case 0:
		own = s.i18n.T(st.lang, "lib.state.own.none")
	case 1:
		own = s.i18n.T(st.lang, "lib.state.own.one")
	default:
		own = s.i18n.Format(st.lang, i18n.Msg("lib.state.own.many", "n", strconv.Itoa(len(st.own))))
	}
	return shipped, own
}

// settingsForm is what the hiding form shows: the stored setting, or what was
// posted when the form was refused.
type settingsForm struct {
	On, Whole, Confirm bool
	People             []string
}

func (s *server) renderListsSettings(w http.ResponseWriter, r *http.Request, p principal, status int, form *settingsForm, errText string) {
	s.renderListsSettingsUnits(w, r, p, status, form, errText, nil, "")
}

// renderListsSettingsUnits is renderListsSettings with the units a refused
// form chose (nil: the stored ones) and the group that has none left on.
func (s *server) renderListsSettingsUnits(w http.ResponseWriter, r *http.Request, p principal, status int, form *settingsForm, errText string, unitsOn []string, unitsErr string) {
	lang := s.langOf(r)
	st, err := s.libState(p, lang)
	if err != nil {
		s.fail(w, "library state", err)
		return
	}
	if form == nil {
		form = &settingsForm{On: st.settings.HideAllergen, Whole: st.settings.Everyone, People: st.settings.People, Confirm: st.settings.HideAllergen}
	}
	shipped, own := s.libraryRowStates(st)
	alwaysRow, err := s.hubRowFor(p, lang)
	if err != nil {
		s.fail(w, "always row", err)
		return
	}

	var withAllergies []allergyPerson
	var without []string
	var holders []personChip // people with ticked allergens: the ones hiding can be chosen for
	var sample template.HTML
	for _, x := range st.people {
		f := st.foods[x.ID]
		items, _ := food.ParseOther(f.Other)
		if len(f.Allergens) == 0 && len(items) == 0 {
			without = append(without, x.Name)
			continue
		}
		a := allergyPerson{Chip: st.chips[x.ID], Unchecked: s.uncheckedText(lang, items)}
		if len(f.Allergens) > 0 {
			a.Allergens = s.allergenWords(lang, f.Allergens)
			holders = append(holders, st.chips[x.ID])
			if sample == "" {
				sample = s.warnHTML(st, library.Warning{People: []string{x.ID}, Allergens: f.Allergens[:1]})
			}
		}
		withAllergies = append(withAllergies, a)
	}
	var withoutText string
	switch len(without) {
	case 0:
	case 1:
		withoutText = s.i18n.Format(lang, i18n.Msg("lists.set.people.without_one", "names", without[0]))
	default:
		withoutText = s.i18n.Format(lang, i18n.Msg("lists.set.people.without_many", "names", s.joinWords(lang, without)))
	}

	confirmed := ""
	if st.settings.ConfirmedBy != "" && !st.settings.ConfirmedAt.IsZero() {
		if c, ok := st.chips[st.settings.ConfirmedBy]; ok {
			confirmed = s.i18n.Format(lang, i18n.Msg("lists.set.confirmed", "name", c.Name, "when", p.dateTime(st.settings.ConfirmedAt)))
		}
	}
	data := map[string]any{
		"Units":  s.unitsForm(p, lang, unitsOn, unitsErr),
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID),
		"LibrarySymbol": hejmoSymbol, "ShippedState": shipped, "OwnState": own, "AlwaysRow": alwaysRow,
		"Anyone": len(withAllergies) > 0, "CanHide": len(holders) > 0,
		"AllergyPeople": withAllergies, "WithoutText": withoutText, "Sample": sample,
		"Form": form, "Holders": holders, "Confirmed": confirmed, "FreeText": s.freeText(st),
		"Liability": []string{"lists.set.liab.1", "lists.set.liab.2", "lists.set.liab.3", "lists.set.liab.4"},
		"BackHref":  "/settings", "BackText": s.i18n.T(lang, "hub.back"),
	}
	if errText != "" {
		data["ErrorField"], data["ErrorText"] = "liability", errText
	}
	s.render(w, r, status, "settings_lists.html", data)
}

func (s *server) handleListsSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.renderListsSettings(w, r, p, http.StatusOK, nil, "")
}

func (s *server) handleListsSettingsSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	old, err := s.db.LibrarySettings(p.household.ID)
	if err != nil {
		s.fail(w, "library settings", err)
		return
	}
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	next := old
	next.HideAllergen = r.PostFormValue("hide") != ""
	if next.HideAllergen {
		var chosen []string
		for _, id := range r.PostForm["people"] {
			if slices.ContainsFunc(people, func(x store.Person) bool { return x.ID == id }) && !slices.Contains(chosen, id) {
				chosen = append(chosen, id)
			}
		}
		next.Everyone = r.PostFormValue("whole_family") != "" || len(chosen) == 0
		next.People = chosen
		if next.Everyone {
			next.People = []string{}
		}
		if r.PostFormValue("liability") == "" {
			form := &settingsForm{On: true, Whole: next.Everyone, People: chosen}
			s.renderListsSettings(w, r, p, http.StatusUnprocessableEntity, form, s.i18n.T(s.langOf(r), "lists.set.confirm.error"))
			return
		}
		if !old.HideAllergen || old.ConfirmedBy == "" { // the notice is read now; a notice read before stays as it was
			next.ConfirmedBy = p.person.ID
		}
	}
	if err := s.db.SetLibrarySettings(p.household.ID, next); err != nil {
		s.fail(w, "set library settings", err)
		return
	}
	key := "flash.hide_off"
	if next.HideAllergen {
		key = "flash.hide_on"
	}
	s.redirectSaved(w, r, listsSettingsPath, key)
}
