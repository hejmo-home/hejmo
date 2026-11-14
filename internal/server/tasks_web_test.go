package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestTasksPageNeedsASignedInParent(t *testing.T) {
	b, _ := configuredBox(t)
	if r := b.get("/tasks"); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Fatalf("signed out = %d %q", r.status, r.location)
	}
}

func TestParentCreatesAndDeletesTasksInTheWebInterface(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	r := b.post("/tasks", url.Values{"person": {tb.miaID}, "title": {"Brush teeth"}, "emoji": {"🪥"}, "repeat": {"weekly"}, "weekdays": {"MO", "TU"}, "part": {"morning"}})
	if r.status != http.StatusSeeOther || r.location != "/tasks" {
		t.Fatalf("create = %d %q\n%s", r.status, r.location, r.body)
	}
	page := b.do(http.MethodGet, "/tasks", nil, map[string]string{"Accept-Language": "en"})
	if page.status != http.StatusOK {
		t.Fatalf("page = %d", page.status)
	}
	for _, want := range []string{"Brush teeth", "Every", "Mon, Tue", "Morning", "🪥", "Mia"} {
		if !strings.Contains(page.body, want) {
			t.Fatalf("page lacks %q:\n%s", want, page.body)
		}
	}
	// API parity: the same task is there.
	_, _, list := tb.parent.call(http.MethodGet, "/tasks", nil)
	if len(list) != 1 || list[0].(map[string]any)["repeat"] != "weekly" || list[0].(map[string]any)["part_of_day"] != "morning" {
		t.Fatalf("API list = %v", list)
	}
	id := list[0].(map[string]any)["id"].(string)
	if r := b.post("/tasks/"+id+"/delete", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("delete = %d", r.status)
	}
	if _, _, list := tb.parent.call(http.MethodGet, "/tasks", nil); len(list) != 0 {
		t.Fatalf("left over: %v", list)
	}
	if r := b.post("/tasks/"+id+"/delete", url.Values{}); r.status != http.StatusNotFound {
		t.Fatalf("second delete = %d", r.status)
	}
}

func TestInvalidTaskFormShowsAnError(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	r := b.do(http.MethodPost, "/tasks", url.Values{"person": {tb.miaID}, "title": {" "}, "repeat": {"daily"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `role="alert"`) {
		t.Fatalf("blank title = %d\n%s", r.status, r.body)
	}
	r = b.post("/tasks", url.Values{"person": {tb.miaID}, "title": {"x"}, "repeat": {"once"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("once without date = %d", r.status)
	}
}

// Acceptance: Given a parent on the go, when they confirm a child's task in
// the web interface, then it is stored as done.
func TestParentConfirmsAChildsTaskInTheWebInterface(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	id := tb.newDaily(t, tb.miaID)
	if r := b.post("/tasks/"+id+"/done", url.Values{"done": {"1"}}); r.status != http.StatusSeeOther || r.location != "/tasks" {
		t.Fatalf("confirm = %d %q", r.status, r.location)
	}
	if got := taskState(t, tb.child, id, ""); got["done"] != true {
		t.Fatalf("stored state = %v", got)
	}
	if page := b.get("/tasks"); page.status != http.StatusOK || !strings.Contains(page.body, "data-done") || !strings.Contains(page.body, "<s>Brush teeth</s>") {
		t.Fatalf("page does not show the task as done (%d)", page.status)
	}
	if r := b.post("/tasks/"+id+"/done", url.Values{"done": {"0"}}); r.status != http.StatusSeeOther {
		t.Fatalf("reopen = %d", r.status)
	}
	if got := taskState(t, tb.child, id, ""); got["done"] != false {
		t.Fatalf("after reopen = %v", got)
	}
	if r := b.post("/tasks/00000000-0000-4000-8000-000000000000/done", url.Values{"done": {"1"}}); r.status != http.StatusNotFound {
		t.Fatalf("unknown = %d", r.status)
	}
}

func TestTasksPageIsGerman(t *testing.T) {
	tb := newTaskBox(t)
	r := tb.parent.b.do(http.MethodGet, "/tasks", nil, map[string]string{"Accept-Language": "de"})
	if r.status != http.StatusOK || !strings.Contains(r.body, "Aufgaben") {
		t.Fatalf("de page = %d", r.status)
	}
}
