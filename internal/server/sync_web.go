package server

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/store"
)

const webTimeLayout = "02/01/2006 15:04"

// parentPage resolves the signed-in parent for a web page, or answers with the
// redirect or refusal itself.
func (s *server) parentPage(w http.ResponseWriter, r *http.Request) (principal, bool) {
	if !s.db.Configured() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return principal{}, false
	}
	person, ok := s.currentPerson(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return principal{}, false
	}
	if person.Role != store.RoleParent {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return principal{}, false
	}
	h, err := s.db.HomeHousehold()
	if err != nil {
		s.fail(w, "household", err)
		return principal{}, false
	}
	return principal{household: h, person: person, scopes: "write"}, true
}

// syncStale reports whether any synced calendar has gone 24 hours without
// a successful sync.
func (s *server) syncStale(householdID string) bool {
	cals, err := s.db.SyncedCalendars(householdID)
	if err != nil {
		s.logger.Error("sync state", "err", err)
		return false
	}
	for _, c := range cals {
		if calsync.StateAt(s.now(), c.Sync.StartedAt, c.Sync.LastSuccess) == calsync.Stale {
			return true
		}
	}
	return false
}

type calendarView struct {
	ID, Name, Color, Audience, LastSuccess, LastError string
	Stale                                             bool
	IntervalMinutes                                   int
}

type logView struct{ At, Calendar, Kind, Message string }

type choice struct{ ID, Name string }

var syncIntervals = []int{5, 15, 30, 60, 120, 360, 1440}

func (s *server) renderCalendars(w http.ResponseWriter, r *http.Request, p principal, status int, form map[string]any) {
	loc := p.location()
	at := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.In(loc).Format(webTimeLayout)
	}
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	groups, err := s.db.Groups(p.household.ID)
	if err != nil {
		s.fail(w, "groups", err)
		return
	}
	names := map[string]string{}
	var peopleChoices, groupChoices []choice
	for _, x := range people {
		names[x.ID] = x.Name
		peopleChoices = append(peopleChoices, choice{x.ID, x.Name})
	}
	for _, x := range groups {
		names[x.ID] = x.Name
		groupChoices = append(groupChoices, choice{x.ID, x.Name})
	}
	synced, err := s.db.SyncedCalendars(p.household.ID)
	if err != nil {
		s.fail(w, "synced calendars", err)
		return
	}
	var cals []calendarView
	for _, c := range synced {
		v := calendarView{ID: c.ID, Name: c.Name, Color: c.Color, LastSuccess: at(c.Sync.LastSuccess), LastError: s.say(r, c.Sync.LastError),
			Stale: calsync.StateAt(s.now(), c.Sync.StartedAt, c.Sync.LastSuccess) == calsync.Stale, IntervalMinutes: c.Sync.IntervalMinutes}
		for _, id := range append(append([]string{}, c.Audience.People...), c.Audience.Groups...) {
			if v.Audience != "" {
				v.Audience += ", "
			}
			v.Audience += names[id]
		}
		cals = append(cals, v)
	}
	conns, err := s.db.Connections(p.household.ID)
	if err != nil {
		s.fail(w, "connections", err)
		return
	}
	var connViews []connectionView
	for _, c := range conns {
		connViews = append(connViews, connectionView{ID: c.ID, URL: c.URL, Username: c.Username})
	}
	entries, err := s.db.SyncLog(p.household.ID, 50)
	if err != nil {
		s.fail(w, "sync log", err)
		return
	}
	var log []logView
	for _, e := range entries {
		log = append(log, logView{at(e.At), e.CalendarName, e.Kind, s.say(r, e.Message)})
	}
	data := map[string]any{
		"Person": p.person, "Calendars": cals, "Log": log, "People": peopleChoices, "Groups": groupChoices,
		"Intervals": syncIntervals, "Connections": connViews, "SyncStale": s.syncStale(p.household.ID), "Form": form,
	}
	if form != nil {
		data["Error"] = form["Error"]
	}
	s.render(w, r, status, "calendars.html", data)
}

func (s *server) handleCalendarsPage(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.renderCalendars(w, r, p, http.StatusOK, nil)
	}
}

func (s *server) handleCalendarAdd(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	interval, convErr := strconv.Atoi(r.PostFormValue("interval"))
	in := newCalendarInput{
		Kind: "ics", Name: r.PostFormValue("name"), URL: r.PostFormValue("url"), Color: r.PostFormValue("color"),
		Audience: &audienceJSON{People: nonNil(r.PostForm["people"]), Groups: nonNil(r.PostForm["groups"])},
	}
	if convErr == nil {
		in.IntervalMinutes = &interval
	}
	_, err := s.addICS(r, p, in)
	var bad *inputError
	switch {
	case errors.As(err, &bad):
		form := map[string]any{"Error": "calendars.error." + bad.Field, "Name": in.Name, "URL": in.URL, "Color": in.Color}
		s.renderCalendars(w, r, p, http.StatusUnprocessableEntity, form)
		return
	case err != nil:
		s.fail(w, "add calendar", err)
		return
	}
	http.Redirect(w, r, "/calendars", http.StatusSeeOther)
}

func (s *server) handleCalendarSync(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	// A failed sync is in the log the page shows next.
	if err := s.sync.Sync(r.Context(), p.household.ID, r.PathValue("id")); errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/calendars", http.StatusSeeOther)
}

func (s *server) handleCalendarDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if err := s.db.DeleteSyncedCalendar(p.household.ID, r.PathValue("id")); errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "delete calendar", err)
		return
	}
	http.Redirect(w, r, "/calendars", http.StatusSeeOther)
}
