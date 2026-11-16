package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

// shippedTemplate is a template that comes with Hejmo in the language in use:
// its name and the titles of its steps are locale texts.
func (s *server) shippedTemplate(lang string, st task.ShippedTemplate) task.RoutineTemplate {
	t := task.RoutineTemplate{ID: st.ID, Name: s.i18n.T(lang, "routine.tpl."+st.ID), Part: st.Part, Shipped: true}
	for _, step := range st.Steps {
		t.Steps = append(t.Steps, task.TemplateStep{ID: step.Key, Title: s.i18n.T(lang, "routine.tpl."+st.ID+"."+step.Key),
			Emoji: step.Emoji, Stars: step.Stars, Repeat: step.Repeat(), Weekdays: step.Weekdays})
	}
	return t
}

// shippedTemplates lists the templates that come with Hejmo, in the language in use.
func (s *server) shippedTemplates(lang string) []task.RoutineTemplate {
	out := make([]task.RoutineTemplate, 0, len(task.Shipped))
	for _, st := range task.Shipped {
		out = append(out, s.shippedTemplate(lang, st))
	}
	return out
}

// routineTemplate finds a shipped or an own template; ErrNotFound if there is none.
func (s *server) routineTemplate(lang, householdID, id string) (task.RoutineTemplate, error) {
	if st, ok := task.ShippedByID(id); ok {
		return s.shippedTemplate(lang, st), nil
	}
	return s.db.RoutineTemplate(householdID, id)
}

// defaultTemplateName is the name a saved template is offered: "Abendroutine
// von Mia", cut to the longest name a template may have.
func (s *server) defaultTemplateName(lang, routine, person string) string {
	name := s.i18n.Format(lang, i18n.Msg("routine.tpl.default_name", "routine", routine, "person", person))
	if utf8.RuneCountInString(name) > task.MaxRoutineName {
		name = string([]rune(name)[:task.MaxRoutineName])
	}
	return name
}

// errShippedTemplate is returned for a change to a template that comes with Hejmo.
var errShippedTemplate = errors.New("a template that comes with Hejmo cannot be changed; use it, adapt the routine and save it as your own")

// ownTemplate loads a template that can be changed: shipped ones are errShippedTemplate.
func (s *server) ownTemplate(householdID, id string) (task.RoutineTemplate, error) {
	if _, ok := task.ShippedByID(id); ok {
		return task.RoutineTemplate{}, errShippedTemplate
	}
	return s.db.RoutineTemplate(householdID, id)
}

// applyTemplate makes a routine for a person from a template: an independent
// copy of its steps, starting today. An empty part takes the template's
// suggestion, an empty name the stock name of the part. An unknown template is
// an invalid "template".
func (s *server) applyTemplate(p principal, lang, templateID, personID string, part task.PartOfDay, name string) (task.Routine, error) {
	tpl, err := s.routineTemplate(lang, p.household.ID, templateID)
	if errors.Is(err, store.ErrNotFound) {
		return task.Routine{}, store.InvalidField("template", "unknown template")
	} else if err != nil {
		return task.Routine{}, err
	}
	if part == "" {
		part = tpl.Part
	}
	if name == "" {
		name = s.defaultRoutineName(lang, string(part))
	}
	return s.db.CreateRoutineFromSteps(p.household.ID, task.Routine{PersonID: personID, Name: name, Part: part}, tpl.Steps, s.today(p))
}

type templateStepJSON struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Emoji    string   `json:"emoji"`
	Stars    int      `json:"stars"`
	Repeat   string   `json:"repeat"`
	Weekdays []string `json:"weekdays"`
}

type routineTemplateJSON struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	PartOfDay     string             `json:"part_of_day"`
	Source        string             `json:"source"`
	OriginRoutine string             `json:"origin_routine,omitempty"`
	OriginPerson  string             `json:"origin_person,omitempty"`
	SavedAt       *time.Time         `json:"saved_at,omitempty"`
	Steps         []templateStepJSON `json:"steps"`
}

func routineTemplateToJSON(t task.RoutineTemplate) routineTemplateJSON {
	j := routineTemplateJSON{ID: t.ID, Name: t.Name, PartOfDay: string(t.Part), Source: "own", Steps: []templateStepJSON{}}
	if t.Shipped {
		j.Source = "shipped"
	} else {
		j.OriginRoutine, j.OriginPerson = t.OriginRoutine, t.OriginPerson
		if !t.SavedAt.IsZero() {
			at := t.SavedAt
			j.SavedAt = &at
		}
	}
	for _, s := range t.Steps {
		j.Steps = append(j.Steps, templateStepJSON{ID: s.ID, Title: s.Title, Emoji: s.Emoji, Stars: s.Stars, Repeat: string(s.Repeat), Weekdays: nonNil(s.Weekdays)})
	}
	return j
}

// templateError answers a store error of a template call.
func (s *server) templateError(w http.ResponseWriter, what string, err error) {
	var exists *store.TemplateExistsError
	switch {
	case errors.Is(err, errShippedTemplate):
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &exists):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "field": "name"})
	default:
		s.routineError(w, what, err)
	}
}

func (s *server) writeTemplate(w http.ResponseWriter, p principal, id string, status int) {
	t, err := s.db.RoutineTemplate(p.household.ID, id)
	if err != nil {
		s.templateError(w, "routine template", err)
		return
	}
	writeJSON(w, status, routineTemplateToJSON(t))
}

