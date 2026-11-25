package server

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/shop"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

// The tick list /meals/shop and what follows it (ticket 283, ADR 0015: HTML
// routes with API parity). Parents only; a child gets a 404.

var isoWeekPattern = regexp.MustCompile(`^(\d{4})-W(\d{2})$`)

// shopParent is parentPage with a 404 for a child: the page does not exist for them.
func (s *server) shopParent(w http.ResponseWriter, r *http.Request) (principal, bool) {
	p, ok := s.memberPage(w, r)
	if ok && p.person.Role != store.RoleParent {
		http.NotFound(w, r)
		return principal{}, false
	}
	return p, ok
}

// parseShopSelection reads "meal:<day>.<mealtime>" or "week:<day or ISO week>".
func parseShopSelection(raw, today string) (shopSelection, bool) {
	kind, v, _ := strings.Cut(raw, ":")
	switch kind {
	case "meal":
		if _, _, ok := parseMealKey(v); ok {
			return shopSelection{Meal: v}, true
		}
	case "week":
		if m := isoWeekPattern.FindStringSubmatch(v); m != nil {
			y, _ := strconv.Atoi(m[1])
			wk, _ := strconv.Atoi(m[2])
			jan4 := time.Date(y, 1, 4, 0, 0, 0, 0, time.UTC)
			return shopSelection{Week: true, Monday: weekOf(jan4).AddDate(0, 0, (wk-1)*7)}, true
		}
		if _, err := time.Parse(task.DateLayout, v); err == nil {
			return shopSelection{Week: true, Monday: requestedWeek(v, today)}, true
		}
	}
	return shopSelection{}, false
}

func (sel shopSelection) token() string {
	if sel.Week {
		return "week:" + sel.Monday.Format(task.DateLayout)
	}
	return "meal:" + sel.Meal
}

func (sel shopSelection) monday(today string) time.Time {
	if sel.Week {
		return sel.Monday
	}
	day, _, _ := parseMealKey(sel.Meal)
	return requestedWeek(day, today)
}

// shopFormIn reads what the tick list posted.
func shopFormIn(r *http.Request, sel shopSelection) shopIn {
	in := shopIn{Sel: sel, ListID: r.PostFormValue("list"), Posted: true, Picks: map[string]bool{}, Was: map[string]shopWas{}, Versions: map[string]string{}}
	for _, k := range r.PostForm["pick"] {
		in.Picks[k] = true
	}
	if sel.Week {
		in.Meals = map[string]bool{}
		for _, k := range r.PostForm["meal"] {
			in.Meals[k] = true
		}
	}
	for _, v := range r.PostForm["rv"] {
		if id, ver, ok := strings.Cut(v, "|"); ok {
			in.Versions[id] = ver
		}
	}
	for _, v := range r.PostForm["was"] {
		if f := strings.SplitN(v, "|", 4); len(f) == 4 {
			base, _ := strconv.ParseFloat(f[1], 64)
			in.Was[f[0]] = shopWas{Base: base, Unit: f[2], Name: f[3]}
		}
	}
	return in
}

func (s *server) handleShopPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.shopParent(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	raw := ""
	switch {
	case q.Get("meal") != "":
		raw = "meal:" + q.Get("meal")
	case q.Get("week") != "":
		raw = "week:" + q.Get("week")
	}
	sel, ok := parseShopSelection(raw, s.today(p))
	if !ok {
		http.NotFound(w, r)
		return
	}
	c, err := s.itemContext(p, s.langOf(r))
	if err != nil {
		s.fail(w, "shop context", err)
		return
	}
	pg, err := c.buildShop(shopIn{Sel: sel, ListID: q.Get("list")})
	if err != nil {
		s.fail(w, "shop", err)
		return
	}
	if !sel.Week && len(pg.MealCands) == 0 {
		http.NotFound(w, r)
		return
	}
	s.renderShop(w, r, c, http.StatusOK, pg, shopOut{})
}

// shopOut is what a page shows next to the tick list: the notices and errors.
type shopOut struct {
	NoTick bool
}

