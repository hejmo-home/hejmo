package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldav"
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
}

type discoveredJSON struct {
	URL   string `json:"url"`
	Name  string `json:"name"`
	Added bool   `json:"added"`
}

func connectionOf(c store.Connection) connectionJSON {
	return connectionJSON{ID: c.ID, Kind: c.Kind, URL: c.URL, Username: c.Username, Single: c.Single}
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
func (s *server) connect(r *http.Request, p principal, in newConnectionInput) (store.Connection, []caldav.Calendar, error) {
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
		return store.Connection{}, nil, &inputError{"provider", "provider must be icloud or caldav"}
	}
	if strings.TrimSpace(in.Username) == "" {
		return store.Connection{}, nil, &inputError{"username", "username is required"}
	}
	if in.Password == "" {
		return store.Connection{}, nil, &inputError{"password", "password is required"}
	}
	ctx, cancel := context.WithTimeout(r.Context(), discoverTimeoutSec*time.Second)
	defer cancel()
	found, err := s.sync.Discover(ctx, address, in.Username, in.Password, in.Single)
	if err != nil {
		if caldav.IsUnauthorized(err) {
			return store.Connection{}, nil, &inputError{"login", "the server refused the user name or password"}
		}
		return store.Connection{}, nil, &inputError{"server", "could not read calendars from the server: " + err.Error()}
	}
	conn, err := s.db.CreateCalDAVConnection(p.household.ID, store.NewCalDAVConnection{
		URL: address, Username: in.Username, Password: in.Password, Single: in.Single, Now: s.now(),
	})
	return conn, found, err
}

// rediscover lists a connection's calendars again, marking those already synced.
func (s *server) rediscover(ctx context.Context, p principal, conn store.Connection) ([]discoveredJSON, error) {
	user, password, err := s.db.ConnectionCredentials(p.household.ID, conn.ID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, discoverTimeoutSec*time.Second)
	defer cancel()
	found, err := s.sync.Discover(ctx, conn.URL, user, password, conn.Single)
	if err != nil {
		if caldav.IsUnauthorized(err) {
			return nil, &inputError{"login", "the server refused the user name or password"}
		}
		return nil, &inputError{"server", "could not read calendars from the server: " + err.Error()}
	}
	return s.markAdded(p, conn, found)
}

func (s *server) markAdded(p principal, conn store.Connection, found []caldav.Calendar) ([]discoveredJSON, error) {
	synced, err := s.db.SyncedCalendars(p.household.ID)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, c := range synced {
		if c.ConnectionID == conn.ID {
			have[caldav.AddressKey(c.URL)] = true
		}
	}
	out := make([]discoveredJSON, 0, len(found))
	for _, f := range found {
		out = append(out, discoveredJSON{URL: f.URL, Name: f.Name, Added: have[caldav.AddressKey(f.URL)]})
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
	if strings.TrimSpace(in.Name) == "" {
		return store.SyncedCalendar{}, &inputError{"name", "name is required"}
	}
	remote, err := url.Parse(strings.TrimSpace(in.URL))
	base, _ := url.Parse(conn.URL)
	if err != nil || base == nil || remote.Host == "" || !strings.EqualFold(remote.Host, base.Host) || (remote.Scheme != "http" && remote.Scheme != "https") {
		return store.SyncedCalendar{}, &inputError{"calendar", "url must be a calendar on the connection's server"}
	}
	opts, err := s.calendarOptions(p, in, defaultCalDAVSync)
	if err != nil {
		return store.SyncedCalendar{}, err
	}
	existing, err := s.db.SyncedCalendars(p.household.ID)
	if err != nil {
		return store.SyncedCalendar{}, err
	}
	for _, c := range existing {
		if c.ConnectionID == conn.ID && caldav.AddressKey(c.URL) == caldav.AddressKey(in.URL) {
			return store.SyncedCalendar{}, &inputError{"duplicate", "this calendar is already synced"}
		}
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

func (s *server) apiAddConnectionCalendar(w http.ResponseWriter, r *http.Request, p principal) {
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
