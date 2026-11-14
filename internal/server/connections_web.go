package server

import (
	"errors"
	"net/http"
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

// renderSelection shows the calendars found on a connection so parents can
// tick the ones to sync. Colour and audience are chosen per calendar.
func (s *server) renderSelection(w http.ResponseWriter, r *http.Request, p principal, status int, conn store.Connection, found []discoveredJSON, errKey string) {
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
	var peopleChoices, groupChoices []choice
	for _, x := range people {
		peopleChoices = append(peopleChoices, choice{x.ID, x.Name})
	}
	for _, x := range groups {
		groupChoices = append(groupChoices, choice{x.ID, x.Name})
	}
	s.render(w, r, status, "connection.html", map[string]any{
		"Person": p.person, "Connection": connectionView{ID: conn.ID, URL: conn.URL, Username: conn.Username},
		"Found": found, "People": peopleChoices, "Groups": groupChoices, "Intervals": syncIntervals,
		"DefaultInterval": defaultCalDAVSync, "SyncStale": s.syncStale(p.household.ID), "Error": errKey,
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
		form := map[string]any{"ConnError": connectionErrorKey(bad), "ConnProvider": in.Provider, "ConnURL": in.URL, "ConnUsername": in.Username, "ConnSingle": in.Single}
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
	s.renderSelection(w, r, p, http.StatusOK, conn, marked, "")
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
		s.renderSelection(w, r, p, http.StatusBadGateway, conn, nil, connectionErrorKey(bad))
		return
	case err != nil:
		s.fail(w, "discover", err)
		return
	}
	s.renderSelection(w, r, p, http.StatusOK, conn, found, "")
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
	for _, address := range r.PostForm["calendar"] {
		in := newCalendarInput{
			Name: r.PostFormValue("name_" + address), URL: address, Color: r.PostFormValue("color_" + address),
			Audience: &audienceJSON{People: nonNil(r.PostForm["people_"+address]), Groups: nonNil(r.PostForm["groups_"+address])},
		}
		if convErr == nil {
			in.IntervalMinutes = &interval
		}
		_, err := s.addConnectionCalendar(r, p, conn.ID, in)
		var bad *inputError
		switch {
		case errors.As(err, &bad):
			found, derr := s.rediscover(r.Context(), p, conn)
			if derr != nil {
				found = nil
			}
			s.renderSelection(w, r, p, http.StatusUnprocessableEntity, conn, found, connectionErrorKey(bad))
			return
		case err != nil:
			s.fail(w, "add calendar", err)
			return
		}
	}
	http.Redirect(w, r, "/calendars", http.StatusSeeOther)
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
	http.Redirect(w, r, "/calendars", http.StatusSeeOther)
}
