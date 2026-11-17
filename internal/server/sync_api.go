package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/icsfeed"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/store"
)

const (
	defaultSyncMinutes = 60
	minSyncMinutes     = 5
	maxSyncMinutes     = 24 * 60
)

// calendarColors is the fixed set a calendar's colour is picked from (the
// muted person colours of the design, ADR 0017).
var calendarColors = []string{"#2F5E7A", "#B4583A", "#4E7D52", "#9A7420", "#86587F", "#3A7A74"}

// nextColors picks n colours for new calendars: first those no existing
// calendar uses, then the set again from its start.
func nextColors(taken []string, n int) []string {
	used := map[string]int{}
	for _, c := range taken {
		used[strings.ToUpper(c)]++
	}
	out := make([]string, 0, n)
	for len(out) < n {
		best := calendarColors[0]
		for _, c := range calendarColors {
			if used[strings.ToUpper(c)] < used[strings.ToUpper(best)] {
				best = c
			}
		}
		used[strings.ToUpper(best)]++
		out = append(out, best)
	}
	return out
}

// takenColors lists the colours of a household's synced calendars.
func (s *server) takenColors(householdID string) ([]string, error) {
	cals, err := s.db.SyncedCalendars(householdID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range cals {
		out = append(out, c.Color)
	}
	return out, nil
}

var hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

type syncJSON struct {
	State           calsync.State `json:"state"`
	IntervalMinutes int           `json:"interval_minutes"`
	LastAttemptAt   *string       `json:"last_attempt_at"`
	LastSuccessAt   *string       `json:"last_success_at"`
	LastError       string        `json:"last_error"`
	// EventCount is how many events the calendar holds after the last
	// successful run (a series counts once); LastRun says what that run did.
	EventCount int     `json:"event_count"`
	LastRun    runJSON `json:"last_run"`
}

type runJSON struct {
	New     int `json:"new"`
	Changed int `json:"changed"`
	Removed int `json:"removed"`
}

type calendarJSON struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Kind     string       `json:"kind"`
	Color    string       `json:"color"`
	ReadOnly bool         `json:"read_only"`
	Shared   bool         `json:"share_through_box"`
	Waste    bool         `json:"waste_collection"`
	Audience audienceJSON `json:"audience"`
	Sync     *syncJSON    `json:"sync,omitempty"`
}

type audienceJSON struct {
	People []string `json:"people"`
	Groups []string `json:"groups"`
}

// langOf is the language of text a handler builds for a web page or its
// scripts: the same as the page itself (pageLanguage), so a switch changes it
// at once.
func (s *server) langOf(r *http.Request) string {
	return s.pageLanguage(r)
}

// displayLang is the language of a display: the browser's (Accept-Language,
// else German), never a parent's choice.
func (s *server) displayLang(r *http.Request) string {
	return s.i18n.Match(r.Header.Get("Accept-Language"))
}

// say translates a stored message for the request's language.
func (s *server) say(r *http.Request, m i18n.Message) string {
	return s.i18n.Format(s.langOf(r), m)
}

// title shows an event title; untitled feed events get a localised placeholder.
func (s *server) title(r *http.Request, t string) string {
	if strings.TrimSpace(t) == "" {
		return s.i18n.T(s.langOf(r), "event.untitled")
	}
	return t
}

func (s *server) syncState(r *http.Request, c store.SyncedCalendar, loc *time.Location) *syncJSON {
	ts := func(t time.Time) *string {
		if t.IsZero() {
			return nil
		}
		v := t.In(loc).Format(time.RFC3339)
		return &v
	}
	return &syncJSON{
		State:           calsync.StateAt(s.now(), c.Sync.StartedAt, c.Sync.LastSuccess),
		IntervalMinutes: c.Sync.IntervalMinutes,
		LastAttemptAt:   ts(c.Sync.LastAttempt),
		LastSuccessAt:   ts(c.Sync.LastSuccess),
		LastError:       s.say(r, c.Sync.LastError),
		EventCount:      c.Sync.EventCount,
		LastRun:         runJSON{c.Sync.LastRun.New, c.Sync.LastRun.Changed, c.Sync.LastRun.Removed},
	}
}

