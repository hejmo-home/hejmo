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
		if calsync.StateAt(s.now(), c.Sync.StartedAt, c.Sync.LastSuccess) == calsync.Stale {
			return true
		}
	}
	return false
}

type logView struct{ At, Calendar, Kind, Message string }

type choice struct{ ID, Name string }

var syncIntervals = []int{5, 15, 30, 60, 120, 360, 1440}

// syncFlag is what a synced calendar's state says in words when something is
// wrong: a refused login, a problem of the last run, or no success for a day.
func (s *server) syncFlag(r *http.Request, p principal, c store.SyncedCalendar, fact bool) *flagView {
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

func (s *server) renderCalendars(w http.ResponseWriter, r *http.Request, p principal, status int, form map[string]any) {
	at := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return p.dateTimeSeconds(t)
	}
	chips, groupNames, peopleChoices, groupChoices, err := s.audienceMaps(p.household.ID)
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
		log = append(log, logView{at(e.At), e.CalendarName, e.Kind, s.say(r, e.Message)})
	}
	var taken []string
	for _, c := range synced {
		taken = append(taken, c.Color)
	}
	data := map[string]any{
		"AutoColor": nextColors(taken, 1)[0],
		"Person":    p.person, "Rows": subscribed, "ConnRows": connRows, "Log": log, "People": peopleChoices, "Groups": groupChoices,
		"Intervals": syncIntervals, "IntervalChosen": intervalChosen(form), "SyncStale": s.syncStale(p.household.ID), "HTTPSWarning": s.httpsWarning(), "Form": form,
	}
	if form != nil {
		data["Error"], data["ErrorField"] = form["Error"], form["ErrorField"]
	}
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
	if len(names) == 0 {
		row.Lines = append(row.Lines, rowLine{Text: s.say(r, i18n.Msg("connections.on_box_none"))})
	} else {
		row.Lines = append(row.Lines, rowLine{Text: s.say(r, i18n.Msg("connections.on_box", "names", strings.Join(names, ", ")))})
	}
	switch {
	case flag != nil:
		row.Flag = flag
	case !last.IsZero():
		row.Lines = append(row.Lines, rowLine{Text: s.say(r, i18n.Msg("sync.last", "when", p.when(last)))})
	case len(names) > 0:
		row.Lines = append(row.Lines, rowLine{Text: s.say(r, i18n.Msg("sync.never"))})
	}
	return row
}

// serviceName names the service of a connection: iCloud, or the CalDAV server's host.
func (s *server) serviceName(r *http.Request, c store.Connection) string {
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
	const mark = "\x00name\x00"
	text := html.EscapeString(s.say(r, i18n.Msg(key, "host", connectionHost(c), "name", mark)))
	return template.HTML(strings.Replace(text, mark, "<q>"+html.EscapeString(sourceName)+"</q>", 1))
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
	facts := []factView{{Label: s.say(r, i18n.Msg("calendars.fact.address")), Value: c.URL}, forFact,
		{Label: s.say(r, i18n.Msg("calendars.fact.color")), Value: c.Color}, last}
	if !c.Sync.LastSuccess.IsZero() {
		run := c.Sync.LastRun
		facts = append(facts,
			factView{Label: s.say(r, i18n.Msg("calendars.fact.events")), Value: s.say(r, i18n.Msg("calendars.fact.events_value", "count", strconv.Itoa(run.Events)))},
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

// intervalChosen is the update interval the add form shows selected: the one
// just typed after an error, else the default of an hour.
func intervalChosen(form map[string]any) int {
	if n, ok := form["Interval"].(int); ok && n > 0 {
		return n
	}
	return 60
}
