package store_test

import (
	"errors"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

const subDay = "2026-11-14"

// schoolBag is Mia's daily "Pack school bag" worth 2 stars with three subtasks.
func (f taskFixture) schoolBag(t *testing.T) (task.Task, []task.Subtask) {
	t.Helper()
	k, err := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: "Pack school bag", Repeat: task.Daily, Date: subDay, Stars: 2})
	if err != nil {
		t.Fatal(err)
	}
	var subs []task.Subtask
	for _, title := range []string{"Books", "Pencil case", "Lunch box"} {
		s, err := f.db.AddSubtask(f.h.ID, k.ID, task.Subtask{Title: title}, subDay, taskNow)
		if err != nil {
			t.Fatal(err)
		}
		subs = append(subs, s)
	}
	return k, subs
}

func (f taskFixture) miaBalance(t *testing.T) int {
	t.Helper()
	acc, err := f.db.StarAccount(f.h.ID, f.mia.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	return acc.Balance
}

func (f taskFixture) taskDone(t *testing.T, id, day string) bool {
	t.Helper()
	done, err := f.db.TaskDone(id, day)
	if err != nil {
		t.Fatal(err)
	}
	return done
}

func (f taskFixture) subDone(t *testing.T, taskID, subID, day string) bool {
	t.Helper()
	list, err := f.db.Subtasks(f.h.ID, taskID, day)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.ID == subID {
			return s.Done
		}
	}
	t.Fatalf("subtask %s not found", subID)
	return false
}

// Acceptance: Given "Pack school bag" with three subtasks, when all three are
// ticked, then the task is done and its stars are credited once.
func TestTickingTheLastSubtaskCompletesTheTaskAndCreditsStarsOnce(t *testing.T) {
	f := newTaskFixture(t)
	k, subs := f.schoolBag(t)
	for i, s := range subs {
		ch, err := f.db.SetSubtaskDone(f.h.ID, k.ID, s.ID, subDay, true, f.mama.ID, taskNow)
		if err != nil {
			t.Fatal(err)
		}
		last := i == len(subs)-1
		if ch.TaskChanged != last || ch.TaskDone != last {
			t.Fatalf("tick %d: change = %+v", i+1, ch)
		}
		if f.taskDone(t, k.ID, subDay) != last {
			t.Fatalf("tick %d: task done = %v", i+1, !last)
		}
	}
	if got := f.miaBalance(t); got != 2 {
		t.Fatalf("balance = %d, want 2", got)
	}
	// Ticking a subtask again changes nothing and credits nothing.
	if ch, err := f.db.SetSubtaskDone(f.h.ID, k.ID, subs[2].ID, subDay, true, f.mama.ID, taskNow); err != nil || ch.TaskChanged {
		t.Fatalf("second tick = %+v, %v", ch, err)
	}
	if got := f.miaBalance(t); got != 2 {
		t.Fatalf("balance after repeat = %d, want 2", got)
	}
}

// Acceptance: Given a done task, when one subtask is unticked, then the task is
// open again and its stars are deducted. The other subtasks stay ticked.
func TestUntickingASubtaskOfADoneTaskReopensItAndDeductsStars(t *testing.T) {
	f := newTaskFixture(t)
	k, subs := f.schoolBag(t)
	for _, s := range subs {
		if _, err := f.db.SetSubtaskDone(f.h.ID, k.ID, s.ID, subDay, true, "", taskNow); err != nil {
			t.Fatal(err)
		}
	}
	ch, err := f.db.SetSubtaskDone(f.h.ID, k.ID, subs[1].ID, subDay, false, "", taskNow)
	if err != nil || !ch.TaskChanged || ch.TaskDone || ch.Stars != 2 {
		t.Fatalf("untick = %+v, %v", ch, err)
	}
	if f.taskDone(t, k.ID, subDay) || f.miaBalance(t) != 0 {
		t.Fatalf("task done = %v, balance = %d", f.taskDone(t, k.ID, subDay), f.miaBalance(t))
	}
	if !f.subDone(t, k.ID, subs[0].ID, subDay) || !f.subDone(t, k.ID, subs[2].ID, subDay) || f.subDone(t, k.ID, subs[1].ID, subDay) {
		t.Fatal("only the unticked subtask should be open")
	}
}

