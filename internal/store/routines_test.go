package store_test

import (
	"errors"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

// routineSteps adds tasks to a routine in order and returns their ids.
func (f taskFixture) routineSteps(t *testing.T, r task.Routine, titles ...string) []string {
	t.Helper()
	var ids []string
	for _, title := range titles {
		k, err := f.db.AddTask(f.h.ID, task.Task{PersonID: r.PersonID, Title: title, Repeat: task.Daily, Date: "2026-11-14", Stars: 1, RoutineID: r.ID})
		if err != nil {
			t.Fatalf("AddTask(%s) = %v", title, err)
		}
		ids = append(ids, k.ID)
	}
	return ids
}

func (f taskFixture) newRoutine(t *testing.T, person store.Person, name string, part task.PartOfDay) task.Routine {
	t.Helper()
	r, err := f.db.CreateRoutine(f.h.ID, task.Routine{PersonID: person.ID, Name: name, Part: part})
	if err != nil || r.ID == "" {
		t.Fatalf("CreateRoutine(%s) = %+v, %v", name, r, err)
	}
	return r
}

// order is the titles of a routine's tasks in their order.
func (f taskFixture) order(t *testing.T, routineID string) []string {
	t.Helper()
	list, err := f.db.RoutineTasks(f.h.ID, routineID)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for i, k := range list {
		if k.Position != i+1 {
			t.Errorf("%s has position %d, want %d", k.Title, k.Position, i+1)
		}
		titles = append(titles, k.Title)
	}
	return titles
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRoutineRoundTrip(t *testing.T) {
	f := newTaskFixture(t)
	r := f.newRoutine(t, f.mia, " Morgenroutine ", task.Morning)
	got, err := f.db.Routine(f.h.ID, r.ID)
	if err != nil || got.Name != "Morgenroutine" || got.PersonID != f.mia.ID || got.Part != task.Morning {
		t.Fatalf("Routine = %+v, %v", got, err)
	}
	list, err := f.db.Routines(f.h.ID)
	if err != nil || len(list) != 1 || list[0].ID != r.ID {
		t.Fatalf("Routines = %v, %v", list, err)
	}
	if _, err := f.db.Routine(f.h.ID, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown routine = %v, want ErrNotFound", err)
	}
}

func TestCreateRoutineRefusesInvalidInput(t *testing.T) {
	f := newTaskFixture(t)
	for name, r := range map[string]task.Routine{
		"no name":      {PersonID: f.mia.ID, Name: " ", Part: task.Morning},
		"any time":     {PersonID: f.mia.ID, Name: "x", Part: task.AnyTime},
		"unknown ppl":  {PersonID: "no-such-person", Name: "x", Part: task.Morning},
		"no person":    {Name: "x", Part: task.Morning},
		"unknown part": {PersonID: f.mia.ID, Name: "x", Part: "noon"},
	} {
		if _, err := f.db.CreateRoutine(f.h.ID, r); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s = %v, want ErrInvalid", name, err)
		}
	}
}

// A person has at most one routine per part of the day; the error names the one that exists.
func TestOneRoutinePerPersonAndPartOfDay(t *testing.T) {
	f := newTaskFixture(t)
	first := f.newRoutine(t, f.mia, "Morgenroutine", task.Morning)
	_, err := f.db.CreateRoutine(f.h.ID, task.Routine{PersonID: f.mia.ID, Name: "Noch eine", Part: task.Morning})
	var exists *store.RoutineExistsError
	if !errors.Is(err, store.ErrConflict) || !errors.As(err, &exists) || exists.Existing.ID != first.ID {
		t.Fatalf("second morning routine = %v, want ErrConflict naming the first", err)
	}
	f.newRoutine(t, f.mia, "Abendroutine", task.Evening)
	f.newRoutine(t, f.mama, "Mamas Morgen", task.Morning)
	f.newRoutine(t, f.mia, "Nachmittag", task.AfterSchool)
}

// Acceptance: Given a task in a routine, then it takes the routine's part of the day.
func TestTaskInARoutineTakesTheRoutinesPartOfDay(t *testing.T) {
	f := newTaskFixture(t)
	r := f.newRoutine(t, f.mia, "Nachmittag", task.AfterSchool)
	k, err := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: "Hausaufgaben", Repeat: task.Daily, Date: "2026-11-14", PartOfDay: task.Evening, RoutineID: r.ID})
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.db.Task(f.h.ID, k.ID)
	if err != nil || got.PartOfDay != task.AfterSchool || got.RoutineID != r.ID || got.Position != 1 {
		t.Fatalf("task = %+v, %v; want after school in the routine at 1", got, err)
	}
	// Changing the routine's part changes it for every task of the routine.
	if err := f.db.UpdateRoutine(f.h.ID, r.ID, "Nachmittag", task.Evening); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.Task(f.h.ID, k.ID); got.PartOfDay != task.Evening {
		t.Fatalf("after the routine moved to the evening the task is %q", got.PartOfDay)
	}
	list, _ := f.db.Tasks(f.h.ID)
	if len(list) != 1 || list[0].PartOfDay != task.Evening || list[0].RoutineID != r.ID {
		t.Fatalf("Tasks = %+v", list)
	}
}

