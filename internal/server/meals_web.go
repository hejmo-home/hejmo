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

	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

// mealCell is one field of the week form. Kind says where its meal comes from:
// "free" (typed by hand or empty), "template" or "adjusted". A recipe entry
// has a RecipeID (and a Recipe to show when the recipe still exists); its
// portions are typed in Portions.
type mealCell struct {
	Name, Mealtime, Value string
	Day                   string // YYYY-MM-DD
	Kind                  string
	TemplateID            string
	TemplateName          string
	TemplateDish          string
	CanRestore            bool // an adjusted meal of today or later can go back to its template
	RecipeID              string
	Portions              string
	Recipe                *mealRecipe
	PortionsBad           bool   // the portions are refused: the field shows PortionsError
	PortionsError         string // in words, with what is allowed
}

// mealRecipe is what a recipe entry shows: the framed box of the page, a link
// for children, a suggestion's box.
type mealRecipe struct {
	ID, Symbol, Name string
	Pic              *rphPic // the small cover instead of the symbol
	Meta             string  // "Recipe · Main dish · 45 min"
	Href             string  // the recipe with this entry's portions
	Portions         string  // as typed
	For              string  // "for all 4" or "you are 4"
	People           int
	Warns            []template.HTML
	Unchecked        string
	Diet             string
	// ShopState says the meal is on a list already (ticket 283); ShopBtn is the button that puts the missing ingredients on one, "" for none.
	ShopState, ShopBtn string
}

type mealDay struct {
	Date    string // YYYY-MM-DD
	Weekday int    // time.Weekday, for the day's name
	Short   string // 17/11
	Today   bool
	Cells   []mealCell
	Planned []mealCell // the cells with an entry, for the read-only view
}

// portionsPattern is what portions look like: digits only (1 to 99 checked after).
var portionsPattern = regexp.MustCompile(`^\d{1,3}$`)

// parsePortions reads typed portions: a whole number from 1 to 99.
func parsePortions(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if !portionsPattern.MatchString(s) {
		return 0, false
	}
	n, _ := strconv.Atoi(s)
	return n, n >= 1 && n <= recipe.MaxPortions
}

// mealField is the name of the form field of a day's mealtime.
func mealField(day string, m meal.Mealtime) string { return "meal_" + day + "_" + string(m) }

// weekOf returns the Monday of the week that holds day. The meal plan is shown
// a week at a time, Monday to Sunday.
func weekOf(day time.Time) time.Time {
	back := (int(day.Weekday()) + 6) % 7
	return day.AddDate(0, 0, -back)
}

// requestedWeek is the Monday of the week the request asks for with ?week=
// (any day of it), or of the week of today if it asks for none or a bad one.
func requestedWeek(raw, today string) time.Time {
	if d, err := time.Parse(task.DateLayout, raw); err == nil {
		return weekOf(d)
	}
	d, _ := time.Parse(task.DateLayout, today)
	return weekOf(d)
}

// weekDays builds the seven days of a week with a cell per mealtime. values
// holds what the cells show: the stored plan, or what was typed after an error.
func weekDays(f store.DateFormat, monday time.Time, today string, values map[string]string, marks map[string]store.PlanMeal) []mealDay {
	var days []mealDay
	for i := 0; i < 7; i++ {
		d := monday.AddDate(0, 0, i)
		day := mealDay{Date: d.Format(task.DateLayout), Weekday: int(d.Weekday()), Short: datefmt.Short(f, d)}
		day.Today = day.Date == today
		for _, m := range meal.Mealtimes() {
			c := mealCell{Name: mealField(day.Date, m), Mealtime: string(m), Day: day.Date}
			c.Value = values[c.Name]
			c.RecipeID, c.Portions = values[c.Name+".recipe"], values[c.Name+".portions"]
			c.Kind = "free"
			if m, ok := marks[c.Name]; ok {
				switch m.Source {
				case store.SourceTemplate:
					c.Kind = "template"
				case store.SourceAdjusted:
					c.Kind = "adjusted"
					c.CanRestore = day.Date >= today
				}
				if c.Kind != "free" {
					c.TemplateID, c.TemplateName, c.TemplateDish = m.TemplateID, m.TemplateName, m.TemplateDish
				}
			}
			day.Cells = append(day.Cells, c)
		}
		days = append(days, day)
	}
	return days
}

