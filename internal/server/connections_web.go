package server

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/hejmo-home/hejmo/internal/store"
)

type connectionView struct {
	ID, URL, Username string
}

// connectionErrorKey is the i18n key explaining an input error.
func connectionErrorKey(e *inputError) string {
	switch e.Field {
	case "name", "color", "interval", "audience":
		return "calendars.error." + e.Field
	case "url":
		return "connections.error.url"
	default: // provider, username, password, login, server
		return "connections.error." + e.Field
	}
}

// connectionErrorField names the input of the connection form an error belongs
// to. Address problems of iCloud show at the service, since iCloud hides the
// address field.
func connectionErrorField(e *inputError, provider string) string {
	switch e.Field {
	case "provider", "username", "password", "url":
		return e.Field
	case "login":
		return "password"
	}
	if provider == "icloud" {
		return "provider"
	}
	return "url" // server, foreign_host, downgrade
}

// selectionErrorField names what an error of the calendar choice belongs to:
// the input concerned, or the calendar as a whole.
func selectionErrorField(e *inputError) string {
	switch e.Field {
	case "interval", "name", "color", "audience":
		return e.Field
	}
	return "calendar"
}

// selectionCalendar is one calendar on the choice page with what the parent
// entered for it, or the defaults.
type selectionCalendar struct {
	URL, Name, Color string
	Added, Ticked    bool
	People, Groups   []string
}

// selectionDefaultColor is the colour offered for a calendar not yet chosen.
const selectionDefaultColor = "#2F5E7A"

// selectionCalendars shows the discovered calendars with the values of a
// submitted form (nil for the defaults), so a page shown again after an error
// keeps what was entered.
func selectionCalendars(found []discoveredJSON, form url.Values) []selectionCalendar {
	out := make([]selectionCalendar, 0, len(found))
	for _, f := range found {
		c := selectionCalendar{URL: f.URL, Name: f.Name, Color: selectionDefaultColor, Added: f.Added}
		if form != nil && !f.Added {
			c.Ticked = slices.Contains(form["calendar"], f.URL)
			if form.Has("name_" + f.URL) {
				c.Name = form.Get("name_" + f.URL)
			}
			if v := form.Get("color_" + f.URL); v != "" {
				c.Color = v
			}
			c.People, c.Groups = form["people_"+f.URL], form["groups_"+f.URL]
		}
		out = append(out, c)
	}
	return out
}

// renderSelection shows the calendars found on a connection so parents can
// tick the ones to sync. Colour and audience are chosen per calendar. A
// submitted form, if any, fills the page again.
func (s *server) renderSelection(w http.ResponseWriter, r *http.Request, p principal, status int, conn store.Connection, found []discoveredJSON, form url.Values, errKey, errField, errCalendar string) {
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
	peopleChoices := chipsOf(people)
	var groupChoices []choice
	for _, x := range groups {
		groupChoices = append(groupChoices, choice{x.ID, x.Name})
	}
	interval := defaultCalDAVSync
	if n, err := strconv.Atoi(form.Get("interval")); err == nil {
		interval = n
	}
	s.render(w, r, status, "connection.html", map[string]any{
		"Person": p.person, "Connection": connectionView{ID: conn.ID, URL: conn.URL, Username: conn.Username},
		"Found": selectionCalendars(found, form), "People": peopleChoices, "Groups": groupChoices, "Intervals": syncIntervals,
		"DefaultInterval": interval, "SyncStale": s.syncStale(p.household.ID),
		"Error": errKey, "ErrorField": errField, "ErrorCalendar": errCalendar,
	})
}

