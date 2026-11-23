package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// childPersonalDevice is a personal device paired for Mia through the real pairing flow (5.13),
// next to the parent who paired it and a task of Mia's and one of Sebastian's.
type childPersonalDevice struct {
	parent, personalDevice *apiClient
	screen                 *screen
	miaTask                string
	sebastianTask          string
}

func newChildPersonalDevice(t *testing.T) childPersonalDevice {
	t.Helper()
	parent := signedInAPI(t)
	b := parent.b
	mia := personNamed(t, b, "Mia")
	sebastian := personNamed(t, b, "Sebastian")
	scr := pairedPersonalDevice(t, b, mia, "Mia's tablet")
	pb := *b
	pb.client = scr.client
	personalDevice := &apiClient{b: &pb, router: parent.router, cookie: true}
	tb := taskBox{parent: parent}
	return childPersonalDevice{parent: parent, personalDevice: personalDevice, screen: scr, miaTask: tb.newDaily(t, mia.ID), sebastianTask: tb.newDaily(t, sebastian.ID)}
}

// 5.14: Given a personal device paired for Mia, when it ticks off Mia's task, then it is done.
func TestChildPersonalDeviceTicksOffTheChildsOwnTask(t *testing.T) {
	t.Parallel()
	cp := newChildPersonalDevice(t)
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
	if status, _, _ := cp.personalDevice.call(http.MethodPut, "/tasks/"+cp.miaTask+"/done", map[string]any{"done": true}); status != http.StatusOK {
		t.Fatalf("API tick = %d", status)
	}
	if got := taskState(t, cp.parent, cp.miaTask, ""); got["done"] != true {
		t.Fatalf("stored state after API tick = %v", got)
	}
}

func TestChildPersonalDeviceShowsOnlyTheChildsOwnTasks(t *testing.T) {
	t.Parallel()
	cp := newChildPersonalDevice(t)
	cp.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": personNamed(t, cp.parent.b, "Sebastian").ID, "title": "Feed the cat", "repeat": "daily"})
	page := cp.screen.get("/tasks", "en")
	if page.status != http.StatusOK || !strings.Contains(page.body, "Brush teeth") {
		t.Fatalf("child GET /tasks = %d, want 200 with Mia's own task\n%s", page.status, page.body)
	}
	if strings.Contains(page.body, "Feed the cat") || strings.Contains(page.body, "Sebastian") {
		t.Errorf("Mia's tablet shows Sebastian's tasks:\n%s", page.body)
	}
}

func TestChildPersonalDevicePageIsGerman(t *testing.T) {
	t.Parallel()
	cp := newChildPersonalDevice(t)
	page := cp.screen.get("/tasks", "de")
	if page.status != http.StatusOK || !strings.Contains(page.body, "Deine Aufgaben für heute") {
		t.Fatalf("de = %d, want the German child intro\n%s", page.status, page.body)
	}
	if strings.Contains(page.body, "Your tasks for today") {
		t.Errorf("German page shows the English intro")
	}
}

