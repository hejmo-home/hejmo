package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/iosprofile"
	"github.com/hejmo-home/hejmo/internal/store"
)

// deviceScope is the token scopes of a new CalDAV client: its calendars and,
// since ADR 0018, the task lists. Tokens made before that hold calendars only
// and are device tokens all the same.
const deviceScope = "calendars tasks"

type sharedView struct {
	ID, Name, Color string
	Shared, Always  bool
}

// deviceTokens lists the household's device tokens (scopes calendars and tasks only):
// the tokens other software uses through the API are not device tokens.
func (s *server) deviceTokens(householdID string) ([]store.APIToken, error) {
	tokens, err := s.db.APITokens(householdID)
	if err != nil {
		return nil, err
	}
	var out []store.APIToken
	for _, t := range tokens {
		if isDeviceToken(t) {
			out = append(out, t)
		}
	}
	return out, nil
}

func isDeviceToken(t store.APIToken) bool {
	scopes := strings.Fields(t.Scopes)
	for _, sc := range scopes {
		if sc != "calendars" && sc != "tasks" {
			return false
		}
	}
	return len(scopes) > 0
}

// scopeTextKey names the message that says what a device token may do.
func scopeTextKey(t store.APIToken) string {
	cal, tasks := scopedTo(t.Scopes, "calendars"), scopedTo(t.Scopes, "tasks")
	switch {
	case cal && tasks:
		return "caldav.scope.calendars_tasks"
	case tasks:
		return "caldav.scope.tasks"
	}
	return "caldav.scope.calendars"
}

// chipByPerson looks up the marker and name of each person of the household.
func (s *server) chipByPerson(householdID string) (map[string]personChip, error) {
	people, err := s.db.People(householdID)
	if err != nil {
		return nil, err
	}
	out := map[string]personChip{}
	for _, c := range chipsOf(people) {
		out[c.ID] = c
	}
	return out, nil
}

// whoLine is the "For <person>" line of a row: the person as marker and name,
// or just the name when the person is gone.
func (s *server) whoLine(r *http.Request, chips map[string]personChip, personID, fallback string) rowLine {
	line := rowLine{Text: s.say(r, i18n.Msg("row.for"))}
	if c, ok := chips[personID]; ok {
		line.People = []personChip{c}
	} else {
		line.Text += " " + fallback
	}
	return line
}

// renderCalDAV shows the calendar apps page: the address and user name, the
// device tokens as calm rows, and the calendars shared through the box.
func (s *server) renderCalDAV(w http.ResponseWriter, r *http.Request, p principal, status int, form map[string]any) {
	acc, err := s.db.AccountByPerson(p.person.ID)
	if err != nil {
		s.fail(w, "account", err)
		return
	}
	tokens, err := s.deviceTokens(p.household.ID)
	if err != nil {
		s.fail(w, "tokens", err)
		return
	}
	chips, err := s.chipByPerson(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	var rows []rowView
	for _, t := range tokens {
		row := rowView{Href: "/caldav/tokens/" + t.ID, Title: t.Name, Lines: []rowLine{s.whoLine(r, chips, t.PersonID, t.PersonName)}}
		if t.LastUsed.IsZero() {
			row.Flag = &flagView{Kind: "warn", Text: s.say(r, i18n.Msg("caldav.token.flag.never"))}
		} else {
			row.Lines = append(row.Lines, rowLine{Text: s.say(r, i18n.Msg("caldav.token.last_used_at", "when", p.when(t.LastUsed)))})
		}
		rows = append(rows, row)
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
	facts := []factView{
		{Label: s.say(r, i18n.Msg("caldav.address")), Value: s.httpsOrigin(r) + davPrefix + "/"},
		{Label: s.say(r, i18n.Msg("caldav.username")), Value: acc.Username},
	}
	data := map[string]any{
		"Person": p.person, "Facts": facts, "Rows": rows, "Calendars": shared, "SyncStale": s.syncStale(p.household.ID), "Form": form,
	}
	if form != nil {
		data["Error"], data["ErrorField"] = form["Error"], form["ErrorField"]
		data["ProfileError"] = form["ProfileError"]
	}
	// With the box's own CA the phone must trust the box before the profile can work (ADR 0014).
	data["CAFirst"] = s.pki.Ready() && !s.publicInUse()
	s.render(w, r, status, "caldav.html", data)
}

func (s *server) handleCalDAVPage(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.renderCalDAV(w, r, p, http.StatusOK, nil)
	}
}

func (s *server) handleDeviceTokenCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if !validDeviceName(name) {
		s.renderCalDAV(w, r, p, http.StatusUnprocessableEntity, map[string]any{"Error": "caldav.error.name", "ErrorField": "name", "Name": name})
		return
	}
	value := auth.NewToken()
	created, err := s.db.CreateAPIToken(store.APIToken{Name: name, Hash: auth.HashToken(value), PersonID: p.person.ID, Scopes: deviceScope})
	if err != nil {
		s.fail(w, "create device token", err)
		return
	}
	acc, err := s.db.AccountByPerson(p.person.ID)
	if err != nil {
		s.fail(w, "account", err)
		return
	}
	// The secret is shown on this answer only: no redirect, nothing stored but its hash.
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, http.StatusOK, "token_once.html", map[string]any{
		"Person": p.person, "Name": name, "Token": value, "ID": created.ID, "Address": s.httpsOrigin(r) + davPrefix + "/", "Username": acc.Username,
		"SuccessText": s.say(r, i18n.Msg("flash.token_created", "name", name)),
	})
}

