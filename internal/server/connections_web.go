package server

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

type connectionView struct {
	ID, URL, Username string
	// ICloud is set for iCloud; other servers show their host as the name.
	ICloud bool
	Host   string
}

func newConnectionView(c store.Connection) connectionView {
	v := connectionView{ID: c.ID, URL: c.URL, Username: c.Username, ICloud: c.URL == iCloudServer}
	if u, err := url.Parse(c.URL); err == nil {
		v.Host = u.Host
	}
	return v
}

// connectionErrorKey is the i18n key explaining an input error. provider is
// the service being connected ("icloud" or "caldav"); empty for a connection
// that is already saved, where the guide's steps are not on the page.
func connectionErrorKey(e *inputError, provider string) string {
	switch e.Field {
	case "name", "color", "interval", "audience":
		return "calendars.error." + e.Field
	case "url":
		return "connections.error.url"
	case "login":
		if provider == "icloud" {
			return "connections.error.login_icloud"
		}
	case "server":
		switch provider {
		case "icloud":
			return "connections.error.server_icloud"
		case "":
			return "connections.error.server_saved"
		}
	}
	return "connections.error." + e.Field // provider, username, password, login, server, foreign_host, downgrade
}

// connectionErrorField names the input of the connect page an error belongs
// to; empty means it has no field and shows as a general message. A refused
// login sits at the password. iCloud has no address field, so its server
// problems show generally.
func connectionErrorField(e *inputError, provider string) string {
	switch e.Field {
	case "username", "password", "url":
		return e.Field
	case "login":
		return "password"
	case "server", "foreign_host", "downgrade":
		if provider == "caldav" {
			return "url"
		}
	}
	return ""
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
func (s *server) renderSelection(w http.ResponseWriter, r *http.Request, p principal, status int, conn store.Connection, found []discoveredJSON, form url.Values, errKey, errField, errCalendar string, success ...i18n.Message) {
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
	var mine []store.SyncedCalendar
	var taken []string
	for _, c := range all {
		taken = append(taken, c.Color)
		if c.ConnectionID == conn.ID {
			mine = append(mine, c)
		}
	}
	data := map[string]any{
		"Person": p.person, "Connection": newConnectionView(conn),
		"BackHref": "/calendars", "BackLabel": "calendars.back",
		"Found": selectionCalendars(found, form, mine, taken), "People": peopleChoices, "Groups": groupChoices, "Intervals": syncIntervals,
		"DefaultInterval": interval, "SyncStale": s.syncStale(p.household.ID),
		"Error": errKey, "ErrorField": errField, "ErrorCalendar": errCalendar,
	}
	if len(success) > 0 {
		data["SuccessMsg"] = success[0]
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
		s.renderConnectNew(w, r, p, http.StatusUnprocessableEntity, in.Provider, map[string]any{
			"Error": connectionErrorKey(bad, in.Provider), "ErrorField": connectionErrorField(bad, in.Provider),
			"ConnURL": in.URL, "ConnUsername": in.Username, "ConnSingle": in.Single,
		})
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
	s.renderSelection(w, r, p, http.StatusOK, conn, marked, nil, "", "", "", connectedMessage(in.Provider, len(marked)))
}

// connectedMessage is the success line after connecting: what was connected
// and how many calendars were found.
func connectedMessage(provider string, n int) i18n.Message {
	key := "connections.success." + provider
	switch n {
	case 0:
		return i18n.Msg(key + ".none")
	case 1:
		return i18n.Msg(key + ".one")
	}
	return i18n.Msg(key+".many", "n", strconv.Itoa(n))
}

// renderConnectNew shows the connect page with the guide of one service
// checked. Without a service, or with an unknown one, iCloud is checked.
func (s *server) renderConnectNew(w http.ResponseWriter, r *http.Request, p principal, status int, service string, extra map[string]any) {
	if service != "caldav" {
		service = "icloud"
	}
	data := map[string]any{
		"Person": p.person, "Service": service, "SyncStale": s.syncStale(p.household.ID),
		"BackHref": "/calendars", "BackLabel": "calendars.back",
		"Error": "", "ErrorField": "", "ConnURL": "", "ConnUsername": "", "ConnSingle": false,
	}
	for k, v := range extra {
		data[k] = v
	}
	s.render(w, r, status, "connection_new.html", data)
}

func (s *server) handleConnectNew(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.renderConnectNew(w, r, p, http.StatusOK, r.URL.Query().Get("service"), nil)
	}
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
		s.renderSelection(w, r, p, http.StatusBadGateway, conn, nil, nil, connectionErrorKey(bad, ""), "", "")
		return
	case err != nil:
		s.fail(w, "discover", err)
		return
	}
	s.renderSelection(w, r, p, http.StatusOK, conn, found, nil, "", "", "")
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
			show(connectionErrorKey(bad, ""), selectionErrorField(bad), address)
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
		"Person": p.person, "Connection": newConnectionView(conn),
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
	if err := s.db.DeleteConnection(p.household.ID, r.PathValue("id")); errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "delete connection", err)
		return
	}
	s.redirectSaved(w, r, "/calendars", "flash.connection_removed")
}
