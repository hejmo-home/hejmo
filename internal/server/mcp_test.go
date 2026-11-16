package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 7.7 (ADR 0018): the MCP server. These tests speak MCP through the
// official client, against the real box over TLS.

// switchMCP turns the MCP server on or off through the REST API.
func (c *apiClient) switchMCP(t *testing.T, on bool) {
	t.Helper()
	if status, body, _ := c.call(http.MethodPut, "/mcp", map[string]any{"enabled": on}); status != http.StatusOK || body["enabled"] != on {
		t.Fatalf("switch MCP %v = %d %v", on, status, body)
	}
}

// newMCPToken creates a token for MCP (area -> none, read or write) and returns its secret.
func (c *apiClient) newMCPToken(t *testing.T, name string, areas map[string]string) string {
	t.Helper()
	status, body, _ := c.call(http.MethodPost, "/mcp/tokens", map[string]any{"name": name, "areas": areas})
	if status != http.StatusCreated {
		t.Fatalf("create MCP token = %d %v", status, body)
	}
	return body["token"].(string)
}

type bearer struct {
	base  http.RoundTripper
	token string
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// mcpConnect opens an MCP session with a bearer token; the error says the connection was refused.
func (c *apiClient) mcpConnect(token string) (*mcp.ClientSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	httpClient := &http.Client{Transport: bearer{base: c.b.bareClient().Transport, token: token}}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-assistant", Version: "1"}, nil)
	return client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: c.b.srv.URL + "/mcp", HTTPClient: httpClient, MaxRetries: -1, DisableStandaloneSSE: true}, nil)
}

