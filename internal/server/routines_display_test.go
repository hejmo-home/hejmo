package server_test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/task"
)

// routineFor adds a routine for a person and its steps (in this order), all due daily.
func (f *familyBox) routineFor(t *testing.T, who, name string, part task.PartOfDay, titles ...string) (task.Routine, []task.Task) {
	t.Helper()
	r, err := f.db.CreateRoutine(f.householdID, task.Routine{PersonID: f.people[who].ID, Name: name, Part: part})
	if err != nil {
		t.Fatal(err)
	}
	var steps []task.Task
	for _, title := range titles {
		k, err := f.db.AddTask(f.householdID, task.Task{PersonID: f.people[who].ID, Title: title, Repeat: task.Daily, Date: "2026-11-14", Stars: 1, RoutineID: r.ID})
		if err != nil {
			t.Fatal(err)
		}
		steps = append(steps, k)
	}
	return r, steps
}

var routineBoxRe = regexp.MustCompile(`(?s)<section class="routine"[^>]*>.*?</section>`)

func routineBox(t *testing.T, page string) string {
	t.Helper()
	b := routineBoxRe.FindString(page)
	if b == "" {
		t.Fatalf("no routine box on the page:\n%s", page)
	}
	return b
}

// Acceptance: Given Mia's morning routine with four steps, when all are
// ticked off, then the progress shows 4/4.
func TestPersonalViewShowsFourOfFourWhenTheMorningRoutineIsDone(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	_, steps := f.routineFor(t, "Mia", "Morning routine", task.Morning, "Brush teeth", "Get dressed", "Breakfast", "Pack school bag")
	f.now = f.at(14, 7, 0)
	box := routineBox(t, f.personalPage(t, "en", "Mia", 14, 7).body)
	if !strings.Contains(box, "0 of 4 done") || strings.Contains(box, "data-all-done") {
		t.Fatalf("before ticking:\n%s", box)
	}
	for _, k := range steps {
		if r := f.display.post("/display/tasks/"+k.ID+"/done", url.Values{"done": {"1"}}); r.status != 303 {
			t.Fatalf("tick = %d", r.status)
		}
	}
	page := f.personalPage(t, "en", "Mia", 14, 7).body
	box = routineBox(t, page)
	if !strings.Contains(box, "4 of 4 done") || !strings.Contains(box, "data-all-done") {
		t.Fatalf("after ticking everything:\n%s", box)
	}
	if !strings.Contains(page, "All 4 tasks done for today") {
		t.Errorf("the head does not say all 4 tasks are done:\n%s", page)
	}
	if got := progressOf(t, f.view(t, "en", f.at(14, 7, 0)), "Mia"); got != "4/4" {
		t.Errorf("family view progress = %q, want 4/4", got)
	}
}

// ADR 0018 / 0016: the head count and the family column count every task of
// today, routine steps and single tasks together; the family view shows no routine bar.
func TestProgressCountsRoutineStepsAndSingleTasksTogether(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	_, steps := f.routineFor(t, "Mia", "Morning routine", task.Morning, "Brush teeth", "Get dressed", "Breakfast")
	f.task(t, "Mia", "Feed the cat", task.Daily)
	if err := f.db.SetTaskDone(f.householdID, steps[0].ID, "2026-11-14", true, "", f.at(14, 7, 0)); err != nil {
		t.Fatal(err)
	}
	page := f.personalPage(t, "en", "Mia", 14, 8).body
	if !strings.Contains(page, "1 of 4 tasks done") || !strings.Contains(routineBox(t, page), "1 of 3 done") {
		t.Errorf("personal view:\n%s", page)
	}
	family := f.view(t, "en", f.at(14, 8, 0))
	if got := progressOf(t, family, "Mia"); got != "1/4" {
		t.Errorf("family view progress = %q, want 1/4", got)
	}
	if strings.Contains(family, `class="routine`) || strings.Contains(family, "Morning routine") {
		t.Error("the family view shows the routine")
	}
}

