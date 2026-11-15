package server

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/holiday"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/wholine"
)

// The calendar screen (ticket 5.12, ADR 0011 and 0015): the week, and creating,
// changing and deleting events. Everything here is also possible through the
// REST API; these routes call the same domain and store code.

var calendarWeekdays = []string{"MO", "TU", "WE", "TH", "FR", "SA", "SU"}

// timeZoneChoices are offered as suggestions; any IANA zone name is accepted.
var timeZoneChoices = []string{"Europe/Berlin", "Europe/Vienna", "Europe/Zurich", "Europe/London", "Europe/Paris", "Europe/Rome", "UTC", "America/New_York", "America/Los_Angeles", "Asia/Tokyo", "Pacific/Auckland"}

// eventItem is one event as the week and the search show it.
type eventItem struct {
	Title, Time, Location, Date string
	Href                        string // empty for events of read-only calendars: no edit actions
	Slot                        int    // colour slot of the first person in the audience, 0 = whole family
	People                      []personChip
	Groups                      []string
	WhoParts                    []wholine.Part // who it is for, in words (wholine, nobody left out), one part per name for the page to keep whole
	Repeats, ReadOnly           bool
}

type weekBar struct {
	Item            eventItem
	Col, Span, Lane int // 1-based, for the grid
	Before, After   bool
}

type weekDay struct {
	ISO, Date, Name, Holiday string
	Today                    bool
	Events, AllDay           []eventItem
}

// calendarContext holds what is needed to turn events into items.
type calendarContext struct {
	s         *server
	r         *http.Request
	p         principal
	loc       *time.Location
	chips     map[string]personChip
	groups    map[string]string
	readOnly  map[string]bool
	calendars []store.Calendar
	// For the who-line: everyone in display order, the subgroups, and who sees
	// what (the calendars' audiences).
	household []wholine.Person
	wgroups   []wholine.Group
	viewers   []calendar.Viewer
	audiences map[string]calendar.Audience
}

func (s *server) calendarContext(w http.ResponseWriter, r *http.Request, p principal) (*calendarContext, bool) {
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return nil, false
	}
	groups, err := s.db.Groups(p.household.ID)
	if err != nil {
		s.fail(w, "groups", err)
		return nil, false
	}
	cals, err := s.db.Calendars(p.household.ID)
	if err != nil {
		s.fail(w, "calendars", err)
		return nil, false
	}
	c := &calendarContext{s: s, r: r, p: p, loc: p.location(), chips: map[string]personChip{}, groups: map[string]string{}, readOnly: map[string]bool{}, calendars: cals}
	c.audiences = map[string]calendar.Audience{}
	memberOf := map[string][]string{}
	for _, chip := range chipsOf(people) {
		c.chips[chip.ID] = chip
	}
	for _, g := range groups {
		c.groups[g.ID] = g.Name
		c.wgroups = append(c.wgroups, wholine.Group{Name: g.Name, Members: g.Members})
		for _, m := range g.Members {
			memberOf[m] = append(memberOf[m], g.ID)
		}
	}
	for _, cal := range cals {
		c.readOnly[cal.ID] = cal.ReadOnly
		c.audiences[cal.ID] = cal.Audience
	}
	for _, p := range people {
		c.household = append(c.household, wholine.Person{ID: p.ID, Name: p.Name})
		c.viewers = append(c.viewers, calendar.Viewer{PersonID: p.ID, Groups: memberOf[p.ID]})
	}
	return c, true
}

// clock formats a time of day the way the household wants it.
func (c *calendarContext) clock(t time.Time) string {
	if c.p.household.TimeFormat == store.Time12h {
		return t.In(c.loc).Format("3:04 PM")
	}
	return t.In(c.loc).Format("15:04")
}

// eventHref is the address of an occurrence's page: the series (or single
// event) and the original start of the occurrence.
func eventHref(o calendar.Occurrence) string {
	id := o.EventID
	if o.SeriesID != "" {
		id = o.SeriesID
	}
	return "/calendar/events/" + id + "?" + url.Values{"occurrence": {o.OriginalStart().Format(time.RFC3339)}}.Encode()
}

