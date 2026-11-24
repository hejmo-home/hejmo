package server

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"

	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/streak"
	"github.com/hejmo-home/hejmo/internal/task"
)

// personalReturnSeconds is how long a display stays on a personal view without
// interaction before it returns to the family view (ADR 0015).
const personalReturnSeconds = 30

// errNoPerson means the person of a personal view is gone (deleted meanwhile).
var errNoPerson = errors.New("no such person")

// pairedDisplayOrRedirect finds the paired display behind the request.
// Anything else gets the display address, which shows the pairing screen.
func (s *server) pairedDisplayOrRedirect(w http.ResponseWriter, r *http.Request) (store.Display, bool) {
	if d, _, ok := s.currentDisplay(r); ok && d.Paired {
		return d, true
	}
	http.Redirect(w, r, "/display", http.StatusSeeOther)
	return store.Display{}, false
}

// personalView builds a person's view: their events for today and the next 6
// days and their tasks for today (ADR 0015). errNoPerson if they are gone.
func (s *server) personalView(h store.Household, d *store.Display, lang, personID string, home bool) (familyview.PersonalView, error) {
	data, err := s.displayData(h, lang, familyview.PersonalDays)
	if err != nil {
		return familyview.PersonalView{}, err
	}
	var person *familyview.Person
	for i := range data.people {
		if data.people[i].ID == personID {
			person = &data.people[i]
		}
	}
	if person == nil {
		return familyview.PersonalView{}, errNoPerson
	}
	day := data.now.Format(task.DateLayout)
	due, err := s.db.TasksForDay(h.ID, day)
	if err != nil {
		return familyview.PersonalView{}, err
	}
	in := familyview.PersonalInput{Now: data.now, Person: *person, People: data.people, Calendars: data.calendars, Occurrences: data.occurrences, TimeFormat: h.TimeFormat, Home: home}
	if d != nil { // the header blocks of the display apply to its personal views (Q130)
		lay, err := s.db.DisplayLayout(h.ID, d.ID)
		if err != nil {
			return familyview.PersonalView{}, err
		}
		in.Layout, in.Weather, in.TemperatureUnit = &lay, s.homeWeather(h), h.TemperatureUnit
	}
	for _, t := range due {
		if t.PersonID == person.ID {
			in.Tasks = append(in.Tasks, viewTask(t))
		}
	}
	routines, err := s.db.Routines(h.ID)
	if err != nil {
		return familyview.PersonalView{}, err
	}
	weeks, err := s.routineWeeks(h, day)
	if err != nil {
		return familyview.PersonalView{}, err
	}
	in.Routines = viewRoutines(routines, person.ID, weeks)
	sums, err := s.db.StarSummaries(h.ID)
	if err != nil {
		return familyview.PersonalView{}, err
	}
	in.Stars = familyview.Stars{Shown: sums[person.ID].Shown, Balance: sums[person.ID].Balance}
	goal, err := s.db.Goal(h.ID, person.ID)
	if err != nil {
		return familyview.PersonalView{}, err
	}
	in.Goal = displayGoal(goal)
	if in.Goal != nil {
		people, err := s.db.People(h.ID)
		if err != nil {
			return familyview.PersonalView{}, err
		}
		var parents []string
		for _, x := range people {
			if x.Role == store.RoleParent {
				parents = append(parents, x.Name)
			}
		}
		in.Parents = s.joinNames(lang, "rwd.or", parents)
	}
	return familyview.BuildPersonal(in), nil
}

// viewTask is a task due today as the personal view and a child's tasks page show it.
func viewTask(t store.DayTask) familyview.Task {
	k := familyview.Task{ID: t.ID, Title: t.Title, Emoji: t.Emoji, Part: string(t.PartOfDay), Done: t.Done, Stars: t.Stars, RoutineID: t.RoutineID, Position: t.Position}
	for _, s := range t.Subtasks {
		k.Subtasks = append(k.Subtasks, familyview.Subtask{ID: s.ID, Title: s.Title, Emoji: s.Emoji, Done: s.Done})
	}
	return k
}

// viewRoutines are the routines of one person.
func viewRoutines(all []task.Routine, personID string, weeks map[string]*streak.Week) []familyview.Routine {
	var out []familyview.Routine
	for _, r := range all {
		if r.PersonID == personID {
			out = append(out, familyview.Routine{ID: r.ID, Name: r.Name, Part: string(r.Part), Week: weeks[r.ID]})
		}
	}
	return out
}

// renderPersonal renders a person's view markup and its version, like
// renderFamily: the version leaves the clock out, the display ticks it.
func (s *server) renderPersonal(h store.Household, d *store.Display, lang, personID string, home bool) (template.HTML, string, error) {
	view, err := s.personalView(h, d, lang, personID, home)
	if err != nil {
		return "", "", err
	}
	clock := view.Clock
	view.Clock = clockMark
	return s.renderVersioned("personalView", lang, view, clock)
}

