package server_test

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// ADR 0015 parity: a display opens a pairing and reads its state through
// /api/v1 (ticket 61), not only through its HTML routes.

func openAPIPairing(c *apiClient) (int, map[string]any) {
	status, body, _ := c.call(http.MethodPost, "/display/pairing", nil)
	return status, body
}

func TestDisplayOpensAPairingAndReadsItsStateThroughTheAPI(t *testing.T) {
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
	anon := newAPIClient(t, newBox(t))
	if status, _, _ := anon.call(http.MethodGet, "/display/state", nil); status != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", status)
	}
	if status, st, _ := anon.withToken("unknown").call(http.MethodGet, "/display/state", nil); status != http.StatusOK || st["state"] != "unpaired" {
		t.Fatalf("unknown token = %d %v, want 200 unpaired", status, st)
	}
}

func TestDisplayTokenOpensNothingElseInTheAPI(t *testing.T) {
	c := signedInAPI(t)
	_, opened := openAPIPairing(newAPIClient(t, c.b))
	d := c.withToken(opened["token"].(string))
	if status, _, _ := d.call(http.MethodGet, "/people", nil); status != http.StatusUnauthorized {
		t.Fatalf("display token on /people = %d, want 401", status)
	}
	// And an API token is no display token.
	if status, _, _ := c.withToken(c.newToken("read")).call(http.MethodGet, "/display/state", nil); status != http.StatusOK {
		t.Fatalf("api token on state = %d", status)
	}
}

func TestPairingOpenedThroughTheAPIIsLimitedPerAddress(t *testing.T) {
	b := signedInBox(t)
	anon := newAPIClient(t, b)
	refused := 0
	for i := 0; i < 40; i++ {
		// Pages and the API share one allowance per address.
		if i%2 == 0 {
			if r := newScreen(b).get("/display", "en"); r.status == http.StatusTooManyRequests {
				refused++
			}
			continue
		}
		switch status, _ := openAPIPairing(anon); status {
		case http.StatusTooManyRequests:
			refused++
		case http.StatusCreated:
		default:
			t.Fatalf("open %d = %d", i, status)
		}
	}
	if refused == 0 {
		t.Fatal("40 pairings opened from one address, none refused")
	}
	// The refusal says when to try again.
	raw, status, _ := anon.callRaw(http.MethodPost, "/display/pairing", nil)
	if status != http.StatusTooManyRequests {
		t.Fatalf("still limited = %d %s", status, raw)
	}
	b.now = b.now.Add(11 * time.Minute)
	if status, _ := openAPIPairing(anon); status != http.StatusCreated {
		t.Fatalf("after the wait = %d, want 201", status)
	}
}

func TestWaitingDisplaysOpenedThroughTheAPIAreCapped(t *testing.T) {
	b := signedInBox(t)
	anon := newAPIClient(t, b)
	_, first := openAPIPairing(anon)
	for i := 0; i < 30; i++ {
		b.now = b.now.Add(2 * time.Minute)
		if status, _ := openAPIPairing(anon); status != http.StatusCreated {
			t.Fatalf("open %d = %d", i, status)
		}
	}
	// The oldest waiting display was dropped: its token is unknown now.
	if status, st, _ := anon.withToken(first["token"].(string)).call(http.MethodGet, "/display/state", nil); status != http.StatusOK || st["state"] != "unpaired" {
		t.Fatalf("evicted display state = %d %v", status, st)
	}
}
