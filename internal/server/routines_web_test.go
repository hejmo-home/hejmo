package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var enHeader = map[string]string{"Accept-Language": "en"}

// page reads a page of the parent in English and fails unless it is served.
func (tb taskBox) page(t *testing.T, path string) string {
	t.Helper()
	r := tb.parent.b.do(http.MethodGet, path, nil, enHeader)
	if r.status != http.StatusOK {
		t.Fatalf("GET %s = %d\n%s", path, r.status, r.body)
	}
	return r.body
}

func (tb taskBox) mustPost(t *testing.T, path string, v url.Values) resp {
	t.Helper()
	r := tb.parent.b.do(http.MethodPost, path, v, enHeader)
	if r.status != http.StatusSeeOther {
		t.Fatalf("POST %s = %d\n%s", path, r.status, r.body)
	}
	return r
}

func has(t *testing.T, page string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(page, w) {
			t.Errorf("page lacks %q:\n%s", w, page)
		}
	}
}

func lacks(t *testing.T, page string, nots ...string) {
	t.Helper()
	for _, n := range nots {
		if strings.Contains(page, n) {
			t.Errorf("page has %q:\n%s", n, page)
		}
	}
}

func TestNewRoutinePageOffersOnePersonAndThreePartsOfTheDay(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	p := tb.page(t, "/routines/new")
	has(t, p, "New routine", `type="radio" name="person"`, `name="part" value="morning"`, `name="part" value="afterschool"`, `name="part" value="evening"`,
		"After school", `name="name"`, `value="Morning routine"`, "More options", "Create routine")
	lacks(t, p, "Whole family", "whole_family", `type="checkbox" name="person"`)
	de := tb.parent.b.do(http.MethodGet, "/routines/new", nil, map[string]string{"Accept-Language": "de"}).body
	has(t, de, "Neue Routine", "Nach der Schule", "Morgenroutine", "Routine anlegen")
}

func TestParentCreatesARoutineInTheWebInterface(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	r := tb.mustPost(t, "/routines", url.Values{"person": {tb.miaID}, "part": {"afterschool"}, "name": {" Homework time "}})
	m := regexp.MustCompile(`^/routines/([0-9a-f-]{36})$`).FindStringSubmatch(r.location)
	if m == nil {
		t.Fatalf("redirect = %q, want the new routine's page", r.location)
	}
	p := tb.page(t, r.location)
	has(t, p, "<h1>Homework time</h1>", "Mia", "After school", `data-success`, "Homework time")
	// API parity.
	got := routineOf(t, tb.parent, m[1])
	if got["name"] != "Homework time" || got["part_of_day"] != "afterschool" || got["person_id"] != tb.miaID {
		t.Fatalf("API routine = %v", got)
	}
	// Without a name the part's default name is used.
	r = tb.mustPost(t, "/routines", url.Values{"person": {tb.miaID}, "part": {"evening"}, "name": {""}})
	has(t, tb.page(t, r.location), "<h1>Evening routine</h1>")
}

func TestSecondRoutineForThePartIsRefusedAtTheFieldAndNamesTheExistingOne(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	first := tb.newRoutine(t, tb.miaID, "Morning routine", "morning")
	r := tb.parent.b.do(http.MethodPost, "/routines", url.Values{"person": {tb.miaID}, "part": {"morning"}, "name": {"Again"}}, enHeader)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", r.status)
	}
	has(t, r.body, `id="err-part"`, `role="alert"`, "already has a routine", "Morning routine", `href="/routines/`+first+`"`, `value="Again"`)
	// Moreover nothing was created.
	if _, _, list := tb.parent.call(http.MethodGet, "/routines", nil); len(list) != 1 {
		t.Fatalf("routines = %v", list)
	}
	for name, form := range map[string]url.Values{
		"no person":    {"part": {"morning"}},
		"unknown part": {"person": {tb.miaID}, "part": {"noon"}},
	} {
		if r := tb.parent.b.do(http.MethodPost, "/routines", form, enHeader); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `role="alert"`) {
			t.Errorf("%s = %d", name, r.status)
		}
	}
}