func (s *server) renderShop(w http.ResponseWriter, r *http.Request, c *itemCtx, status int, pg *shopPage, out shopOut) {
	p := c.p
	today := s.today(p)
	monday := pg.Sel.monday(today)
	back := "/meals?" + url.Values{"week": {monday.Format(task.DateLayout)}}.Encode()
	pg.OnN = len(pg.On) + pg.SameN
	data := map[string]any{
		"EmptyKind": "", "Person": p.person, "Shop": pg, "Back": back, "For": pg.Sel.token(), "Week": pg.Sel.Week, "NoTick": out.NoTick,
		"SyncStale": s.syncStale(p.household.ID),
		"WeekNo":    isoWeek(monday),
	}
	switch {
	case pg.NoList:
		data["EmptyKind"] = "nolist"
	case !pg.Sel.Week && len(pg.MealCands) == 1 && pg.MealCands[0].Status() == "empty":
		data["EmptyKind"] = "noingredients"
	case !pg.Sel.Week && len(pg.MealCands) == 1 && pg.MealCands[0].Status() == "done":
		data["EmptyKind"] = "done"
	}
	if !pg.Sel.Week && len(pg.MealCands) == 1 {
		m := pg.MealCands[0]
		data["Meal"] = c.shopMealHead(m)
	}
	if pg.Sel.Week {
		data["Lead"] = c.shopWeekLead(pg, monday, today)
	}
	data["PatternsJSON"] = patternsJSON(pg.Patterns)
	data["Title"] = "shop.title"
	if pg.Sel.Week {
		data["Title"] = "shop.title.week"
	}
	s.render(w, r, status, "meal_shop.html", data)
}

type shopMealHead struct {
	Name, Symbol, When, Portions, Scaled string
}

func (c *itemCtx) shopMealHead(m shopCand) shopMealHead {
	h := shopMealHead{Name: m.Rec.Name, Symbol: m.Rec.Category.Symbol(), When: c.dayLong(m.Day) + ", " + c.t("meals.mealtime."+string(m.Mealtime))}
	people := c.householdPortions()
	n := m.Add
	switch {
	case m.OnList > 0 && m.Add > 0:
		h.Portions = c.t("shop.head.missing", "add", strconv.Itoa(m.Add), "total", strconv.Itoa(m.Planned))
	default:
		who := c.t("meals.rcp.you_are", "n", strconv.Itoa(people))
		if n == people {
			who = c.t("meals.rcp.for_all", "n", strconv.Itoa(n))
		}
		h.Portions = c.plural("shop.meal.portions", n) + ", " + who
	}
	if m.Rec.Portions != n && n > 0 {
		h.Scaled = c.t("shop.head.scaled", "base", strconv.Itoa(m.Rec.Portions))
	}
	if m.Starter {
		h.When += " · " + c.t("shop.meal.starter")
	}
	return h
}

// shopWeekLead says what the week page is about.
func (c *itemCtx) shopWeekLead(pg *shopPage, monday time.Time, today string) string {
	open, partial := 0, []shopCand{}
	for _, m := range pg.MealCands {
		if m.Status() == "open" {
			open++
			if m.OnList > 0 {
				partial = append(partial, m)
			}
		}
	}
	text := c.t("shop.lead.week", "week", strconv.Itoa(isoWeek(monday))) + " " + c.plural("shop.lead.recipes", open)
	switch len(partial) {
	case 0:
	case 1:
		text += c.t("shop.lead.missing.one", "k", strconv.Itoa(partial[0].Add))
	default:
		text += c.t("shop.lead.missing.many", "m", strconv.Itoa(len(partial)))
	}
	text += "."
	var past []string
	for d := monday; d.Format(task.DateLayout) < today && d.Before(monday.AddDate(0, 0, 7)); d = d.AddDate(0, 0, 1) {
		past = append(past, c.t("meals.weekday."+strconv.Itoa(int(d.Weekday()))))
	}
	switch len(past) {
	case 0:
	case 1:
		text += " " + c.t("shop.lead.past.one", "day", past[0])
	default:
		text += " " + c.t("shop.lead.past.many", "days", strings.Join(past[:len(past)-1], ", ")+" "+c.t("shop.and")+" "+past[len(past)-1])
	}
	return text
}

