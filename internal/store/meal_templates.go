package store

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/recipe"
)

// Where the meal in a cell of the plan comes from.
const (
	SourceManual   = "manual"   // typed by hand
	SourceTemplate = "template" // filled in by a meal plan template
	SourceAdjusted = "adjusted" // a template fills the cell, but this week's change wins
)

// mealHorizonDays is how far ahead template meals are written into the
// meal-plan calendar: a year and a week. Pages and the API compute meals for
// any day on their own.
const mealHorizonDays = 371

// ErrConflict marks a request that clashes with what is stored (HTTP 409).
var ErrConflict = errors.New("store: conflict")

// TemplateClash is one other template a template clashes with, and where.
type TemplateClash struct {
	Template meal.Template
	Clash    meal.Clash
}

// ClashError says which templates a template clashes with. It is ErrConflict.
type ClashError struct{ Clashes []TemplateClash }

func (e *ClashError) Error() string {
	var names []string
	for _, c := range e.Clashes {
		names = append(names, c.Template.Name)
	}
	return "the template fills the same meals as " + strings.Join(names, ", ")
}

// Is makes errors.Is(err, ErrConflict) true.
func (e *ClashError) Is(target error) bool { return target == ErrConflict }

// PlanMeal is the meal a cell of the plan shows, and where it comes from. An
// adjusted meal may be empty ("nothing this week"); TemplateDish is what the
// template fills in.
//
// Text is the dish: the free text, or for a recipe the recipe's current name.
// RecipeID and Portions (1 to 99) are set when the entry is a recipe.
type PlanMeal struct {
	Day          string
	Mealtime     meal.Mealtime
	Text         string
	Source       string
	TemplateID   string
	TemplateName string
	TemplateDish string
	RecipeID     string
	Portions     int
}

type cell struct {
	day      string
	mealtime meal.Mealtime
}

func (o MealOptions) today() string {
	if o.Today != "" {
		return o.Today
	}
	loc := o.Location
	if loc == nil {
		loc = time.UTC
	}
	return time.Now().In(loc).Format(dateLayout)
}

type queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// layerEntry is what a hand-typed entry or an adjustment holds: free text, or a
// recipe (its name as text) with portions.
type layerEntry struct {
	text     string
	recipeID string
	portions int
}

// mealLayers holds what decides a cell from a day on: the templates, the
// hand-typed entries and the adjustments.
type mealLayers struct {
	templates      []meal.Template
	manual, adjust map[cell]layerEntry
	recipeNames    map[string]string // the household's recipes by id, for the recipe slots of templates
	people         int               // portions of a recipe a template plans: the people in the household, 1 to 99
}

