package server

import (
	"html"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/recipe"
)

// The suggestions under a meal field of the week form (ticket 281, ADR 0021):
// recipes first, then earlier free-text dishes, then the typed text as free
// text. The script of the meal plan asks for the list while a parent types and
// the server writes it, as for the ingredient lines of a recipe; without the
// script the field stays a text field and a text that is a recipe's name is
// matched when the week is saved.

type mealSuggRecipe struct {
	*mealRecipe
	Box *mealCell // the box that choosing the recipe puts in place of the field
}

type mealSuggDish struct{ Text, Sub string }

type mealSuggView struct {
	Cell, Intro, Typed, KeepName, KeepSub string
	Recipes                               []mealSuggRecipe
	Dishes                                []mealSuggDish
	NoRecipes                             template.HTML
	Unchecked                             template.HTML
}

// whenText is "Thursday, 12.11.", a day in words.
func (s *server) whenText(p principal, lang, day string) string {
	d := mustDay(day)
	return s.i18n.T(lang, "meals.weekday."+strconv.Itoa(int(d.Weekday()))) + ", " + datefmt.Short(p.household.DateFormat, d)
}

// handleMealSuggest answers the list under a meal field: GET /meals/suggest?q=&cell=
// (the field's name, which also gives the mealtime) or &mealtime=. Less than two
// letters get no content.
func (s *server) handleMealSuggest(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	typed := strings.Join(strings.Fields(r.URL.Query().Get("q")), " ")
	if n := utf8.RuneCountInString(typed); n < recipe.MinSuggestLetters || n > meal.MaxText {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	cell := r.URL.Query().Get("cell")
	day, mt, hasCell := parseMealField(cell)
	if !hasCell {
		cell, mt = "", meal.Mealtime(r.URL.Query().Get("mealtime"))
		if !mt.Valid() {
			mt = ""
		}
	}
	c, err := s.itemContext(p, lang)
	if err != nil {
		s.fail(w, "meal context", err)
		return
	}
	all, err := s.db.Recipes(p.household.ID)
	if err != nil {
		s.fail(w, "recipes", err)
		return
	}
	plain := make([]recipe.Recipe, 0, len(all))
	var names []string
	byID := map[string]int{}
	for i, x := range all {
		plain = append(plain, x.Recipe)
		names = append(names, x.Name)
		byID[x.ID] = i
	}
	earlier, err := s.db.EarlierDishes(p.household.ID, s.today(p))
	if err != nil {
		s.fail(w, "earlier dishes", err)
		return
	}
	people := c.householdPortions()
	view := mealSuggView{Cell: cell, Typed: typed, KeepName: c.t("meals.sugg.keep", "q", typed), KeepSub: c.t("meals.sugg.keep.hint")}
	for _, x := range recipe.SuggestRecipes(plain, typed, mt, recipe.MaxSuggestedRecipes) {
		rec := all[byID[x.ID]]
		row := mealSuggRecipe{mealRecipe: c.mealRecipe(rec, day, string(mt), people, people)}
		if hasCell {
			row.Box = &mealCell{Name: cell, Mealtime: string(mt), Day: day, Recipe: row.mealRecipe}
		}
		view.Recipes = append(view.Recipes, row)
	}
	for _, d := range recipe.EarlierDishes(earlier, typed, names, recipe.MaxSuggestedDishes) {
		view.Dishes = append(view.Dishes, mealSuggDish{Text: d.Text, Sub: c.t("meals.sugg.old", "when", s.whenText(p, lang, d.Day))})
	}
	nr, nd := len(view.Recipes), len(view.Dishes)
	switch {
	case nr+nd == 0:
		view.Intro = c.t("meals.sugg.none_match", "q", typed)
	default:
		var parts []string
		if nr > 0 {
			parts = append(parts, c.count("meals.sugg.count.recipes", nr))
		}
		if nd > 0 {
			parts = append(parts, c.count("meals.sugg.count.dishes", nd))
		}
		key, tap := "meals.sugg.q.many", c.t("meals.sugg.tap")
		if nr+nd == 1 {
			key = "meals.sugg.q.one"
		}
		if nr > 0 {
			tap = c.t("meals.sugg.tap.recipe", "n", strconv.Itoa(people))
		}
		view.Intro = c.t(key, "what", c.names(parts), "q", typed, "tap", tap)
	}
	if nr+nd > 0 && len(all) == 0 {
		view.NoRecipes = template.HTML(repl(html.EscapeString(c.t("meals.sugg.no_recipes", "link", "\x00")), "\x00", htmlLink(recipesPath, c.t("meals.recipes.button")))) // #nosec G203 -- the text is ours, the link escaped
	}
	if nr > 0 {
		if free := s.freeText(c.st); len(free) > 0 {
			var parts []string
			for _, f := range free {
				parts = append(parts, `<span class="who"><span class="marker" data-person="`+strconv.Itoa(f.Chip.Slot)+`" data-shape="`+html.EscapeString(f.Chip.Shape)+`" aria-hidden="true"></span>`+html.EscapeString(f.Chip.Name+":")+`</span> `+html.EscapeString(f.Text))
			}
			view.Unchecked = template.HTML(html.EscapeString(c.t("meals.sugg.unchecked")) + " " + strings.Join(parts, ", ")) // #nosec G203 -- names and texts escaped
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.execute(w, "mealSuggest", lang, view); err != nil {
		s.logger.Error("render meal suggestions", "err", err)
	}
}

// count is "1 recipe" or "3 recipes": key.one and key.other.
func (c *itemCtx) count(key string, n int) string {
	if n == 1 {
		return c.t(key + ".one")
	}
	return c.t(key+".other", "n", strconv.Itoa(n))
}