func (c *calendarContext) item(o calendar.Occurrence) eventItem {
	it := eventItem{Title: c.s.title(c.r, o.Title), Location: o.Location, ReadOnly: c.readOnly[o.CalendarID], Repeats: o.SeriesID != ""}
	if !it.ReadOnly {
		it.Href = eventHref(o)
	}
	if !o.AllDay {
		it.Time = c.clock(o.Start)
	}
	for _, id := range o.People {
		if chip, ok := c.chips[id]; ok {
			it.People = append(it.People, chip)
		}
	}
	for _, id := range o.Groups {
		if name, ok := c.groups[id]; ok {
			it.Groups = append(it.Groups, name)
		}
	}
	if len(it.People) > 0 {
		it.Slot = it.People[0].Slot
	}
	lang := c.s.langOf(c.r)
	it.WhoParts = wholine.Of(wholine.Input{Household: c.household, Groups: c.wgroups, Audience: calendar.ViewersOf(o, c.audiences, c.viewers)},
		c.s.i18n, lang).Parts
	it.Date = o.Start.In(c.loc).Format("02/01/2006")
	return it
}

// weekStartOf reads ?week= (any day of the week) and defaults to this week.
func (s *server) weekStartOf(r *http.Request, p principal) time.Time {
	loc := p.location()
	if d, err := time.ParseInLocation(dateLayout, r.URL.Query().Get("week"), loc); err == nil {
		return calendar.StartOfWeek(d)
	}
	return calendar.StartOfWeek(s.now().In(loc))
}

func weekLink(t time.Time) string { return "/calendar?week=" + t.Format(dateLayout) }

func (s *server) handleCalendarWeek(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	c, ok := s.calendarContext(w, r, p)
	if !ok {
		return
	}
	start := s.weekStartOf(r, p)
	end := start.AddDate(0, 0, 7)
	events, err := s.db.EventsBetween(p.household.ID, start, end)
	if err != nil {
		s.fail(w, "load events", err)
		return
	}
	occs, err := calendar.Expand(events, start, end)
	if err != nil {
		s.fail(w, "expand events", err)
		return
	}
	holidays := map[string]string{}
	for _, h := range holiday.Between(p.household.Region, start, end.AddDate(0, 0, -1)) {
		name := h.NameEN
		if s.langOf(r) == "de" {
			name = h.NameDE
		}
		holidays[h.Date.Format(dateLayout)] = name
	}
	days := make([]weekDay, 7)
	today := s.now().In(c.loc).Format(dateLayout)
	for i := range days {
		d := start.AddDate(0, 0, i)
		days[i] = weekDay{ISO: d.Format(dateLayout), Date: d.Format("02/01"), Name: calendarWeekdays[i], Holiday: holidays[d.Format(dateLayout)], Today: d.Format(dateLayout) == today}
	}
	for i, list := range calendar.WeekDays(occs, start) {
		for _, o := range list {
			days[i].Events = append(days[i].Events, c.item(o))
		}
	}
	var bars []weekBar
	for _, b := range calendar.WeekBars(occs, start) {
		it := c.item(b.Occurrence)
		bars = append(bars, weekBar{it, b.Col + 1, b.Span, b.Lane + 1, b.ContinuesBefore, b.ContinuesAfter})
		for d := b.Col; d < b.Col+b.Span; d++ {
			days[d].AllDay = append(days[d].AllDay, it)
		}
	}
	_, week := start.ISOWeek()
	s.render(w, r, http.StatusOK, "calendar.html", map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Days": days, "Bars": bars,
		"Week": week, "From": start.Format("02/01"), "To": end.AddDate(0, 0, -1).Format("02/01/2006"),
		"Prev": weekLink(start.AddDate(0, 0, -7)), "Next": weekLink(end), "ThisWeek": weekLink(calendar.StartOfWeek(s.now().In(c.loc))),
		"NewHref": "/calendar/new?date=" + start.Format(dateLayout), "Weekdays": calendarWeekdays,
	})
}

func (s *server) handleCalendarSearch(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	c, ok := s.calendarContext(w, r, p)
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var items []eventItem
	if q != "" {
		found, err := s.db.SearchEvents(p.household.ID, q)
		if err != nil {
			s.fail(w, "search", err)
			return
		}
		for _, e := range found {
			o := calendar.Occurrence{EventID: e.ID, SeriesID: e.SeriesID, RecurrenceID: e.RecurrenceID, CalendarID: e.CalendarID, Title: e.Title, Location: e.Location,
				AllDay: e.AllDay, Start: e.Start, End: e.End, People: e.People, Groups: e.Groups}
			it := c.item(o)
			it.Repeats = e.Recurring() || e.SeriesID != ""
			items = append(items, it)
		}
	}
	s.render(w, r, http.StatusOK, "calendar_search.html", map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Query": q, "Items": items, "Searched": q != "",
	})
}

