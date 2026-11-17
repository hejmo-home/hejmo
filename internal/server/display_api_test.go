package server_test

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

// ADR 0015 parity: a display opens a pairing and reads its state through
// /api/v1 (ticket 61), not only through its HTML routes.

func openAPIPairing(c *apiClient) (int, map[string]any) {
	status, body, _ := c.call(http.MethodPost, "/display/pairing", nil)
	return status, body
}

func TestDisplayOpensAPairingAndReadsItsStateThroughTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	anon := newAPIClient(t, c.b) // a display has no session and no token yet

	status, opened := openAPIPairing(anon)
	code, _ := opened["code"].(string)
	token, _ := opened["token"].(string)
	if status != http.StatusCreated || len(code) != 6 || token == "" || opened["state"] != "pairing" {
		t.Fatalf("open = %d %v", status, opened)
	}
	if u, _ := opened["pair_url"].(string); !strings.Contains(u, "/displays?code="+code) {
		t.Fatalf("pair_url = %q", u)
	}

	d := anon.withToken(token)
	status, st, _ := d.call(http.MethodGet, "/display/state", nil)
	if status != http.StatusOK || st["state"] != "pairing" || st["code"] != code {
		t.Fatalf("state while waiting = %d %v", status, st)
	}

	if status, _, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": code, "name": "Kitchen", "type": "screen"}); status != http.StatusCreated {
		t.Fatalf("pair = %d", status)
	}
	status, st, _ = d.call(http.MethodGet, "/display/state", nil)
	if status != http.StatusOK || st["state"] != "paired" || st["theme"] == "" || st["theme"] == nil {
		t.Fatalf("state after pairing = %d %v", status, st)
	}

	// The token is a bearer token for the display's other endpoints too.
	req, _ := http.NewRequest(http.MethodGet, c.b.srv.URL+"/display/view", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.b.bareClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /display/view with the display token = %d, want 200", res.StatusCode)
	}

	// Revoked: the state says so.
	_, _, list := c.call(http.MethodGet, "/displays", nil)
	id := list[0].(map[string]any)["id"].(string)
	if status, _, _ := c.call(http.MethodDelete, "/displays/"+id, nil); status != http.StatusNoContent {
		t.Fatalf("revoke = %d", status)
	}
	status, st, _ = d.call(http.MethodGet, "/display/state", nil)
	if status != http.StatusOK || st["state"] != "unpaired" {
		t.Fatalf("state after revoke = %d %v", status, st)
	}
}

func TestDisplayStateNeedsABearerToken(t *testing.T) {
	t.Parallel()
	anon := newAPIClient(t, newBox(t))
	if status, _, _ := anon.call(http.MethodGet, "/display/state", nil); status != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", status)
	}
	if status, st, _ := anon.withToken("unknown").call(http.MethodGet, "/display/state", nil); status != http.StatusOK || st["state"] != "unpaired" {
		t.Fatalf("unknown token = %d %v, want 200 unpaired", status, st)
	}
}

func TestDisplayTokenOpensNothingElseInTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, opened := openAPIPairing(newAPIClient(t, c.b))
	d := c.withToken(opened["token"].(string))
	if status, _, _ := d.call(http.MethodGet, "/people", nil); status != http.StatusUnauthorized {
		t.Fatalf("display token on /people = %d, want 401", status)
	}
	// And an API token is no display token.
	if status, st, _ := c.withToken(c.newToken("read")).call(http.MethodGet, "/display/state", nil); status != http.StatusOK || st["state"] != "unpaired" {
		t.Fatalf("api token on state = %d %v, want 200 unpaired", status, st)
	}
}

func TestPairingOpenedThroughTheAPIIsLimitedPerAddress(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	anon := newAPIClient(t, b)
	// Ten pairing pages use up the allowance of this address ...
	for i := 0; i < 10; i++ {
		if r := newScreen(b).get("/display", "en"); r.status != http.StatusOK {
			t.Fatalf("page %d = %d", i, r.status)
		}
	}
	// ... and the API route shares it.
	raw, status, _ := anon.callRaw(http.MethodPost, "/display/pairing", nil)
	if status != http.StatusTooManyRequests {
		t.Fatalf("API open after 10 page opens = %d %s, want 429", status, raw)
	}
	b.now = b.now.Add(11 * time.Minute)
	if status, _ := openAPIPairing(anon); status != http.StatusCreated {
		t.Fatalf("after the wait = %d, want 201", status)
	}
}

func TestPairingNeedsASetUpBox(t *testing.T) {
	t.Parallel()
	anon := newAPIClient(t, newBox(t))
	if status, body := openAPIPairing(anon); status != http.StatusConflict || body["error"] == nil {
		t.Fatalf("open on a box not set up = %d %v, want 409 with an error", status, body)
	}
}

func TestWaitingDisplaysOpenedThroughTheAPIAreCapped(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	anon := newAPIClient(t, b)
	// Each opens from its own address, so the per-address limit does not
	// interfere; all codes stay valid (no time passes).
	const opened = store.MaxWaitingDisplays + 5
	tokens := make([]string, opened)
	for i := range tokens {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/display/pairing", nil)
		req.RemoteAddr = fmt.Sprintf("10.0.0.%d:4000", i+1)
		req.TLS = &tls.ConnectionState{} // as if it came in over HTTPS
		rec := httptest.NewRecorder()
		b.handler.ServeHTTP(rec, req)
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusCreated {
			t.Fatalf("open %d = %d %s", i, rec.Code, rec.Body)
		}
		tokens[i], _ = out["token"].(string)
	}
	state := func(i int) any {
		status, st, _ := anon.withToken(tokens[i]).call(http.MethodGet, "/display/state", nil)
		if status != http.StatusOK {
			t.Fatalf("state %d = %d", i, status)
		}
		return st["state"]
	}
	// The oldest waiting displays were dropped; the newest MaxWaitingDisplays remain.
	if got := state(opened - store.MaxWaitingDisplays - 1); got != "unpaired" {
		t.Fatalf("display just beyond the cap = %v, want unpaired", got)
	}
	if got := state(opened - store.MaxWaitingDisplays); got != "pairing" {
		t.Fatalf("oldest display within the cap = %v, want pairing", got)
	}
	if got := state(opened - 1); got != "pairing" {
		t.Fatalf("newest display = %v, want pairing", got)
	}
}

func TestOtherAuthorizationSchemesLeaveTheDisplayCookieAlone(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	s := newScreen(b)
	if r := s.get("/display", "en"); r.status != http.StatusOK {
		t.Fatalf("open = %d", r.status)
	}
	// A reverse proxy in front of the box may add Basic auth to every request.
	req, _ := http.NewRequest(http.MethodGet, b.srv.URL+"/display/state", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	res, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var st map[string]any
	_ = json.NewDecoder(res.Body).Decode(&st)
	if st["state"] != "pairing" {
		t.Fatalf("state with a Basic header = %v, want the cookie's display (pairing)", st)
	}
}