// attachRecipes gives the recipe entries of the days their recipe to show, and fills the days' planned entries.
// Typed portions that are no number from 1 to 99 stay as typed; the link then opens the recipe for the household.
func (s *server) attachRecipes(c *itemCtx, days []mealDay, recipes map[string]store.Recipe, errField string) {
	people := c.householdPortions()
	for i := range days {
		for j := range days[i].Cells {
			cell := &days[i].Cells[j]
			if rec, ok := recipes[cell.RecipeID]; ok && cell.RecipeID != "" {
				n, valid := parsePortions(cell.Portions)
				if !valid {
					n = people
				}
				cell.Recipe = c.mealRecipe(rec, cell.Day, cell.Mealtime, n, people)
				cell.Recipe.Portions = cell.Portions
				if errField == cell.Name+".portions" {
					cell.PortionsBad = true
					key := "meals.rcp.error.portions"
					if strings.TrimSpace(cell.Portions) == "" {
						key = "meals.rcp.error.portions.empty"
					}
					cell.PortionsError = c.t(key, "typed", strings.TrimSpace(cell.Portions), "n", strconv.Itoa(people))
				}
				cell.Value = rec.Name
			} else if cell.RecipeID != "" { // a recipe that is gone: what is left is its name, or nothing
				cell.RecipeID, cell.Portions = "", ""
			}
			if cell.Value != "" || cell.Recipe != nil {
				days[i].Planned = append(days[i].Planned, *cell)
			}
		}
	}
}

// mealRecipe is the recipe of an entry of the plan with n portions for a day and mealtime.
func (c *itemCtx) mealRecipe(rec store.Recipe, day, mealtime string, n, people int) *mealRecipe {
	out := &mealRecipe{ID: rec.ID, Symbol: rec.Category.Symbol(), Name: rec.Name, Pic: c.smallCover(rec.ID), People: people, Portions: strconv.Itoa(n),
		Href: recipesPath + "/" + rec.ID + "?portions=" + strconv.Itoa(n) + "&plan=" + day + "/" + mealtime}
	meta := []string{c.t("meals.rcp.kind"), c.categoryName(rec.Category)}
	if rec.Minutes > 0 {
		meta = append(meta, c.minutesShort(rec.Minutes))
	}
	out.Meta = strings.Join(meta, " · ")
	if n == people {
		out.For = c.t("meals.rcp.for_all", "n", strconv.Itoa(n))
	} else {
		out.For = c.t("meals.rcp.you_are", "n", strconv.Itoa(people))
	}
	out.Warns = c.warnLines(c.warnings(rec.Recipe))
	if free := c.freeNames(rec.Recipe); len(free) > 0 {
		out.Unchecked = c.t("recipes.unchecked.list", "names", strings.Join(free, ", "))
	}
	if d := recipe.DietOf(rec.Ingredients, c.lookup).Mark; d != "" {
		out.Diet = c.dietWord(d)
	}
	return out
}

// recipesByID are the household's recipes, for the entries of a page.
func (s *server) recipesByID(p principal) (map[string]store.Recipe, error) {
	list, err := s.db.Recipes(p.household.ID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]store.Recipe, len(list))
	for _, r := range list {
		out[r.ID] = r
	}
	return out, nil
}