func (s *server) handleConnect(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	in := newConnectionInput{
		Provider: r.PostFormValue("provider"), URL: r.PostFormValue("url"), Username: r.PostFormValue("username"),
		Password: r.PostFormValue("password"), Single: r.PostFormValue("single_calendar") != "",
	}
	conn, found, err := s.connect(r, p, in)
	var bad *inputError
	switch {
	case errors.As(err, &bad):
		// The password is never sent back to the page.
		if in.Provider == "icloud" {
			in.URL, in.Single = "", false // iCloud ignores both; do not bring them back when the form is shown again
		}
		form := map[string]any{"ConnError": connectionErrorKey(bad), "ConnErrorField": connectionErrorField(bad, in.Provider), "ConnProvider": in.Provider, "ConnURL": in.URL, "ConnUsername": in.Username, "ConnSingle": in.Single}
		s.renderCalendars(w, r, p, http.StatusUnprocessableEntity, form)
		return
	case err != nil:
		s.fail(w, "connect", err)
		return
	}
	marked, err := s.markAdded(p, conn, found)
	if err != nil {
		s.fail(w, "connect", err)
		return
	}
	s.renderSelection(w, r, p, http.StatusOK, conn, marked, nil, "", "", "")
}

func (s *server) handleConnectionPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	conn, err := s.db.Connection(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "connection", err)
		return
	}
	found, err := s.rediscover(r.Context(), p, conn)
	var bad *inputError
	switch {
	case errors.As(err, &bad):
		s.renderSelection(w, r, p, http.StatusBadGateway, conn, nil, nil, connectionErrorKey(bad), "", "")
		return
	case err != nil:
		s.fail(w, "discover", err)
		return
	}
	s.renderSelection(w, r, p, http.StatusOK, conn, found, nil, "", "", "")
}

func (s *server) handleConnectionCalendars(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	conn, err := s.db.Connection(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "connection", err)
		return
	}
	_ = r.ParseForm()
	interval, convErr := strconv.Atoi(r.PostFormValue("interval"))
	var chosen []newCalendarInput
	for _, address := range r.PostForm["calendar"] {
		in := newCalendarInput{
			Name: r.PostFormValue("name_" + address), URL: address, Color: r.PostFormValue("color_" + address),
			Audience: &audienceJSON{People: nonNil(r.PostForm["people_"+address]), Groups: nonNil(r.PostForm["groups_"+address])},
		}
		if convErr == nil {
			in.IntervalMinutes = &interval
		}
		chosen = append(chosen, in)
	}
	// Everything is checked before anything is saved, so a mistake in one
	// calendar leaves the others for the next try instead of half-saving.
	show := func(errKey, errField, errCalendar string) {
		found, derr := s.rediscover(r.Context(), p, conn)
		if derr != nil {
			// Without calendars the error cannot sit at one; show it generally.
			found, errField = nil, ""
		} else if errField != "" && !canTick(found, errCalendar) {
			// A calendar that is already synced (or unknown) shows no field,
			// so the message goes in the general error line.
			errField = ""
		}
		s.renderSelection(w, r, p, http.StatusUnprocessableEntity, conn, found, r.PostForm, errKey, errField, errCalendar)
	}
	if len(chosen) == 0 {
		show("connections.error.none_ticked", "", "")
		return
	}
	for _, in := range chosen {
		_, err := s.checkConnectionCalendar(p, conn, in)
		var bad *inputError
		switch {
		case errors.As(err, &bad):
			show(connectionErrorKey(bad), selectionErrorField(bad), in.URL)
			return
		case err != nil:
			s.fail(w, "add calendar", err)
			return
		}
	}
	for _, in := range chosen {
		if _, err := s.addConnectionCalendar(r, p, conn.ID, in); err != nil {
			s.fail(w, "add calendar", err)
			return
		}
	}
	s.redirectSaved(w, r, "/calendars", "flash.calendar_added")
}

// canTick reports whether the calendar at address is shown with a field the
// parents can still tick, so an error can sit there.
func canTick(found []discoveredJSON, address string) bool {
	return slices.ContainsFunc(found, func(f discoveredJSON) bool { return f.URL == address && !f.Added })
}

func (s *server) handleConnectionDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if err := s.db.DeleteConnection(p.household.ID, r.PathValue("id")); errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "delete connection", err)
		return
	}
	s.redirectSaved(w, r, "/calendars", "flash.connection_removed")
}
