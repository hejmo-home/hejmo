package server

import (
	"errors"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/datefmt"
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
	ByRemoved              bool // the booker has been removed; By is their last name
}

// starLogLines is how many star log entries per person the tasks page shows.
const starLogLines = 20

type personTasks struct {
	Name, Color string
	Tasks       []taskView
}

var taskWeekdays = []string{"MO", "TU", "WE", "TH", "FR", "SA", "SU"}

func newTaskView(f store.DateFormat, t task.Task, today string, done bool) taskView {
	v := taskView{ID: t.ID, Title: t.Title, Emoji: t.Emoji, Repeat: string(t.Repeat), Weekdays: t.Weekdays, Part: string(t.PartOfDay), Due: t.DueOn(today), Done: done, Stars: t.Stars}
	if d, err := time.Parse(task.DateLayout, t.Date); err == nil {
		v.Date = datefmt.Date(f, d)
	}
	return v
}

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
	choices := chipsOf(people)
	child := p.person.Role == store.RoleChild
	for _, person := range people {
		// A child's phone shows the child's own tasks only (ADR 0015).
		if child && person.ID != p.person.ID {
			continue
		}
		if !child && sums[person.ID].Shown {
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
			g.Tasks = append(g.Tasks, newTaskView(p.household.DateFormat, t, today, doneToday[t.ID]))
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
		"Person": p.person, "Groups": groups, "StarCards": cards, "StarForm": starForm, "StarError": starForm["Error"], "People": choices, "Weekdays": taskWeekdays, "Repeats": []string{"once", "daily", "weekly"}, "Form": form, "Child": child, "MaxStars": task.MaxStars,
		"SyncStale": s.syncStale(p.household.ID), "Error": form["Error"], "ErrorField": form["ErrorField"],
	}
	s.render(w, r, status, "tasks.html", data)
}

func (s *server) handleTasksPage(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.memberPage(w, r); ok {
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
		s.redirectSaved(w, r, "/tasks", "flash.task_added")
	case errors.Is(err, store.ErrInvalid):
		var bad *task.InvalidError
		field := ""
		if errors.As(err, &bad) {
			field = bad.Field
		}
		form := map[string]any{"Error": taskErrorKey(field), "ErrorField": field, "Person": in.PersonID, "Title": in.Title, "Emoji": in.Emoji,
			"Repeat": string(in.Repeat), "Date": in.Date, "Part": string(in.PartOfDay), "Weekdays": in.Weekdays, "Stars": r.PostFormValue("stars")}
		s.renderTasks(w, r, p, http.StatusUnprocessableEntity, form, nil)
	default:
		s.fail(w, "add task", err)
	}
}

func (s *server) handleTaskDone(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	switch _, err := s.tickTask(p, r.PathValue("id"), r.PostFormValue("done") == "1"); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, errNotYourTask):
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
	case err != nil:
		s.fail(w, "tick task", err)
	default:
		s.redirectSaved(w, r, "/tasks", doneKey(r))
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
		s.redirectSaved(w, r, "/tasks", "flash.task_deleted")
	}
}

// doneKey is the success line for ticking a task off or reopening it.
func doneKey(r *http.Request) string {
	if r.PostFormValue("done") == "1" {
		return "flash.task_done"
	}
	return "flash.task_reopened"
}

// taskErrorKey is the text for an invalid task field. A field without its own
// text falls back to the general one.
func taskErrorKey(field string) string {
	switch field {
	case "title", "emoji", "person", "date", "weekdays", "repeat", "part", "stars":
		return "tasks.error." + field
	}
	return "tasks.error.invalid"
}

// starCardOf builds a person's star account for the page: balance and log, newest first.
func (s *server) starCardOf(p principal, person store.Person, acc store.StarAccount) starCard {
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
		c.Entries = append(c.Entries, starEntryView{Delta: delta, Label: label, By: e.BookedByName, ByRemoved: e.BookedByRemoved, When: p.dateTime(e.At)})
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

// creditedView is what today's tick of a task credited, said in words on the
// detail page. HTML comes from the locales with the person's name escaped.
type creditedView struct {
	Stars int
	HTML  template.HTML
}

// handleTaskPage is the detail page of a task: what it is, its star value and,
// behind a confirmation, deleting it (ADR 0017: destructive actions only on
// detail pages).
func (s *server) handleTaskPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.renderTaskPage(w, r, p, http.StatusOK, "", "")
}

// renderTaskPage renders the detail page of the task in the path. After a
// refused star value it shows what was typed (typed) and the error at the field.
func (s *server) renderTaskPage(w http.ResponseWriter, r *http.Request, p principal, status int, typed, errKey string) {
	t, err := s.db.Task(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "task", err)
		return
	}
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	var owner personChip
	for _, c := range chipsOf(people) {
		if c.ID == t.PersonID {
			owner = c
		}
	}
	today := s.today(p)
	doneToday, err := s.db.TasksDoneOn(p.household.ID, today)
	if err != nil {
		s.fail(w, "task state", err)
		return
	}
	data := map[string]any{
		"Person": p.person, "Task": newTaskView(p.household.DateFormat, t, today, doneToday[t.ID]), "Owner": owner, "SyncStale": s.syncStale(p.household.ID),
		"MaxStars": task.MaxStars, "StarsValue": strconv.Itoa(t.Stars),
	}
	if errKey != "" {
		data["StarsValue"], data["Error"], data["ErrorField"] = typed, errKey, "stars"
	}
	if credited, done, err := s.db.TaskDoneStars(t.ID, today); err != nil {
		s.fail(w, "credited stars", err)
		return
	} else if done {
		data["Credited"] = s.creditedNotice(s.langOf(r), owner.Name, credited)
	}
	s.render(w, r, status, "task.html", data)
}

// creditedNotice says what today's tick credited and what reopening deducts.
func (s *server) creditedNotice(lang, owner string, stars int) creditedView {
	name := html.EscapeString(owner)
	var text string
	switch stars {
	case 0:
		text = s.i18n.T(lang, "tasks.stars.credited.zero")
	case 1:
		text = fmt.Sprintf(s.i18n.T(lang, "tasks.stars.credited.one"), name)
	default:
		text = fmt.Sprintf(s.i18n.T(lang, "tasks.stars.credited.other"), name, stars)
	}
	return creditedView{Stars: stars, HTML: template.HTML(text)}
}

// starsSavedKey is the text of the success line after a changed star value:
// no stars, one, or several.
func starsSavedKey(n int) string {
	switch n {
	case 0:
		return "tasks.stars.saved.zero"
	case 1:
		return "tasks.stars.saved.one"
	}
	return "tasks.stars.saved.other"
}

// handleTaskStars changes a task's star value from its detail page. The value
// counts from the next tick; credited ticks keep theirs.
func (s *server) handleTaskStars(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	typed := strings.TrimSpace(r.PostFormValue("stars"))
	n, convErr := strconv.Atoi(typed)
	id := r.PathValue("id")
	err := convErr
	if err == nil {
		err = s.db.SetTaskStars(p.household.ID, id, n)
	} else {
		err = store.ErrInvalid // not a whole number: refused with the field kept
	}
	switch {
	case err == nil:
		s.redirectSaved(w, r, "/tasks/"+id, starsSavedKey(n)+":"+strconv.Itoa(n))
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrInvalid):
		s.renderTaskPage(w, r, p, http.StatusUnprocessableEntity, typed, "tasks.error.stars")
	default:
		s.fail(w, "set task stars", err)
	}
}
