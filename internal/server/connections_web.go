package server

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/outbound"
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
		switch provider {
		case "icloud":
			return "connections.error.login_icloud"
		case "caldav":
			return "connections.error.login_caldav"
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

// wasteRow is a read-only calendar of a connection with its waste collection switch.
type wasteRow struct {
	ID, Name string
	Waste    bool
}

// writeRow is a CalDAV calendar of a connection with its two-way / read-only switch.
type writeRow struct {
	ID, Name string
	TwoWay   bool
	// Locked: Google shares the calendar for viewing only, so it can never be two-way.
	Locked bool
}

// selectionCalendar is one calendar on the choice page with what the parent
// entered for it, or what is stored, or the defaults. On means the switch is on.
type selectionCalendar struct {
	URL, Name, Color string
	// SourceName is the name at the source (iCloud, the CalDAV server): shown
	// in the head, never editable. Empty when the server was not asked.
	SourceName string
	Hint       template.HTML // "Only in Hejmo. ..." under the field; empty without a SourceName
	LastSync   string        // when it last synced, in words; empty for a calendar that is not synced
	// Google only: where the calendar comes from ("Calendar in Google", "Shared by …") and what the box may
	// do with it, in words, with Access ("read_write" or "read_only") for the tests and the API.
	Source, AccessLabel, AccessText, Access string
	Synced, On                              bool
	People, Groups                          []string
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
		c := selectionCalendar{URL: f.URL, Name: f.Name, Color: colors[i], Access: f.Access}
		if !f.fromStore {
			c.SourceName = f.Name
		}
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
		{Label: s.say(r, i18n.Msg("connections.fact.since")), Value: p.day(conn.CreatedAt)},
	}
	google := conn.Kind == "google"
	if google {
		var gerr error
		if facts, gerr = s.googleFacts(r, p, conn, lastFact); gerr != nil {
			s.fail(w, "google app", gerr)
			return
		}
	}
	shown := selectionCalendars(found, form, mine, taken)
	for i := range shown {
		shown[i].Hint = s.nameHint(r, conn, shown[i].SourceName)
		if google {
			s.googleCard(r, &shown[i], found)
		}
	}
	for i, c := range shown {
		for _, m := range mine {
			if c.Synced && caldavclient.AddressKey(m.URL) == caldavclient.AddressKey(c.URL) {
				shown[i].LastSync = s.syncLast(r, p, m)
			}
		}
	}
	var wasteRows []wasteRow
	for _, m := range mine {
		if m.ReadOnly {
			wasteRows = append(wasteRows, wasteRow{ID: m.ID, Name: m.Name, Waste: m.Waste})
		}
	}
	var writeRows []writeRow
	for _, m := range mine {
		if m.Kind == "caldav" || m.Kind == "google" {
			writeRows = append(writeRows, writeRow{ID: m.ID, Name: m.Name, TwoWay: !m.ReadOnly, Locked: m.SourceReadOnly})
		}
	}
	data := map[string]any{
		"Waste":     wasteRows,
		"WriteBack": writeRows,
		"Person":    p.person, "Connection": newConnectionView(conn), "Title": conn.Username,
		"Found": shown, "People": peopleChoices, "Groups": groupChoices, "Intervals": syncIntervals,
		"DefaultInterval": interval, "SyncStale": s.syncStale(p.household.ID),
		"Error": errKey, "ErrorField": errField, "ErrorCalendar": errCalendar,
		"Facts": facts, "Names": names, "ICloud": conn.URL == iCloudServer, "Host": connectionHost(conn), "SourceKicker": s.sourceKicker(r, conn),
		"LoginOpen": !refusedAt.IsZero() || errField == "password",
		"Google":    google, "GApp": googleAppView{}, "ConnID": conn.ID,
	}
	if google {
		app, aerr := s.googleAppView(r, p)
		if aerr != nil {
			s.fail(w, "google app", aerr)
			return
		}
		data["GApp"], data["GAccounts"], data["LoginOpen"] = app, app.Accounts, true
		data["GNotices"] = s.googleNoticesOf(r, p, conn.ID)
		data["Explain"] = s.say(r, i18n.Msg("google.remove.explain"))
		googleData(data)
	}
	if !refusedAt.IsZero() {
		data["Refused"] = s.say(r, i18n.Msg("connections.error.refused", "when", p.when(refusedAt)))
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
	if !s.gate.Allowed(outbound.Calendars) { // nothing is asked of the server: back to the page that says why
		http.Redirect(w, r, "/connections/new", http.StatusSeeOther)
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
	if service != "caldav" && service != "google" {
		service = "icloud"
	}
	app, err := s.googleAppView(r, p)
	if err != nil {
		s.fail(w, "google app", err)
		return
	}
	data := googleData(map[string]any{
		"Person": p.person, "Service": service, "SyncStale": s.syncStale(p.household.ID),
		"Error": "", "ErrorField": "", "ConnURL": "", "ConnUsername": "", "ConnSingle": false,
		"OutNote": s.noteFor(r, outbound.Calendars, "outside.note.connect"),
		"GApp":    app, "GClientID": "", "GGuide": false, "GReplace": false,
	})
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
	if !s.gate.Allowed(outbound.Calendars) { // paused: what the box has, nothing from the server
		s.renderPausedConnection(w, r, p, conn)
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
		errKey, status := connectionErrorKey(bad, ""), http.StatusBadGateway
		if bad.Field == "login" || bad.Field == "signin" {
			status = http.StatusOK
			if refused, _ := s.refusedSince(p, conn); refused || bad.Field == "signin" {
				errKey = "" // the line about the refused login, or the notice to sign in again, says it
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
		d := discoveredJSON{URL: c.URL, Name: c.Name, Added: true, CalendarID: c.ID, fromStore: true}
		if conn.Kind == "google" {
			d.Access, d.Reason = "read_write", "writable"
			if c.SourceReadOnly {
				d.Access, d.Reason = "read_only", "shared"
			}
		}
		out = append(out, d)
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
		s.renderSelection(w, r, p, http.StatusUnprocessableEntity, conn, stored, nil, connectionErrorKey(bad, ""), "password", "")
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
		} else if errField == "calendar" && errCalendar == "" && len(found) > 0 {
			errCalendar = found[0].URL // nothing is switched on: the first card says so
		} else if errField != "" && !shownIn(found, errCalendar) {
			// An unknown calendar shows no field, so the message goes in the
			// general error line.
			errField = ""
		}
		s.renderSelection(w, r, p, http.StatusUnprocessableEntity, conn, found, r.PostForm, errKey, errField, errCalendar)
	}
	if len(chosen) == 0 && len(keep) == 0 && len(off) == 0 {
		show("connections.error.none_ticked", "calendar", "") // at the first card's switch
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

// pausedCalendar is a calendar of a paused connection, as the page lists it.
type pausedCalendar struct{ Name, Line string }

// renderPausedConnection is the page of a connection while its connection to the
// outside is off (ticket 201, Q143): the facts, the calendars already on the
// box with their event counts, and nothing that needs the server.
func (s *server) renderPausedConnection(w http.ResponseWriter, r *http.Request, p principal, conn store.Connection) {
	all, err := s.db.SyncedCalendars(p.household.ID)
	if err != nil {
		s.fail(w, "synced calendars", err)
		return
	}
	chips, groups, _, _, err := s.audienceMaps(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	var last time.Time
	var cals []pausedCalendar
	for _, c := range calendarsOf(all, conn.ID) {
		if c.Sync.LastSuccess.After(last) {
			last = c.Sync.LastSuccess
		}
		people, groupNames, whole := audienceOf(c.Audience, chips, groups)
		who := s.say(r, i18n.Msg("chips.whole_family"))
		if !whole {
			var n []string
			for _, pc := range people {
				n = append(n, pc.Name)
			}
			who = strings.Join(append(n, groupNames...), ", ")
		}
		line := s.say(r, i18n.Msg("outside.onbox.line", "who", who, "n", strconv.Itoa(c.Sync.EventCount)))
		if !c.Sync.LastSuccess.IsZero() {
			line += " " + s.say(r, i18n.Msg("outside.onbox.until", "date", p.day(c.Sync.LastSuccess)))
		}
		cals = append(cals, pausedCalendar{Name: c.Name, Line: line})
	}
	facts := []factView{
		{Label: s.say(r, i18n.Msg("connections.fact.service")), Value: s.serviceName(r, conn)},
		{Label: s.say(r, i18n.Msg("outside.fact.sync")), Flag: s.pausedFlag(r, p, outbound.Calendars, time.Time{})},
		{Label: s.say(r, i18n.Msg("outside.fact.last")), Value: s.syncLastPlain(r, p, last)},
		{Label: s.say(r, i18n.Msg("connections.fact.since")), Value: p.day(conn.CreatedAt)},
	}
	s.render(w, r, http.StatusOK, "connection_paused.html", map[string]any{
		"Connection": newConnectionView(conn), "Title": conn.Username, "Facts": facts, "Calendars": cals,
		"OutNote": s.noteFor(r, outbound.Calendars, "outside.note.connection"),
	})
}

func (s *server) syncLastPlain(r *http.Request, p principal, t time.Time) string {
	if t.IsZero() {
		return s.say(r, i18n.Msg("sync.never"))
	}
	return p.dateTimeSeconds(t)
}
