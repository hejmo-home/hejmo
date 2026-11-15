package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/task"
)

var progressRe = regexp.MustCompile(`data-progress="(\d+/\d+)"`)

// progressOf is the progress bar text of a person's column, "" without a bar.
func progressOf(t *testing.T, page, who string) string {
	t.Helper()
	for _, m := range columnRe.FindAllStringSubmatch(page, -1) {
		if m[1] == who {
			if p := progressRe.FindStringSubmatch(m[0]); p != nil {
				return p[1]
			}
		}
	}
	return ""
}

// 6.6 (ADR 0016): each column shows the person's task progress today as
// done/total; a person without tasks today shows no bar.
func TestFamilyViewColumnsShowTodaysTaskProgress(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	a := f.task(t, "Mia", "Homework", task.Daily)
	f.task(t, "Mia", "Feed the cat", task.Daily)
	f.task(t, "Mia", "Piano", task.Daily)
	f.task(t, "Papa", "Bins", task.Daily)
	if _, err := f.db.AddTask(f.householdID, task.Task{PersonID: f.people["Mia"].ID, Title: "Next week", Repeat: task.Once, Date: "2026-11-20"}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetTaskDone(f.householdID, a.ID, "2026-11-14", true, "", f.at(14, 8, 0)); err != nil {
		t.Fatal(err)
	}
	page := f.view(t, "en", f.at(14, 9, 0))
	for who, want := range map[string]string{"Mia": "1/3", "Papa": "0/1", "Mama": "", "Sebastian": ""} {
		if got := progressOf(t, page, who); got != want {
			t.Errorf("%s: progress %q, want %q", who, got, want)
		}
	}
	if strings.Contains(page, `data-tile="tasks"`) {
		t.Error("task tile in the footer (ADR 0016: progress is in the columns)")
	}
}

// 6.6 criterion: given a parent on the go, when they confirm a child's task in
// the web interface, then it is immediately visible as done on the display:
// in the personal view and in the progress of the family view.
func TestParentsConfirmationShowsOnTheDisplayImmediately(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	tk := f.task(t, "Mia", "Homework", task.Daily)
	f.now = f.at(14, 9, 0)
	if got := progressOf(t, f.view(t, "en", f.at(14, 9, 0)), "Mia"); got != "0/1" {
		t.Fatalf("before: %q", got)
	}
	if r := f.post("/tasks/"+tk.ID+"/done", url.Values{"done": {"1"}}); r.status != http.StatusSeeOther {
		t.Fatalf("confirm = %d", r.status)
	}
	if got := progressOf(t, f.view(t, "en", f.at(14, 9, 0)), "Mia"); got != "1/1" {
		t.Errorf("family view after confirm: %q, want 1/1", got)
	}
	if !strings.Contains(f.personalPage(t, "en", "Mia", 14, 9).body, `data-task="`+tk.ID+`" data-done="true"`) {
		t.Error("personal view does not show the task as done")
	}
}

// 6.6 x 5.5: a tick, from either side, makes the box announce a new version of
// the family view and of the personal view within the live interval.
func TestTickingAnnouncesNewVersionsOfBothViews(t *testing.T) {
	l := newLiveBox(t, 10*time.Millisecond)
	tk := l.task(t, "Mia", "Homework", task.Daily)
	family := l.open(t)
	personal := l.openPath(t, "/display/events?person="+l.people["Mia"].ID)
	f0, p0 := family.first(t), personal.first(t)
	if r := l.post("/tasks/"+tk.ID+"/done", url.Values{"done": {"1"}}); r.status != http.StatusSeeOther {
		t.Fatalf("confirm = %d", r.status)
	}
	if v, ok := family.next(t, 2*time.Second); !ok || v == f0 {
		t.Errorf("family view not announced (%q, %v)", v, ok)
	}
	if v, ok := personal.next(t, 2*time.Second); !ok || v == p0 {
		t.Errorf("personal view not announced (%q, %v)", v, ok)
	}
	code, _, body := l.get("/display/view")
	if code != 200 || !strings.Contains(body, `data-progress="1/1"`) {
		t.Errorf("family markup after tick = %d:\n%s", code, body)
	}
}

// 6.6 criterion: given a child ticking off a task on the display, then it is
// done in the web interface within 10 seconds (it is stored at once, and both
// the web page and the REST API read the same state), and the family view
// counts it.
func TestTickOnTheDisplayIsDoneInTheWebInterface(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	tk := f.task(t, "Mia", "Homework", task.Daily)
	f.now = f.at(14, 9, 0)
	if r := f.display.post("/display/tasks/"+tk.ID+"/done", url.Values{"done": {"1"}}); r.status != http.StatusSeeOther {
		t.Fatalf("tick = %d", r.status)
	}
	if page := f.get("/tasks"); !strings.Contains(page.body, "<s>Homework</s>") {
		t.Errorf("web interface does not show the task as done:\n%s", page.body)
	}
	if got := progressOf(t, f.view(t, "en", f.at(14, 9, 0)), "Mia"); got != "1/1" {
		t.Errorf("family view after display tick: %q", got)
	}
}
