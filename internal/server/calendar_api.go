package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/holiday"
	"github.com/hejmo-home/hejmo/internal/store"
)

const dateLayout = "2006-01-02"

func (p principal) location() *time.Location {
	if loc, err := time.LoadLocation(p.household.Timezone); err == nil {
		return loc
	}
	return time.UTC
}

// timeLayout is how the household wants date and time shown in the web
// interface (ADR 0016).
func (p principal) timeLayout() string {
	if p.household.TimeFormat == store.Time12h {
		return "02/01/2006 3:04 PM"
	}
	return "02/01/2006 15:04"
}

// syncLayout is timeLayout with seconds: the times of sync runs and sync
// problems, where "21:46:26" tells which run it was (ticket 5.17).
func (p principal) syncLayout() string {
	if p.household.TimeFormat == store.Time12h {
		return "02/01/2006 3:04:05 PM"
	}
	return "02/01/2006 15:04:05"
}

// clockLayout is the time of day alone, with seconds, in the household's format.
func (p principal) clockLayout() string {
	if p.household.TimeFormat == store.Time12h {
		return "3:04:05 PM"
	}
	return "15:04:05"
}

func formatTime(t time.Time, allDay bool, loc *time.Location) string {
	if allDay {
		return t.In(loc).Format(dateLayout)
	}
	return t.In(loc).Format(time.RFC3339)
}

