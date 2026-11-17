package server_test

import (
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

// miaEveningRoutine makes Mia's evening routine with three steps and returns its id.
func (tb taskBox) miaEveningRoutine(t *testing.T) string {
	t.Helper()
	rid := tb.newRoutine(t, tb.miaID, "Abendroutine", "evening")
	for _, k := range []map[string]any{
		{"title": "Zimmer aufräumen", "emoji": "🧸", "stars": 1, "repeat": "daily"},
		{"title": "Duschen", "emoji": "🚿", "stars": 2, "repeat": "weekly", "weekdays": []string{"MO", "WE", "FR"}},
		{"title": "Zähne putzen", "emoji": "🪥", "stars": 0, "repeat": "daily"},
	} {
		k["person_id"], k["routine_id"] = tb.miaID, rid
		if status, r, _ := tb.parent.call(http.MethodPost, "/tasks", k); status != http.StatusCreated {
			t.Fatalf("create step = %d %v", status, r)
		}
	}
	return rid
}

// sebastian adds the child the acceptance criteria of #92 apply templates to.
func (tb taskBox) sebastian(t *testing.T) string {
	t.Helper()
	status, p, _ := tb.parent.call(http.MethodPost, "/people", map[string]any{"name": "Sebastian", "role": "child"})
	if status != http.StatusCreated {
		t.Fatalf("add Sebastian = %d %v", status, p)
	}
	return p["id"].(string)
}

func (tb taskBox) saveTemplate(t *testing.T, routineID, name string) map[string]any {
	t.Helper()
	body := map[string]any{"routine_id": routineID}
	if name != "" {
		body["name"] = name
	}
	status, tpl, _ := tb.parent.call(http.MethodPost, "/routine-templates", body)
	if status != http.StatusCreated {
		t.Fatalf("save template = %d %v", status, tpl)
	}
	return tpl
}

func tplStepTitles(tpl map[string]any) []string {
	var out []string
	for _, s := range tpl["steps"].([]any) {
		out = append(out, s.(map[string]any)["title"].(string))
	}
	return out
}

func TestShippedRoutineTemplatesAreListedInGerman(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	status, _, list := tb.parent.call(http.MethodGet, "/routine-templates", nil)
	if status != http.StatusOK || len(list) != 4 {
		t.Fatalf("list = %d, %d templates", status, len(list))
	}
	first := list[0].(map[string]any)
	if first["id"] != "school-morning" || first["name"] != "Morgen Schulkind" || first["source"] != "shipped" || first["part_of_day"] != "morning" {
		t.Fatalf("first = %v", first)
	}
	steps := first["steps"].([]any)
	if len(steps) != 5 || steps[0].(map[string]any)["title"] != "Anziehen" || steps[0].(map[string]any)["emoji"] != "👕" || steps[0].(map[string]any)["stars"] != float64(1) {
		t.Fatalf("steps = %v", steps)
	}
	if bag := steps[3].(map[string]any); bag["repeat"] != "weekly" || len(bag["weekdays"].([]any)) != 5 {
		t.Fatalf("bag = %v", bag)
	}
	status, one, _ := tb.parent.call(http.MethodGet, "/routine-templates/evening", nil)
	if status != http.StatusOK || one["name"] != "Abend" {
		t.Fatalf("one = %d %v", status, one)
	}
	if status, _, _ := tb.parent.call(http.MethodGet, "/routine-templates/nope", nil); status != http.StatusNotFound {
		t.Fatalf("unknown = %d", status)
	}
}

// Acceptance (#92, 1): Given "Morgen Schulkind", when a parent applies it to Sebastian, he has a morning routine with its steps.
func TestApplyingAShippedTemplateGivesThePersonARoutine(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	seb := tb.sebastian(t)
	status, r, _ := tb.parent.call(http.MethodPost, "/routines", map[string]any{"person_id": seb, "template_id": "school-morning"})
	if status != http.StatusCreated || r["part_of_day"] != "morning" || r["person_id"] != seb || r["name"] != "Morgenroutine" || len(ids(r["task_ids"])) != 5 {
		t.Fatalf("apply = %d %v", status, r)
	}
	first := taskState(t, tb.parent, ids(r["task_ids"])[0], "")
	if first["title"] != "Anziehen" || first["emoji"] != "👕" || first["stars"] != float64(1) || first["repeat"] != "daily" || first["person_id"] != seb || first["routine_id"] != r["id"] {
		t.Fatalf("first step = %v", first)
	}
	// Any part of the day can be picked; the second morning routine is refused as always.
	status, e, _ := tb.parent.call(http.MethodPost, "/routines", map[string]any{"person_id": seb, "template_id": "school-morning"})
	if status != http.StatusConflict || e["existing_routine_id"] != r["id"] {
		t.Fatalf("second morning routine = %d %v", status, e)
	}
	status, ev, _ := tb.parent.call(http.MethodPost, "/routines", map[string]any{"person_id": seb, "template_id": "school-morning", "part_of_day": "evening", "name": "Abend anders"})
	if status != http.StatusCreated || ev["part_of_day"] != "evening" || ev["name"] != "Abend anders" {
		t.Fatalf("evening from the morning template = %d %v", status, ev)
	}
	if status, _, _ := tb.parent.call(http.MethodPost, "/routines", map[string]any{"person_id": seb, "template_id": "nope"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown template = %d, want 422", status)
	}
}

// Acceptance (#92, 3): Given a parent saves Mia's evening routine as a template, it can be applied to another person.
func TestSavedTemplateCanBeAppliedToAnotherPerson(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	seb := tb.sebastian(t)
	tpl := tb.saveTemplate(t, tb.miaEveningRoutine(t), "")
	if tpl["name"] != "Abendroutine von Mia" || tpl["source"] != "own" || tpl["part_of_day"] != "evening" || tpl["origin_routine"] != "Abendroutine" || tpl["origin_person"] != "Mia" || tpl["saved_at"] == nil {
		t.Fatalf("template = %v", tpl)
	}
	if got := tplStepTitles(tpl); len(got) != 3 || got[1] != "Duschen" {
		t.Fatalf("steps = %v", got)
	}
	status, r, _ := tb.parent.call(http.MethodPost, "/routines", map[string]any{"person_id": seb, "template_id": tpl["id"]})
	if status != http.StatusCreated || r["part_of_day"] != "evening" || r["person_id"] != seb || len(ids(r["task_ids"])) != 3 {
		t.Fatalf("apply = %d %v", status, r)
	}
	if status, _, list := tb.parent.call(http.MethodGet, "/routine-templates", nil); status != http.StatusOK || len(list) != 5 || list[0].(map[string]any)["id"] != tpl["id"] {
		t.Fatalf("list = %d, own template should come first among %d", status, len(list))
	}
}

// Acceptance (#92, 2): changing the copy does not change the template, and vice versa.
func TestTemplateAndRoutineMadeFromItStayIndependent(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	tpl := tb.saveTemplate(t, tb.miaEveningRoutine(t), "Abend")
	id := tpl["id"].(string)
	_, r, _ := tb.parent.call(http.MethodPost, "/routines", map[string]any{"person_id": tb.mamaID, "template_id": id})
	copyIDs := ids(r["task_ids"])

	// The copy changes ...
	if status, _, _ := tb.parent.call(http.MethodPatch, "/tasks/"+copyIDs[0], map[string]any{"stars": 7}); status != http.StatusOK {
		t.Fatalf("patch task = %d", status)
	}
	if status, _, _ := tb.parent.call(http.MethodDelete, "/tasks/"+copyIDs[1], nil); status != http.StatusNoContent {
		t.Fatalf("delete task = %d", status)
	}
	_, now, _ := tb.parent.call(http.MethodGet, "/routine-templates/"+id, nil)
	if got := tplStepTitles(now); len(got) != 3 || got[0] != "Zimmer aufräumen" {
		t.Fatalf("template after changing the copy = %v", got)
	}

	// ... and the template changes.
	steps := now["steps"].([]any)
	first, second := steps[0].(map[string]any)["id"].(string), steps[1].(map[string]any)["id"].(string)
	if status, _, _ := tb.parent.call(http.MethodPatch, "/routine-templates/"+id+"/steps/"+first, map[string]any{"title": "Aufräumen", "stars": 4}); status != http.StatusOK {
		t.Fatalf("patch step = %d", status)
	}
	if status, _, _ := tb.parent.call(http.MethodDelete, "/routine-templates/"+id+"/steps/"+second, nil); status != http.StatusOK {
		t.Fatalf("delete step = %d", status)
	}
	if status, _, _ := tb.parent.call(http.MethodDelete, "/routine-templates/"+id, nil); status != http.StatusNoContent {
		t.Fatalf("delete template = %d", status)
	}
	rt := routineOf(t, tb.parent, r["id"].(string))
	if len(ids(rt["task_ids"])) != 2 {
		t.Fatalf("routine after changing the template = %v", rt)
	}
	if got := taskState(t, tb.parent, copyIDs[0], ""); got["title"] != "Zimmer aufräumen" || got["stars"] != float64(7) {
		t.Fatalf("copy = %v", got)
	}
	if got := routineOf(t, tb.parent, tb.miaEveningRoutineID(t)); len(ids(got["task_ids"])) != 3 {
		t.Fatalf("Mia's routine = %v", got)
	}
}

func (tb taskBox) miaEveningRoutineID(t *testing.T) string {
	t.Helper()
	_, _, list := tb.parent.call(http.MethodGet, "/routines?person="+tb.miaID, nil)
	for _, x := range list {
		if m := x.(map[string]any); m["part_of_day"] == "evening" {
			return m["id"].(string)
		}
	}
	t.Fatal("no evening routine of Mia")
	return ""
}

func TestSavingATemplateRefusesWhatCannotBeSaved(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	rid := tb.miaEveningRoutine(t)
	tb.saveTemplate(t, rid, "Abend")
	if status, _, _ := tb.parent.call(http.MethodPost, "/routine-templates", map[string]any{"routine_id": rid, "name": "abend"}); status != http.StatusConflict {
		t.Fatalf("same name = %d, want 409", status)
	}
	empty := tb.newRoutine(t, tb.mamaID, "Leer", "morning")
	if status, _, _ := tb.parent.call(http.MethodPost, "/routine-templates", map[string]any{"routine_id": empty, "name": "x"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("empty routine = %d, want 422", status)
	}
	if status, _, _ := tb.parent.call(http.MethodPost, "/routine-templates", map[string]any{"routine_id": "00000000-0000-4000-8000-000000000000"}); status != http.StatusNotFound {
		t.Fatalf("unknown routine = %d, want 404", status)
	}
	once := tb.newRoutine(t, tb.mamaID, "Einmalig", "evening")
	tb.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": tb.mamaID, "routine_id": once, "title": "Arzt", "repeat": "once", "date": "2026-11-20"})
	if status, _, _ := tb.parent.call(http.MethodPost, "/routine-templates", map[string]any{"routine_id": once, "name": "y"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("only a once-only task = %d, want 422", status)
	}
}

func TestEditingAnOwnTemplateInTheAPI(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	tpl := tb.saveTemplate(t, tb.miaEveningRoutine(t), "Abend")
	id := tpl["id"].(string)
	if status, r, _ := tb.parent.call(http.MethodPatch, "/routine-templates/"+id, map[string]any{"name": "Abend neu", "part_of_day": "afterschool"}); status != http.StatusOK || r["name"] != "Abend neu" || r["part_of_day"] != "afterschool" {
		t.Fatalf("patch = %d %v", status, r)
	}
	status, r, _ := tb.parent.call(http.MethodPost, "/routine-templates/"+id+"/steps", map[string]any{"title": "Vorlesen", "emoji": "📖", "stars": 1, "repeat": "daily"})
	if status != http.StatusCreated || len(tplStepTitles(r)) != 4 || tplStepTitles(r)[3] != "Vorlesen" {
		t.Fatalf("add step = %d %v", status, r)
	}
	var stepIDs []string
	for _, s := range r["steps"].([]any) {
		stepIDs = append(stepIDs, s.(map[string]any)["id"].(string))
	}
	order := []string{stepIDs[3], stepIDs[0], stepIDs[1], stepIDs[2]}
	if status, r, _ := tb.parent.call(http.MethodPut, "/routine-templates/"+id+"/order", map[string]any{"step_ids": order}); status != http.StatusOK || tplStepTitles(r)[0] != "Vorlesen" {
		t.Fatalf("order = %d %v", status, r)
	}
	if status, _, _ := tb.parent.call(http.MethodPut, "/routine-templates/"+id+"/order", map[string]any{"step_ids": order[:2]}); status != http.StatusUnprocessableEntity {
		t.Fatalf("partial order = %d, want 422", status)
	}
	if status, _, _ := tb.parent.call(http.MethodPatch, "/routine-templates/"+id+"/steps/"+order[1], map[string]any{"repeat": "weekly"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("weekly without days = %d, want 422", status)
	}
	if status, _, _ := tb.parent.call(http.MethodPatch, "/routine-templates/"+id+"/steps/nope", map[string]any{"title": "x"}); status != http.StatusNotFound {
		t.Fatalf("unknown step = %d, want 404", status)
	}
}

func TestShippedAndForeignTemplatesCannotBeChanged(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPatch, "/routine-templates/evening", map[string]any{"name": "x"}},
		{http.MethodDelete, "/routine-templates/evening", nil},
		{http.MethodPost, "/routine-templates/evening/steps", map[string]any{"title": "x", "repeat": "daily"}},
		{http.MethodDelete, "/routine-templates/evening/steps/story", nil},
		{http.MethodPut, "/routine-templates/evening/order", map[string]any{"step_ids": []string{"story"}}},
	} {
		if status, _, _ := tb.parent.call(c.method, c.path, c.body); status != http.StatusConflict {
			t.Errorf("%s %s = %d, want 409 (shipped templates stay as they are)", c.method, c.path, status)
		}
	}
	if status, _, _ := tb.parent.call(http.MethodDelete, "/routine-templates/00000000-0000-4000-8000-000000000000", nil); status != http.StatusNotFound {
		t.Errorf("unknown template = %d, want 404", status)
	}
}

func TestChildCannotManageRoutineTemplates(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	rid := tb.miaEveningRoutine(t)
	tpl := tb.saveTemplate(t, rid, "Abend")
	if status, _, _ := tb.child.call(http.MethodPost, "/routine-templates", map[string]any{"routine_id": rid, "name": "Kind"}); status != http.StatusForbidden {
		t.Errorf("child save = %d, want 403", status)
	}
	if status, _, _ := tb.child.call(http.MethodDelete, "/routine-templates/"+tpl["id"].(string), nil); status != http.StatusForbidden {
		t.Errorf("child delete = %d, want 403", status)
	}
	if status, _, _ := tb.child.call(http.MethodPost, "/routines", map[string]any{"person_id": tb.miaID, "template_id": "school-morning"}); status != http.StatusForbidden {
		t.Errorf("child apply = %d, want 403", status)
	}
}

func TestDefaultTemplateNameIsShortenedToTheLimit(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	rid := tb.newRoutine(t, tb.miaID, strings.Repeat("A", 58), "evening")
	if status, r, _ := tb.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": tb.miaID, "routine_id": rid, "title": "Duschen", "repeat": "daily"}); status != http.StatusCreated {
		t.Fatalf("create step = %d %v", status, r)
	}
	tpl := tb.saveTemplate(t, rid, "")
	if name, _ := tpl["name"].(string); utf8.RuneCountInString(name) != 60 || !strings.HasPrefix(name, "AAAA") {
		t.Fatalf("name = %q, want the default name cut to 60 characters", name)
	}
}

func TestTheLastStepOfATemplateStays(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	tpl := tb.saveTemplate(t, tb.miaEveningRoutine(t), "Abend")
	id := tpl["id"].(string)
	steps := tpl["steps"].([]any)
	for _, s := range steps[1:] {
		if status, _, _ := tb.parent.call(http.MethodDelete, "/routine-templates/"+id+"/steps/"+s.(map[string]any)["id"].(string), nil); status != http.StatusOK {
			t.Fatalf("delete step = %d", status)
		}
	}
	if status, _, _ := tb.parent.call(http.MethodDelete, "/routine-templates/"+id+"/steps/"+steps[0].(map[string]any)["id"].(string), nil); status != http.StatusUnprocessableEntity {
		t.Fatalf("delete last step = %d, want 422", status)
	}
	_, now, _ := tb.parent.call(http.MethodGet, "/routine-templates/"+id, nil)
	if len(tplStepTitles(now)) != 1 {
		t.Fatalf("steps = %v", tplStepTitles(now))
	}
}
