package server

import (
	"net/http"
	"slices"
	"strings"

	"golang.org/x/text/collate"
	textlang "golang.org/x/text/language"

	"github.com/hejmo-home/hejmo/internal/food"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

// foodView is a person's diet and allergies in words, for the person page and
// the people overview (ticket 163). Allergies lists the ticked allergens;
// Unchecked is the note for the free text, which the box cannot check.
type foodView struct {
	Diet      string
	Allergies string
	Unchecked string
	// Quiet is set for someone who eats everything and has no allergies: the
	// overview says nothing about them.
	Quiet bool
	// None says "no allergies entered" (nothing ticked, no free text).
	None string
}

func (s *server) allergenName(lang, key string) string {
	return s.i18n.T(lang, "allergen."+key)
}

// uncheckedText is the note for the free-text allergies of a person, or ""
// when there are none.
func (s *server) uncheckedText(lang string, other []string) string {
	if len(other) == 0 {
		return ""
	}
	return s.i18n.Format(lang, i18n.Msg("food.unchecked", "text", strings.Join(other, ", ")))
}

func (s *server) foodViewOf(lang string, f store.Food) foodView {
	other, _ := food.ParseOther(f.Other)
	v := foodView{Diet: s.i18n.T(lang, "diet."+f.Diet), Unchecked: s.uncheckedText(lang, other)}
	if f.Diet == "" {
		v.Diet = s.i18n.T(lang, "diet."+string(food.Omnivore))
	}
	if len(f.Allergens) > 0 {
		names := make([]string, len(f.Allergens))
		for i, a := range f.Allergens {
			names[i] = s.allergenName(lang, a)
		}
		sortNames(lang, names)
		v.Allergies = s.i18n.Format(lang, i18n.Msg("food.allergies", "list", strings.Join(names, ", ")))
	}
	v.Quiet = (f.Diet == "" || f.Diet == string(food.Omnivore)) && v.Allergies == "" && v.Unchecked == ""
	if v.Allergies == "" && v.Unchecked == "" {
		v.None = s.i18n.T(lang, "food.row.none")
	}
	return v
}

// sortNames puts names in the alphabetical order of the page language.
func sortNames(lang string, names []string) {
	c := collate.New(textlang.Make(lang))
	slices.SortFunc(names, c.CompareString)
}

type dietOption struct {
	Value, Name, Desc string
	Checked           bool
}

type allergenCard struct {
	Key, Name, Examples string
	Checked             bool
}

// foodForm is what the page "Essen und Allergien" shows: the stored entry, or
// the entry of a form that was refused.
type foodForm struct {
	Diet      string
	Allergens []string
	Other     string
}

func (s *server) renderFood(w http.ResponseWriter, r *http.Request, p principal, status int, person store.Person, people []store.Person, form foodForm, errKey, errText string) {
	lang := s.langOf(r)
	var chip personChip
	for _, c := range chipsOf(people) {
		if c.ID == person.ID {
			chip = c
		}
	}
	diets := make([]dietOption, len(food.Diets))
	for i, d := range food.Diets {
		diets[i] = dietOption{string(d), s.i18n.T(lang, "diet."+string(d)), s.i18n.T(lang, "diet."+string(d)+".desc"), string(d) == form.Diet}
	}
	cards := make([]allergenCard, len(food.Allergens))
	for i, a := range food.Allergens {
		cards[i] = allergenCard{a, s.allergenName(lang, a), s.i18n.T(lang, "allergen."+a+".examples"), slices.Contains(form.Allergens, a)}
	}
	c := collate.New(textlang.Make(lang))
	slices.SortFunc(cards, func(x, y allergenCard) int { return c.CompareString(x.Name, y.Name) })
	items, _ := food.ParseOther(form.Other)
	sample := s.uncheckedText(lang, items)
	if sample == "" {
		sample = s.i18n.T(lang, "food.sample.empty")
	}
	f := func(key string) string { return s.i18n.Format(lang, i18n.Msg(key, "name", person.Name)) }
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Subject": chip,
		"DietLegend": f("food.diet.legend"), "AllergensHint": f("food.allergens.hint"),
		"Diets": diets, "Cards": cards, "Other": form.Other, "Sample": sample,
		"SampleTemplate": s.i18n.T(lang, "food.unchecked"), "SampleEmpty": s.i18n.T(lang, "food.sample.empty"),
		"Error": errKey, "ErrorText": errText,
	}
	if errKey != "" {
		data["ErrorField"] = "other"
	}
	s.render(w, r, status, "food.html", data)
}

func (s *server) handleFoodPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	person, people, ok := s.personFor(w, r, p)
	if !ok {
		return
	}
	f, err := s.db.PersonFood(p.household.ID, person.ID)
	if err != nil {
		s.fail(w, "get food", err)
		return
	}
	s.renderFood(w, r, p, http.StatusOK, person, people, foodForm{f.Diet, f.Allergens, f.Other}, "", "")
}

func (s *server) handleFoodSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	person, people, ok := s.personFor(w, r, p)
	if !ok {
		return
	}
	_ = r.ParseForm()
	lang := s.langOf(r)
	form := foodForm{Diet: r.PostFormValue("diet"), Allergens: r.PostForm["allergens"], Other: strings.TrimSpace(r.PostFormValue("other"))}
	if form.Diet == "" {
		form.Diet = string(food.Omnivore) // a form without a choice keeps the default
	}
	refuse := func(key, text string) {
		s.renderFood(w, r, p, http.StatusUnprocessableEntity, person, people, form, key, text)
	}
	if !food.ValidDiet(form.Diet) {
		http.Error(w, "unknown diet", http.StatusUnprocessableEntity)
		return
	}
	allergens, ok := food.NormalizeAllergens(form.Allergens)
	if !ok {
		http.Error(w, "unknown allergen", http.StatusUnprocessableEntity)
		return
	}
	items, conflict := food.ParseOther(form.Other)
	text := strings.Join(items, ", ")
	switch {
	case conflict != nil:
		refuse("food.error.conflict", s.i18n.Format(lang, i18n.Msg("food.error.conflict", "word", conflict.Word, "card", s.allergenName(lang, conflict.Allergen))))
		return
	case !food.OtherFits(text):
		refuse("food.error.long", s.i18n.T(lang, "food.error.long"))
		return
	}
	if err := s.db.SetPersonFood(p.household.ID, person.ID, store.Food{Diet: form.Diet, Allergens: allergens, Other: text}); err != nil {
		s.fail(w, "set food", err)
		return
	}
	s.redirectSavedNamed(w, r, "/people/"+person.ID, "flash.food_saved", person.Name)
}
