package server_test

import (
	"net/http"
	"testing"
	"time"
)

// 179 (ADR 0015: API parity with the web form): the brightness values of a
// display through the API, contract-checked against openapi.yaml.
func TestBrightnessValuesWorkThroughTheAPI(t *testing.T) {
	c := signedInAPI(t)
	code := codeOn(t, newScreen(c.b).get("/display", "en").body)
	_, shown, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": code, "name": "Kitchen", "type": "screen"})
	id := shown["id"].(string)
	b, _ := shown["brightness"].(map[string]any)
	q, _ := b["quiet"].(map[string]any)
	if b["day"] != 100.0 || b["evening"] != 40.0 || b["light_source"] != "sun" || q["mode"] != "off" || q["level"] != 5.0 || q["wake_minutes"] != 1.0 {
		t.Fatalf("default brightness = %v", shown["brightness"])
	}
	if b["current"] != nil {
		t.Fatalf("current without a kiosk app = %v", b["current"])
	}
	status, got, _ := c.call(http.MethodPatch, "/displays/"+id, map[string]any{"brightness": map[string]any{
		"day": 80, "evening": 25, "light_source": "room", "quiet": map[string]any{"mode": "dim", "level": 3, "wake_minutes": 5}}})
	if status != http.StatusOK {
		t.Fatalf("patch = %d %v", status, got)
	}
	b, _ = got["brightness"].(map[string]any)
	q, _ = b["quiet"].(map[string]any)
	if b["day"] != 80.0 || b["evening"] != 25.0 || b["light_source"] != "room" || q["mode"] != "dim" || q["level"] != 3.0 || q["wake_minutes"] != 5.0 {
		t.Fatalf("patched brightness = %v", b)
	}
	// Only the fields sent change.
	_, got, _ = c.call(http.MethodPatch, "/displays/"+id, map[string]any{"brightness": map[string]any{"day": 90}})
	b, _ = got["brightness"].(map[string]any)
	if b["day"] != 90.0 || b["evening"] != 25.0 || b["quiet"].(map[string]any)["mode"] != "dim" {
		t.Fatalf("partial patch = %v", b)
	}
	// The contract refuses these itself; the box must too.
	for _, bad := range []string{
		`{"brightness":{"day":0}}`, `{"brightness":{"evening":101}}`, `{"brightness":{"quiet":{"level":0}}}`,
		`{"brightness":{"quiet":{"wake_minutes":31}}}`, `{"brightness":{"quiet":{"mode":"bright"}}}`, `{"brightness":{"light_source":"moon"}}`,
	} {
		if status := rawCall(t, c, http.MethodPatch, "/displays/"+id, bad); status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", bad, status)
		}
	}
}

// The API says what the display does now and whether the kiosk app confirmed it.
func TestTheAPIShowsWhatTheDisplayDoesNow(t *testing.T) {
	fake := &fakeKiosk{}
	c := signedInAPI(t, withKiosk(fake))
	code := codeOn(t, newScreen(c.b).get("/display", "en").body)
	_, shown, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": code, "name": "Kitchen", "type": "screen"})
	id := shown["id"].(string)
	c.call(http.MethodPatch, "/displays/"+id, map[string]any{"kiosk": map[string]any{"url": tablet}})
	if _, err := c.b.db.SQL().Exec(`UPDATE households SET latitude = 48.15, longitude = 11.46`); err != nil {
		t.Fatal(err)
	}
	c.b.now = time.Date(2026, 11, 14, 17, 0, 0, 0, time.UTC) // 18:00 in Munich: after sunset
	c.b.kioskRun.Tick(t.Context())
	_, _, list := c.call(http.MethodGet, "/displays", nil)
	cur, _ := list[0].(map[string]any)["brightness"].(map[string]any)["current"].(map[string]any)
	if cur["phase"] != "evening" || cur["percent"] != 40.0 || cur["confirmed"] != true || cur["reachable"] != true || cur["light_sensor"] != false || cur["since"] == nil {
		t.Fatalf("current = %v", cur)
	}
	fake.setFail(true)
	c.b.kioskRun.Tick(t.Context())
	_, _, list = c.call(http.MethodGet, "/displays", nil)
	cur, _ = list[0].(map[string]any)["brightness"].(map[string]any)["current"].(map[string]any)
	if cur["confirmed"] != false || cur["reachable"] != false || cur["percent"] != 40.0 {
		t.Fatalf("current while the kiosk app does not answer = %v", cur)
	}
}
