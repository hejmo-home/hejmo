package server_test

import (
	"net/http"
	"testing"
)

func starAccount(t *testing.T, c *apiClient, person string) map[string]any {
	t.Helper()
	status, acc, _ := c.call(http.MethodGet, "/people/"+person+"/stars", nil)
	if status != http.StatusOK {
		t.Fatalf("GET stars = %d", status)
	}
	return acc
}

// ADR 0018: a new task's default star value is 1 for a child and 0 for an
// adult; parents can set it per task.
func TestNewTaskStarValueDefaultsByRoleAndCanBeSet(t *testing.T) {
	tb := newTaskBox(t)
	mk := func(person string, extra map[string]any) map[string]any {
		t.Helper()
		body := map[string]any{"person_id": person, "title": "Tidy", "repeat": "daily"}
		for k, v := range extra {
			body[k] = v
		}
		status, k, _ := tb.parent.call(http.MethodPost, "/tasks", body)
		if status != http.StatusCreated {
			t.Fatalf("create = %d %v", status, k)
		}
		return k
	}
	if k := mk(tb.miaID, nil); k["stars"] != float64(1) {
		t.Errorf("child default = %v, want 1", k["stars"])
	}
	if k := mk(tb.mamaID, nil); k["stars"] != float64(0) {
		t.Errorf("adult default = %v, want 0", k["stars"])
	}
	if k := mk(tb.miaID, map[string]any{"stars": 3}); k["stars"] != float64(3) {
		t.Errorf("explicit = %v, want 3", k["stars"])
	}
	if k := mk(tb.miaID, map[string]any{"stars": 0}); k["stars"] != float64(0) {
		t.Errorf("explicit 0 = %v, want 0 (not the child default)", k["stars"])
	}
	_, _, list := tb.parent.call(http.MethodGet, "/tasks", nil)
	if list[2].(map[string]any)["stars"] != float64(3) {
		t.Errorf("listed = %v", list[2])
	}
	if got := rawCall(t, tb.parent, http.MethodPost, "/tasks", `{"person_id":"`+tb.miaID+`","title":"x","repeat":"daily","stars":-2}`); got != http.StatusUnprocessableEntity {
		t.Errorf("negative stars = %d, want 422", got)
	}
}

// Acceptance: Given a task worth 3 stars, when Mia ticks it off, then her
// balance rises by 3 and a log entry is created. Unticking deducts and logs.
func TestTickingOnTheApiCreditsAndUntickingDeductsStars(t *testing.T) {
	tb := newTaskBox(t)
	_, k, _ := tb.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": tb.miaID, "title": "Tidy room", "repeat": "daily", "stars": 3})
	id := k["id"].(string)
	if status, _, _ := tb.child.call(http.MethodPut, "/tasks/"+id+"/done", map[string]any{"done": true}); status != http.StatusOK {
		t.Fatalf("tick = %d", status)
	}
	acc := starAccount(t, tb.child, tb.miaID)
	entries := acc["entries"].([]any)
	if acc["balance"] != float64(3) || len(entries) != 1 {
		t.Fatalf("account = %v", acc)
	}
	e := entries[0].(map[string]any)
	if e["delta"] != float64(3) || e["task_id"] != id || e["task_title"] != "Tidy room" || e["booked_by"] != "Mia" {
		t.Fatalf("entry = %v", e)
	}
	tb.child.call(http.MethodPut, "/tasks/"+id+"/done", map[string]any{"done": false})
	acc = starAccount(t, tb.parent, tb.miaID)
	if acc["balance"] != float64(0) || len(acc["entries"].([]any)) != 2 || acc["entries"].([]any)[0].(map[string]any)["delta"] != float64(-3) {
		t.Fatalf("after untick = %v", acc)
	}
}

// Acceptance: Given a parent books -5 with note "redeemed: ice cream", then
// the balance and the log show it.
func TestParentBooksStarsByHandOnTheApi(t *testing.T) {
	tb := newTaskBox(t)
	status, e, _ := tb.parent.call(http.MethodPost, "/people/"+tb.miaID+"/stars", map[string]any{"delta": -5, "note": "redeemed: ice cream"})
	if status != http.StatusCreated || e["delta"] != float64(-5) || e["note"] != "redeemed: ice cream" || e["booked_by"] != "Mama" {
		t.Fatalf("book = %d %v", status, e)
	}
	acc := starAccount(t, tb.child, tb.miaID)
	if acc["balance"] != float64(-5) || acc["person_id"] != tb.miaID || len(acc["entries"].([]any)) != 1 {
		t.Fatalf("account = %v", acc)
	}
}

func TestStarBookingIsParentsOnlyAndValidated(t *testing.T) {
	tb := newTaskBox(t)
	path := "/people/" + tb.miaID + "/stars"
	if status, _, _ := tb.child.call(http.MethodPost, path, map[string]any{"delta": 5, "note": "cheat"}); status != http.StatusForbidden {
		t.Errorf("child books = %d, want 403", status)
	}
	for name, body := range map[string]map[string]any{
		"zero":    {"delta": 0, "note": "x"},
		"no note": {"delta": 1, "note": " "},
	} {
		if status, _, _ := tb.parent.call(http.MethodPost, path, body); status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, status)
		}
	}
	if status, _, _ := tb.parent.call(http.MethodPost, "/people/00000000-0000-4000-8000-000000000000/stars", map[string]any{"delta": 1, "note": "x"}); status != http.StatusNotFound {
		t.Errorf("unknown person = %d, want 404", status)
	}
	if status, _, _ := tb.parent.call(http.MethodGet, "/people/00000000-0000-4000-8000-000000000000/stars", nil); status != http.StatusNotFound {
		t.Errorf("unknown person GET = %d, want 404", status)
	}
	if acc := starAccount(t, tb.parent, tb.miaID); acc["balance"] != float64(0) {
		t.Errorf("refused bookings must not count: %v", acc)
	}
}
