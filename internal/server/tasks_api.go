package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/hejmo-home/hejmo/internal/badge"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

type taskJSON struct {
	ID        string   `json:"id"`
	PersonID  string   `json:"person_id"`
	Title     string   `json:"title"`
	Emoji     string   `json:"emoji"`
	Notes     string   `json:"notes"`
	Repeat    string   `json:"repeat"`
	Weekdays  []string `json:"weekdays"`
	Date      string   `json:"date"`
	PartOfDay string   `json:"part_of_day"`
	Stars     int      `json:"stars"`
	RoutineID string   `json:"routine_id"`
	Position  int      `json:"position"`
	Due       bool     `json:"due"`
	Done      bool     `json:"done"`
	// Subtasks are in their order, with their state on the requested day.
	Subtasks []subtaskJSON `json:"subtasks"`
}

type subtaskJSON struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Emoji    string `json:"emoji"`
	Position int    `json:"position"`
	Done     bool   `json:"done"`
}

// today is the current day in the household's time zone.
func (s *server) today(p principal) string {
	return s.now().In(p.location()).Format(task.DateLayout)
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// taskToJSON renders a task; done is whether it is ticked off on day.
func taskToJSON(t task.Task, day string, done bool, subs []store.DaySubtask) taskJSON {
	out := make([]subtaskJSON, 0, len(subs))
	for _, k := range subs {
		out = append(out, subtaskJSON{ID: k.ID, Title: k.Title, Emoji: k.Emoji, Position: k.Position, Done: k.Done})
	}
	return taskJSON{Subtasks: out, ID: t.ID, PersonID: t.PersonID, Title: t.Title, Emoji: t.Emoji, Notes: t.Notes, Repeat: string(t.Repeat),
		Weekdays: nonNil(t.Weekdays), Date: t.Date, PartOfDay: string(t.PartOfDay), Stars: t.Stars, RoutineID: t.RoutineID, Position: t.Position, Due: t.DueOn(day), Done: done}
}

// taskStateToJSON renders one task with its state on day.
func (s *server) taskStateToJSON(p principal, t task.Task) (taskJSON, error) {
	day := s.today(p)
	done, err := s.db.TaskDone(t.ID, day)
	if err != nil {
		return taskJSON{}, err
	}
	subs, err := s.db.Subtasks(p.household.ID, t.ID, day)
	return taskToJSON(t, day, done, subs), err
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
	person := r.URL.Query().Get("person")
	if person != "" && !uuidPattern.MatchString(person) {
		writeError(w, http.StatusUnprocessableEntity, "person must be a UUID")
		return
	}
	list, err := s.db.Tasks(p.household.ID)
	if err != nil {
		s.fail(w, "list tasks", err)
		return
	}
	done, err := s.db.TasksDoneOn(p.household.ID, day)
	if err != nil {
		s.fail(w, "task state", err)
		return
	}
	subs, err := s.db.SubtasksByTask(p.household.ID, day)
	if err != nil {
		s.fail(w, "subtasks", err)
		return
	}
	out := make([]taskJSON, 0, len(list))
	for _, t := range list {
		if person != "" && t.PersonID != person {
			continue
		}
		out = append(out, taskToJSON(t, day, done[t.ID], subs[t.ID]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiCreateTask(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		PersonID  string   `json:"person_id"`
		Title     string   `json:"title"`
		Emoji     string   `json:"emoji"`
		Notes     string   `json:"notes"`
		Repeat    string   `json:"repeat"`
		Weekdays  []string `json:"weekdays"`
		Date      string   `json:"date"`
		PartOfDay string   `json:"part_of_day"`
		Stars     *int     `json:"stars"`
		RoutineID string   `json:"routine_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	t, err := s.createTask(p, task.Task{PersonID: in.PersonID, Title: in.Title, Emoji: in.Emoji, Notes: in.Notes, Repeat: task.Repeat(in.Repeat),
		Weekdays: in.Weekdays, Date: in.Date, PartOfDay: task.PartOfDay(in.PartOfDay), RoutineID: in.RoutineID}, in.Stars)
	switch {
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case err != nil:
		s.fail(w, "create task", err)
	default:
		j, _ := s.taskStateToJSON(p, t)
		writeJSON(w, http.StatusCreated, j)
	}
}

// createTask stores a task; a repeating task without a date starts today. A
// task without a star value (nil) gets the default of its person: 1 for a
// child, 0 for an adult (ADR 0018).
func (s *server) createTask(p principal, t task.Task, stars *int) (task.Task, error) {
	if t.Date == "" && t.Repeat != task.Once {
		t.Date = s.today(p)
	}
	if stars != nil {
		t.Stars = *stars
	} else if owner, err := s.db.Person(p.household.ID, t.PersonID); err == nil {
		t.Stars = task.DefaultStars(owner.Role == store.RoleChild)
	}
	return s.db.AddTask(p.household.ID, t)
}

// apiPatchTask changes a task's star value, the only field of a task that can
// be edited so far; any other field is refused. The value counts from the next
// tick, credited ticks keep theirs.
func (s *server) apiPatchTask(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Stars *int `json:"stars"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		var wrongType *json.UnmarshalTypeError
		if errors.As(err, &wrongType) && wrongType.Field == "stars" {
			// Well-formed JSON, but not a whole number (a string, a fraction).
			writeFieldError(w, http.StatusUnprocessableEntity, "stars", "stars must be a whole number")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid JSON or an unknown field: only stars can be changed")
		return
	}
	if in.Stars == nil {
		writeFieldError(w, http.StatusUnprocessableEntity, "stars", "stars is required")
		return
	}
	n := *in.Stars
	id := r.PathValue("id")
	switch err := s.db.SetTaskStars(p.household.ID, id, n); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "task not found")
	case errors.Is(err, store.ErrInvalid):
		writeFieldError(w, http.StatusUnprocessableEntity, "stars", fmt.Sprintf("stars must be between 0 and %d", task.MaxStars))
	case err != nil:
		s.fail(w, "set task stars", err)
	default:
		t, err := s.db.Task(p.household.ID, id)
		if err != nil {
			s.fail(w, "task", err)
			return
		}
		j, err := s.taskStateToJSON(p, t)
		if err != nil {
			s.fail(w, "task state", err)
			return
		}
		writeJSON(w, http.StatusOK, j)
	}
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
// It also returns the badges the tick awarded (ticket 295).
func (s *server) tickTask(p principal, id string, done bool) (task.Task, []badge.Key, error) {
	t, err := s.db.Task(p.household.ID, id)
	if err != nil {
		return task.Task{}, nil, err
	}
	if p.person.Role == store.RoleChild && t.PersonID != p.person.ID {
		return task.Task{}, nil, errNotYourTask
	}
	won, err := s.db.TickTask(p.household.ID, id, s.today(p), done, p.person.ID, s.now())
	return t, won, err
}

var errNotYourTask = errors.New("a child may only tick off their own tasks")

func (s *server) apiSetTaskDone(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Done *bool `json:"done"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if in.Done == nil {
		writeError(w, http.StatusUnprocessableEntity, "done is required")
		return
	}
	t, _, err := s.tickTask(p, r.PathValue("id"), *in.Done)
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
		j, _ := s.taskStateToJSON(p, t)
		writeJSON(w, http.StatusOK, j)
	}
}

// subtaskReply answers a subtask route with the task and its state today, or
// with the error the store gave.
func (s *server) subtaskReply(w http.ResponseWriter, p principal, id string, status int, err error) {
	var bad *task.InvalidError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "task or subtask not found")
	case errors.As(err, &bad):
		writeFieldError(w, http.StatusUnprocessableEntity, bad.Field, err.Error())
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case err != nil:
		s.fail(w, "subtask", err)
	default:
		t, err := s.db.Task(p.household.ID, id)
		if err != nil {
			s.fail(w, "task", err)
			return
		}
		j, err := s.taskStateToJSON(p, t)
		if err != nil {
			s.fail(w, "task state", err)
			return
		}
		writeJSON(w, status, j)
	}
}

func (s *server) apiAddSubtask(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Title string `json:"title"`
		Emoji string `json:"emoji"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	id := r.PathValue("id")
	_, err := s.db.AddSubtask(p.household.ID, id, task.Subtask{Title: in.Title, Emoji: in.Emoji}, s.today(p), s.now())
	s.subtaskReply(w, p, id, http.StatusCreated, err)
}

func (s *server) apiPatchSubtask(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Title *string `json:"title"`
		Emoji *string `json:"emoji"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON or an unknown field: only title and emoji can be changed")
		return
	}
	id, sub := r.PathValue("id"), r.PathValue("sub_id")
	list, err := s.db.Subtasks(p.household.ID, id, s.today(p))
	if err != nil {
		s.subtaskReply(w, p, id, http.StatusOK, err)
		return
	}
	title, emoji, found := "", "", false
	for _, k := range list {
		if k.ID == sub {
			title, emoji, found = k.Title, k.Emoji, true
		}
	}
	if !found {
		s.subtaskReply(w, p, id, http.StatusOK, store.ErrNotFound)
		return
	}
	if in.Title != nil {
		title = *in.Title
	}
	if in.Emoji != nil {
		emoji = *in.Emoji
	}
	s.subtaskReply(w, p, id, http.StatusOK, s.db.UpdateSubtask(p.household.ID, id, sub, title, emoji))
}

func (s *server) apiMoveSubtask(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Direction string `json:"direction"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || (in.Direction != "up" && in.Direction != "down") {
		writeFieldError(w, http.StatusUnprocessableEntity, "direction", "direction must be up or down")
		return
	}
	id := r.PathValue("id")
	s.subtaskReply(w, p, id, http.StatusOK, s.db.MoveSubtask(p.household.ID, id, r.PathValue("sub_id"), in.Direction == "down"))
}

func (s *server) apiDeleteSubtask(w http.ResponseWriter, r *http.Request, p principal) {
	id := r.PathValue("id")
	_, err := s.db.DeleteSubtask(p.household.ID, id, r.PathValue("sub_id"), s.today(p), p.person.ID, s.now())
	s.subtaskReply(w, p, id, http.StatusOK, err)
}

// tickSubtask ticks one subtask off or open for today. A child may only change
// their own tasks (ADR 0015).
func (s *server) tickSubtask(p principal, taskID, subID string, done bool) (store.SubtaskChange, error) {
	t, err := s.db.Task(p.household.ID, taskID)
	if err != nil {
		return store.SubtaskChange{}, err
	}
	if p.person.Role == store.RoleChild && t.PersonID != p.person.ID {
		return store.SubtaskChange{}, errNotYourTask
	}
	return s.db.SetSubtaskDone(p.household.ID, taskID, subID, s.today(p), done, p.person.ID, s.now())
}

func (s *server) apiSetSubtaskDone(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Done *bool `json:"done"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if in.Done == nil {
		writeError(w, http.StatusUnprocessableEntity, "done is required")
		return
	}
	id := r.PathValue("id")
	_, err := s.tickSubtask(p, id, r.PathValue("sub_id"), *in.Done)
	if errors.Is(err, errNotYourTask) {
		writeError(w, http.StatusForbidden, "forbidden: "+err.Error())
		return
	}
	s.subtaskReply(w, p, id, http.StatusOK, err)
}