// eventForm is what the event form shows and what a submission carries.
type eventForm struct {
	Title, Description, Location           string
	AllDay                                 bool
	StartDate, StartTime, EndDate, EndTime string
	TimeZone, Repeat, Until, Calendar      string
	People, Groups                         []string
	Scope, Occurrence                      string
}

func (f eventForm) values() map[string]any {
	return map[string]any{"Title": f.Title, "Description": f.Description, "Location": f.Location, "AllDay": f.AllDay,
		"StartDate": f.StartDate, "StartTime": f.StartTime, "EndDate": f.EndDate, "EndTime": f.EndTime, "TimeZone": f.TimeZone,
		"Repeat": f.Repeat, "Until": f.Until, "Calendar": f.Calendar, "People": f.People, "Groups": f.Groups, "Scope": f.Scope, "Occurrence": f.Occurrence}
}

func readEventForm(r *http.Request) eventForm {
	_ = r.ParseForm()
	v := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	return eventForm{Title: v("title"), Description: strings.TrimSpace(r.PostFormValue("description")), Location: v("location"),
		AllDay: r.PostFormValue("all_day") != "", StartDate: v("start_date"), StartTime: v("start_time"), EndDate: v("end_date"), EndTime: v("end_time"),
		TimeZone: v("timezone"), Repeat: v("repeat"), Until: v("until"), Calendar: v("calendar"),
		People: r.PostForm["people"], Groups: r.PostForm["groups"], Scope: v("scope"), Occurrence: v("occurrence")}
}

// eventError is a refused field of the form.
type eventError struct{ field, key string }

func (e *eventError) Error() string { return e.key }

// inputOf names the form input an error is shown at.
var inputOf = map[string]string{"start": "start_date", "end": "end_date", "invalid": ""}

func badField(field string) *eventError {
	input, ok := inputOf[field]
	if !ok {
		input = field
	}
	return &eventError{input, "calendar.error." + field}
}

// times turns the form's dates and times into the event's start and end in loc.
// All-day events end on the day after the last day (exclusive end).
func (f eventForm) times(loc *time.Location) (start, end time.Time, err error) {
	day, err := time.ParseInLocation(dateLayout, f.StartDate, loc)
	if err != nil {
		return start, end, badField("start")
	}
	endDay := day
	if f.EndDate != "" {
		if endDay, err = time.ParseInLocation(dateLayout, f.EndDate, loc); err != nil {
			return start, end, badField("end")
		}
	}
	if f.AllDay {
		if endDay.Before(day) {
			return start, end, badField("end")
		}
		return day, endDay.AddDate(0, 0, 1), nil
	}
	if start, err = time.ParseInLocation("2006-01-02 15:04", f.StartDate+" "+f.StartTime, loc); err != nil {
		return start, end, badField("start")
	}
	endTime := f.EndTime
	if endTime == "" {
		return start, end, badField("end")
	}
	if end, err = time.ParseInLocation("2006-01-02 15:04", endDay.Format(dateLayout)+" "+endTime, loc); err != nil || !end.After(start) {
		return start, end, badField("end")
	}
	return start, end, nil
}

// audience keeps the chosen people and groups that belong to the household;
// choosing nobody means the whole family.
func (c *calendarContext) audience(f eventForm) (people, groups []string) {
	for _, id := range f.People {
		if _, ok := c.chips[id]; ok && !slices.Contains(people, id) {
			people = append(people, id)
		}
	}
	for _, id := range f.Groups {
		if _, ok := c.groups[id]; ok && !slices.Contains(groups, id) {
			groups = append(groups, id)
		}
	}
	return people, groups
}

// untilOf is the last moment of the series' end date in loc; zero if none.
func (f eventForm) untilOf(loc *time.Location) (time.Time, error) {
	if f.Until == "" {
		return time.Time{}, nil
	}
	d, err := time.ParseInLocation(dateLayout, f.Until, loc)
	if err != nil {
		return time.Time{}, badField("until")
	}
	return d.AddDate(0, 0, 1).Add(-time.Second), nil
}

