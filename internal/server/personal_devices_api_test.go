package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// ADR 0015: everything the web interface can do is also possible through the API.
func TestPairingAndRevokingPersonalDevicesWorkThroughTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	status, shown, _ := c.call(http.MethodPost, "/personal-devices/pairings", nil)
	if status != http.StatusCreated || shown["id"] == "" || shown["expires_at"] == "" {
		t.Fatalf("create pairing = %d %v", status, shown)
	}
	path := joinPath(t, `data-join-url="`+shown["url"].(string)+`"`)

	personalDevice := newScreen(c.b)
	if r := personalDevice.get(path, "en"); r.status != http.StatusOK {
		t.Fatalf("personal device opens the code = %d", r.status)
	}
	personalDevice.get(path, "en")

	status, _, waiting := c.call(http.MethodGet, "/personal-devices/pairings", nil)
	if status != http.StatusOK || len(waiting) != 1 || waiting[0].(map[string]any)["id"] != shown["id"] {
		t.Fatalf("waiting personal devices = %d %v", status, waiting)
	}
	mia := personNamed(t, c.b, "Mia")
	if status, _, _ := c.call(http.MethodPost, "/personal-devices/pairings/"+shown["id"].(string)+"/confirm", map[string]any{"person_id": mia.ID, "name": " "}); status != http.StatusUnprocessableEntity {
		t.Fatalf("blank name = %d, want 422", status)
	}
	status, ph, _ := c.call(http.MethodPost, "/personal-devices/pairings/"+shown["id"].(string)+"/confirm", map[string]any{"person_id": mia.ID, "name": "Mia's tablet"})
	if status != http.StatusCreated || ph["name"] != "Mia's tablet" || ph["person_id"] != mia.ID || ph["person_name"] != "Mia" {
		t.Fatalf("confirm = %d %v", status, ph)
	}
	if r := personalDevice.get(path, "en"); r.status != http.StatusSeeOther {
		t.Fatalf("personal device after confirmation = %d", r.status)
	}
	if r := personalDevice.get("/api/v1/me", "en"); !strings.Contains(r.body, `"Mia"`) {
		t.Fatalf("personal device is not Mia: %s", r.body)
	}
	status, _, list := c.call(http.MethodGet, "/personal-devices", nil)
	if status != http.StatusOK || len(list) != 1 || list[0].(map[string]any)["person_name"] != "Mia" {
		t.Fatalf("list = %d %v", status, list)
	}
	id := list[0].(map[string]any)["id"].(string)
	if status, _, _ := c.call(http.MethodDelete, "/personal-devices/"+id, nil); status != http.StatusNoContent {
		t.Fatalf("revoke = %d", status)
	}
	if r := personalDevice.get("/api/v1/me", "en"); r.status != http.StatusUnauthorized {
		t.Fatalf("revoked personal device = %d, want 401", r.status)
	}
	if status, _, _ := c.call(http.MethodDelete, "/personal-devices/"+id, nil); status != http.StatusNotFound {
		t.Fatalf("second revoke = %d, want 404", status)
	}
	if status, _, _ := c.call(http.MethodPost, "/personal-devices/pairings/"+shown["id"].(string)+"/confirm", map[string]any{"person_id": mia.ID, "name": "X"}); status != http.StatusNotFound {
		t.Fatalf("confirm a used pairing = %d, want 404", status)
	}
}

func TestOnlyScopedParentsManagePersonalDevices(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	ro := c.withToken(c.newToken("read"))
	if status, _, _ := ro.call(http.MethodGet, "/personal-devices", nil); status != http.StatusOK {
		t.Fatalf("read token lists = %d", status)
	}
	if status, _, _ := ro.call(http.MethodPost, "/personal-devices/pairings", nil); status != http.StatusForbidden {
		t.Fatalf("read token creates a pairing = %d, want 403", status)
	}
	if status, _, _ := ro.call(http.MethodDelete, "/personal-devices/00000000-0000-4000-8000-000000000000", nil); status != http.StatusForbidden {
		t.Fatalf("read token revokes = %d, want 403", status)
	}
}

// #243: the API says the kind of device a waiting personal device is, from the stored kind.
func TestWaitingPersonalDeviceNamesItsKindThroughTheAPI(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ ua, kind string }{
		{iPhone, "phone"},
		{"Mozilla/5.0 (iPad; CPU OS 19_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148 Safari/604.1", "tablet"},
		{"", "device"},
	} {
		c := signedInAPI(t)
		_, shown, _ := c.call(http.MethodPost, "/personal-devices/pairings", nil)
		path := joinPath(t, `data-join-url="`+shown["url"].(string)+`"`)
		req, _ := http.NewRequest(http.MethodGet, c.b.srv.URL+path, nil)
		req.Header.Set("User-Agent", tc.ua)
		res, err := c.b.trustingClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		status, _, waiting := c.call(http.MethodGet, "/personal-devices/pairings", nil)
		if status != http.StatusOK || len(waiting) != 1 {
			t.Fatalf("waiting = %d %v", status, waiting)
		}
		if got := waiting[0].(map[string]any)["kind"]; got != tc.kind {
			t.Errorf("kind for %q = %v, want %q", tc.ua, got, tc.kind)
		}
	}
}
