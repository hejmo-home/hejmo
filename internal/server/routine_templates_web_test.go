package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var tplIDPattern = regexp.MustCompile(`/routines/templates/([0-9a-f-]{36})`)

// routineIDAt reads the id of the routine a redirect points at.
func routineIDAt(t *testing.T, location string) string {
	t.Helper()
	m := regexp.MustCompile(`^/routines/([0-9a-f-]{36})$`).FindStringSubmatch(location)
	if m == nil {
		t.Fatalf("redirect = %q, want a routine's page", location)
	}
	return m[1]
}

func TestTasksPageLeadsToTheTemplatesAndSuggestsOneToAChildWithoutTasks(t *testing.T) {
	tb := newTaskBox(t)
	p := tb.page(t, "/tasks")
	has(t, p, `href="/routines/templates"`, "Routine templates", "Mia has no tasks yet", "School-child morning", `href="/routines/new?person=`+tb.miaID+`"`)
	de := tb.parent.b.do(http.MethodGet, "/tasks", nil, map[string]string{"Accept-Language": "de"}).body
	has(t, de, "Routine-Vorlagen", "Mia hat noch keine Aufgaben", "Morgen Schulkind")
	tb.newDaily(t, tb.miaID)
	lacks(t, tb.page(t, "/tasks"), "Mia has no tasks yet")
}

func TestTemplatesMenuListsOwnFirstThenTheOnesFromHejmoInTheLanguageInUse(t *testing.T) {
	tb := newTaskBox(t)
	p := tb.page(t, "/routines/templates")
	has(t, p, "<h1>Routine templates</h1>", "All tasks", "Your templates", "No templates of your own yet", "Save as template", `href="/tasks"`,
		"Templates from Hejmo", "stay as they are", "School-child morning", "Daycare-child morning", "School-child afternoon", `aria-label="Evening"`,
		"Morning · comes with Hejmo", "After school · comes with Hejmo", "🥣 Eat breakfast", "📖 Bedtime story", "no stars",
		`href="/routines/new?template=school-morning"`, "Use for someone")
	lacks(t, p, `href="/routines/templates/school-morning"`, "Open template")
	de := tb.parent.b.do(http.MethodGet, "/routines/templates", nil, map[string]string{"Accept-Language": "de"}).body
	has(t, de, "Morgen Schulkind", "Nachmittag Schulkind", "Nach der Schule · kommt mit Hejmo", "Gute-Nacht-Geschichte", "ohne Sterne", "Mo, Di, Mi, Do, Fr", "Vorlagen von Hejmo")

	tpl := tb.saveTemplate(t, tb.miaEveningRoutine(t), "Evening routine of Mia")
	p = tb.page(t, "/routines/templates")
	has(t, p, "Evening routine of Mia", "Evening · your own template", "Open template", `href="/routines/templates/`+tpl["id"].(string)+`"`, "Mon, Wed, Fri", "🚿 Duschen")
	lacks(t, p, "No templates of your own yet")
	if i, j := strings.Index(p, "Evening routine of Mia"), strings.Index(p, "School-child morning"); i < 0 || j < i {
		t.Error("own templates must come before the ones from Hejmo")
	}
}

func TestTemplatesPagesAreForParentsOnly(t *testing.T) {
	cp := newChildPhone(t)
	for _, path := range []string{"/routines/templates", "/routines/templates/evening"} {
		if r := cp.screen.get(path, "en"); r.status != http.StatusForbidden {
			t.Errorf("child GET %s = %d, want 403", path, r.status)
		}
	}
}

func TestNewRoutinePageOffersTheTemplatesAsChoiceCards(t *testing.T) {
	tb := newTaskBox(t)
	tpl := tb.saveTemplate(t, tb.miaEveningRoutine(t), "Evening routine of Mia")
	p := tb.page(t, "/routines/new")
	has(t, p, "How do you start?", `name="template" value="" checked`, "No template", "Your templates", "Templates from Hejmo",
		`name="template" value="`+tpl["id"].(string)+`"`, `name="template" value="school-morning"`, `<span class="tpart">Morning</span> · 5 tasks:`, `<span class="st">👕 Get dressed,</span>`, `<span class="st">👟 Put on shoes and jacket</span>`,
		"independent copy", "does not change the template, and the other way round", "Create routine")

	// Coming from "Use for someone": the template is chosen and the part of the day follows its suggestion.
	p = tb.page(t, "/routines/new?template="+tpl["id"].(string)+"&person="+tb.miaID)
	has(t, p, `name="template" value="`+tpl["id"].(string)+`" checked`, `name="part" value="evening" checked`, `name="person" value="`+tb.miaID+`" checked`, `value="Evening routine"`)
	lacks(t, p, `name="template" value="" checked`)
	has(t, tb.page(t, "/routines/new?template=school-afternoon"), `name="part" value="afterschool" checked`, `value="After-school routine"`)
	// An unknown template is ignored.
	has(t, tb.page(t, "/routines/new?template=nope"), `name="template" value="" checked`, `name="part" value="morning" checked`)
}

