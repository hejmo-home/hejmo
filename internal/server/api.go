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
	{"PATCH", "/people/{id}", auth.Perm("people", auth.Write), (*server).apiUpdatePerson},
	{"PUT", "/people/order", auth.Perm("people", auth.Write), (*server).apiOrderPeople},

	{"GET", "/food", auth.Perm("health", auth.Read), (*server).apiListFood},
	{"GET", "/people/{id}/food", auth.Perm("health", auth.Read), (*server).apiGetFood},
	{"PUT", "/people/{id}/food", auth.Perm("health", auth.Write), (*server).apiPutFood},
	{"GET", "/people/{id}/stars", auth.Perm("tasks", auth.Read), (*server).apiGetStars},
	{"POST", "/people/{id}/stars", auth.Perm("tasks", auth.Write), (*server).apiBookStars},
	{"GET", "/me/passkeys", auth.Perm("people", auth.Read), (*server).apiListPasskeys},
	{"POST", "/me/passkeys/registration", auth.Perm("people", auth.Write), (*server).apiPasskeyBegin},
	{"POST", "/me/passkeys", auth.Perm("people", auth.Write), (*server).apiPasskeyCreate},
	{"PATCH", "/me/passkeys/{id}", auth.Perm("people", auth.Write), (*server).apiRenamePasskey},
	{"DELETE", "/me/passkeys/{id}", auth.Perm("people", auth.Delete), (*server).apiDeletePasskey},
	{"POST", "/me/recovery-codes", auth.Perm("people", auth.Write), (*server).apiNewRecoveryCodes},
	{"POST", "/me/recovery-codes/saved", auth.Perm("people", auth.Write), (*server).apiRecoveryCodesSaved},
	{"GET", "/people/{id}/invitation", auth.Perm("tokens", auth.Read), (*server).apiGetInvitation},
	{"POST", "/people/{id}/invitation", auth.Perm("tokens", auth.Write), (*server).apiCreateInvitation},
	{"DELETE", "/people/{id}/invitation", auth.Perm("tokens", auth.Delete), (*server).apiWithdrawInvitation},
	{"POST", "/people/{id}/reset-otp", auth.Perm("people", auth.Write), (*server).apiResetOTP},
	{"GET", "/tokens", auth.Perm("tokens", auth.Read), (*server).apiListTokens},
	{"POST", "/tokens", auth.Perm("tokens", auth.Write), (*server).apiCreateToken},
	{"GET", "/mcp", auth.Perm("settings", auth.Read), (*server).apiGetMCP},
	{"PUT", "/mcp", auth.Perm("settings", auth.Write), (*server).apiSetMCP},
	{"POST", "/mcp/tokens", auth.Perm("tokens", auth.Write), (*server).apiCreateMCPToken},
	{"POST", "/caldav-profile", auth.Perm("tokens", auth.Write), (*server).apiIOSProfile},
	{"DELETE", "/tokens/{id}", auth.Perm("tokens", auth.Delete), (*server).apiRevokeToken},
	{"GET", "/groups", auth.Perm("people", auth.Read), (*server).apiListGroups},
	{"POST", "/groups", auth.Perm("people", auth.Write), (*server).apiCreateGroup},
	{"PATCH", "/groups/{id}", auth.Perm("people", auth.Write), (*server).apiUpdateGroup},
	{"DELETE", "/groups/{id}", auth.Perm("people", auth.Write), (*server).apiDeleteGroup},
	{"GET", "/calendars", auth.Perm("events", auth.Read), (*server).apiListCalendars},
	{"POST", "/calendars", auth.Perm("events", auth.Write), (*server).apiAddCalendar},
	{"PATCH", "/calendars/{id}", auth.Perm("events", auth.Write), (*server).apiUpdateCalendar},
	{"GET", "/day-notes", auth.Perm("displays", auth.Read), (*server).apiListDayNotes},
	{"PUT", "/day-notes/{day}", auth.Perm("displays", auth.Write), (*server).apiSetDayNote},
	{"DELETE", "/day-notes/{day}", auth.Perm("displays", auth.Delete), (*server).apiDeleteDayNote},
	{"DELETE", "/calendars/{id}", auth.Perm("events", auth.Delete), (*server).apiDeleteCalendar},
	{"POST", "/calendars/{id}/sync", auth.Perm("events", auth.Write), (*server).apiSyncCalendar},
	{"GET", "/calendar-connections", auth.Perm("settings", auth.Read), (*server).apiListConnections},
	{"POST", "/calendar-connections", auth.Perm("settings", auth.Write), (*server).apiConnect},
	{"DELETE", "/calendar-connections/{id}", auth.Perm("settings", auth.Delete), (*server).apiDeleteConnection},
	{"PUT", "/calendar-connections/{id}/credentials", auth.Perm("settings", auth.Write), (*server).apiChangeConnectionPassword},
	{"GET", "/calendar-connections/{id}/calendars", auth.Perm("settings", auth.Write), (*server).apiDiscover},
	{"GET", "/calendar-connections/{id}/event-counts", auth.Perm("settings", auth.Write), (*server).apiEventCounts},
	{"POST", "/calendar-connections/{id}/calendars", auth.Perm("settings", auth.Write), (*server).apiAddConnectionCalendar},
	{"GET", "/sync-log", auth.Perm("settings", auth.Read), (*server).apiSyncLog},
	{"GET", "/https", auth.Perm("settings", auth.Read), (*server).apiHTTPS},
	{"PATCH", "/https", auth.Perm("settings", auth.Write), (*server).apiSetRouterName},
	{"GET", "/settings", auth.Perm("settings", auth.Read), (*server).apiGetSettings},
	{"PATCH", "/settings", auth.Perm("settings", auth.Write), (*server).apiUpdateSettings},
	{"GET", "/places", auth.Perm("settings", auth.Read), (*server).apiSearchPlaces},
	{"GET", "/update", auth.Perm("settings", auth.Read), (*server).apiGetUpdate},
	{"POST", "/update/dismiss", auth.Perm("settings", auth.Write), (*server).apiDismissUpdate},
	{"GET", "/events", auth.Perm("events", auth.Read), (*server).apiListEvents},
	{"POST", "/events", auth.Perm("events", auth.Write), (*server).apiCreateEvent},
	{"PATCH", "/events/{id}", auth.Perm("events", auth.Write), (*server).apiUpdateEvent},
	{"DELETE", "/events/{id}", auth.Perm("events", auth.Delete), (*server).apiDeleteEvent},
	{"GET", "/holidays", auth.Perm("events", auth.Read), (*server).apiHolidays},
	{"GET", "/search", auth.Perm("events", auth.Read), (*server).apiSearch},
	{"GET", "/tasks", auth.Perm("tasks", auth.Read), (*server).apiListTasks},
	{"POST", "/tasks", auth.Perm("tasks", auth.Write), (*server).apiCreateTask},
	{"PATCH", "/tasks/{id}", auth.Perm("tasks", auth.Write), (*server).apiPatchTask},
	{"DELETE", "/tasks/{id}", auth.Perm("tasks", auth.Delete), (*server).apiDeleteTask},
	{"PUT", "/tasks/{id}/done", auth.Perm("tasks", auth.Complete), (*server).apiSetTaskDone},
	{"POST", "/tasks/{id}/subtasks", auth.Perm("tasks", auth.Write), (*server).apiAddSubtask},
	{"PATCH", "/tasks/{id}/subtasks/{sub_id}", auth.Perm("tasks", auth.Write), (*server).apiPatchSubtask},
	{"DELETE", "/tasks/{id}/subtasks/{sub_id}", auth.Perm("tasks", auth.Write), (*server).apiDeleteSubtask},
	{"POST", "/tasks/{id}/subtasks/{sub_id}/move", auth.Perm("tasks", auth.Write), (*server).apiMoveSubtask},
	{"PUT", "/tasks/{id}/subtasks/{sub_id}/done", auth.Perm("tasks", auth.Complete), (*server).apiSetSubtaskDone},
	{"GET", "/routines", auth.Perm("tasks", auth.Read), (*server).apiListRoutines},
	{"POST", "/routines", auth.Perm("tasks", auth.Write), (*server).apiCreateRoutine},
	{"GET", "/routines/{id}", auth.Perm("tasks", auth.Read), (*server).apiGetRoutine},
	{"PATCH", "/routines/{id}", auth.Perm("tasks", auth.Write), (*server).apiPatchRoutine},
	{"DELETE", "/routines/{id}", auth.Perm("tasks", auth.Delete), (*server).apiDissolveRoutine},
	{"POST", "/routines/{id}/tasks", auth.Perm("tasks", auth.Write), (*server).apiRoutineAddTask},
	{"DELETE", "/routines/{id}/tasks/{task_id}", auth.Perm("tasks", auth.Write), (*server).apiRoutineRemoveTask},
	{"PUT", "/routines/{id}/order", auth.Perm("tasks", auth.Write), (*server).apiRoutineOrder},
	{"GET", "/routine-templates", auth.Perm("tasks", auth.Read), (*server).apiListRoutineTemplates},
	{"POST", "/routine-templates", auth.Perm("tasks", auth.Write), (*server).apiSaveRoutineTemplate},
	{"GET", "/routine-templates/{id}", auth.Perm("tasks", auth.Read), (*server).apiGetRoutineTemplate},
	{"PATCH", "/routine-templates/{id}", auth.Perm("tasks", auth.Write), (*server).apiPatchRoutineTemplate},
	{"DELETE", "/routine-templates/{id}", auth.Perm("tasks", auth.Delete), (*server).apiDeleteRoutineTemplate},
	{"POST", "/routine-templates/{id}/steps", auth.Perm("tasks", auth.Write), (*server).apiAddTemplateStep},
	{"PATCH", "/routine-templates/{id}/steps/{step_id}", auth.Perm("tasks", auth.Write), (*server).apiPatchTemplateStep},
	{"DELETE", "/routine-templates/{id}/steps/{step_id}", auth.Perm("tasks", auth.Write), (*server).apiDeleteTemplateStep},
	{"PUT", "/routine-templates/{id}/order", auth.Perm("tasks", auth.Write), (*server).apiTemplateOrder},
	{"GET", "/lists", auth.Perm("lists", auth.Read), (*server).apiListLists},
	{"POST", "/lists", auth.Perm("lists", auth.Write), (*server).apiCreateList},
	{"GET", "/lists/{id}", auth.Perm("lists", auth.Read), (*server).apiGetList},
	{"PATCH", "/lists/{id}", auth.Perm("lists", auth.Write), (*server).apiUpdateList},
	{"DELETE", "/lists/{id}", auth.Perm("lists", auth.Delete), (*server).apiDeleteList},
	{"POST", "/lists/{id}/entries", auth.Perm("lists", auth.Write), (*server).apiAddEntry},
	{"DELETE", "/lists/{id}/entries/{entry}", auth.Perm("lists", auth.Delete), (*server).apiDeleteEntry},
	{"PUT", "/lists/{id}/entries/{entry}/done", auth.Perm("lists", auth.Complete), (*server).apiSetEntryDone},
	{"PUT", "/lists/{id}/entries/{entry}/position", auth.Perm("lists", auth.Write), (*server).apiMoveEntry},
	{"DELETE", "/lists/{id}/completed-entries", auth.Perm("lists", auth.Delete), (*server).apiDeleteCompleted},
	{"GET", "/articles", auth.Perm("lists", auth.Read), (*server).apiListArticles},
	{"POST", "/articles", auth.Perm("lists", auth.Write), (*server).apiCreateArticle},
	{"GET", "/articles/{id}", auth.Perm("lists", auth.Read), (*server).apiGetArticle},
	{"PATCH", "/articles/{id}", auth.Perm("lists", auth.Write), (*server).apiUpdateArticle},
	{"DELETE", "/articles/{id}", auth.Perm("lists", auth.Delete), (*server).apiDeleteArticle},
	{"GET", "/articles/{id}/picture", auth.Perm("lists", auth.Read), (*server).apiGetArticlePicture},
	{"PUT", "/articles/{id}/picture", auth.Perm("lists", auth.Write), (*server).apiPutArticlePicture},
	{"PUT", "/articles/{id}/hidden", auth.Perm("lists", auth.Write), (*server).apiSetArticleHidden},
	{"POST", "/articles/{id}/copy", auth.Perm("lists", auth.Write), (*server).apiCopyArticle},
	{"GET", "/articles/{id}/warnings", auth.Perm("health", auth.Read), (*server).apiArticleWarnings},
	{"GET", "/library/hiding", auth.Perm("health", auth.Read), (*server).apiGetHiding},
	{"PUT", "/library/hiding", auth.Perm("health", auth.Write), (*server).apiPutHiding},
	{"GET", "/meals", auth.Perm("meals", auth.Read), (*server).apiListMeals},
	{"PUT", "/meals/{date}/{mealtime}", auth.Perm("meals", auth.Write), (*server).apiSetMeal},
	{"DELETE", "/meals/{date}/{mealtime}", auth.Perm("meals", auth.Delete), (*server).apiDeleteMeal},
	{"POST", "/meals/{date}/{mealtime}/restore", auth.Perm("meals", auth.Write), (*server).apiRestoreMeal},
	{"GET", "/meal-templates", auth.Perm("meals", auth.Read), (*server).apiListMealTemplates},
	{"POST", "/meal-templates", auth.Perm("meals", auth.Write), (*server).apiCreateMealTemplate},
	{"GET", "/meal-templates/{id}", auth.Perm("meals", auth.Read), (*server).apiGetMealTemplate},
	{"PUT", "/meal-templates/{id}", auth.Perm("meals", auth.Write), (*server).apiUpdateMealTemplate},
	{"DELETE", "/meal-templates/{id}", auth.Perm("meals", auth.Delete), (*server).apiDeleteMealTemplate},
	{"GET", "/displays", auth.Perm("displays", auth.Read), (*server).apiListDisplays},
	{"POST", "/displays", auth.Perm("displays", auth.Write), (*server).apiPairDisplay},
	{"PATCH", "/displays/{id}", auth.Perm("displays", auth.Write), (*server).apiUpdateDisplay},
	{"DELETE", "/displays/{id}", auth.Perm("displays", auth.Delete), (*server).apiRevokeDisplay},
	{"GET", "/albums", auth.Perm("displays", auth.Read), (*server).apiListAlbums},
	{"POST", "/albums", auth.Perm("displays", auth.Write), (*server).apiCreateAlbum},
	{"GET", "/albums/{id}", auth.Perm("displays", auth.Read), (*server).apiGetAlbum},
	{"PATCH", "/albums/{id}", auth.Perm("displays", auth.Write), (*server).apiRenameAlbum},
	{"DELETE", "/albums/{id}", auth.Perm("displays", auth.Delete), (*server).apiDeleteAlbum},
	{"GET", "/albums/{id}/photos", auth.Perm("displays", auth.Read), (*server).apiListAlbumPhotos},
	{"POST", "/albums/{id}/photos", auth.Perm("displays", auth.Write), (*server).apiUploadPhoto},
	{"PATCH", "/photos/{id}", auth.Perm("displays", auth.Write), (*server).apiUpdatePhoto},
	{"DELETE", "/photos/{id}", auth.Perm("displays", auth.Delete), (*server).apiDeletePhoto},
	{"GET", "/photos/{id}/image", auth.Perm("displays", auth.Read), (*server).apiPhotoImage},
	{"GET", "/photo-storage", auth.Perm("displays", auth.Read), (*server).apiPhotoStorage},
	{"PATCH", "/photo-storage", auth.Perm("displays", auth.Write), (*server).apiSetPhotoLimit},
	{"GET", "/phones", auth.Perm("tokens", auth.Read), (*server).apiListPhones},
	{"DELETE", "/phones/{id}", auth.Perm("tokens", auth.Delete), (*server).apiRevokePhone},
	{"POST", "/phones/{id}/repair", auth.Perm("tokens", auth.Write), (*server).apiRepairPhone},
	{"GET", "/phones/pairings", auth.Perm("tokens", auth.Read), (*server).apiListPhonePairings},
	{"POST", "/phones/pairings", auth.Perm("tokens", auth.Write), (*server).apiCreatePhonePairing},
	{"POST", "/phones/pairings/{id}/confirm", auth.Perm("tokens", auth.Write), (*server).apiConfirmPhonePairing},
}

