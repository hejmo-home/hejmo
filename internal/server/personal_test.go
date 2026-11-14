package server_test

import (
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/task"
)

// personalPage fetches a person's view on the display, the clock at the given time.
func (f *familyBox) personalPage(t *testing.T, lang, who string, day, hour int) resp {
	t.Helper()
	f.now = f.at(day, hour, 0)
	return f.display.get("/display/person/"+f.people[who].ID, lang)
}

func (f *familyBox) task(t *testing.T, who, title string, repeat task.Repeat) task.Task {
	t.Helper()
	tk, err := f.db.AddTask(f.householdID, task.Task{PersonID: f.people[who].ID, Title: title, Repeat: repeat, Date: "2026-11-14"})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

// 5.6: tapping a profile on the display opens the personal view: each person's
// name in the family view is a link of its own.
func TestFamilyViewColumnsLinkToThePersonalView(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	page := f.view(t, "en", f.at(14, 9, 0))
	for n, p := range f.people {
		if !strings.Contains(page, `href="/display/person/`+p.ID+`"`) {
			t.Errorf("no link to %s's view", n)
		}
	}
}

// 5.6 (ADR 0015): the personal view shows the person's events for today and the
// next 6 days, nothing of other people and nothing beyond.
func TestPersonalViewShowsEventsOfSevenDays(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.event(t, "Swimming", f.at(14, 16, 0), f.at(14, 17, 0), false, "Mia")
	f.event(t, "Piano", f.at(20, 15, 0), f.at(20, 16, 0), false, "Mia")
	f.event(t, "Dentist", f.at(15, 9, 0), f.at(15, 10, 0), false, "Mama")
	f.event(t, "Later", f.at(21, 9, 0), f.at(21, 10, 0), false, "Mia")
	r := f.personalPage(t, "en", "Mia", 14, 9)
	if r.status != 200 {
		t.Fatalf("status = %d", r.status)
	}
	for _, want := range []string{"Swimming", "16:00–17:00", "Piano", "Mia"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, not := range []string{"Dentist", "Later"} {
		if strings.Contains(r.body, not) {
			t.Errorf("page shows %q", not)
		}
	}
	if n := strings.Count(r.body, "data-day="); n != 7 {
		t.Errorf("days = %d, want 7", n)
	}
}

// 5.6: the personal view is German on a German browser.
func TestPersonalViewIsGerman(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	r := f.personalPage(t, "de", "Mia", 14, 9)
	if !strings.Contains(r.body, "Zurück zur Familienansicht") {
		t.Errorf("no German back link:\n%s", r.body)
	}
}

// 5.6: only a paired display opens it; unknown people are not found.
func TestPersonalViewNeedsAPairedDisplayAndAKnownPerson(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	stranger := newScreen(f.box)
	if r := stranger.get("/display/person/"+f.people["Mia"].ID, "en"); r.status != 303 || r.location != "/display" {
		t.Errorf("unpaired display: %d %q", r.status, r.location)
	}
	// Never a bare error page: it has no display.js and would leave the kiosk stuck.
	if r := f.display.get("/display/person/00000000-0000-4000-8000-000000000000", "en"); r.status != 303 || r.location != "/display" {
		t.Errorf("unknown person = %d %q, want 303 /display", r.status, r.location)
	}
	if r := f.display.get("/display/person/nonsense", "en"); r.status != 303 || r.location != "/display" {
		t.Errorf("malformed id = %d %q, want 303 /display", r.status, r.location)
	}
}

var (
	returnSecondsRe = regexp.MustCompile(`data-return-seconds="([0-9]+)"`)
	returnURLRe     = regexp.MustCompile(`data-return-url="([^"]+)"`)
)

// 5.6: Given a tap on a profile, when no interaction follows for 30 seconds,
// then the display returns to the family view. The page carries the delay and
// the target; display.js does the counting.
func TestPersonalViewReturnsToTheFamilyViewAfter30Seconds(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	r := f.personalPage(t, "en", "Mia", 14, 9)
	if m := returnSecondsRe.FindStringSubmatch(r.body); m == nil || m[1] != "30" {
		t.Errorf("return delay = %v, want 30", m)
	}
	if m := returnURLRe.FindStringSubmatch(r.body); m == nil || m[1] != "/display" {
		t.Errorf("return target = %v, want /display", m)
	}
	if !strings.Contains(r.body, `href="/display"`) {
		t.Error("no link back to the family view")
	}
}

// 5.6: Given interaction within 30 seconds, then the timer restarts. The
// script restarts the countdown on every touch, key press and scroll.
func TestDisplayScriptRestartsTheTimerOnInteraction(t *testing.T) {
	b, err := os.ReadFile("../../web/static/display.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	for _, ev := range []string{"pointerdown", "touchstart", "keydown", "scroll"} {
		if !strings.Contains(js, `"`+ev+`"`) || !strings.Contains(js, "addEventListener") {
			t.Errorf("display.js does not restart the timer on %s", ev)
		}
	}
	for _, want := range []string{"data-return-seconds", "data-return-url", "clearTimeout"} {
		if !strings.Contains(js, want) {
			t.Errorf("display.js lacks %s", want)
		}
	}
}

// 5.6 (ADR 0015): today's tasks of the person show with large checkboxes, ticked
// or not; other people's tasks and tasks not due today stay out.
func TestPersonalViewShowsTodaysTasksWithLargeCheckboxes(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	open := f.task(t, "Mia", "Homework", task.Daily)
	done := f.task(t, "Mia", "Feed the cat", task.Daily)
	f.task(t, "Papa", "Take out bins", task.Daily)
	if _, err := f.db.AddTask(f.householdID, task.Task{PersonID: f.people["Mia"].ID, Title: "Next week", Repeat: task.Once, Date: "2026-11-20"}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetTaskDone(f.householdID, done.ID, "2026-11-14", true, "", f.at(14, 8, 0)); err != nil {
		t.Fatal(err)
	}
	body := f.personalPage(t, "en", "Mia", 14, 9).body
	if !strings.Contains(body, "Homework") || !strings.Contains(body, "Feed the cat") {
		t.Fatalf("tasks missing:\n%s", body)
	}
	for _, not := range []string{"Take out bins", "Next week"} {
		if strings.Contains(body, not) {
			t.Errorf("page shows %q", not)
		}
	}
	if !strings.Contains(body, `data-task="`+open.ID+`" data-done="false"`) || !strings.Contains(body, `data-task="`+done.ID+`" data-done="true"`) {
		t.Errorf("task state not marked:\n%s", body)
	}
	if !strings.Contains(body, `class="big-check"`) {
		t.Error("no large checkbox")
	}
}

// 5.6 (ADR 0015): a display may tick tasks off and open again without the parent PIN.
func TestDisplayTicksATaskOffAndOpenAgain(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	tk := f.task(t, "Mia", "Homework", task.Daily)
	f.now = f.at(14, 9, 0)
	r := f.display.post("/display/tasks/"+tk.ID+"/done", url.Values{"done": {"1"}})
	if r.status != 303 || r.location != "/display/person/"+f.people["Mia"].ID {
		t.Fatalf("tick = %d %q", r.status, r.location)
	}
	if ok, _ := f.db.TaskDone(tk.ID, "2026-11-14"); !ok {
		t.Fatal("task not ticked")
	}
	if !strings.Contains(f.personalPage(t, "en", "Mia", 14, 9).body, `data-task="`+tk.ID+`" data-done="true"`) {
		t.Error("page does not show the tick")
	}
	if r := f.display.post("/display/tasks/"+tk.ID+"/done", url.Values{"done": {"0"}}); r.status != 303 {
		t.Fatalf("untick = %d", r.status)
	}
	if ok, _ := f.db.TaskDone(tk.ID, "2026-11-14"); ok {
		t.Fatal("task still ticked")
	}
}

// 5.6: only a paired display may tick; a task that is not due today or does not exist is refused.
func TestDisplayTickingIsRefusedForUnpairedDisplaysAndUnknownTasks(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	tk := f.task(t, "Mia", "Homework", task.Daily)
	f.now = f.at(14, 9, 0)
	if r := newScreen(f.box).post("/display/tasks/"+tk.ID+"/done", url.Values{"done": {"1"}}); r.status != 303 || r.location != "/display" {
		t.Errorf("unpaired = %d %q, want 303 /display", r.status, r.location)
	}
	if ok, _ := f.db.TaskDone(tk.ID, "2026-11-14"); ok {
		t.Error("an unpaired display ticked a task")
	}
}

// A failed tick goes back to the personal view (or the display home), never to a bare error page
// that would leave the kiosk stuck without display.js.
func TestDisplayFailedTicksGoBackToTheDisplay(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	tk := f.task(t, "Mia", "Homework", task.Daily)
	f.now = f.at(14, 9, 0)
	home := "/display/person/" + f.people["Mia"].ID
	if r := f.display.post("/display/tasks/00000000-0000-4000-8000-000000000000/done", url.Values{"done": {"1"}}); r.status != 303 || r.location != "/display" {
		t.Errorf("unknown task = %d %q, want 303 /display", r.status, r.location)
	}
	if r := f.display.post("/display/tasks/"+tk.ID+"/done", url.Values{}); r.status != 303 || r.location != home {
		t.Errorf("missing done = %d %q, want 303 %s", r.status, r.location, home)
	}
	future, _ := f.db.AddTask(f.householdID, task.Task{PersonID: f.people["Mia"].ID, Title: "Later", Repeat: task.Once, Date: "2026-11-20"})
	if r := f.display.post("/display/tasks/"+future.ID+"/done", url.Values{"done": {"1"}}); r.status != 303 || r.location != home {
		t.Errorf("task not due = %d %q, want 303 %s", r.status, r.location, home)
	}
}

// The tick carries the day the page was rendered for. Like PUT /tasks/{id}/done
// (ADR 0015: the REST API can do everything a display can) it only ticks today:
// a tap on a page left over from an earlier day ticks nothing and shows today.
func TestDisplayTickRefusesAnyDayButToday(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	tk := f.task(t, "Mia", "Homework", task.Daily)
	f.now = f.at(14, 9, 0)
	if !strings.Contains(f.personalPage(t, "en", "Mia", 14, 9).body, `name="day" value="2026-11-14"`) {
		t.Error("form does not carry the day")
	}
	home := "/display/person/" + f.people["Mia"].ID
	for _, day := range []string{"2026-01-01", "2026-11-13", "2026-11-15"} {
		if r := f.display.post("/display/tasks/"+tk.ID+"/done", url.Values{"done": {"1"}, "day": {day}}); r.status != 303 || r.location != home {
			t.Errorf("day %s = %d %q, want 303 %s", day, r.status, r.location, home)
		}
		if ok, _ := f.db.TaskDone(tk.ID, day); ok {
			t.Errorf("day %s was ticked", day)
		}
	}
	f.display.post("/display/tasks/"+tk.ID+"/done", url.Values{"done": {"1"}, "day": {"2026-11-14"}})
	if ok, _ := f.db.TaskDone(tk.ID, "2026-11-14"); !ok {
		t.Error("today was not ticked")
	}
}

var (
	eventsURLRe = regexp.MustCompile(`data-events-url="([^"]+)"`)
	viewURLRe   = regexp.MustCompile(`data-view-url="([^"]+)"`)
)

// 5.6 x 5.5: Given a personal view is open, when an event of the person changes,
// then the box announces a new version of that view and serves its markup for a
// partial refresh. Changes of other people and the clock do not move it.
func TestPersonalViewReceivesLiveUpdates(t *testing.T) {
	l := newLiveBox(t, 10*time.Millisecond)
	id := l.people["Mia"].ID
	_, _, page := l.get("/display/person/" + id)
	pageVersion := versionRe.FindStringSubmatch(page)
	events, view := eventsURLRe.FindStringSubmatch(page), viewURLRe.FindStringSubmatch(page)
	if pageVersion == nil || events == nil || view == nil {
		t.Fatalf("personal page lacks data-version, data-events-url or data-view-url:\n%s", page)
	}
	if !strings.Contains(page, "data-view") || !strings.Contains(page, "data-clock-zone=") {
		t.Fatalf("personal page has no swappable view or clock zone:\n%s", page)
	}
	s := l.openPath(t, strings.ReplaceAll(events[1], "&amp;", "&"))
	if v := s.first(t); v != pageVersion[1] {
		t.Fatalf("announced %q on connect, page shows %q", v, pageVersion[1])
	}

	l.event(t, "Dentist", l.at(14, 9, 0), l.at(14, 10, 0), false, "Mama") // not Mia's
	l.clock.set(l.at(14, 8, 1))                                           // a new minute alone
	if v, ok := s.next(t, 400*time.Millisecond); ok {
		t.Fatalf("announced %q without a change to Mia's view", v)
	}

	l.event(t, "Swimming", l.at(14, 16, 0), l.at(14, 17, 0), false, "Mia")
	v, ok := s.next(t, 2*time.Second)
	if !ok || v == pageVersion[1] {
		t.Fatalf("no new version after Mia's event changed (got %q, ok=%v)", v, ok)
	}
	code, hdr, body := l.get(view[1])
	if code != 200 || !strings.Contains(body, "Swimming") || !strings.Contains(body, "08:01") || strings.Contains(body, "<html") {
		t.Fatalf("GET %s = %d, want Mia's view markup alone with the event:\n%s", view[1], code, body)
	}
	if hdr.Get("X-Hejmo-View") != v {
		t.Fatalf("view version %q, announced %q", hdr.Get("X-Hejmo-View"), v)
	}

	// A task of hers changes the view too.
	l.task(t, "Mia", "Tidy room", task.Daily)
	if v2, ok := s.next(t, 2*time.Second); !ok || v2 == v {
		t.Fatalf("no new version after a task appeared (got %q, ok=%v)", v2, ok)
	}
}

// 5.6 x 5.5: the live endpoints of a personal view are for paired displays, and
// a person who is gone sends the display back instead of leaving it stuck.
func TestPersonalLiveEndpointsNeedAPairedDisplayAndAKnownPerson(t *testing.T) {
	l := newLiveBox(t, 10*time.Millisecond)
	id := l.people["Mia"].ID
	stranger := newScreen(l.box)
	req, _ := http.NewRequest(http.MethodGet, l.url+"/display/person/"+id+"/view", nil)
	res, err := stranger.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("unpaired GET personal view = %d, want 403", res.StatusCode)
	}
	if code, _, _ := l.get("/display/person/nobody/view"); code != http.StatusNotFound {
		t.Fatalf("unknown person view = %d, want 404", code)
	}
	if v, ok := l.openPath(t, "/display/events?person=nobody").next(t, 2*time.Second); !ok || v != "gone" {
		t.Fatalf("unknown person announced %q (ok=%v), want gone", v, ok)
	}
}

// 5.6 x 5.5: Given a personal view that is kept current over SSE, then the
// return to the family view still comes after 30 seconds without interaction:
// the page carries both, and a refresh is no interaction (the script restarts
// the countdown only from input events, once at load).
func TestPersonalViewKeepsItsReturnTimerWhileLive(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	r := f.personalPage(t, "en", "Mia", 14, 9)
	for _, want := range []string{`data-return-seconds="30"`, `data-return-url="/display"`, `data-events-url=`, `data-view-url=`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("personal page lacks %s", want)
		}
	}
	b, err := os.ReadFile("../../web/static/display.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	if n := strings.Count(js, "restart()"); n != 1 {
		t.Errorf("display.js calls restart() %d times, want once (at load); refreshes must not restart the countdown", n)
	}
	for _, want := range []string{"data-events-url", "data-view-url", "new EventSource(eventsURL)", "fetch(viewURL"} {
		if !strings.Contains(js, want) {
			t.Errorf("display.js lacks %q", want)
		}
	}
}