// Acceptance (#92, 1): Given "Morgen Schulkind", when a parent applies it to Sebastian, then he has a morning routine with its steps.
func TestParentAppliesAShippedTemplateInTheWebInterface(t *testing.T) {
	tb := newTaskBox(t)
	seb := tb.sebastian(t)
	r := tb.mustPost(t, "/routines", url.Values{"person": {seb}, "part": {"morning"}, "template": {"school-morning"}, "name": {"Morning routine"}})
	rid := routineIDAt(t, r.location)
	p := tb.page(t, "/routines/"+rid)
	has(t, p, "<h1>Morning routine</h1>", "data-success", "The routine “Morning routine” of Sebastian is created, with the 5 tasks of the template “School-child morning”", "It now belongs to Sebastian",
		"Get dressed", "Eat breakfast", "Pack your school bag")
	got := routineOf(t, tb.parent, rid)
	if got["person_id"] != seb || got["part_of_day"] != "morning" || len(ids(got["task_ids"])) != 5 {
		t.Fatalf("routine = %v", got)
	}
	// The routine keeps no mark of where it came from.
	lacks(t, tb.page(t, "/routines/"+rid), "School-child morning</", "from template")
}

func TestAShippedTemplateIsCopiedInGerman(t *testing.T) {
	tb := newTaskBox(t)
	de := map[string]string{"Accept-Language": "de"}
	r := tb.parent.b.do(http.MethodPost, "/routines", url.Values{"person": {tb.miaID}, "part": {"afterschool"}, "template": {"school-afternoon"}, "name": {"Nachmittag"}}, de)
	if r.status != http.StatusSeeOther {
		t.Fatalf("POST = %d %s", r.status, r.body)
	}
	p := tb.parent.b.do(http.MethodGet, r.location, nil, de).body
	has(t, p, "Mias Nachmittag ist angelegt, mit den 4 Aufgaben der Vorlage „Nachmittag Schulkind“", "Hausaufgaben machen", "Schulranzen auspacken")
}

func TestAnyPartOfTheDayCanBePickedForATemplate(t *testing.T) {
	tb := newTaskBox(t)
	r := tb.mustPost(t, "/routines", url.Values{"person": {tb.miaID}, "part": {"evening"}, "template": {"school-morning"}, "name": {""}})
	got := routineOf(t, tb.parent, routineIDAt(t, r.location))
	if got["part_of_day"] != "evening" || got["name"] != "Evening routine" {
		t.Fatalf("routine = %v", got)
	}
}

func TestTemplateAndPersonStayChosenWhenThePartIsTaken(t *testing.T) {
	tb := newTaskBox(t)
	first := tb.newRoutine(t, tb.miaID, "Morning routine", "morning")
	r := tb.parent.b.do(http.MethodPost, "/routines", url.Values{"person": {tb.miaID}, "part": {"morning"}, "template": {"school-morning"}, "name": {"Morning routine"}}, enHeader)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", r.status)
	}
	has(t, r.body, `id="err-part"`, `href="/routines/`+first+`"`, `name="template" value="school-morning" checked`, `name="person" value="`+tb.miaID+`" checked`)
	if _, _, list := tb.parent.call(http.MethodGet, "/routines", nil); len(list) != 1 {
		t.Fatalf("routines = %d, want only the first", len(list))
	}
	if _, _, all := tb.parent.call(http.MethodGet, "/tasks", nil); len(all) != 0 {
		t.Fatalf("a refused routine left %d tasks", len(all))
	}
	r = tb.parent.b.do(http.MethodPost, "/routines", url.Values{"person": {tb.miaID}, "part": {"evening"}, "template": {"nope"}}, enHeader)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-template"`) {
		t.Fatalf("unknown template = %d", r.status)
	}
}

