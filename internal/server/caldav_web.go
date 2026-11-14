package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

// deviceScope is the token scope of a phone's calendar app.
const deviceScope = "calendars"

type deviceView struct{ ID, Name, Person, Created, LastUsed string }

type sharedView struct {
	ID, Name, Color string
	Shared, Always  bool
}

// renderCalDAV shows the calendar apps page. newToken is the value of a token
// just created, shown this once.
func (s *server) renderCalDAV(w http.ResponseWriter, r *http.Request, p principal, status int, form map[string]any, newName, newToken string) {
	loc := p.location()
	at := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.In(loc).Format(webTimeLayout)
	}
	acc, err := s.db.AccountByPerson(p.person.ID)
	if err != nil {
		s.fail(w, "account", err)
		return
	}
	tokens, err := s.db.APITokens(p.household.ID)
	if err != nil {
		s.fail(w, "tokens", err)
		return
	}
	var devices []deviceView
	for _, t := range tokens {
		if scopes := strings.Fields(t.Scopes); len(scopes) == 1 && scopes[0] == deviceScope {
			devices = append(devices, deviceView{t.ID, t.Name, t.PersonName, at(t.Created), at(t.LastUsed)})
		}
	}
	if err := s.db.EnsureFamilyCalendar(p.household.ID); err != nil {
		s.fail(w, "family calendar", err)
		return
	}
	cals, err := s.db.Calendars(p.household.ID)
	if err != nil {
		s.fail(w, "calendars", err)
		return
	}
	var shared []sharedView
	for _, c := range cals {
		shared = append(shared, sharedView{c.ID, c.Name, c.Color, c.Shared, c.Family})
	}
	data := map[string]any{
		"Person": p.person, "Address": s.httpsOrigin(r) + davPrefix + "/", "Username": acc.Username,
		"Tokens": devices, "Calendars": shared, "SyncStale": s.syncStale(p.household.ID), "Form": form,
	}
	if form != nil {
		data["Error"] = form["Error"]
	}
	if newToken != "" {
		data["NewToken"] = newToken
		data["NewTokenTitle"] = s.i18n.Format(s.langOf(r), i18n.Msg("caldav.token.created_title", "name", newName))
	}
	s.render(w, r, status, "caldav.html", data)
}

func (s *server) handleCalDAVPage(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.renderCalDAV(w, r, p, http.StatusOK, nil, "", "")
	}
}

func (s *server) handleDeviceTokenCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" || len(name) > 80 {
		s.renderCalDAV(w, r, p, http.StatusUnprocessableEntity, map[string]any{"Error": "caldav.error.name", "Name": name}, "", "")
		return
	}
	value := auth.NewToken()
	if _, err := s.db.CreateAPIToken(store.APIToken{Name: name, Hash: auth.HashToken(value), PersonID: p.person.ID, Scopes: deviceScope}); err != nil {
		s.fail(w, "create device token", err)
		return
	}
	s.renderCalDAV(w, r, p, http.StatusOK, nil, name, value)
}

func (s *server) handleDeviceTokenRevoke(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	t, err := s.db.APITokenByID(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "token", err)
		return
	}
	if err := s.db.RevokeAPIToken(t.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, "revoke token", err)
		return
	}
	http.Redirect(w, r, "/caldav", http.StatusSeeOther)
}

func (s *server) handleCalendarShare(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	switch err := s.db.SetCalendarShared(p.household.ID, r.PathValue("id"), r.PostFormValue("shared") == "on"); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	case err != nil:
		s.fail(w, "share calendar", err)
	default:
		http.Redirect(w, r, "/caldav", http.StatusSeeOther)
	}
}
