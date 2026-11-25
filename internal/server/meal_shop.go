package server

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/shop"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// Ingredients of planned recipes on a list, by hand (ticket 283, ADR 0019 and
// 0021, docs/design/preview-283.html). The rule is internal/shop; this file
// finds the meals, the list and the recipes, and says in words what will
// happen. The web page and the API share it.

const shopPath = "/meals/shop"

// shopSelection is what the page is about: one meal, or the days of a week from today on.
type shopSelection struct {
	Week   bool
	Meal   string    // "<day>.<mealtime>"
	Monday time.Time // for a week
}

func parseMealKey(key string) (string, meal.Mealtime, bool) {
	day, mt, ok := strings.Cut(key, ".")
	if _, err := time.Parse(task.DateLayout, day); err != nil || !ok || !meal.Mealtime(mt).Valid() {
		return "", "", false
	}
	return day, meal.Mealtime(mt), true
}

// shopCand is a meal of the plan that has a recipe.
type shopCand struct {
	Key, Day string
	Mealtime meal.Mealtime
	Rec      store.Recipe
	Starter  bool
	Version  string
	Planned  int
	OnList   int // portions that are on a list already
	ListName string
	RunDay   string
	Template string
	Add      int // portions to put on now
	Checked  bool
	Gone     bool
}

// Status is "empty" (the recipe has no ingredients), "done" (all portions are on a list) or "open".
func (m shopCand) Status() string {
	switch {
	case len(m.Rec.Ingredients) == 0:
		return "empty"
	case m.Add <= 0:
		return "done"
	}
	return "open"
}

// recipeVersion changes with every edit of the recipe's ingredients or time of change.
func recipeVersion(rec store.Recipe, starterRecipe bool) string {
	if starterRecipe {
		return "starter"
	}
	h := sha1.New()
	for _, in := range rec.Ingredients {
		fmt.Fprintf(h, "%v|%s|%s|%s|%s\n", in.Amount, in.Unit, in.Text, in.ArticleID, in.Variety)
	}
	fmt.Fprintf(h, "%d", rec.Portions)
	return rec.UpdatedAt.UTC().Format(time.RFC3339) + "-" + hex.EncodeToString(h.Sum(nil))[:8]
}

// shopCandidates are the meals with a recipe the selection names.
func (c *itemCtx) shopCandidates(sel shopSelection) ([]shopCand, error) {
	s, p := c.s, c.p
	today := s.today(p)
	var from, to string
	if sel.Week {
		from, to = sel.Monday.Format(task.DateLayout), sel.Monday.AddDate(0, 0, 6).Format(task.DateLayout)
		if from < today {
			from = today
		}
	} else {
		day, _, ok := parseMealKey(sel.Meal)
		if !ok {
			return nil, store.ErrNotFound
		}
		from, to = day, day
	}
	if from > to {
		return nil, nil
	}
	plan, err := s.db.MealPlan(p.household.ID, from, to, today)
	if err != nil {
		return nil, err
	}
	onList, err := s.db.ShopMeals(p.household.ID, from, to)
	if err != nil {
		return nil, err
	}
	var out []shopCand
	for _, m := range plan {
		key := m.Day + "." + string(m.Mealtime)
		if m.RecipeID == "" || (!sel.Week && key != sel.Meal) {
			continue
		}
		rec, st, err := c.recipeOrStarter(p.household.ID, m.RecipeID)
		if err != nil {
			continue // the recipe is gone since: this is not a recipe meal any more
		}
		cand := shopCand{Key: key, Day: m.Day, Mealtime: m.Mealtime, Rec: rec, Starter: st != nil, Planned: m.Portions, Add: m.Portions}
		cand.Version = recipeVersion(rec, cand.Starter)
		if m.Source == store.SourceTemplate {
			cand.Template = m.TemplateName
		}
		for _, o := range onList {
			if o.Day == m.Day && o.Mealtime == m.Mealtime && o.RecipeID == m.RecipeID {
				cand.OnList, cand.ListName, cand.RunDay = o.Portions, o.ListName, o.RunDay
			}
		}
		if cand.OnList > 0 {
			cand.Add = max(cand.Planned-cand.OnList, 0)
		}
		out = append(out, cand)
	}
	return out, nil
}

