package server

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"

	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/store"
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
func (s *server) personalView(h store.Household, lang, personID string) (familyview.PersonalView, error) {
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
	in := familyview.PersonalInput{Now: data.now, Person: *person, Calendars: data.calendars, Occurrences: data.occurrences, TimeFormat: h.TimeFormat}
	for _, t := range due {
		if t.PersonID == person.ID {
			in.Tasks = append(in.Tasks, familyview.Task{ID: t.ID, Title: t.Title, Emoji: t.Emoji, Part: string(t.PartOfDay), Done: t.Done, Stars: t.Stars})
		}
	}
	sums, err := s.db.StarSummaries(h.ID)
	if err != nil {
		return familyview.PersonalView{}, err
	}
	in.Stars = familyview.Stars{Shown: sums[person.ID].Shown, Balance: sums[person.ID].Balance}
	return familyview.BuildPersonal(in), nil
}

// renderPersonal renders a person's view markup and its version, like
// renderFamily: the version leaves the clock out, the display ticks it.
func (s *server) renderPersonal(h store.Household, lang, personID string) (template.HTML, string, error) {
	view, err := s.personalView(h, lang, personID)
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
	view, version, err := s.renderPersonal(h, s.langOf(r), id)
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
		"Theme": displayTheme(d), "ReturnSeconds": personalReturnSeconds,
		"ViewURL": "/display/person/" + url.PathEscape(id) + "/view", "EventsURL": "/display/events?person=" + url.QueryEscape(id),
	})
}

// handleDisplayPersonView serves a person's view markup alone, for the
// display's script to swap in (partial update). 404 when the person is gone:
// the script then returns to the family view.
func (s *server) handleDisplayPersonView(w http.ResponseWriter, r *http.Request) {
	_, h, ok := s.pairedDisplay(w, r)
	if !ok {
		return
	}
	markup, version, err := s.renderPersonal(h, s.langOf(r), r.PathValue("id"))
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
