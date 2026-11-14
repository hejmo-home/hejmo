package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

type taskJSON struct {
	ID        string   `json:"id"`
	PersonID  string   `json:"person_id"`
	Title     string   `json:"title"`
	Emoji     string   `json:"emoji"`
	Repeat    string   `json:"repeat"`
	Weekdays  []string `json:"weekdays"`
	Date      string   `json:"date"`
	PartOfDay string   `json:"part_of_day"`
	Due       bool     `json:"due"`
	Done      bool     `json:"done"`
}

// today is the current day in the household's time zone.
func (s *server) today(p principal) string {
	return s.now().In(p.location()).Format(task.DateLayout)
}

func (s *server) taskToJSON(t task.Task, day string) (taskJSON, error) {
	j := taskJSON{ID: t.ID, PersonID: t.PersonID, Title: t.Title, Emoji: t.Emoji, Repeat: string(t.Repeat),
		Weekdays: nonNil(t.Weekdays), Date: t.Date, PartOfDay: string(t.PartOfDay), Due: t.DueOn(day)}
	done, err := s.db.TaskDone(t.ID, day)
	j.Done = done
	return j, err
}

func (s *server) apiListTasks(w http.ResponseWriter, r *http.Request, p principal) {
	day := s.today(p)
	if d := r.URL.Query().Get("date"); d != "" {
		if _, err := time.Parse(task.DateLayout, d); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "date must be YYYY-MM-DD")
			return
		}
		day = d
	}
	list, err := s.db.Tasks(p.household.ID)
	if err != nil {
		s.fail(w, "list tasks", err)
		return
	}
	person := r.URL.Query().Get("person")
	out := make([]taskJSON, 0, len(list))
	for _, t := range list {
		if person != "" && t.PersonID != person {
			continue
		}
		j, err := s.taskToJSON(t, day)
		if err != nil {
			s.fail(w, "task state", err)
			return
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiCreateTask(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		PersonID  string   `json:"person_id"`
		Title     string   `json:"title"`
		Emoji     string   `json:"emoji"`
		Repeat    string   `json:"repeat"`
		Weekdays  []string `json:"weekdays"`
		Date      string   `json:"date"`
		PartOfDay string   `json:"part_of_day"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	t, err := s.createTask(p, task.Task{PersonID: in.PersonID, Title: in.Title, Emoji: in.Emoji, Repeat: task.Repeat(in.Repeat),
		Weekdays: in.Weekdays, Date: in.Date, PartOfDay: task.PartOfDay(in.PartOfDay)})
	switch {
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case err != nil:
		s.fail(w, "create task", err)
	default:
		j, _ := s.taskToJSON(t, s.today(p))
		writeJSON(w, http.StatusCreated, j)
	}
}

// createTask stores a task; a repeating task without a date starts today.
func (s *server) createTask(p principal, t task.Task) (task.Task, error) {
	if t.Date == "" && t.Repeat != task.Once {
		t.Date = s.today(p)
	}
	return s.db.AddTask(p.household.ID, t)
}

func (s *server) apiDeleteTask(w http.ResponseWriter, r *http.Request, p principal) {
	switch err := s.db.DeleteTask(p.household.ID, r.PathValue("id")); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "task not found")
	case err != nil:
		s.fail(w, "delete task", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// tickTask ticks a task off or open for today. A child may only change their
// own tasks (ADR 0015).
func (s *server) tickTask(p principal, id string, done bool) (task.Task, error) {
	t, err := s.db.Task(p.household.ID, id)
	if err != nil {
		return task.Task{}, err
	}
	if p.person.Role == store.RoleChild && t.PersonID != p.person.ID {
		return task.Task{}, errNotYourTask
	}
	return t, s.db.SetTaskDone(p.household.ID, id, s.today(p), done, p.person.ID, s.now())
}

var errNotYourTask = errors.New("a child may only tick off their own tasks")

func (s *server) apiSetTaskDone(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Done *bool `json:"done"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Done == nil {
		writeError(w, http.StatusUnprocessableEntity, "done is required")
		return
	}
	t, err := s.tickTask(p, r.PathValue("id"), *in.Done)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "task not found")
	case errors.Is(err, errNotYourTask):
		writeError(w, http.StatusForbidden, "forbidden: "+err.Error())
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case err != nil:
		s.fail(w, "tick task", err)
	default:
		j, _ := s.taskToJSON(t, s.today(p))
		writeJSON(w, http.StatusOK, j)
	}
}
