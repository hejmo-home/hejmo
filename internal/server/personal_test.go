package server_test

import (
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

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
