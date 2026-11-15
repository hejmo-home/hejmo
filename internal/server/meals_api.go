package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

// maxMealDays is the longest range one request may list.
const maxMealDays = 62

type mealJSON struct {
	Date     string `json:"date"`
	Mealtime string `json:"mealtime"`
	Text     string `json:"text"`
}

func mealToJSON(m store.Meal) mealJSON { return mealJSON{m.Day, string(m.Mealtime), m.Text} }

// mealOptions says how a save is written into the meal-plan calendar: the
// calendar's name and the event titles use the language of the request.
func (s *server) mealOptions(lang string, p principal) store.MealOptions {
	return store.MealOptions{
		CalendarName: s.i18n.T(lang, "meals.calendar_name"),
		Location:     p.location(),
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
	list, err := s.db.Meals(p.household.ID, from, to)
	if err != nil {
		s.fail(w, "list meals", err)
		return
	}
	out := make([]mealJSON, 0, len(list))
	for _, m := range list {
		out = append(out, mealToJSON(m))
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
		Text *string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	m, ok := mealTarget(w, r)
	if !ok {
		return
	}
	if in.Text == nil {
		writeError(w, http.StatusUnprocessableEntity, "text is required")
		return
	}
	text, err := meal.CleanText(*in.Text)
	if err == nil && text == "" {
		err = &meal.InvalidError{Field: "text", Msg: "text is empty: remove a meal with DELETE"}
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	m.Text = text
	switch err := s.saveMeals(r, p, []store.Meal{m}); {
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case err != nil:
		s.fail(w, "set meal", err)
	default:
		writeJSON(w, http.StatusOK, mealToJSON(m))
	}
}

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
