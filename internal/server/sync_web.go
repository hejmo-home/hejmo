package server

import (
	"errors"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/store"
)

// parentPage resolves the signed-in parent for a web page, or answers with the
// redirect or refusal itself.
func (s *server) parentPage(w http.ResponseWriter, r *http.Request) (principal, bool) {
	p, ok := s.memberPage(w, r)
	if ok && p.person.Role != store.RoleParent {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return principal{}, false
	}
	return p, ok
}

// memberPage is parentPage for screens a child's device session may use too
// (ADR 0015): the handler checks what the person's role allows.
func (s *server) memberPage(w http.ResponseWriter, r *http.Request) (principal, bool) {
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
		if !s.gate.Allowed(calsync.ServiceOf(c.Kind)) {
			continue // paused by offline mode or a switch: not a problem
		}
		if calsync.StateAt(s.now(), c.Sync.StartedAt, c.Sync.LastSuccess) == calsync.Stale {
			return true
		}
	}
	return false
}

// pausedFlag is the word tag of a calendar or connection whose connection to
// the outside is off, with its last sync: a state, not a problem (ticket 201).
// It is nil if the connection is on.
func (s *server) pausedFlag(r *http.Request, p principal, svc outbound.Service, last time.Time) *flagView {
	var key string
	switch s.gate.Verdict(svc) {
	case outbound.VerdictAllowed:
		return nil
	case outbound.VerdictOffline:
		key = "outside.paused"
	default:
		key = "outside.paused.off"
	}
	after := s.say(r, i18n.Msg("sync.never"))
	if !last.IsZero() {
		after = s.say(r, i18n.Msg("outside.paused.last", "when", p.dateTimeSeconds(last)))
	}
	return &flagView{Kind: "paused", Text: s.say(r, i18n.Msg(key)), After: after}
}

type logView struct{ At, Calendar, Kind, Message, Detail string }

type choice struct{ ID, Name string }

var syncIntervals = []int{5, 15, 30, 60, 120, 360, 1440}

// syncFlag is what a synced calendar's state says in words when something is
// wrong: a refused login, a problem of the last run, or no success for a day.
func (s *server) syncFlag(r *http.Request, p principal, c store.SyncedCalendar, fact bool) *flagView {
	if f := s.pausedFlag(r, p, calsync.ServiceOf(c.Kind), c.Sync.LastSuccess); f != nil {
		return f
	}
	stale := calsync.StateAt(s.now(), c.Sync.StartedAt, c.Sync.LastSuccess) == calsync.Stale
	problem := c.Sync.LastError.Key != ""
	switch {
	case c.Sync.LastError.Key == loginFailKey:
		key := "sync.flag.login"
		if fact {
			key = "sync.flag.login_fact"
		}
		return &flagView{Kind: "problem", Text: s.say(r, i18n.Msg(key, "when", p.when(c.Sync.LastAttempt)))}
	case problem && stale:
		return &flagView{Kind: "problem", Text: s.say(r, i18n.Msg("sync.flag.stale_problem", "error", s.say(r, c.Sync.LastError)))}
	case problem:
		return &flagView{Kind: "problem", Text: s.say(r, i18n.Msg("sync.flag.problem", "error", s.say(r, c.Sync.LastError)))}
	case stale:
		return &flagView{Kind: "problem", Text: s.say(r, i18n.Msg("sync.flag.stale"))}
	}
	return nil
}

// loginFailKey is the stored message of a sync that the server refused the login for.
const loginFailKey = "sync.fail.login"

// syncLast says when a calendar was last synced successfully.
func (s *server) syncLast(r *http.Request, p principal, c store.SyncedCalendar) string {
	if c.Sync.LastSuccess.IsZero() {
		return s.say(r, i18n.Msg("sync.never"))
	}
	return s.say(r, i18n.Msg("sync.last", "when", p.when(c.Sync.LastSuccess)))
}

// audienceOf splits an audience into the people (as chips) and the names of
// the subgroups; nobody and no group means the whole family.
func audienceOf(a calendar.Audience, chips map[string]personChip, groups map[string]string) (people []personChip, groupNames []string, whole bool) {
	for _, id := range a.People {
		if c, ok := chips[id]; ok {
			people = append(people, c)
		}
	}
	for _, id := range a.Groups {
		if name, ok := groups[id]; ok {
			groupNames = append(groupNames, name)
		}
	}
	return people, groupNames, len(people) == 0 && len(groupNames) == 0
}

// audienceLine says who a calendar is for in a row: the whole family, or the
// people as markers and the subgroups by name.
func (s *server) audienceLine(r *http.Request, a calendar.Audience, chips map[string]personChip, groups map[string]string) rowLine {
	people, groupNames, whole := audienceOf(a, chips, groups)
	text := s.say(r, i18n.Msg("row.for"))
	if len(groupNames) > 0 {
		text += " " + strings.Join(groupNames, ", ")
	}
	return rowLine{Text: text, Whole: whole, People: people}
}

