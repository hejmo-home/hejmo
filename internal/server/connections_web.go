package server

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/i18n"
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
// entered for it, or what is stored, or the defaults. On means the switch is on.
type selectionCalendar struct {
	URL, Name, Color string
	LastSync         string // when it last synced, in words; empty for a calendar that is not synced
	Synced, On       bool
	People, Groups   []string
}

// selectionCalendars shows the discovered calendars with the values of a
// submitted form (nil: the stored values of synced calendars, defaults for the
// others), so a page shown again after an error keeps what was entered. synced
// are the calendars of the connection, taken, the colours already in use.
func selectionCalendars(found []discoveredJSON, form url.Values, synced []store.SyncedCalendar, taken []string) []selectionCalendar {
	byKey := map[string]store.SyncedCalendar{}
	for _, c := range synced {
		byKey[caldavclient.AddressKey(c.URL)] = c
	}
	colors := nextColors(taken, len(found))
	out := make([]selectionCalendar, 0, len(found))
	for i, f := range found {
		c := selectionCalendar{URL: f.URL, Name: f.Name, Color: colors[i]}
		if stored, ok := byKey[caldavclient.AddressKey(f.URL)]; ok {
			c.Synced, c.On, c.Name, c.Color = true, true, stored.Name, stored.Color
			c.People, c.Groups = stored.Audience.People, stored.Audience.Groups
		}
		if form != nil {
			c.On = slices.Contains(form["calendar"], f.URL)
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
	all, err := s.db.SyncedCalendars(p.household.ID)
	if err != nil {
		s.fail(w, "synced calendars", err)
		return
	}
	mine := calendarsOf(all, conn.ID)
	var taken []string
	for _, c := range all {
		taken = append(taken, c.Color)
	}
	// What the box knows about the connection: when it last synced, and
	// whether the server refused the login.
	var last time.Time
	var refusedAt time.Time
	var names []string
	for _, c := range mine {
		names = append(names, c.Name)
		if c.Sync.LastSuccess.After(last) {
			last = c.Sync.LastSuccess
		}
		if c.Sync.LastError.Key == loginFailKey && c.Sync.LastAttempt.After(refusedAt) {
			refusedAt = c.Sync.LastAttempt
		}
	}
	lastFact := factView{Label: s.say(r, i18n.Msg("connections.fact.last")), Value: s.say(r, i18n.Msg("sync.never"))}
	if !last.IsZero() {
		lastFact.Value = p.when(last)
	}
	for _, c := range mine {
		if f := s.syncFlag(r, p, c, true); f != nil && (lastFact.Flag == nil || c.Sync.LastError.Key == loginFailKey) {
			lastFact.Flag = f
		}
	}
	facts := []factView{
		{Label: s.say(r, i18n.Msg("connections.fact.service")), Value: s.serviceName(r, conn)},
		{Label: s.say(r, i18n.Msg("connections.fact.username")), Value: conn.Username},
		lastFact,
		{Label: s.say(r, i18n.Msg("connections.fact.since")), Value: conn.CreatedAt.In(p.location()).Format(dayLayout)},
	}
	shown := selectionCalendars(found, form, mine, taken)
	for i, c := range shown {
		for _, m := range mine {
			if c.Synced && caldavclient.AddressKey(m.URL) == caldavclient.AddressKey(c.URL) {
				shown[i].LastSync = s.syncLast(r, p, m)
			}
		}
	}
	data := map[string]any{
		"Person": p.person, "Connection": connectionView{ID: conn.ID, URL: conn.URL, Username: conn.Username}, "Title": conn.Username,
		"Found": shown, "People": peopleChoices, "Groups": groupChoices, "Intervals": syncIntervals,
		"DefaultInterval": interval, "SyncStale": s.syncStale(p.household.ID),
		"Error": errKey, "ErrorField": errField, "ErrorCalendar": errCalendar,
		"Facts": facts, "Names": names, "ICloud": conn.URL == iCloudServer,
		"LoginOpen": !refusedAt.IsZero() || errField == "password",
	}
	if !refusedAt.IsZero() {
		data["Refused"] = s.say(r, i18n.Msg("connections.error.refused", "when", p.when(refusedAt)))
	}
	s.render(w, r, status, "connection.html", data)
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
		// The server cannot be asked now (the login was refused, or it is not
		// reachable): the page still shows the calendars the box has, so the
		// password can be replaced and the connection can be disconnected.
		stored, serr := s.storedCalendars(p, conn)
		if serr != nil {
			s.fail(w, "synced calendars", serr)
			return
		}
		errKey, status := connectionErrorKey(bad), http.StatusBadGateway
		if bad.Field == "login" {
			status = http.StatusOK
			if refused, _ := s.refusedSince(p, conn); refused {
				errKey = "" // the line about the refused login says it
			}
		}
		s.renderSelection(w, r, p, status, conn, stored, nil, errKey, "", "")
		return
	case err != nil:
		s.fail(w, "discover", err)
		return
	}
	s.renderSelection(w, r, p, http.StatusOK, conn, found, nil, "", "", "")
}

// calendarsOf picks the calendars of one connection out of the household's synced calendars.
func calendarsOf(all []store.SyncedCalendar, connectionID string) []store.SyncedCalendar {
	var mine []store.SyncedCalendar
	for _, c := range all {
		if c.ConnectionID == connectionID {
			mine = append(mine, c)
		}
	}
	return mine
}

// storedCalendars lists the calendars a connection has on the box, in the
// shape of discovered ones, for when the server cannot be asked.
func (s *server) storedCalendars(p principal, conn store.Connection) ([]discoveredJSON, error) {
	all, err := s.db.SyncedCalendars(p.household.ID)
	if err != nil {
		return nil, err
	}
	var out []discoveredJSON
	for _, c := range calendarsOf(all, conn.ID) {
		out = append(out, discoveredJSON{URL: c.URL, Name: c.Name, Added: true, CalendarID: c.ID})
	}
	return out, nil
}

// refusedSince reports whether the last sync of one of the connection's
// calendars was refused at login.
func (s *server) refusedSince(p principal, conn store.Connection) (bool, error) {
	all, err := s.db.SyncedCalendars(p.household.ID)
	if err != nil {
		return false, err
	}
	for _, c := range calendarsOf(all, conn.ID) {
		if c.Sync.LastError.Key == loginFailKey {
			return true, nil
		}
	}
	return false, nil
}

// handleConnectionCredentials replaces the password of a connection after the
// server accepted it. A refused login is shown at the password field and
// leaves the old password in place.
func (s *server) handleConnectionCredentials(w http.ResponseWriter, r *http.Request) {
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
	err = s.changeConnectionPassword(r.Context(), p, conn.ID, r.PostFormValue("password"))
	var bad *inputError
	switch {
	case errors.As(err, &bad):
		// Never sent back: the new password is not put on the page again.
		stored, serr := s.storedCalendars(p, conn)
		if serr != nil {
			s.fail(w, "synced calendars", serr)
			return
		}
		s.renderSelection(w, r, p, http.StatusUnprocessableEntity, conn, stored, nil, connectionErrorKey(bad), "password", "")
	case err != nil:
		s.fail(w, "change password", err)
	default:
		s.redirectSaved(w, r, "/connections/"+conn.ID, "flash.connection_password_saved")
	}
}

// choiceKeep is a synced calendar that stays switched on, with what the
// parents entered for it.
type choiceKeep struct {
	cal     store.SyncedCalendar
	changes calendarChanges
}

// handleConnectionCalendars saves the switches of Choose calendars: a calendar
// switched on is added (or, if already synced, updated), one switched off is
// removed from the box after a confirmation. Nothing is sent to the server.
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
	synced, err := s.db.SyncedCalendars(p.household.ID)
	if err != nil {
		s.fail(w, "synced calendars", err)
		return
	}
	have := map[string]store.SyncedCalendar{} // by address, this connection only
	for _, c := range synced {
		if c.ConnectionID == conn.ID {
			have[caldavclient.AddressKey(c.URL)] = c
		}
	}
	interval, convErr := strconv.Atoi(r.PostFormValue("interval"))
	var chosen []newCalendarInput
	var keep []choiceKeep
	on := map[string]bool{}
	for _, address := range r.PostForm["calendar"] {
		key := caldavclient.AddressKey(address)
		if on[key] {
			continue // the same calendar sent twice is saved once
		}
		on[key] = true
		name, color := r.PostFormValue("name_"+address), r.PostFormValue("color_"+address)
		audience := &audienceJSON{People: nonNil(r.PostForm["people_"+address]), Groups: nonNil(r.PostForm["groups_"+address])}
		if cal, isSynced := have[key]; isSynced {
			changes := calendarChanges{Name: &name, Color: &color, Audience: audience}
			if color == "" {
				changes.Color = nil // the form sent no colour: the calendar keeps its own
			}
			keep = append(keep, choiceKeep{cal, changes})
			continue
		}
		in := newCalendarInput{Name: name, URL: address, Color: color, Audience: audience}
		if convErr == nil {
			in.IntervalMinutes = &interval
		}
		chosen = append(chosen, in)
	}
	// A synced calendar is switched off only if the form showed it and its
	// switch is off now: a calendar the form did not show is left alone.
	var off []store.SyncedCalendar
	for _, address := range r.PostForm["shown"] {
		if cal, isSynced := have[caldavclient.AddressKey(address)]; isSynced && !on[caldavclient.AddressKey(address)] && !slices.ContainsFunc(off, func(c store.SyncedCalendar) bool { return c.ID == cal.ID }) {
			off = append(off, cal)
		}
	}
	// Everything is checked before anything is saved, so a mistake in one
	// calendar leaves the others for the next try instead of half-saving.
	show := func(errKey, errField, errCalendar string) {
		found, derr := s.rediscover(r.Context(), p, conn)
		if derr != nil {
			// Without calendars the error cannot sit at one; show it generally.
			found, errField = nil, ""
		} else if errField != "" && !shownIn(found, errCalendar) {
			// An unknown calendar shows no field, so the message goes in the
			// general error line.
			errField = ""
		}
		s.renderSelection(w, r, p, http.StatusUnprocessableEntity, conn, found, r.PostForm, errKey, errField, errCalendar)
	}
	if len(chosen) == 0 && len(keep) == 0 && len(off) == 0 {
		show("connections.error.none_ticked", "", "")
		return
	}
	check := func(err error, address string) bool {
		var bad *inputError
		switch {
		case errors.As(err, &bad):
			show(connectionErrorKey(bad), selectionErrorField(bad), address)
			return false
		case err != nil:
			s.fail(w, "add calendar", err)
			return false
		}
		return true
	}
	for _, in := range chosen {
		if _, err := s.checkConnectionCalendar(p, conn, in); !check(err, in.URL) {
			return
		}
	}
	for _, k := range keep {
		if err := s.checkSyncedChanges(p, k.cal, k.changes); !check(err, k.cal.URL) {
			return
		}
	}
	if len(off) > 0 && r.PostFormValue("confirm_off") == "" {
		s.renderSwitchOffConfirmation(w, r, p, conn, off)
		return
	}
	for _, k := range keep {
		if err := s.changeSyncedCalendar(p, k.cal, k.changes); err != nil {
			s.fail(w, "change calendar", err)
			return
		}
	}
	for _, in := range chosen {
		if _, err := s.addConnectionCalendar(r, p, conn.ID, in); err != nil {
			s.fail(w, "add calendar", err)
			return
		}
	}
	// Switch-offs come last: a store error before this point leaves nothing removed.
	for _, cal := range off {
		if err := s.db.DeleteSyncedCalendar(p.household.ID, cal.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.fail(w, "switch calendar off", err)
			return
		}
	}
	if len(keep) == 0 && len(off) == 0 {
		s.redirectSaved(w, r, "/calendars", "flash.calendar_added")
		return
	}
	s.redirectSaved(w, r, "/calendars", "flash.calendars_saved")
}