func TestRoutinePagesAreForParentsOnly(t *testing.T) {
	t.Parallel()
	cp := newChildPhone(t)
	for _, path := range []string{"/routines/new", "/routines/00000000-0000-4000-8000-000000000000"} {
		if r := cp.screen.get(path, "en"); r.status != http.StatusForbidden {
			t.Errorf("child GET %s = %d, want 403", path, r.status)
		}
	}
	if r := cp.screen.post("/routines", url.Values{"person": {personNamed(t, cp.parent.b, "Mia").ID}, "part": {"morning"}}); r.status != http.StatusForbidden {
		t.Errorf("child POST /routines = %d, want 403", r.status)
	}
	tb := taskBox{parent: cp.parent}
	if r := tb.parent.b.do(http.MethodGet, "/routines/00000000-0000-4000-8000-000000000000", nil, enHeader); r.status != http.StatusNotFound {
		t.Errorf("unknown routine = %d, want 404", r.status)
	}
}

// routineOfFour is Mia's morning routine with four steps.
func (tb taskBox) routineOfFour(t *testing.T) (string, []string) {
	t.Helper()
	rid := tb.newRoutine(t, tb.miaID, "Morning routine", "morning")
	var steps []string
	for _, title := range []string{"Brush teeth", "Get dressed", "Breakfast", "Pack school bag"} {
		steps = append(steps, tb.newStep(t, rid, tb.miaID, title))
	}
	return rid, steps
}

func TestRoutinePageShowsFactsAndTheOrderWithMoveButtons(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	rid, steps := tb.routineOfFour(t)
	tb.parent.call(http.MethodPut, "/tasks/"+steps[0]+"/done", map[string]any{"done": true})
	p := tb.page(t, "/routines/"+rid)
	has(t, p, "All tasks", "<h1>Morning routine</h1>", `class="facts"`, "Part of the day", "1 of 4 done", "Order", `action="/routines/`+rid+`/move"`)
	if n := strings.Count(p, `value="up"`); n != 3 {
		t.Errorf("%d up buttons, want 3 (none on the first step)", n)
	}
	if n := strings.Count(p, `value="down"`); n != 3 {
		t.Errorf("%d down buttons, want 3 (none on the last step)", n)
	}
	if i, j := strings.Index(p, "Brush teeth"), strings.Index(p, "Pack school bag"); i < 0 || j < i {
		t.Error("steps are not in their order")
	}
	has(t, p, `href="/tasks/`+steps[1]+`"`, "Add task", "Dissolve routine")
}

// Acceptance: Given a parent reorders steps, then the personal view shows the new order
// (here: the stored order the view reads, and the sentence naming the new place).
func TestMovingAStepChangesTheOrderAndSaysWhereItWent(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	rid, steps := tb.routineOfFour(t)
	tb.mustPost(t, "/routines/"+rid+"/move", url.Values{"task": {steps[2]}, "direction": {"up"}})
	p := tb.page(t, "/routines/"+rid)
	has(t, p, `“Breakfast” is now at place 2 of 4`)
	if got := ids(routineOf(t, tb.parent, rid)["task_ids"]); got[0] != steps[0] || got[1] != steps[2] || got[2] != steps[1] || got[3] != steps[3] {
		t.Fatalf("order = %v", got)
	}
	tb.mustPost(t, "/routines/"+rid+"/move", url.Values{"task": {steps[2]}, "direction": {"down"}})
	if got := ids(routineOf(t, tb.parent, rid)["task_ids"]); got[1] != steps[1] || got[2] != steps[2] {
		t.Fatalf("order after moving back = %v", got)
	}
	for name, form := range map[string]url.Values{
		"first up":     {"task": {steps[0]}, "direction": {"up"}},
		"last down":    {"task": {steps[3]}, "direction": {"down"}},
		"no direction": {"task": {steps[1]}},
		"foreign task": {"task": {"00000000-0000-4000-8000-000000000000"}, "direction": {"up"}},
	} {
		if r := tb.parent.b.do(http.MethodPost, "/routines/"+rid+"/move", form, enHeader); r.status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, r.status)
		}
	}
}