// shopWas is a row as the page showed it before, for what changed in the recipe since.
type shopWas struct {
	Base float64
	Unit string
	Name string
}

// shopIn is what the page or the request asks to see.
type shopIn struct {
	Sel      shopSelection
	ListID   string
	Posted   bool            // the ticks below are the person's
	Meals    map[string]bool // ticked meals of a week; nil: all
	Picks    map[string]bool
	Was      map[string]shopWas
	Versions map[string]string // recipe id to the version the page was made from
}

// shopRowView is one row of the tick list.
type shopRowView struct {
	Key, Name, Symbol, Qty, Src, Tag string
	Free, Checked, Pantry, New       bool
	How                              template.HTML
	Action                           list.AddAction
}

type shopMealView struct {
	Key, Name, Symbol, When, Detail, Link string
	Checked, Static, Empty                bool
	Add, Planned, OnList                  int
	Recipe                                string
}

type shopListChip struct {
	ID, Name string
	Slot     int
	Symbol   string
	Chosen   bool
}

// shopPage is everything the tick list shows, for the page and the API.
type shopPage struct {
	Sel                                     shopSelection
	Lists                                   []shopListChip
	ListID                                  string
	ListName                                string
	ListGone                                string // the name the person had chosen, if that list is gone
	NoList                                  bool
	Meals                                   []shopMealView
	MealCands                               []shopCand
	Selected                                []shopCand
	Head                                    shopHead
	Rows                                    []shopRowView
	New, On, Pantry                         []shopRowView
	Same                                    []template.HTML
	OnN, SameN                              int
	Gone                                    []string
	Changed                                 []string // names of the recipes that changed since the page was made
	ChangedAt                               string
	Missing                                 []string // meals that are not planned any more
	Versions                                []string // "recipe|version" for the hidden fields
	WasRows                                 []string
	Ticked, TickedNew, TickedOn, PantryLeft int
	MealsLeft                               []string
	Sentence                                template.HTML
	Button                                  string
	Patterns                                map[string]string
	Empty                                   string // "", "nolist", "noingredients"
	Name, Symbol, WhenHead, Portions        string
	Scaled                                  string
	rows                                    []shop.Row
	steps                                   []shop.Step
}

type shopHead struct{ Title string }

// weekdayShort is "Di" or "Tue".
func (c *itemCtx) weekdayShort(day string) string {
	d, _ := time.Parse(task.DateLayout, day)
	return c.t("shop.day." + strconv.Itoa(int(d.Weekday())))
}

func (c *itemCtx) dayLong(day string) string {
	d, _ := time.Parse(task.DateLayout, day)
	return c.t("meals.weekday."+strconv.Itoa(int(d.Weekday()))) + ", " + datefmt.Short(c.p.household.DateFormat, d)
}

func (c *itemCtx) plural(key string, n int, kv ...string) string {
	form := ".other"
	if n == 1 {
		form = ".one"
	}
	return c.t(key+form, append([]string{"n", strconv.Itoa(n)}, kv...)...)
}

// shopResolve is the article an ingredient names, as the list needs it.
func (c *itemCtx) shopResolve(in recipe.Ingredient) shop.Article {
	if in.ArticleID != "" {
		if it, ok := c.article(in.ArticleID); ok {
			return shop.Article{ID: it.ID, Text: it.Name(c.lang), Pantry: it.Pantry}
		}
	}
	return shop.Article{Text: in.Text}
}

func (c *itemCtx) shopShow(r shop.Row) list.Show {
	var it *articleItem
	if r.ArticleID != "" {
		if a, ok := c.article(r.ArticleID); ok {
			it = &a
		}
	}
	return c.show(it)
}

// shopShown is the quantity of a row in the unit it reads best in.
func (c *itemCtx) shopShown(r shop.Row, q list.Quantity) list.Quantity {
	if q.None() {
		return q
	}
	return list.Quantity{Base: q.Base, Unit: c.shopShow(r)(q.Base, q.Kind())}
}

// qtyShare is a quantity as one meal's share: "300 ml", and "2" for pieces.
func (c *itemCtx) qtyShare(q list.Quantity) string {
	if q.None() {
		return ""
	}
	if q.Unit == unit.Piece {
		return unit.Format(q.Amount(), q.Unit, c.lang)
	}
	return c.qtyWords(q)
}

