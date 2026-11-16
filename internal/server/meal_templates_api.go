package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

type templateEntryJSON struct {
	Weekday  int    `json:"weekday"` // 1 Monday to 7 Sunday
	Mealtime string `json:"mealtime"`
	Text     string `json:"text"`
}

type mealTemplateJSON struct {
	ID        string              `json:"id,omitempty"`
	Name      string              `json:"name"`
	Mealtimes string              `json:"mealtimes"` // "all" or "one"
	Mealtime  string              `json:"mealtime,omitempty"`
	Interval  int                 `json:"interval"`
	StartWeek string              `json:"start_week"`
	Entries   []templateEntryJSON `json:"entries"`
}

func templateToJSON(t meal.Template) mealTemplateJSON {
	out := mealTemplateJSON{ID: t.ID, Name: t.Name, Mealtimes: "one", Mealtime: string(t.Mealtime), Interval: t.Interval, StartWeek: t.Start, Entries: []templateEntryJSON{}}
	if t.AllMealtimes {
		out.Mealtimes, out.Mealtime = "all", ""
	}
	filled := t.Filled()
	slots := make([]meal.Slot, 0, len(filled))
	for s := range filled {
		slots = append(slots, s)
	}
	order := meal.Mealtimes()
	slices.SortFunc(slots, func(a, b meal.Slot) int {
		if a.Weekday != b.Weekday {
			return a.Weekday - b.Weekday
		}
		return slices.Index(order, a.Mealtime) - slices.Index(order, b.Mealtime)
	})
	for _, s := range slots {
		out.Entries = append(out.Entries, templateEntryJSON{s.Weekday, string(s.Mealtime), filled[s]})
	}
	return out
}

// templateFromJSON reads a template from a request; the error says what is wrong.
func templateFromJSON(in mealTemplateJSON) (meal.Template, error) {
	t := meal.Template{Name: in.Name, Interval: in.Interval, Start: in.StartWeek, Mealtime: meal.Mealtime(in.Mealtime), Entries: map[meal.Slot]string{}}
	switch in.Mealtimes {
	case "all":
		t.AllMealtimes, t.Mealtime = true, ""
	case "one":
	default:
		return t, &meal.InvalidError{Field: "mealtimes", Msg: "mealtimes is all or one"}
	}
	for _, e := range in.Entries {
		if e.Weekday < 1 || e.Weekday > 7 {
			return t, &meal.InvalidError{Field: "entries", Msg: "weekday is 1 (Monday) to 7 (Sunday)"}
		}
		if !meal.Mealtime(e.Mealtime).Valid() {
			return t, &meal.InvalidError{Field: "entries", Msg: "unknown mealtime"}
		}
		t.Entries[meal.Slot{Weekday: e.Weekday, Mealtime: meal.Mealtime(e.Mealtime)}] = e.Text
	}
	return t, nil
}

type clashJSON struct {
	Template  map[string]string `json:"template"`
	Mealtime  string            `json:"mealtime"`
	Weekdays  []int             `json:"weekdays"`
	FirstWeek string            `json:"first_week"`
}

func (s *server) apiListMealTemplates(w http.ResponseWriter, r *http.Request, p principal) {
	list, err := s.db.MealTemplates(p.household.ID)
	if err != nil {
		s.fail(w, "list meal templates", err)
		return
	}
	out := make([]mealTemplateJSON, 0, len(list))
	for _, t := range list {
		out = append(out, templateToJSON(t))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiGetMealTemplate(w http.ResponseWriter, r *http.Request, p principal) {
	t, err := s.db.MealTemplate(p.household.ID, r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such template")
	case err != nil:
		s.fail(w, "get meal template", err)
	default:
		writeJSON(w, http.StatusOK, templateToJSON(t))
	}
}

// writeTemplateError answers a refused template save.
func (s *server) writeTemplateError(w http.ResponseWriter, what string, err error) {
	var clash *store.ClashError
	switch {
	case errors.As(err, &clash):
		out := make([]clashJSON, 0, len(clash.Clashes))
		for _, c := range clash.Clashes {
			out = append(out, clashJSON{Template: map[string]string{"id": c.Template.ID, "name": c.Template.Name}, Mealtime: string(c.Clash.Mealtime),
				Weekdays: c.Clash.Weekdays, FirstWeek: c.Clash.Week.Format(task.DateLayout)})
		}
		writeJSON(w, http.StatusConflict, map[string]any{"error": clash.Error(), "clashes": out})
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such template")
	default:
		s.fail(w, what, err)
	}
}

func readTemplate(w http.ResponseWriter, r *http.Request) (meal.Template, bool) {
	var in mealTemplateJSON
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return meal.Template{}, false
	}
	t, err := templateFromJSON(in)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return t, false
	}
	return t, true
}

func (s *server) apiCreateMealTemplate(w http.ResponseWriter, r *http.Request, p principal) {
	t, ok := readTemplate(w, r)
	if !ok {
		return
	}
	got, err := s.db.CreateMealTemplate(p.household.ID, t, s.mealOptions(s.langOf(r), p))
	if err != nil {
		s.writeTemplateError(w, "create meal template", err)
		return
	}
	writeJSON(w, http.StatusCreated, templateToJSON(got))
}

func (s *server) apiUpdateMealTemplate(w http.ResponseWriter, r *http.Request, p principal) {
	t, ok := readTemplate(w, r)
	if !ok {
		return
	}
	t.ID = r.PathValue("id")
	got, err := s.db.UpdateMealTemplate(p.household.ID, t, s.mealOptions(s.langOf(r), p))
	if err != nil {
		s.writeTemplateError(w, "update meal template", err)
		return
	}
	writeJSON(w, http.StatusOK, templateToJSON(got))
}

func (s *server) apiDeleteMealTemplate(w http.ResponseWriter, r *http.Request, p principal) {
	switch err := s.db.DeleteMealTemplate(p.household.ID, r.PathValue("id"), s.mealOptions(s.langOf(r), p)); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such template")
	case err != nil:
		s.fail(w, "delete meal template", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