func (s *server) apiListRoutineTemplates(w http.ResponseWriter, r *http.Request, p principal) {
	own, err := s.db.RoutineTemplates(p.household.ID)
	if err != nil {
		s.fail(w, "list routine templates", err)
		return
	}
	out := make([]routineTemplateJSON, 0, len(own)+len(task.Shipped))
	for _, t := range own {
		out = append(out, routineTemplateToJSON(t))
	}
	for _, t := range s.shippedTemplates(s.langOf(r)) {
		out = append(out, routineTemplateToJSON(t))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiGetRoutineTemplate(w http.ResponseWriter, r *http.Request, p principal) {
	t, err := s.routineTemplate(s.langOf(r), p.household.ID, r.PathValue("id"))
	if err != nil {
		s.templateError(w, "routine template", err)
		return
	}
	writeJSON(w, http.StatusOK, routineTemplateToJSON(t))
}

func (s *server) apiSaveRoutineTemplate(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		RoutineID string `json:"routine_id"`
		Name      string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	rt, err := s.db.Routine(p.household.ID, in.RoutineID)
	if err != nil {
		s.templateError(w, "routine", err)
		return
	}
	name := in.Name
	if name == "" {
		people, _ := s.db.People(p.household.ID)
		owner, _ := personNamedIn(people, rt.PersonID)
		name = s.defaultTemplateName(s.langOf(r), rt.Name, owner.Name)
	}
	tpl, err := s.db.SaveRoutineAsTemplate(p.household.ID, rt.ID, name)
	if err != nil {
		s.templateError(w, "routine template", err)
		return
	}
	s.writeTemplate(w, p, tpl.ID, http.StatusCreated)
}

func (s *server) apiPatchRoutineTemplate(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Name      *string `json:"name"`
		PartOfDay *string `json:"part_of_day"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON or an unknown field: only name and part_of_day can be changed")
		return
	}
	cur, err := s.ownTemplate(p.household.ID, r.PathValue("id"))
	if err != nil {
		s.templateError(w, "routine template", err)
		return
	}
	name, part := cur.Name, cur.Part
	if in.Name != nil {
		name = *in.Name
	}
	if in.PartOfDay != nil {
		part = task.PartOfDay(*in.PartOfDay)
	}
	if err := s.db.UpdateRoutineTemplate(p.household.ID, cur.ID, name, part); err != nil {
		s.templateError(w, "routine template", err)
		return
	}
	s.writeTemplate(w, p, cur.ID, http.StatusOK)
}

func (s *server) apiDeleteRoutineTemplate(w http.ResponseWriter, r *http.Request, p principal) {
	cur, err := s.ownTemplate(p.household.ID, r.PathValue("id"))
	if err == nil {
		err = s.db.DeleteRoutineTemplate(p.household.ID, cur.ID)
	}
	if err != nil {
		s.templateError(w, "routine template", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) apiAddTemplateStep(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Title    string   `json:"title"`
		Emoji    string   `json:"emoji"`
		Stars    *int     `json:"stars"`
		Repeat   string   `json:"repeat"`
		Weekdays []string `json:"weekdays"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	cur, err := s.ownTemplate(p.household.ID, r.PathValue("id"))
	if err != nil {
		s.templateError(w, "routine template", err)
		return
	}
	step := task.TemplateStep{Title: in.Title, Emoji: in.Emoji, Stars: 1, Repeat: task.Repeat(in.Repeat), Weekdays: in.Weekdays}
	if in.Stars != nil {
		step.Stars = *in.Stars
	}
	if _, err := s.db.AddTemplateStep(p.household.ID, cur.ID, step); err != nil {
		s.templateError(w, "routine template", err)
		return
	}
	s.writeTemplate(w, p, cur.ID, http.StatusCreated)
}

func (s *server) apiPatchTemplateStep(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Title    *string  `json:"title"`
		Emoji    *string  `json:"emoji"`
		Stars    *int     `json:"stars"`
		Repeat   *string  `json:"repeat"`
		Weekdays []string `json:"weekdays"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON or an unknown field")
		return
	}
	cur, err := s.ownTemplate(p.household.ID, r.PathValue("id"))
	if err != nil {
		s.templateError(w, "routine template", err)
		return
	}
	var step task.TemplateStep
	found := false
	for _, st := range cur.Steps {
		if st.ID == r.PathValue("step_id") {
			step, found = st, true
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "step not found")
		return
	}
	if in.Title != nil {
		step.Title = *in.Title
	}
	if in.Emoji != nil {
		step.Emoji = *in.Emoji
	}
	if in.Stars != nil {
		step.Stars = *in.Stars
	}
	if in.Repeat != nil {
		step.Repeat = task.Repeat(*in.Repeat)
		step.Weekdays = nil
	}
	if in.Weekdays != nil {
		step.Weekdays = in.Weekdays
	}
	if step.Repeat == task.Daily {
		step.Weekdays = nil
	}
	if err := s.db.UpdateTemplateStep(p.household.ID, cur.ID, step); err != nil {
		s.templateError(w, "routine template", err)
		return
	}
	s.writeTemplate(w, p, cur.ID, http.StatusOK)
}

func (s *server) apiDeleteTemplateStep(w http.ResponseWriter, r *http.Request, p principal) {
	cur, err := s.ownTemplate(p.household.ID, r.PathValue("id"))
	if err == nil {
		err = s.db.DeleteTemplateStep(p.household.ID, cur.ID, r.PathValue("step_id"))
	}
	if err != nil {
		s.templateError(w, "routine template step", err)
		return
	}
	s.writeTemplate(w, p, cur.ID, http.StatusOK)
}

func (s *server) apiTemplateOrder(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		StepIDs []string `json:"step_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	cur, err := s.ownTemplate(p.household.ID, r.PathValue("id"))
	if err == nil {
		err = s.db.SetTemplateOrder(p.household.ID, cur.ID, in.StepIDs)
	}
	if err != nil {
		s.templateError(w, "routine template", err)
		return
	}
	s.writeTemplate(w, p, cur.ID, http.StatusOK)
}