func (c *apiClient) mcpSession(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	s, err := c.mcpConnect(token)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func toolNames(t *testing.T, s *mcp.ClientSession) []string {
	t.Helper()
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func hasName(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// callTool calls a tool and returns the text it answered and whether the call failed.
func callTool(s *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err.Error(), true
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	return text.String(), res.IsError
}

var allAreas = map[string]string{"events": "write", "lists": "write", "tasks": "write", "meals": "write"}

func TestMCPRefusesConnectionsWhileSwitchedOff(t *testing.T) {
	tb := newTaskBox(t)
	c := tb.parent
	token := c.newMCPToken(t, "Claude", allAreas) // MCP is off by default
	if s, err := c.mcpConnect(token); err == nil {
		s.Close()
		t.Fatal("a client connected while MCP was off")
	}
	c.switchMCP(t, true)
	c.mcpSession(t, token) // the same token works once MCP is on
	c.switchMCP(t, false)
	if s, err := c.mcpConnect(token); err == nil {
		s.Close()
		t.Fatal("a client connected after MCP was switched off again")
	}
}

func TestMCPStopsAnOpenSessionWhenSwitchedOffOrRevoked(t *testing.T) {
	c := newTaskBox(t).parent
	c.switchMCP(t, true)
	status, tok, _ := c.call(http.MethodPost, "/mcp/tokens", map[string]any{"name": "Claude", "areas": map[string]any{"lists": "write"}})
	if status != http.StatusCreated {
		t.Fatalf("create = %d", status)
	}
	s := c.mcpSession(t, tok["token"].(string))
	if _, failed := callTool(s, "list_lists", nil); failed {
		t.Fatal("the first call failed")
	}
	c.switchMCP(t, false)
	if _, failed := callTool(s, "list_lists", nil); !failed {
		t.Error("an open session kept working after MCP was switched off")
	}
	c.switchMCP(t, true)
	if status, _, _ := c.call(http.MethodDelete, "/tokens/"+tok["id"].(string), nil); status != http.StatusNoContent {
		t.Fatalf("revoke = %d", status)
	}
	if _, failed := callTool(s, "list_lists", nil); !failed {
		t.Error("an open session kept working after its token was revoked")
	}
}

func TestMCPTokenWithEventsReadOnlyCannotCreateAnEvent(t *testing.T) {
	c := newTaskBox(t).parent
	c.switchMCP(t, true)
	s := c.mcpSession(t, c.newMCPToken(t, "Kalender lesen", map[string]string{"events": "read"}))
	names := toolNames(t, s)
	if !hasName(names, "list_events") {
		t.Errorf("the token cannot even read events: %v", names)
	}
	args := map[string]any{"title": "Elternabend", "start": "2026-11-17T19:30:00+01:00", "end": "2026-11-17T21:00:00+01:00"}
	if _, failed := callTool(s, "create_event", args); !failed {
		t.Error("a read-only token created an event")
	}
	if got := c.occurrences(t, "from=2026-11-01T00:00:00Z&to=2026-12-01T00:00:00Z"); len(got) != 0 {
		t.Errorf("the refused call still wrote: %v", got)
	}
	for _, n := range names {
		if strings.Contains(n, "create") || strings.Contains(n, "update") || strings.Contains(n, "delete") || strings.Contains(n, "add") {
			t.Errorf("read-only token is offered the changing tool %q", n)
		}
	}
	// Areas the token does not have are not offered at all.
	for _, n := range names {
		if strings.Contains(n, "list_entry") || strings.Contains(n, "task") || strings.Contains(n, "meal") {
			t.Errorf("an events-only token is offered %q", n)
		}
	}
}

func TestMCPRefusesAnOrdinaryAPIToken(t *testing.T) {
	c := newTaskBox(t).parent
	c.switchMCP(t, true)
	ordinary := c.newToken("write")
	if s, err := c.mcpConnect(ordinary); err == nil {
		s.Close()
		t.Fatal("MCP accepted an API token that was not created for MCP")
	}
	// And the other way round: an MCP token does not open the REST API or CalDAV.
	mcpToken := c.newMCPToken(t, "Claude", allAreas)
	if status, _, _ := c.withToken(mcpToken).call(http.MethodGet, "/lists", nil); status != http.StatusUnauthorized {
		t.Errorf("REST API with an MCP token = %d, want 401", status)
	}
	if _, err := c.phoneWith(t, "mama", mcpToken).calendars(); err == nil {
		t.Error("CalDAV accepted an MCP token")
	}
}

func TestMCPRefusesATokenOfAChild(t *testing.T) {
	tb := newTaskBox(t)
	tb.parent.switchMCP(t, true)
	const secret = "hejmo_test_child_mcp"
	if _, err := tb.parent.b.db.CreateAPIToken(store.APIToken{Name: "Mia MCP", Hash: auth.HashToken(secret), PersonID: tb.miaID, Scopes: "lists", MCP: true}); err != nil {
		t.Fatal(err)
	}
	if s, err := tb.parent.mcpConnect(secret); err == nil {
		s.Close()
		t.Fatal("MCP accepted a token of a child")
	}
}

func TestMCPRefusesWithoutAnyToken(t *testing.T) {
	c := newTaskBox(t).parent
	c.switchMCP(t, true)
	if s, err := c.mcpConnect(""); err == nil {
		s.Close()
		t.Fatal("MCP accepted a client without a token")
	}
	if s, err := c.mcpConnect("hejmo_nonsense"); err == nil {
		s.Close()
		t.Fatal("MCP accepted an unknown token")
	}
}

func TestMCPIsServedOnlyOverHTTPS(t *testing.T) {
	b := newFreshBox(t) // HTTPS is not set up: reachable over plain HTTP only
	h, err := b.db.CreateHousehold(store.Household{Name: "Family"})
	if err != nil {
		t.Fatal(err)
	}
	mama, err := b.db.AddPerson(h.ID, store.Person{Name: "Mama", Role: store.RoleParent})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.db.SetMCPEnabled(h.ID, true); err != nil {
		t.Fatal(err)
	}
	const secret = "hejmo_test_plain_mcp"
	if _, err := b.db.CreateAPIToken(store.APIToken{Name: "Claude", Hash: auth.HashToken(secret), PersonID: mama.ID, Scopes: "lists", MCP: true}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"x","version":"1"}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := httptest.NewRecorder()
	b.handler.ServeHTTP(rec, req) // no TLS on this request
	if rec.Code != http.StatusForbidden {
		t.Errorf("MCP over plain HTTP = %d, want 403\n%s", rec.Code, rec.Body)
	}
}

// The tool list holds events, lists, tasks and the meal plan, and never
// anything that manages tokens, settings, people, displays or phones.
func TestMCPToolListHasNoTokenOrSettingsTools(t *testing.T) {
	c := newTaskBox(t).parent
	c.switchMCP(t, true)
	s := c.mcpSession(t, c.newMCPToken(t, "Alles", allAreas))
	names := toolNames(t, s)
	if len(names) < 20 {
		t.Fatalf("only %d tools: %v", len(names), names)
	}
	for _, n := range names {
		for _, banned := range []string{"token", "setting", "people", "person", "display", "phone", "connection", "invitation", "otp", "recovery", "https", "update_check"} {
			if strings.Contains(n, banned) {
				t.Errorf("tool %q manages %s", n, banned)
			}
		}
	}
	// Every tool is a REST function of one of the four areas, with the scope it needs.
	for _, tool := range server.MCPTools() {
		if !server.ServesAPIRoute(tool.Method, tool.Path) {
			t.Errorf("tool %s mirrors %s %s, which the REST API does not serve", tool.Name, tool.Method, tool.Path)
		}
		area := false
		for _, a := range auth.MCPAreas {
			area = area || tool.Permission.Resource == a
		}
		if !area {
			t.Errorf("tool %s needs %s, outside the MCP areas", tool.Name, tool.Permission)
		}
	}
	for _, want := range []string{"list_events", "create_event", "list_lists", "add_list_entry", "list_tasks", "create_task", "list_routines", "get_stars", "list_meals", "set_meal"} {
		if !hasName(names, want) {
			t.Errorf("tool %q is missing: %v", want, names)
		}
	}
}

func TestMCPToolsDoWhatTheRESTFunctionsDo(t *testing.T) {
	tb := newTaskBox(t)
	c := tb.parent
	c.switchMCP(t, true)
	s := c.mcpSession(t, c.newMCPToken(t, "Claude", allAreas))

	// events
	if out, failed := callTool(s, "create_event", map[string]any{"title": "Elternabend", "start": "2026-11-17T19:30:00+01:00", "end": "2026-11-17T21:00:00+01:00"}); failed {
		t.Fatalf("create_event: %s", out)
	}
	out, failed := callTool(s, "list_events", map[string]any{"from": "2026-11-17T00:00:00+01:00", "to": "2026-11-18T00:00:00+01:00"})
	if failed || !strings.Contains(out, "Elternabend") {
		t.Fatalf("list_events = %v %s", failed, out)
	}
	if got := c.occurrences(t, "from=2026-11-17T00:00:00Z&to=2026-11-19T00:00:00Z"); len(got) != 1 {
		t.Errorf("REST sees %v", got)
	}

	// lists: the path parameter, then a body
	lb := listBox{tb}
	listID := lb.newList(t, "Einkauf", "manual")
	if out, failed := callTool(s, "add_list_entry", map[string]any{"id": listID, "text": "Milch"}); failed {
		t.Fatalf("add_list_entry: %s", out)
	}
	if got := entryTexts(t, c, listID); len(got) != 1 || got[0] != "Milch" {
		t.Errorf("entries = %v", got)
	}
	if out, failed := callTool(s, "get_list", map[string]any{"id": listID}); failed || !strings.Contains(out, "Milch") {
		t.Errorf("get_list = %v %s", failed, out)
	}

	// tasks and stars
	if out, failed := callTool(s, "create_task", map[string]any{"person_id": tb.miaID, "title": "Zähne putzen", "repeat": "daily"}); failed {
		t.Fatalf("create_task: %s", out)
	}
	if out, failed := callTool(s, "list_tasks", nil); failed || !strings.Contains(out, "Zähne putzen") {
		t.Errorf("list_tasks = %v %s", failed, out)
	}
	if out, failed := callTool(s, "get_stars", map[string]any{"id": tb.miaID}); failed {
		t.Errorf("get_stars: %s", out)
	}

	// meal plan: two path parameters
	if out, failed := callTool(s, "set_meal", map[string]any{"date": "2026-11-17", "mealtime": "dinner", "text": "Nudeln"}); failed {
		t.Fatalf("set_meal: %s", out)
	}
	if got := mealsBetween(t, c, "2026-11-17", "2026-11-17"); len(got) != 1 {
		t.Errorf("meal plan = %v", got)
	}

	// An error of the REST function reaches the assistant as an error it can read.
	out, failed = callTool(s, "add_list_entry", map[string]any{"id": "no-such-list", "text": "x"})
	if !failed || !strings.Contains(strings.ToLower(out), "not found") {
		t.Errorf("unknown list = %v %q", failed, out)
	}
}

// Scopes are enforced unchanged: lists:read lets an assistant read lists and nothing else.
func TestMCPEnforcesScopesPerAreaAndAction(t *testing.T) {
	tb := newTaskBox(t)
	c := tb.parent
	c.switchMCP(t, true)
	listID := listBox{tb}.newList(t, "Einkauf", "manual")
	s := c.mcpSession(t, c.newMCPToken(t, "Nur lesen", map[string]string{"lists": "read", "meals": "write"}))
	if out, failed := callTool(s, "list_lists", nil); failed || !strings.Contains(out, "Einkauf") {
		t.Errorf("list_lists = %v %s", failed, out)
	}
	if _, failed := callTool(s, "add_list_entry", map[string]any{"id": listID, "text": "Milch"}); !failed {
		t.Error("a read-only token added a list entry")
	}
	if _, failed := callTool(s, "list_tasks", nil); !failed {
		t.Error("a token without tasks read tasks")
	}
	if _, failed := callTool(s, "set_meal", map[string]any{"date": "2026-11-17", "mealtime": "lunch", "text": "Suppe"}); failed {
		t.Error("a token with meals read and change could not plan a meal")
	}
}

func TestMCPNotesTheUseOfTheToken(t *testing.T) {
	c := newTaskBox(t).parent
	c.switchMCP(t, true)
	token := c.newMCPToken(t, "Claude", allAreas)
	if got := mcpTokenByName(t, c, "Claude"); !got.LastUsed.IsZero() {
		t.Fatal("a new token claims to have been used")
	}
	c.mcpSession(t, token)
	if got := mcpTokenByName(t, c, "Claude"); got.LastUsed.IsZero() {
		t.Error("using the token left no trace")
	}
}

func mcpTokenByName(t *testing.T, c *apiClient, name string) store.APIToken {
	t.Helper()
	h, _ := c.b.db.HomeHousehold()
	all, err := c.b.db.APITokens(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range all {
		if tok.Name == name {
			return tok
		}
	}
	t.Fatalf("no token %q", name)
	return store.APIToken{}
}
