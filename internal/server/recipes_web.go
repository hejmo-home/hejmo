package server

import (
	"errors"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/starter"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

// The recipe pages (ticket 279, ADR 0015: HTML routes with API parity):
// the list under "Essen", the recipe, and for parents the form and the delete
// button. Children read: they see the same list, the same recipe and the same
// warnings, without changing anything.

const recipesPath = "/meals/recipes"

var crumbRecipes = crumb{recipesPath, "recipes.title", ""}

// rcpRow is one row of the list.
type rcpRow struct {
	ID, Symbol, Name string
	Meta             []string
	Diet             string
	Tags             []string
	Warns            []template.HTML
	Unchecked        string
}

func (c *itemCtx) t(key string, kv ...string) string {
	return c.s.i18n.Format(c.lang, i18n.Msg(key, kv...))
}

func (c *itemCtx) categoryName(cat recipe.Category) string { return c.t("recipes.cat." + string(cat)) }

// minutesShort is "45 Min." or "2 Std. 30 Min.".
func (c *itemCtx) minutesShort(n int) string {
	h, m := recipe.HoursMinutes(n)
	switch {
	case h == 0:
		return c.t("recipes.meta.min", "n", strconv.Itoa(m))
	case m == 0:
		return c.t("recipes.meta.hours", "h", strconv.Itoa(h))
	}
	return c.t("recipes.meta.hours.min", "h", strconv.Itoa(h), "m", strconv.Itoa(m))
}

// minutesLong is "45 Minuten" or "2 Std. 30 Min.".
func (c *itemCtx) minutesLong(n int) string {
	h, m := recipe.HoursMinutes(n)
	switch {
	case h == 0 && m == 1:
		return c.t("recipes.time.one")
	case h == 0:
		return c.t("recipes.time.min", "n", strconv.Itoa(m))
	case m == 0:
		return c.t("recipes.time.hours", "h", strconv.Itoa(h))
	}
	return c.t("recipes.time.hours.min", "h", strconv.Itoa(h), "m", strconv.Itoa(m))
}

func (c *itemCtx) portionsText(n int) string {
	if n == 1 {
		return c.t("recipes.meta.portions.one")
	}
	return c.t("recipes.meta.portions.other", "n", strconv.Itoa(n))
}

func (c *itemCtx) dietWord(mark string) string { return c.t("recipes.diet." + mark) }

// names joins names in words: "A, B and C".
func (c *itemCtx) names(list []string) string { return c.s.joinWords(c.lang, list) }

func (c *itemCtx) row(r store.Recipe) rcpRow {
	out := rcpRow{ID: r.ID, Symbol: r.Category.Symbol(), Name: r.Name, Tags: r.Tags}
	out.Meta = append(out.Meta, c.categoryName(r.Category))
	if r.Minutes > 0 {
		out.Meta = append(out.Meta, c.minutesShort(r.Minutes))
	}
	out.Meta = append(out.Meta, c.portionsText(r.Portions))
	out.Diet = recipe.DietOf(r.Ingredients, c.lookup).Mark
	if out.Diet != "" {
		out.Diet = c.dietWord(out.Diet)
	}
	out.Warns = c.warnLines(c.warnings(r.Recipe))
	if free := c.freeNames(r.Recipe); len(free) > 0 {
		out.Unchecked = c.t("recipes.unchecked.list", "names", strings.Join(free, ", "))
	}
	return out
}

type rcpFilter struct {
	Href, Symbol, Text string
	Current            bool
}

// listHref is the address of the list with a search and a category.
func listHref(q, cat string) string { return listHrefAt(recipesPath, q, cat) }

// listHrefAt is the address of a list page (own recipes or the starter collection).
func listHrefAt(base, q, cat string) string {
	v := url.Values{}
	if q != "" {
		v.Set("q", q)
	}
	if cat != "" {
		v.Set("category", cat)
	}
	if len(v) == 0 {
		return base
	}
	return base + "?" + v.Encode()
}

func (s *server) handleRecipes(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	c, err := s.itemContext(p, lang)
	if err != nil {
		s.fail(w, "recipe context", err)
		return
	}
	list, err := s.db.Recipes(p.household.ID)
	if err != nil {
		s.fail(w, "recipes", err)
		return
	}
	q := strings.Join(strings.Fields(r.URL.Query().Get("q")), " ")
	cat := r.URL.Query().Get("category")
	if !recipe.ValidCategory(cat) {
		cat = ""
	}
	var rows []rcpRow
	for _, x := range list {
		if (cat == "" || string(x.Category) == cat) && recipe.Matches(x.Recipe, q) {
			rows = append(rows, c.row(x))
		}
	}
	parent := auth.RoleAllows(p.person.Role, auth.Perm("meals", auth.Write))
	filters := []rcpFilter{{Href: listHref(q, ""), Text: c.t("recipes.filter.all"), Current: cat == ""}}
	for _, k := range recipe.Categories {
		filters = append(filters, rcpFilter{Href: listHref(q, string(k)), Symbol: k.Symbol(), Text: c.categoryName(k), Current: cat == string(k)})
	}
	data := map[string]any{
		"Rows": rows, "Q": q, "Cat": cat, "Parent": parent, "Filters": filters, "Any": len(list) > 0, "Searching": q != "" || cat != "",
		"FreeText": s.freeText(c.st), "ClearHref": listHref("", cat), "AllHref": recipesPath, "NewHref": recipesPath + "/new",
		"StarterHref": starterPath, "StarterLink": c.t("recipes.starter.link", "n", strconv.Itoa(starter.Count())),
	}
	switch {
	case q != "":
		data["ResultTitle"] = c.t("recipes.res.search", "q", q)
		data["NoneText"] = c.t("recipes.none.search", "q", q)
		data["NewFromSearch"] = c.t("recipes.none.search.create", "q", q)
		data["NewFromSearchHref"] = recipesPath + "/new?" + url.Values{"name": {q}}.Encode()
	case cat != "":
		data["ResultTitle"] = c.categoryName(recipe.Category(cat))
		data["NoneText"] = c.t("recipes.none.cat", "cat", c.categoryName(recipe.Category(cat)))
	}
	s.render(w, r, http.StatusOK, "recipes.html", data)
}

// rcpIngView is one ingredient of the recipe page.
type rcpIngView struct {
	Qty, Name, Variety string
	Free               bool
	Warns              []template.HTML
}

// qtyScaled is the quantity of an ingredient for n portions of a recipe written for base: as written for
// the recipe's own portions, else rounded to one decimal (never to a whole number, at least 0.1). The
// unit stays the one written: kitchen and container measures scale but are never converted.
func (c *itemCtx) qtyScaled(in recipe.Ingredient, base, n int) string {
	if in.Amount <= 0 || n == base {
		return c.qtyText(in)
	}
	v := recipe.Scale(in.Amount, base, n)
	out := recipe.FormatScaled(v, c.lang)
	if in.Unit == "" {
		return out
	}
	return out + " " + c.unitWord(in.Unit, v != 1)
}

// qtyText is "600 g", "1 Stück" or "3 EL"; nothing for an ingredient without a quantity.
func (c *itemCtx) qtyText(in recipe.Ingredient) string {
	if in.Amount <= 0 {
		return ""
	}
	n := recipe.FormatAmount(in.Amount, c.lang)
	if in.Unit == "" {
		return n
	}
	return n + " " + c.unitWord(in.Unit, recipe.FormatAmountNumber(in.Amount) != 1)
}

func (s *server) handleRecipePage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	c, err := s.itemContext(p, lang)
	if err != nil {
		s.fail(w, "recipe context", err)
		return
	}
	rec, entry, err := c.recipeOrStarter(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "recipe", err)
		return
	}
	parent := auth.RoleAllows(p.person.Role, auth.Perm("meals", auth.Write)) && entry == nil
	n, plan := rec.Portions, ""
	if v, ok := parsePortions(r.URL.Query().Get("portions")); ok {
		n = v
	}
	var planDay, planMeal string
	if m := planPattern.FindStringSubmatch(r.URL.Query().Get("plan")); m != nil {
		if _, err := time.Parse(task.DateLayout, m[1]); err == nil {
			planDay, planMeal, plan = m[1], m[2], m[1]+"/"+m[2]
		}
	}
	data := map[string]any{
		"R": rec, "Symbol": rec.Category.Symbol(), "Title": rec.Name, "Parent": parent, "CategoryName": c.categoryName(rec.Category),
		"PortionsText": c.t("recipes.facts.portions_written", "n", strconv.Itoa(rec.Portions)), "PV": s.portionsView(c, p, rec, n, planDay, planMeal, plan),
		"Tags": rec.Tags, "Tips": rec.Tips, "Source": rec.Source, "SourceLink": recipe.SourceLink(rec.Source),
		"Steps": rec.Steps, "Warns": c.warnLines(c.warnings(rec.Recipe)), "HasWarn": len(c.warnings(rec.Recipe)) > 0,
		"FreeText": s.freeText(c.st), "EditHref": recipesPath + "/" + rec.ID + "/edit",
	}
	if entry != nil {
		data["Starter"] = c.t("recipes.starter.note")
		data["SourceName"] = c.t("recipes.starter.credit", "site", entry.Site)
	}
	if rec.Portions == 1 {
		data["IngsFor"] = c.t("recipes.ings.for.one")
	}
	if rec.Minutes > 0 {
		data["Time"] = c.minutesLong(rec.Minutes)
	}
	if len(rec.Mealtimes) > 0 {
		data["Mealtimes"] = rec.Mealtimes
	}
	d := recipe.DietOf(rec.Ingredients, c.lookup)
	if d.Mark != "" || len(d.Prevents) > 0 {
		data["DietWhy"] = c.dietWhy(d)
		if d.Mark != "" {
			data["Diet"] = c.dietWord(d.Mark)
		}
	}
	if n := c.nutritionView(rec.Recipe); n != nil {
		data["Nut"] = n
	}
	free := c.freeNames(rec.Recipe)
	if len(free) > 0 {
		key := "recipes.check.unchecked.many"
		if len(free) == 1 {
			key = "recipes.check.unchecked.one"
		}
		data["UncheckedText"] = c.t(key, "names", strings.Join(free, ", "))
	}
	var ings []rcpIngView
	for _, in := range rec.Ingredients {
		v := rcpIngView{Qty: c.qtyScaled(in, rec.Portions, n), Name: in.Text, Variety: in.Variety}
		if a, ok := c.lookup(in.ArticleID); in.ArticleID != "" && ok {
			v.Warns = c.warnLines(library.Warnings(a, c.st.persons))
		} else {
			v.Free = true
		}
		ings = append(ings, v)
	}
	data["Ings"] = ings
	if parent {
		data["Who"] = c.whoText(rec)
		data["DeleteExplain"] = c.t("recipes.delete.explain", "name", rec.Name)
		data["DeleteConfirm"] = c.t("recipes.delete.confirm", "name", rec.Name)
		planned, err := s.plannedText(c, p, lang, rec)
		if err != nil {
			s.fail(w, "planned meals", err)
			return
		}
		data["DeletePlanned"] = planned
	}
	s.render(w, r, http.StatusOK, "recipe.html", data)
}

