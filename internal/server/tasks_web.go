package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

type taskView struct {
	ID, Title, Emoji, Repeat, Date, Part string
	Weekdays                             []string
	Due, Done                            bool
	Stars                                int
}

// starCard is a person's star account on the tasks page.
type starCard struct {
	Name, Color string
	Balance     int
	Entries     []starEntryView
}

type starEntryView struct {
	Delta, Label, By, When string
}

// starLogLines is how many star log entries per person the tasks page shows.
const starLogLines = 20

type personTasks struct {
	Name, Color string
	Tasks       []taskView
}

var taskWeekdays = []string{"MO", "TU", "WE", "TH", "FR", "SA", "SU"}

func (s *server) renderTasks(w http.ResponseWriter, r *http.Request, p principal, status int, form, starForm map[string]any) {
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
	doneToday, err := s.db.TasksDoneOn(p.household.ID, today)
	if err != nil {
		s.fail(w, "task state", err)
		return
	}
	sums, err := s.db.StarSummaries(p.household.ID)
	if err != nil {
		s.fail(w, "stars", err)
		return
	}
	var cards []starCard
	var groups []personTasks
	var choices []choice
	for _, person := range people {
		choices = append(choices, choice{person.ID, person.Name})
		if sums[person.ID].Shown {
			acc, err := s.db.StarAccount(p.household.ID, person.ID, starLogLines)
			if err != nil {
				s.fail(w, "star log", err)
				return
			}
			cards = append(cards, s.starCardOf(p, person, acc))
		}
		g := personTasks{Name: person.Name, Color: person.Color}
		for _, t := range list {
			if t.PersonID != person.ID {
				continue
			}
			v := taskView{ID: t.ID, Title: t.Title, Emoji: t.Emoji, Repeat: string(t.Repeat), Weekdays: t.Weekdays, Part: string(t.PartOfDay), Due: t.DueOn(today), Done: doneToday[t.ID], Stars: t.Stars}
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
		form = map[string]any{"Repeat": "daily", "Date": today, "Weekdays": []string{}}
	}
	if starForm == nil {
		starForm = map[string]any{}
	}
	data := map[string]any{
		"Person": p.person, "Groups": groups, "StarCards": cards, "StarForm": starForm, "StarError": starForm["Error"], "People": choices, "Weekdays": taskWeekdays, "Repeats": []string{"once", "daily", "weekly"}, "Form": form,
		"SyncStale": s.syncStale(p.household.ID), "Error": form["Error"],
	}
	s.render(w, r, status, "tasks.html", data)
}

func (s *server) handleTasksPage(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.renderTasks(w, r, p, http.StatusOK, nil, nil)
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
	var stars *int
	if v := strings.TrimSpace(r.PostFormValue("stars")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			n = -1 // refused by validation, with the form kept
		}
		stars = &n
	}
	switch _, err := s.createTask(p, in, stars); {
	case err == nil:
		http.Redirect(w, r, "/tasks", http.StatusSeeOther)
	case errors.Is(err, store.ErrInvalid):
		form := map[string]any{"Error": "tasks.error.invalid", "Person": in.PersonID, "Title": in.Title, "Emoji": in.Emoji,
			"Repeat": string(in.Repeat), "Date": in.Date, "Part": string(in.PartOfDay), "Weekdays": in.Weekdays, "Stars": r.PostFormValue("stars")}
		s.renderTasks(w, r, p, http.StatusUnprocessableEntity, form, nil)
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

// starCardOf builds a person's star account for the page: balance and log, newest first.
func (s *server) starCardOf(p principal, person store.Person, acc store.StarAccount) starCard {
	layout := "02/01/2006 15:04"
	if p.household.TimeFormat == store.Time12h {
		layout = "02/01/2006 3:04 PM"
	}
	c := starCard{Name: person.Name, Color: person.Color, Balance: acc.Balance}
	for _, e := range acc.Entries {
		label := e.Note
		if e.TaskID != "" || e.TaskTitle != "" {
			label = e.TaskTitle
		}
		delta := strconv.Itoa(e.Delta)
		if e.Delta > 0 {
			delta = "+" + delta
		}
		c.Entries = append(c.Entries, starEntryView{Delta: delta, Label: label, By: e.BookedByName, When: e.At.In(p.location()).Format(layout)})
	}
	return c
}

// handleStarBooking books stars by hand for a person: a parent's form on the tasks page.
func (s *server) handleStarBooking(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	person, note, amount := r.PostFormValue("person"), r.PostFormValue("note"), strings.TrimSpace(r.PostFormValue("delta"))
	delta, err := strconv.Atoi(amount)
	if err == nil {
		_, err = s.db.BookStars(p.household.ID, person, delta, note, p.person.ID, s.now())
	} else {
		err = store.ErrInvalid
	}
	switch {
	case err == nil:
		http.Redirect(w, r, "/tasks", http.StatusSeeOther)
	case errors.Is(err, store.ErrInvalid), errors.Is(err, store.ErrNotFound):
		s.renderTasks(w, r, p, http.StatusUnprocessableEntity, nil,
			map[string]any{"Error": "tasks.stars.error.invalid", "Person": person, "Delta": amount, "Note": note})
	default:
		s.fail(w, "book stars", err)
	}
}
