package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"github.com/hejmo-home/hejmo/api"
	"github.com/hejmo-home/hejmo/internal/server"
)

// apiClient calls the API and checks every request and response against
// the OpenAPI description (contract test).
type apiClient struct {
	b      *box
	router routers.Router
	token  string
	cookie bool
}

func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(api.OpenAPI)
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("openapi.yaml invalid: %v", err)
	}
	return doc
}

func newAPIClient(t *testing.T, b *box) *apiClient {
	doc := loadSpec(t)
	doc.Servers = openapi3.Servers{{URL: b.srv.URL + "/api/v1"}}
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}
	return &apiClient{b: b, router: router}
}

func (c *apiClient) call(method, path string, body any) (int, map[string]any, []any) {
	t := c.b.t
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req, _ := http.NewRequest(method, c.b.srv.URL+"/api/v1"+path, bytes.NewReader(raw))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.cookie {
		req.Header.Set("Origin", c.b.srv.URL)
	}

	route, params, err := c.router.FindRoute(req)
	if err != nil {
		t.Fatalf("%s %s is not in openapi.yaml: %v", method, path, err)
	}
	in := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route,
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
	if err := openapi3filter.ValidateRequest(context.Background(), in); err != nil {
		t.Fatalf("request violates contract: %v", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(raw))
	req.ContentLength = int64(len(raw)) // the validator applied schema defaults to the length, not to raw

	client := c.b.bareClient()
	if c.cookie {
		client = c.b.client
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	resBody, _ := io.ReadAll(res.Body)

	out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: res.StatusCode, Header: res.Header,
		Options: &openapi3filter.Options{IncludeResponseStatus: true}}
	out.SetBodyBytes(resBody)
	if err := openapi3filter.ValidateResponse(context.Background(), out); err != nil {
		t.Fatalf("%s %s: response %d violates contract: %v\n%s", method, path, res.StatusCode, err, resBody)
	}

	var obj map[string]any
	var list []any
	if bytes.HasPrefix(bytes.TrimSpace(resBody), []byte("[")) {
		_ = json.Unmarshal(resBody, &list)
	} else {
		_ = json.Unmarshal(resBody, &obj)
	}
	return res.StatusCode, obj, list
}

// signedInAPI returns a client using the parent's browser session.
func signedInAPI(t *testing.T, opts ...boxOption) *apiClient {
	b, secret := configuredBox(t, opts...)
	if r := b.post("/login", loginForm(b, secret, "correct horse battery")); r.status != http.StatusSeeOther {
		t.Fatalf("login = %d", r.status)
	}
	c := newAPIClient(t, b)
	c.cookie = true
	return c
}

func (c *apiClient) newToken(scopes string) string {
	c.b.t.Helper()
	status, body, _ := c.call(http.MethodPost, "/tokens", map[string]any{"name": "test", "scopes": scopes})
	if status != http.StatusCreated {
		c.b.t.Fatalf("create token = %d %v", status, body)
	}
	return body["token"].(string)
}

func (c *apiClient) withToken(token string) *apiClient {
	return &apiClient{b: c.b, router: c.router, token: token}
}

func TestOpenAPIDocumentsEveryRoute(t *testing.T) {
	doc := loadSpec(t)
	for _, r := range server.APIRoutes() {
		item := doc.Paths.Find(r.Path)
		if item == nil || item.GetOperation(r.Method) == nil {
			t.Errorf("%s %s is served but not documented in openapi.yaml", r.Method, r.Path)
		}
	}
	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			if !server.ServesAPIRoute(method, path) {
				t.Errorf("%s %s is documented but not served", method, path)
			}
		}
	}
}

func TestOpenAPIFileIsServed(t *testing.T) {
	b := newBox(t)
	res, err := b.bareClient().Get(b.srv.URL + "/api/v1/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), "openapi: 3.") {
		t.Fatalf("GET openapi.yaml = %d", res.StatusCode)
	}
}

