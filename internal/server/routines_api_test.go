package server_test

import (
	"net/http"
	"strings"
	"testing"
)

func (tb taskBox) newRoutine(t *testing.T, person, name, part string) string {
	t.Helper()
	status, r, _ := tb.parent.call(http.MethodPost, "/routines", map[string]any{"person_id": person, "name": name, "part_of_day": part})
	if status != http.StatusCreated {
		t.Fatalf("create routine = %d %v", status, r)
	}
	return r["id"].(string)
}

func (tb taskBox) newStep(t *testing.T, routine, person, title string) string {
	t.Helper()
	status, k, _ := tb.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": person, "title": title, "repeat": "daily", "routine_id": routine})
	if status != http.StatusCreated {
		t.Fatalf("create step %s = %d %v", title, status, k)
	}
	return k["id"].(string)
}

func routineOf(t *testing.T, c *apiClient, id string) map[string]any {
	t.Helper()
	status, r, _ := c.call(http.MethodGet, "/routines/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /routines/%s = %d", id, status)
	}
	return r
}

func ids(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestParentCreatesReadsAndListsRoutines(t *testing.T) {
	tb := newTaskBox(t)
	id := tb.newRoutine(t, tb.miaID, "Morgenroutine", "morning")
	r := routineOf(t, tb.parent, id)
	if r["name"] != "Morgenroutine" || r["person_id"] != tb.miaID || r["part_of_day"] != "morning" || len(ids(r["task_ids"])) != 0 {
		t.Fatalf("routine = %v", r)
	}
	status, _, list := tb.child.call(http.MethodGet, "/routines", nil)
	if status != http.StatusOK || len(list) != 1 || list[0].(map[string]any)["id"] != id {
		t.Fatalf("list = %d %v", status, list)
	}
	if status, _, list := tb.parent.call(http.MethodGet, "/routines?person="+tb.mamaID, nil); status != http.StatusOK || len(list) != 0 {
		t.Fatalf("list of Mama = %d %v", status, list)
	}
	if status, _, _ := tb.parent.call(http.MethodGet, "/routines/00000000-0000-4000-8000-000000000000", nil); status != http.StatusNotFound {
		t.Fatalf("unknown routine = %d, want 404", status)
	}
}

func TestRoutineInputIsValidated(t *testing.T) {
	tb := newTaskBox(t)
	for name, body := range map[string]map[string]any{
		"blank name":  {"person_id": tb.miaID, "name": " ", "part_of_day": "morning"},
		"unknown ppl": {"person_id": "00000000-0000-4000-8000-000000000000", "name": "x", "part_of_day": "morning"},
	} {
		if status, _, _ := tb.parent.call(http.MethodPost, "/routines", body); status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, status)
		}
	}
}

// The contract client refuses these before they are sent, so the box's own
// checks are exercised with raw requests.
func TestRoutineLimitsOfTheContractAreEnforced(t *testing.T) {
	tb := newTaskBox(t)
	for name, body := range map[string]string{
		"blank part":   `{"person_id":"` + tb.miaID + `","name":"x","part_of_day":""}`,
		"any time":     `{"person_id":"` + tb.miaID + `","name":"x","part_of_day":"anytime"}`,
		"unknown part": `{"person_id":"` + tb.miaID + `","name":"x","part_of_day":"noon"}`,
		"long name":    `{"person_id":"` + tb.miaID + `","name":"` + strings.Repeat("x", 200) + `","part_of_day":"morning"}`,
	} {
		if got := rawCall(t, tb.parent, http.MethodPost, "/routines", body); got != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, got)
		}
	}
}

// At most one routine per person and part of the day: 409 naming the existing one.
func TestSecondRoutineForTheSamePartIsAConflict(t *testing.T) {
	tb := newTaskBox(t)
	first := tb.newRoutine(t, tb.miaID, "Morgenroutine", "morning")
	status, r, _ := tb.parent.call(http.MethodPost, "/routines", map[string]any{"person_id": tb.miaID, "name": "Noch eine", "part_of_day": "morning"})
	if status != http.StatusConflict || r["existing_routine_id"] != first {
		t.Fatalf("second routine = %d %v, want 409 naming %s", status, r, first)
	}
	tb.newRoutine(t, tb.miaID, "Abendroutine", "evening")
	tb.newRoutine(t, tb.mamaID, "Mamas Morgen", "morning")
}

func TestChildCannotChangeRoutines(t *testing.T) {
	tb := newTaskBox(t)
	if status, _, _ := tb.child.call(http.MethodPost, "/routines", map[string]any{"person_id": tb.miaID, "name": "x", "part_of_day": "morning"}); status != http.StatusForbidden {
		t.Fatalf("child create = %d, want 403", status)
	}
	id := tb.newRoutine(t, tb.miaID, "Morgenroutine", "morning")
	if status, _, _ := tb.child.call(http.MethodDelete, "/routines/"+id, nil); status != http.StatusForbidden {
		t.Fatalf("child dissolve = %d, want 403", status)
	}
	if status, _, _ := tb.child.call(http.MethodPatch, "/routines/"+id, map[string]any{"name": "x"}); status != http.StatusForbidden {
		t.Fatalf("child rename = %d, want 403", status)
	}
}