// sharePart is what one meal brought to one entry.
type sharePart struct {
	Recipe, Day string
	Qty         list.Quantity
}

// shareLine is the line under an entry that says which meals it is for. Prior
// is what was there before; handChanged says the quantity was changed by hand,
// so only the names are said.
func (c *itemCtx) shareLine(parts []sharePart, prior list.Quantity, handChanged bool) string {
	if len(parts) == 0 {
		return ""
	}
	name := func(p sharePart) string { return p.Recipe + " (" + c.weekdayShort(p.Day) + ")" }
	anyQty := false
	names := make([]string, len(parts))
	for i, p := range parts {
		anyQty = anyQty || !p.Qty.None()
		names[i] = name(p)
	}
	if handChanged || !anyQty {
		return c.t("shop.src.for.cap", "name", strings.Join(names, " + "))
	}
	items := make([]string, len(parts))
	for i, p := range parts {
		items[i] = c.t("shop.src.for", "name", names[i])
		if !p.Qty.None() {
			items[i] = c.qtyShare(p.Qty) + " " + items[i]
		}
	}
	line := strings.Join(items, " + ")
	if !prior.None() {
		line = c.t("shop.src.prior", "qty", c.qtyWords(prior)) + " + " + line
	}
	return line
}

// buildShop works out the page: meals, rows, outcomes, counts and sentences.
func (c *itemCtx) buildShop(in shopIn) (*shopPage, error) {
	s, p := c.s, c.p
	pg := &shopPage{Sel: in.Sel}
	cands, err := c.shopCandidates(in.Sel)
	if err != nil {
		return nil, err
	}
	pg.MealCands = cands
	// what changed in the recipes since the page was made
	changedRec := map[string]bool{}
	for _, m := range cands {
		if v, ok := in.Versions[m.Rec.ID]; ok && v != m.Version && m.Status() != "empty" {
			if !slices.Contains(pg.Changed, m.Rec.Name) {
				pg.Changed = append(pg.Changed, m.Rec.Name)
				pg.ChangedAt = c.t("shop.changed.at", "time", p.clockSeconds(m.Rec.UpdatedAt)[:5])
			}
			changedRec[m.Rec.ID] = true
		}
		pg.Versions = append(pg.Versions, m.Rec.ID+"|"+m.Version)
	}
	// the meals
	for i := range cands {
		m := &cands[i]
		m.Checked = m.Status() == "open" && (in.Meals == nil || in.Meals[m.Key])
		if !in.Sel.Week {
			m.Checked = m.Status() == "open"
		}
		if m.Checked {
			pg.Selected = append(pg.Selected, *m)
		}
		pg.Meals = append(pg.Meals, c.shopMealView(*m))
	}
	for _, m := range cands {
		if m.Status() == "open" && !m.Checked {
			pg.MealsLeft = append(pg.MealsLeft, m.Rec.Name)
		}
	}
	// the lists
	lists, err := s.db.Lists(p.household.ID)
	if err != nil {
		return nil, err
	}
	var offered []list.List
	for _, l := range lists {
		if l.Suggestions {
			offered = append(offered, l)
		}
	}
	chosen := in.ListID
	if chosen != "" && !slices.ContainsFunc(offered, func(l list.List) bool { return l.ID == chosen }) {
		pg.ListGone = chosen
		chosen = ""
	} else if chosen == "" && !in.Posted && len(offered) > 0 {
		if last, _ := s.db.ShopLastList(p.household.ID); slices.ContainsFunc(offered, func(l list.List) bool { return l.ID == last }) {
			chosen = last
		} else {
			chosen = offered[0].ID
		}
	}
	pg.ListID = chosen
	pg.NoList = len(offered) == 0
	for _, l := range offered {
		pg.Lists = append(pg.Lists, shopListChip{ID: l.ID, Name: l.Name, Slot: list.Slot(l.Color), Symbol: l.Symbol, Chosen: l.ID == chosen})
		if l.ID == chosen {
			pg.ListName = l.Name
		}
	}
	// rows
	var meals []shop.Meal
	for _, m := range pg.Selected {
		meals = append(meals, shop.Meal{Key: m.Key, Day: m.Day, Mealtime: m.Mealtime, RecipeID: m.Rec.ID, RecipeName: m.Rec.Name, Portions: m.Add, Base: m.Rec.Portions, Ingredients: m.Rec.Ingredients})
	}
	pg.rows = shop.Rows(meals, c.shopResolve)
	var entries []list.Entry
	if chosen != "" {
		if entries, err = s.db.Entries(p.household.ID, chosen); err != nil {
			return nil, err
		}
	}
	pg.steps = shop.Plan(entries, pg.rows, c.shopShow)
	multi := in.Sel.Week || len(pg.Selected) > 1
	seen := map[string]bool{}
	for _, st := range pg.steps {
		seen[st.Row.Key] = true
		rv := c.shopRow(st, entries, multi, pg.Selected)
		if st.Plan.Action == list.Exists {
			pg.Same = append(pg.Same, c.sameSentence(st, entries))
			pg.SameN++
			continue
		}
		switch {
		case in.Posted:
			rv.Checked = in.Picks[st.Row.Key]
			if _, was := in.Was[st.Row.Key]; !was && len(in.Was) > 0 {
				rv.Checked = !rv.Pantry // a row that was not on the page before comes ticked
			}
		default:
			rv.Checked = !rv.Pantry
		}
		if w, ok := in.Was[st.Row.Key]; ok && c.rowInChanged(st.Row, changedRec, pg.Selected) {
			if q := (list.Quantity{Base: w.Base, Unit: w.Unit}); q.Base != st.Row.Qty.Base || q.Unit != st.Row.Qty.Unit {
				rv.Tag = c.t("shop.tag.was", "qty", c.wasWords(q, rv.Free))
			}
		} else if !ok && len(changedRec) > 0 && len(in.Was) > 0 && c.rowInChanged(st.Row, changedRec, pg.Selected) {
			rv.Tag = c.t("shop.tag.new")
		}
		pg.Rows = append(pg.Rows, rv)
		switch {
		case rv.Pantry:
			pg.Pantry = append(pg.Pantry, rv)
		case rv.New:
			pg.New = append(pg.New, rv)
		default:
			pg.On = append(pg.On, rv)
		}
		if rv.Checked {
			pg.Ticked++
			if rv.New {
				pg.TickedNew++
			} else {
				pg.TickedOn++
			}
		} else if rv.Pantry {
			pg.PantryLeft++
		}
		pg.WasRows = append(pg.WasRows, fmt.Sprintf("%s|%v|%s|%s", st.Row.Key, st.Row.Qty.Base, st.Row.Qty.Unit, st.Row.Text))
	}
	for key, w := range in.Was {
		if !seen[key] && len(changedRec) > 0 {
			pg.Gone = append(pg.Gone, w.Name)
		}
	}
	slices.Sort(pg.Gone)
	c.shopWords(pg)
	return pg, nil
}

