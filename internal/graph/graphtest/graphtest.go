// Package graphtest is a fake Microsoft identity platform and Microsoft Graph
// for tests: a token endpoint with the device-code flow, and the calendar
// calls the box makes. No test talks to Microsoft.
package graphtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server is the fake. Auth lives under /common/oauth2/v2.0, Graph under /v1.0.
type Server struct {
	*httptest.Server

	mu sync.Mutex
	// KnownClients are the app IDs the identity platform knows; empty = all.
	KnownClients []string
	// Account is the signed-in account's address; Name its display name.
	Account, Name string
	// DeviceState is what the poll answers until changed: "pending",
	// "approved", "declined", "expired", "consent".
	DeviceState string
	// RevokeRefresh makes every refresh fail with invalid_grant.
	RevokeRefresh bool
	// DeviceCodeDown makes the device-code endpoint answer 503.
	DeviceCodeDown bool

	Calendars []Calendar
	events    map[string]map[string]map[string]any // calendar id -> event id -> event
	version   int
	changes   map[string][]change // calendar id -> changes, version ascending
	nextID    int
	// Requests is every request line seen, in order ("GET /v1.0/me"), and
	// Polls counts device-code polls.
	Requests []string
	Polls    int
	// Refreshes counts refresh-token grants; the refresh token rotates.
	Refreshes    int
	refreshToken string
	// PageSize limits delta pages (0 = 100).
	PageSize int
	// ExpireDeltaTokens makes delta tokens answer 410.
	ExpireDeltaTokens bool
	// Bodies holds the JSON bodies of POST and PATCH requests, by request line.
	Bodies map[string][]map[string]any
	// FailWrites makes POST, PATCH and DELETE on events answer this status.
	FailWrites int
}

// Calendar is a calendar of the account.
type Calendar struct {
	ID, Name, OwnerName, OwnerAddress string
	CanEdit                           bool
}

type change struct {
	version int
	id      string
	removed bool
}

// New starts the fake with the account Mama and no calendars.
func New() *Server {
	s := &Server{Account: "mama@example.org", Name: "Mama Maier", DeviceState: "pending",
		events: map[string]map[string]map[string]any{}, changes: map[string][]change{}, Bodies: map[string][]map[string]any{}, refreshToken: "refresh-0"}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// AuthBase and GraphBase are what the client is configured with.
func (s *Server) AuthBase() string  { return s.URL }
func (s *Server) GraphBase() string { return s.URL + "/v1.0" }

// AddCalendar adds a calendar owned by the account.
func (s *Server) AddCalendar(id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calendars = append(s.Calendars, Calendar{ID: id, Name: name, OwnerName: s.Name, OwnerAddress: s.Account, CanEdit: true})
}

// AddShared adds a calendar another person shared, read-only.
func (s *Server) AddShared(id, name, ownerName, ownerAddress string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calendars = append(s.Calendars, Calendar{ID: id, Name: name, OwnerName: ownerName, OwnerAddress: ownerAddress})
}

// Put stores or replaces an event (a Graph event as JSON fields) and records
// the change for delta. It returns the id.
func (s *Server) Put(calendarID string, ev map[string]any) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.put(calendarID, ev, time.Time{})
}

// PutAt is Put with the time Outlook says the event was last modified.
func (s *Server) PutAt(calendarID string, ev map[string]any, modified time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.put(calendarID, ev, modified)
}

func (s *Server) put(calendarID string, ev map[string]any, mod time.Time) string {
	if s.events[calendarID] == nil {
		s.events[calendarID] = map[string]map[string]any{}
	}
	id, _ := ev["id"].(string)
	if id == "" {
		s.nextID++
		id = fmt.Sprintf("ev-%d", s.nextID)
		ev["id"] = id
	}
	if _, ok := ev["type"]; !ok {
		ev["type"] = "singleInstance"
	}
	s.version++
	ev["changeKey"] = fmt.Sprintf("ck-%d", s.version)
	if mod.IsZero() {
		mod = time.Now()
	}
	ev["lastModifiedDateTime"] = mod.UTC().Format("2006-01-02T15:04:05.0000000Z")
	s.events[calendarID][id] = ev
	s.changes[calendarID] = append(s.changes[calendarID], change{s.version, id, false})
	return id
}

// Remove deletes an event.
func (s *Server) Remove(calendarID, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.remove(calendarID, id)
}

func (s *Server) remove(calendarID, id string) {
	delete(s.events[calendarID], id)
	s.version++
	s.changes[calendarID] = append(s.changes[calendarID], change{s.version, id, true})
}

// Event returns a copy of a stored event, or nil.
func (s *Server) Event(calendarID, id string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.events[calendarID][id]; ok {
		out := map[string]any{}
		for k, v := range e {
			out[k] = v
		}
		return out
	}
	return nil
}