func TestAPIRequiresAuthentication(t *testing.T) {
	c := newAPIClient(t, newBox(t))
	if status, _, _ := c.call(http.MethodGet, "/people", nil); status != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d, want 401", status)
	}
	c.token = "hejmo_invalid"
	if status, _, _ := c.call(http.MethodGet, "/people", nil); status != http.StatusUnauthorized {
		t.Fatalf("bad token = %d, want 401", status)
	}
}

func TestParentManagesPeopleViaSession(t *testing.T) {
	c := signedInAPI(t)
	status, me, _ := c.call(http.MethodGet, "/me", nil)
	if status != http.StatusOK || me["name"] != "Mama" || me["role"] != "parent" {
		t.Fatalf("me = %d %v", status, me)
	}
	status, mia, _ := c.call(http.MethodPost, "/people", map[string]any{"name": "Mia", "role": "child"})
	if status != http.StatusCreated || mia["role"] != "child" || mia["color"] == "" || mia["shape"] == "" {
		t.Fatalf("create Mia = %d %v", status, mia)
	}
	status, _, people := c.call(http.MethodGet, "/people", nil)
	if status != http.StatusOK || len(people) != 2 {
		t.Fatalf("people = %d %v", status, people)
	}
	status, got, _ := c.call(http.MethodGet, "/people/"+mia["id"].(string), nil)
	if status != http.StatusOK || got["name"] != "Mia" {
		t.Fatalf("get Mia = %d %v", status, got)
	}
	if status, _, _ := c.call(http.MethodGet, "/people/00000000-0000-4000-8000-000000000000", nil); status != http.StatusNotFound {
		t.Fatalf("unknown person = %d, want 404", status)
	}
	if status, _, _ := c.call(http.MethodPost, "/people", map[string]any{"name": "", "role": "child"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("empty name = %d, want 422", status)
	}
}

// Acceptance (Platform): Given an API token with "read only", when it is used
// to create something, then the API answers 403. (The calendar variant of this
// test follows with events in work package 3.)
func TestReadOnlyTokenCannotWrite(t *testing.T) {
	c := signedInAPI(t)
	ro := c.withToken(c.newToken("read"))

	if status, _, people := ro.call(http.MethodGet, "/people", nil); status != http.StatusOK || len(people) != 1 {
		t.Fatalf("read with read token = %d", status)
	}
	if status, _, _ := ro.call(http.MethodPost, "/people", map[string]any{"name": "Mia", "role": "child"}); status != http.StatusForbidden {
		t.Fatalf("write with read token = %d, want 403", status)
	}
	if status, _, _ := ro.call(http.MethodPost, "/tokens", map[string]any{"name": "x", "scopes": "write"}); status != http.StatusForbidden {
		t.Fatalf("token escalation with read token = %d, want 403", status)
	}
}

func TestTokenRevocation(t *testing.T) {
	c := signedInAPI(t)
	status, created, _ := c.call(http.MethodPost, "/tokens", map[string]any{"name": "Assistant", "scopes": "read"})
	if status != http.StatusCreated {
		t.Fatalf("create = %d", status)
	}
	tok := c.withToken(created["token"].(string))
	if status, _, _ := tok.call(http.MethodGet, "/people", nil); status != http.StatusOK {
		t.Fatalf("before revoke = %d", status)
	}
	if status, _, _ := c.call(http.MethodDelete, "/tokens/"+created["id"].(string), nil); status != http.StatusNoContent {
		t.Fatalf("revoke = %d", status)
	}
	if status, _, _ := tok.call(http.MethodGet, "/people", nil); status != http.StatusUnauthorized {
		t.Fatalf("after revoke = %d, want 401", status)
	}
}

func TestTokenScopesAreValidated(t *testing.T) {
	c := signedInAPI(t)
	if status, _, _ := c.call(http.MethodPost, "/tokens", map[string]any{"name": "x", "scopes": "admin"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown scope = %d, want 422", status)
	}
}

func TestCookieAPIWritesNeedSameOrigin(t *testing.T) {
	c := signedInAPI(t)
	req, _ := http.NewRequest(http.MethodPost, c.b.srv.URL+"/api/v1/people", strings.NewReader(`{"name":"Mia","role":"child"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example")
	res, err := c.b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site API write = %d, want 403", res.StatusCode)
	}
}

var _ = url.Values{}