func (c *itemCtx) rowInChanged(r shop.Row, changed map[string]bool, sel []shopCand) bool {
	for _, p := range r.Parts {
		for _, m := range sel {
			if m.Key == p.Meal && changed[m.Rec.ID] {
				return true
			}
		}
	}
	return false
}

func (c *itemCtx) wasWords(q list.Quantity, free bool) string {
	if q.None() {
		return c.t("shop.tag.was.none")
	}
	return c.qtyWords(q)
}

func (c *itemCtx) shopMealView(m shopCand) shopMealView {
	v := shopMealView{Key: m.Key, Name: m.Rec.Name, Symbol: m.Rec.Category.Symbol(), Checked: m.Checked, Add: m.Add, Planned: m.Planned, OnList: m.OnList, Recipe: m.Rec.ID}
	d, _ := time.Parse(task.DateLayout, m.Day)
	v.When = c.dayLong(m.Day) + ", " + c.t("meals.mealtime."+string(m.Mealtime))
	_ = d
	parts := []string{v.When}
	switch m.Status() {
	case "empty":
		v.Static, v.Empty = true, true
		v.Link = recipesPath + "/" + m.Rec.ID + "/edit"
		if m.Starter {
			v.Link = recipesPath + "/" + m.Rec.ID
		}
	case "done":
		v.Static = true
		parts = nil
		v.Detail = c.t("shop.meal.done", "list", m.ListName, "day", c.runDay(m.RunDay), "portions", c.plural("shop.meal.portions", m.OnList))
		v.When = c.dayLong(m.Day) + ", " + c.t("meals.mealtime."+string(m.Mealtime))
	default:
		if m.OnList > 0 {
			parts = append(parts, c.t("shop.meal.missing", "add", strconv.Itoa(m.Add), "total", strconv.Itoa(m.Planned), "n", strconv.Itoa(m.OnList), "day", c.runDay(m.RunDay), "list", m.ListName))
		} else {
			parts = append(parts, c.plural("shop.meal.portions", m.Planned))
		}
		if m.Template != "" {
			parts = slices.Insert(parts, 1, c.t("shop.meal.template", "name", m.Template))
		}
		if m.Starter {
			parts = slices.Insert(parts, 1, c.t("shop.meal.starter"))
		}
		v.Detail = strings.Join(parts, " · ")
		return v
	}
	return v
}

