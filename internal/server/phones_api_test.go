package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// ADR 0015: everything the web interface can do is also possible through the API.
func TestPairingAndRevokingPhonesWorkThroughTheAPI(t *testing.T) {
	c := signedInAPI(t)
	status, shown, _ := c.call(http.MethodPost, "/phones/pairings", nil)
	if status != http.StatusCreated || shown["id"] == "" || shown["expires_at"] == "" {
		t.Fatalf("create pairing = %d %v", status, shown)
	}
	path := joinPath(t, `data-join-url="`+shown["url"].(string)+`"`)

	phone := newScreen(c.b)
	if r := phone.get(path, "en"); r.status != http.StatusOK {
		t.Fatalf("phone opens the code = %d", r.status)
	}
	phone.get(path, "en")

	status, _, waiting := c.call(http.MethodGet, "/phones/pairings", nil)
	if status != http.StatusOK || len(waiting) != 1 || waiting[0].(map[string]any)["id"] != shown["id"] {
		t.Fatalf("waiting phones = %d %v", status, waiting)
	}
	mia := personNamed(t, c.b, "Mia")
	if status, _, _ := c.call(http.MethodPost, "/phones/pairings/"+shown["id"].(string)+"/confirm", map[string]any{"person_id": mia.ID, "name": " "}); status != http.StatusUnprocessableEntity {
		t.Fatalf("blank name = %d, want 422", status)
	}
	status, ph, _ := c.call(http.MethodPost, "/phones/pairings/"+shown["id"].(string)+"/confirm", map[string]any{"person_id": mia.ID, "name": "Mia's phone"})
	if status != http.StatusCreated || ph["name"] != "Mia's phone" || ph["person_id"] != mia.ID || ph["person_name"] != "Mia" {
		t.Fatalf("confirm = %d %v", status, ph)
	}
	if r := phone.get(path, "en"); r.status != http.StatusSeeOther {
		t.Fatalf("phone after confirmation = %d", r.status)
	}
	if r := phone.get("/api/v1/me", "en"); !strings.Contains(r.body, `"Mia"`) {
		t.Fatalf("phone is not Mia: %s", r.body)
	}
	status, _, list := c.call(http.MethodGet, "/phones", nil)
	if status != http.StatusOK || len(list) != 1 || list[0].(map[string]any)["person_name"] != "Mia" {
		t.Fatalf("list = %d %v", status, list)
	}
	id := list[0].(map[string]any)["id"].(string)
	if status, _, _ := c.call(http.MethodDelete, "/phones/"+id, nil); status != http.StatusNoContent {
		t.Fatalf("revoke = %d", status)
	}
	if r := phone.get("/api/v1/me", "en"); r.status != http.StatusUnauthorized {
		t.Fatalf("revoked phone = %d, want 401", r.status)
	}
	if status, _, _ := c.call(http.MethodDelete, "/phones/"+id, nil); status != http.StatusNotFound {
		t.Fatalf("second revoke = %d, want 404", status)
	}
	if status, _, _ := c.call(http.MethodPost, "/phones/pairings/"+shown["id"].(string)+"/confirm", map[string]any{"person_id": mia.ID, "name": "X"}); status != http.StatusNotFound {
		t.Fatalf("confirm a used pairing = %d, want 404", status)
	}
}

func TestOnlyScopedParentsManagePhones(t *testing.T) {
	c := signedInAPI(t)
	ro := c.withToken(c.newToken("read"))
	if status, _, _ := ro.call(http.MethodGet, "/phones", nil); status != http.StatusOK {
		t.Fatalf("read token lists = %d", status)
	}
	if status, _, _ := ro.call(http.MethodPost, "/phones/pairings", nil); status != http.StatusForbidden {
		t.Fatalf("read token creates a pairing = %d, want 403", status)
	}
	if status, _, _ := ro.call(http.MethodDelete, "/phones/00000000-0000-4000-8000-000000000000", nil); status != http.StatusForbidden {
		t.Fatalf("read token revokes = %d, want 403", status)
	}
}
