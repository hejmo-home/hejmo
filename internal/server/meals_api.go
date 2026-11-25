package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

// maxMealDays is the longest range one request may list.
const maxMealDays = 62

type mealJSON struct {
	Date         string `json:"date"`
	Mealtime     string `json:"mealtime"`
	Text         string `json:"text"`
	Dish         string `json:"dish"`
	RecipeID     string `json:"recipe_id,omitempty"`
	Portions     int    `json:"portions,omitempty"`
	Source       string `json:"source"`
	TemplateID   string `json:"template_id,omitempty"`
	TemplateDish string `json:"template_dish,omitempty"`
}

// mealToJSON is an entry of the plan: its text is the dish, the recipe's current name for a recipe.
func mealToJSON(m store.PlanMeal) mealJSON {
	return mealJSON{m.Day, string(m.Mealtime), m.Text, m.Text, m.RecipeID, m.Portions, m.Source, m.TemplateID, m.TemplateDish}
}

// householdPortions is how many portions a recipe entry starts with: the
// number of people in the household, from 1 to 99.
func (s *server) householdPortions(p principal) (int, error) {
	people, err := s.db.People(p.household.ID)
	if err != nil {
		return 0, err
	}
	return store.HouseholdPortions(len(people)), nil
}

// planMeal is the meal a cell shows now, with where it comes from.
func (s *server) planMeal(p principal, day string, m meal.Mealtime) (store.PlanMeal, error) {
	list, err := s.db.MealPlan(p.household.ID, day, day, s.today(p))
	if err != nil {
		return store.PlanMeal{}, err
	}
	for _, e := range list {
		if e.Mealtime == m {
			return e, nil
		}
	}
	return store.PlanMeal{Day: day, Mealtime: m, Source: store.SourceManual}, nil
}

// mealOptions says how a save is written into the meal-plan calendar: the
// calendar's name and the event titles use the language of the request.
func (s *server) mealOptions(lang string, p principal) store.MealOptions {
	return store.MealOptions{
		CalendarName: s.i18n.T(lang, "meals.calendar_name"),
		Location:     p.location(),
		Today:        s.today(p),
		Title: func(m meal.Mealtime, text string) string {
			return meal.EventTitle(s.i18n.T(lang, "meals.mealtime."+string(m)), text)
		},
	}
}

// saveMeals stores entries of the meal plan; empty text removes an entry.
func (s *server) saveMeals(r *http.Request, p principal, changes []store.Meal) error {
	return s.db.SetMeals(p.household.ID, changes, s.mealOptions(s.langOf(r), p))
}

func (s *server) apiListMeals(w http.ResponseWriter, r *http.Request, p principal) {
	q := r.URL.Query()
	from, to := q.Get("from"), q.Get("to")
	switch {
	case from == "" && to == "":
		day, err := time.Parse(task.DateLayout, s.today(p))
		if err != nil {
			s.fail(w, "today", err)
			return
		}
		from, to = day.Format(task.DateLayout), day.AddDate(0, 0, 6).Format(task.DateLayout)
	case from == "" || to == "":
		writeError(w, http.StatusUnprocessableEntity, "give both from and to, or neither")
		return
	}
	a, errA := time.Parse(task.DateLayout, from)
	b, errB := time.Parse(task.DateLayout, to)
	if errA != nil || errB != nil || b.Before(a) || b.Sub(a) > (maxMealDays-1)*24*time.Hour {
		writeError(w, http.StatusUnprocessableEntity, "from and to must be days (YYYY-MM-DD), to not before from, at most 62 days")
		return
	}
	list, err := s.db.MealPlan(p.household.ID, from, to, s.today(p))
	if err != nil {
		s.fail(w, "list meals", err)
		return
	}
	out := make([]mealJSON, 0, len(list))
	for _, m := range list {
		if m.Text != "" { // an entry emptied for a week is no meal
			out = append(out, mealToJSON(m))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// mealTarget reads the day and mealtime of the path.
func mealTarget(w http.ResponseWriter, r *http.Request) (store.Meal, bool) {
	m := store.Meal{Day: r.PathValue("date"), Mealtime: meal.Mealtime(r.PathValue("mealtime"))}
	if _, err := time.Parse(task.DateLayout, m.Day); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "date must be a day (YYYY-MM-DD)")
		return m, false
	}
	if !m.Mealtime.Valid() {
		writeError(w, http.StatusUnprocessableEntity, "mealtime must be breakfast, lunch, snack or dinner")
		return m, false
	}
	return m, true
}

func (s *server) apiSetMeal(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Text     *string `json:"text"`
		RecipeID *string `json:"recipe_id"`
		Portions *int    `json:"portions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	m, ok := mealTarget(w, r)
	if !ok {
		return
	}
	switch {
	case in.Text != nil && in.RecipeID != nil:
		writeError(w, http.StatusUnprocessableEntity, "give text or recipe_id, not both")
		return
	case in.Text == nil && in.RecipeID == nil:
		writeError(w, http.StatusUnprocessableEntity, "text or recipe_id is required")
		return
	case in.RecipeID == nil && in.Portions != nil:
		writeError(w, http.StatusUnprocessableEntity, "portions need a recipe_id")
		return
	case in.RecipeID != nil:
		m.RecipeID = *in.RecipeID
		if in.Portions != nil {
			m.Portions = *in.Portions
		} else {
			n, err := s.householdPortions(p)
			if err != nil {
				s.fail(w, "household size", err)
				return
			}
			m.Portions = n
		}
		if m.RecipeID == "" || m.Portions < 1 || m.Portions > recipe.MaxPortions {
			writeError(w, http.StatusUnprocessableEntity, "recipe_id is a recipe of the household, portions a whole number from 1 to 99")
			return
		}
	default:
		text, err := meal.CleanText(*in.Text)
		if err == nil && text == "" {
			err = &meal.InvalidError{Field: "text", Msg: "text is empty: remove a meal with DELETE"}
		}
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		m.Text = text
	}
	switch err := s.saveMeals(r, p, []store.Meal{m}); {
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case err != nil:
		s.fail(w, "set meal", err)
	default:
		got, err := s.planMeal(p, m.Day, m.Mealtime)
		if err != nil {
			s.fail(w, "read meal", err)
			return
		}
		writeJSON(w, http.StatusOK, mealToJSON(got))
	}
}

// apiRestoreMeal is "Back to template": it drops the week's change of a meal.
func (s *server) apiRestoreMeal(w http.ResponseWriter, r *http.Request, p principal) {
	m, ok := mealTarget(w, r)
	if !ok {
		return
	}
	switch err := s.db.RestoreMeal(p.household.ID, m.Day, m.Mealtime, s.mealOptions(s.langOf(r), p)); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "this meal is not adjusted")
		return
	case err != nil:
		s.fail(w, "restore meal", err)
		return
	}
	got, err := s.planMeal(p, m.Day, m.Mealtime)
	if err != nil {
		s.fail(w, "read meal", err)
		return
	}
	writeJSON(w, http.StatusOK, mealToJSON(got))
}

// apiDeleteMeal removes a meal. Where a template fills the day, it empties the
// meal for that week (an adjustment).
func (s *server) apiDeleteMeal(w http.ResponseWriter, r *http.Request, p principal) {
	m, ok := mealTarget(w, r)
	if !ok {
		return
	}
	if err := s.saveMeals(r, p, []store.Meal{m}); err != nil { // empty text removes
		s.fail(w, "delete meal", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