// checkSyncedChanges checks the entries for a synced calendar that stays on
// without saving them.
func (s *server) checkSyncedChanges(p principal, cal store.SyncedCalendar, in calendarChanges) error {
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return &inputError{"name", "name is required"}
	}
	if in.Color != nil && !hexColor.MatchString(strings.TrimSpace(*in.Color)) {
		return &inputError{"color", "color must be #RRGGBB"}
	}
	if in.Audience != nil {
		ok, err := s.audienceExists(p.household.ID, calendar.Audience{People: in.Audience.People, Groups: in.Audience.Groups})
		if err != nil {
			return err
		}
		if !ok {
			return &inputError{"audience", "audience: unknown person or group"}
		}
	}
	return nil
}

// renderSwitchOffConfirmation asks before calendars leave the box. It carries
// everything the form sent as hidden fields, so confirming saves the same
// choice; nothing has been saved yet.
func (s *server) renderSwitchOffConfirmation(w http.ResponseWriter, r *http.Request, p principal, conn store.Connection, off []store.SyncedCalendar) {
	type field struct{ Name, Value string }
	var hidden []field
	keys := make([]string, 0, len(r.PostForm))
	for k := range r.PostForm {
		if k != "confirm_off" {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		for _, v := range r.PostForm[k] {
			hidden = append(hidden, field{k, v})
		}
	}
	var names []string
	for _, c := range off {
		names = append(names, c.Name)
	}
	s.render(w, r, http.StatusOK, "connection_off.html", map[string]any{
		"Person": p.person, "Connection": connectionView{ID: conn.ID, URL: conn.URL, Username: conn.Username},
		"Names": names, "Hidden": hidden, "SyncStale": s.syncStale(p.household.ID),
	})
}

// shownIn reports whether the calendar at address is on the choice page, so
// an error can sit at its fields.
func shownIn(found []discoveredJSON, address string) bool {
	return slices.ContainsFunc(found, func(f discoveredJSON) bool { return f.URL == address })
}

func (s *server) handleConnectionDelete(w http.ResponseWriter, r *http.Request) {
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
	if err := s.db.DeleteConnection(p.household.ID, conn.ID); errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "delete connection", err)
		return
	}
	s.redirectSavedNamed(w, r, "/calendars", "flash.connection_removed", conn.Username)
}