// choices are the people, groups, calendars and presets the form offers.
func (c *calendarContext) choices() map[string]any {
	var chips []personChip
	for _, chip := range c.chips {
		chips = append(chips, chip)
	}
	slices.SortFunc(chips, func(a, b personChip) int { return a.Slot - b.Slot })
	type choice struct{ ID, Name string }
	var groups []choice
	for id, name := range c.groups {
		groups = append(groups, choice{id, name})
	}
	slices.SortFunc(groups, func(a, b choice) int { return strings.Compare(a.Name, b.Name) })
	var writable []store.Calendar
	for _, cal := range c.calendars {
		if !cal.ReadOnly {
			writable = append(writable, cal)
		}
	}
	return map[string]any{"People": chips, "GroupChoices": groups, "Calendars": writable, "Repeats": calendar.Repeats, "Scopes": []calendar.Scope{calendar.ScopeThis, calendar.ScopeFollowing, calendar.ScopeAll}, "Zones": timeZoneChoices}
}

func (s *server) renderEventForm(w http.ResponseWriter, r *http.Request, c *calendarContext, status int, mode string, f eventForm, extra map[string]any, err *eventError) {
	data := c.choices()
	data["Person"], data["SyncStale"], data["Mode"], data["Form"] = c.p.person, s.syncStale(c.p.household.ID), mode, f.values()
	for k, v := range extra {
		data[k] = v
	}
	if err != nil {
		data["Error"], data["ErrorField"] = err.key, err.field
	}
	s.render(w, r, status, "calendar_event.html", data)
}

func (s *server) handleEventNew(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	c, ok := s.calendarContext(w, r, p)
	if !ok {
		return
	}
	day := s.now().In(c.loc)
	if d, err := time.ParseInLocation(dateLayout, r.URL.Query().Get("date"), c.loc); err == nil {
		day = d
	}
	fam, err := s.db.FamilyCalendar(p.household.ID)
	if err != nil {
		s.fail(w, "family calendar", err)
		return
	}
	f := eventForm{StartDate: day.Format(dateLayout), EndDate: day.Format(dateLayout), StartTime: "09:00", EndTime: "10:00",
		TimeZone: p.household.Timezone, Repeat: string(calendar.RepeatNone), Calendar: fam.ID}
	s.renderEventForm(w, r, c, http.StatusOK, "new", f, nil, nil)
}

func (s *server) handleEventCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	c, ok := s.calendarContext(w, r, p)
	if !ok {
		return
	}
	f := readEventForm(r)
	e, err := c.newEvent(&f)
	if err == nil {
		_, err = s.createEvent(p, e)
	}
	var bad *eventError
	switch {
	case err == nil:
		s.redirectSaved(w, r, weekLink(calendar.StartOfWeek(e.Start.In(c.loc))), "flash.event_created")
	case errors.As(err, &bad):
		s.renderEventForm(w, r, c, http.StatusUnprocessableEntity, "new", f, nil, bad)
	case errors.Is(err, store.ErrInvalid):
		s.renderEventForm(w, r, c, http.StatusUnprocessableEntity, "new", f, nil, badField("invalid"))
	default:
		s.fail(w, "create event", err)
	}
}

// newEvent validates a submitted form into a new event.
func (c *calendarContext) newEvent(f *eventForm) (calendar.Event, error) {
	var e calendar.Event
	if f.Title == "" {
		return e, badField("title")
	}
	zone := f.TimeZone
	if zone == "" {
		zone = c.p.household.Timezone
		f.TimeZone = zone
	}
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "Local" {
		return e, badField("timezone")
	}
	start, end, err := f.times(loc)
	if err != nil {
		return e, err
	}
	until, err := f.untilOf(loc)
	if err != nil {
		return e, err
	}
	if f.Repeat == "" {
		f.Repeat = string(calendar.RepeatNone)
	}
	rule, err := calendar.RepeatRule(calendar.Repeat(f.Repeat), until)
	if err != nil || f.Repeat == string(calendar.RepeatCustom) {
		return e, badField("repeat")
	}
	if !until.IsZero() && until.Before(start) {
		return e, badField("until")
	}
	if f.Calendar == "" {
		fam, err := c.s.db.FamilyCalendar(c.p.household.ID)
		if err != nil {
			return e, err
		}
		f.Calendar = fam.ID
	}
	if !c.writable(f.Calendar) {
		return e, badField("calendar")
	}
	e = calendar.Event{CalendarID: f.Calendar, Title: f.Title, Description: f.Description, Location: f.Location, AllDay: f.AllDay, Start: start, End: end, RRule: rule}
	e.People, e.Groups = c.audience(*f)
	return e, nil
}