// displayAPIRoutes are the endpoints a display uses before and after it is
// paired. They take no person: opening a pairing needs no credentials, and
// the state is read with the display's own token as bearer token.
var displayAPIRoutes = []struct {
	Method, Path string
	handle       func(*server, http.ResponseWriter, *http.Request)
}{
	{"POST", "/display/pairing", (*server).apiOpenPairing},
	{"GET", "/display/state", (*server).apiDisplayState},
	{"POST", "/display/wake", (*server).apiDisplayWake},
	{"GET", "/display/saver", (*server).apiDisplaySaver},
	{"GET", "/display/photos/{id}", (*server).apiDisplayPhoto},
}

// APIRoutes lists the served endpoints (used by the contract test).
func APIRoutes() []APIRoute {
	out := append([]APIRoute(nil), apiRoutes...)
	for _, r := range displayAPIRoutes {
		out = append(out, APIRoute{Method: r.Method, Path: r.Path})
	}
	return out
}

// ServesAPIRoute reports whether method and path are served.
func ServesAPIRoute(method, path string) bool {
	for _, r := range APIRoutes() {
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
	for _, r := range displayAPIRoutes {
		mux.HandleFunc(r.Method+" /api/v1"+r.Path, func(w http.ResponseWriter, req *http.Request) { r.handle(s, w, req) })
	}
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
		if t.MCP { // a token made for MCP works there and nowhere else
			return principal{}, false
		}
		s.touchToken(t)
		p, err := s.db.Person(h.ID, t.PersonID)
		return principal{household: h, person: p, scopes: t.Scopes}, err == nil
	}
	p, ok := s.currentPerson(r)
	return principal{household: h, person: p, scopes: "write " + auth.HealthResource}, ok
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
	if !in.Role.Valid() {
		writeError(w, http.StatusUnprocessableEntity, "a valid role is required")
		return
	}
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "list people", err)
		return
	}
	look := personInput{in.Name, in.Color, in.Shape}
	if field, _ := checkLook(people, "", &look, nil); field != "" {
		writeError(w, http.StatusUnprocessableEntity, "invalid "+field)
		return
	}
	var ok bool
	if look.Color, look.Shape, ok = freeStyle(people, look.Color, look.Shape); !ok {
		writeError(w, http.StatusUnprocessableEntity, "no free colour and marker left")
		return
	}
	in.Name, in.Color, in.Shape = look.Name, look.Color, look.Shape
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
	MCP        bool    `json:"mcp"`
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
		j := tokenJSON{ID: t.ID, Name: t.Name, Scopes: t.Scopes, Person: t.PersonName, CreatedAt: t.Created.In(loc).Format(time.RFC3339), MCP: t.MCP}
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

// writeFieldError is writeError for a refused field: the body names it.
func writeFieldError(w http.ResponseWriter, status int, field, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "field": field})
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