func plainCalendarJSON(c store.Calendar) calendarJSON {
	return calendarJSON{ID: c.ID, Name: c.Name, Kind: c.Kind, Color: c.Color, ReadOnly: c.ReadOnly, Shared: c.Shared, Waste: c.Waste,
		Audience: audienceJSON{nonNil(c.Audience.People), nonNil(c.Audience.Groups)}}
}

func (s *server) apiListCalendars(w http.ResponseWriter, r *http.Request, p principal) {
	if err := s.db.EnsureFamilyCalendar(p.household.ID); err != nil {
		s.fail(w, "family calendar", err)
		return
	}
	cals, err := s.db.Calendars(p.household.ID)
	if err != nil {
		s.fail(w, "list calendars", err)
		return
	}
	synced, err := s.db.SyncedCalendars(p.household.ID)
	if err != nil {
		s.fail(w, "list synced calendars", err)
		return
	}
	state := map[string]*syncJSON{}
	for _, c := range synced {
		state[c.ID] = s.syncState(r, c, p.location())
	}
	out := make([]calendarJSON, 0, len(cals))
	for _, c := range cals {
		j := plainCalendarJSON(c)
		j.Sync = state[c.ID]
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, out)
}

type newCalendarInput struct {
	Kind            string        `json:"kind"`
	Name            string        `json:"name"`
	URL             string        `json:"url"`
	Color           string        `json:"color"`
	Audience        *audienceJSON `json:"audience"`
	IntervalMinutes *int          `json:"sync_interval_minutes"`
}

// inputError is something the person adding a calendar can fix. Field names
// the form field ("name", "url", "interval", "audience", "color", "kind").
type inputError struct{ Field, Msg string }

func (e *inputError) Error() string { return e.Msg }

// addICS validates a new ICS subscription, stores it and runs its first sync.
// The error is an *inputError for bad input, anything else is a server fault.
func (s *server) addICS(r *http.Request, p principal, in newCalendarInput) (store.SyncedCalendar, error) {
	if in.Kind != "ics" {
		return store.SyncedCalendar{}, &inputError{"kind", "kind must be ics"}
	}
	if strings.TrimSpace(in.Name) == "" {
		return store.SyncedCalendar{}, &inputError{"name", "name is required"}
	}
	address, err := icsfeed.NormalizeURL(in.URL)
	if err != nil {
		return store.SyncedCalendar{}, &inputError{"url", "url: " + err.Error()}
	}
	opts, err := s.calendarOptions(p, in, defaultSyncMinutes)
	if err != nil {
		return store.SyncedCalendar{}, err
	}
	cal, err := s.db.CreateICSCalendar(p.household.ID, store.NewICS{Name: in.Name, URL: address, Color: opts.color, Audience: opts.audience, IntervalMinutes: opts.interval, Now: s.now()})
	if err != nil {
		return store.SyncedCalendar{}, err
	}
	// The first sync runs now. If it fails the calendar stays and the failure
	// is in the sync log; the scheduler retries.
	_ = s.sync.Sync(r.Context(), p.household.ID, cal.ID)
	return s.db.SyncedCalendar(p.household.ID, cal.ID)
}

// calendarSettings are the colour, audience and update interval a parent
// chooses for a synced calendar.
type calendarSettings struct {
	color    string
	audience calendar.Audience
	interval int
}

// calendarOptions checks and completes those choices. The error is an
// *inputError.
func (s *server) calendarOptions(p principal, in newCalendarInput, defaultMinutes int) (calendarSettings, error) {
	out := calendarSettings{interval: defaultMinutes}
	if in.IntervalMinutes != nil {
		out.interval = *in.IntervalMinutes
	}
	if out.interval < minSyncMinutes || out.interval > maxSyncMinutes {
		return out, &inputError{"interval", "sync_interval_minutes must be between 5 and 1440"}
	}
	if in.Audience != nil {
		out.audience = calendar.Audience{People: in.Audience.People, Groups: in.Audience.Groups}
	}
	ok, err := s.audienceExists(p.household.ID, out.audience)
	if err != nil {
		return out, err
	}
	if !ok {
		return out, &inputError{"audience", "audience: unknown person or group"}
	}
	out.color = strings.TrimSpace(in.Color)
	if out.color == "" {
		taken, err := s.takenColors(p.household.ID)
		if err != nil {
			return out, err
		}
		out.color = nextColors(taken, 1)[0]
	}
	if !hexColor.MatchString(out.color) {
		return out, &inputError{"color", "color must be #RRGGBB"}
	}
	return out, nil
}

