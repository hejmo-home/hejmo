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

// ADR 0016: the "show tomorrow block" setting is also available through the API.
func TestTomorrowBlockSettingWorksThroughTheAPI(t *testing.T) {
	c := signedInAPI(t)
	d := newScreen(c.b)
	code := codeOn(t, d.get("/display", "en").body)
	status, shown, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": code, "name": "Kitchen", "type": "screen"})
	if status != http.StatusCreated || shown["show_tomorrow"] != false {
		t.Fatalf("pair = %d %v, want show_tomorrow false", status, shown)
	}
	id := shown["id"].(string)
	status, got, _ := c.call(http.MethodPatch, "/displays/"+id, map[string]any{"show_tomorrow": true})
	if status != http.StatusOK || got["show_tomorrow"] != true {
		t.Fatalf("patch = %d %v", status, got)
	}
	if _, _, list := c.call(http.MethodGet, "/displays", nil); list[0].(map[string]any)["show_tomorrow"] != true {
		t.Fatalf("list = %v", list)
	}
	if status, _, _ := c.call(http.MethodPatch, "/displays/00000000-0000-4000-8000-000000000000", map[string]any{"show_tomorrow": true}); status != http.StatusNotFound {
		t.Fatalf("unknown display = %d, want 404", status)
	}
	ro := c.withToken(c.newToken("read"))
	if status, _, _ := ro.call(http.MethodPatch, "/displays/"+id, map[string]any{"show_tomorrow": false}); status != http.StatusForbidden {
		t.Fatalf("read token patches = %d, want 403", status)
	}
}

// ADR 0015: theme and offsets are also available through the API.
func TestDisplayThemeWorksThroughTheAPI(t *testing.T) {
	c := signedInAPI(t)
	d := newScreen(c.b)
	code := codeOn(t, d.get("/display", "en").body)
	_, shown, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": code, "name": "Kitchen", "type": "screen"})
	if shown["theme"] != "auto" || shown["sunrise_offset"] != float64(0) || shown["sunset_offset"] != float64(0) {
		t.Fatalf("a new screen = %v, want theme auto, offsets 0", shown)
	}
	id := shown["id"].(string)
	status, got, _ := c.call(http.MethodPatch, "/displays/"+id, map[string]any{"theme": "dark", "sunset_offset": -30})
	if status != http.StatusOK || got["theme"] != "dark" || got["sunset_offset"] != float64(-30) || got["sunrise_offset"] != float64(0) {
		t.Fatalf("patch = %d %v", status, got)
	}
	// Only the fields sent change.
	if _, got, _ := c.call(http.MethodPatch, "/displays/"+id, map[string]any{"sunrise_offset": 45}); got["theme"] != "dark" || got["sunset_offset"] != float64(-30) || got["sunrise_offset"] != float64(45) {
		t.Fatalf("second patch = %v", got)
	}
	for _, bad := range []map[string]any{{"theme": "sepia"}, {"sunrise_offset": 181}, {"sunset_offset": -181}} {
		if status, _, _ := c.call(http.MethodPatch, "/displays/"+id, bad); status != http.StatusUnprocessableEntity {
			t.Errorf("%v = %d, want 422", bad, status)
		}
	}
	if status, _, _ := c.call(http.MethodPatch, "/displays/00000000-0000-4000-8000-000000000000", map[string]any{"theme": "dark"}); status != http.StatusNotFound {
		t.Errorf("unknown display = %d, want 404", status)
	}
}