// Acceptance: Given a parent reorders steps, then the personal view shows the new order.
func TestPersonalViewShowsTheParentsNewOrder(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	r, steps := f.routineFor(t, "Mia", "Morning routine", task.Morning, "Brush teeth", "Get dressed", "Breakfast")
	order := func() string {
		var titles []string
		for _, m := range regexp.MustCompile(`class="title">([^<]*)<`).FindAllStringSubmatch(routineBox(t, f.personalPage(t, "en", "Mia", 14, 7).body), -1) {
			titles = append(titles, m[1])
		}
		return strings.Join(titles, ",")
	}
	if got := order(); got != "Brush teeth,Get dressed,Breakfast" {
		t.Fatalf("order = %q", got)
	}
	if r := f.post("/routines/"+r.ID+"/move", url.Values{"task": {steps[2].ID}, "direction": {"up"}}); r.status != 303 {
		t.Fatalf("move = %d\n%s", r.status, r.body)
	}
	if got := order(); got != "Brush teeth,Breakfast,Get dressed" {
		t.Fatalf("order after reordering = %q", got)
	}
}

// The display shows the parts in order, with the routine in its part, the
// first open step marked for children who cannot read, and done steps in place.
func TestPersonalViewShowsPartsRoutinesAndTheNextStep(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.routineFor(t, "Mia", "Morning routine", task.Morning, "Brush teeth", "Get dressed", "Breakfast")
	f.routineFor(t, "Mia", "After school", task.AfterSchool, "Homework")
	f.task(t, "Mia", "Feed the cat", task.Daily)
	if _, err := f.db.AddTask(f.householdID, task.Task{PersonID: f.people["Mia"].ID, Title: "Bath", Repeat: task.Daily, Date: "2026-11-14", PartOfDay: task.Evening}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetTaskDone(f.householdID, func() string { ts, _ := f.db.Tasks(f.householdID); return ts[0].ID }(), "2026-11-14", true, "", f.at(14, 7, 0)); err != nil {
		t.Fatal(err)
	}
	page := f.personalPage(t, "en", "Mia", 14, 8).body
	var parts []string
	for _, m := range regexp.MustCompile(`data-part="([a-z]*)"`).FindAllStringSubmatch(page, -1) {
		parts = append(parts, m[1])
	}
	if strings.Join(parts, ",") != "morning,afterschool,anytime,evening" {
		t.Fatalf("parts = %v", parts)
	}
	morning := routineBox(t, page)
	if n := strings.Count(morning, "data-next"); n != 1 || !strings.Contains(morning, `data-next><form`) {
		t.Errorf("%d next steps in the morning routine:\n%s", n, morning)
	}
	if !strings.Contains(morning, "Get dressed: Open, tap to tick off, next") {
		t.Errorf("the next step does not say so in its accessible name:\n%s", morning)
	}
	if strings.Contains(morning, `aria-label="Brush teeth: Done`) == false {
		t.Errorf("a done step must stay in place:\n%s", morning)
	}
	for _, part := range []string{"Morning", "After school", "Any time today", "Evening"} {
		if !strings.Contains(page, part) {
			t.Errorf("part %q missing:\n%s", part, page)
		}
	}
	de := f.personalPage(t, "de", "Mia", 14, 8).body
	for _, want := range []string{"Nach der Schule", "von 3 erledigt", "als Nächstes"} {
		if !strings.Contains(de, want) {
			t.Errorf("German page lacks %q", want)
		}
	}
}

// The star value of a step carries its unit for screen readers.
func TestPersonalViewGivesTheStepStarsAUnitForScreenReaders(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.routineFor(t, "Mia", "Morning routine", task.Morning, "Brush teeth")
	page := f.personalPage(t, "en", "Mia", 14, 8).body
	if want := `data-task-stars="1">1<span class="sr"> Stars</span></span>`; !strings.Contains(page, want) {
		t.Errorf("page lacks %q:\n%s", want, page)
	}
}