func parseTime(s string, allDay bool, loc *time.Location) (time.Time, error) {
	if allDay {
		return time.ParseInLocation(dateLayout, s, loc)
	}
	t, err := time.Parse(time.RFC3339, s)
	return t.In(loc), err
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

type eventJSON struct {
	ID          string   `json:"id"`
	CalendarID  string   `json:"calendar_id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Location    string   `json:"location"`
	AllDay      bool     `json:"all_day"`
	Start       string   `json:"start"`
	End         string   `json:"end"`
	RRule       string   `json:"rrule"`
	People      []string `json:"people"`
	Groups      []string `json:"groups"`
}

func (s *server) eventToJSON(r *http.Request, e calendar.Event, loc *time.Location) eventJSON {
	return eventJSON{e.ID, e.CalendarID, s.title(r, e.Title), e.Description, e.Location, e.AllDay,
		formatTime(e.Start, e.AllDay, loc), formatTime(e.End, e.AllDay, loc), e.RRule, nonNil(e.People), nonNil(e.Groups)}
}

type occurrenceJSON struct {
	EventID      string   `json:"event_id"`
	SeriesID     string   `json:"series_id"`
	RecurrenceID string   `json:"recurrence_id"`
	CalendarID   string   `json:"calendar_id"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Location     string   `json:"location"`
	AllDay       bool     `json:"all_day"`
	Start        string   `json:"start"`
	End          string   `json:"end"`
	People       []string `json:"people"`
	Groups       []string `json:"groups"`
}

func (s *server) apiListEvents(w http.ResponseWriter, r *http.Request, p principal) {
	loc := p.location()
	from, err1 := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	to, err2 := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
	if err1 != nil || err2 != nil || !to.After(from) || to.Sub(from) > 400*24*time.Hour {
		writeError(w, http.StatusUnprocessableEntity, "from and to must be RFC 3339 times, at most 400 days apart")
		return
	}
	events, err := s.db.EventsBetween(p.household.ID, from.In(loc), to.In(loc))
	if err != nil {
		s.fail(w, "load events", err)
		return
	}
	occs, err := calendar.Expand(events, from, to)
	if err != nil {
		s.fail(w, "expand events", err)
		return
	}
	if person := r.URL.Query().Get("person"); person != "" {
		if occs, err = s.forPerson(p.household.ID, person, occs); err != nil {
			s.fail(w, "filter events", err)
			return
		}
	}
	out := make([]occurrenceJSON, 0, len(occs))
	for _, o := range occs {
		rec := ""
		if o.SeriesID != "" {
			rec = formatTime(o.RecurrenceID, false, loc)
		}
		out = append(out, occurrenceJSON{o.EventID, o.SeriesID, rec, o.CalendarID, s.title(r, o.Title), o.Description, o.Location, o.AllDay,
			formatTime(o.Start, o.AllDay, loc), formatTime(o.End, o.AllDay, loc), nonNil(o.People), nonNil(o.Groups)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) forPerson(householdID, personID string, occs []calendar.Occurrence) ([]calendar.Occurrence, error) {
	groups, err := s.db.PersonGroups(personID)
	if err != nil {
		return nil, err
	}
	cals, err := s.db.Calendars(householdID)
	if err != nil {
		return nil, err
	}
	audiences := map[string]calendar.Audience{}
	for _, c := range cals {
		audiences[c.ID] = c.Audience
	}
	return calendar.Visible(occs, audiences, calendar.Viewer{PersonID: personID, Groups: groups}), nil
}

type eventInput struct {
	CalendarID  *string   `json:"calendar_id"`
	Title       *string   `json:"title"`
	Description *string   `json:"description"`
	Location    *string   `json:"location"`
	AllDay      *bool     `json:"all_day"`
	Start       *string   `json:"start"`
	End         *string   `json:"end"`
	RRule       *string   `json:"rrule"`
	People      *[]string `json:"people"`
	Groups      *[]string `json:"groups"`
	TimeZone    *string   `json:"timezone"` // create only
}

func readEventInput(r *http.Request) (eventInput, error) {
	var in eventInput
	err := json.NewDecoder(r.Body).Decode(&in)
	return in, err
}

func (s *server) apiCreateEvent(w http.ResponseWriter, r *http.Request, p principal) {
	in, err := readEventInput(r)
	if err != nil || in.Title == nil || in.Start == nil || in.End == nil {
		writeError(w, http.StatusBadRequest, "title, start and end are required")
		return
	}
	loc := p.location()
	if in.TimeZone != nil && *in.TimeZone != "" {
		zone, err := time.LoadLocation(*in.TimeZone)
		if err != nil || *in.TimeZone == "Local" {
			writeError(w, http.StatusUnprocessableEntity, "timezone: use an IANA time zone name such as Europe/Berlin")
			return
		}
		loc = zone
	}
	e := calendar.Event{Title: *in.Title, AllDay: in.AllDay != nil && *in.AllDay}
	if e.Start, err = parseTime(*in.Start, e.AllDay, loc); err == nil {
		e.End, err = parseTime(*in.End, e.AllDay, loc)
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "start/end: use RFC 3339, or YYYY-MM-DD for all-day events")
		return
	}
	for dst, src := range map[*string]*string{&e.Description: in.Description, &e.Location: in.Location, &e.RRule: in.RRule, &e.CalendarID: in.CalendarID} {
		if src != nil {
			*dst = *src
		}
	}
	if in.People != nil {
		e.People = *in.People
	}
	if in.Groups != nil {
		e.Groups = *in.Groups
	}
	if e.RRule != "" {
		if err := calendar.ValidateRRule(e.RRule); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "rrule: "+err.Error())
			return
		}
	}
	if e.CalendarID == "" {
		cal, err := s.db.FamilyCalendar(p.household.ID)
		if err != nil {
			s.fail(w, "family calendar", err)
			return
		}
		e.CalendarID = cal.ID
	}
	if s.refuseReadOnly(w, p.household.ID, e.CalendarID) {
		return
	}
	created, err := s.db.CreateEvent(p.household.ID, e)
	if s.storeError(w, "create event", err) {
		return
	}
	writeJSON(w, http.StatusCreated, s.eventToJSON(r, created, p.location()))
}

// seriesTarget resolves an event id plus ?occurrence= and ?scope=.
func (s *server) seriesTarget(w http.ResponseWriter, r *http.Request, p principal) ([]calendar.Event, time.Time, calendar.Scope, bool) {
	series, err := s.db.Series(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "event not found")
		return nil, time.Time{}, "", false
	}
	if err != nil {
		s.fail(w, "load series", err)
		return nil, time.Time{}, "", false
	}
	scope := calendar.Scope(r.URL.Query().Get("scope"))
	if scope == "" {
		scope = calendar.ScopeThis
	}
	if !scope.Valid() {
		writeError(w, http.StatusUnprocessableEntity, "scope must be this, following or all")
		return nil, time.Time{}, "", false
	}
	occ := series[0].Start
	if q := r.URL.Query().Get("occurrence"); q != "" {
		t, err := parseTime(q, series[0].AllDay && len(q) == len(dateLayout), p.location())
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "occurrence must be an RFC 3339 time")
			return nil, time.Time{}, "", false
		}
		occ = t.In(series[0].Start.Location())
	}
	return series, occ, scope, true
}