func (s *server) handleDeviceTokenPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	t, err := s.db.APITokenByID(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && !isDeviceToken(t)) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "token", err)
		return
	}
	chips, err := s.chipByPerson(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	who := factView{Label: s.say(r, i18n.Msg("caldav.fact.for"))}
	if c, ok := chips[t.PersonID]; ok {
		who.People = []personChip{c}
	} else {
		who.Value = t.PersonName
	}
	last := factView{Label: s.say(r, i18n.Msg("caldav.fact.last_used"))}
	if t.LastUsed.IsZero() {
		last.Flag = &flagView{Kind: "warn", Text: s.say(r, i18n.Msg("caldav.token.flag.never"))}
	} else {
		last.Value = p.when(t.LastUsed)
	}
	s.render(w, r, http.StatusOK, "token.html", map[string]any{
		"Person": p.person, "Token": t, "Title": t.Name,
		"Facts": []factView{who,
			{Label: s.say(r, i18n.Msg("caldav.fact.scope")), Value: s.say(r, i18n.Msg(scopeTextKey(t)))},
			{Label: s.say(r, i18n.Msg("caldav.fact.created")), Value: p.when(t.Created)},
			last},
	})
}

// iosProfile creates a new device token for the person and returns the iOS
// configuration profile that holds it (ADR 0017). The box keeps no copy: the
// token is stored hashed, the profile only exists in this answer.
// The token opens the person's calendars and the task lists (ADR 0018).
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
	name := strings.TrimSpace(r.PostFormValue("profile_name"))
	switch {
	case r.TLS == nil: // a token must never travel unencrypted
		s.renderCalDAV(w, r, p, http.StatusForbidden, map[string]any{"ProfileError": "caldav.profile.error.https", "ProfileName": name})
	case !validDeviceName(name):
		s.renderCalDAV(w, r, p, http.StatusUnprocessableEntity, map[string]any{"Error": "caldav.error.name", "ErrorField": "profile_name", "ProfileName": name})
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
	s.redirectSavedNamed(w, r, "/caldav", "flash.token_revoked", t.Name)
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
		s.redirectSaved(w, r, "/caldav", shareKey(r))
	}
}

// shareKey is the success line for turning sharing through the box on or off.
func shareKey(r *http.Request) string {
	if r.PostFormValue("shared") == "on" {
		return "flash.share_on"
	}
	return "flash.share_off"
}
