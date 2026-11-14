package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/api"
	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/store"
)

// APIRoute is one documented REST endpoint (path relative to /api/v1).
type APIRoute struct {
	Method, Path string
	perm         auth.Permission
	handle       func(*server, http.ResponseWriter, *http.Request, principal)
}

var apiRoutes = []APIRoute{
	{"GET", "/me", auth.Perm("people", auth.Read), (*server).apiMe},
	{"GET", "/people", auth.Perm("people", auth.Read), (*server).apiListPeople},
	{"POST", "/people", auth.Perm("people", auth.Write), (*server).apiCreatePerson},
	{"GET", "/people/{id}", auth.Perm("people", auth.Read), (*server).apiGetPerson},
	{"GET", "/tokens", auth.Perm("tokens", auth.Read), (*server).apiListTokens},
	{"POST", "/tokens", auth.Perm("tokens", auth.Write), (*server).apiCreateToken},
	{"DELETE", "/tokens/{id}", auth.Perm("tokens", auth.Delete), (*server).apiRevokeToken},
	{"GET", "/groups", auth.Perm("people", auth.Read), (*server).apiListGroups},
	{"POST", "/groups", auth.Perm("people", auth.Write), (*server).apiCreateGroup},
	{"GET", "/calendars", auth.Perm("events", auth.Read), (*server).apiListCalendars},
	{"POST", "/calendars", auth.Perm("events", auth.Write), (*server).apiAddCalendar},
	{"PATCH", "/calendars/{id}", auth.Perm("events", auth.Write), (*server).apiUpdateCalendar},
	{"DELETE", "/calendars/{id}", auth.Perm("events", auth.Delete), (*server).apiDeleteCalendar},
	{"POST", "/calendars/{id}/sync", auth.Perm("events", auth.Write), (*server).apiSyncCalendar},
	{"GET", "/sync-log", auth.Perm("settings", auth.Read), (*server).apiSyncLog},
	{"GET", "/https", auth.Perm("settings", auth.Read), (*server).apiHTTPS},
	{"GET", "/settings", auth.Perm("settings", auth.Read), (*server).apiGetSettings},
	{"PATCH", "/settings", auth.Perm("settings", auth.Write), (*server).apiUpdateSettings},
	{"GET", "/events", auth.Perm("events", auth.Read), (*server).apiListEvents},
	{"POST", "/events", auth.Perm("events", auth.Write), (*server).apiCreateEvent},
	{"PATCH", "/events/{id}", auth.Perm("events", auth.Write), (*server).apiUpdateEvent},
	{"DELETE", "/events/{id}", auth.Perm("events", auth.Delete), (*server).apiDeleteEvent},
	{"GET", "/holidays", auth.Perm("events", auth.Read), (*server).apiHolidays},
	{"GET", "/search", auth.Perm("events", auth.Read), (*server).apiSearch},
	{"GET", "/tasks", auth.Perm("tasks", auth.Read), (*server).apiListTasks},
	{"POST", "/tasks", auth.Perm("tasks", auth.Write), (*server).apiCreateTask},
	{"DELETE", "/tasks/{id}", auth.Perm("tasks", auth.Delete), (*server).apiDeleteTask},
	{"PUT", "/tasks/{id}/done", auth.Perm("tasks", auth.Complete), (*server).apiSetTaskDone},
	{"GET", "/displays", auth.Perm("displays", auth.Read), (*server).apiListDisplays},
	{"POST", "/displays", auth.Perm("displays", auth.Write), (*server).apiPairDisplay},
	{"PATCH", "/displays/{id}", auth.Perm("displays", auth.Write), (*server).apiUpdateDisplay},
	{"DELETE", "/displays/{id}", auth.Perm("displays", auth.Delete), (*server).apiRevokeDisplay},
}

// APIRoutes lists the served endpoints (used by the contract test).
func APIRoutes() []APIRoute { return append([]APIRoute(nil), apiRoutes...) }

// ServesAPIRoute reports whether method and path are served.
func ServesAPIRoute(method, path string) bool {
	for _, r := range apiRoutes {
		if strings.EqualFold(r.Method, method) && r.Path == path {
			return true
		}
	}
	return false
}

// principal is who is calling and what they may do.
type principal struct {
	household store.Household
	person    store.Person
	scopes    string
}

func (p principal) may(perm auth.Permission) bool {
	return auth.RoleAllows(p.person.Role, perm) && auth.ScopesAllow(p.scopes, perm)
}

func (s *server) routeAPI(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(api.OpenAPI)
	})
	for _, r := range apiRoutes {
		mux.HandleFunc(r.Method+" /api/v1"+r.Path, func(w http.ResponseWriter, req *http.Request) {
			p, ok := s.authenticate(req)
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if !p.may(r.perm) {
				writeError(w, http.StatusForbidden, "forbidden: needs "+r.perm.String())
				return
			}
			r.handle(s, w, req, p)
		})
	}
}