// 5.14: Given a personal device paired for Mia, when it tries to tick off Sebastian's
// task or delete an event, then this is refused.
func TestChildPersonalDeviceIsRefusedOtherTasksAndDeletingEvents(t *testing.T) {
	t.Parallel()
	cp := newChildPersonalDevice(t)
	if r := cp.screen.post("/tasks/"+cp.sebastianTask+"/done", url.Values{"done": {"1"}}); r.status != http.StatusForbidden {
		t.Errorf("web tick of Sebastian's task = %d, want 403", r.status)
	}
	if status, _, _ := cp.personalDevice.call(http.MethodPut, "/tasks/"+cp.sebastianTask+"/done", map[string]any{"done": true}); status != http.StatusForbidden {
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
	if status, _, _ := cp.personalDevice.call(http.MethodDelete, "/events/"+id, nil); status != http.StatusForbidden {
		t.Errorf("child deletes event = %d, want 403", status)
	}
	if status, _, _ := cp.personalDevice.call(http.MethodDelete, "/tasks/"+cp.miaTask, nil); status != http.StatusForbidden {
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

// 5.14: Given a personal device paired for Mia, then it shows no settings or device management.
func TestChildPersonalDeviceShowsNoSettingsOrDeviceManagement(t *testing.T) {
	t.Parallel()
	cp := newChildPersonalDevice(t)
	for _, page := range []string{"/", "/tasks"} {
		body := cp.screen.get(page, "en").body
		for _, href := range []string{"/settings", "/personal-devices", "/displays", "/caldav", "/calendars"} {
			if strings.Contains(body, `href="`+href+`"`) {
				t.Errorf("%s links to %s on a child's personal device", page, href)
			}
		}
	}
	for _, page := range []string{"/settings", "/personal-devices", "/displays", "/caldav", "/calendars"} {
		if r := cp.screen.get(page, "en"); r.status != http.StatusForbidden {
			t.Errorf("child GET %s = %d, want 403", page, r.status)
		}
	}
	for _, path := range []string{"/personal-devices", "/tokens", "/displays", "/settings"} {
		if status, _, _ := cp.personalDevice.call(http.MethodGet, path, nil); status != http.StatusForbidden {
			t.Errorf("child API GET %s = %d, want 403", path, status)
		}
	}
	if r := cp.screen.post("/personal-devices/pairings", url.Values{}); r.status != http.StatusForbidden {
		t.Errorf("child shows a QR code = %d, want 403", r.status)
	}
}

// Q46 (ticket 197): a child's personal device has no Sign out anywhere; a parent unpairs it.
func TestChildsPersonalDeviceOffersNoSignOut(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	personalDevice := pairedPersonalDevice(t, b, personNamed(t, b, "Mia"), "Mia's tablet")
	for _, page := range []string{"/", "/tasks", "/lists"} {
		if body := personalDevice.get(page, "en").body; strings.Contains(body, `action="/logout"`) || strings.Contains(body, "Sign out") {
			t.Errorf("child's personal device offers Sign out on %s", page)
		}
	}
	// Parents are unchanged, on the browser and on their own personal device.
	if body := b.get("/").body; !strings.Contains(body, `action="/logout"`) {
		t.Error("parent's browser lost Sign out")
	}
	pp := pairedPersonalDevice(t, b, personNamed(t, b, "Mama"), "Mama's tablet")
	if body := pp.get("/", "en").body; !strings.Contains(body, `action="/logout"`) {
		t.Error("parent's personal device lost Sign out")
	}
}

func TestSignOutPostFromAChildsPersonalDeviceIsRefusedAndKeepsTheSession(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	personalDevice := pairedPersonalDevice(t, b, personNamed(t, b, "Mia"), "Mia's tablet")
	for lang, want := range map[string]string{"en": "A parent removes", "de": "Eltern entfernen"} {
		r := personalDevice.postLang("/logout", url.Values{}, lang)
		if r.status != http.StatusForbidden || !strings.Contains(r.body, want) {
			t.Errorf("%s: child's sign-out = %d %q, want 403 with %q", lang, r.status, r.body, want)
		}
	}
	if r := personalDevice.get("/", "en"); r.status != http.StatusOK {
		t.Errorf("child's personal device after refused sign-out = %d, want still signed in", r.status)
	}
}

// Q46 / PO criterion on #197, drawn by #198: parents see how to hand a personal device to
// someone else or switch the person (pair it for another person), once, at the
// place where they do it: one sentence on the overview, a line under the box
// title on a child's personal device, du-style in EN and DE.
func TestParentsSeeHowToHandAPersonalDeviceOnOrSwitchThePerson(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	pairedPersonalDevice(t, b, personNamed(t, b, "Mia"), "Mia's tablet")
	m := personalDeviceIDRe.FindStringSubmatch(b.get("/personal-devices").body)
	if m == nil {
		t.Fatal("no paired personal device on the personal devices page")
	}
	for path, wants := range map[string]map[string]string{
		"/personal-devices":         {"en": "Tap a device to remove it or pair it for another person.", "de": "Tippe auf ein Gerät, um es zu entfernen oder für eine andere Person zu koppeln."},
		"/personal-devices/" + m[1]: {"en": "Does someone else get this device? Pair it again in one step here. Mia cannot sign out on this device.", "de": "Bekommt jemand anderes dieses Gerät? Hier koppelst du es in einem Schritt neu. Mia kann sich auf dem Gerät nicht selbst abmelden."},
	} {
		for lang, want := range wants {
			r := b.do(http.MethodGet, path, nil, map[string]string{"Accept-Language": lang})
			if r.status != http.StatusOK || !strings.Contains(r.body, want) {
				t.Errorf("%s (%s) = %d, lacks the hint %q", path, lang, r.status, want)
			}
		}
	}
}

// A child's personal device that opens someone else's invitation link gets no Sign out
// button, only the way home and a note that a parent's device is needed.
func TestChildsPersonalDeviceOpeningAForeignInvitationOffersNoSignOut(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	tok := e.create()
	personalDevice := pairedPersonalDevice(t, e.b, e.mia, "Mia's tablet")
	for lang, want := range map[string]string{"en": "device is needed for this", "de": "Gerät der Eltern nötig"} {
		r := personalDevice.get("/invite/"+tok, lang)
		if r.status != http.StatusOK || !strings.Contains(r.body, want) || !strings.Contains(r.body, `href="/"`) {
			t.Errorf("%s: foreign invitation on a child's personal device = %d, lacks %q or the way home\n%s", lang, r.status, want, r.body)
		}
		if strings.Contains(r.body, `action="/logout"`) || strings.Contains(r.body, "Sign out") || strings.Contains(r.body, "Abmelden") {
			t.Errorf("%s: child's personal device offers Sign out on the invitation page", lang)
		}
	}
	// A parent's own personal device keeps the button.
	pp := pairedPersonalDevice(t, e.b, personNamed(t, e.b, "Mama"), "Mama's tablet")
	if body := pp.get("/invite/"+tok, "en").body; !strings.Contains(body, `action="/logout"`) {
		t.Error("parent's personal device lost Sign out on the invitation page")
	}
}