// Acceptance: Given a task in a routine, then it takes the routine's part of the day.
func TestTaskInARoutineTakesItsPartOfDay(t *testing.T) {
	tb := newTaskBox(t)
	rid := tb.newRoutine(t, tb.miaID, "Nachmittag", "afterschool")
	status, k, _ := tb.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": tb.miaID, "title": "Hausaufgaben", "repeat": "daily", "part_of_day": "evening", "routine_id": rid})
	if status != http.StatusCreated || k["part_of_day"] != "afterschool" || k["routine_id"] != rid || k["position"] != float64(1) {
		t.Fatalf("step = %d %v", status, k)
	}
	// A single task has no routine and no place, and cannot be "after school".
	single := tb.newDaily(t, tb.miaID)
	if got := taskState(t, tb.parent, single, ""); got["routine_id"] != "" || got["position"] != float64(0) {
		t.Fatalf("single task = %v", got)
	}
	if status, _, _ := tb.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": tb.miaID, "title": "x", "repeat": "daily", "part_of_day": "afterschool"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("after school without a routine = %d, want 422", status)
	}
	// The routine's own part of the day moves all its tasks.
	if status, r, _ := tb.parent.call(http.MethodPatch, "/routines/"+rid, map[string]any{"part_of_day": "evening"}); status != http.StatusOK || r["part_of_day"] != "evening" {
		t.Fatalf("patch = %d %v", status, r)
	}
	if got := taskState(t, tb.parent, k["id"].(string), ""); got["part_of_day"] != "evening" {
		t.Fatalf("task after the routine moved = %v", got)
	}
}

func TestStepFromAnotherPersonIsRefused(t *testing.T) {
	tb := newTaskBox(t)
	rid := tb.newRoutine(t, tb.miaID, "Morgenroutine", "morning")
	if status, _, _ := tb.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": tb.mamaID, "title": "x", "repeat": "daily", "routine_id": rid}); status != http.StatusUnprocessableEntity {
		t.Fatalf("task of Mama in Mia's routine = %d, want 422", status)
	}
	if status, _, _ := tb.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": tb.miaID, "title": "x", "repeat": "daily", "routine_id": "00000000-0000-4000-8000-000000000000"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown routine = %d, want 422", status)
	}
}

func TestExistingTaskJoinsAndLeavesARoutine(t *testing.T) {
	tb := newTaskBox(t)
	rid := tb.newRoutine(t, tb.miaID, "Morgenroutine", "morning")
	first := tb.newStep(t, rid, tb.miaID, "Zähne putzen")
	single := tb.newDaily(t, tb.miaID)
	status, r, _ := tb.parent.call(http.MethodPost, "/routines/"+rid+"/tasks", map[string]any{"task_id": single})
	if status != http.StatusOK || len(ids(r["task_ids"])) != 2 || ids(r["task_ids"])[1] != single {
		t.Fatalf("adopt = %d %v", status, r)
	}
	if got := taskState(t, tb.parent, single, ""); got["part_of_day"] != "morning" || got["position"] != float64(2) {
		t.Fatalf("adopted task = %v", got)
	}
	// Twice, or in a second routine, is a conflict: a task belongs to at most one routine.
	if status, _, _ := tb.parent.call(http.MethodPost, "/routines/"+rid+"/tasks", map[string]any{"task_id": single}); status != http.StatusConflict {
		t.Fatalf("adopt twice = %d, want 409", status)
	}
	if status, _, _ := tb.parent.call(http.MethodPost, "/routines/"+rid+"/tasks", map[string]any{"task_id": tb.newDaily(t, tb.mamaID)}); status != http.StatusUnprocessableEntity {
		t.Fatalf("task of Mama = %d, want 422", status)
	}
	if status, _, _ := tb.parent.call(http.MethodPost, "/routines/"+rid+"/tasks", map[string]any{"task_id": "00000000-0000-4000-8000-000000000000"}); status != http.StatusNotFound {
		t.Fatalf("unknown task = %d, want 404", status)
	}
	if status, _, _ := tb.parent.call(http.MethodDelete, "/routines/"+rid+"/tasks/"+first, nil); status != http.StatusNoContent {
		t.Fatalf("leave = %d, want 204", status)
	}
	if got := taskState(t, tb.parent, first, ""); got["routine_id"] != "" || got["part_of_day"] != "morning" {
		t.Fatalf("task that left = %v", got)
	}
	if got := routineOf(t, tb.parent, rid); len(ids(got["task_ids"])) != 1 {
		t.Fatalf("routine after leaving = %v", got)
	}
	if status, _, _ := tb.parent.call(http.MethodDelete, "/routines/"+rid+"/tasks/"+first, nil); status != http.StatusNotFound {
		t.Fatalf("leave twice = %d, want 404 (the task is no longer in this routine)", status)
	}
}

