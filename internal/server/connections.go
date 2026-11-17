package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/googleauth"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/store"
)

const (
	iCloudServer       = "https://caldav.icloud.com"
	defaultCalDAVSync  = 5
	discoverTimeoutSec = 45
)

type connectionJSON struct {
	ID        string           `json:"id"`
	Kind      string           `json:"kind"`
	URL       string           `json:"url"`
	Username  string           `json:"username"`
	Single    bool             `json:"single_calendar"`
	Calendars []discoveredJSON `json:"calendars,omitempty"`
	// Google only: Google refuses the stored sign-in since this time, so the connection must sign in again.
	NeedsSignIn    bool       `json:"needs_signin,omitempty"`
	SignInNeededAt *time.Time `json:"signin_needed_at,omitempty"`
}

type discoveredJSON struct {
	URL   string `json:"url"`
	Name  string `json:"name"`
	Added bool   `json:"added"`
	// CalendarID is the id of the synced calendar when Added: the calendar
	// to change or to switch off with /calendars/{id}.
	CalendarID string `json:"calendar_id,omitempty"`
	// Google only: whether the box may change the calendar ("read_write") or only show it
	// ("read_only"), and why ("writable", "shared", "birthdays", "google").
	Access string `json:"access,omitempty"`
	Reason string `json:"reason,omitempty"`
	// SharedBy is the Google account that shares the calendar with this one, when known.
	SharedBy string `json:"shared_by,omitempty"`
	// fromStore marks a calendar the box knows but the server was not asked
	// about: Name is the name in Hejmo, not the name at the source (web only).
	fromStore bool
}

func connectionOf(c store.Connection) connectionJSON {
	j := connectionJSON{ID: c.ID, Kind: c.Kind, URL: c.URL, Username: c.Username, Single: c.Single}
	if !c.SignInNeededAt.IsZero() {
		at := c.SignInNeededAt
		j.NeedsSignIn, j.SignInNeededAt = true, &at
	}
	return j
}

type newConnectionInput struct {
	Provider string `json:"provider"` // icloud or caldav
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
	Single   bool   `json:"single_calendar"`
}

// connect checks the input, signs in and discovers the calendars, and only then
// stores the connection (with its password sealed). A refused login or an
// unreachable server stores nothing.
func (s *server) connect(r *http.Request, p principal, in newConnectionInput) (store.Connection, []caldavclient.Calendar, error) {
	address := strings.TrimSpace(in.URL)
	switch in.Provider {
	case "icloud":
		address, in.Single = iCloudServer, false
	case "caldav":
		u, err := url.Parse(address)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return store.Connection{}, nil, &inputError{"url", "url must be a web address starting with https://"}
		}
	default:
		return store.Connection{}, nil, &inputError{"provider", "provider must be icloud or caldav (Google signs in with POST /google/authorize and POST /google/callback)"}
	}
	if strings.TrimSpace(in.Username) == "" {
		return store.Connection{}, nil, &inputError{"username", "username is required"}
	}
	if in.Password == "" {
		return store.Connection{}, nil, &inputError{"password", "password is required"}
	}
	ctx, cancel := context.WithTimeout(r.Context(), discoverTimeoutSec*time.Second)
	defer cancel()
	found, err := s.discoverChecked(ctx, address, in.Username, in.Password, in.Single)
	if err != nil {
		return store.Connection{}, nil, err
	}
	conn, err := s.db.CreateCalDAVConnection(p.household.ID, store.NewCalDAVConnection{
		URL: address, Username: in.Username, Password: in.Password, Single: in.Single, Now: s.now(),
	})
	return conn, found, err
}

// rediscover lists a connection's calendars again, marking those already synced.
func (s *server) rediscover(ctx context.Context, p principal, conn store.Connection) ([]discoveredJSON, error) {
	if conn.Kind == "google" {
		return s.googleCalendarsOf(ctx, p, conn)
	}
	user, password, err := s.db.ConnectionCredentials(p.household.ID, conn.ID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, discoverTimeoutSec*time.Second)
	defer cancel()
	found, err := s.discoverChecked(ctx, conn.URL, user, password, conn.Single)
	if err != nil {
		return nil, err
	}
	return s.markAdded(p, conn, found)
}