func TestAddTaskToAnotherPersonsRoutineIsRefused(t *testing.T) {
	f := newTaskFixture(t)
	r := f.newRoutine(t, f.mia, "Morgenroutine", task.Morning)
	_, err := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mama.ID, Title: "x", Repeat: task.Daily, Date: "2026-11-14", RoutineID: r.ID})
	if !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("task of Mama in Mia's routine = %v, want ErrInvalid", err)
	}
	_, err = f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: "x", Repeat: task.Daily, Date: "2026-11-14", RoutineID: "no-such-routine"})
	if !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("unknown routine = %v, want ErrInvalid", err)
	}
}

func TestTasksJoinARoutineAtTheEnd(t *testing.T) {
	f := newTaskFixture(t)
	r := f.newRoutine(t, f.mia, "Morgenroutine", task.Morning)
	f.routineSteps(t, r, "Zähne putzen", "Anziehen")
	single, _ := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: "Blumen gießen", Repeat: task.Daily, Date: "2026-11-14", PartOfDay: task.Evening})
	if err := f.db.AddTaskToRoutine(f.h.ID, r.ID, single.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.order(t, r.ID); !equal(got, []string{"Zähne putzen", "Anziehen", "Blumen gießen"}) {
		t.Fatalf("order = %v", got)
	}
	if got, _ := f.db.Task(f.h.ID, single.ID); got.PartOfDay != task.Morning {
		t.Fatalf("adopted task is %q, want the routine's morning", got.PartOfDay)
	}
	// Joining twice, or a second routine, is refused: a task belongs to at most one routine.
	if err := f.db.AddTaskToRoutine(f.h.ID, r.ID, single.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("task already in the routine = %v, want ErrConflict", err)
	}
	evening := f.newRoutine(t, f.mia, "Abendroutine", task.Evening)
	if err := f.db.AddTaskToRoutine(f.h.ID, evening.ID, single.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("task of another routine = %v, want ErrConflict", err)
	}
	other, _ := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mama.ID, Title: "Mamas", Repeat: task.Daily, Date: "2026-11-14"})
	if err := f.db.AddTaskToRoutine(f.h.ID, r.ID, other.ID); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("task of another person = %v, want ErrInvalid", err)
	}
	if err := f.db.AddTaskToRoutine(f.h.ID, r.ID, "no-such-task"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown task = %v, want ErrNotFound", err)
	}
	if err := f.db.AddTaskToRoutine(f.h.ID, "no-such-routine", single.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown routine = %v, want ErrNotFound", err)
	}
}

// Acceptance: Given a parent reorders steps, then the new order is stored.
func TestRoutineOrderCanBeChanged(t *testing.T) {
	f := newTaskFixture(t)
	r := f.newRoutine(t, f.mia, "Morgenroutine", task.Morning)
	ids := f.routineSteps(t, r, "Zähne putzen", "Anziehen", "Frühstücken", "Ranzen packen")
	if err := f.db.SetRoutineOrder(f.h.ID, r.ID, []string{ids[0], ids[2], ids[1], ids[3]}); err != nil {
		t.Fatal(err)
	}
	if got := f.order(t, r.ID); !equal(got, []string{"Zähne putzen", "Frühstücken", "Anziehen", "Ranzen packen"}) {
		t.Fatalf("order = %v", got)
	}
	for name, bad := range map[string][]string{
		"missing one": {ids[0], ids[1], ids[2]},
		"extra":       {ids[0], ids[1], ids[2], ids[3], "x"},
		"duplicate":   {ids[0], ids[0], ids[1], ids[2]},
		"foreign":     {ids[0], ids[1], ids[2], "x"},
	} {
		if err := f.db.SetRoutineOrder(f.h.ID, r.ID, bad); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s = %v, want ErrInvalid", name, err)
		}
	}
	if got := f.order(t, r.ID); !equal(got, []string{"Zähne putzen", "Frühstücken", "Anziehen", "Ranzen packen"}) {
		t.Fatalf("a refused order changed the routine: %v", got)
	}
}

func TestTaskLeavesARoutineAndKeepsItsPartOfDay(t *testing.T) {
	f := newTaskFixture(t)
	morning := f.newRoutine(t, f.mia, "Morgenroutine", task.Morning)
	ids := f.routineSteps(t, morning, "Zähne putzen", "Anziehen", "Frühstücken")
	if err := f.db.RemoveTaskFromRoutine(f.h.ID, ids[1]); err != nil {
		t.Fatal(err)
	}
	got, _ := f.db.Task(f.h.ID, ids[1])
	if got.RoutineID != "" || got.PartOfDay != task.Morning || got.Position != 0 {
		t.Fatalf("removed task = %+v, want a single morning task", got)
	}
	if o := f.order(t, morning.ID); !equal(o, []string{"Zähne putzen", "Frühstücken"}) {
		t.Fatalf("order = %v, want the rest numbered 1, 2", o)
	}
	// "After school" exists only in routines: a task leaving it is for any time.
	after := f.newRoutine(t, f.mia, "Nachmittag", task.AfterSchool)
	aid := f.routineSteps(t, after, "Hausaufgaben")
	if err := f.db.RemoveTaskFromRoutine(f.h.ID, aid[0]); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.Task(f.h.ID, aid[0]); got.PartOfDay != task.AnyTime || got.RoutineID != "" {
		t.Fatalf("task out of after school = %+v, want any time", got)
	}
	if err := f.db.RemoveTaskFromRoutine(f.h.ID, aid[0]); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("removing a single task = %v, want ErrInvalid", err)
	}
	if err := f.db.RemoveTaskFromRoutine(f.h.ID, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown task = %v, want ErrNotFound", err)
	}
}