// Acceptance: Given a parent reorders steps, then the new order is what the API (and so the view) reads.
func TestParentReordersTheSteps(t *testing.T) {
	tb := newTaskBox(t)
	rid := tb.newRoutine(t, tb.miaID, "Morgenroutine", "morning")
	a, b, c := tb.newStep(t, rid, tb.miaID, "A"), tb.newStep(t, rid, tb.miaID, "B"), tb.newStep(t, rid, tb.miaID, "C")
	if got := ids(routineOf(t, tb.parent, rid)["task_ids"]); got[0] != a || got[1] != b || got[2] != c {
		t.Fatalf("created order = %v", got)
	}
	status, r, _ := tb.parent.call(http.MethodPut, "/routines/"+rid+"/order", map[string]any{"task_ids": []string{c, a, b}})
	if status != http.StatusOK {
		t.Fatalf("reorder = %d %v", status, r)
	}
	if got := ids(routineOf(t, tb.parent, rid)["task_ids"]); got[0] != c || got[1] != a || got[2] != b {
		t.Fatalf("new order = %v", got)
	}
	if got := taskState(t, tb.parent, c, ""); got["position"] != float64(1) {
		t.Fatalf("position of C = %v", got["position"])
	}
	for name, bad := range map[string][]string{"missing": {a, b}, "duplicate": {a, a, b}, "foreign": {a, b, "00000000-0000-4000-8000-000000000000"}} {
		if status, _, _ := tb.parent.call(http.MethodPut, "/routines/"+rid+"/order", map[string]any{"task_ids": bad}); status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, status)
		}
	}
}

func TestDissolvingARoutineKeepsItsTasks(t *testing.T) {
	tb := newTaskBox(t)
	rid := tb.newRoutine(t, tb.miaID, "Nachmittag", "afterschool")
	step := tb.newStep(t, rid, tb.miaID, "Hausaufgaben")
	if status, _, _ := tb.parent.call(http.MethodDelete, "/routines/"+rid, nil); status != http.StatusNoContent {
		t.Fatalf("dissolve = %d", status)
	}
	if got := taskState(t, tb.parent, step, ""); got["routine_id"] != "" || got["part_of_day"] != "" {
		t.Fatalf("task after dissolving = %v, want a single any-time task", got)
	}
	if status, _, _ := tb.parent.call(http.MethodGet, "/routines/"+rid, nil); status != http.StatusNotFound {
		t.Fatalf("dissolved routine = %d, want 404", status)
	}
	if status, _, _ := tb.parent.call(http.MethodDelete, "/routines/"+rid, nil); status != http.StatusNotFound {
		t.Fatalf("dissolve twice = %d, want 404", status)
	}
}

func TestPatchRoutineNameAndConflict(t *testing.T) {
	tb := newTaskBox(t)
	morning := tb.newRoutine(t, tb.miaID, "Morgenroutine", "morning")
	evening := tb.newRoutine(t, tb.miaID, "Abendroutine", "evening")
	if status, r, _ := tb.parent.call(http.MethodPatch, "/routines/"+morning, map[string]any{"name": "Schulmorgen"}); status != http.StatusOK || r["name"] != "Schulmorgen" || r["part_of_day"] != "morning" {
		t.Fatalf("rename = %d %v", status, r)
	}
	if status, r, _ := tb.parent.call(http.MethodPatch, "/routines/"+morning, map[string]any{"part_of_day": "evening"}); status != http.StatusConflict || r["existing_routine_id"] != evening {
		t.Fatalf("move onto a taken part = %d %v", status, r)
	}
	if status, _, _ := tb.parent.call(http.MethodPatch, "/routines/"+morning, map[string]any{"name": " "}); status != http.StatusUnprocessableEntity {
		t.Fatalf("blank name = %d, want 422", status)
	}
}

// The star account, the progress and VTODO export are not touched by routines:
// a routine step is an ordinary task for ticking.
func TestRoutineStepIsTickedLikeAnyTask(t *testing.T) {
	tb := newTaskBox(t)
	rid := tb.newRoutine(t, tb.miaID, "Morgenroutine", "morning")
	step := tb.newStep(t, rid, tb.miaID, "Zähne putzen")
	if status, k, _ := tb.child.call(http.MethodPut, "/tasks/"+step+"/done", map[string]any{"done": true}); status != http.StatusOK || k["done"] != true || k["routine_id"] != rid {
		t.Fatalf("tick = %d %v", status, k)
	}
}