// planPattern is the origin of the portions of a recipe page: a day and a mealtime of the meal plan.
var planPattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})/(breakfast|lunch|snack|dinner)$`)

// portionsView is the "For - 6 + portions" line of the recipe page and the sentence under it: where the
// number comes from, what the recipe was written for, that quantities are rounded to one decimal, and
// the way back to the recipe as written. The steps are links, the server does the sums.
type portionsView struct {
	N                    int
	Less, More           string
	LessOff, MoreOff     bool
	Hint, AsWritten      string
	AsWrittenHref        string
	LessLabel, MoreLabel string
}

func (s *server) portionsView(c *itemCtx, p principal, rec store.Recipe, n int, planDay, planMeal, plan string) portionsView {
	href := func(to int) string {
		v := url.Values{}
		if to != rec.Portions || plan != "" {
			v.Set("portions", strconv.Itoa(to))
		}
		out := recipesPath + "/" + rec.ID
		if len(v) > 0 {
			out += "?portions=" + v.Get("portions")
		}
		if plan != "" {
			if len(v) == 0 {
				out += "?plan=" + plan
			} else {
				out += "&plan=" + plan
			}
		}
		return out
	}
	pv := portionsView{N: n, LessOff: n <= 1, MoreOff: n >= recipe.MaxPortions, LessLabel: c.t("meals.rcp.portions.less"), MoreLabel: c.t("meals.rcp.portions.more")}
	pv.Less, pv.More = href(max(n-1, 1)), href(min(n+1, recipe.MaxPortions))
	hint := c.t("recipes.portions.hint", "portions", c.portionsText(rec.Portions))
	if planDay != "" {
		hint = c.t("recipes.portions.from_plan."+planMeal, "when", s.whenText(p, c.lang, planDay)) + " " + hint
	}
	pv.Hint = hint
	if n != rec.Portions || plan != "" {
		pv.AsWritten = c.t("recipes.portions.as_written", "portions", c.portionsText(rec.Portions))
		pv.AsWrittenHref = recipesPath + "/" + rec.ID
	}
	return pv
}

// plannedText is the sentence of the delete box that lists the coming meal plan entries of a recipe
// ("on <b>Tuesday, 24.11., Dinner</b> and on ..."), HTML with the names escaped; empty when none.
func (s *server) plannedText(c *itemCtx, p principal, lang string, rec store.Recipe) (template.HTML, error) {
	planned, err := s.db.PlannedMealsOfRecipe(p.household.ID, rec.ID, s.today(p))
	if err != nil || len(planned) == 0 {
		return "", err
	}
	var items []string
	for _, m := range planned {
		when := s.whenText(p, lang, m.Day) + ", " + s.i18n.T(lang, "meals.mealtime."+string(m.Mealtime))
		items = append(items, repl(html.EscapeString(c.t("recipes.delete.planned.item", "when", "\x00")), "\x00", "<b>"+html.EscapeString(when)+"</b>"))
	}
	return template.HTML(repl(html.EscapeString(c.t("recipes.delete.planned", "list", "\x00")), "\x00", s.joinWords(lang, items))), nil // #nosec G203 -- names escaped, the text is ours
}

// dietWhy says why a recipe has its diet mark, or none, and what Hejmo does not check.
func (c *itemCtx) dietWhy(d recipe.Diet) string {
	var out string
	switch {
	case d.Mark == recipe.MarkVegan:
		out = c.t("recipes.diet.why.vegan")
	case d.Mark == recipe.MarkVegetarian:
		out = c.t("recipes.diet.why.vegetarian", "names", strings.Join(d.Prevents, ", "))
	default:
		out = c.t("recipes.diet.why.none", "names", strings.Join(d.Prevents, ", "))
	}
	if len(d.Free) > 0 {
		key := "recipes.diet.free.many"
		if len(d.Free) == 1 {
			key = "recipes.diet.free.one"
		}
		out += " " + c.t(key, "names", c.names(d.Free))
	}
	return out
}

// whoText says who wrote a recipe and who changed it last, to the second.
func (c *itemCtx) whoText(r store.Recipe) string {
	out := c.t("recipes.who.created", "name", r.CreatedByName, "when", c.p.dateTimeSeconds(r.CreatedAt))
	if r.CreatedByName == "" {
		out = c.t("recipes.who.created.anon", "when", c.p.dateTimeSeconds(r.CreatedAt))
	}
	if r.UpdatedAt.After(r.CreatedAt) {
		if r.UpdatedByName == "" {
			out += " " + c.t("recipes.who.changed.anon", "when", c.p.dateTimeSeconds(r.UpdatedAt))
		} else {
			out += " " + c.t("recipes.who.changed", "name", r.UpdatedByName, "when", c.p.dateTimeSeconds(r.UpdatedAt))
		}
	}
	return out
}

func (s *server) handleRecipeDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	rec, err := s.db.Recipe(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "recipe", err)
		return
	}
	if err := s.db.DeleteRecipe(p.household.ID, rec.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, "delete recipe", err)
		return
	}
	s.redirectSavedNamed(w, r, recipesPath, "flash.recipe_deleted", rec.Name)
}

// htmlLink is a link with escaped text, for a sentence the page builds.
func htmlLink(href, text string) string {
	return `<a href="` + html.EscapeString(href) + `">` + html.EscapeString(text) + `</a>`
}
