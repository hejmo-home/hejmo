package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/iosprofile"
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
		return t.In(loc).Format(p.timeLayout())
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
		data["ProfileError"] = form["ProfileError"]
	}
	// With the box's own CA the phone must trust the box before the profile can work (ADR 0014).
	data["CAFirst"] = s.pki.Ready() && !s.publicInUse()
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
	if !validDeviceName(name) {
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

// iosProfile creates a new device token for the person and returns the iOS
// configuration profile that holds it (ADR 0017). The box keeps no copy: the
// token is stored hashed, the profile only exists in this answer.
//
// TODO(#94): add the tasks scope to this token once tasks sync over CalDAV.
func (s *server) iosProfile(r *http.Request, p principal, name string) ([]byte, error) {
	acc, err := s.db.AccountByPerson(p.person.ID)
	if err != nil {
		return nil, err
	}
	origin, err := url.Parse(s.httpsOrigin(r))
	if err != nil {
		return nil, err
	}
	port := 443
	if origin.Port() != "" {
		if port, err = strconv.Atoi(origin.Port()); err != nil {
			return nil, err
		}
	}
	value := auth.NewToken()
	if _, err := s.db.CreateAPIToken(store.APIToken{Name: name, Hash: auth.HashToken(value), PersonID: p.person.ID, Scopes: deviceScope}); err != nil {
		return nil, err
	}
	return iosprofile.CalDAV(iosprofile.Params{
		DeviceName: name, Host: origin.Hostname(), Port: port, Username: acc.Username, Token: value,
		PrincipalPath: davPrefix + "/" + p.person.ID + "/", Identifier: "org.hejmo.box.caldav." + p.person.ID,
		ProfileUUID: iosprofile.NewUUID(), PayloadUUID: iosprofile.NewUUID(),
	})
}

func sendProfile(w http.ResponseWriter, profile []byte) {
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Content-Disposition", `attachment; filename="hejmo-calendars.mobileconfig"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(profile)
}

// validDeviceName counts characters, as maxLength in openapi.yaml and maxlength
// in the form do.
func validDeviceName(name string) bool { return name != "" && utf8.RuneCountInString(name) <= 80 }

func (s *server) handleIOSProfile(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	switch {
	case r.TLS == nil: // a token must never travel unencrypted
		s.renderCalDAV(w, r, p, http.StatusForbidden, map[string]any{"ProfileError": "caldav.profile.error.https", "ProfileName": name}, "", "")
	case !validDeviceName(name):
		s.renderCalDAV(w, r, p, http.StatusUnprocessableEntity, map[string]any{"ProfileError": "caldav.error.name", "ProfileName": name}, "", "")
	default:
		profile, err := s.iosProfile(r, p, name)
		if err != nil {
			s.fail(w, "ios profile", err)
			return
		}
		sendProfile(w, profile)
	}
}

func (s *server) apiIOSProfile(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	name := strings.TrimSpace(in.Name)
	switch {
	case r.TLS == nil:
		writeError(w, http.StatusForbidden, "the profile holds a token and is served over HTTPS only")
	case !validDeviceName(name):
		writeError(w, http.StatusUnprocessableEntity, "name is required (at most 80 characters)")
	default:
		profile, err := s.iosProfile(r, p, name)
		if err != nil {
			s.fail(w, "ios profile", err)
			return
		}
		sendProfile(w, profile)
	}
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