// discoverChecked signs in and lists the server's calendars. A refused login,
// a server that sends the client to another domain and an unreachable server
// are an *inputError; the same check serves a new connection and a new password.
func (s *server) discoverChecked(ctx context.Context, address, user, password string, single bool) ([]caldavclient.Calendar, error) {
	found, err := s.sync.Discover(ctx, address, user, password, single)
	if err != nil {
		if caldavclient.IsUnauthorized(err) {
			return nil, &inputError{"login", "the server refused the user name or password"}
		}
		var foreign *caldavclient.ForeignHostError
		if errors.As(err, &foreign) {
			return nil, foreignHostError(foreign)
		}
		return nil, &inputError{"server", "could not read calendars from the server: " + err.Error()}
	}
	return found, nil
}

// passwordSyncTimeoutSec bounds the syncs that follow a new password.
const passwordSyncTimeoutSec = 30

// changeConnectionPassword checks a new password against the server and, if the
// server accepts it, stores it sealed and has the connection's calendars sync
// again at once. The error is an *inputError for bad input, store.ErrNotFound
// for an unknown connection.
func (s *server) changeConnectionPassword(ctx context.Context, p principal, id, password string) error {
	conn, err := s.db.Connection(p.household.ID, id)
	if err != nil {
		return err
	}
	if conn.Kind == "google" {
		return &inputError{"provider", "a Google connection has no password: sign in again with POST /google/authorize"}
	}
	if password == "" {
		return &inputError{"password", "password is required"}
	}
	dctx, cancel := context.WithTimeout(ctx, discoverTimeoutSec*time.Second)
	defer cancel()
	if _, err := s.discoverChecked(dctx, conn.URL, conn.Username, password, conn.Single); err != nil {
		return err
	}
	if err := s.db.SetConnectionPassword(p.household.ID, conn.ID, password); err != nil {
		return err
	}
	// The calendars were waiting for a working login: sync them now instead of at their next, backed-off turn.
	// The time is bounded, so a connection with many calendars cannot keep the request open for long; what
	// is not synced by then waits for its next turn.
	sctx, stop := context.WithTimeout(ctx, passwordSyncTimeoutSec*time.Second)
	defer stop()
	if all, err := s.db.SyncedCalendars(p.household.ID); err == nil {
		for _, c := range calendarsOf(all, conn.ID) {
			if sctx.Err() != nil {
				break
			}
			_ = s.sync.Sync(sctx, p.household.ID, c.ID)
		}
	}
	return nil
}

func (s *server) apiChangeConnectionPassword(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	err := s.changeConnectionPassword(r.Context(), p, r.PathValue("id"), in.Password)
	var bad *inputError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "connection not found")
	case errors.As(err, &bad):
		writeError(w, http.StatusUnprocessableEntity, bad.Msg)
	case err != nil:
		s.fail(w, "change password", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *server) markAdded(p principal, conn store.Connection, found []caldavclient.Calendar) ([]discoveredJSON, error) {
	list := make([]discoveredJSON, 0, len(found))
	for _, f := range found {
		list = append(list, discoveredJSON{URL: f.URL, Name: f.Name})
	}
	return s.markAddedList(p, conn, list)
}

// markAddedList marks which of the found calendars the box already syncs.
func (s *server) markAddedList(p principal, conn store.Connection, found []discoveredJSON) ([]discoveredJSON, error) {
	synced, err := s.db.SyncedCalendars(p.household.ID)
	if err != nil {
		return nil, err
	}
	have := map[string]string{}
	for _, c := range synced {
		if c.ConnectionID == conn.ID {
			have[caldavclient.AddressKey(c.URL)] = c.ID
		}
	}
	out := make([]discoveredJSON, 0, len(found))
	for _, f := range found {
		f.CalendarID, f.Added = "", false
		if id, added := have[caldavclient.AddressKey(f.URL)]; added {
			f.CalendarID, f.Added = id, true
		}
		out = append(out, f)
	}
	return out, nil
}