// Ticking the task ticks all its subtasks (credited once); unticking the task
// unticks all of them and deducts the stars (product owner Q7).
func TestTickingAndUntickingTheTaskTicksAndUntickAllSubtasks(t *testing.T) {
	f := newTaskFixture(t)
	k, subs := f.schoolBag(t)
	if _, err := f.db.SetSubtaskDone(f.h.ID, k.ID, subs[0].ID, subDay, true, "", taskNow); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetTaskDone(f.h.ID, k.ID, subDay, true, "", taskNow); err != nil {
		t.Fatal(err)
	}
	for _, s := range subs {
		if !f.subDone(t, k.ID, s.ID, subDay) {
			t.Fatalf("%s should be ticked", s.Title)
		}
	}
	if f.miaBalance(t) != 2 {
		t.Fatalf("balance = %d, want 2", f.miaBalance(t))
	}
	if err := f.db.SetTaskDone(f.h.ID, k.ID, subDay, false, "", taskNow); err != nil {
		t.Fatal(err)
	}
	for _, s := range subs {
		if f.subDone(t, k.ID, s.ID, subDay) {
			t.Fatalf("%s should be open", s.Title)
		}
	}
	if f.miaBalance(t) != 0 {
		t.Fatalf("balance = %d, want 0", f.miaBalance(t))
	}
}

// Ticks belong to one occurrence: the next day starts open again (Q8).
func TestSubtaskTicksBelongToTheOccurrence(t *testing.T) {
	f := newTaskFixture(t)
	k, subs := f.schoolBag(t)
	if _, err := f.db.SetSubtaskDone(f.h.ID, k.ID, subs[0].ID, subDay, true, "", taskNow); err != nil {
		t.Fatal(err)
	}
	if f.subDone(t, k.ID, subs[0].ID, "2026-11-15") {
		t.Fatal("tomorrow must start open")
	}
}

func TestSubtaskOfATaskNotDueIsRefused(t *testing.T) {
	f := newTaskFixture(t)
	k, err := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: "Piano", Repeat: task.Weekly, Weekdays: []string{"MO"}, Date: subDay})
	if err != nil {
		t.Fatal(err)
	}
	s, err := f.db.AddSubtask(f.h.ID, k.ID, task.Subtask{Title: "Scales"}, subDay, taskNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.SetSubtaskDone(f.h.ID, k.ID, s.ID, subDay, true, "", taskNow); !errors.Is(err, store.ErrInvalid) { // a Saturday
		t.Fatalf("not due = %v, want store.ErrInvalid", err)
	}
}

// A subtask added to a task that is done today counts as done today (Q8).
func TestSubtaskAddedToADoneTaskCountsAsDone(t *testing.T) {
	f := newTaskFixture(t)
	k, _ := f.schoolBag(t)
	if err := f.db.SetTaskDone(f.h.ID, k.ID, subDay, true, "", taskNow); err != nil {
		t.Fatal(err)
	}
	s, err := f.db.AddSubtask(f.h.ID, k.ID, task.Subtask{Title: "Water bottle"}, subDay, taskNow)
	if err != nil {
		t.Fatal(err)
	}
	if !f.subDone(t, k.ID, s.ID, subDay) || !f.taskDone(t, k.ID, subDay) || f.miaBalance(t) != 2 {
		t.Fatalf("subtask done = %v, task done = %v, balance = %d", f.subDone(t, k.ID, s.ID, subDay), f.taskDone(t, k.ID, subDay), f.miaBalance(t))
	}
}

// Removing the last open subtask completes the task and credits its stars.
func TestRemovingTheLastOpenSubtaskCompletesTheTask(t *testing.T) {
	f := newTaskFixture(t)
	k, subs := f.schoolBag(t)
	for _, s := range subs[:2] {
		if _, err := f.db.SetSubtaskDone(f.h.ID, k.ID, s.ID, subDay, true, "", taskNow); err != nil {
			t.Fatal(err)
		}
	}
	ch, err := f.db.DeleteSubtask(f.h.ID, k.ID, subs[2].ID, subDay, "", taskNow)
	if err != nil || !ch.TaskChanged || !ch.TaskDone || ch.Stars != 2 {
		t.Fatalf("delete = %+v, %v", ch, err)
	}
	if !f.taskDone(t, k.ID, subDay) || f.miaBalance(t) != 2 {
		t.Fatalf("task done = %v, balance = %d", f.taskDone(t, k.ID, subDay), f.miaBalance(t))
	}
}

