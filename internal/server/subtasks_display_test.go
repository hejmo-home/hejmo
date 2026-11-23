package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/task"
)

// bagFor adds "Pack school bag" (2 stars) for a person with three subtasks.
func (f *familyBox) bagFor(t *testing.T, who string, routineID string) (task.Task, []task.Subtask) {
	t.Helper()
	k, err := f.db.AddTask(f.householdID, task.Task{PersonID: f.people[who].ID, Title: "Pack school bag", Emoji: "🎒", Repeat: task.Daily, Date: "2026-11-14", Stars: 2, RoutineID: routineID})
	if err != nil {
		t.Fatal(err)
	}
	var subs []task.Subtask
	for _, title := range []string{"Exercise books", "Pencil case", "Lunch box"} {
		s, err := f.db.AddSubtask(f.householdID, k.ID, task.Subtask{Title: title}, "2026-11-14", f.at(14, 7, 0))
		if err != nil {
			t.Fatal(err)
		}
		subs = append(subs, s)
	}
	return k, subs
}

var subGroupRe = regexp.MustCompile(`(?s)<li class="has-subs"[^>]*>.*?</li>\s*</ol>\s*</div></li>`)

// Acceptance: subtasks are tickable on displays. A task with subtasks is one
// card: the task's own checkbox with stars, then every subtask with its own
// checkbox and no stars, the progress as boxes and in words.
func TestPersonalViewShowsATaskWithItsSubtasksAsOneCard(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	k, subs := f.bagFor(t, "Mia", "")
	if err := f.db.SetTaskDone(f.householdID, f.task(t, "Mia", "Feed the cat", task.Daily).ID, "2026-11-14", true, "", f.at(14, 7, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.SetSubtaskDone(f.householdID, k.ID, subs[0].ID, "2026-11-14", true, "", f.at(14, 7, 0)); err != nil {
		t.Fatal(err)
	}
	page := f.personalPage(t, "en", "Mia", 14, 8).body
	card := subGroupRe.FindString(page)
	if card == "" {
		t.Fatalf("no card with subtasks:\n%s", page)
	}
	for _, want := range []string{
		`class="big-check head"`, `action="/display/tasks/` + k.ID + `/done"`,
		`aria-label="Pack school bag: Open, 1 of 3 subtasks done, tap to tick all, 2 Stars"`,
		`<span>1 of 3 done</span>`, `<i data-done>`,
		`action="/display/tasks/` + k.ID + `/subtasks/` + subs[1].ID + `/done"`,
		`aria-label="Pencil case, subtask of Pack school bag: Open, tap to tick off"`,
		`aria-label="Exercise books, subtask of Pack school bag: Done, tap to open again"`,
		`class="big-check sub"`,
	} {
		if !strings.Contains(card, want) {
			t.Errorf("card lacks %q:\n%s", want, card)
		}
	}
	if n := strings.Count(card, `class="task-stars"`); n != 1 {
		t.Errorf("%d star badges in the card, want 1 (stars belong to the task):\n%s", n, card)
	}
	if n := strings.Count(card, "<i>") + strings.Count(card, "<i data-done>"); n != 3 {
		t.Errorf("%d progress boxes, want one per subtask (3)", n)
	}
	// The head counts tasks, not subtasks: 1 of 2 tasks done.
	if !strings.Contains(page, "1 of 2 tasks done") {
		t.Errorf("head does not count tasks:\n%s", page)
	}
	if got := progressOf(t, f.view(t, "en", f.at(14, 8, 0)), "Mia"); got != "1/2" {
		t.Errorf("family view progress = %q, want 1/2", got)
	}
	if de := f.personalPage(t, "de", "Mia", 14, 8).body; !strings.Contains(de, "1 von 3 erledigt") || !strings.Contains(de, "Teilaufgabe von Pack school bag") {
		t.Errorf("German card:\n%s", de)
	}
}

// Acceptance: Given the last subtask ticked on a display, the task is done and
// its stars are credited once; unticking a subtask reopens it.
func TestDisplayTicksSubtasksAndTheTask(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	k, subs := f.bagFor(t, "Mia", "")
	f.now = f.at(14, 7, 0)
	for _, s := range subs {
		if r := f.display.post("/display/tasks/"+k.ID+"/subtasks/"+s.ID+"/done", url.Values{"done": {"1"}}); r.status != http.StatusSeeOther || r.location != "/display/person/"+f.people["Mia"].ID {
			t.Fatalf("tick = %d %q", r.status, r.location)
		}
	}
	if done, _ := f.db.TaskDone(k.ID, "2026-11-14"); !done {
		t.Fatal("task not done after its last subtask")
	}
	acc, _ := f.db.StarAccount(f.householdID, f.people["Mia"].ID, 10)
	if acc.Balance != 2 {
		t.Fatalf("balance = %d, want 2", acc.Balance)
	}
	if r := f.display.post("/display/tasks/"+k.ID+"/subtasks/"+subs[1].ID+"/done", url.Values{"done": {"0"}}); r.status != http.StatusSeeOther {
		t.Fatalf("untick = %d", r.status)
	}
	acc, _ = f.db.StarAccount(f.householdID, f.people["Mia"].ID, 10)
	if done, _ := f.db.TaskDone(k.ID, "2026-11-14"); done || acc.Balance != 0 {
		t.Fatalf("task done = %v, balance = %d after unticking a subtask", done, acc.Balance)
	}
	// Tapping the task ticks all subtasks.
	if r := f.display.post("/display/tasks/"+k.ID+"/done", url.Values{"done": {"1"}}); r.status != http.StatusSeeOther {
		t.Fatalf("tick task = %d", r.status)
	}
	list, _ := f.db.Subtasks(f.householdID, k.ID, "2026-11-14")
	for _, s := range list {
		if !s.Done {
			t.Fatalf("%s should be ticked with the task", s.Title)
		}
	}
	// A subtask of a deleted task or a page of an earlier day ticks nothing.
	if r := f.display.post("/display/tasks/"+k.ID+"/subtasks/"+subs[0].ID+"/done", url.Values{"done": {"0"}, "day": {"2026-11-13"}}); r.status != http.StatusSeeOther {
		t.Fatalf("stale page = %d", r.status)
	}
	if list, _ := f.db.Subtasks(f.householdID, k.ID, "2026-11-14"); !list[0].Done {
		t.Fatal("a page of an earlier day must tick nothing")
	}
	if r := f.display.post("/display/tasks/"+k.ID+"/subtasks/nope/done", url.Values{"done": {"1"}}); r.status != http.StatusSeeOther {
		t.Fatalf("unknown subtask = %d, want a redirect", r.status)
	}
}

// Inside a routine the "next" marker moves to the first open subtask of the
// next task; outside routines nothing is marked.
func TestNextMarkerSitsOnTheFirstOpenSubtaskInARoutine(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	r, steps := f.routineFor(t, "Mia", "Morning routine", task.Morning, "Brush teeth")
	_ = steps
	_, subs := f.bagFor(t, "Mia", r.ID)
	if err := f.db.SetTaskDone(f.householdID, steps[0].ID, "2026-11-14", true, "", f.at(14, 7, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.SetSubtaskDone(f.householdID, subs[0].TaskID, subs[0].ID, "2026-11-14", true, "", f.at(14, 7, 0)); err != nil {
		t.Fatal(err)
	}
	box := routineBox(t, f.personalPage(t, "en", "Mia", 14, 8).body)
	if n := strings.Count(box, "data-next"); n != 1 {
		t.Fatalf("%d next marks:\n%s", n, box)
	}
	if !strings.Contains(box, `<li data-done="false" data-next><form method="post" action="/display/tasks/`+subs[1].TaskID+`/subtasks/`+subs[1].ID+`/done">`) {
		t.Errorf("the next mark is not on the first open subtask:\n%s", box)
	}
	if !strings.Contains(box, "Pencil case, subtask of Pack school bag: Open, tap to tick off, next") {
		t.Errorf("accessible name does not say next:\n%s", box)
	}
	if strings.Contains(box, `class="has-subs" data-done="false" data-next`) {
		t.Errorf("the task itself is marked next:\n%s", box)
	}
}

// Subtasks outside a routine are never marked next.
func TestNothingIsMarkedNextOutsideRoutines(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	f.bagFor(t, "Mia", "")
	if page := f.personalPage(t, "en", "Mia", 14, 8).body; strings.Contains(page, "data-next") {
		t.Errorf("a next mark outside a routine:\n%s", page)
	}
}

// Acceptance: subtasks are tickable on personal devices: the child's tasks page shows
// the same cards, and the child ticks only subtasks of their own tasks.
func TestChildPersonalDeviceTicksSubtasks(t *testing.T) {
	t.Parallel()
	cp := newChildPersonalDevice(t)
	tb := taskBox{parent: cp.parent}
	mia := personNamed(t, cp.parent.b, "Mia")
	_, k, _ := cp.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": mia.ID, "title": "Tidy room", "repeat": "daily", "stars": 3})
	id := k["id"].(string)
	var subs []string
	for _, title := range []string{"Clothes", "Books"} {
		_, got, _ := cp.parent.call(http.MethodPost, "/tasks/"+id+"/subtasks", map[string]any{"title": title})
		l := got["subtasks"].([]any)
		subs = append(subs, l[len(l)-1].(map[string]any)["id"].(string))
	}
	_ = tb
	page := cp.screen.get("/tasks", "en").body
	for _, want := range []string{`class="has-subs"`, `action="/tasks/` + id + `/subtasks/` + subs[0] + `/done"`, "0 of 2 done"} {
		if !strings.Contains(page, want) {
			t.Fatalf("child page lacks %q:\n%s", want, page)
		}
	}
	for _, s := range subs {
		if r := cp.screen.post("/tasks/"+id+"/subtasks/"+s+"/done", url.Values{"done": {"1"}}); r.status != http.StatusSeeOther || r.location != "/tasks" {
			t.Fatalf("tick = %d %q", r.status, r.location)
		}
	}
	if got := taskState(t, cp.parent, id, ""); got["done"] != true {
		t.Fatalf("task = %v, want done after both subtasks", got)
	}
	// Not the child's own task.
	_, other, _ := cp.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": personNamed(t, cp.parent.b, "Sebastian").ID, "title": "Taxes", "repeat": "daily"})
	oid := other["id"].(string)
	_, got, _ := cp.parent.call(http.MethodPost, "/tasks/"+oid+"/subtasks", map[string]any{"title": "Forms"})
	osub := got["subtasks"].([]any)[0].(map[string]any)["id"].(string)
	if r := cp.screen.post("/tasks/"+oid+"/subtasks/"+osub+"/done", url.Values{"done": {"1"}}); r.status != http.StatusForbidden {
		t.Fatalf("tick of another's subtask = %d, want 403", r.status)
	}
}