func TestRoutinePageOffersSavingAsATemplateAndExplainsWhatGoesIn(t *testing.T) {
	tb := newTaskBox(t)
	rid := tb.miaEveningRoutine(t)
	p := tb.page(t, "/routines/"+rid)
	has(t, p, "Save as template", `action="/routines/`+rid+`/template"`, `name="tname" type="text" value="Abendroutine of Mia"`,
		"the part of the day (in the evening) and the 3 tasks above in their order, with emoji, stars and repetition", "stay with Mia",
		"is a copy", "Save as template</button>")
	if i, j := strings.Index(p, "Name and part of the day"), strings.Index(p, `action="/routines/`+rid+`/template"`); i < 0 || j < i {
		t.Error("Save as template belongs below Name and part of the day")
	}
	if i, j := strings.Index(p, `action="/routines/`+rid+`/template"`), strings.Index(p, "Dissolve routine"); j < i {
		t.Error("Save as template belongs above the danger box")
	}
	lacks(t, p, "Not in the template")

	// A once-only task stays out, and the notice names it.
	tb.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": tb.miaID, "routine_id": rid, "title": "Bring library card", "emoji": "🎫", "repeat": "once", "date": "2026-11-18"})
	p = tb.page(t, "/routines/"+rid)
	has(t, p, `class="left-out"`, "Not in the template: “🎫 Bring library card” (on 18.11.2026), because it is once-only.", "the 3 tasks above")

	// An empty routine says why it cannot be saved instead of offering a button.
	empty := tb.newRoutine(t, tb.mamaID, "Mama morning", "morning")
	p = tb.page(t, "/routines/"+empty)
	has(t, p, "A routine without tasks cannot become a template. Add tasks above first.")
	lacks(t, p, `action="/routines/`+empty+`/template"`)
}

// Acceptance (#92, 3): Given a parent saves Mia's evening routine as a template, then it can be applied to Sebastian.
func TestSavedTemplateIsAppliedToAnotherPersonInTheWebInterface(t *testing.T) {
	tb := newTaskBox(t)
	seb := tb.sebastian(t)
	rid := tb.miaEveningRoutine(t)
	r := tb.mustPost(t, "/routines/"+rid+"/template", url.Values{"tname": {"Evening routine of Mia"}})
	if r.location != "/routines/"+rid {
		t.Fatalf("redirect = %q, want the routine's page", r.location)
	}
	p := tb.page(t, r.location)
	m := regexp.MustCompile(`href="/routines/new\?template=([0-9a-f-]{36})"`).FindStringSubmatch(p)
	if m == nil {
		t.Fatalf("the success line must lead to Use for someone:\n%s", p)
	}
	has(t, p, "data-success", "Saved as template “Evening routine of Mia”.", "Use for someone")
	// The new routine's page: Sebastian gets an evening routine with Mia's steps.
	r = tb.mustPost(t, "/routines", url.Values{"person": {seb}, "part": {"evening"}, "template": {m[1]}, "name": {"Evening routine"}})
	p = tb.page(t, r.location)
	has(t, p, "with the 3 tasks of the template “Evening routine of Mia”", "changes neither the template nor Mia&#39;s routine", "Zimmer aufräumen", "Duschen")
	got := routineOf(t, tb.parent, routineIDAt(t, r.location))
	if got["person_id"] != seb || len(ids(got["task_ids"])) != 3 {
		t.Fatalf("routine = %v", got)
	}
	k := taskState(t, tb.parent, ids(got["task_ids"])[1], "")
	if k["title"] != "Duschen" || k["stars"] != float64(2) || k["repeat"] != "weekly" {
		t.Fatalf("copied step = %v", k)
	}
}

func TestSavingATemplateRefusesATakenNameAtTheFieldAndSuggestsAnother(t *testing.T) {
	tb := newTaskBox(t)
	rid := tb.miaEveningRoutine(t)
	tb.saveTemplate(t, rid, "Evening routine of Mia")
	r := tb.parent.b.do(http.MethodPost, "/routines/"+rid+"/template", url.Values{"tname": {"Evening routine of Mia"}}, enHeader)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", r.status)
	}
	has(t, r.body, `id="err-tname"`, `role="alert"`, "There is already a template of your own called “Evening routine of Mia”", "for example “Evening routine of Mia, 2”",
		`name="tname" type="text" value="Evening routine of Mia"`, `<details class="more" open>`)
	r = tb.parent.b.do(http.MethodPost, "/routines/"+rid+"/template", url.Values{"tname": {" "}}, enHeader)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-tname"`) {
		t.Fatalf("blank name = %d", r.status)
	}
	empty := tb.newRoutine(t, tb.mamaID, "Mama morning", "morning")
	if r := tb.parent.b.do(http.MethodPost, "/routines/"+empty+"/template", url.Values{"tname": {"x"}}, enHeader); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("empty routine = %d, want 422", r.status)
	}
	if r := tb.parent.b.do(http.MethodPost, "/routines/00000000-0000-4000-8000-000000000000/template", url.Values{"tname": {"x"}}, enHeader); r.status != http.StatusNotFound {
		t.Fatalf("unknown routine = %d, want 404", r.status)
	}
}