func (s *server) audienceExists(householdID string, a calendar.Audience) (bool, error) {
	people, err := s.db.People(householdID)
	if err != nil {
		return false, err
	}
	groups, err := s.db.Groups(householdID)
	if err != nil {
		return false, err
	}
	known := map[string]bool{}
	for _, x := range people {
		known[x.ID] = true
	}
	for _, x := range groups {
		known[x.ID] = true
	}
	for _, id := range append(append([]string{}, a.People...), a.Groups...) {
		if !known[id] {
			return false, nil
		}
	}
	return true, nil
}

func (s *server) apiAddCalendar(w http.ResponseWriter, r *http.Request, p principal) {
	var in newCalendarInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if in.Kind == "ics" && s.refuseOff(w, outbound.ICS) {
		return
	}
	cal, err := s.addICS(r, p, in)
	var bad *inputError
	switch {
	case errors.As(err, &bad):
		writeError(w, http.StatusUnprocessableEntity, bad.Msg)
		return
	case err != nil:
		s.fail(w, "add ICS calendar", err)
		return
	}
	j := plainCalendarJSON(cal.Calendar)
	j.Sync = s.syncState(r, cal, p.location())
	writeJSON(w, http.StatusCreated, j)
}

func (s *server) apiSyncCalendar(w http.ResponseWriter, r *http.Request, p principal) {
	err := s.sync.Sync(r.Context(), p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "synced calendar not found")
		return
	}
	var refused *outbound.RefusedError
	if errors.As(err, &refused) { // paused: not a failure
		s.refuseOff(w, refused.Service)
		return
	}
	// Any other error is a sync failure; it is in the log and in the state below.
	cal, err := s.db.SyncedCalendar(p.household.ID, r.PathValue("id"))
	if s.storeError(w, "reload synced calendar", err) {
		return
	}
	j := plainCalendarJSON(cal.Calendar)
	j.Sync = s.syncState(r, cal, p.location())
	writeJSON(w, http.StatusOK, j)
}

// calendarChanges are the fields of a synced calendar a parent may change;
// nil leaves a field as it is.
type calendarChanges struct {
	Name     *string       `json:"name"`
	Color    *string       `json:"color"`
	Audience *audienceJSON `json:"audience"`
}

func (c calendarChanges) any() bool { return c.Name != nil || c.Color != nil || c.Audience != nil }

// changeSyncedCalendar checks and stores new name, colour and audience of a
// synced calendar. The error is an *inputError for bad input, store.ErrNotFound
// for an unknown calendar.
func (s *server) changeSyncedCalendar(p principal, cal store.SyncedCalendar, in calendarChanges) error {
	name, color, audience := cal.Name, cal.Color, cal.Audience
	if in.Name != nil {
		if name = strings.TrimSpace(*in.Name); name == "" {
			return &inputError{"name", "name is required"}
		}
	}
	if in.Color != nil {
		if color = strings.TrimSpace(*in.Color); !hexColor.MatchString(color) {
			return &inputError{"color", "color must be #RRGGBB"}
		}
	}
	if in.Audience != nil {
		audience = calendar.Audience{People: in.Audience.People, Groups: in.Audience.Groups}
		ok, err := s.audienceExists(p.household.ID, audience)
		if err != nil {
			return err
		}
		if !ok {
			return &inputError{"audience", "audience: unknown person or group"}
		}
	}
	return s.db.UpdateSyncedCalendar(p.household.ID, cal.ID, name, color, audience)
}

// calendarExists is nil when the household has a calendar with this id.
func (s *server) calendarExists(householdID, id string) error {
	_, err := s.db.Calendar(householdID, id)
	return err
}

