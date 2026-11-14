package server

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/store"
)

// parentPage resolves the signed-in parent for a web page, or answers with the
// redirect or refusal itself.
func (s *server) parentPage(w http.ResponseWriter, r *http.Request) (principal, bool) {
	if !s.db.Configured() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return principal{}, false
	}
	person, ok := s.currentPerson(r)
	if !ok {
		to := "/login"
		if r.Method == http.MethodGet {
			to += "?" + url.Values{"next": {r.URL.RequestURI()}}.Encode()
		}
		http.Redirect(w, r, to, http.StatusSeeOther)
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

// calendarGroup is one heading of the subscribed calendars: a person, or the
// whole household. Color is empty for the household.
type calendarGroup struct {
	Name, Color string
	Calendars   []calendarView
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
		return t.In(loc).Format(p.timeLayout())
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
	var groupChoices []choice
	for _, x := range people {
		names[x.ID] = x.Name
	}
	peopleChoices := chipsOf(people)
	for _, x := range groups {
		names[x.ID] = x.Name
		groupChoices = append(groupChoices, choice{x.ID, x.Name})
	}
	synced, err := s.db.SyncedCalendars(p.household.ID)
	if err != nil {
		s.fail(w, "synced calendars", err)
		return
	}
	// Grouped by person in household order; a calendar shows under every
	// person of its audience (groups expand to their members). Calendars with
	// no audience go first under "Whole household". Calendars nobody sees go last under "Not assigned". Empty groups are dropped.
	members := map[string][]string{}
	for _, x := range groups {
		members[x.ID] = x.Members
	}
	whole := calendarGroup{Name: s.i18n.T(s.langOf(r), "calendars.whole_household")}
	unassigned := calendarGroup{Name: s.i18n.T(s.langOf(r), "calendars.unassigned")}
	byPerson := map[string][]calendarView{}
	for _, c := range synced {
		v := calendarView{ID: c.ID, Name: c.Name, Color: c.Color, LastSuccess: at(c.Sync.LastSuccess), LastError: s.say(r, c.Sync.LastError),
			Stale: calsync.StateAt(s.now(), c.Sync.StartedAt, c.Sync.LastSuccess) == calsync.Stale, IntervalMinutes: c.Sync.IntervalMinutes}
		for _, id := range append(append([]string{}, c.Audience.People...), c.Audience.Groups...) {
			if v.Audience != "" {
				v.Audience += ", "
			}
			v.Audience += names[id]
		}
		if len(c.Audience.People) == 0 && len(c.Audience.Groups) == 0 {
			whole.Calendars = append(whole.Calendars, v)
			continue
		}
		audience := map[string]bool{}
		for _, id := range c.Audience.People {
			audience[id] = true
		}
		for _, id := range c.Audience.Groups {
			for _, m := range members[id] {
				audience[m] = true
			}
		}
		placed := false
		for _, x := range people {
			if audience[x.ID] {
				byPerson[x.ID] = append(byPerson[x.ID], v)
				placed = true
			}
		}
		if !placed {
			unassigned.Calendars = append(unassigned.Calendars, v)
		}
	}
	var cals []calendarGroup
	if len(whole.Calendars) > 0 {
		cals = append(cals, whole)
	}
	for _, x := range people {
		if len(byPerson[x.ID]) > 0 {
			cals = append(cals, calendarGroup{Name: x.Name, Color: x.Color, Calendars: byPerson[x.ID]})
		}
	}
	if len(unassigned.Calendars) > 0 {
		cals = append(cals, unassigned)
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
		"Intervals": syncIntervals, "IntervalChosen": intervalChosen(form), "Connections": connViews, "SyncStale": s.syncStale(p.household.ID), "HTTPSWarning": s.httpsWarning(), "Form": form,
		"ConnError": "", "ConnErrorField": "",
	}
	if form != nil {
		data["Error"], data["ErrorField"] = form["Error"], form["ErrorField"]
		data["ConnError"], data["ConnErrorField"] = form["ConnError"], form["ConnErrorField"]
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
		form := map[string]any{"Error": "calendars.error." + bad.Field, "ErrorField": calendarErrorField(bad.Field), "Name": in.Name, "URL": in.URL, "Color": in.Color,
			"Interval": interval, "People": in.Audience.People, "Groups": in.Audience.Groups}
		s.renderCalendars(w, r, p, http.StatusUnprocessableEntity, form)
		return
	case err != nil:
		s.fail(w, "add calendar", err)
		return
	}
	s.redirectSaved(w, r, "/calendars", "flash.calendar_added")
}

func (s *server) handleCalendarSync(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	// A failed sync is in the log the page shows next, not a success line.
	err := s.sync.Sync(r.Context(), p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err == nil {
		s.redirectSaved(w, r, "/calendars", "flash.calendar_synced")
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
	s.redirectSaved(w, r, "/calendars", "flash.calendar_removed")
}

// calendarErrorField names the input of the add form that an error belongs to;
// an error without an input (the kind) shows above the form instead.
func calendarErrorField(field string) string {
	switch field {
	case "name", "url", "color", "interval":
		return field
	case "audience":
		return "people"
	}
	return ""
}

// intervalChosen is the update interval the add form shows selected: the one
// just typed after an error, else the default of an hour.
func intervalChosen(form map[string]any) int {
	if n, ok := form["Interval"].(int); ok && n > 0 {
		return n
	}
	return 60
}