func TestParentAddsANewTaskToTheRoutine(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	rid := tb.newRoutine(t, tb.miaID, "After school", "afterschool")
	tb.mustPost(t, "/routines/"+rid+"/tasks", url.Values{"source": {"new"}, "title": {"Homework"}, "emoji": {"✏️"}, "stars": {"2"}, "repeat": {"daily"}})
	got := routineOf(t, tb.parent, rid)
	if len(ids(got["task_ids"])) != 1 {
		t.Fatalf("routine = %v", got)
	}
	k := taskState(t, tb.parent, ids(got["task_ids"])[0], "")
	if k["title"] != "Homework" || k["part_of_day"] != "afterschool" || k["stars"] != float64(2) || k["position"] != float64(1) || k["routine_id"] != rid {
		t.Fatalf("task = %v", k)
	}
	// A weekly task needs its weekdays, a once task its date; the form says so and keeps what was typed.
	tb.mustPost(t, "/routines/"+rid+"/tasks", url.Values{"source": {"new"}, "title": {"Piano"}, "repeat": {"weekly"}, "weekdays": {"MO", "WE"}})
	r := tb.parent.b.do(http.MethodPost, "/routines/"+rid+"/tasks", url.Values{"source": {"new"}, "title": {" "}, "emoji": {"🎹"}, "stars": {"3"}, "repeat": {"weekly"}, "weekdays": {"TU"}}, enHeader)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("blank title = %d", r.status)
	}
	has(t, r.body, `id="err-title"`, `value="🎹"`, `value="TU" checked`, "<h1>After school</h1>")
	r = tb.parent.b.do(http.MethodPost, "/routines/"+rid+"/tasks", url.Values{"source": {"new"}, "title": {"x"}, "repeat": {"weekly"}}, enHeader)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-weekdays"`) {
		t.Fatalf("weekly without weekdays = %d", r.status)
	}
	if got := ids(routineOf(t, tb.parent, rid)["task_ids"]); len(got) != 2 {
		t.Fatalf("a refused task was added: %v", got)
	}
}

func TestParentTakesAnExistingTaskIntoTheRoutine(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	rid := tb.newRoutine(t, tb.miaID, "Morning routine", "morning")
	other := tb.newRoutine(t, tb.miaID, "Evening routine", "evening")
	inOther := tb.newStep(t, other, tb.miaID, "In the evening routine")
	single := tb.newDaily(t, tb.miaID)
	mamas := tb.newDaily(t, tb.mamaID)
	p := tb.page(t, "/routines/"+rid)
	has(t, p, `name="source" value="existing"`, `name="task" value="`+single+`"`, "Brush teeth")
	lacks(t, p, `value="`+inOther+`"`, `value="`+mamas+`"`)
	tb.mustPost(t, "/routines/"+rid+"/tasks", url.Values{"source": {"existing"}, "task": {single}})
	if k := taskState(t, tb.parent, single, ""); k["routine_id"] != rid || k["part_of_day"] != "morning" {
		t.Fatalf("task = %v", k)
	}
	for name, v := range map[string]string{"in another routine": inOther, "of another person": mamas, "none": ""} {
		r := tb.parent.b.do(http.MethodPost, "/routines/"+rid+"/tasks", url.Values{"source": {"existing"}, "task": {v}}, enHeader)
		if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `role="alert"`) {
			t.Errorf("%s = %d, want 422 with an error", name, r.status)
		}
	}
}

func TestParentRenamesARoutineAndMovesItToAnotherPart(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	rid, steps := tb.routineOfFour(t)
	evening := tb.newRoutine(t, tb.miaID, "Evening routine", "evening")
	tb.mustPost(t, "/routines/"+rid+"/settings", url.Values{"name": {"School morning"}, "part": {"afterschool"}})
	p := tb.page(t, "/routines/"+rid)
	has(t, p, "<h1>School morning</h1>")
	if k := taskState(t, tb.parent, steps[0], ""); k["part_of_day"] != "afterschool" {
		t.Fatalf("task part = %v, want after school for every task of the routine", k["part_of_day"])
	}
	r := tb.parent.b.do(http.MethodPost, "/routines/"+rid+"/settings", url.Values{"name": {"School morning"}, "part": {"evening"}}, enHeader)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("part taken = %d", r.status)
	}
	has(t, r.body, `id="err-part"`, `href="/routines/`+evening+`"`, "Evening routine")
	if r := tb.parent.b.do(http.MethodPost, "/routines/"+rid+"/settings", url.Values{"name": {" "}, "part": {"morning"}}, enHeader); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("blank name = %d", r.status)
	}
}

func TestDissolvingARoutineKeepsEveryTask(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	rid, steps := tb.routineOfFour(t)
	p := tb.page(t, "/routines/"+rid)
	has(t, p, `class="danger"`, "Brush teeth", "Pack school bag", `action="/routines/`+rid+`/delete"`, "Yes, dissolve")
	r := tb.mustPost(t, "/routines/"+rid+"/delete", url.Values{})
	if r.location != "/tasks" {
		t.Fatalf("redirect = %q", r.location)
	}
	for _, id := range steps {
		if k := taskState(t, tb.parent, id, ""); k["routine_id"] != "" || k["part_of_day"] != "morning" {
			t.Fatalf("task after dissolving = %v", k)
		}
	}
	if r := tb.parent.b.do(http.MethodPost, "/routines/"+rid+"/delete", url.Values{}, enHeader); r.status != http.StatusNotFound {
		t.Fatalf("second dissolve = %d, want 404", r.status)
	}
}