// Events returns the ids of the stored events of a calendar.
func (s *Server) Events(calendarID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id := range s.events[calendarID] {
		ids = append(ids, id)
	}
	return ids
}

// Count returns how many requests started with the given prefix, e.g. "GET /v1.0/me/events".
func (s *Server) Count(prefix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.Requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

// Set changes a field under the lock.
func (s *Server) Set(f func(*Server)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthError(w http.ResponseWriter, status int, code, desc string, aadsts int) {
	writeJSON(w, status, map[string]any{"error": code, "error_description": "AADSTS" + strconv.Itoa(aadsts) + ": " + desc, "error_codes": []int{aadsts}})
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests = append(s.Requests, r.Method+" "+r.URL.Path)
	switch {
	case strings.HasPrefix(r.URL.Path, "/common/oauth2/v2.0/devicecode"):
		s.deviceCode(w, r)
	case strings.HasPrefix(r.URL.Path, "/common/oauth2/v2.0/token"):
		s.token(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1.0/"):
		if r.Header.Get("Authorization") != "Bearer access-"+s.refreshToken && !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer access-") {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": "InvalidAuthenticationToken"}})
			return
		}
		s.graph(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) known(id string) bool {
	if len(s.KnownClients) == 0 {
		return true
	}
	for _, k := range s.KnownClients {
		if k == id {
			return true
		}
	}
	return false
}

func (s *Server) deviceCode(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if s.DeviceCodeDown {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	if !s.known(r.PostForm.Get("client_id")) {
		oauthError(w, 400, "unauthorized_client", "Application with identifier was not found in the directory", 700016)
		return
	}
	s.DeviceState = "pending" // every new code starts out waiting
	writeJSON(w, 200, map[string]any{"device_code": "DEVCODE", "user_code": "B7KQ2M9XL", "verification_uri": "https://microsoft.com/devicelogin", "expires_in": 900, "interval": 5})
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	switch r.PostForm.Get("grant_type") {
	case "urn:ietf:params:oauth:grant-type:device_code":
		s.Polls++
		switch s.DeviceState {
		case "pending":
			oauthError(w, 400, "authorization_pending", "pending", 70016)
		case "declined":
			oauthError(w, 400, "authorization_declined", "declined", 70020)
		case "expired":
			oauthError(w, 400, "expired_token", "expired", 70019)
		case "consent":
			oauthError(w, 400, "invalid_grant", "needs admin approval", 65001)
		default:
			writeJSON(w, 200, map[string]any{"access_token": "access-1", "refresh_token": s.refreshToken, "expires_in": 3600, "token_type": "Bearer"})
		}
	case "refresh_token":
		if s.RevokeRefresh || r.PostForm.Get("refresh_token") != s.refreshToken {
			oauthError(w, 400, "invalid_grant", "The refresh token has expired or was revoked", 70000)
			return
		}
		s.Refreshes++
		s.refreshToken = fmt.Sprintf("refresh-%d", s.Refreshes)
		writeJSON(w, 200, map[string]any{"access_token": "access-" + s.refreshToken, "refresh_token": s.refreshToken, "expires_in": 3600, "token_type": "Bearer"})
	default:
		oauthError(w, 400, "unsupported_grant_type", "x", 70000)
	}
}

func (s *Server) graph(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1.0")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case path == "/me":
		writeJSON(w, 200, map[string]any{"displayName": s.Name, "mail": s.Account, "userPrincipalName": s.Account})
	case path == "/me/calendars":
		var vals []map[string]any
		for _, c := range s.Calendars {
			vals = append(vals, map[string]any{"id": c.ID, "name": c.Name, "canEdit": c.CanEdit,
				"owner": map[string]any{"name": c.OwnerName, "address": c.OwnerAddress}})
		}
		writeJSON(w, 200, map[string]any{"value": vals})
	case len(parts) == 5 && parts[1] == "calendars" && parts[3] == "calendarView" && parts[4] == "delta":
		s.delta(w, r, parts[2])
	case len(parts) == 4 && parts[1] == "calendars" && parts[3] == "calendarView":
		s.view(w, r, parts[2])
	case len(parts) == 4 && parts[1] == "calendars" && parts[3] == "events" && r.Method == http.MethodPost:
		if s.FailWrites != 0 {
			writeJSON(w, s.FailWrites, map[string]any{"error": map[string]any{"code": "ErrorAccessDenied"}})
			return
		}
		body := s.body(r)
		s.record(r, body)
		id := s.put(parts[2], body, time.Time{})
		writeJSON(w, 201, s.events[parts[2]][id])
	case len(parts) == 4 && parts[1] == "calendars" && parts[3] == "events" && r.Method == http.MethodGet:
		n := 0
		for _, e := range s.events[parts[2]] {
			if e["type"] != "occurrence" && e["type"] != "exception" {
				n++
			}
		}
		writeJSON(w, 200, map[string]any{"@odata.count": n, "value": []any{}})
	case len(parts) == 4 && parts[1] == "events" && parts[3] == "instances":
		s.instances(w, r, parts[2])
	case len(parts) == 3 && parts[1] == "events":
		s.eventCall(w, r, parts[2])
	case len(parts) >= 4 && parts[1] == "events" && parts[3] == "extensions":
		body := s.body(r)
		s.record(r, body)
		writeJSON(w, 201, map[string]any{})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) body(r *http.Request) map[string]any {
	b, _ := io.ReadAll(r.Body)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return out
}

func (s *Server) record(r *http.Request, body map[string]any) {
	k := r.Method + " " + r.URL.Path
	s.Bodies[k] = append(s.Bodies[k], body)
}

func (s *Server) find(id string) (string, map[string]any) {
	for cal, evs := range s.events {
		if e, ok := evs[id]; ok {
			return cal, e
		}
	}
	return "", nil
}

func (s *Server) eventCall(w http.ResponseWriter, r *http.Request, id string) {
	cal, ev := s.find(id)
	switch r.Method {
	case http.MethodGet:
		if ev == nil {
			writeJSON(w, 404, map[string]any{"error": map[string]any{"code": "ErrorItemNotFound"}})
			return
		}
		writeJSON(w, 200, ev)
	case http.MethodPatch, http.MethodDelete:
		if s.FailWrites != 0 {
			writeJSON(w, s.FailWrites, map[string]any{"error": map[string]any{"code": "ErrorAccessDenied"}})
			return
		}
		if ev == nil {
			writeJSON(w, 404, map[string]any{"error": map[string]any{"code": "ErrorItemNotFound"}})
			return
		}
		if r.Method == http.MethodDelete {
			s.remove(cal, id)
			w.WriteHeader(204)
			return
		}
		body := s.body(r)
		s.record(r, body)
		merged := map[string]any{}
		for k, v := range ev {
			merged[k] = v
		}
		for k, v := range body {
			merged[k] = v
		}
		s.put(cal, merged, time.Time{})
		writeJSON(w, 200, merged)
	}
}

func (s *Server) instances(w http.ResponseWriter, r *http.Request, master string) {
	cal, _ := s.find(master)
	var vals []map[string]any
	for _, e := range s.events[cal] {
		if e["seriesMasterId"] == master {
			vals = append(vals, e)
		}
	}
	writeJSON(w, 200, map[string]any{"value": vals})
}

func (s *Server) view(w http.ResponseWriter, r *http.Request, cal string) {
	var vals []map[string]any
	for _, e := range s.events[cal] {
		if e["type"] != "seriesMaster" {
			vals = append(vals, e)
		}
	}
	writeJSON(w, 200, map[string]any{"value": vals})
}

// delta answers calendarView/delta: instances and single events (never
// series masters), pages of PageSize, and a deltaLink with the version.
func (s *Server) delta(w http.ResponseWriter, r *http.Request, cal string) {
	q := r.URL.Query()
	since, skip := 0, 0
	if t := q.Get("$deltatoken"); t != "" {
		if s.ExpireDeltaTokens {
			writeJSON(w, 410, map[string]any{"error": map[string]any{"code": "syncStateNotFound"}})
			return
		}
		since, _ = strconv.Atoi(t)
	}
	skip, _ = strconv.Atoi(q.Get("$skiptoken"))
	latest := map[string]change{}
	for _, c := range s.changes[cal] {
		if c.version > since {
			latest[c.id] = c
		}
	}
	var all []map[string]any
	for _, c := range s.changes[cal] {
		if l, ok := latest[c.id]; !ok || l.version != c.version {
			continue
		}
		if c.removed {
			all = append(all, map[string]any{"id": c.id, "@removed": map[string]any{"reason": "deleted"}})
		} else if e := s.events[cal][c.id]; e != nil && e["type"] != "seriesMaster" {
			all = append(all, e)
		}
	}
	size := s.PageSize
	if size == 0 {
		size = 100
	}
	end := min(skip+size, len(all))
	out := map[string]any{"value": all[skip:end]}
	base := s.GraphBase() + "/me/calendars/" + url.PathEscape(cal) + "/calendarView/delta?startDateTime=" + url.QueryEscape(q.Get("startDateTime")) + "&endDateTime=" + url.QueryEscape(q.Get("endDateTime"))
	if end < len(all) {
		out["@odata.nextLink"] = base + "&$skiptoken=" + strconv.Itoa(end) + "&$deltatoken=" + strconv.Itoa(since)
	} else {
		out["@odata.deltaLink"] = base + "&$deltatoken=" + strconv.Itoa(s.version)
	}
	writeJSON(w, 200, out)
}

// SetDeviceState changes what the device-code poll answers.
func (s *Server) SetDeviceState(state string) { s.Set(func(s *Server) { s.DeviceState = state }) }
