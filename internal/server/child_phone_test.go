package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// childPhone is a phone paired for Mia through the real pairing flow (5.13),
// next to the parent who paired it and a task of Mia's and one of Sebastian's.
type childPhone struct {
	parent, phone *apiClient
	screen        *screen
	miaTask       string
	sebastianTask string
}

func newChildPhone(t *testing.T) childPhone {
	t.Helper()
	parent := signedInAPI(t)
	b := parent.b
	mia := personNamed(t, b, "Mia")
	sebastian := personNamed(t, b, "Sebastian")
	scr := pairedPhone(t, b, mia, "Mia's phone")
	pb := *b
	pb.client = scr.client
	phone := &apiClient{b: &pb, router: parent.router, cookie: true}
	tb := taskBox{parent: parent}
	return childPhone{parent: parent, phone: phone, screen: scr, miaTask: tb.newDaily(t, mia.ID), sebastianTask: tb.newDaily(t, sebastian.ID)}
}

// 5.14: Given a phone paired for Mia, when it ticks off Mia's task, then it is done.
func TestChildPhoneTicksOffTheChildsOwnTask(t *testing.T) {
	cp := newChildPhone(t)
	page := cp.screen.get("/tasks", "en")
	if page.status != http.StatusOK || !strings.Contains(page.body, "Brush teeth") {
		t.Fatalf("child GET /tasks = %d\n%s", page.status, page.body)
	}
	if strings.Contains(page.body, `action="/tasks"`) || strings.Contains(page.body, "/delete") {
		t.Errorf("child's task page offers creating or deleting tasks:\n%s", page.body)
	}
	if r := cp.screen.post("/tasks/"+cp.miaTask+"/done", url.Values{"done": {"1"}}); r.status != http.StatusSeeOther || r.location != "/tasks" {
		t.Fatalf("tick = %d %q\n%s", r.status, r.location, r.body)
	}
	if got := taskState(t, cp.parent, cp.miaTask, ""); got["done"] != true {
		t.Fatalf("stored state = %v", got)
	}
	if r := cp.screen.post("/tasks/"+cp.miaTask+"/done", url.Values{"done": {"0"}}); r.status != http.StatusSeeOther {
		t.Fatalf("untick = %d", r.status)
	}
	if got := taskState(t, cp.parent, cp.miaTask, ""); got["done"] != false {
		t.Fatalf("after untick = %v", got)
	}
	// API parity.
	if status, _, _ := cp.phone.call(http.MethodPut, "/tasks/"+cp.miaTask+"/done", map[string]any{"done": true}); status != http.StatusOK {
		t.Fatalf("API tick = %d", status)
	}
	if got := taskState(t, cp.parent, cp.miaTask, ""); got["done"] != true {
		t.Fatalf("stored state after API tick = %v", got)
	}
}

func TestChildPhoneShowsOnlyTheChildsOwnTasks(t *testing.T) {
	cp := newChildPhone(t)
	cp.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": personNamed(t, cp.parent.b, "Sebastian").ID, "title": "Feed the cat", "repeat": "daily"})
	page := cp.screen.get("/tasks", "en")
	if page.status != http.StatusOK || !strings.Contains(page.body, "Brush teeth") {
		t.Fatalf("child GET /tasks = %d, want 200 with Mia's own task\n%s", page.status, page.body)
	}
	if strings.Contains(page.body, "Feed the cat") || strings.Contains(page.body, "Sebastian") {
		t.Errorf("Mia's phone shows Sebastian's tasks:\n%s", page.body)
	}
}

func TestChildPhonePageIsGerman(t *testing.T) {
	cp := newChildPhone(t)
	page := cp.screen.get("/tasks", "de")
	if page.status != http.StatusOK || !strings.Contains(page.body, "Deine Aufgaben für heute") {
		t.Fatalf("de = %d, want the German child intro\n%s", page.status, page.body)
	}
	if strings.Contains(page.body, "Your tasks for today") {
		t.Errorf("German page shows the English intro")
	}
}

// 5.14: Given a phone paired for Mia, when it tries to tick off Sebastian's
// task or delete an event, then this is refused.
func TestChildPhoneIsRefusedOtherTasksAndDeletingEvents(t *testing.T) {
	cp := newChildPhone(t)
	if r := cp.screen.post("/tasks/"+cp.sebastianTask+"/done", url.Values{"done": {"1"}}); r.status != http.StatusForbidden {
		t.Errorf("web tick of Sebastian's task = %d, want 403", r.status)
	}
	if status, _, _ := cp.phone.call(http.MethodPut, "/tasks/"+cp.sebastianTask+"/done", map[string]any{"done": true}); status != http.StatusForbidden {
		t.Errorf("API tick of Sebastian's task = %d, want 403", status)
	}
	if got := taskState(t, cp.parent, cp.sebastianTask, ""); got["done"] != false {
		t.Errorf("Sebastian's task changed: %v", got)
	}
	status, ev, _ := cp.parent.call(http.MethodPost, "/events", map[string]any{"title": "Dentist", "start": "2026-11-16T10:00:00+01:00", "end": "2026-11-16T11:00:00+01:00"})
	if status != http.StatusCreated {
		t.Fatalf("create event = %d %v", status, ev)
	}
	id := ev["id"].(string)
	if status, _, _ := cp.phone.call(http.MethodDelete, "/events/"+id, nil); status != http.StatusForbidden {
		t.Errorf("child deletes event = %d, want 403", status)
	}
	if status, _, _ := cp.phone.call(http.MethodDelete, "/tasks/"+cp.miaTask, nil); status != http.StatusForbidden {
		t.Errorf("child deletes task = %d, want 403", status)
	}
	if r := cp.screen.get("/tasks/"+cp.miaTask, "en"); r.status != http.StatusForbidden {
		t.Errorf("child GET /tasks/{id} = %d, want 403", r.status)
	}
	if r := cp.screen.post("/tasks/"+cp.miaTask+"/delete", url.Values{}); r.status != http.StatusForbidden {
		t.Errorf("web delete of task = %d, want 403", r.status)
	}
	if r := cp.screen.post("/tasks", url.Values{"person": {personNamed(t, cp.parent.b, "Mia").ID}, "title": {"x"}, "repeat": {"daily"}}); r.status != http.StatusForbidden {
		t.Errorf("web create task = %d, want 403", r.status)
	}
}

// 5.14: Given a phone paired for Mia, then it shows no settings or device management.
func TestChildPhoneShowsNoSettingsOrDeviceManagement(t *testing.T) {
	cp := newChildPhone(t)
	for _, page := range []string{"/", "/tasks"} {
		body := cp.screen.get(page, "en").body
		for _, href := range []string{"/settings", "/phones", "/displays", "/caldav", "/calendars"} {
			if strings.Contains(body, `href="`+href+`"`) {
				t.Errorf("%s links to %s on a child's phone", page, href)
			}
		}
	}
	for _, page := range []string{"/settings", "/phones", "/displays", "/caldav", "/calendars"} {
		if r := cp.screen.get(page, "en"); r.status != http.StatusForbidden {
			t.Errorf("child GET %s = %d, want 403", page, r.status)
		}
	}
	for _, path := range []string{"/phones", "/tokens", "/displays", "/settings"} {
		if status, _, _ := cp.phone.call(http.MethodGet, path, nil); status != http.StatusForbidden {
			t.Errorf("child API GET %s = %d, want 403", path, status)
		}
	}
	if r := cp.screen.post("/phones/pairings", url.Values{}); r.status != http.StatusForbidden {
		t.Errorf("child shows a QR code = %d, want 403", r.status)
	}
}