// Removing the only subtask leaves a plain open task: nothing is completed.
func TestRemovingTheOnlySubtaskDoesNotCompleteTheTask(t *testing.T) {
	f := newTaskFixture(t)
	k, err := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: "Tidy", Repeat: task.Daily, Date: subDay, Stars: 1})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := f.db.AddSubtask(f.h.ID, k.ID, task.Subtask{Title: "Bed"}, subDay, taskNow)
	ch, err := f.db.DeleteSubtask(f.h.ID, k.ID, s.ID, subDay, "", taskNow)
	if err != nil || ch.TaskChanged || f.taskDone(t, k.ID, subDay) {
		t.Fatalf("delete = %+v, %v", ch, err)
	}
}

func TestSubtaskLimitsAndValidation(t *testing.T) {
	f := newTaskFixture(t)
	k, _ := f.schoolBag(t)
	for i := 3; i < task.MaxSubtasks; i++ {
		if _, err := f.db.AddSubtask(f.h.ID, k.ID, task.Subtask{Title: "More"}, subDay, taskNow); err != nil {
			t.Fatal(err)
		}
	}
	_, err := f.db.AddSubtask(f.h.ID, k.ID, task.Subtask{Title: "One too many"}, subDay, taskNow)
	var bad *task.InvalidError
	if !errors.Is(err, store.ErrInvalid) || !errors.As(err, &bad) || bad.Field != "subtasks" {
		t.Fatalf("11th subtask = %v, want a refusal naming subtasks", err)
	}
	g, _ := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: "Other", Repeat: task.Daily, Date: subDay})
	if _, err := f.db.AddSubtask(f.h.ID, g.ID, task.Subtask{Title: "  "}, subDay, taskNow); !errors.As(err, &bad) || bad.Field != "title" {
		t.Fatalf("blank title = %v", err)
	}
	if _, err := f.db.AddSubtask(f.h.ID, g.ID, task.Subtask{Title: "x", Emoji: "12345678901234567"}, subDay, taskNow); !errors.As(err, &bad) || bad.Field != "emoji" {
		t.Fatalf("long emoji = %v", err)
	}
	if _, err := f.db.AddSubtask(f.h.ID, "no-such-task", task.Subtask{Title: "x"}, subDay, taskNow); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown task = %v", err)
	}
}

func TestSubtasksKeepTheirOrderAndCanBeMovedAndEdited(t *testing.T) {
	f := newTaskFixture(t)
	k, subs := f.schoolBag(t)
	titles := func() []string {
		list, err := f.db.Subtasks(f.h.ID, k.ID, subDay)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, s := range list {
			out = append(out, s.Title)
		}
		return out
	}
	if err := f.db.MoveSubtask(f.h.ID, k.ID, subs[2].ID, false); err != nil { // up
		t.Fatal(err)
	}
	if got := titles(); got[0] != "Books" || got[1] != "Lunch box" || got[2] != "Pencil case" {
		t.Fatalf("order = %v", got)
	}
	if err := f.db.MoveSubtask(f.h.ID, k.ID, subs[0].ID, true); err != nil { // down
		t.Fatal(err)
	}
	if got := titles(); got[0] != "Lunch box" || got[1] != "Books" {
		t.Fatalf("order = %v", got)
	}
	if err := f.db.UpdateSubtask(f.h.ID, k.ID, subs[0].ID, " Schoolbooks ", "📚"); err != nil {
		t.Fatal(err)
	}
	if got := titles(); got[1] != "Schoolbooks" {
		t.Fatalf("title = %v", got)
	}
}

func TestSubtasksOfDayAreGroupedPerTaskAndDeletedWithTheTask(t *testing.T) {
	f := newTaskFixture(t)
	k, subs := f.schoolBag(t)
	if _, err := f.db.SetSubtaskDone(f.h.ID, k.ID, subs[0].ID, subDay, true, "", taskNow); err != nil {
		t.Fatal(err)
	}
	by, err := f.db.SubtasksByTask(f.h.ID, subDay)
	if err != nil || len(by[k.ID]) != 3 || !by[k.ID][0].Done || by[k.ID][1].Done {
		t.Fatalf("by task = %+v, %v", by, err)
	}
	due, err := f.db.TasksForDay(f.h.ID, subDay)
	if err != nil || len(due) != 1 || len(due[0].Subtasks) != 3 || !due[0].Subtasks[0].Done {
		t.Fatalf("day tasks = %+v, %v", due, err)
	}
	if err := f.db.DeleteTask(f.h.ID, k.ID); err != nil {
		t.Fatal(err)
	}
	if by, _ := f.db.SubtasksByTask(f.h.ID, subDay); len(by) != 0 {
		t.Fatalf("subtasks outlived their task: %+v", by)
	}
}
