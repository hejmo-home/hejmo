package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

type taskView struct {
	ID, Title, Emoji, Repeat, Date, Part string
	Weekdays                             []string
	Due, Done                            bool
}

type personTasks struct {
	Name, Color string
	Tasks       []taskView
}

var taskWeekdays = []string{"MO", "TU", "WE", "TH", "FR", "SA", "SU"}

func (s *server) renderTasks(w http.ResponseWriter, r *http.Request, p principal, status int, form map[string]any) {
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	list, err := s.db.Tasks(p.household.ID)
	if err != nil {
		s.fail(w, "tasks", err)
		return
	}
	today := s.today(p)
	var groups []personTasks
	var choices []choice
	for _, person := range people {
		choices = append(choices, choice{person.ID, person.Name})
		g := personTasks{Name: person.Name, Color: person.Color}
		for _, t := range list {
			if t.PersonID != person.ID {
				continue
			}
			done, err := s.db.TaskDone(t.ID, today)
			if err != nil {
				s.fail(w, "task state", err)
				return
			}
			v := taskView{ID: t.ID, Title: t.Title, Emoji: t.Emoji, Repeat: string(t.Repeat), Weekdays: t.Weekdays, Part: string(t.PartOfDay), Due: t.DueOn(today), Done: done}
			if d, err := time.Parse(task.DateLayout, t.Date); err == nil {
				v.Date = d.Format("02/01/2006")
			}
			g.Tasks = append(g.Tasks, v)
		}
		if len(g.Tasks) > 0 {
			groups = append(groups, g)
		}
	}
	if form == nil {
		form = map[string]any{"Repeat": "daily", "Date": today}
	}
	data := map[string]any{
		"Person": p.person, "Groups": groups, "People": choices, "Weekdays": taskWeekdays, "Repeats": []string{"once", "daily", "weekly"}, "Form": form,
		"SyncStale": s.syncStale(p.household.ID), "Error": form["Error"],
	}
	s.render(w, r, status, "tasks.html", data)
}

func (s *server) handleTasksPage(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.renderTasks(w, r, p, http.StatusOK, nil)
	}
}

func (s *server) handleTaskAdd(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	in := task.Task{PersonID: r.PostFormValue("person"), Title: r.PostFormValue("title"), Emoji: r.PostFormValue("emoji"),
		Repeat: task.Repeat(r.PostFormValue("repeat")), Date: r.PostFormValue("date"), PartOfDay: task.PartOfDay(r.PostFormValue("part"))}
	if in.Repeat == task.Weekly {
		in.Weekdays = r.PostForm["weekdays"]
	}
	switch _, err := s.createTask(p, in); {
	case err == nil:
		http.Redirect(w, r, "/tasks", http.StatusSeeOther)
	case errors.Is(err, store.ErrInvalid):
		form := map[string]any{"Error": "tasks.error.invalid", "Person": in.PersonID, "Title": in.Title, "Emoji": in.Emoji,
			"Repeat": string(in.Repeat), "Date": in.Date, "Part": string(in.PartOfDay), "Weekdays": in.Weekdays}
		s.renderTasks(w, r, p, http.StatusUnprocessableEntity, form)
	default:
		s.fail(w, "add task", err)
	}
}

func (s *server) handleTaskDone(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	switch _, err := s.tickTask(p, r.PathValue("id"), r.PostFormValue("done") == "1"); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
	case err != nil:
		s.fail(w, "tick task", err)
	default:
		http.Redirect(w, r, "/tasks", http.StatusSeeOther)
	}
}

func (s *server) handleTaskDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	switch err := s.db.DeleteTask(p.household.ID, r.PathValue("id")); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, "delete task", err)
	default:
		http.Redirect(w, r, "/tasks", http.StatusSeeOther)
	}
}