func (s *server) renderMeals(w http.ResponseWriter, r *http.Request, p principal, status int, monday time.Time, typed map[string]string, errKey, errField string, dirty bool) {
	sunday := monday.AddDate(0, 0, 6)
	today := s.today(p)
	lang := s.langOf(r)
	s.rollMealPlan(p.household, lang)
	list, err := s.db.MealPlan(p.household.ID, monday.Format(task.DateLayout), sunday.Format(task.DateLayout), today)
	if err != nil {
		s.fail(w, "meals", err)
		return
	}
	values := map[string]string{}
	marks := map[string]store.PlanMeal{}
	for _, m := range list {
		name := mealField(m.Day, m.Mealtime)
		values[name] = m.Text
		marks[name] = m
		if m.RecipeID != "" {
			values[name+".recipe"], values[name+".portions"] = m.RecipeID, strconv.Itoa(m.Portions)
		}
	}
	if typed != nil {
		values = typed
	}
	days := weekDays(p.household.DateFormat, monday, today, values, marks)
	hasRecipe := false
	for _, d := range days {
		for _, c := range d.Cells {
			hasRecipe = hasRecipe || c.RecipeID != ""
		}
	}
	var data0 *shopWeekCard
	var free []freeTextPerson
	if hasRecipe {
		c, err := s.itemContext(p, lang)
		if err != nil {
			s.fail(w, "meals context", err)
			return
		}
		recipes, err := s.recipesByID(p)
		if err != nil {
			s.fail(w, "recipes", err)
			return
		}
		s.attachRecipes(c, days, recipes, errField)
		shown := false
		for _, d := range days {
			for _, cell := range d.Cells {
				shown = shown || cell.Recipe != nil
			}
		}
		if shown && p.person.Role == store.RoleParent {
			free = s.freeText(c.st)
		}
	} else {
		for i := range days {
			for _, c := range days[i].Cells {
				if c.Value != "" {
					days[i].Planned = append(days[i].Planned, c)
				}
			}
		}
	}
	var flash *shopFlashView
	if p.person.Role == store.RoleParent {
		c, err := s.itemContext(p, lang)
		if err != nil {
			s.fail(w, "meals context", err)
			return
		}
		if hasRecipe {
			if err := c.attachShop(days, monday, values); err != nil {
				s.fail(w, "meals shop", err)
				return
			}
		}
		if wk, err := c.shopWeekCard(monday); err != nil {
			s.fail(w, "meals shop week", err)
			return
		} else if wk != nil {
			data0 = wk
		}
		if status == http.StatusOK {
			flash = s.shopFlash(r, c)
		}
	}
	from, to := datefmt.RangeParts(p.household.DateFormat, monday, sunday)
	planned := false
	errDay, errMealtime := 0, ""
	for _, d := range days {
		planned = planned || len(d.Planned) > 0
		for _, c := range d.Cells {
			if errField != "" && (c.Name == errField || c.Name+".portions" == errField) {
				errDay, errMealtime = d.Weekday, c.Mealtime
			}
		}
	}
	data := map[string]any{
		"ShopWeek": data0, "ShopFlash": flash, "SuccessInline": flash != nil,
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Days": days, "Planned": planned,
		"Week": monday.Format(task.DateLayout), "Prev": monday.AddDate(0, 0, -7).Format(task.DateLayout), "Next": monday.AddDate(0, 0, 7).Format(task.DateLayout),
		"WeekNo": isoWeek(monday), "From": from, "To": to, "ErrorWeekday": errDay, "ErrorMealtime": errMealtime, "ThisWeek": monday.Format(task.DateLayout) == weekOf(mustDay(today)).Format(task.DateLayout),
		"Editable": p.person.Role == store.RoleParent, "Error": errKey, "ErrorField": errField, "ErrorPortions": strings.HasSuffix(errField, ".portions"),
		"Dirty": dirty, "FreeText": free, "Suggest": "/meals/suggest",
	}
	if p.person.Role == store.RoleParent {
		sum, err := s.templateSummary(p, lang, monday)
		if err != nil {
			s.fail(w, "meal templates", err)
			return
		}
		data["TemplateSummary"] = sum
		data["HasTemplates"] = sum != ""
	}
	s.render(w, r, status, "meals.html", data)
}

// isoWeek is the number of the ISO calendar week that starts on monday.
func isoWeek(monday time.Time) int {
	_, w := monday.AddDate(0, 0, 3).ISOWeek() // the Thursday decides, as the standard has it
	return w
}

func mustDay(s string) time.Time {
	d, _ := time.Parse(task.DateLayout, s)
	return d
}

func (s *server) handleMealsPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	s.renderMeals(w, r, p, http.StatusOK, requestedWeek(r.URL.Query().Get("week"), s.today(p)), nil, "", "", false)
}

// saveTypedMeal saves the text typed for one meal field.
func (s *server) saveTypedMeal(r *http.Request, p principal, field, raw string) error {
	day, mt, ok := parseMealField(field)
	if !ok {
		return nil
	}
	text, err := meal.CleanText(raw)
	if err != nil {
		return err
	}
	return s.saveMeals(r, p, []store.Meal{{Day: day, Mealtime: mt, Text: text}})
}

// restoreField drops the adjustment of the meal a form field stands for.
func (s *server) restoreField(r *http.Request, p principal, field string) error {
	day, mt, ok := parseMealField(field)
	if !ok {
		return store.ErrNotFound
	}
	return s.db.RestoreMeal(p.household.ID, day, mt, s.mealOptions(s.langOf(r), p))
}

var mealFieldPattern = regexp.MustCompile(`^meal_(\d{4}-\d{2}-\d{2})_(breakfast|lunch|snack|dinner)$`)

// parseMealField is the day and mealtime of a meal field's name.
func parseMealField(field string) (string, meal.Mealtime, bool) {
	m := mealFieldPattern.FindStringSubmatch(field)
	if m == nil {
		return "", "", false
	}
	return m[1], meal.Mealtime(m[2]), true
}