func TestTaskPageNamesItsRoutineAndLetsItLeave(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	rid, steps := tb.routineOfFour(t)
	p := tb.page(t, "/tasks/"+steps[2])
	has(t, p, `class="in-routine"`, `href="/routines/`+rid+`"`, "Morning routine", "number 3 of 4", "Take out of the routine", `action="/tasks/`+steps[2]+`/routine"`)
	single := tb.page(t, "/tasks/"+tb.newDaily(t, tb.mamaID))
	lacks(t, single, "in-routine", "/routine\"")
	tb.mustPost(t, "/tasks/"+steps[2]+"/routine", url.Values{})
	if k := taskState(t, tb.parent, steps[2], ""); k["routine_id"] != "" || k["part_of_day"] != "morning" {
		t.Fatalf("task = %v", k)
	}
	if r := tb.parent.b.do(http.MethodPost, "/tasks/"+steps[2]+"/routine", url.Values{}, enHeader); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("leaving twice = %d, want 422", r.status)
	}
}

func TestTasksPageShowsRoutineCardsBeforeTheOtherTasks(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	rid, steps := tb.routineOfFour(t)
	tb.newDaily(t, tb.miaID) // a single task of Mia
	tb.newDaily(t, tb.mamaID)
	tb.parent.call(http.MethodPut, "/tasks/"+steps[0]+"/done", map[string]any{"done": true})
	p := tb.page(t, "/tasks")
	has(t, p, `href="/routines/new"`, "New routine", `class="rcard"`, `href="/routines/`+rid+`"`, "Morning routine", "today 1 of 4 done", `class="nr"`, "Open routine", "More tasks of Mia")
	if i, j := strings.Index(p, `class="rcard"`), strings.Index(p, "More tasks of Mia"); i < 0 || j < i {
		t.Error("the routine card must come before the other tasks")
	}
	if n := strings.Count(p, "More tasks of"); n != 1 {
		t.Errorf("%d 'More tasks' headings, want only Mia's (Mama has no routine)", n)
	}
	// Every title is shown and links to its task.
	for _, id := range steps {
		has(t, p, `href="/tasks/`+id+`"`)
	}
	// Someone with only routine tasks has no "More tasks" heading.
	only := newTaskBox(t)
	_, _ = only.routineOfFour(t)
	lacks(t, only.page(t, "/tasks"), "More tasks of")
}

func TestChildTasksPageShowsRoutineBoxesWithBigCheckboxes(t *testing.T) {
	t.Parallel()
	cp := newChildPhone(t)
	tb := taskBox{parent: cp.parent, miaID: personNamed(t, cp.parent.b, "Mia").ID}
	rid, steps := tb.routineOfFour(t)
	cp.parent.call(http.MethodPut, "/tasks/"+steps[0]+"/done", map[string]any{"done": true})
	p := cp.screen.get("/tasks", "en").body
	has(t, p, `class="child-tasks"`, `class="routine"`, "Morning routine", "1 of 4 done", `class="big-check"`, `action="/tasks/`+steps[1]+`/done"`, "data-next", "next")
	lacks(t, p, `href="/tasks/`+steps[1]+`"`, "/routines/"+rid)
	if i, j := strings.Index(p, "Get dressed"), strings.Index(p, "Breakfast"); i < 0 || j < i {
		t.Error("steps are not in the parents' order")
	}
}

// A done step on the tasks page strikes through its emoji and its title together.
func TestTasksPageStrikesEmojiAndTitleOfADoneStep(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	rid := tb.newRoutine(t, tb.miaID, "Morning routine", "morning")
	status, k, _ := tb.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": tb.miaID, "title": "Brush teeth", "emoji": "🪥", "repeat": "daily", "routine_id": rid})
	if status != http.StatusCreated {
		t.Fatalf("create step = %d %v", status, k)
	}
	tb.parent.call(http.MethodPut, "/tasks/"+k["id"].(string)+"/done", map[string]any{"done": true})
	has(t, tb.page(t, "/tasks"), "<s>🪥 Brush teeth</s>")
}
