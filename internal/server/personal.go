package server

import (
	"errors"
	"net/http"

	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

// personalReturnSeconds is how long a display stays on a personal view without
// interaction before it returns to the family view (ADR 0015).
const personalReturnSeconds = 30

// pairedDisplay finds the paired display behind the request. Anything else
// gets the display address, which shows the pairing screen.
func (s *server) pairedDisplay(w http.ResponseWriter, r *http.Request) (store.Display, bool) {
	if d, _, ok := s.currentDisplay(r); ok && d.Paired {
		return d, true
	}
	http.Redirect(w, r, "/display", http.StatusSeeOther)
	return store.Display{}, false
}

// handleDisplayPerson is a person's view on a display: their events for today
// and the next 6 days and their tasks for today (ADR 0015).
func (s *server) handleDisplayPerson(w http.ResponseWriter, r *http.Request) {
	d, ok := s.pairedDisplay(w, r)
	if !ok {
		return
	}
	h, err := s.db.HomeHousehold()
	if err != nil {
		s.fail(w, "household", err)
		return
	}
	lang := s.langOf(r)
	data, err := s.displayData(h, lang, familyview.PersonalDays)
	if err != nil {
		s.fail(w, "personal view", err)
		return
	}
	var person *familyview.Person
	for i := range data.people {
		if data.people[i].ID == r.PathValue("id") {
			person = &data.people[i]
		}
	}
	if person == nil {
		http.NotFound(w, r)
		return
	}
	day := data.now.Format(task.DateLayout)
	due, err := s.db.TasksForDay(h.ID, day)
	if err != nil {
		s.fail(w, "tasks", err)
		return
	}
	in := familyview.PersonalInput{Now: data.now, Person: *person, Calendars: data.calendars, Occurrences: data.occurrences}
	for _, t := range due {
		if t.PersonID == person.ID {
			in.Tasks = append(in.Tasks, familyview.Task{ID: t.ID, Title: t.Title, Emoji: t.Emoji, Part: string(t.PartOfDay), Done: t.Done})
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, http.StatusOK, "display_person.html", map[string]any{
		"Display": d, "PollSeconds": displayPollSeconds, "View": familyview.BuildPersonal(in),
		"Theme": displayTheme(d), "ReturnSeconds": personalReturnSeconds,
	})
}

// handleDisplayTaskDone ticks a task off or open again on a display. Without
// the parent PIN a display may do this for every task (ADR 0015).
func (s *server) handleDisplayTaskDone(w http.ResponseWriter, r *http.Request) {
	if d, _, ok := s.currentDisplay(r); !ok || !d.Paired {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	h, err := s.db.HomeHousehold()
	if err != nil {
		s.fail(w, "household", err)
		return
	}
	var done bool
	switch r.PostFormValue("done") {
	case "1":
		done = true
	case "0":
	default:
		http.Error(w, "done must be 1 or 0", http.StatusUnprocessableEntity)
		return
	}
	t, err := s.db.Task(h.ID, r.PathValue("id"))
	if err == nil {
		err = s.db.SetTaskDone(h.ID, t.ID, s.today(principal{household: h}), done, "", s.now())
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
	case err != nil:
		s.fail(w, "tick task", err)
	default:
		http.Redirect(w, r, "/display/person/"+t.PersonID, http.StatusSeeOther)
	}
}