// writable reports whether calendarID is a calendar of the household that
// accepts changes.
func (c *calendarContext) writable(calendarID string) bool {
	for _, cal := range c.calendars {
		if cal.ID == calendarID {
			return !cal.ReadOnly
		}
	}
	return false
}

func (s *server) createEvent(p principal, e calendar.Event) (calendar.Event, error) {
	return s.db.CreateEvent(p.household.ID, e)
}

// occurrenceFrom reads the original start of the occurrence a page is about.
func occurrenceFrom(raw string, loc *time.Location) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return t.In(loc), true
}

// eventTarget loads the series of an event page and the occurrence it is about.
func (s *server) eventTarget(w http.ResponseWriter, r *http.Request, p principal, raw string) ([]calendar.Event, calendar.Occurrence, bool) {
	series, err := s.db.Series(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return nil, calendar.Occurrence{}, false
	}
	if err != nil {
		s.fail(w, "load series", err)
		return nil, calendar.Occurrence{}, false
	}
	start := series[0].Start
	if raw != "" {
		var ok bool
		if start, ok = occurrenceFrom(raw, series[0].Start.Location()); !ok {
			http.NotFound(w, r)
			return nil, calendar.Occurrence{}, false
		}
	}
	occ, ok := calendar.OccurrenceOf(series, start)
	if !ok {
		http.NotFound(w, r)
		return nil, calendar.Occurrence{}, false
	}
	return series, occ, true
}

// formOf fills the form with an existing occurrence, in the event's own zone.
func formOf(series []calendar.Event, o calendar.Occurrence, occRaw string) eventForm {
	master := series[0]
	loc := master.Start.Location()
	start, end := o.Start.In(loc), o.End.In(loc)
	f := eventForm{Title: o.Title, Description: o.Description, Location: o.Location, AllDay: o.AllDay, TimeZone: loc.String(), Calendar: o.CalendarID,
		StartDate: start.Format(dateLayout), StartTime: start.Format("15:04"), EndDate: end.Format(dateLayout), EndTime: end.Format("15:04"),
		People: o.People, Groups: o.Groups, Scope: string(calendar.ScopeThis), Occurrence: occRaw}
	if o.AllDay {
		f.EndDate = end.AddDate(0, 0, -1).Format(dateLayout)
	}
	rep, until := calendar.RepeatOf(master.RRule)
	f.Repeat = string(rep)
	if !until.IsZero() {
		f.Until = until.In(loc).Format(dateLayout)
	}
	return f
}

func (s *server) handleEventPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	c, ok := s.calendarContext(w, r, p)
	if !ok {
		return
	}
	raw := r.URL.Query().Get("occurrence")
	series, occ, ok := s.eventTarget(w, r, p, raw)
	if !ok {
		return
	}
	if raw == "" {
		raw = occ.OriginalStart().Format(time.RFC3339)
	}
	s.renderEventPage(w, r, c, http.StatusOK, series, occ, formOf(series, occ, raw), nil)
}

func (s *server) renderEventPage(w http.ResponseWriter, r *http.Request, c *calendarContext, status int, series []calendar.Event, occ calendar.Occurrence, f eventForm, bad *eventError) {
	master := series[0]
	f.AllDay = occ.AllDay
	extra := map[string]any{"EventID": r.PathValue("id"), "Item": c.item(occ), "Recurring": master.Recurring(), "ReadOnly": c.readOnly[occ.CalendarID],
		"CalendarName": c.calendarName(occ.CalendarID), "Zone": master.Start.Location().String(), "CurrentRepeat": f.Repeat}
	s.renderEventForm(w, r, c, status, "edit", f, extra, bad)
}

func (c *calendarContext) calendarName(id string) string {
	for _, cal := range c.calendars {
		if cal.ID == id {
			return cal.Name
		}
	}
	return ""
}

