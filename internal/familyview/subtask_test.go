package familyview_test

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/familyview"
)

func subs(done ...bool) []familyview.Subtask {
	var out []familyview.Subtask
	for i, d := range done {
		out = append(out, familyview.Subtask{ID: string(rune('a' + i)), Title: "S" + string(rune('1'+i)), Done: d})
	}
	return out
}

func nextSub(b *familyview.RoutineBox) string {
	for _, k := range b.Tasks {
		for _, s := range k.Subtasks {
			if s.Next {
				return k.Title + "/" + s.Title
			}
		}
	}
	return ""
}

// Inside a routine the "next" marker sits on the first open subtask of the
// next task, not on the task itself.
func TestNextMarkerMovesToTheFirstOpenSubtask(t *testing.T) {
	bag := inRoutine(morningRt, 2, "Ranzen", false)
	bag.Subtasks = subs(true, false, false)
	v := withRoutines([]familyview.Routine{morningRt}, inRoutine(morningRt, 1, "Zähne", true), bag)
	box := v.Parts()[0].Routine
	if got := nextSub(box); got != "Ranzen/S2" {
		t.Fatalf("next subtask = %q, want Ranzen/S2", got)
	}
	for _, k := range box.Tasks {
		if k.Next {
			t.Fatalf("task %s must not be marked when its subtask is", k.Title)
		}
	}
}

// A next task without subtasks is marked as before.
func TestNextMarkerStaysOnATaskWithoutSubtasks(t *testing.T) {
	v := withRoutines([]familyview.Routine{morningRt}, inRoutine(morningRt, 1, "Zähne", false))
	if !v.Parts()[0].Routine.Tasks[0].Next {
		t.Fatal("the first open task should be next")
	}
}

// Outside routines nothing is marked, and the head counts tasks, not subtasks.
func TestNothingMarkedOutsideRoutinesAndProgressCountsTasks(t *testing.T) {
	bag := familyview.Task{ID: "bag", Title: "Ranzen", Subtasks: subs(true, false)}
	v := withRoutines(nil, bag, familyview.Task{ID: "cat", Title: "Katze", Done: true})
	if got := v.Parts()[0].Tasks[0].Subtasks; got[0].Next || got[1].Next {
		t.Fatalf("subtasks outside a routine are marked: %+v", got)
	}
	if p := v.Progress(); p.Done != 1 || p.Total != 2 {
		t.Fatalf("progress = %+v, want 1/2 tasks", p)
	}
}

func TestSubtaskProgressWords(t *testing.T) {
	k := familyview.Task{Subtasks: subs(true, false, false)}
	if d, n := k.SubtasksDone(); d != 1 || n != 3 {
		t.Fatalf("SubtasksDone = %d of %d", d, n)
	}
}
