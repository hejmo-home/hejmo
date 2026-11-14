package server_test

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// ADR 0015: everything the web interface can do is also possible through the API.
func TestPairingAndUnpairingWorkThroughTheAPI(t *testing.T) {
	c := signedInAPI(t)
	d := newScreen(c.b)
	code := codeOn(t, d.get("/display", "en").body)

	status, shown, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": code, "name": "Kitchen", "room": "Kitchen", "type": "screen"})
	if status != http.StatusCreated || shown["name"] != "Kitchen" || shown["type"] != "screen" || shown["id"] == "" {
		t.Fatalf("pair = %d %v", status, shown)
	}
	if !strings.Contains(d.get("/display", "en").body, `data-state="paired"`) {
		t.Fatal("display not paired through the API")
	}
	status, _, list := c.call(http.MethodGet, "/displays", nil)
	if status != http.StatusOK || len(list) != 1 || list[0].(map[string]any)["id"] != shown["id"] {
		t.Fatalf("list = %d %v", status, list)
	}
	if status, _, _ := c.call(http.MethodDelete, "/displays/"+shown["id"].(string), nil); status != http.StatusNoContent {
		t.Fatalf("revoke = %d", status)
	}
	if !strings.Contains(d.get("/display", "en").body, `data-state="pairing"`) {
		t.Fatal("display still paired after revoke through the API")
	}
	if status, _, _ := c.call(http.MethodDelete, "/displays/"+shown["id"].(string), nil); status != http.StatusNotFound {
		t.Fatalf("second revoke = %d, want 404", status)
	}
}

func TestAPIRefusesWrongExpiredAndInvalidPairing(t *testing.T) {
	c := signedInAPI(t)
	code := codeOn(t, newScreen(c.b).get("/display", "en").body)
	wrong := "000000"
	if code == wrong {
		wrong = "000001"
	}
	if status, _, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": wrong, "name": "X", "type": "screen"}); status != http.StatusNotFound {
		t.Fatalf("wrong code = %d, want 404", status)
	}
	if status, _, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": code, "name": " ", "type": "screen"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("blank name = %d, want 422", status)
	}
	c.b.now = c.b.now.Add(11 * time.Minute)
	if status, _, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": code, "name": "X", "type": "screen"}); status != http.StatusNotFound {
		t.Fatalf("expired code = %d, want 404", status)
	}
}

func TestOnlyScopedParentsManageDisplays(t *testing.T) {
	c := signedInAPI(t)
	ro := c.withToken(c.newToken("read"))
	if status, _, _ := ro.call(http.MethodGet, "/displays", nil); status != http.StatusOK {
		t.Fatalf("read token lists = %d", status)
	}
	if status, _, _ := ro.call(http.MethodPost, "/displays", map[string]any{"code": "123456", "name": "X", "type": "screen"}); status != http.StatusForbidden {
		t.Fatalf("read token pairs = %d, want 403", status)
	}
	if status, _, _ := ro.call(http.MethodDelete, "/displays/00000000-0000-4000-8000-000000000000", nil); status != http.StatusForbidden {
		t.Fatalf("read token revokes = %d, want 403", status)
	}
}

func TestAPIAnswers429AfterFiveWrongCodes(t *testing.T) {
	c := signedInAPI(t)
	code := codeOn(t, newScreen(c.b).get("/display", "en").body)
	wrong := "000000"
	if code == wrong {
		wrong = "000001"
	}
	for i := 0; i < 5; i++ {
		c.call(http.MethodPost, "/displays", map[string]any{"code": wrong, "name": "K", "type": "screen"})
	}
	if status, _, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": code, "name": "K", "type": "screen"}); status != http.StatusTooManyRequests {
		t.Fatalf("after 5 wrong codes = %d, want 429", status)
	}
}
