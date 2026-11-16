package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

type routineJSON struct {
	ID        string   `json:"id"`
	PersonID  string   `json:"person_id"`
	Name      string   `json:"name"`
	PartOfDay string   `json:"part_of_day"`
	TaskIDs   []string `json:"task_ids"`
}

// routineToJSON renders a routine with the ids of its tasks in their order.
func (s *server) routineToJSON(householdID string, r task.Routine) (routineJSON, error) {
	list, err := s.db.RoutineTasks(householdID, r.ID)
	if err != nil {
		return routineJSON{}, err
	}
	ids := make([]string, 0, len(list))
	for _, k := range list {
		ids = append(ids, k.ID)
	}
	return routineJSON{ID: r.ID, PersonID: r.PersonID, Name: r.Name, PartOfDay: string(r.Part), TaskIDs: ids}, nil
}

// routineError answers a store error of a routine call: 422 for input that
// cannot be stored, 409 for a clash (naming the routine in the way), 404 for
// something that is not there.
func (s *server) routineError(w http.ResponseWriter, what string, err error) {
	var exists *store.RoutineExistsError
	var bad *task.InvalidError
	switch {
	case errors.As(err, &exists):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "existing_routine_id": exists.Existing.ID})
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, what+" not found")
	case errors.Is(err, store.ErrInvalid) && errors.As(err, &bad):
		writeFieldError(w, http.StatusUnprocessableEntity, bad.Field, err.Error())
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		s.fail(w, what, err)
	}
}

func (s *server) apiListRoutines(w http.ResponseWriter, r *http.Request, p principal) {
	person := r.URL.Query().Get("person")
	if person != "" && !uuidPattern.MatchString(person) {
		writeError(w, http.StatusUnprocessableEntity, "person must be a UUID")
		return
	}
	list, err := s.db.Routines(p.household.ID)
	if err != nil {
		s.fail(w, "list routines", err)
		return
	}
	out := make([]routineJSON, 0, len(list))
	for _, rt := range list {
		if person != "" && rt.PersonID != person {
			continue
		}
		j, err := s.routineToJSON(p.household.ID, rt)
		if err != nil {
			s.fail(w, "routine tasks", err)
			return
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiGetRoutine(w http.ResponseWriter, r *http.Request, p principal) {
	s.writeRoutine(w, p, r.PathValue("id"), http.StatusOK)
}

// writeRoutine answers with the routine as it is stored now.
func (s *server) writeRoutine(w http.ResponseWriter, p principal, id string, status int) {
	rt, err := s.db.Routine(p.household.ID, id)
	if err != nil {
		s.routineError(w, "routine", err)
		return
	}
	j, err := s.routineToJSON(p.household.ID, rt)
	if err != nil {
		s.fail(w, "routine tasks", err)
		return
	}
	writeJSON(w, status, j)
}

func (s *server) apiCreateRoutine(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		PersonID  string `json:"person_id"`
		Name      string `json:"name"`
		PartOfDay string `json:"part_of_day"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	rt, err := s.db.CreateRoutine(p.household.ID, task.Routine{PersonID: in.PersonID, Name: in.Name, Part: task.PartOfDay(in.PartOfDay)})
	if err != nil {
		s.routineError(w, "create routine", err)
		return
	}
	s.writeRoutine(w, p, rt.ID, http.StatusCreated)
}

func (s *server) apiPatchRoutine(w http.ResponseWriter, r *http.Request, p principal) {
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
	id := r.PathValue("id")
	cur, err := s.db.Routine(p.household.ID, id)
	if err != nil {
		s.routineError(w, "routine", err)
		return
	}
	name, part := cur.Name, cur.Part
	if in.Name != nil {
		name = *in.Name
	}
	if in.PartOfDay != nil {
		part = task.PartOfDay(*in.PartOfDay)
	}
	if err := s.db.UpdateRoutine(p.household.ID, id, name, part); err != nil {
		s.routineError(w, "routine", err)
		return
	}
	s.writeRoutine(w, p, id, http.StatusOK)
}

// apiDissolveRoutine removes the routine and keeps its tasks as single tasks.
func (s *server) apiDissolveRoutine(w http.ResponseWriter, r *http.Request, p principal) {
	if err := s.db.DissolveRoutine(p.household.ID, r.PathValue("id")); err != nil {
		s.routineError(w, "routine", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) apiRoutineAddTask(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		TaskID string `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	id := r.PathValue("id")
	if err := s.db.AddTaskToRoutine(p.household.ID, id, in.TaskID); err != nil {
		s.routineError(w, "routine or task", err)
		return
	}
	s.writeRoutine(w, p, id, http.StatusOK)
}

func (s *server) apiRoutineRemoveTask(w http.ResponseWriter, r *http.Request, p principal) {
	// The task must be in this routine: the path names both.
	t, err := s.db.Task(p.household.ID, r.PathValue("task_id"))
	if err == nil && t.RoutineID != r.PathValue("id") {
		err = store.ErrNotFound
	}
	if err == nil {
		err = s.db.RemoveTaskFromRoutine(p.household.ID, t.ID)
	}
	if err != nil {
		s.routineError(w, "routine task", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) apiRoutineOrder(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		TaskIDs []string `json:"task_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	id := r.PathValue("id")
	if err := s.db.SetRoutineOrder(p.household.ID, id, in.TaskIDs); err != nil {
		s.routineError(w, "routine", err)
		return
	}
	s.writeRoutine(w, p, id, http.StatusOK)
}