// handleMealsSave saves the week of the form: every meal field that is in the
// form, an empty one removing its meal. A recipe entry has its recipe and portions next to the
// text; a changed text that is the name of a recipe (ignoring case) becomes that recipe. Fields of
// other weeks are ignored, so a stale form cannot change them.
func (s *server) handleMealsSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	today := s.today(p)
	monday := requestedWeek(r.PostFormValue("week"), today)
	recipes, err := s.recipesByID(p)
	if err != nil {
		s.fail(w, "recipes", err)
		return
	}
	byName := map[string]store.Recipe{}
	for _, rec := range recipes {
		byName[recipe.NameKey(rec.Name)] = rec
	}
	stored, err := s.db.MealPlan(p.household.ID, monday.Format(task.DateLayout), monday.AddDate(0, 0, 6).Format(task.DateLayout), today)
	if err != nil {
		s.fail(w, "meals", err)
		return
	}
	was := map[string]store.PlanMeal{}
	for _, m := range stored {
		was[mealField(m.Day, m.Mealtime)] = m
	}
	var changes []store.Meal
	typed := map[string]string{}
	badField, badKey := "", ""
	other := r.PostFormValue("other")     // "Different dish": the box of one meal becomes an empty field again
	restore := r.PostFormValue("restore") // "Back to template" of one meal, saved with the week
	people := 0
	for _, d := range weekDays(p.household.DateFormat, monday, today, nil, nil) {
		for _, c := range d.Cells {
			vals, hasText := r.PostForm[c.Name]
			rids, hasRecipe := r.PostForm[c.Name+".recipe"]
			if (!hasText || len(vals) == 0) && (!hasRecipe || len(rids) == 0) {
				continue
			}
			var text, rid, ps string
			if hasText && len(vals) > 0 {
				text = vals[0]
			}
			if hasRecipe && len(rids) > 0 {
				rid = rids[0]
			}
			ps = r.PostFormValue(c.Name + ".portions")
			typed[c.Name], typed[c.Name+".recipe"], typed[c.Name+".portions"] = text, rid, ps
			if c.Name == other {
				typed[c.Name], typed[c.Name+".recipe"], typed[c.Name+".portions"] = "", "", ""
				continue
			}
			if c.Name == restore {
				continue // it goes back to the template, whatever is typed
			}
			if rid != "" {
				if _, ok := recipes[rid]; !ok {
					continue // the recipe is gone since the form was made: leave the meal alone
				}
				n, valid := parsePortions(ps)
				if !valid {
					if badField == "" {
						badField, badKey = c.Name+".portions", "meals.rcp.error.portions"
					}
					continue
				}
				changes = append(changes, store.Meal{Day: d.Date, Mealtime: meal.Mealtime(c.Mealtime), RecipeID: rid, Portions: n})
				continue
			}
			clean, err := meal.CleanText(text)
			if err != nil && badField == "" {
				badField, badKey = c.Name, "meals.error.text"
			}
			if old := was[c.Name]; clean != "" && (clean != old.Text || old.RecipeID != "") {
				if rec, ok := byName[recipe.NameKey(clean)]; ok { // without script: the name of a recipe is that recipe
					if people == 0 {
						if people, err = s.householdPortions(p); err != nil {
							s.fail(w, "household size", err)
							return
						}
					}
					typed[c.Name], typed[c.Name+".recipe"], typed[c.Name+".portions"] = "", rec.ID, strconv.Itoa(people)
					changes = append(changes, store.Meal{Day: d.Date, Mealtime: meal.Mealtime(c.Mealtime), RecipeID: rec.ID, Portions: people})
					continue
				}
			}
			changes = append(changes, store.Meal{Day: d.Date, Mealtime: meal.Mealtime(c.Mealtime), Text: clean})
		}
	}
	if other != "" {
		s.renderMeals(w, r, p, http.StatusOK, monday, typed, "", "", true)
		return
	}
	if badField != "" {
		s.renderMeals(w, r, p, http.StatusUnprocessableEntity, monday, typed, badKey, badField, true)
		return
	}
	switch err := s.saveMeals(r, p, changes); {
	case err == nil && r.PostFormValue("shop") != "":
		sel := "week:" + monday.Format(task.DateLayout)
		if v := r.PostFormValue("shop"); v != "week" {
			if day, mt, ok := parseMealField(v); ok {
				sel = "meal:" + day + "." + string(mt)
			}
		}
		kind, v, _ := strings.Cut(sel, ":")
		http.Redirect(w, r, shopPath+"?"+url.Values{kind: {v}}.Encode(), http.StatusSeeOther)
	case err == nil:
		key := "flash.meals_saved:" + monday.Format(task.DateLayout)
		if restore != "" {
			if _, ok := typed[restore]; ok {
				switch err := s.restoreField(r, p, restore); {
				case err == nil:
					key = "flash.meal_restored:" + restore
				case errors.Is(err, store.ErrNotFound): // it was not adjusted any more: what was typed is saved
					if err := s.saveTypedMeal(r, p, restore, typed[restore]); err != nil {
						s.fail(w, "save meals", err)
						return
					}
				default:
					s.fail(w, "restore meal", err)
					return
				}
			}
		}
		s.redirectSaved(w, r, "/meals?"+url.Values{"week": {monday.Format(task.DateLayout)}}.Encode(), key)
	case errors.Is(err, store.ErrInvalid):
		s.renderMeals(w, r, p, http.StatusUnprocessableEntity, monday, typed, "meals.error.text", "", true)
	default:
		s.fail(w, "save meals", err)
	}
}