func (s *server) handleEventEdit(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	c, ok := s.calendarContext(w, r, p)
	if !ok {
		return
	}
	f := readEventForm(r)
	series, occ, ok := s.eventTarget(w, r, p, f.Occurrence)
	if !ok {
		return
	}
	if c.readOnly[occ.CalendarID] {
		http.Error(w, "this is a read-only calendar: its events are changed at their source", http.StatusUnprocessableEntity)
		return
	}
	master := series[0]
	scope := calendar.Scope(f.Scope)
	if !master.Recurring() {
		scope = calendar.ScopeAll
	}
	if !scope.Valid() {
		scope = calendar.ScopeThis
	}
	patch, err := c.patch(series, occ, f, scope)
	var bad *eventError
	if err == nil {
		var ch calendar.Change
		if ch, err = (calendar.Editor{NewID: store.NewID}).Edit(series, occ.OriginalStart(), scope, patch); err == nil {
			err = s.db.ApplyChange(p.household.ID, ch)
		}
	}
	switch {
	case err == nil:
		s.redirectSaved(w, r, weekLink(calendar.StartOfWeek(patchStart(patch, occ).In(c.loc))), "flash.event_saved")
	case errors.As(err, &bad):
		s.renderEventPage(w, r, c, http.StatusUnprocessableEntity, series, occ, f, bad)
	case errors.Is(err, calendar.ErrNoSuchOccurrence):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrInvalid):
		s.renderEventPage(w, r, c, http.StatusUnprocessableEntity, series, occ, f, badField("invalid"))
	default:
		s.fail(w, "update event", err)
	}
}

func patchStart(p calendar.Patch, o calendar.Occurrence) time.Time {
	if p.Start != nil {
		return *p.Start
	}
	return o.Start
}

// patch turns a submitted form into the changes to an occurrence. The zone of
// an event is not changed by an edit. The repeat is only part of the patch if
// it was changed and the edit reaches the series.
func (c *calendarContext) patch(series []calendar.Event, occ calendar.Occurrence, f eventForm, scope calendar.Scope) (calendar.Patch, error) {
	var p calendar.Patch
	if f.Title == "" {
		return p, badField("title")
	}
	master := series[0]
	loc := master.Start.Location()
	f.AllDay = occ.AllDay
	start, end, err := f.times(loc)
	if err != nil {
		return p, err
	}
	people, groups := c.audience(f)
	if people == nil {
		people = []string{}
	}
	if groups == nil {
		groups = []string{}
	}
	p = calendar.Patch{Title: &f.Title, Description: &f.Description, Location: &f.Location, Start: &start, End: &end, People: &people, Groups: &groups}
	if scope == calendar.ScopeThis || f.Repeat == "" || f.Repeat == string(calendar.RepeatCustom) {
		return p, nil
	}
	curRepeat, curUntil := calendar.RepeatOf(master.RRule)
	// The form shows the end of the series as a date. A series cut by "this and
	// following" ends a second before the cut, so only a changed date counts as
	// a new end: otherwise the cut-off occurrences would come back.
	untouchedEnd := f.Until == "" && curUntil.IsZero() || !curUntil.IsZero() && f.Until == curUntil.In(loc).Format(dateLayout)
	if string(curRepeat) == f.Repeat && untouchedEnd {
		return p, nil
	}
	until, err := f.untilOf(loc)
	if err != nil {
		return p, err
	}
	if untouchedEnd {
		until = curUntil
	}
	rule, err := calendar.RepeatRule(calendar.Repeat(f.Repeat), until)
	if err != nil {
		return p, badField("repeat")
	}
	if !until.IsZero() && until.Before(start) {
		return p, badField("until")
	}
	p.RRule = &rule
	return p, nil
}

func (s *server) handleEventDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	c, ok := s.calendarContext(w, r, p)
	if !ok {
		return
	}
	_ = r.ParseForm()
	series, occ, ok := s.eventTarget(w, r, p, r.PostFormValue("occurrence"))
	if !ok {
		return
	}
	if c.readOnly[occ.CalendarID] {
		http.Error(w, "this is a read-only calendar: its events are changed at their source", http.StatusUnprocessableEntity)
		return
	}
	scope := calendar.Scope(r.PostFormValue("scope"))
	if !scope.Valid() {
		scope = calendar.ScopeThis
	}
	if !series[0].Recurring() {
		scope = calendar.ScopeAll
	}
	ch, err := calendar.Editor{NewID: store.NewID}.Delete(series, occ.OriginalStart(), scope)
	if err == nil {
		err = s.db.ApplyChange(p.household.ID, ch)
	}
	switch {
	case err == nil:
		s.redirectSaved(w, r, weekLink(calendar.StartOfWeek(occ.Start.In(c.loc))), "flash.event_deleted")
	case errors.Is(err, calendar.ErrNoSuchOccurrence):
		http.NotFound(w, r)
	default:
		s.fail(w, "delete event", err)
	}
}