func (s *server) apiUpdateEvent(w http.ResponseWriter, r *http.Request, p principal) {
	series, occ, scope, ok := s.seriesTarget(w, r, p)
	if !ok || s.refuseReadOnly(w, p.household.ID, series[0].CalendarID) {
		return
	}
	in, err := readEventInput(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if in.TimeZone != nil {
		writeError(w, http.StatusUnprocessableEntity, "timezone: the time zone of an event cannot be changed")
		return
	}
	allDay := series[0].AllDay
	loc := series[0].Start.Location()
	patch := calendar.Patch{Title: in.Title, Description: in.Description, Location: in.Location, RRule: in.RRule, People: in.People, Groups: in.Groups}
	for dst, src := range map[**time.Time]*string{&patch.Start: in.Start, &patch.End: in.End} {
		if src == nil {
			continue
		}
		t, err := parseTime(*src, allDay, loc)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "start/end: use RFC 3339, or YYYY-MM-DD for all-day events")
			return
		}
		*dst = &t
	}
	if in.RRule != nil && *in.RRule != "" {
		if err := calendar.ValidateRRule(*in.RRule); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "rrule: "+err.Error())
			return
		}
	}
	ch, err := calendar.Editor{NewID: store.NewID}.Edit(series, occ, scope, patch)
	if s.calendarError(w, err) {
		return
	}
	if s.storeError(w, "update event", s.db.ApplyChange(p.household.ID, ch)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) apiDeleteEvent(w http.ResponseWriter, r *http.Request, p principal) {
	series, occ, scope, ok := s.seriesTarget(w, r, p)
	if !ok || s.refuseReadOnly(w, p.household.ID, series[0].CalendarID) {
		return
	}
	ch, err := calendar.Editor{NewID: store.NewID}.Delete(series, occ, scope)
	if s.calendarError(w, err) {
		return
	}
	if s.storeError(w, "delete event", s.db.ApplyChange(p.household.ID, ch)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) calendarError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, calendar.ErrNoSuchOccurrence):
		writeError(w, http.StatusNotFound, "no such occurrence")
	default:
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	}
	return true
}

func (s *server) storeError(w http.ResponseWriter, what string, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		s.fail(w, what, err)
	}
	return true
}

func (s *server) apiSearch(w http.ResponseWriter, r *http.Request, p principal) {
	events, err := s.db.SearchEvents(p.household.ID, r.URL.Query().Get("q"))
	if err != nil {
		s.fail(w, "search", err)
		return
	}
	out := make([]eventJSON, 0, len(events))
	for _, e := range events {
		out = append(out, s.eventToJSON(r, e, p.location()))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiHolidays(w http.ResponseWriter, r *http.Request, p principal) {
	from, err1 := time.Parse(dateLayout, r.URL.Query().Get("from"))
	to, err2 := time.Parse(dateLayout, r.URL.Query().Get("to"))
	if err1 != nil || err2 != nil || to.Sub(from) > 3*366*24*time.Hour {
		writeError(w, http.StatusUnprocessableEntity, "from and to must be dates (YYYY-MM-DD), at most three years apart")
		return
	}
	type holidayJSON struct {
		Date    string `json:"date"`
		Name    string `json:"name"`
		NameEN  string `json:"name_en"`
		Partial bool   `json:"partial"`
	}
	out := []holidayJSON{}
	for _, h := range holiday.Between(p.household.Region, from, to) {
		out = append(out, holidayJSON{h.Date.Format(dateLayout), h.NameDE, h.NameEN, h.Partial})
	}
	writeJSON(w, http.StatusOK, out)
}

type groupJSON struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

func (s *server) apiListGroups(w http.ResponseWriter, _ *http.Request, p principal) {
	groups, err := s.db.Groups(p.household.ID)
	if err != nil {
		s.fail(w, "list groups", err)
		return
	}
	out := make([]groupJSON, 0, len(groups))
	for _, g := range groups {
		out = append(out, groupJSON{g.ID, g.Name, nonNil(g.Members)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiCreateGroup(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Name    string   `json:"name"`
		Members []string `json:"members"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeError(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.Name)) > maxPersonName {
		writeError(w, http.StatusUnprocessableEntity, "name is too long")
		return
	}
	g, err := s.db.CreateGroup(p.household.ID, in.Name, in.Members)
	if err != nil {
		s.fail(w, "create group", err)
		return
	}
	groups, _ := s.db.Groups(p.household.ID)
	for _, x := range groups {
		if x.ID == g.ID {
			g = x
		}
	}
	writeJSON(w, http.StatusCreated, groupJSON{g.ID, g.Name, nonNil(g.Members)})
}
