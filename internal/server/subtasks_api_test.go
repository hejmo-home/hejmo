package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// schoolBag is Mia's daily "Pack school bag" worth 2 stars with three subtasks.
func (tb taskBox) schoolBag(t *testing.T) (string, []string) {
	t.Helper()
	status, k, _ := tb.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": tb.miaID, "title": "Pack school bag", "emoji": "🎒", "repeat": "daily", "stars": 2})
	if status != http.StatusCreated {
		t.Fatalf("create = %d %v", status, k)
	}
	id := k["id"].(string)
	var subs []string
	for _, title := range []string{"Books", "Pencil case", "Lunch box"} {
		status, got, _ := tb.parent.call(http.MethodPost, "/tasks/"+id+"/subtasks", map[string]any{"title": title})
		if status != http.StatusCreated {
			t.Fatalf("add %s = %d %v", title, status, got)
		}
		list := got["subtasks"].([]any)
		subs = append(subs, list[len(list)-1].(map[string]any)["id"].(string))
	}
	return id, subs
}

func (tb taskBox) balance(t *testing.T) float64 {
	t.Helper()
	status, acc, _ := tb.parent.call(http.MethodGet, "/people/"+tb.miaID+"/stars", nil)
	if status != http.StatusOK {
		t.Fatalf("stars = %d", status)
	}
	return acc["balance"].(float64)
}

func subState(t *testing.T, task map[string]any) []bool {
	t.Helper()
	var out []bool
	for _, s := range task["subtasks"].([]any) {
		out = append(out, s.(map[string]any)["done"].(bool))
	}
	return out
}

// Acceptance: Given "Pack school bag" with three subtasks, when all three are
// ticked, then the task is done and its stars are credited once.
func TestTickingAllSubtasksCompletesTheTaskAndCreditsStarsOnce(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	id, subs := tb.schoolBag(t)
	for i, s := range subs {
		status, k, _ := tb.child.call(http.MethodPut, "/tasks/"+id+"/subtasks/"+s+"/done", map[string]any{"done": true})
		if status != http.StatusOK {
			t.Fatalf("tick %d = %d %v", i, status, k)
		}
		if want := i == len(subs)-1; k["done"] != want {
			t.Fatalf("after tick %d task done = %v, want %v", i+1, k["done"], want)
		}
	}
	tb.child.call(http.MethodPut, "/tasks/"+id+"/subtasks/"+subs[0]+"/done", map[string]any{"done": true}) // again
	if got := tb.balance(t); got != 2 {
		t.Fatalf("balance = %v, want 2", got)
	}
	if got := taskState(t, tb.parent, id, ""); got["done"] != true || len(subState(t, got)) != 3 {
		t.Fatalf("task = %v", got)
	}
}

// Acceptance: Given a done task, when one subtask is unticked, then the task is
// open again and its stars are deducted.
func TestUntickingASubtaskReopensTheTaskAndDeductsStars(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	id, subs := tb.schoolBag(t)
	if status, k, _ := tb.parent.call(http.MethodPut, "/tasks/"+id+"/done", map[string]any{"done": true}); status != http.StatusOK {
		t.Fatalf("tick task = %d %v", status, k)
	} else if s := subState(t, k); !s[0] || !s[1] || !s[2] {
		t.Fatalf("ticking the task must tick all subtasks: %v", s)
	}
	status, k, _ := tb.child.call(http.MethodPut, "/tasks/"+id+"/subtasks/"+subs[1]+"/done", map[string]any{"done": false})
	if status != http.StatusOK || k["done"] != false {
		t.Fatalf("untick = %d %v", status, k)
	}
	if s := subState(t, k); !s[0] || s[1] || !s[2] {
		t.Fatalf("only the unticked subtask is open: %v", s)
	}
	if got := tb.balance(t); got != 0 {
		t.Fatalf("balance = %v, want 0", got)
	}
	// Unticking the task unticks all subtasks (Q7).
	tb.parent.call(http.MethodPut, "/tasks/"+id+"/done", map[string]any{"done": true})
	if _, k, _ := tb.parent.call(http.MethodPut, "/tasks/"+id+"/done", map[string]any{"done": false}); k["done"] != false {
		t.Fatalf("untick task = %v", k)
	} else if s := subState(t, k); s[0] || s[1] || s[2] {
		t.Fatalf("unticking the task unticks all subtasks: %v", s)
	}
}

