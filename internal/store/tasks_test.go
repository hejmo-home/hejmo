package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

type taskFixture struct {
	db        *store.DB
	h         store.Household
	mama, mia store.Person
}

func newTaskFixture(t *testing.T) taskFixture {
	t.Helper()
	db, _ := openTemp(t)
	h, mama := seedParent(t, db)
	mia, err := db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	if err != nil {
		t.Fatal(err)
	}
	return taskFixture{db, h, mama, mia}
}

var taskNow = time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)

func TestTaskRoundTrip(t *testing.T) {
	f := newTaskFixture(t)
	in := task.Task{PersonID: f.mia.ID, Title: "Piano", Emoji: "🎹", Repeat: task.Weekly, Weekdays: []string{"WE", "MO"}, Date: "2026-11-14", PartOfDay: task.Evening}
	got, err := f.db.AddTask(f.h.ID, in)
	if err != nil || got.ID == "" {
		t.Fatalf("AddTask = %+v, %v", got, err)
	}
	list, err := f.db.Tasks(f.h.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("Tasks = %v, %v", list, err)
	}
	g := list[0]
	if g.Title != "Piano" || g.Emoji != "🎹" || g.PersonID != f.mia.ID || g.Repeat != task.Weekly ||
		len(g.Weekdays) != 2 || g.Weekdays[0] != "MO" || g.Date != "2026-11-14" || g.PartOfDay != task.Evening {
		t.Fatalf("round trip = %+v", g)
	}
}

func TestAddTaskRefusesInvalidAndForeignPeople(t *testing.T) {
	f := newTaskFixture(t)
	if _, err := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: "", Repeat: task.Daily, Date: "2026-11-14"}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("blank title = %v, want store.ErrInvalid", err)
	}
	if _, err := f.db.AddTask(f.h.ID, task.Task{PersonID: "no-such-person", Title: "x", Repeat: task.Daily, Date: "2026-11-14"}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("unknown person = %v, want store.ErrInvalid", err)
	}
}

// Acceptance: Given a daily task for Mia, when it is ticked off today, then it
// is open again tomorrow.
func TestDailyTaskTickedOffTodayIsOpenTomorrow(t *testing.T) {
	f := newTaskFixture(t)
	k, _ := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: "Brush teeth", Repeat: task.Daily, Date: "2026-11-14"})
	if err := f.db.SetTaskDone(f.h.ID, k.ID, "2026-11-14", true, f.mia.ID, taskNow); err != nil {
		t.Fatal(err)
	}
	today, _ := f.db.TasksForDay(f.h.ID, "2026-11-14")
	tomorrow, _ := f.db.TasksForDay(f.h.ID, "2026-11-15")
	if len(today) != 1 || !today[0].Done || len(tomorrow) != 1 || tomorrow[0].Done {
		t.Fatalf("today = %+v, tomorrow = %+v", today, tomorrow)
	}
	// Unticking makes it open again.
	if err := f.db.SetTaskDone(f.h.ID, k.ID, "2026-11-14", false, f.mia.ID, taskNow); err != nil {
		t.Fatal(err)
	}
	if again, _ := f.db.TasksForDay(f.h.ID, "2026-11-14"); again[0].Done {
		t.Fatal("task still done after untick")
	}
}

func TestTickingTwiceIsHarmlessAndOnlyDueDaysCanBeTicked(t *testing.T) {
	f := newTaskFixture(t)
	k, _ := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: "Once", Repeat: task.Once, Date: "2026-11-16"})
	if err := f.db.SetTaskDone(f.h.ID, k.ID, "2026-11-14", true, f.mia.ID, taskNow); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("not due = %v, want ErrInvalid", err)
	}
	for i := 0; i < 2; i++ {
		if err := f.db.SetTaskDone(f.h.ID, k.ID, "2026-11-16", true, f.mia.ID, taskNow); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.SetTaskDone(f.h.ID, "nope", "2026-11-16", true, f.mia.ID, taskNow); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown task = %v, want ErrNotFound", err)
	}
}

func TestTasksForDayListsOnlyDueTasksPerPerson(t *testing.T) {
	f := newTaskFixture(t)
	f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: "Mon", Repeat: task.Weekly, Weekdays: []string{"MO"}, Date: "2026-11-01"})
	f.db.AddTask(f.h.ID, task.Task{PersonID: f.mama.ID, Title: "Daily", Repeat: task.Daily, Date: "2026-11-01"})
	// 14/11/2026 is a Saturday.
	got, err := f.db.TasksForDay(f.h.ID, "2026-11-14")
	if err != nil || len(got) != 1 || got[0].Title != "Daily" || got[0].PersonID != f.mama.ID {
		t.Fatalf("TasksForDay = %+v, %v", got, err)
	}
}

func TestDeleteTaskAndCascadeWithPerson(t *testing.T) {
	f := newTaskFixture(t)
	k, _ := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: "x", Repeat: task.Daily, Date: "2026-11-14"})
	_ = f.db.SetTaskDone(f.h.ID, k.ID, "2026-11-14", true, f.mia.ID, taskNow)
	if err := f.db.DeleteTask(f.h.ID, k.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.db.DeleteTask(f.h.ID, k.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
	if list, _ := f.db.Tasks(f.h.ID); len(list) != 0 {
		t.Fatalf("tasks left: %v", list)
	}
}