func loadMealLayers(q queryer, householdID, fromDay string) (*mealLayers, error) {
	l := &mealLayers{manual: map[cell]layerEntry{}, adjust: map[cell]layerEntry{}}
	var err error
	if l.templates, err = loadTemplates(q, householdID, ""); err != nil {
		return nil, err
	}
	if err := l.loadTemplateRecipes(q, householdID); err != nil {
		return nil, err
	}
	for table, into := range map[string]map[cell]layerEntry{"meal_manual": l.manual, "meal_adjustments": l.adjust} {
		rows, err := q.Query(`SELECT day, mealtime, text, recipe_id, portions FROM `+table+` WHERE household_id = ? AND day >= ?`, householdID, fromDay)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var c cell
			var e layerEntry
			if err := rows.Scan(&c.day, &c.mealtime, &e.text, &e.recipeID, &e.portions); err != nil {
				rows.Close()
				return nil, err
			}
			into[c] = e
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return l, nil
}

// loadTemplateRecipes reads what a recipe slot of a template needs: the names of
// the household's recipes and the number of people.
func (l *mealLayers) loadTemplateRecipes(q queryer, householdID string) error {
	l.recipeNames = map[string]string{}
	var used bool
	for _, t := range l.templates {
		used = used || len(t.Recipes) > 0
	}
	if !used {
		return nil
	}
	rows, err := q.Query(`SELECT id, name FROM recipes WHERE household_id = ?`, householdID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return err
		}
		l.recipeNames[id] = name
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	var n int
	if err := q.QueryRow(`SELECT COUNT(*) FROM household_members WHERE household_id = ?`, householdID).Scan(&n); err != nil {
		return err
	}
	l.people = HouseholdPortions(n)
	return nil
}

// HouseholdPortions is how many portions a recipe entry starts with for a
// household of that many people: at least 1, at most recipe.MaxPortions.
func HouseholdPortions(people int) int { return min(max(people, 1), recipe.MaxPortions) }

// loadTemplates reads the household's templates by name, or only one.
func loadTemplates(q queryer, householdID, id string) ([]meal.Template, error) {
	query := `SELECT id, name, all_mealtimes, mealtime, every, start_week FROM meal_templates WHERE household_id = ?`
	args := []any{householdID}
	if id != "" {
		query += ` AND id = ?`
		args = append(args, id)
	}
	rows, err := q.Query(query+` ORDER BY name COLLATE NOCASE, id`, args...)
	if err != nil {
		return nil, err
	}
	var out []meal.Template
	for rows.Next() {
		var t meal.Template
		var all int
		if err := rows.Scan(&t.ID, &t.Name, &all, &t.Mealtime, &t.Interval, &t.Start); err != nil {
			rows.Close()
			return nil, err
		}
		t.AllMealtimes = all == 1
		t.Entries = map[meal.Slot]string{}
		t.Recipes = map[meal.Slot]string{}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	byID := map[string]*meal.Template{}
	for i := range out {
		byID[out[i].ID] = &out[i]
	}
	er, err := q.Query(`SELECT e.template_id, e.weekday, e.mealtime, e.text, e.recipe_id FROM meal_template_entries e JOIN meal_templates t ON t.id = e.template_id WHERE t.household_id = ?`, householdID)
	if err != nil {
		return nil, err
	}
	defer er.Close()
	for er.Next() {
		var tid string
		var s meal.Slot
		var text, recipeID string
		if err := er.Scan(&tid, &s.Weekday, &s.Mealtime, &text, &recipeID); err != nil {
			return nil, err
		}
		if t := byID[tid]; t != nil {
			t.Entries[s] = text
			if recipeID != "" {
				t.Recipes[s] = recipeID
			}
		}
	}
	return out, er.Err()
}

// templateFor is the template that fills a cell, and its dish: the recipe's
// current name for a recipe slot (then recipeID is the recipe), else the text.
func (l *mealLayers) templateFor(c cell) (tpl *meal.Template, dish, recipeID string) {
	d, err := time.Parse(dateLayout, c.day)
	if err != nil {
		return nil, "", ""
	}
	for i := range l.templates {
		dish, ok := l.templates[i].Dish(d, c.mealtime)
		if !ok {
			continue
		}
		if id := l.templates[i].RecipeAt(d, c.mealtime); id != "" {
			if name, known := l.recipeNames[id]; known {
				return &l.templates[i], name, id
			}
		}
		return &l.templates[i], dish, ""
	}
	return nil, "", ""
}

// resolve is the meal of a cell from today on: this week's adjustment, else
// the template that fills it, else the hand-typed entry.
func (l *mealLayers) resolve(c cell) PlanMeal {
	m := PlanMeal{Day: c.day, Mealtime: c.mealtime, Source: SourceManual}
	tpl, dish, recipeID := l.templateFor(c)
	adj, hasAdj := l.adjust[c]
	switch {
	case tpl != nil && hasAdj:
		m.Text, m.RecipeID, m.Portions = adj.text, adj.recipeID, adj.portions
		m.Source, m.TemplateID, m.TemplateName, m.TemplateDish = SourceAdjusted, tpl.ID, tpl.Name, dish
	case tpl != nil:
		m.Text, m.Source, m.TemplateID, m.TemplateName, m.TemplateDish = dish, SourceTemplate, tpl.ID, tpl.Name, dish
		if recipeID != "" {
			m.RecipeID, m.Portions = recipeID, l.people
		}
	case hasAdj:
		m.Text, m.RecipeID, m.Portions = adj.text, adj.recipeID, adj.portions // the template that was adjusted is gone: a plain entry
	default:
		e := l.manual[c]
		m.Text, m.RecipeID, m.Portions = e.text, e.recipeID, e.portions
	}
	return m
}

// set stores an entry for a cell from today on: an adjustment where a template
// fills it (none when the text is the template's own dish, or the recipe is the
// template's own for the household's size), else the hand-typed entry.
func (l *mealLayers) set(tx *sql.Tx, householdID string, c cell, e layerEntry) error {
	put := func(table string, into map[cell]layerEntry, keep bool) error {
		if !keep {
			delete(into, c)
			_, err := tx.Exec(`DELETE FROM `+table+` WHERE household_id = ? AND day = ? AND mealtime = ?`, householdID, c.day, c.mealtime)
			return err
		}
		into[c] = e
		_, err := tx.Exec(`INSERT INTO `+table+` (household_id, day, mealtime, text, recipe_id, portions) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (household_id, day, mealtime) DO UPDATE SET text = excluded.text, recipe_id = excluded.recipe_id, portions = excluded.portions`,
			householdID, c.day, c.mealtime, e.text, e.recipeID, e.portions)
		return err
	}
	if tpl, dish, recipeID := l.templateFor(c); tpl != nil {
		own := false
		if recipeID != "" {
			own = e.recipeID == recipeID && e.portions == l.people
		} else {
			own = e.recipeID == "" && e.text == dish
		}
		return put("meal_adjustments", l.adjust, !own)
	}
	if _, stale := l.adjust[c]; stale {
		if err := put("meal_adjustments", l.adjust, false); err != nil {
			return err
		}
	}
	return put("meal_manual", l.manual, e.text != "")
}

// releaseAdjustments turns the adjustments of the cells that template id fills
// into plain entries (an emptied one into nothing). After a template edit,
// after is the edited template and only cells it no longer fills are released.
func (l *mealLayers) releaseAdjustments(tx *sql.Tx, householdID, id string, after *meal.Template) error {
	for c, e := range l.adjust {
		if t, _, _ := l.templateFor(c); t == nil || t.ID != id {
			continue
		}
		if after != nil {
			if _, ok := after.Dish(mustParseDay(c.day), c.mealtime); ok {
				continue
			}
		}
		var err error
		if e.text == "" {
			_, err = tx.Exec(`DELETE FROM meal_manual WHERE household_id = ? AND day = ? AND mealtime = ?`, householdID, c.day, c.mealtime)
		} else {
			_, err = tx.Exec(`INSERT INTO meal_manual (household_id, day, mealtime, text, recipe_id, portions) VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT (household_id, day, mealtime) DO UPDATE SET text = excluded.text, recipe_id = excluded.recipe_id, portions = excluded.portions`,
				householdID, c.day, c.mealtime, e.text, e.recipeID, e.portions)
		}
		if err == nil {
			_, err = tx.Exec(`DELETE FROM meal_adjustments WHERE household_id = ? AND day = ? AND mealtime = ?`, householdID, c.day, c.mealtime)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func mustParseDay(day string) time.Time {
	d, _ := time.Parse(dateLayout, day)
	return d
}

// MealTemplates lists the household's templates by name.
func (db *DB) MealTemplates(householdID string) ([]meal.Template, error) {
	return loadTemplates(db.sql, householdID, "")
}

// MealTemplate returns one template, ErrNotFound if the household has none
// with that id.
func (db *DB) MealTemplate(householdID, id string) (meal.Template, error) {
	list, err := loadTemplates(db.sql, householdID, id)
	if err != nil {
		return meal.Template{}, err
	}
	if len(list) == 0 {
		return meal.Template{}, ErrNotFound
	}
	return list[0], nil
}

func invalidTemplate(err error) error {
	var bad *meal.InvalidError
	if errors.As(err, &bad) {
		return invalidMeal(bad.Field, bad.Msg)
	}
	return err
}

// nameRecipeSlots checks that every recipe slot of t is a recipe of the
// household and makes its entry the recipe's current name (ErrInvalid otherwise).
func nameRecipeSlots(q rowQueryer, householdID string, t *meal.Template) error {
	if len(t.Recipes) == 0 {
		return nil
	}
	entries := map[meal.Slot]string{}
	for s, text := range t.Entries {
		entries[s] = text
	}
	recipes := map[meal.Slot]string{}
	for s, id := range t.Recipes {
		if id == "" {
			continue
		}
		name, err := recipeName(q, householdID, id)
		if errors.Is(err, ErrNotFound) {
			return invalidMeal("entries", "unknown recipe")
		}
		if err != nil {
			return err
		}
		entries[s], recipes[s] = name, id
	}
	t.Entries, t.Recipes = entries, recipes
	return nil
}

// rowQueryer is what reads one row.
type rowQueryer interface {
	QueryRow(string, ...any) *sql.Row
}

// checkClashes refuses a template that fills a meal another one fills too.
func checkClashes(t meal.Template, others []meal.Template, today string) error {
	day, err := time.Parse(dateLayout, today)
	if err != nil {
		return err
	}
	var clashes []TemplateClash
	for _, other := range others {
		if other.ID == t.ID {
			continue
		}
		if c, ok := meal.FirstClash(t, other, day); ok {
			clashes = append(clashes, TemplateClash{other, c})
		}
	}
	if len(clashes) > 0 {
		return &ClashError{clashes}
	}
	return nil
}

// saveMealTemplate writes a template and has the plan follow it.
func (db *DB) saveMealTemplate(householdID string, t meal.Template, o MealOptions, create bool) (meal.Template, error) {
	t.Name = strings.TrimSpace(t.Name)
	if err := t.Validate(); err != nil {
		return meal.Template{}, invalidTemplate(err)
	}
	if t.AllMealtimes {
		t.Mealtime = ""
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return meal.Template{}, err
	}
	defer tx.Rollback()
	if err := nameRecipeSlots(tx, householdID, &t); err != nil {
		return meal.Template{}, err
	}
	others, err := loadTemplates(tx, householdID, "")
	if err != nil {
		return meal.Template{}, err
	}
	if !create {
		found := false
		for _, other := range others {
			found = found || other.ID == t.ID
		}
		if !found {
			return meal.Template{}, ErrNotFound
		}
	}
	if err := checkClashes(t, others, o.today()); err != nil {
		return meal.Template{}, err
	}
	all := 0
	if t.AllMealtimes {
		all = 1
	}
	if !create {
		// Adjustments of cells the edited template stops filling become plain
		// entries, as when it is deleted; none stay behind to resurface later.
		layers, err := loadMealLayers(tx, householdID, o.today())
		if err != nil {
			return meal.Template{}, err
		}
		if err := layers.releaseAdjustments(tx, householdID, t.ID, &t); err != nil {
			return meal.Template{}, err
		}
	}
	if create {
		t.ID = newID()
		_, err = tx.Exec(`INSERT INTO meal_templates (id, household_id, name, all_mealtimes, mealtime, every, start_week, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.ID, householdID, t.Name, all, t.Mealtime, t.Interval, t.Start, now(), now())
	} else {
		_, err = tx.Exec(`UPDATE meal_templates SET name = ?, all_mealtimes = ?, mealtime = ?, every = ?, start_week = ?, updated_at = ? WHERE id = ? AND household_id = ?`,
			t.Name, all, t.Mealtime, t.Interval, t.Start, now(), t.ID, householdID)
		if err == nil {
			_, err = tx.Exec(`DELETE FROM meal_template_entries WHERE template_id = ?`, t.ID)
		}
	}
	if err != nil {
		return meal.Template{}, err
	}
	filled := t.Filled()
	for s, text := range filled {
		if _, err := tx.Exec(`INSERT INTO meal_template_entries (template_id, weekday, mealtime, text, recipe_id) VALUES (?, ?, ?, ?, ?)`, t.ID, s.Weekday, s.Mealtime, text, t.Recipes[s]); err != nil {
			return meal.Template{}, err
		}
	}
	t.Entries = filled
	for s := range t.Recipes {
		if _, ok := filled[s]; !ok {
			delete(t.Recipes, s)
		}
	}
	if err := refreshMealPlan(tx, householdID, o); err != nil {
		return meal.Template{}, err
	}
	return t, tx.Commit()
}

// CreateMealTemplate stores a new template and fills the meal plan from today
// on. A template that fills a meal another one fills too is refused with a
// *ClashError and nothing is saved.
func (db *DB) CreateMealTemplate(householdID string, t meal.Template, o MealOptions) (meal.Template, error) {
	return db.saveMealTemplate(householdID, t, o, true)
}

// UpdateMealTemplate replaces the template with t.ID. Changes reach today and
// later days, never days before today; adjusted meals keep their change.
func (db *DB) UpdateMealTemplate(householdID string, t meal.Template, o MealOptions) (meal.Template, error) {
	return db.saveMealTemplate(householdID, t, o, false)
}

// DeleteMealTemplate removes a template. From today on its meals leave the
// plan, except adjusted ones, which become plain entries; days before today
// keep what they showed.
func (db *DB) DeleteMealTemplate(householdID, id string, o MealOptions) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	today := o.today()
	layers, err := loadMealLayers(tx, householdID, today)
	if err != nil {
		return err
	}
	var tpl *meal.Template
	for i := range layers.templates {
		if layers.templates[i].ID == id {
			tpl = &layers.templates[i]
		}
	}
	if tpl == nil {
		return ErrNotFound
	}
	if err := layers.releaseAdjustments(tx, householdID, id, nil); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM meal_templates WHERE id = ? AND household_id = ?`, id, householdID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE meal_plan SET source = 'manual', template_id = '', template_dish = '' WHERE household_id = ? AND template_id = ? AND day < ?`, householdID, id, today); err != nil {
		return err
	}
	if err := refreshMealPlan(tx, householdID, o); err != nil {
		return err
	}
	return tx.Commit()
}

// RefreshMealPlan writes the meals the templates fill in for the coming year
// into the meal-plan calendar. Saving a template does it too; it is called as
// the days go by, so the calendar keeps reaching a year ahead.
func (db *DB) RefreshMealPlan(householdID string, o MealOptions) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := refreshMealPlan(tx, householdID, o); err != nil {
		return err
	}
	return tx.Commit()
}

// refreshMealPlan makes the plan rows and events from today on say what the
// layers decide: for the next year and for every row that exists.
func refreshMealPlan(tx *sql.Tx, householdID string, o MealOptions) error {
	today := o.today()
	start, err := time.Parse(dateLayout, today)
	if err != nil {
		return err
	}
	layers, err := loadMealLayers(tx, householdID, today)
	if err != nil {
		return err
	}
	w := &planWriter{tx: tx, householdID: householdID, o: o}
	have := map[cell]PlanMeal{}
	rows, err := tx.Query(`SELECT day, mealtime, text, source, template_id, template_dish, recipe_id, portions FROM meal_plan WHERE household_id = ? AND day >= ?`, householdID, today)
	if err != nil {
		return err
	}
	for rows.Next() {
		var m PlanMeal
		if err := rows.Scan(&m.Day, &m.Mealtime, &m.Text, &m.Source, &m.TemplateID, &m.TemplateDish, &m.RecipeID, &m.Portions); err != nil {
			rows.Close()
			return err
		}
		have[cell{m.Day, m.Mealtime}] = m
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	cells := map[cell]bool{}
	for c := range have {
		cells[c] = true
	}
	if len(layers.templates) > 0 {
		for i := 0; i < mealHorizonDays; i++ {
			day := start.AddDate(0, 0, i).Format(dateLayout)
			for _, m := range meal.Mealtimes() {
				cells[cell{day, m}] = true
			}
		}
	}
	list := make([]cell, 0, len(cells))
	for c := range cells {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].day != list[j].day {
			return list[i].day < list[j].day
		}
		return list[i].mealtime < list[j].mealtime
	})
	for _, c := range list {
		want := layers.resolve(c)
		old, ok := have[c]
		if !ok && want.Text == "" {
			continue
		}
		if ok && old.Text == want.Text && old.Source == want.Source && old.TemplateID == want.TemplateID && old.TemplateDish == want.TemplateDish && old.RecipeID == want.RecipeID && old.Portions == want.Portions {
			continue
		}
		if err := w.write(want); err != nil {
			return err
		}
	}
	return nil
}

// MealPlan lists the meals of a range of days, both included, with where each
// comes from: the effective plan (this week's adjustment, else the template,
// else the hand-typed entry). Days before today show what they showed. An
// adjusted meal that was emptied is listed with empty text while its day is
// still to come; it has no meal_plan row, so once the day is over it shows as a
// plain empty cell. Meals come by day and then in the order of the day.
func (db *DB) MealPlan(householdID, from, to, today string) ([]PlanMeal, error) {
	a, errA := time.Parse(dateLayout, from)
	b, errB := time.Parse(dateLayout, to)
	if errA != nil || errB != nil {
		return nil, invalidMeal("date", "a day is YYYY-MM-DD")
	}
	layers, err := loadMealLayers(db.sql, householdID, from)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, t := range layers.templates {
		names[t.ID] = t.Name
	}
	past := map[cell]PlanMeal{}
	if from < today {
		rows, err := db.sql.Query(`SELECT day, mealtime, text, source, template_id, template_dish, recipe_id, portions FROM meal_plan WHERE household_id = ? AND day >= ? AND day <= ? AND day < ?`, householdID, from, to, today)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var m PlanMeal
			if err := rows.Scan(&m.Day, &m.Mealtime, &m.Text, &m.Source, &m.TemplateID, &m.TemplateDish, &m.RecipeID, &m.Portions); err != nil {
				return nil, err
			}
			m.TemplateName = names[m.TemplateID]
			if m.TemplateName == "" {
				m.Source, m.TemplateID, m.TemplateDish = SourceManual, "", ""
			}
			past[cell{m.Day, m.Mealtime}] = m
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	var out []PlanMeal
	for d := a; !d.After(b); d = d.AddDate(0, 0, 1) {
		day := d.Format(dateLayout)
		for _, mt := range meal.Mealtimes() {
			c := cell{day, mt}
			if day < today {
				if m, ok := past[c]; ok {
					out = append(out, m)
				}
				continue
			}
			if m := layers.resolve(c); m.Text != "" || m.Source == SourceAdjusted {
				out = append(out, m)
			}
		}
	}
	return out, db.currentRecipeNames(householdID, out)
}

// currentRecipeNames makes the dish of every recipe entry the recipe's current name.
func (db *DB) currentRecipeNames(householdID string, list []PlanMeal) error {
	var any bool
	for _, m := range list {
		any = any || m.RecipeID != ""
	}
	if !any {
		return nil
	}
	rows, err := db.sql.Query(`SELECT id, name FROM recipes WHERE household_id = ?`, householdID)
	if err != nil {
		return err
	}
	defer rows.Close()
	names := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		names[id] = name
	}
	for i := range list {
		if name, ok := names[list[i].RecipeID]; ok && list[i].RecipeID != "" {
			list[i].Text = name
		}
	}
	return rows.Err()
}
