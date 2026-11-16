package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func pageEN(b *box, path string) resp {
	return b.do(http.MethodGet, path, nil, map[string]string{"Accept-Language": "en"})
}

// Acceptance: Subtasks are editable in the web interface.
func TestParentAddsEditsMovesAndRemovesSubtasksOnTheTaskPage(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	id := tb.newDaily(t, tb.miaID)
	// Empty state: one sentence and the form.
	page := pageEN(b, "/tasks/"+id).body
	for _, want := range []string{`class="subcard"`, `class="subempty"`, `action="/tasks/` + id + `/subtasks"`, `name="title"`, `name="emoji"`, "Add subtask"} {
		if !strings.Contains(page, want) {
			t.Fatalf("empty task page lacks %q:\n%s", want, page)
		}
	}
	if r := b.post("/tasks/"+id+"/subtasks", url.Values{"title": {"Books"}, "emoji": {"📚"}}); r.status != http.StatusSeeOther || r.location != "/tasks/"+id+"#subs" {
		t.Fatalf("add = %d %q\n%s", r.status, r.location, r.body)
	}
	if page := pageEN(b, "/tasks/"+id).body; !strings.Contains(page, "Subtask “Books” added.") {
		t.Errorf("no success line naming the subtask:\n%s", page)
	}
	b.post("/tasks/"+id+"/subtasks", url.Values{"title": {"Pencil case"}})
	page = pageEN(b, "/tasks/"+id).body
	for _, want := range []string{"📚 Books", "Pencil case", "Today 0 of 2 done", "Open", `class="tickbox"`, "Change or remove", "Move Books down", "Move Pencil case up"} {
		if !strings.Contains(page, want) {
			t.Errorf("task page lacks %q:\n%s", want, page)
		}
	}
	_, k, _ := tb.parent.call(http.MethodGet, "/tasks", nil)
	_ = k
	got := taskState(t, tb.parent, id, "")
	subs := got["subtasks"].([]any)
	first, second := subs[0].(map[string]any)["id"].(string), subs[1].(map[string]any)["id"].(string)
	if r := b.post("/tasks/"+id+"/subtasks/"+second+"/move", url.Values{"direction": {"up"}}); r.status != http.StatusSeeOther {
		t.Fatalf("move = %d", r.status)
	}
	if got := taskState(t, tb.parent, id, "")["subtasks"].([]any); got[0].(map[string]any)["id"] != second {
		t.Fatalf("order after move = %v", got)
	}
	if r := b.post("/tasks/"+id+"/subtasks/"+first, url.Values{"title": {"Schoolbooks"}, "emoji": {"📘"}}); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	if got := taskState(t, tb.parent, id, "")["subtasks"].([]any); got[1].(map[string]any)["title"] != "Schoolbooks" {
		t.Fatalf("title after save = %v", got)
	}
	if r := b.post("/tasks/"+id+"/subtasks/"+first+"/delete", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("remove = %d", r.status)
	}
	if got := taskState(t, tb.parent, id, "")["subtasks"].([]any); len(got) != 1 {
		t.Fatalf("after remove = %v", got)
	}
	if r := b.post("/tasks/"+id+"/subtasks/"+first+"/delete", url.Values{}); r.status != http.StatusNotFound {
		t.Fatalf("second remove = %d, want 404", r.status)
	}
}

func TestSubtaskFormErrorsAppearAtTheFieldAndKeepTheEmoji(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	id := tb.newDaily(t, tb.miaID)
	r := b.do(http.MethodPost, "/tasks/"+id+"/subtasks", url.Values{"title": {" "}, "emoji": {"✏️"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("blank title = %d", r.status)
	}
	for _, want := range []string{`aria-invalid="true"`, `role="alert"`, "Please enter a title for the subtask.", `value="✏️"`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("error page lacks %q:\n%s", want, r.body)
		}
	}
	if r := b.post("/tasks/00000000-0000-4000-8000-000000000000/subtasks", url.Values{"title": {"x"}}); r.status != http.StatusNotFound {
		t.Fatalf("unknown task = %d", r.status)
	}
}

func TestSubtaskLimitOfTenReplacesTheFormWithASentence(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	id, _ := tb.schoolBag(t)
	for i := 3; i < 10; i++ {
		b.post("/tasks/"+id+"/subtasks", url.Values{"title": {"More"}})
	}
	page := pageEN(b, "/tasks/"+id).body
	if strings.Contains(page, `class="subadd"`) || !strings.Contains(page, "10 subtasks") {
		t.Errorf("at ten subtasks the form must give way to a sentence:\n%s", page)
	}
	if r := b.post("/tasks/"+id+"/subtasks", url.Values{"title": {"Eleven"}}); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("11th = %d, want 422", r.status)
	}
}