// runDay says when a run happened: "heute" or "Montag, 23.11.".
func (c *itemCtx) runDay(day string) string {
	if day == c.s.today(c.p) {
		return c.t("shop.today")
	}
	return c.dayLong(day)
}

func (c *itemCtx) shopRow(st shop.Step, entries []list.Entry, multi bool, sel []shopCand) shopRowView {
	r := st.Row
	art := (*articleItem)(nil)
	if r.ArticleID != "" {
		if a, ok := c.article(r.ArticleID); ok {
			art = &a
		}
	}
	rv := shopRowView{Key: r.Key, Name: entryLabel(r.Text, r.Variety), Free: r.ArticleID == "", Pantry: r.Pantry, Action: st.Plan.Action}
	if art != nil {
		rv.Symbol = art.Symbol
	}
	shown := c.shopShown(r, r.Qty)
	if !shown.None() {
		rv.Qty = c.qtyWords(shown)
	}
	rv.New = st.Plan.Action == list.AddedNew && st.Plan.After == ""
	kitchen := c.kitchenText(r, multi)
	how := ""
	switch {
	case st.Plan.Action == list.AddedTo:
		how = string(c.htmlMsg("lq.merge.same", "now", c.boldQty(st.Plan.Before), "add", c.boldQty(shown), "total", c.boldQty(st.Plan.Result)))
	case st.Plan.Action == list.Filled:
		how = string(c.htmlMsg("shop.how.filled", "qty", c.boldQty(st.Plan.Result)))
	case st.Plan.Action == list.AddedNew && st.Plan.After != "":
		var old list.Quantity
		for _, e := range entries {
			if e.ID == st.Plan.After {
				old = e.Qty
			}
		}
		if !old.None() {
			how = string(c.htmlMsg("shop.how.other", "now", c.boldQty(old), "add", html.EscapeString(c.qtyWords(shown)), "a", html.EscapeString(c.unitWord(old.Unit, true)), "b", html.EscapeString(c.unitWord(shown.Unit, true))))
		}
	case r.Pantry && kitchen != "":
		how = html.EscapeString(c.t("shop.how.recipe", "text", kitchen))
	case kitchen != "" && r.ArticleID == "":
		how = html.EscapeString(c.t("shop.how.free.none", "text", kitchen))
	case kitchen != "":
		how = html.EscapeString(c.t("shop.how.kitchen", "text", kitchen))
	case r.ArticleID == "" && !r.Pantry:
		how = html.EscapeString(c.t("shop.how.free"))
	}
	if r.ArticleID == "" && kitchen != "" && !r.Pantry && st.Plan.Action == list.AddedNew && st.Plan.After == "" {
		how += " " + html.EscapeString(c.t("shop.how.unchecked"))
	}
	rv.How = template.HTML(how)
	if multi || r.Pantry {
		var parts []sharePart
		for _, pt := range r.Parts {
			for _, m := range sel {
				if m.Key == pt.Meal {
					parts = append(parts, sharePart{Recipe: m.Rec.Name, Day: m.Day, Qty: c.shopShown(r, pt.Qty)})
				}
			}
		}
		if !(r.Pantry && kitchen != "") {
			rv.Src = c.shareLine(parts, list.Quantity{}, false)
		}
		if st.Plan.Action == list.AddedNew && len(sel) == 1 {
			rv.Src = ""
		}
	}
	return rv
}

