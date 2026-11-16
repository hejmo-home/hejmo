package store_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

func sp(s string) *string { return &s }

func (f taskFixture) once(t *testing.T) task.Task {
	t.Helper()
	k, err := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: "Pack swim bag", Notes: "Towel", Repeat: task.Once, Date: "2026-11-20", Stars: 2})
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// ADR 0018: a task has an optional free-text note.
func TestTaskNotesRoundTripAndAreBounded(t *testing.T) {
	f := newTaskFixture(t)
	k := f.once(t)
	got, err := f.db.Task(f.h.ID, k.ID)
	if err != nil || got.Notes != "Towel" || got.UpdatedAt.IsZero() {
		t.Fatalf("Task = %+v, %v", got, err)
	}
	long := task.Task{PersonID: f.mia.ID, Title: "x", Notes: strings.Repeat("n", task.MaxNotes+1), Repeat: task.Daily, Date: "2026-11-14"}
	if _, err := f.db.AddTask(f.h.ID, long); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("overlong notes = %v, want ErrInvalid", err)
	}
}

func TestEditTaskChangesTitleNotesAndTheDayOfAOnceTaskOnly(t *testing.T) {
	f := newTaskFixture(t)
	k := f.once(t)
	if err := f.db.EditTask(f.h.ID, k.ID, store.TaskEdit{Title: sp("  Pack the swim bag "), Notes: sp(""), Date: sp("2026-11-25")}); err != nil {
		t.Fatal(err)
	}
	got, _ := f.db.Task(f.h.ID, k.ID)
	if got.Title != "Pack the swim bag" || got.Notes != "" || got.Date != "2026-11-25" {
		t.Fatalf("after edit: %+v", got)
	}
	if got.Stars != 2 || got.PersonID != f.mia.ID || got.Repeat != task.Once {
		t.Fatalf("an edit touched what it must not: %+v", got)
	}
	// Nil fields stay as they are.
	if err := f.db.EditTask(f.h.ID, k.ID, store.TaskEdit{Notes: sp("Goggles")}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.Task(f.h.ID, k.ID); got.Title != "Pack the swim bag" || got.Notes != "Goggles" {
		t.Fatalf("partial edit: %+v", got)
	}
}

func TestEditTaskRefusesWhatCannotBeStored(t *testing.T) {
	f := newTaskFixture(t)
	k := f.once(t)
	daily := f.starTask(t, 1)
	for name, c := range map[string]struct {
		id string
		e  store.TaskEdit
	}{
		"blank title":            {k.ID, store.TaskEdit{Title: sp(" ")}},
		"bad date":               {k.ID, store.TaskEdit{Date: sp("tomorrow")}},
		"a day for a daily task": {daily.ID, store.TaskEdit{Date: sp("2026-12-01")}},
	} {
		if err := f.db.EditTask(f.h.ID, c.id, c.e); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s = %v, want ErrInvalid", name, err)
		}
	}
	if err := f.db.EditTask(f.h.ID, "nope", store.TaskEdit{Title: sp("x")}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown task = %v, want ErrNotFound", err)
	}
}

// A done once task stays done when the day moves, and its credited stars stay.
func TestMovingADoneOnceTaskKeepsItDoneAndItsStars(t *testing.T) {
	f := newTaskFixture(t)
	k := f.once(t)
	if err := f.db.SetTaskDone(f.h.ID, k.ID, "2026-11-20", true, "", taskNow); err != nil {
		t.Fatal(err)
	}
	if err := f.db.EditTask(f.h.ID, k.ID, store.TaskEdit{Date: sp("2026-11-22")}); err != nil {
		t.Fatal(err)
	}
	if done, _ := f.db.TaskDone(k.ID, "2026-11-22"); !done {
		t.Fatal("the task is open after its day moved")
	}
	if done, _ := f.db.TaskDone(k.ID, "2026-11-20"); done {
		t.Fatal("the old day is still done")
	}
	if got := f.balance(t, f.mia.ID); got != 2 {
		t.Fatalf("balance %d, want 2", got)
	}
	// Unticking on the new day deducts what was credited, once.
	if err := f.db.SetTaskDone(f.h.ID, k.ID, "2026-11-22", false, "", taskNow); err != nil {
		t.Fatal(err)
	}
	if got := f.balance(t, f.mia.ID); got != 0 {
		t.Fatalf("balance after unticking %d, want 0", got)
	}
}

func TestTaskCompletionTimeIsKnownOnceDone(t *testing.T) {
	f := newTaskFixture(t)
	k := f.once(t)
	if _, ok, err := f.db.TaskCompletedAt(k.ID, "2026-11-20"); ok || err != nil {
		t.Fatalf("open task: ok=%v err=%v", ok, err)
	}
	_ = f.db.SetTaskDone(f.h.ID, k.ID, "2026-11-20", true, "", taskNow)
	at, ok, err := f.db.TaskCompletedAt(k.ID, "2026-11-20")
	if err != nil || !ok || !at.Equal(taskNow) {
		t.Fatalf("done task: %v %v %v", at, ok, err)
	}
}

// Clients sync on the ETag (UpdatedAt): saving the same values again must not move it.
func TestEditTaskWithIdenticalValuesLeavesUpdatedAtAlone(t *testing.T) {
	f := newTaskFixture(t)
	k := f.once(t)
	const old = "2026-01-02T03:04:05Z"
	if _, err := f.db.SQL().Exec(`UPDATE tasks SET updated_at = ? WHERE id = ?`, old, k.ID); err != nil {
		t.Fatal(err)
	}
	same := store.TaskEdit{Title: sp(k.Title), Notes: sp(k.Notes), Date: sp(k.Date)}
	if err := f.db.EditTask(f.h.ID, k.ID, same); err != nil {
		t.Fatal(err)
	}
	got, _ := f.db.Task(f.h.ID, k.ID)
	if got.UpdatedAt.UTC().Format(time.RFC3339) != old {
		t.Fatalf("UpdatedAt = %v after a no-op edit, want %s", got.UpdatedAt, old)
	}
	if err := f.db.EditTask(f.h.ID, k.ID, store.TaskEdit{Notes: sp("Goggles")}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.Task(f.h.ID, k.ID); got.UpdatedAt.UTC().Format(time.RFC3339) == old {
		t.Fatal("a real edit did not move UpdatedAt")
	}
}