// Dissolving a routine deletes no task, no done day and no star booking.
func TestDissolvingARoutineKeepsItsTasks(t *testing.T) {
	f := newTaskFixture(t)
	r := f.newRoutine(t, f.mia, "Morgenroutine", task.Morning)
	ids := f.routineSteps(t, r, "Zähne putzen", "Anziehen")
	if err := f.db.SetTaskDone(f.h.ID, ids[0], "2026-11-14", true, f.mia.ID, taskNow); err != nil {
		t.Fatal(err)
	}
	if err := f.db.DissolveRoutine(f.h.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Routine(f.h.ID, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("dissolved routine = %v, want ErrNotFound", err)
	}
	for _, id := range ids {
		got, err := f.db.Task(f.h.ID, id)
		if err != nil || got.RoutineID != "" || got.PartOfDay != task.Morning {
			t.Fatalf("task after dissolving = %+v, %v; want a single morning task", got, err)
		}
	}
	if stars, done, _ := f.db.TaskDoneStars(ids[0], "2026-11-14"); !done || stars != 1 {
		t.Fatalf("done day after dissolving: stars %d, done %v", stars, done)
	}
	// An after-school routine's tasks become any-time tasks.
	after := f.newRoutine(t, f.mia, "Nachmittag", task.AfterSchool)
	aid := f.routineSteps(t, after, "Hausaufgaben")
	if err := f.db.DissolveRoutine(f.h.ID, after.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.Task(f.h.ID, aid[0]); got.PartOfDay != task.AnyTime {
		t.Fatalf("task of a dissolved after-school routine is %q, want any time", got.PartOfDay)
	}
	// The part is free again.
	f.newRoutine(t, f.mia, "Neu", task.Morning)
	if err := f.db.DissolveRoutine(f.h.ID, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("dissolving twice = %v, want ErrNotFound", err)
	}
}

func TestUpdateRoutine(t *testing.T) {
	f := newTaskFixture(t)
	morning := f.newRoutine(t, f.mia, "Morgenroutine", task.Morning)
	f.newRoutine(t, f.mia, "Abendroutine", task.Evening)
	if err := f.db.UpdateRoutine(f.h.ID, morning.ID, "Schulmorgen", task.Morning); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.Routine(f.h.ID, morning.ID); got.Name != "Schulmorgen" {
		t.Fatalf("name = %q", got.Name)
	}
	var exists *store.RoutineExistsError
	if err := f.db.UpdateRoutine(f.h.ID, morning.ID, "x", task.Evening); !errors.Is(err, store.ErrConflict) || !errors.As(err, &exists) || exists.Existing.Name != "Abendroutine" {
		t.Fatalf("moving onto a taken part = %v, want ErrConflict naming Abendroutine", err)
	}
	if err := f.db.UpdateRoutine(f.h.ID, morning.ID, " ", task.Morning); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("blank name = %v, want ErrInvalid", err)
	}
	if err := f.db.UpdateRoutine(f.h.ID, "nope", "x", task.Morning); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown routine = %v, want ErrNotFound", err)
	}
}

func TestDeletingATaskClosesTheGapInItsRoutine(t *testing.T) {
	f := newTaskFixture(t)
	r := f.newRoutine(t, f.mia, "Morgenroutine", task.Morning)
	ids := f.routineSteps(t, r, "Zähne putzen", "Anziehen", "Frühstücken")
	if err := f.db.DeleteTask(f.h.ID, ids[0]); err != nil {
		t.Fatal(err)
	}
	if got := f.order(t, r.ID); !equal(got, []string{"Anziehen", "Frühstücken"}) {
		t.Fatalf("order = %v", got)
	}
}

// Two requests that both pass the pre-check must not end in a raw SQLite
// error: the loser of the race sees the same RoutineExistsError (409).
func TestInsertRoutineMapsTheUniqueConstraintToRoutineExists(t *testing.T) {
	f := newTaskFixture(t)
	first := f.newRoutine(t, f.mia, "Morning routine", task.Morning)
	err := f.db.InsertRoutineUnchecked(f.h.ID, task.Routine{PersonID: f.mia.ID, Name: "Other", Part: task.Morning})
	var exists *store.RoutineExistsError
	if !errors.As(err, &exists) || exists.Existing.ID != first.ID || !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second insert = %v, want RoutineExistsError for %s", err, first.ID)
	}
}