// addConnectionCalendar stores a calendar the parent ticked and runs its first
// sync. The error is an *inputError for bad input, store.ErrNotFound for an
// unknown connection.
func (s *server) addConnectionCalendar(r *http.Request, p principal, connectionID string, in newCalendarInput) (store.SyncedCalendar, error) {
	conn, err := s.db.Connection(p.household.ID, connectionID)
	if err != nil {
		return store.SyncedCalendar{}, err
	}
	opts, err := s.checkConnectionCalendar(p, conn, in)
	if err != nil {
		return store.SyncedCalendar{}, err
	}
	if conn.Kind == "google" {
		return s.addGoogleCalendar(r, p, conn, in, opts)
	}
	cal, err := s.db.AddCalDAVCalendar(p.household.ID, conn.ID, store.NewCalDAVCalendar{
		Name: in.Name, RemoteURL: strings.TrimSpace(in.URL), Color: opts.color, Audience: opts.audience, IntervalMinutes: opts.interval, Now: s.now(),
	})
	if err != nil {
		return store.SyncedCalendar{}, err
	}
	// The first sync runs now. If it fails the calendar stays and the failure
	// is in the sync log; the scheduler retries.
	_ = s.sync.Sync(r.Context(), p.household.ID, cal.ID)
	return s.db.SyncedCalendar(p.household.ID, cal.ID)
}

// addGoogleCalendar stores a calendar of a Google connection the parent ticked,
// with the access Google gives it, and runs its first sync.
func (s *server) addGoogleCalendar(r *http.Request, p principal, conn store.Connection, in newCalendarInput, opts calendarSettings) (store.SyncedCalendar, error) {
	found, err := s.googleAccessFor(r.Context(), p, conn, in.URL)
	if err != nil {
		return store.SyncedCalendar{}, err
	}
	id, ok := googleauth.CalendarIDOf(found.URL)
	if !ok {
		return store.SyncedCalendar{}, &inputError{"calendar", "url must be a calendar of this Google account"}
	}
	cal, err := s.db.AddGoogleCalendar(p.household.ID, conn.ID, store.NewGoogleCalendar{
		Name: in.Name, CalendarID: id, Color: opts.color, Audience: opts.audience, IntervalMinutes: opts.interval,
		SourceReadOnly: found.Access == "read_only", Now: s.now(),
	})
	if err != nil {
		return store.SyncedCalendar{}, err
	}
	_ = s.sync.Sync(r.Context(), p.household.ID, cal.ID)
	return s.db.SyncedCalendar(p.household.ID, cal.ID)
}

// checkConnectionCalendar validates a calendar choice without saving it, so a
// form with several calendars can be checked as a whole before anything is
// stored. The error is an *inputError for bad input.
func (s *server) checkConnectionCalendar(p principal, conn store.Connection, in newCalendarInput) (calendarSettings, error) {
	if strings.TrimSpace(in.Name) == "" {
		return calendarSettings{}, &inputError{"name", "name is required"}
	}
	remote, err := url.Parse(strings.TrimSpace(in.URL))
	base, _ := url.Parse(conn.URL)
	if err != nil || base == nil || remote.Host == "" || caldavclient.CheckSameDomain(base, remote) != nil {
		return calendarSettings{}, &inputError{"calendar", "url must be a calendar on the connection's server"}
	}
	opts, err := s.calendarOptions(p, in, defaultCalDAVSync)
	if err != nil {
		return calendarSettings{}, err
	}
	existing, err := s.db.SyncedCalendars(p.household.ID)
	if err != nil {
		return calendarSettings{}, err
	}
	for _, c := range existing {
		if c.ConnectionID == conn.ID && caldavclient.AddressKey(c.URL) == caldavclient.AddressKey(in.URL) {
			return calendarSettings{}, &inputError{"duplicate", "this calendar is already synced"}
		}
	}
	return opts, nil
}