// kitchenText is what the recipe says in kitchen measures: "3 EL", "1 Prise (Di), 1 Prise (Do)" for several meals.
func (c *itemCtx) kitchenText(r shop.Row, multi bool) string {
	var out []string
	for _, p := range r.Parts {
		if p.Kitchen == "" {
			continue
		}
		q := recipe.FormatScaled(p.KitchenAmount, c.lang) + " " + c.unitWord(p.Kitchen, p.KitchenAmount != 1)
		if multi {
			day, _, _ := parseMealKey(p.Meal)
			q += " (" + c.weekdayShort(day) + ")"
		}
		out = append(out, q)
	}
	return strings.Join(out, ", ")
}

// sameSentence is the piece of the sentence about an entry that stays as it is.
func (c *itemCtx) sameSentence(st shop.Step, entries []list.Entry) template.HTML {
	var detail []string
	if !st.Plan.Target.Qty.None() {
		detail = append(detail, c.qtyWords(st.Plan.Target.Qty))
	}
	if k := c.kitchenText(st.Row, false); k != "" {
		detail = append(detail, c.t("shop.same.recipe", "text", k))
	} else if st.Plan.Target.Qty.None() {
		detail = append(detail, c.t("shop.same.noqty"))
	}
	return template.HTML("<b>" + html.EscapeString(entryLabel(st.Row.Text, st.Row.Variety)) + "</b> (" + html.EscapeString(strings.Join(detail, ", ")) + ")")
}

// shopWords makes the count sentence, the button text and the patterns the script fills in.
func (c *itemCtx) shopWords(pg *shopPage) {
	on := pg.TickedOn + pg.SameN
	list := pg.ListName
	pat := map[string]string{}
	bold := func(s string) string { return "<b>" + html.EscapeString(s) + "</b>" }
	if list != "" {
		pat["head.many"] = c.t("shop.sum.head", "n", "{n}", "list", list)
		pat["head.one"] = c.t("shop.sum.head.one", "list", list)
		pat["head.nothing"] = c.t("shop.sum.head.nothing", "list", list)
		pat["head.none"] = c.t("shop.sum.head.none")
		pat["button.many"] = c.t("shop.button", "n", "{n}", "list", list)
		pat["button.one"] = c.t("shop.button.one", "list", list)
		pat["button.none"] = c.t("shop.button.none", "list", list)
	}
	pat["count"] = c.t("shop.sum.count", "new", "{new}", "on", "{on}")
	pat["pantry.one"] = c.t("shop.sum.pantry.one")
	pat["pantry.many"] = c.t("shop.sum.pantry", "n", "{n}")
	switch {
	case list == "":
		pg.Sentence = template.HTML(bold(c.t("shop.sum.nolist.head")) + " " + html.EscapeString(c.t("shop.sum.nolist")))
		pg.Button = c.t("shop.button.nolist")
	case pg.Ticked == 0:
		pg.Sentence = template.HTML(bold(pat["head.none"]) + " " + html.EscapeString(c.t("shop.sum.none")))
		pg.Button = pat["button.none"]
	default:
		head := strings.ReplaceAll(pat["head.many"], "{n}", strconv.Itoa(pg.Ticked))
		btn := strings.ReplaceAll(pat["button.many"], "{n}", strconv.Itoa(pg.Ticked))
		if pg.Ticked == 1 {
			head, btn = pat["head.one"], pat["button.one"]
		}
		if pg.TickedNew == 0 {
			head = pat["head.nothing"]
		}
		text := strings.NewReplacer("{new}", strconv.Itoa(pg.TickedNew), "{on}", strconv.Itoa(on)).Replace(pat["count"])
		if len(pg.MealsLeft) > 0 {
			text += " " + c.t("shop.sum.meals.left", "names", strings.Join(pg.MealsLeft, ", "))
		}
		switch {
		case pg.PantryLeft == 1:
			text += " " + pat["pantry.one"]
		case pg.PantryLeft > 1:
			text += " " + strings.ReplaceAll(pat["pantry.many"], "{n}", strconv.Itoa(pg.PantryLeft))
		}
		pg.Sentence = template.HTML(bold(head) + " " + html.EscapeString(text))
		pg.Button = btn
	}
	pg.Patterns = pat
}

