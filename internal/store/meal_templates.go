package store

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/meal"
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
type PlanMeal struct {
	Day          string
	Mealtime     meal.Mealtime
	Text         string
	Source       string
	TemplateID   string
	TemplateName string
	TemplateDish string
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
}

// mealLayers holds what decides a cell from a day on: the templates, the
// hand-typed entries and the adjustments.
type mealLayers struct {
	templates      []meal.Template
	manual, adjust map[cell]string
}

func loadMealLayers(q queryer, householdID, fromDay string) (*mealLayers, error) {
	l := &mealLayers{manual: map[cell]string{}, adjust: map[cell]string{}}
	var err error
	if l.templates, err = loadTemplates(q, householdID, ""); err != nil {
		return nil, err
	}
	for table, into := range map[string]map[cell]string{"meal_manual": l.manual, "meal_adjustments": l.adjust} {
		rows, err := q.Query(`SELECT day, mealtime, text FROM `+table+` WHERE household_id = ? AND day >= ?`, householdID, fromDay)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var c cell
			var text string
			if err := rows.Scan(&c.day, &c.mealtime, &text); err != nil {
				rows.Close()
				return nil, err
			}
			into[c] = text
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return l, nil
}

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
	er, err := q.Query(`SELECT e.template_id, e.weekday, e.mealtime, e.text FROM meal_template_entries e JOIN meal_templates t ON t.id = e.template_id WHERE t.household_id = ?`, householdID)
	if err != nil {
		return nil, err
	}
	defer er.Close()
	for er.Next() {
		var tid string
		var s meal.Slot
		var text string
		if err := er.Scan(&tid, &s.Weekday, &s.Mealtime, &text); err != nil {
			return nil, err
		}
		if t := byID[tid]; t != nil {
			t.Entries[s] = text
		}
	}
	return out, er.Err()
}

// templateFor is the template that fills a cell, and its dish.
func (l *mealLayers) templateFor(c cell) (*meal.Template, string) {
	d, err := time.Parse(dateLayout, c.day)
	if err != nil {
		return nil, ""
	}
	for i := range l.templates {
		if dish, ok := l.templates[i].Dish(d, c.mealtime); ok {
			return &l.templates[i], dish
		}
	}
	return nil, ""
}

// resolve is the meal of a cell from today on: this week's adjustment, else
// the template that fills it, else the hand-typed entry.
func (l *mealLayers) resolve(c cell) PlanMeal {
	m := PlanMeal{Day: c.day, Mealtime: c.mealtime, Source: SourceManual}
	tpl, dish := l.templateFor(c)
	adj, hasAdj := l.adjust[c]
	switch {
	case tpl != nil && hasAdj:
		m.Text, m.Source, m.TemplateID, m.TemplateName, m.TemplateDish = adj, SourceAdjusted, tpl.ID, tpl.Name, dish
	case tpl != nil:
		m.Text, m.Source, m.TemplateID, m.TemplateName, m.TemplateDish = dish, SourceTemplate, tpl.ID, tpl.Name, dish
	case hasAdj:
		m.Text = adj // the template that was adjusted is gone: a plain entry
	default:
		m.Text = l.manual[c]
	}
	return m
}

// set stores a typed text for a cell from today on: an adjustment where a
// template fills it (none when the text is the template's own dish), else the
// hand-typed entry.
func (l *mealLayers) set(tx *sql.Tx, householdID string, c cell, text string) error {
	put := func(table string, into map[cell]string, keep bool) error {
		if !keep {
			delete(into, c)
			_, err := tx.Exec(`DELETE FROM `+table+` WHERE household_id = ? AND day = ? AND mealtime = ?`, householdID, c.day, c.mealtime)
			return err
		}
		into[c] = text
		_, err := tx.Exec(`INSERT INTO `+table+` (household_id, day, mealtime, text) VALUES (?, ?, ?, ?)
			ON CONFLICT (household_id, day, mealtime) DO UPDATE SET text = excluded.text`, householdID, c.day, c.mealtime, text)
		return err
	}
	if tpl, dish := l.templateFor(c); tpl != nil {
		return put("meal_adjustments", l.adjust, text != dish)
	}
	if _, stale := l.adjust[c]; stale {
		if err := put("meal_adjustments", l.adjust, false); err != nil {
			return err
		}
	}
	return put("meal_manual", l.manual, text != "")
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
		if _, err := tx.Exec(`INSERT INTO meal_template_entries (template_id, weekday, mealtime, text) VALUES (?, ?, ?, ?)`, t.ID, s.Weekday, s.Mealtime, text); err != nil {
			return meal.Template{}, err
		}
	}
	t.Entries = filled
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
	for c, text := range layers.adjust {
		if t, _ := layers.templateFor(c); t == nil || t.ID != id {
			continue
		}
		if text == "" {
			_, err = tx.Exec(`DELETE FROM meal_manual WHERE household_id = ? AND day = ? AND mealtime = ?`, householdID, c.day, c.mealtime)
		} else {
			_, err = tx.Exec(`INSERT INTO meal_manual (household_id, day, mealtime, text) VALUES (?, ?, ?, ?)
				ON CONFLICT (household_id, day, mealtime) DO UPDATE SET text = excluded.text`, householdID, c.day, c.mealtime, text)
		}
		if err == nil {
			_, err = tx.Exec(`DELETE FROM meal_adjustments WHERE household_id = ? AND day = ? AND mealtime = ?`, householdID, c.day, c.mealtime)
		}
		if err != nil {
			return err
		}
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
	rows, err := tx.Query(`SELECT day, mealtime, text, source, template_id, template_dish FROM meal_plan WHERE household_id = ? AND day >= ?`, householdID, today)
	if err != nil {
		return err
	}
	for rows.Next() {
		var m PlanMeal
		if err := rows.Scan(&m.Day, &m.Mealtime, &m.Text, &m.Source, &m.TemplateID, &m.TemplateDish); err != nil {
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
		if ok && old.Text == want.Text && old.Source == want.Source && old.TemplateID == want.TemplateID && old.TemplateDish == want.TemplateDish {
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
// adjusted meal that was emptied is listed with empty text. Meals come by day
// and then in the order of the day.
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
		rows, err := db.sql.Query(`SELECT day, mealtime, text, source, template_id, template_dish FROM meal_plan WHERE household_id = ? AND day >= ? AND day <= ? AND day < ?`, householdID, from, to, today)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var m PlanMeal
			if err := rows.Scan(&m.Day, &m.Mealtime, &m.Text, &m.Source, &m.TemplateID, &m.TemplateDish); err != nil {
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
	return out, nil
}