// Parents tick single subtasks on the task page; the success line names the
// consequence for the task and the stars. API parity with the REST routes.
func TestParentTicksSubtasksOnTheTaskPageAndSeesTheConsequence(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	id, subs := tb.schoolBag(t)
	for i, s := range subs {
		r := b.post("/tasks/"+id+"/subtasks/"+s+"/done", url.Values{"done": {"1"}})
		if r.status != http.StatusSeeOther || r.location != "/tasks/"+id+"#subs" {
			t.Fatalf("tick = %d %q", r.status, r.location)
		}
		line := successLine(pageEN(b, "/tasks/"+id).body)
		if last := i == len(subs)-1; last != strings.Contains(line, "is done and Mia gets 2 stars") || !strings.Contains(line, "ticked off") {
			t.Fatalf("after tick %d the success line is %q", i+1, line)
		}
	}
	if got := taskState(t, tb.parent, id, ""); got["done"] != true || tb.balance(t) != 2 {
		t.Fatalf("task = %v balance %v", got, tb.balance(t))
	}
	r := b.post("/tasks/"+id+"/subtasks/"+subs[2]+"/done", url.Values{"done": {"0"}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("untick = %d", r.status)
	}
	if line := successLine(pageEN(b, "/tasks/"+id).body); !strings.Contains(line, "is open again") || !strings.Contains(line, "2 stars are deducted") {
		t.Errorf("the success line does not say what unticking did: %q", line)
	}
	if tb.balance(t) != 0 {
		t.Fatalf("balance = %v", tb.balance(t))
	}
}

// The tasks page lists every subtask under its task and the tick button says
// that it ticks all of them.
func TestTasksPageListsSubtasksAndSaysWhatTheTickButtonDoes(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	id, subs := tb.schoolBag(t)
	b.post("/tasks/"+id+"/subtasks/"+subs[0]+"/done", url.Values{"done": {"1"}})
	page := pageEN(b, "/tasks").body
	for _, want := range []string{`class="sublist"`, "1 of 3 subtasks done", "Books", "Mark all as done", `class="sbox"`, `data-done`} {
		if !strings.Contains(page, want) {
			t.Errorf("tasks page lacks %q:\n%s", want, page)
		}
	}
	b.post("/tasks/"+id+"/done", url.Values{"done": {"1"}})
	page = pageEN(b, "/tasks").body
	if !strings.Contains(page, "Mark all as open") || !strings.Contains(page, "3 of 3 subtasks done") {
		t.Errorf("after ticking the task:\n%s", page)
	}
	de := b.do(http.MethodGet, "/tasks", nil, map[string]string{"Accept-Language": "de"}).body
	if !strings.Contains(de, "Teilaufgaben erledigt") || !strings.Contains(de, "Alle als offen markieren") {
		t.Errorf("German tasks page:\n%s", de)
	}
}

// The "Aufgabe hinzufügen" form stays short: its success line links to the
// subtasks of the new task.
func TestNewTaskSuccessLineLinksToItsSubtasks(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	b.post("/tasks", url.Values{"person": {tb.miaID}, "title": {"Tidy room"}, "repeat": {"daily"}})
	_, _, list := tb.parent.call(http.MethodGet, "/tasks", nil)
	id := list[0].(map[string]any)["id"].(string)
	page := pageEN(b, "/tasks").body
	if want := `<a href="/tasks/` + id + `#subs">Add subtasks</a>`; !strings.Contains(page, want) || !strings.Contains(page, "“Tidy room” created for Mia.") {
		t.Errorf("success line lacks %q:\n%s", want, page)
	}
}

// Removing the last open subtask completes the task; the page says so before
// the parent confirms.
func TestRemoveSentenceNamesTheConsequenceOfRemovingTheLastOpenSubtask(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	id, subs := tb.schoolBag(t)
	b.post("/tasks/"+id+"/subtasks/"+subs[0]+"/done", url.Values{"done": {"1"}})
	b.post("/tasks/"+id+"/subtasks/"+subs[1]+"/done", url.Values{"done": {"1"}})
	page := pageEN(b, "/tasks/"+id).body
	if want := "“Lunch box” is the last open subtask. If you remove it, all the others are ticked: “Pack school bag” is then done and Mia gets 2 stars."; !strings.Contains(page, want) {
		t.Errorf("page lacks %q:\n%s", want, page)
	}
	if !strings.Contains(page, "Removing only deletes this subtask. The others and the stars stay.") {
		t.Errorf("the usual remove sentence is missing:\n%s", page)
	}
	b.post("/tasks/"+id+"/subtasks/"+subs[2]+"/delete", url.Values{})
	if line := successLine(pageEN(b, "/tasks/"+id).body); !strings.Contains(line, "removed. All the others are ticked") || !strings.Contains(line, "Mia gets 2 stars") {
		t.Errorf("success line after removing: %q", line)
	}
	if got := taskState(t, tb.parent, id, ""); got["done"] != true {
		t.Fatalf("task = %v", got)
	}
}

func TestChildCannotEditSubtasksInTheWebInterface(t *testing.T) {
	cp := newChildPhone(t)
	for _, path := range []string{"/tasks/" + cp.miaTask + "/subtasks", "/tasks/" + cp.miaTask + "/subtasks/x", "/tasks/" + cp.miaTask + "/subtasks/x/move", "/tasks/" + cp.miaTask + "/subtasks/x/delete"} {
		if r := cp.screen.post(path, url.Values{"title": {"x"}}); r.status != http.StatusForbidden {
			t.Errorf("child POST %s = %d, want 403", path, r.status)
		}
	}
}