// ---- the success line after adding and after taking back

type shopFlashView struct {
	Line, ListName, ListID, RunID string
	Undone                        bool
	Lines                         []template.HTML
}

// shopLine is the success line after a run: what is new, what was added to what, and what was left out.
func (c *itemCtx) shopLine(run store.ShopRun) shopFlashView {
	var news, added, filled []string
	for _, st := range run.Steps {
		label := entryLabel(st.Text, st.Variety)
		switch st.Action {
		case list.AddedNew:
			news = append(news, label)
		case list.AddedTo:
			added = append(added, c.t("shop.flash.added", "name", label, "qty", c.qtyWords(st.Added), "total", c.qtyWords(st.After)))
		case list.Filled:
			filled = append(filled, c.t("shop.flash.filled", "name", label, "qty", c.qtyWords(st.After)))
		}
	}
	names := make([]string, len(run.Meals))
	for i, m := range run.Meals {
		names[i] = m.RecipeName + " (" + c.weekdayShort(m.Day) + ")"
	}
	what := strings.Join(names, ", ")
	if len(names) > 3 {
		what = c.plural("shop.flash.meals", len(names))
	}
	n := len(run.Steps)
	head := c.plural("shop.flash.head", n, "what", what, "list", run.ListName)
	var rest []string
	if len(news) > 0 {
		rest = append(rest, c.t("shop.flash.new", "n", strconv.Itoa(len(news))))
	}
	rest = append(rest, added...)
	rest = append(rest, filled...)
	text := strings.Join(rest, ", ")
	if text != "" {
		text += "."
	}
	if len(run.LeftOut) > 0 {
		text += " " + c.t("shop.flash.left", "names", strings.Join(run.LeftOut, ", "))
	}
	v := shopFlashView{ListName: run.ListName, ListID: run.ListID, RunID: run.ID}
	v.Lines = []template.HTML{template.HTML("<b>" + html.EscapeString(head) + "</b> " + html.EscapeString(strings.TrimSpace(text)))}
	return v
}

// undoLines say what taking a run back did.
func (c *itemCtx) undoLines(u store.ShopUndo) []template.HTML {
	var parts []string
	if len(u.Removed) > 0 {
		parts = append(parts, c.t("shop.undo.removed", "names", strings.Join(u.Removed, ", ")))
	}
	var back []string
	for _, r := range u.Reverted {
		if r.Qty.None() {
			back = append(back, c.t("shop.undo.reverted.none", "name", r.Text))
		} else {
			back = append(back, c.t("shop.undo.reverted", "name", r.Text, "qty", c.qtyWords(r.Qty)))
		}
	}
	if len(back) > 0 {
		parts = append(parts, strings.Join(back, ", ")+".")
	}
	lines := []template.HTML{template.HTML("<b>" + html.EscapeString(c.t("shop.undo.head")) + "</b> " + html.EscapeString(strings.Join(parts, " ")))}
	if len(u.Ticked) > 0 {
		lines = append(lines, template.HTML(html.EscapeString(c.plural("shop.undo.ticked", len(u.Ticked), "names", strings.Join(u.Ticked, ", ")))))
	}
	return lines
}

// listSources are the lines under the open entries of a list that meals brought quantities to.
func (c *itemCtx) listSources(es []list.Entry) (map[string]string, map[string]string, error) {
	var ids []string
	for _, e := range es {
		if !e.Done {
			ids = append(ids, e.ID)
		}
	}
	src, err := c.s.db.EntrySources(c.p.household.ID, ids)
	if err != nil {
		return nil, nil, err
	}
	lines, tiles := map[string]string{}, map[string]string{}
	for _, e := range es {
		s, ok := src[e.ID]
		if e.Done || !ok || len(s.Parts) == 0 {
			continue
		}
		art := c.articleOf(e)
		var parts []sharePart
		var sum float64
		for _, p := range s.Parts {
			q := p.Qty
			if !q.None() {
				sum += q.Base
				q = list.Quantity{Base: q.Base, Unit: c.show(art)(q.Base, q.Kind())}
			}
			parts = append(parts, sharePart{Recipe: p.RecipeName, Day: p.Day, Qty: q})
		}
		expect := s.Prior.Base + sum
		hand := expect-e.Qty.Base > 1e-6 || e.Qty.Base-expect > 1e-6
		prior := s.Prior
		if !prior.None() {
			prior = list.Quantity{Base: prior.Base, Unit: c.show(art)(prior.Base, prior.Kind())}
		}
		lines[e.ID] = c.shareLine(parts, prior, hand)
		tiles[e.ID] = c.shareLine(parts, list.Quantity{}, true)
	}
	return lines, tiles, nil
}

