package familyview_test

import (
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/familyview"
)

var (
	morningRt     = familyview.Routine{ID: "r-morning", Name: "Morgenroutine", Part: "morning"}
	afterRt = familyview.Routine{ID: "r-after", Name: "Nachmittag", Part: "afterschool"}
)

func inRoutine(r familyview.Routine, pos int, title string, done bool) familyview.Task {
	return familyview.Task{ID: title, Title: title, Part: r.Part, Done: done, RoutineID: r.ID, Position: pos, Stars: 1}
}

func withRoutines(routines []familyview.Routine, tasks ...familyview.Task) familyview.PersonalView {
	mia := family()[3]
	return familyview.BuildPersonal(familyview.PersonalInput{
		Now: at(14, 9, 0), Person: mia, Calendars: map[string]calendar.Audience{"cal": {}},
		Tasks: tasks, Routines: routines,
	})
}

func routineTitles(b *familyview.RoutineBox) string {
	var out []string
	for _, k := range b.Tasks {
		out = append(out, k.Title)
	}
	return strings.Join(out, ",")
}

// Acceptance: Given Mia's morning routine with four steps, when all are ticked
// off, then the progress shows 4/4.
func TestRoutineWithAllStepsTickedShowsFourOfFour(t *testing.T) {
	v := withRoutines([]familyview.Routine{morningRt},
		inRoutine(morningRt, 1, "Zähne", true), inRoutine(morningRt, 2, "Anziehen", true),
		inRoutine(morningRt, 3, "Frühstück", true), inRoutine(morningRt, 4, "Ranzen", true))
	parts := v.Parts()
	if len(parts) != 1 || parts[0].Routine == nil {
		t.Fatalf("parts = %+v, want the morning with its routine", parts)
	}
	b := parts[0].Routine
	if b.Progress.Done != 4 || b.Progress.Total != 4 || !b.AllDone {
		t.Fatalf("routine progress = %+v all done %v, want 4/4", b.Progress, b.AllDone)
	}
	if p := v.Progress(); p.Done != 4 || p.Total != 4 {
		t.Fatalf("the person's progress = %+v, want 4/4", p)
	}
}

// ADR 0018: the progress of the person counts all tasks, routine steps and single tasks.
func TestPersonProgressCountsRoutineStepsAndSingleTasks(t *testing.T) {
	v := withRoutines([]familyview.Routine{morningRt},
		inRoutine(morningRt, 1, "Zähne", true), inRoutine(morningRt, 2, "Anziehen", false),
		familyview.Task{ID: "cat", Title: "Katze", Done: true})
	if p := v.Progress(); p.Done != 2 || p.Total != 3 {
		t.Fatalf("progress = %+v, want 2 of 3", p)
	}
	b := v.Parts()[0].Routine
	if b.Progress.Done != 1 || b.Progress.Total != 2 || b.AllDone {
		t.Fatalf("routine progress = %+v", b.Progress)
	}
}

// Acceptance: Given a parent reorders steps, then the personal view shows the new order.
func TestRoutineStepsShowInTheParentsOrder(t *testing.T) {
	v := withRoutines([]familyview.Routine{morningRt},
		inRoutine(morningRt, 3, "Frühstück", false), inRoutine(morningRt, 1, "Zähne", false),
		inRoutine(morningRt, 4, "Ranzen", false), inRoutine(morningRt, 2, "Anziehen", false))
	if got := routineTitles(v.Parts()[0].Routine); got != "Zähne,Anziehen,Frühstück,Ranzen" {
		t.Fatalf("steps = %q", got)
	}
	v = withRoutines([]familyview.Routine{morningRt},
		inRoutine(morningRt, 2, "Frühstück", false), inRoutine(morningRt, 1, "Zähne", false),
		inRoutine(morningRt, 3, "Ranzen", false), inRoutine(morningRt, 4, "Anziehen", false))
	if got := routineTitles(v.Parts()[0].Routine); got != "Zähne,Frühstück,Ranzen,Anziehen" {
		t.Fatalf("after reordering steps = %q", got)
	}
}

// A done step keeps its place; the first open step is the next one.
func TestFirstOpenStepIsNext(t *testing.T) {
	v := withRoutines([]familyview.Routine{morningRt},
		inRoutine(morningRt, 1, "Zähne", true), inRoutine(morningRt, 2, "Anziehen", false), inRoutine(morningRt, 3, "Frühstück", false))
	b := v.Parts()[0].Routine
	var next []string
	for _, k := range b.Tasks {
		if k.Next {
			next = append(next, k.Title)
		}
	}
	if len(next) != 1 || next[0] != "Anziehen" {
		t.Fatalf("next = %v, want only Anziehen", next)
	}
	if got := routineTitles(b); got != "Zähne,Anziehen,Frühstück" {
		t.Fatalf("a done step must stay in place: %q", got)
	}
	done := withRoutines([]familyview.Routine{morningRt}, inRoutine(morningRt, 1, "Zähne", true))
	if k := done.Parts()[0].Routine.Tasks[0]; k.Next {
		t.Fatal("a routine that is done has no next step")
	}
}

// Parts of the day: morning, after school, any time, evening; a routine stands
// in its part before the part's single tasks.
func TestPartsHoldRoutinesBeforeSingleTasks(t *testing.T) {
	v := withRoutines([]familyview.Routine{morningRt, afterRt},
		familyview.Task{ID: "e", Title: "Abend", Part: "evening"},
		familyview.Task{ID: "a", Title: "Katze"},
		inRoutine(afterRt, 1, "Hausaufgaben", false),
		familyview.Task{ID: "m", Title: "Blumen", Part: "morning"},
		inRoutine(morningRt, 1, "Zähne", false))
	var got []string
	for _, p := range v.Parts() {
		var r string
		if p.Routine != nil {
			r = "[" + p.Routine.Name + ":" + routineTitles(p.Routine) + "]"
		}
		var singles []string
		for _, k := range p.Tasks {
			singles = append(singles, k.Title)
		}
		got = append(got, p.Part+"="+r+strings.Join(singles, ","))
	}
	want := "morningRt=[Morgenroutine:Zähne]Blumen afterschool=[Nachmittag:Hausaufgaben] =Katze evening=Abend"
	if strings.Join(got, " ") != want {
		t.Fatalf("parts = %q\nwant    %q", strings.Join(got, " "), want)
	}
}

// A routine without a task due today does not appear; a step of an unknown
// routine is shown like a single task.
func TestRoutineWithoutTasksTodayIsLeftOut(t *testing.T) {
	v := withRoutines([]familyview.Routine{morningRt, afterRt}, inRoutine(afterRt, 1, "Hausaufgaben", false))
	parts := v.Parts()
	if len(parts) != 1 || parts[0].Part != "afterschool" {
		t.Fatalf("parts = %+v, want only after school", parts)
	}
	orphan := withRoutines(nil, inRoutine(morningRt, 1, "Zähne", false))
	if p := orphan.Parts(); len(p) != 1 || p[0].Routine != nil || len(p[0].Tasks) != 1 {
		t.Fatalf("step of an unknown routine = %+v", p)
	}
}
