package server

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

// mealCell is one field of the week form. Kind says where its meal comes from:
// "free" (typed by hand or empty), "template" or "adjusted".
type mealCell struct {
	Name, Mealtime, Value string
	Kind                  string
	TemplateID            string
	TemplateName          string
	TemplateDish          string
	CanRestore            bool // an adjusted meal of today or later can go back to its template
}

type mealDay struct {
	Date    string // YYYY-MM-DD
	Weekday int    // time.Weekday, for the day's name
	Short   string // 17/11
	Today   bool
	Cells   []mealCell
	Planned []mealCell // the cells with an entry, for the read-only view
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
func weekDays(monday time.Time, today string, values map[string]string, marks map[string]store.PlanMeal) []mealDay {
	var days []mealDay
	for i := 0; i < 7; i++ {
		d := monday.AddDate(0, 0, i)
		day := mealDay{Date: d.Format(task.DateLayout), Weekday: int(d.Weekday()), Short: d.Format("02/01")}
		day.Today = day.Date == today
		for _, m := range meal.Mealtimes() {
			c := mealCell{Name: mealField(day.Date, m), Mealtime: string(m)}
			c.Value = values[c.Name]
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
			if c.Value != "" {
				day.Planned = append(day.Planned, c)
			}
		}
		days = append(days, day)
	}
	return days
}

func (s *server) renderMeals(w http.ResponseWriter, r *http.Request, p principal, status int, monday time.Time, typed map[string]string, errKey, errField string) {
	sunday := monday.AddDate(0, 0, 6)
	today := s.today(p)
	s.rollMealPlan(p.household, s.langOf(r))
	list, err := s.db.MealPlan(p.household.ID, monday.Format(task.DateLayout), sunday.Format(task.DateLayout), today)
	if err != nil {
		s.fail(w, "meals", err)
		return
	}
	values := map[string]string{}
	marks := map[string]store.PlanMeal{}
	for _, m := range list {
		values[mealField(m.Day, m.Mealtime)] = m.Text
		marks[mealField(m.Day, m.Mealtime)] = m
	}
	if typed != nil {
		values = typed
	}
	days := weekDays(monday, today, values, marks)
	planned := false
	errDay, errMealtime := 0, ""
	for _, d := range days {
		planned = planned || len(d.Planned) > 0
		for _, c := range d.Cells {
			if errField != "" && c.Name == errField {
				errDay, errMealtime = d.Weekday, c.Mealtime
			}
		}
	}
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Days": days, "Planned": planned,
		"Week": monday.Format(task.DateLayout), "Prev": monday.AddDate(0, 0, -7).Format(task.DateLayout), "Next": monday.AddDate(0, 0, 7).Format(task.DateLayout),
		"WeekNo": isoWeek(monday), "From": monday.Format("02/01"), "To": sunday.Format("02/01/2006"), "ErrorWeekday": errDay, "ErrorMealtime": errMealtime, "ThisWeek": monday.Format(task.DateLayout) == weekOf(mustDay(today)).Format(task.DateLayout),
		"Editable": p.person.Role == store.RoleParent, "Error": errKey, "ErrorField": errField,
	}
	if p.person.Role == store.RoleParent {
		sum, err := s.templateSummary(p, s.langOf(r), monday)
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
	s.renderMeals(w, r, p, http.StatusOK, requestedWeek(r.URL.Query().Get("week"), s.today(p)), nil, "", "")
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
// form, an empty one removing its meal. Fields of other weeks are ignored, so
// a stale form cannot change them.
func (s *server) handleMealsSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	monday := requestedWeek(r.PostFormValue("week"), s.today(p))
	var changes []store.Meal
	typed := map[string]string{}
	badField, badKey := "", ""
	restore := r.PostFormValue("restore") // "Back to template" of one meal, saved with the week
	for _, d := range weekDays(monday, s.today(p), nil, nil) {
		for _, c := range d.Cells {
			vals, present := r.PostForm[c.Name]
			if !present || len(vals) == 0 {
				continue
			}
			typed[c.Name] = vals[0]
			text, err := meal.CleanText(vals[0])
			if err != nil && badField == "" {
				badField, badKey = c.Name, "meals.error.text"
			}
			if c.Name == restore {
				continue // it goes back to the template, whatever is typed
			}
			changes = append(changes, store.Meal{Day: d.Date, Mealtime: meal.Mealtime(c.Mealtime), Text: text})
		}
	}
	if badField != "" {
		s.renderMeals(w, r, p, http.StatusUnprocessableEntity, monday, typed, badKey, badField)
		return
	}
	switch err := s.saveMeals(r, p, changes); {
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
		s.renderMeals(w, r, p, http.StatusUnprocessableEntity, monday, typed, "meals.error.text", "")
	default:
		s.fail(w, "save meals", err)
	}
}