func (s *server) apiListConnections(w http.ResponseWriter, _ *http.Request, p principal) {
	conns, err := s.db.Connections(p.household.ID)
	if err != nil {
		s.fail(w, "list connections", err)
		return
	}
	out := make([]connectionJSON, 0, len(conns))
	for _, c := range conns {
		out = append(out, connectionOf(c))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiConnect(w http.ResponseWriter, r *http.Request, p principal) {
	if s.refuseOff(w, outbound.Calendars) {
		return
	}
	var in newConnectionInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	conn, found, err := s.connect(r, p, in)
	var bad *inputError
	switch {
	case errors.As(err, &bad):
		writeError(w, http.StatusUnprocessableEntity, bad.Msg)
		return
	case err != nil:
		s.fail(w, "connect", err)
		return
	}
	j := connectionOf(conn)
	if j.Calendars, err = s.markAdded(p, conn, found); err != nil {
		s.fail(w, "connect", err)
		return
	}
	writeJSON(w, http.StatusCreated, j)
}

func (s *server) apiDiscover(w http.ResponseWriter, r *http.Request, p principal) {
	if s.refuseOff(w, outbound.Calendars) {
		return
	}
	conn, err := s.db.Connection(p.household.ID, r.PathValue("id"))
	if s.storeError(w, "connection", err) {
		return
	}
	found, err := s.rediscover(r.Context(), p, conn)
	var bad *inputError
	switch {
	case errors.As(err, &bad):
		writeError(w, http.StatusUnprocessableEntity, bad.Msg)
		return
	case err != nil:
		s.fail(w, "discover", err)
		return
	}
	j := connectionOf(conn)
	j.Calendars = found
	writeJSON(w, http.StatusOK, j)
}

// eventCountJSON is the number of events one found calendar has in the import
// window. State is "count", "empty" (none in the last 12 months or later) or
// "unknown" (the server did not answer in time or cannot count); Text says it
// in the language of the request.
type eventCountJSON struct {
	URL   string `json:"url"`
	State string `json:"state"`
	Count int    `json:"count"`
	Text  string `json:"text"`
}

// apiEventCounts counts the events of every calendar found on a connection.
// The calendars are asked together, each with its own timeout, so one slow
// calendar shows as unknown and does not hold up the others.
func (s *server) apiEventCounts(w http.ResponseWriter, r *http.Request, p principal) {
	if s.refuseOff(w, outbound.Calendars) {
		return
	}
	conn, err := s.db.Connection(p.household.ID, r.PathValue("id"))
	if s.storeError(w, "connection", err) {
		return
	}
	found, err := s.rediscover(r.Context(), p, conn)
	var bad *inputError
	switch {
	case errors.As(err, &bad):
		writeError(w, http.StatusUnprocessableEntity, bad.Msg)
		return
	case err != nil:
		s.fail(w, "discover", err)
		return
	}
	user, password, err := s.db.ConnectionCredentials(p.household.ID, conn.ID)
	if err != nil {
		s.fail(w, "credentials", err)
		return
	}
	count := func(ctx context.Context, address string) (int, error) {
		if conn.Kind == "google" {
			return s.sync.CountConnectionEvents(ctx, p.household, conn, address)
		}
		return s.sync.CountEvents(ctx, conn.URL, user, password, address)
	}
	out := make([]eventCountJSON, len(found))
	var wg sync.WaitGroup
	for i, f := range found {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), s.countWait)
			defer cancel()
			n, err := count(ctx, f.URL)
			out[i] = s.eventCount(r, f.URL, n, err)
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, out)
}

func (s *server) eventCount(r *http.Request, address string, n int, err error) eventCountJSON {
	switch {
	case err != nil:
		return eventCountJSON{URL: address, State: "unknown", Text: s.say(r, i18n.Msg("connections.count.unknown"))}
	case n == 0:
		return eventCountJSON{URL: address, State: "empty", Text: s.say(r, i18n.Msg("connections.count.empty"))}
	case n == 1:
		return eventCountJSON{URL: address, State: "count", Count: 1, Text: s.say(r, i18n.Msg("connections.count.one"))}
	}
	return eventCountJSON{URL: address, State: "count", Count: n, Text: s.say(r, i18n.Msg("connections.count.many", "count", strconv.Itoa(n)))}
}

func (s *server) apiAddConnectionCalendar(w http.ResponseWriter, r *http.Request, p principal) {
	if s.refuseOff(w, outbound.Calendars) {
		return
	}
	var in newCalendarInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	cal, err := s.addConnectionCalendar(r, p, r.PathValue("id"), in)
	var bad *inputError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "connection not found")
		return
	case errors.As(err, &bad):
		writeError(w, http.StatusUnprocessableEntity, bad.Msg)
		return
	case err != nil:
		s.fail(w, "add calendar", err)
		return
	}
	j := plainCalendarJSON(cal.Calendar)
	j.Sync = s.syncState(r, cal, p.location())
	writeJSON(w, http.StatusCreated, j)
}

func (s *server) apiDeleteConnection(w http.ResponseWriter, r *http.Request, p principal) {
	if s.storeError(w, "delete connection", s.db.DeleteConnection(p.household.ID, r.PathValue("id"))) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// foreignHostError is the input error for a server that sent the client to a
// host it may not talk to: another domain, or http where https was chosen.
func foreignHostError(e *caldavclient.ForeignHostError) *inputError {
	if e.Downgrade {
		return &inputError{"downgrade", e.Error()}
	}
	return &inputError{"foreign_host", e.Error()}
}
