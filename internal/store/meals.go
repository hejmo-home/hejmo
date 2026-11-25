package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/recipe"
)

// mealCalendarColor is the colour of the generated calendar in the web
// interface's calendar views.
const mealCalendarColor = "#9A7420"

// Meal is one entry of the meal plan for a day and a mealtime: free text, or a
// recipe of the household with portions (1 to 99). Setting an entry with empty
// Text and no recipe removes it. For a recipe, Text is ignored when it is set
// (the entry shows the recipe's name) and holds that name when it is read.
type Meal struct {
	Day      string // YYYY-MM-DD, in the household's time zone
	Mealtime meal.Mealtime
	Text     string
	RecipeID string
	Portions int
}

// MealOptions says how the entries are written into the generated calendar.
type MealOptions struct {
	// CalendarName names the calendar when it is created ("Meal plan").
	CalendarName string
	// Location is the household's time zone: an entry is an all-day event on its day.
	Location *time.Location
	// Today is the current day (YYYY-MM-DD, in the household's time zone). Days
	// before it keep what they show when a template changes; empty means no day
	// is over.
	Today string
	// Title is the event title of an entry ("Dinner: Pasta bake"), in the
	// language of whoever saves it.
	Title func(meal.Mealtime, string) string
}

// recipeName is the name of a recipe of the household, ErrNotFound if it has none with that id.
func recipeName(q interface {
	QueryRow(string, ...any) *sql.Row
}, householdID, id string) (string, error) {
	var name string
	err := q.QueryRow(`SELECT name FROM recipes WHERE household_id = ? AND id = ?`, householdID, id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, err
}

func invalidMeal(field, msg string) error {
	return fmt.Errorf("%w: %w", ErrInvalid, &meal.InvalidError{Field: field, Msg: msg})
}

// MealCalendar returns the household's meal-plan calendar, ErrNotFound while
// nothing was ever planned.
func (db *DB) MealCalendar(householdID string) (Calendar, error) {
	cals, err := db.Calendars(householdID)
	if err != nil {
		return Calendar{}, err
	}
	for _, c := range cals {
		if c.Kind == "meals" {
			return c, nil
		}
	}
	return Calendar{}, ErrNotFound
}

// SetMeals writes entries of the meal plan, each with its all-day event in the
// meal-plan calendar, in one transaction: either all of them are stored or none.
// The calendar is generated on first use; it is read-only (the events change
// only here) and shared through the box by default (ADR 0015). If an entry
// appears twice, the later one counts. An entry whose text did not change
// leaves its event untouched, so CalDAV clients do not fetch it again.
//
// An entry is free text or a recipe of the household with portions from 1 to
// 99 (else ErrInvalid); a recipe entry shows the recipe's name as its text.
//
// Where a meal plan template fills a day from today on, the entry is that
// week's adjustment: typing the template's own dish removes it, an empty text
// is an adjustment too (nothing this week), a recipe is always one. Days before
// today are written as they are.
func (db *DB) SetMeals(householdID string, changes []Meal, o MealOptions) error {
	last := map[cell]Meal{}
	var order []cell
	for _, c := range changes {
		text, err := meal.CleanText(c.Text)
		if err != nil {
			var bad *meal.InvalidError
			errors.As(err, &bad)
			return invalidMeal(bad.Field, bad.Msg)
		}
		c.Text = text
		if c.RecipeID == "" {
			c.Portions = 0
		} else if c.Portions < 1 || c.Portions > 99 {
			return invalidMeal("portions", "portions are a whole number from 1 to 99")
		}
		if !c.Mealtime.Valid() {
			return invalidMeal("mealtime", "unknown mealtime")
		}
		if _, err := time.Parse(dateLayout, c.Day); err != nil {
			return invalidMeal("date", "a day is YYYY-MM-DD")
		}
		key := cell{c.Day, c.Mealtime}
		if _, seen := last[key]; !seen {
			order = append(order, key)
		}
		last[key] = c
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	layers, err := loadMealLayers(tx, householdID, o.Today)
	if err != nil {
		return err
	}
	w := &planWriter{tx: tx, householdID: householdID, o: o}
	for _, key := range order {
		c := last[key]
		if c.RecipeID != "" {
			name, err := recipeName(tx, householdID, c.RecipeID)
			if errors.Is(err, ErrNotFound) {
				return invalidMeal("recipe_id", "unknown recipe")
			}
			if err != nil {
				return err
			}
			c.Text = name
		}
		if key.day < o.Today {
			old, err := w.existing(key)
			if err != nil {
				return err
			}
			if old.exists && old.Text == c.Text && old.RecipeID == c.RecipeID && old.Portions == c.Portions {
				continue
			}
			if err := w.write(PlanMeal{Day: c.Day, Mealtime: c.Mealtime, Text: c.Text, Source: SourceManual, RecipeID: c.RecipeID, Portions: c.Portions}); err != nil {
				return err
			}
			continue
		}
		if err := layers.set(tx, householdID, key, layerEntry{c.Text, c.RecipeID, c.Portions}); err != nil {
			return err
		}
		if err := w.write(layers.resolve(key)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RestoreMeal removes the adjustment of one meal ("Back to template"): the
// template's dish shows again. It is ErrNotFound if the meal is not adjusted.
func (db *DB) RestoreMeal(householdID, day string, m meal.Mealtime, o MealOptions) error {
	if !m.Valid() {
		return invalidMeal("mealtime", "unknown mealtime")
	}
	if _, err := time.Parse(dateLayout, day); err != nil {
		return invalidMeal("date", "a day is YYYY-MM-DD")
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	layers, err := loadMealLayers(tx, householdID, day)
	if err != nil {
		return err
	}
	key := cell{day, m}
	if _, ok := layers.adjust[key]; !ok || day < o.Today {
		return ErrNotFound
	}
	if _, err := tx.Exec(`DELETE FROM meal_adjustments WHERE household_id = ? AND day = ? AND mealtime = ?`, householdID, day, m); err != nil {
		return err
	}
	delete(layers.adjust, key)
	w := &planWriter{tx: tx, householdID: householdID, o: o}
	if err := w.write(layers.resolve(key)); err != nil {
		return err
	}
	return tx.Commit()
}

// planWriter writes the effective meals into meal_plan and the calendar.
type planWriter struct {
	tx          *sql.Tx
	householdID string
	o           MealOptions
	calID       string
}

type planRow struct {
	PlanMeal
	exists  bool
	eventID string
}

func (w *planWriter) existing(c cell) (planRow, error) {
	r := planRow{PlanMeal: PlanMeal{Day: c.day, Mealtime: c.mealtime}}
	err := w.tx.QueryRow(`SELECT text, event_id, source, template_id, template_dish, recipe_id, portions FROM meal_plan WHERE household_id = ? AND day = ? AND mealtime = ?`,
		w.householdID, c.day, c.mealtime).Scan(&r.Text, &r.eventID, &r.Source, &r.TemplateID, &r.TemplateDish, &r.RecipeID, &r.Portions)
	switch {
	case err == nil:
		r.exists = true
	case errors.Is(err, sql.ErrNoRows):
		r.Text = ""
	default:
		return r, err
	}
	return r, nil
}

// write makes the plan row of one cell (and its event) say what m says; an
// empty text removes them. An unchanged text leaves the event alone.
func (w *planWriter) write(m PlanMeal) error {
	old, err := w.existing(cell{m.Day, m.Mealtime})
	if err != nil {
		return err
	}
	tx, householdID := w.tx, w.householdID
	switch {
	case !old.exists && m.Text == "":
		return nil
	case old.exists && m.Text == "":
		if _, err := tx.Exec(`DELETE FROM events WHERE id = ? AND household_id = ?`, old.eventID, householdID); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM meal_plan WHERE household_id = ? AND day = ? AND mealtime = ?`, householdID, m.Day, m.Mealtime)
		return err
	case old.exists && m.Text == old.Text:
		if old.Source == m.Source && old.TemplateID == m.TemplateID && old.TemplateDish == m.TemplateDish && old.RecipeID == m.RecipeID && old.Portions == m.Portions {
			return nil
		}
		_, err := tx.Exec(`UPDATE meal_plan SET source = ?, template_id = ?, template_dish = ?, recipe_id = ?, portions = ? WHERE household_id = ? AND day = ? AND mealtime = ?`,
			m.Source, m.TemplateID, m.TemplateDish, m.RecipeID, m.Portions, householdID, m.Day, m.Mealtime)
		return err
	}
	if w.calID == "" {
		if w.calID, err = mealCalendarID(tx, householdID, w.o.CalendarName); err != nil {
			return err
		}
	}
	ev, err := mealEvent(Meal{Day: m.Day, Mealtime: m.Mealtime, Text: m.Text}, w.calID, w.o)
	if err != nil {
		return err
	}
	if old.exists {
		ev.ID = old.eventID
		if err := updateEventRow(tx, householdID, ev); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE meal_plan SET text = ?, source = ?, template_id = ?, template_dish = ?, recipe_id = ?, portions = ?, updated_at = ? WHERE household_id = ? AND day = ? AND mealtime = ?`,
			m.Text, m.Source, m.TemplateID, m.TemplateDish, m.RecipeID, m.Portions, now(), householdID, m.Day, m.Mealtime)
		return err
	}
	ev.ID = newID()
	if err := insertEventRow(tx, householdID, ev); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO meal_plan (household_id, day, mealtime, text, event_id, updated_at, source, template_id, template_dish, recipe_id, portions) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		householdID, m.Day, m.Mealtime, m.Text, ev.ID, now(), m.Source, m.TemplateID, m.TemplateDish, m.RecipeID, m.Portions)
	return err
}

// mealEvent is the all-day event of an entry: the whole of its day.
func mealEvent(m Meal, calendarID string, o MealOptions) (calendar.Event, error) {
	loc := o.Location
	if loc == nil {
		loc = time.UTC
	}
	day, err := time.ParseInLocation(dateLayout, m.Day, loc)
	if err != nil {
		return calendar.Event{}, err
	}
	return calendar.Event{
		CalendarID: calendarID, Title: o.Title(m.Mealtime, m.Text), AllDay: true,
		Start: day, End: day.AddDate(0, 0, 1),
	}, nil
}

// mealCalendarID returns the id of the household's meal-plan calendar, creating
// it (read-only, shared through the box) if the household has none.
func mealCalendarID(tx *sql.Tx, householdID, name string) (string, error) {
	var id string
	err := tx.QueryRow(`SELECT id FROM calendars WHERE household_id = ? AND kind = 'meals' ORDER BY created_at, rowid LIMIT 1`, householdID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id, ts := newID(), now()
	_, err = tx.Exec(`INSERT INTO calendars (id, household_id, name, kind, color, read_only, shared, created_at, updated_at)
		VALUES (?, ?, ?, 'meals', ?, 1, 1, ?, ?)`, id, householdID, name, mealCalendarColor, ts, ts)
	return id, err
}

// CountMeals says how many entries the meal plan has, without loading them.
func (db *DB) CountMeals(householdID string) (n int, err error) {
	err = db.sql.QueryRow(`SELECT COUNT(*) FROM meal_plan WHERE household_id = ?`, householdID).Scan(&n)
	return n, err
}

// Meals lists the entries from one day to another, both included (YYYY-MM-DD),
// by day and then by mealtime in the order of the day. It reads the hand-typed
// entries only, without templates or adjustments; the API and pages use
// MealPlan. Kept on purpose for the tests of the plain entries.
func (db *DB) Meals(householdID, from, to string) ([]Meal, error) {
	rows, err := db.sql.Query(`SELECT day, mealtime, text, recipe_id, portions FROM meal_plan WHERE household_id = ? AND day >= ? AND day <= ?
		ORDER BY day, CASE mealtime WHEN 'breakfast' THEN 1 WHEN 'lunch' THEN 2 WHEN 'snack' THEN 3 ELSE 4 END`, householdID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Meal
	for rows.Next() {
		var m Meal
		if err := rows.Scan(&m.Day, &m.Mealtime, &m.Text, &m.RecipeID, &m.Portions); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// PlannedMeal is a day and mealtime of the meal plan.
type PlannedMeal struct {
	Day      string
	Mealtime meal.Mealtime
}

// PlannedMealsOfRecipe lists every meal from today on that is the recipe, by
// day and then in the order of the day.
func (db *DB) PlannedMealsOfRecipe(householdID, recipeID, today string) ([]PlannedMeal, error) {
	rows, err := db.sql.Query(`SELECT day, mealtime FROM meal_plan WHERE household_id = ? AND recipe_id = ? AND day >= ?
		ORDER BY day, CASE mealtime WHEN 'breakfast' THEN 1 WHEN 'lunch' THEN 2 WHEN 'snack' THEN 3 ELSE 4 END`, householdID, recipeID, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlannedMeal
	for rows.Next() {
		var m PlannedMeal
		if err := rows.Scan(&m.Day, &m.Mealtime); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// earlierDishRows is how many different earlier dishes are read at most.
const earlierDishRows = 2000

// EarlierDishes lists the free-text meals of days up to today, each text with
// the last day it was planned, the most recent first. Recipe entries are left
// out; so are days to come.
func (db *DB) EarlierDishes(householdID, today string) ([]recipe.Dish, error) {
	rows, err := db.sql.Query(`SELECT text, MAX(day) AS last FROM meal_plan WHERE household_id = ? AND recipe_id = '' AND text <> '' AND day <= ?
		GROUP BY text ORDER BY last DESC, text LIMIT ?`, householdID, today, earlierDishRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recipe.Dish
	for rows.Next() {
		var d recipe.Dish
		if err := rows.Scan(&d.Text, &d.Day); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