// audienceMaps loads the people (as chips) and subgroups (by name) of a household.
func (s *server) audienceMaps(householdID string) (map[string]personChip, map[string]string, []personChip, []choice, error) {
	people, err := s.db.People(householdID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	groups, err := s.db.Groups(householdID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	chips := map[string]personChip{}
	list := chipsOf(people)
	for _, c := range list {
		chips[c.ID] = c
	}
	names := map[string]string{}
	var choices []choice
	for _, g := range groups {
		names[g.ID] = g.Name
		choices = append(choices, choice{g.ID, g.Name})
	}
	return chips, names, list, choices, nil
}

func (s *server) renderCalendars(w http.ResponseWriter, r *http.Request, p principal, status int) {
	at := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return p.dateTimeSeconds(t)
	}
	chips, groupNames, _, _, err := s.audienceMaps(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	synced, err := s.db.SyncedCalendars(p.household.ID)
	if err != nil {
		s.fail(w, "synced calendars", err)
		return
	}
	// Subscribed calendars (ICS feeds) are rows; the calendars of a connection
	// are managed on the connection's page.
	var subscribed []rowView
	byConn := map[string][]store.SyncedCalendar{}
	for _, c := range synced {
		if c.Kind != "ics" {
			byConn[c.ConnectionID] = append(byConn[c.ConnectionID], c)
			continue
		}
		row := rowView{Href: "/calendars/" + c.ID, Title: c.Name, Lines: []rowLine{s.audienceLine(r, c.Audience, chips, groupNames), {Text: s.syncLast(r, p, c)}}}
		row.Flag = s.syncFlag(r, p, c, false)
		subscribed = append(subscribed, row)
	}
	conns, err := s.db.Connections(p.household.ID)
	if err != nil {
		s.fail(w, "connections", err)
		return
	}
	var connRows []rowView
	for _, c := range conns {
		connRows = append(connRows, s.connectionRow(r, p, c, byConn[c.ID]))
	}
	entries, err := s.db.SyncLog(p.household.ID, 50)
	if err != nil {
		s.fail(w, "sync log", err)
		return
	}
	var log []logView
	for _, e := range entries {
		log = append(log, logView{at(e.At), e.CalendarName, e.Kind, s.say(r, e.Message), e.Detail})
	}
	notice, err := s.reconnectNotice(r, p)
	if err != nil {
		s.fail(w, "reconnect notice", err)
		return
	}
	var offlineTop, connNote *offlineNote
	if s.offline() {
		offlineTop = s.noteFor(r, outbound.Calendars, "outside.note.calendars")
	} else {
		connNote = s.noteFor(r, outbound.Calendars, "outside.note.connect")
	}
	data := map[string]any{
		"GNotices":   s.googleNotices(r, p),
		"Reconnect":  notice,
		"OfflineTop": offlineTop, "ConnNote": connNote, "CanAdd": s.canAddCalendar(),
		"Person": p.person, "Rows": subscribed, "ConnRows": connRows, "Log": log,
		"SyncStale": s.syncStale(p.household.ID), "HTTPSWarning": s.httpsWarning(),
	}
	googleData(data)
	s.render(w, r, status, "calendars.html", data)
}

// connectionRow is the row of one calendar connection: the service, the
// calendars it puts on the box by name, and the last sync or what went wrong.
func (s *server) connectionRow(r *http.Request, p principal, c store.Connection, cals []store.SyncedCalendar) rowView {
	row := rowView{Href: "/connections/" + c.ID, Title: c.Username, Lines: []rowLine{{Text: s.serviceName(r, c)}}}
	var names []string
	var last time.Time
	var flag *flagView
	for _, cal := range cals {
		names = append(names, cal.Name)
		if cal.Sync.LastSuccess.After(last) {
			last = cal.Sync.LastSuccess
		}
		if f := s.syncFlag(r, p, cal, false); f != nil && (flag == nil || cal.Sync.LastError.Key == loginFailKey) {
			flag = f
		}
	}
	if c.Kind == "google" && !c.SignInNeededAt.IsZero() { // Google refuses the sign-in: the words say what to do
		flag = &flagView{Kind: "problem", Text: s.say(r, i18n.Msg("google.flag.signin"))}
	}
	if len(names) == 0 {
		row.Lines = append(row.Lines, rowLine{Text: s.say(r, i18n.Msg("connections.on_box_none"))})
	} else {
		row.Lines = append(row.Lines, rowLine{Text: s.say(r, i18n.Msg("connections.on_box", "names", strings.Join(names, ", ")))})
	}
	if c.Kind == "outlook" && !c.ReauthSince.IsZero() {
		flag = &flagView{Kind: "problem", Text: s.say(r, i18n.Msg("sync.flag.outlook_signin"))}
	}
	switch {
	case flag != nil:
		row.Flag = flag
	case !last.IsZero():
		row.Lines = append(row.Lines, rowLine{Text: s.say(r, i18n.Msg("sync.last", "when", p.when(last)))})
	case len(names) > 0:
		row.Lines = append(row.Lines, rowLine{Text: s.say(r, i18n.Msg("sync.never"))})
	}
	if f := s.pausedFlag(r, p, outbound.Calendars, last); f != nil { // paused: the tag and the last sync replace the rest
		row.Flag = f
		row.Lines = row.Lines[:2]
	}
	return row
}

// serviceName names the service of a connection: iCloud, or the CalDAV server's host.
func (s *server) serviceName(r *http.Request, c store.Connection) string {
	if c.Kind == "google" {
		return s.say(r, i18n.Msg("connections.provider.google"))
	}
	if c.Kind == "outlook" {
		return s.say(r, i18n.Msg("connections.provider.outlook"))
	}
	if c.URL == iCloudServer {
		return s.say(r, i18n.Msg("connections.provider.icloud"))
	}
	return s.say(r, i18n.Msg("connections.service.caldav", "host", connectionHost(c)))
}

// connectionHost is the host of a connection's server, the way people read it.
func connectionHost(c store.Connection) string {
	if u, err := url.Parse(c.URL); err == nil && u.Host != "" {
		return u.Host
	}
	return c.URL
}

// sourceKicker says where the calendars of a connection live ("Calendar in
// iCloud", "Calendar on cloud.example.org"): the small line above the name at
// the source in a card of Choose calendars.
func (s *server) sourceKicker(r *http.Request, c store.Connection) string {
	if c.Kind == "google" {
		return s.say(r, i18n.Msg("google.card.source"))
	}
	if c.URL == iCloudServer {
		return s.say(r, i18n.Msg("connections.card.source_icloud"))
	}
	return s.say(r, i18n.Msg("connections.card.source_host", "host", connectionHost(c)))
}

// nameHint is the sentence under "Name in Hejmo": renaming here does not
// rename the calendar at the source, which keeps its name (shown in <q>). Empty
// when the name at the source is not known. Names are escaped.
func (s *server) nameHint(r *http.Request, c store.Connection, sourceName string) template.HTML {
	if sourceName == "" {
		return ""
	}
	key := "connections.card.hint_host"
	if c.URL == iCloudServer {
		key = "connections.card.hint_icloud"
	}
	if c.Kind == "google" {
		key = "connections.card.hint_google"
	}
	if c.Kind == "outlook" {
		key = "outlook.card.hint"
	}
	const mark = "\x00name\x00"
	text := html.EscapeString(s.say(r, i18n.Msg(key, "host", connectionHost(c), "name", mark)))
	return template.HTML(strings.Replace(text, mark, "<q>"+html.EscapeString(sourceName)+"</q>", 1))
}

func (s *server) handleCalendarsPage(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.renderCalendars(w, r, p, http.StatusOK)
	}
}

// handleCalendarAdd subscribes to a calendar link. The form is the guide of the
// link card on the choice page; a mistake shows there, at its field, with the
// link card chosen and everything typed kept. The first sync has run when the
// success line says the box fetches the events.
func (s *server) handleCalendarAdd(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if !s.gate.Allowed(outbound.ICS) { // the choice page says why a link cannot be subscribed
		http.Redirect(w, r, "/connections/new", http.StatusSeeOther)
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
	// A missing name and a link that is none are both shown at once, each at its
	// field; the other mistakes are found one at a time by addICS.
	missing, _, _ := icsInputErrors(in)
	var cal store.SyncedCalendar
	var err error
	if len(missing) == 0 {
		cal, err = s.addICS(r, p, in)
	}
	var bad *inputError
	switch {
	case len(missing) > 0 || errors.As(err, &bad):
		extra := map[string]any{
			"ICSName": in.Name, "ICSURL": in.URL, "ICSPeople": in.Audience.People, "ICSGroups": in.Audience.Groups,
		}
		if len(missing) > 0 {
			extra["ICSErrorFields"] = missing
		} else {
			extra["ICSError"], extra["ICSErrorField"] = "calendars.error."+bad.Field, calendarErrorField(bad.Field)
		}
		if in.Color != "" {
			extra["ICSColor"] = in.Color
		}
		if convErr == nil {
			extra["ICSInterval"] = interval
		}
		s.renderConnectNew(w, r, p, http.StatusUnprocessableEntity, "ics", extra)
		return
	case err != nil:
		s.fail(w, "add calendar", err)
		return
	}
	s.redirectSavedNamed(w, r, "/calendars", "flash.calendar_subscribed", cal.Name)
}

// syncedCalendarPage finds the subscribed calendar of a detail page; the
// calendars of a connection have no page of their own.
func (s *server) syncedCalendarPage(w http.ResponseWriter, r *http.Request, p principal) (store.SyncedCalendar, bool) {
	c, err := s.db.SyncedCalendar(p.household.ID, r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound), err == nil && c.Kind != "ics":
		http.NotFound(w, r)
		return store.SyncedCalendar{}, false
	case err != nil:
		s.fail(w, "calendar", err)
		return store.SyncedCalendar{}, false
	}
	return c, true
}

func (s *server) handleCalendarPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	c, ok := s.syncedCalendarPage(w, r, p)
	if !ok {
		return
	}
	chips, groupNames, _, _, err := s.audienceMaps(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	people, groupList, whole := audienceOf(c.Audience, chips, groupNames)
	forFact := factView{Label: s.say(r, i18n.Msg("calendars.fact.for")), Whole: whole, People: people, Value: strings.Join(groupList, ", ")}
	last := factView{Label: s.say(r, i18n.Msg("calendars.fact.last"))}
	if f := s.syncFlag(r, p, c, true); f != nil {
		last.Flag = f
	} else if c.Sync.LastSuccess.IsZero() {
		last.Value = s.say(r, i18n.Msg("sync.never"))
	} else {
		last.Value = p.when(c.Sync.LastSuccess)
	}
	facts := []factView{{Label: s.say(r, i18n.Msg("calendars.fact.address")), Value: c.URL, Host: c.Kind == "google"}, forFact,
		{Label: s.say(r, i18n.Msg("calendars.fact.color")), Value: c.Color}, last}
	if !c.Sync.LastSuccess.IsZero() {
		run := c.Sync.LastRun
		facts = append(facts,
			factView{Label: s.say(r, i18n.Msg("calendars.fact.events")), Value: s.say(r, i18n.Msg("calendars.fact.events_value", "count", strconv.Itoa(c.Sync.EventCount)))},
			factView{Label: s.say(r, i18n.Msg("calendars.fact.run")), Value: s.say(r, i18n.Msg("calendars.fact.run_value", "new", strconv.Itoa(run.New), "changed", strconv.Itoa(run.Changed), "removed", strconv.Itoa(run.Removed)))})
	}
	page := map[string]any{"Person": p.person, "Calendar": c, "Facts": facts, "Title": c.Name}
	if c.Waste {
		lang := s.langOf(r)
		nexts, err := s.nextWastes(p.household, lang, []string{c.ID})
		if err != nil {
			s.fail(w, "next waste collection", err)
			return
		}
		if next, ok := nexts[c.ID]; ok {
			page["NextLabel"] = s.i18n.Format(lang, i18n.Msg("calendars.waste.next", "day", s.dayLabel(lang, p.household.DateFormat, next.Day)))
			page["NextTitles"] = s.i18n.Format(lang, i18n.Msg("calendars.waste.next_titles", "titles", strings.Join(next.Titles, ", ")))
		}
	}
	s.render(w, r, http.StatusOK, "calendar_detail.html", page)
}

func (s *server) handleCalendarSync(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	// A failed sync shows as a problem on the page it returns to, not as a success line.
	err := s.sync.Sync(r.Context(), p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	back := "/calendars/" + r.PathValue("id")
	if c, cerr := s.db.SyncedCalendar(p.household.ID, r.PathValue("id")); cerr == nil && c.Kind != "ics" {
		back = "/connections/" + c.ConnectionID
	}
	if err == nil {
		s.redirectSynced(w, r, back, r.PathValue("id"))
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (s *server) handleCalendarDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	c, err := s.db.SyncedCalendar(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "calendar", err)
		return
	}
	if err := s.db.DeleteSyncedCalendar(p.household.ID, c.ID); errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "delete calendar", err)
		return
	}
	s.redirectSavedNamed(w, r, "/calendars", "flash.calendar_removed", c.Name)
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

// handleCalendarReadOnly switches a CalDAV calendar between two-way and read-only.
func (s *server) handleCalendarReadOnly(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	readOnly := r.PostFormValue("read_only") == "1"
	err := s.db.SetCalendarReadOnly(p.household.ID, r.PathValue("id"), readOnly)
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
		return
	case err != nil:
		s.fail(w, "switch calendar", err)
		return
	}
	if !readOnly {
		s.sync.PushSoon(p.household.ID, r.PathValue("id"))
	}
	if c, err := s.db.SyncedCalendar(p.household.ID, r.PathValue("id")); err == nil && c.ConnectionID != "" {
		http.Redirect(w, r, "/connections/"+c.ConnectionID, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/calendars", http.StatusSeeOther)
}