func TestOwnTemplatePageShowsFactsStepsAndTheOtherSections(t *testing.T) {
	tb := newTaskBox(t)
	tpl := tb.saveTemplate(t, tb.miaEveningRoutine(t), "Evening routine of Mia")
	id := tpl["id"].(string)
	p := tb.page(t, "/routines/templates/"+id)
	has(t, p, "Routine templates", "<h1>Evening routine of Mia</h1>", "Origin", "Saved from “Abendroutine” of Mia on", "Part of the day",
		`href="/routines/new?template=`+id+`"`, "Use for someone", "Tasks of the template", "changes only the template",
		`href="/routines/templates/`+id+`/steps/`+tpl["steps"].([]any)[0].(map[string]any)["id"].(string)+`"`, `action="/routines/templates/`+id+`/move"`,
		"Add task", `action="/routines/templates/`+id+`/steps"`, "Name and part of the day", `action="/routines/templates/`+id+`/settings"`, `name="tname"`,
		"Delete template", `action="/routines/templates/`+id+`/delete"`, "Routines that were made from it stay as they are")
	if n := strings.Count(p, `value="up"`); n != 2 {
		t.Errorf("%d up buttons, want 2", n)
	}
	lacks(t, p, `value="once"`)
	for _, path := range []string{"/routines/templates/evening", "/routines/templates/00000000-0000-4000-8000-000000000000"} {
		if r := tb.parent.b.do(http.MethodGet, path, nil, enHeader); r.status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 (a template from Hejmo has no page)", path, r.status)
		}
	}
}