// handleDisplayPerson is a person's view on a display. It stays current like
// the family view: the box announces its version over /display/events?person=,
// and the script swaps in the markup of GET /display/person/{id}/view.
func (s *server) handleDisplayPerson(w http.ResponseWriter, r *http.Request) {
	d, ok := s.pairedDisplayOrRedirect(w, r)
	if !ok {
		return
	}
	h, err := s.db.HomeHousehold()
	if err != nil {
		s.fail(w, "household", err)
		return
	}
	id := r.PathValue("id")
	view, version, err := s.renderPersonal(h, &d, s.displayLang(r), id, false)
	if errors.Is(err, errNoPerson) {
		// Gone meanwhile: back to the display, never to a bare error page
		// (it has no display.js and would leave the kiosk stuck).
		http.Redirect(w, r, "/display", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.fail(w, "personal view", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, http.StatusOK, "display_person.html", map[string]any{
		"Display": d, "PollSeconds": displayPollSeconds, "View": view, "Version": version,
		"Theme": s.displayTheme(h, d), "ReturnSeconds": personalReturnSeconds,
		"ViewURL": "/display/person/" + url.PathEscape(id) + "/view", "EventsURL": "/display/events?person=" + url.QueryEscape(id),
	})
}

// handleDisplayPersonView serves a person's view markup alone, for the
// display's script to swap in (partial update). 404 when the person is gone:
// the script then returns to the family view.
func (s *server) handleDisplayPersonView(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.pairedDisplay(w, r)
	if !ok {
		return
	}
	markup, version, err := s.renderPersonal(h, &d, s.displayLang(r), r.PathValue("id"), false)
	if errors.Is(err, errNoPerson) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "personal view", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Hejmo-View", version)
	_, _ = w.Write([]byte(markup))
}

// handleDisplayTaskDone ticks a task off or open again on a display. Without
// the parent PIN a display may do this for every task (ADR 0015).
func (s *server) handleDisplayTaskDone(w http.ResponseWriter, r *http.Request) {
	if d, _, ok := s.currentDisplay(r); !ok || !d.Paired {
		// Revoked or never paired: the display address shows the pairing screen.
		http.Redirect(w, r, "/display", http.StatusSeeOther)
		return
	}
	h, err := s.db.HomeHousehold()
	if err != nil {
		s.fail(w, "household", err)
		return
	}
	t, err := s.db.Task(h.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		// The task is gone: back to the display, never to a bare error page
		// (it has no display.js and would leave the kiosk stuck).
		http.Redirect(w, r, "/display", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.fail(w, "task", err)
		return
	}
	back := "/display/person/" + t.PersonID
	var done bool
	switch r.PostFormValue("done") {
	case "1":
		done = true
	case "0":
	default:
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	// Like the REST API, a display ticks only today. A page left over from an
	// earlier day ticks nothing; the redirect shows today's state.
	today := s.today(principal{household: h})
	if day := r.PostFormValue("day"); day != "" && day != today {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	day := today
	switch err = s.db.SetTaskDone(h.ID, t.ID, day, done, "", s.now()); {
	case err == nil, errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrInvalid):
		// Not due (any more) or deleted meanwhile: show the current state.
		http.Redirect(w, r, back, http.StatusSeeOther)
	default:
		s.fail(w, "tick task", err)
	}
}

// handleDisplaySubtaskDone ticks one subtask off or open again on a display.
// Like a task, it needs no PIN and ticks only today; the last open subtask
// completes the task (ADR 0018).
func (s *server) handleDisplaySubtaskDone(w http.ResponseWriter, r *http.Request) {
	if d, _, ok := s.currentDisplay(r); !ok || !d.Paired {
		http.Redirect(w, r, "/display", http.StatusSeeOther)
		return
	}
	h, err := s.db.HomeHousehold()
	if err != nil {
		s.fail(w, "household", err)
		return
	}
	t, err := s.db.Task(h.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.Redirect(w, r, "/display", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.fail(w, "task", err)
		return
	}
	back := "/display/person/" + t.PersonID
	done := r.PostFormValue("done") == "1"
	if v := r.PostFormValue("done"); v != "1" && v != "0" {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	today := s.today(principal{household: h})
	if day := r.PostFormValue("day"); day != "" && day != today {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	switch _, err = s.db.SetSubtaskDone(h.ID, t.ID, r.PathValue("sub_id"), today, done, "", s.now()); {
	case err == nil, errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrInvalid):
		http.Redirect(w, r, back, http.StatusSeeOther)
	default:
		s.fail(w, "tick subtask", err)
	}
}