func (s *server) apiUpdateCalendar(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Shared   *bool `json:"share_through_box"`
		ReadOnly *bool `json:"read_only"`
		Waste    *bool `json:"waste_collection"`
		calendarChanges
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if in.Shared == nil && in.ReadOnly == nil && in.Waste == nil && !in.calendarChanges.any() {
		writeError(w, http.StatusUnprocessableEntity, "send share_through_box, read_only, waste_collection, name, color or audience")
		return
	}
	id := r.PathValue("id")
	if in.Waste != nil && *in.Waste { // refuse before anything is saved; unmarking needs no read-only
		c, err := s.db.Calendar(p.household.ID, id)
		if s.storeError(w, "waste calendar", err) {
			return
		}
		readOnly := c.ReadOnly // as it will be after this request
		if in.ReadOnly != nil {
			readOnly = *in.ReadOnly
		}
		if !readOnly {
			writeError(w, http.StatusUnprocessableEntity, "only a read-only calendar can be a waste collection calendar")
			return
		}
	}
	if in.calendarChanges.any() {
		cal, err := s.db.SyncedCalendar(p.household.ID, id)
		if errors.Is(err, store.ErrNotFound) {
			if s.storeError(w, "calendar", s.calendarExists(p.household.ID, id)) {
				return
			}
			writeError(w, http.StatusUnprocessableEntity, "name, color and audience can only be changed on a synced calendar")
			return
		} else if s.storeError(w, "synced calendar", err) {
			return
		}
		var bad *inputError
		switch err := s.changeSyncedCalendar(p, cal, in.calendarChanges); {
		case errors.As(err, &bad):
			writeError(w, http.StatusUnprocessableEntity, bad.Msg)
			return
		case err != nil:
			s.fail(w, "change calendar", err)
			return
		}
	}
	if in.Shared != nil && s.storeError(w, "share calendar", s.db.SetCalendarShared(p.household.ID, id, *in.Shared)) {
		return
	}
	if in.ReadOnly != nil {
		if s.storeError(w, "switch calendar", s.db.SetCalendarReadOnly(p.household.ID, id, *in.ReadOnly)) {
			return
		}
		if !*in.ReadOnly {
			s.sync.PushSoon(p.household.ID, id) // changes queued before the switch
		}
	}
	if in.Waste != nil && s.storeError(w, "waste calendar", s.db.SetCalendarWaste(p.household.ID, id, *in.Waste)) {
		return
	}
	c, err := s.db.Calendar(p.household.ID, id)
	if err != nil {
		s.fail(w, "reload calendar", err)
		return
	}
	j := plainCalendarJSON(c)
	if synced, err := s.db.SyncedCalendar(p.household.ID, id); err == nil {
		j.Sync = s.syncState(r, synced, p.location())
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *server) apiDeleteCalendar(w http.ResponseWriter, r *http.Request, p principal) {
	if s.storeError(w, "delete synced calendar", s.db.DeleteSyncedCalendar(p.household.ID, r.PathValue("id"))) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type syncLogJSON struct {
	Detail       string `json:"detail,omitempty"`
	ID           string `json:"id"`
	CalendarID   string `json:"calendar_id"`
	CalendarName string `json:"calendar_name"`
	At           string `json:"at"`
	Kind         string `json:"kind"`
	Message      string `json:"message"`
}

func (s *server) apiSyncLog(w http.ResponseWriter, r *http.Request, p principal) {
	entries, err := s.db.SyncLog(p.household.ID, 200)
	if err != nil {
		s.fail(w, "sync log", err)
		return
	}
	out := make([]syncLogJSON, 0, len(entries))
	for _, e := range entries {
		out = append(out, syncLogJSON{e.Detail, e.ID, e.CalendarID, e.CalendarName, e.At.In(p.location()).Format(time.RFC3339), e.Kind, s.say(r, e.Message)})
	}
	writeJSON(w, http.StatusOK, out)
}

// readOnly reports whether a calendar of the household is read-only.
func (s *server) readOnly(householdID, calendarID string) (bool, error) {
	cals, err := s.db.Calendars(householdID)
	if err != nil {
		return false, err
	}
	for _, c := range cals {
		if c.ID == calendarID {
			return c.ReadOnly, nil
		}
	}
	return false, nil
}

// refuseReadOnly answers 422 and returns true if the calendar is read-only.
func (s *server) refuseReadOnly(w http.ResponseWriter, householdID, calendarID string) bool {
	ro, err := s.readOnly(householdID, calendarID)
	if err != nil {
		s.fail(w, "calendar lookup", err)
		return true
	}
	if ro {
		writeError(w, http.StatusUnprocessableEntity, "this is a read-only calendar: its events are changed at their source")
	}
	return ro
}
