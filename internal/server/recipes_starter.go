package server

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/starter"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The starter collection (ticket 290, ADR 0021): 200 recipes that ship with
// the box in German and English. They are not stored per household: each is
// built in the language of the person viewing it, read-only, with its
// source credit. A family's own recipes stay a list of their own.

const (
	starterPath     = recipesPath + "/starter"
	starterReadOnly = "starter recipes cannot be changed or deleted"
)

// starterRecipe is a starter recipe in the language of the viewer, shaped like a stored one.
func (c *itemCtx) starterRecipe(e starter.Entry) store.Recipe {
	return store.Recipe{Recipe: e.Recipe(c.lang)}
}

// recipeOrStarter finds a recipe of the household or a starter recipe; entry is
// set for a starter recipe. store.ErrNotFound when there is none.
func (c *itemCtx) recipeOrStarter(householdID, id string) (store.Recipe, *starter.Entry, error) {
	if starter.IsID(id) {
		e, ok := starter.ByID(id)
		if !ok {
			return store.Recipe{}, nil, store.ErrNotFound
		}
		return c.starterRecipe(e), &e, nil
	}
	rec, err := c.s.db.Recipe(householdID, id)
	return rec, nil, err
}

// markStarter adds to the API's answer what only a starter recipe has: its source named.
func (c *itemCtx) markStarter(j recipeJSON, e starter.Entry) recipeJSON {
	j.Starter, j.SourceName = true, &e.Site
	return j
}

// starterMatches are the starter recipes a search and a category keep, sorted A to Z by name.
func (c *itemCtx) starterMatches(q, cat string) []starter.Entry {
	type hit struct {
		e   starter.Entry
		key string
	}
	var hits []hit
	for _, e := range starter.All() {
		r := e.Recipe(c.lang)
		if (cat == "" || string(r.Category) == cat) && recipe.Matches(r, q) {
			hits = append(hits, hit{e, recipe.NameKey(r.Name)})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int { return strings.Compare(a.key, b.key) })
	out := make([]starter.Entry, len(hits))
	for i, h := range hits {
		out[i] = h.e
	}
	return out
}

func (s *server) handleStarterRecipes(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	c, err := s.itemContext(p, s.langOf(r))
	if err != nil {
		s.fail(w, "recipe context", err)
		return
	}
	q := strings.Join(strings.Fields(r.URL.Query().Get("q")), " ")
	cat := r.URL.Query().Get("category")
	if !recipe.ValidCategory(cat) {
		cat = ""
	}
	var rows []rcpRow
	for _, e := range c.starterMatches(q, cat) {
		rows = append(rows, c.row(c.starterRecipe(e)))
	}
	filters := []rcpFilter{{Href: listHrefAt(starterPath, q, ""), Text: c.t("recipes.filter.all"), Current: cat == ""}}
	for _, k := range recipe.Categories {
		filters = append(filters, rcpFilter{Href: listHrefAt(starterPath, q, string(k)), Symbol: k.Symbol(), Text: c.categoryName(k), Current: cat == string(k)})
	}
	data := map[string]any{
		"Lead": c.t("recipes.starter.lead", "n", strconv.Itoa(starter.Count())), "Rows": rows, "Q": q, "Cat": cat, "Filters": filters, "Searching": q != "" || cat != "",
		"FreeText": s.freeText(c.st), "ClearHref": listHrefAt(starterPath, "", cat), "AllHref": starterPath, "OwnHref": recipesPath,
		"Parent": auth.RoleAllows(p.person.Role, auth.Perm("meals", auth.Write)),
	}
	switch {
	case q != "":
		data["ResultTitle"] = c.t("recipes.res.search", "q", q)
		data["NoneText"] = c.t("recipes.none.search", "q", q)
	case cat != "":
		data["ResultTitle"] = c.categoryName(recipe.Category(cat))
		data["NoneText"] = c.t("recipes.none.cat", "cat", c.categoryName(recipe.Category(cat)))
	}
	s.render(w, r, http.StatusOK, "starter.html", data)
}

func (s *server) apiListStarterRecipes(w http.ResponseWriter, r *http.Request, p principal) {
	c, ok := s.apiRecipeContext(w, r, p)
	if !ok {
		return
	}
	out := []recipeJSON{}
	for _, e := range c.starterMatches(r.URL.Query().Get("q"), r.URL.Query().Get("category")) {
		out = append(out, c.markStarter(c.recipeToJSON(c.starterRecipe(e)), e))
	}
	writeJSON(w, http.StatusOK, out)
}