// touchToken records a token's use, at most once a minute.
func (s *server) touchToken(t store.APIToken) {
	if s.now().Sub(t.LastUsed) > time.Minute {
		_ = s.db.TouchAPIToken(t.ID, s.now())
	}
}

// authenticate accepts a bearer token or a browser session (full rights of the person).
func (s *server) authenticate(r *http.Request) (principal, bool) {
	h, err := s.db.HomeHousehold()
	if err != nil {
		return principal{}, false
	}
	if header := r.Header.Get("Authorization"); header != "" {
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok {
			return principal{}, false
		}
		t, err := s.db.APITokenByHash(auth.HashToken(strings.TrimSpace(token)))
		if err != nil {
			return principal{}, false
		}
		s.touchToken(t)
		p, err := s.db.Person(h.ID, t.PersonID)
		return principal{household: h, person: p, scopes: t.Scopes}, err == nil
	}
	p, ok := s.currentPerson(r)
	return principal{household: h, person: p, scopes: "write"}, ok
}

type personJSON struct {
	ID     string     `json:"id"`
	Name   string     `json:"name"`
	Role   store.Role `json:"role"`
	Color  string     `json:"color"`
	Shape  string     `json:"shape"`
	Symbol string     `json:"symbol"`
}

func toJSON(p store.Person) personJSON {
	return personJSON{p.ID, p.Name, p.Role, p.Color, p.Shape, p.Symbol}
}

func (s *server) apiMe(w http.ResponseWriter, _ *http.Request, p principal) {
	writeJSON(w, http.StatusOK, toJSON(p.person))
}

func (s *server) apiListPeople(w http.ResponseWriter, _ *http.Request, p principal) {
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "list people", err)
		return
	}
	out := make([]personJSON, 0, len(people))
	for _, x := range people {
		out = append(out, toJSON(x))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiGetPerson(w http.ResponseWriter, r *http.Request, p principal) {
	person, err := s.db.Person(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "person not found")
		return
	}
	if err != nil {
		s.fail(w, "get person", err)
		return
	}
	writeJSON(w, http.StatusOK, toJSON(person))
}

func (s *server) apiCreatePerson(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Name, Color, Shape, Symbol string
		Role                       store.Role
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if strings.TrimSpace(in.Name) == "" || !in.Role.Valid() {
		writeError(w, http.StatusUnprocessableEntity, "name and a valid role are required")
		return
	}
	if in.Color == "" || in.Shape == "" {
		people, _ := s.db.People(p.household.ID)
		style := personStyles[len(people)%len(personStyles)]
		if in.Color == "" {
			in.Color = style.color
		}
		if in.Shape == "" {
			in.Shape = style.shape
		}
	}
	created, err := s.db.AddPerson(p.household.ID, store.Person{Name: in.Name, Role: in.Role, Color: in.Color, Shape: in.Shape, Symbol: in.Symbol})
	if err != nil {
		s.fail(w, "create person", err)
		return
	}
	writeJSON(w, http.StatusCreated, toJSON(created))
}

func (s *server) apiCreateToken(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct{ Name, Scopes string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeError(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	if err := auth.ValidateScopes(in.Scopes); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	value := auth.NewToken()
	t, err := s.db.CreateAPIToken(store.APIToken{Name: in.Name, Hash: auth.HashToken(value), PersonID: p.person.ID, Scopes: strings.Join(strings.Fields(in.Scopes), " ")})
	if err != nil {
		s.fail(w, "create token", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": t.ID, "name": t.Name, "scopes": t.Scopes, "token": value})
}

type tokenJSON struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Scopes     string  `json:"scopes"`
	Person     string  `json:"person"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt *string `json:"last_used_at"`
}

func (s *server) apiListTokens(w http.ResponseWriter, _ *http.Request, p principal) {
	tokens, err := s.db.APITokens(p.household.ID)
	if err != nil {
		s.fail(w, "list tokens", err)
		return
	}
	loc := p.location()
	out := make([]tokenJSON, 0, len(tokens))
	for _, t := range tokens {
		j := tokenJSON{ID: t.ID, Name: t.Name, Scopes: t.Scopes, Person: t.PersonName, CreatedAt: t.Created.In(loc).Format(time.RFC3339)}
		if !t.LastUsed.IsZero() {
			used := t.LastUsed.In(loc).Format(time.RFC3339)
			j.LastUsedAt = &used
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiRevokeToken(w http.ResponseWriter, r *http.Request, _ principal) {
	err := s.db.RevokeAPIToken(r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "token not found")
		return
	}
	if err != nil {
		s.fail(w, "revoke token", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