// Acceptance (#92, 2): changing the copy does not change the template, and the other way round.
func TestChangingTheTemplateLeavesTheRoutineAloneAndSaysSo(t *testing.T) {
	tb := newTaskBox(t)
	tpl := tb.saveTemplate(t, tb.miaEveningRoutine(t), "Evening routine of Mia")
	id := tpl["id"].(string)
	steps := tpl["steps"].([]any)
	first, second := steps[0].(map[string]any)["id"].(string), steps[1].(map[string]any)["id"].(string)
	r := tb.mustPost(t, "/routines", url.Values{"person": {tb.mamaID}, "part": {"evening"}, "template": {id}, "name": {"Evening routine"}})
	copyID := routineIDAt(t, r.location)

	tb.mustPost(t, "/routines/templates/"+id+"/move", url.Values{"step": {second}, "direction": {"up"}})
	p := tb.page(t, "/routines/templates/"+id)
	has(t, p, "“Duschen” is now at place 1 of 3", "Only the template is changed")
	if i, j := strings.Index(p, "Duschen"), strings.Index(p, "Zimmer aufräumen"); i < 0 || j < i {
		t.Error("the moved step is not first")
	}
	if r := tb.parent.b.do(http.MethodPost, "/routines/templates/"+id+"/move", url.Values{"step": {second}, "direction": {"up"}}, enHeader); r.status != http.StatusUnprocessableEntity {
		t.Errorf("moving the first step up = %d, want 422", r.status)
	}

	tb.mustPost(t, "/routines/templates/"+id+"/steps", url.Values{"title": {"Read aloud"}, "emoji": {"📖"}, "stars": {"2"}, "repeat": {"weekly"}, "weekdays": {"SA", "SU"}})
	has(t, tb.page(t, "/routines/templates/"+id), "“Read aloud” is now part of the template", "📖 Read aloud", "Sat, Sun")
	bad := tb.parent.b.do(http.MethodPost, "/routines/templates/"+id+"/steps", url.Values{"title": {" "}, "repeat": {"daily"}, "stars": {"1"}}, enHeader)
	if bad.status != http.StatusUnprocessableEntity || !strings.Contains(bad.body, `id="err-title"`) {
		t.Fatalf("blank title = %d", bad.status)
	}
	noDays := tb.parent.b.do(http.MethodPost, "/routines/templates/"+id+"/steps", url.Values{"title": {"x"}, "repeat": {"weekly"}}, enHeader)
	if noDays.status != http.StatusUnprocessableEntity || !strings.Contains(noDays.body, `id="err-weekdays"`) {
		t.Fatalf("weekly without days = %d", noDays.status)
	}

	tb.mustPost(t, "/routines/templates/"+id+"/settings", url.Values{"tname": {"Evening, new"}, "part": {"afterschool"}})
	has(t, tb.page(t, "/routines/templates/"+id), "<h1>Evening, new</h1>", "Only the template is changed")
	tb.saveTemplate(t, tb.miaEveningRoutineID(t), "Taken")
	clash := tb.parent.b.do(http.MethodPost, "/routines/templates/"+id+"/settings", url.Values{"tname": {"taken"}, "part": {"evening"}}, enHeader)
	if clash.status != http.StatusUnprocessableEntity || !strings.Contains(clash.body, `id="err-tname"`) {
		t.Fatalf("taken name = %d", clash.status)
	}

	// The step's own page.
	sp := tb.page(t, "/routines/templates/"+id+"/steps/"+first)
	has(t, sp, "<h1>🧸 Zimmer aufräumen</h1>", "Task 2 of 4 of the template “Evening, new”", `action="/routines/templates/`+id+`/steps/`+first+`"`, `name="stars" type="number" value="1"`, "Remove from template")
	lacks(t, sp, `value="once"`)
	tb.mustPost(t, "/routines/templates/"+id+"/steps/"+first, url.Values{"title": {"Tidy up"}, "emoji": {"🧸"}, "stars": {"3"}, "repeat": {"weekly"}, "weekdays": {"MO", "TU"}})
	has(t, tb.page(t, "/routines/templates/"+id), "🧸 Tidy up", "Mon, Tue")
	if r := tb.parent.b.do(http.MethodPost, "/routines/templates/"+id+"/steps/"+first, url.Values{"title": {""}, "repeat": {"daily"}, "stars": {"1"}}, enHeader); r.status != http.StatusUnprocessableEntity {
		t.Errorf("blank step title = %d", r.status)
	}
	tb.mustPost(t, "/routines/templates/"+id+"/steps/"+first+"/delete", url.Values{})
	if _, tpl2, _ := tb.parent.call(http.MethodGet, "/routine-templates/"+id, nil); len(tpl2["steps"].([]any)) != 3 {
		t.Fatalf("template = %v", tpl2)
	}

	// The routine made from the template was not touched by any of it.
	if got := routineOf(t, tb.parent, copyID); len(ids(got["task_ids"])) != 3 || got["name"] != "Evening routine" || got["part_of_day"] != "evening" {
		t.Fatalf("routine = %v", got)
	}
	if k := taskState(t, tb.parent, ids(routineOf(t, tb.parent, copyID)["task_ids"])[0], ""); k["title"] != "Zimmer aufräumen" || k["stars"] != float64(1) {
		t.Fatalf("copied first step = %v", k)
	}

	// And the other way round: changing the copy leaves the template alone, and deleting the template leaves the copy.
	tb.mustPost(t, "/tasks/"+ids(routineOf(t, tb.parent, copyID)["task_ids"])[0]+"/stars", url.Values{"stars": {"9"}})
	_, still, _ := tb.parent.call(http.MethodGet, "/routine-templates/"+id, nil)
	if len(still["steps"].([]any)) != 3 || still["steps"].([]any)[0].(map[string]any)["title"] != "Duschen" {
		t.Fatalf("template changed with the copy: %v", still)
	}
	r = tb.mustPost(t, "/routines/templates/"+id+"/delete", url.Values{})
	if r.location != "/routines/templates" {
		t.Fatalf("redirect = %q", r.location)
	}
	has(t, tb.page(t, r.location), "Template “Evening, new” deleted. Routines that were made from it stay as they are.")
	if got := routineOf(t, tb.parent, copyID); len(ids(got["task_ids"])) != 3 {
		t.Fatalf("routine after deleting the template = %v", got)
	}
	if r := tb.parent.b.do(http.MethodPost, "/routines/templates/"+id+"/delete", url.Values{}, enHeader); r.status != http.StatusNotFound {
		t.Errorf("deleting twice = %d, want 404", r.status)
	}
	if r := tb.parent.b.do(http.MethodPost, "/routines/templates/evening/delete", url.Values{}, enHeader); r.status != http.StatusNotFound {
		t.Errorf("deleting a template from Hejmo = %d, want 404", r.status)
	}
}

func TestTemplateNamesAreEscaped(t *testing.T) {
	tb := newTaskBox(t)
	rid := tb.miaEveningRoutine(t)
	tb.mustPost(t, "/routines/"+rid+"/template", url.Values{"tname": {`<script>alert(1)</script>`}})
	for _, path := range []string{"/routines/templates", "/routines/new", "/routines/" + rid} {
		lacks(t, tb.page(t, path), "<script>alert")
	}
}