func TestParentEditsSubtasks(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	id, subs := tb.schoolBag(t)
	status, k, _ := tb.parent.call(http.MethodPatch, "/tasks/"+id+"/subtasks/"+subs[0], map[string]any{"title": "Schoolbooks", "emoji": "📚"})
	if status != http.StatusOK || k["subtasks"].([]any)[0].(map[string]any)["title"] != "Schoolbooks" {
		t.Fatalf("patch = %d %v", status, k)
	}
	status, k, _ = tb.parent.call(http.MethodPost, "/tasks/"+id+"/subtasks/"+subs[2]+"/move", map[string]any{"direction": "up"})
	if status != http.StatusOK || k["subtasks"].([]any)[1].(map[string]any)["id"] != subs[2] {
		t.Fatalf("move = %d %v", status, k)
	}
	status, k, _ = tb.parent.call(http.MethodDelete, "/tasks/"+id+"/subtasks/"+subs[0], nil)
	if status != http.StatusOK || len(k["subtasks"].([]any)) != 2 {
		t.Fatalf("delete = %d %v", status, k)
	}
	if status, _, _ := tb.parent.call(http.MethodDelete, "/tasks/"+id+"/subtasks/"+subs[0], nil); status != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", status)
	}
}

func TestRemovingTheLastOpenSubtaskCompletesTheTask(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	id, subs := tb.schoolBag(t)
	for _, s := range subs[:2] {
		tb.parent.call(http.MethodPut, "/tasks/"+id+"/subtasks/"+s+"/done", map[string]any{"done": true})
	}
	status, k, _ := tb.parent.call(http.MethodDelete, "/tasks/"+id+"/subtasks/"+subs[2], nil)
	if status != http.StatusOK || k["done"] != true || tb.balance(t) != 2 {
		t.Fatalf("delete = %d %v balance %v", status, k, tb.balance(t))
	}
}

func TestSubtaskInputIsValidatedAndLimited(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	id, _ := tb.schoolBag(t)
	if status, _, _ := tb.parent.call(http.MethodPost, "/tasks/"+id+"/subtasks", map[string]any{"title": " "}); status != http.StatusUnprocessableEntity {
		t.Fatalf("blank title = %d, want 422", status)
	}
	if got := rawCall(t, tb.parent, http.MethodPost, "/tasks/"+id+"/subtasks", `{"title":"`+strings.Repeat("x", 500)+`"}`); got != http.StatusUnprocessableEntity {
		t.Fatalf("long title = %d, want 422", got)
	}
	for i := 3; i < 10; i++ {
		if status, _, _ := tb.parent.call(http.MethodPost, "/tasks/"+id+"/subtasks", map[string]any{"title": "More"}); status != http.StatusCreated {
			t.Fatalf("subtask %d = %d", i+1, status)
		}
	}
	status, k, _ := tb.parent.call(http.MethodPost, "/tasks/"+id+"/subtasks", map[string]any{"title": "Eleven"})
	if status != http.StatusUnprocessableEntity || k["field"] != "subtasks" {
		t.Fatalf("11th = %d %v, want 422 naming subtasks", status, k)
	}
	if status, _, _ := tb.parent.call(http.MethodPost, "/tasks/00000000-0000-4000-8000-000000000000/subtasks", map[string]any{"title": "x"}); status != http.StatusNotFound {
		t.Fatalf("unknown task = %d, want 404", status)
	}
}

func TestChildMayTickOwnSubtasksOnly(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	id, subs := tb.schoolBag(t)
	if status, _, _ := tb.child.call(http.MethodPost, "/tasks/"+id+"/subtasks", map[string]any{"title": "x"}); status != http.StatusForbidden {
		t.Fatalf("child add = %d, want 403", status)
	}
	if status, _, _ := tb.child.call(http.MethodDelete, "/tasks/"+id+"/subtasks/"+subs[0], nil); status != http.StatusForbidden {
		t.Fatalf("child delete = %d, want 403", status)
	}
	_, k, _ := tb.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": tb.mamaID, "title": "Taxes", "repeat": "daily"})
	other := k["id"].(string)
	_, k, _ = tb.parent.call(http.MethodPost, "/tasks/"+other+"/subtasks", map[string]any{"title": "Forms"})
	sub := k["subtasks"].([]any)[0].(map[string]any)["id"].(string)
	if status, _, _ := tb.child.call(http.MethodPut, "/tasks/"+other+"/subtasks/"+sub+"/done", map[string]any{"done": true}); status != http.StatusForbidden {
		t.Fatalf("child tick of another's subtask = %d, want 403", status)
	}
}
