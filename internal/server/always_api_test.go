package server_test

import (
	"net/http"
	"testing"
)

// Ticket 168 (ADR 0015: HTML routes with API parity): the rules of "always on
// the list", the household's time and its holiday over the REST API,
// contract-checked against openapi.yaml.

func apiList(t *testing.T, c *apiClient, name string) string {
	t.Helper()
	status, l, _ := c.call(http.MethodPost, "/lists", map[string]any{"name": name, "color": "blue", "symbol": "circle-filled", "sort": "manual", "suggestions": true})
	if status != http.StatusCreated {
		t.Fatalf("create list = %d %v", status, l)
	}
	return l["id"].(string)
}

func TestARuleIsSetReadAndSwitchedOffOverTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	shop := apiList(t, c, "Shopping")
	if status, _, _ := c.call(http.MethodGet, "/articles/milk/always", nil); status != http.StatusNotFound {
		t.Errorf("no rule yet = %d", status)
	}
	status, r, _ := c.call(http.MethodPut, "/articles/milk/always", map[string]any{"list_id": shop, "weekday": 1, "every": 2, "start_week": "2026-11-18", "quantity": map[string]any{"amount": 3, "unit": "l"}})
	if status != http.StatusOK {
		t.Fatalf("put = %d %v", status, r)
	}
	if r["article_id"] != "milk" || r["enabled"] != true || r["paused"] != false || r["list_id"] != shop || r["list_name"] != "Shopping" || r["weekday"] != float64(1) || r["every"] != float64(2) || r["start_week"] != "2026-11-16" {
		t.Errorf("rule = %v", r)
	}
	if q := r["quantity"].(map[string]any); q["amount"] != float64(3) || q["unit"] != "l" {
		t.Errorf("quantity = %v", q)
	}
	if r["next_date"] == nil {
		t.Error("a rule that is on has a next date")
	}
	status, got, _ := c.call(http.MethodGet, "/articles/milk/always", nil)
	if status != http.StatusOK || got["weekday"] != float64(1) {
		t.Errorf("get = %d %v", status, got)
	}
	status, off, _ := c.call(http.MethodPut, "/articles/milk/always", map[string]any{"enabled": false})
	if status != http.StatusOK || off["enabled"] != false || off["weekday"] != float64(1) || off["next_date"] != nil {
		t.Errorf("off = %d %v", status, off)
	}
	if status, _, _ := c.call(http.MethodDelete, "/articles/milk/always", nil); status != http.StatusNoContent {
		t.Errorf("delete = %d", status)
	}
	if status, _, _ := c.call(http.MethodGet, "/articles/milk/always", nil); status != http.StatusNotFound {
		t.Errorf("after delete = %d", status)
	}
}

func TestARuleThatBreaksTheRulesIsRefusedWithItsField(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	shop := apiList(t, c, "Shopping")
	for name, tc := range map[string]struct {
		article string
		body    map[string]any
		status  int
		field   string
	}{
		"unknown article": {"nothing", map[string]any{"list_id": shop, "weekday": 1}, http.StatusNotFound, ""},
		"no list":         {"milk", map[string]any{"weekday": 1}, http.StatusUnprocessableEntity, "list_id"},
		"unknown list":    {"milk", map[string]any{"list_id": "11111111-1111-1111-1111-111111111111", "weekday": 1}, http.StatusUnprocessableEntity, "list_id"},
		"no weekday":      {"milk", map[string]any{"list_id": shop}, http.StatusUnprocessableEntity, "weekday"},
		"weekday 8":       {"milk", map[string]any{"list_id": shop, "weekday": 8}, http.StatusUnprocessableEntity, "weekday"},
		"every 0":         {"milk", map[string]any{"list_id": shop, "weekday": 1, "every": 0}, http.StatusUnprocessableEntity, "every"},
		"every 53":        {"milk", map[string]any{"list_id": shop, "weekday": 1, "every": 53}, http.StatusUnprocessableEntity, "every"},
		"bad start":       {"milk", map[string]any{"list_id": shop, "weekday": 1, "start_week": "soon"}, http.StatusUnprocessableEntity, "start_week"},
		"bad amount":      {"milk", map[string]any{"list_id": shop, "weekday": 1, "quantity": map[string]any{"amount": 0, "unit": "l"}}, http.StatusUnprocessableEntity, "quantity.amount"},
	} {
		status, body, _ := c.call(http.MethodPut, "/articles/"+tc.article+"/always", tc.body)
		if status != tc.status || (tc.field != "" && body["field"] != tc.field) {
			t.Errorf("%s: %d %v", name, status, body)
		}
	}
	if status, _, _ := c.call(http.MethodPut, "/articles/milk/always", map[string]any{"enabled": false}); status != http.StatusNotFound {
		t.Errorf("switching off nothing = %d", status)
	}
}

func TestTheTimeAndTheHolidayAreSetOverTheAPIAndListTheRules(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	shop := apiList(t, c, "Shopping")
	_, _, _ = c.call(http.MethodPut, "/articles/milk/always", map[string]any{"list_id": shop, "weekday": 1})
	status, s, _ := c.call(http.MethodGet, "/library/always", nil)
	if status != http.StatusOK || s["time"] != "03:00" || s["holiday"] != nil || len(s["rules"].([]any)) != 1 {
		t.Fatalf("settings = %d %v", status, s)
	}
	status, s, _ = c.call(http.MethodPut, "/library/always", map[string]any{"time": "07:30", "holiday": map[string]any{"from": "2026-12-19", "to": "2027-01-03"}})
	if status != http.StatusOK || s["time"] != "07:30" || s["holiday"].(map[string]any)["to"] != "2027-01-03" {
		t.Fatalf("put = %d %v", status, s)
	}
	status, s, _ = c.call(http.MethodPut, "/library/always", map[string]any{"time": "07:30", "holiday": nil})
	if status != http.StatusOK || s["holiday"] != nil {
		t.Errorf("holiday off = %d %v", status, s)
	}
	for name, body := range map[string]map[string]any{
		"bad time": {"time": "7:30"},
		"before":   {"time": "03:00", "holiday": map[string]any{"from": "2026-12-19", "to": "2026-12-01"}},
		"over":     {"time": "03:00", "holiday": map[string]any{"from": "2026-01-01", "to": "2026-01-05"}},
	} {
		if status, b, _ := c.call(http.MethodPut, "/library/always", body); status != http.StatusUnprocessableEntity || b["field"] == nil {
			t.Errorf("%s: %d %v", name, status, b)
		}
	}
}