var _ = library.Fold

// patternsJSON is what the script of the tick list needs to word the count and the button.
func patternsJSON(m map[string]string) string {
	b, _ := json.Marshal(m)
	return string(b)
}

// shopWeekCard is the card above the days of the meal plan (ticket 283): how many recipes from today on need ingredients.
type shopWeekCard struct {
	Text   template.HTML
	Button bool
}

func (c *itemCtx) shopWeekCard(monday time.Time) (*shopWeekCard, error) {
	cands, err := c.shopCandidates(shopSelection{Week: true, Monday: monday})
	if err != nil {
		return nil, err
	}
	var open, done, partial []shopCand
	for _, m := range cands {
		switch m.Status() {
		case "open":
			open = append(open, m)
			if m.OnList > 0 {
				partial = append(partial, m)
			}
		case "done":
			done = append(done, m)
		}
	}
	if len(open) == 0 && len(done) == 0 {
		return nil, nil
	}
	head := "<b>" + html.EscapeString(c.t("shop.week.head", "week", strconv.Itoa(isoWeek(monday)))) + "</b> "
	var text string
	if len(open) == 0 {
		text = c.t("shop.week.alldone")
	} else {
		key := "shop.week.need"
		if len(done) > 0 {
			key = "shop.week.need.more"
		}
		text = c.plural(key, len(open))
		switch len(partial) {
		case 0:
		case 1:
			text += c.t("shop.week.missing.one", "name", partial[0].Rec.Name, "k", strconv.Itoa(partial[0].Add))
		default:
			text += c.t("shop.week.missing.many", "m", strconv.Itoa(len(partial)))
		}
		text += "."
	}
	switch len(done) {
	case 0:
	case 1:
		text += " " + c.t("shop.week.done.one", "name", done[0].Rec.Name, "list", done[0].ListName)
	default:
		text += " " + c.t("shop.week.done.many", "n", strconv.Itoa(len(done)))
	}
	return &shopWeekCard{Text: template.HTML(head + html.EscapeString(text)), Button: len(open) > 0}, nil
}

// attachShop gives the recipe boxes of the week their state: on a list already, and the button.
func (c *itemCtx) attachShop(days []mealDay, monday time.Time, values map[string]string) error {
	on, err := c.s.db.ShopMeals(c.p.household.ID, monday.Format(task.DateLayout), monday.AddDate(0, 0, 6).Format(task.DateLayout))
	if err != nil {
		return err
	}
	for i := range days {
		for j := range days[i].Cells {
			cell := &days[i].Cells[j]
			if cell.Recipe == nil {
				continue
			}
			planned, ok := parsePortions(cell.Portions)
			if !ok {
				planned = c.householdPortions()
			}
			var n int
			var o store.ShopOnList
			for _, x := range on {
				if x.Day == cell.Day && string(x.Mealtime) == cell.Mealtime && x.RecipeID == cell.RecipeID {
					n, o = x.Portions, x
				}
			}
			switch {
			case n == 0:
				cell.Recipe.ShopBtn = c.t("shop.btn.meal")
			default:
				key := "shop.state.day"
				if o.RunDay == c.s.today(c.p) {
					key = "shop.state.today"
				}
				text := c.t(key, "list", o.ListName, "day", c.dayLong(o.RunDay), "portions", c.plural("shop.meal.portions", n))
				if planned > n {
					text += " " + c.t("shop.state.now", "m", strconv.Itoa(planned))
					cell.Recipe.ShopBtn = c.plural("shop.btn.missing", planned-n)
				}
				cell.Recipe.ShopState = text
			}
		}
	}
	return nil
}