func (s *server) handleShopSubmit(w http.ResponseWriter, r *http.Request) {
	p, ok := s.shopParent(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	sel, ok := parseShopSelection(r.PostFormValue("for"), s.today(p))
	if !ok {
		http.NotFound(w, r)
		return
	}
	c, err := s.itemContext(p, s.langOf(r))
	if err != nil {
		s.fail(w, "shop context", err)
		return
	}
	in := shopFormIn(r, sel)
	pg, err := c.buildShop(in)
	if err != nil {
		s.fail(w, "shop", err)
		return
	}
	switch {
	case r.PostFormValue("recount") != "":
		s.renderShop(w, r, c, http.StatusOK, pg, shopOut{})
		return
	case len(pg.Changed) > 0 || len(c.shopMissing(pg, in)) > 0:
		pg.Missing = c.shopMissing(pg, in)
		s.renderShop(w, r, c, http.StatusConflict, pg, shopOut{})
		return
	case pg.ListGone != "" || pg.ListID == "":
		s.renderShop(w, r, c, http.StatusUnprocessableEntity, pg, shopOut{})
		return
	case pg.Ticked == 0:
		s.renderShop(w, r, c, http.StatusUnprocessableEntity, pg, shopOut{NoTick: true})
		return
	}
	run, err := c.addShop(pg)
	switch {
	case errors.Is(err, store.ErrNotFound): // the list was deleted a moment ago
		pg.ListGone = pg.ListID
		pg.ListID = ""
		c.shopWords(pg)
		s.renderShop(w, r, c, http.StatusUnprocessableEntity, pg, shopOut{})
		return
	case err != nil:
		s.fail(w, "shop add", err)
		return
	}
	to := "/meals?" + url.Values{"week": {sel.monday(s.today(p)).Format(task.DateLayout)}}.Encode()
	s.redirectSaved(w, r, to, "flash.meal_shop:"+run.ID)
}

// shopMissing are the ticked meals of the page that are not planned as a recipe any more.
func (c *itemCtx) shopMissing(pg *shopPage, in shopIn) []string {
	have := map[string]bool{}
	for _, m := range pg.MealCands {
		have[m.Key] = true
	}
	var out []string
	if pg.Sel.Week {
		for k := range in.Meals {
			if !have[k] {
				out = append(out, k)
			}
		}
	} else if len(pg.MealCands) == 0 {
		out = append(out, pg.Sel.Meal)
	}
	for i, k := range out {
		if day, mt, ok := parseMealKey(k); ok {
			out[i] = c.dayLong(day) + ", " + c.t("meals.mealtime."+string(mt))
		}
	}
	return out
}

// addShop puts the ticked rows of a page on its list.
func (c *itemCtx) addShop(pg *shopPage) (store.ShopRun, error) {
	checked := map[string]bool{}
	for _, rv := range pg.Rows {
		if rv.Checked {
			checked[rv.Key] = true
		}
	}
	var rows []shop.Row
	var left []string
	for _, st := range pg.steps {
		switch {
		case st.Plan.Action == list.Exists:
		case checked[st.Row.Key]:
			rows = append(rows, st.Row)
		case st.Row.Pantry:
			left = append(left, entryLabel(st.Row.Text, st.Row.Variety))
		}
	}
	var meals []store.ShopMeal
	for _, m := range pg.Selected {
		meals = append(meals, store.ShopMeal{Day: m.Day, Mealtime: m.Mealtime, RecipeID: m.Rec.ID, RecipeName: m.Rec.Name, Portions: m.Add})
	}
	return c.s.db.AddMealShop(c.p.household.ID, store.ShopInput{ListID: pg.ListID, Day: c.s.today(c.p), Rows: rows, Meals: meals, LeftOut: left, Show: c.shopShow})
}

func (s *server) handleShopUndo(w http.ResponseWriter, r *http.Request) {
	p, ok := s.shopParent(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	run, err := s.db.MealShopRun(p.household.ID, r.PostFormValue("run"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	c, err := s.itemContext(p, s.langOf(r))
	if err != nil {
		s.fail(w, "shop context", err)
		return
	}
	to := "/meals"
	if len(run.Meals) > 0 {
		to += "?" + url.Values{"week": {run.Meals[0].Day}}.Encode()
	}
	undo, err := s.db.UndoMealShop(p.household.ID, run.ID)
	switch {
	case errors.Is(err, store.ErrShopTaken):
		s.redirectSaved(w, r, to, "flash.meal_shop_taken")
		return
	case err != nil:
		s.fail(w, "shop undo", err)
		return
	}
	var lines []string
	for _, l := range c.undoLines(undo) {
		lines = append(lines, string(l))
	}
	s.setFlash(w, r, "flash.meal_shop_undone:"+s.flashTexts.put(strings.Join(lines, "\x1e"), s.now()))
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// shopFlash is the success line of the meal plan after adding or taking back, if one waits.
func (s *server) shopFlash(r *http.Request, c *itemCtx) *shopFlashView {
	key, arg := s.pendingFlash(r)
	switch key {
	case "flash.meal_shop":
		run, err := s.db.MealShopRun(c.p.household.ID, arg)
		if err != nil || run.Undone {
			return nil
		}
		v := c.shopLine(run)
		return &v
	case "flash.meal_shop_undone":
		text, ok := s.flashTexts.take(arg, s.now())
		if !ok {
			return nil
		}
		v := &shopFlashView{Undone: true}
		for _, l := range strings.Split(text, "\x1e") {
			v.Lines = append(v.Lines, template.HTML(l))
		}
		return v
	}
	return nil
}
