package server_test

import (
	"bytes"
	"net/http"
	"testing"
)

// Ticket 285 over the REST API, contract-checked against openapi.yaml: the
// household setting "meal_shopping" of GET and PATCH /settings.

func TestTheShoppingListIsByHandInTheSettingsUntilChosenOtherwise(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, s, _ := c.call(http.MethodGet, "/settings", nil)
	m, _ := s["meal_shopping"].(map[string]any)
	if m["mode"] != "manual" || m["days"] != float64(7) || m["list_id"] != nil {
		t.Fatalf("meal_shopping = %v: by hand, 7 days, no list", s["meal_shopping"])
	}
	_, _, lists := c.call(http.MethodGet, "/lists", nil)
	var listID string
	for _, l := range lists {
		if x := l.(map[string]any); x["suggestions"] == true {
			listID = x["id"].(string)
		}
	}
	status, s, _ := c.call(http.MethodPatch, "/settings", map[string]any{"meal_shopping": map[string]any{"mode": "automatic", "days": 5, "list_id": listID}})
	m, _ = s["meal_shopping"].(map[string]any)
	if status != http.StatusOK || m["mode"] != "automatic" || m["days"] != float64(5) || m["list_id"] != listID {
		t.Fatalf("patch = %d %v", status, s)
	}
	status, s, _ = c.call(http.MethodPatch, "/settings", map[string]any{"meal_shopping": map[string]any{"mode": "manual"}})
	m, _ = s["meal_shopping"].(map[string]any)
	if status != http.StatusOK || m["mode"] != "manual" || m["days"] != float64(5) || m["list_id"] != listID {
		t.Errorf("a part that is not given stays: %d %v", status, s)
	}
	status, s, _ = c.call(http.MethodPatch, "/settings", map[string]any{"meal_shopping": map[string]any{"list_id": nil}})
	m, _ = s["meal_shopping"].(map[string]any)
	if status != http.StatusOK || m["list_id"] != nil {
		t.Errorf("null takes the chosen list away: %d %v", status, s)
	}
}

func TestTheShoppingSettingRefusesWhatIsWrong(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	// a list without suggestions: the web form does not offer it, so the API refuses it too (ADR 0015)
	status, plain, _ := c.call(http.MethodPost, "/lists", map[string]any{"name": "Pinnwand", "color": "blue", "symbol": "circle-filled", "sort": "manual", "suggestions": false})
	if status != http.StatusCreated {
		t.Fatalf("create list = %d %v", status, plain)
	}
	for name, body := range map[string]map[string]any{
		"unknown mode":          {"mode": "sometimes"},
		"no days":               {"days": 0},
		"too many days":         {"days": 15},
		"unknown list":          {"list_id": "nope"},
		"list without suggests": {"mode": "automatic", "list_id": plain["id"]},
	} {
		if status, _, _ := c.call(http.MethodPatch, "/settings", map[string]any{"meal_shopping": body}); status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, status)
		}
	}
	_, s, _ := c.call(http.MethodGet, "/settings", nil)
	if m := s["meal_shopping"].(map[string]any); m["mode"] != "manual" || m["days"] != float64(7) || m["list_id"] != nil {
		t.Errorf("nothing was saved: %v", m)
	}
}

// Days as a word cannot go through the contract-checked client (the request
// would violate openapi.yaml); the server answers it with 400, as for any body
// of the wrong type.
func TestTheShoppingSettingRefusesDaysAsAWordWith400(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	req, _ := http.NewRequest(http.MethodPatch, c.b.srv.URL+"/api/v1/settings", bytes.NewReader([]byte(`{"meal_shopping":{"days":"seven"}}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.b.srv.URL)
	res, err := c.b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("days as a word = %d, want 400", res.StatusCode)
	}
}
