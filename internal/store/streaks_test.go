package store_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/streak"
	"github.com/hejmo-home/hejmo/internal/task"
)

func TestStreaksSwitchDefaultsByRoleAndCanBeChanged(t *testing.T) {
	f := newTaskFixture(t)
	mia, _ := f.db.Person(f.h.ID, f.mia.ID)
	mama, _ := f.db.Person(f.h.ID, f.mama.ID)
	if !mia.StreaksShown || mama.StreaksShown {
		t.Fatalf("defaults: child %v, parent %v; want true, false", mia.StreaksShown, mama.StreaksShown)
	}
	if err := f.db.SetStreaksShown(f.h.ID, mia.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetStreaksShown(f.h.ID, mama.ID, true); err != nil {
		t.Fatal(err)
	}
	people, _ := f.db.People(f.h.ID)
	for _, p := range people {
		if p.ID == mia.ID && p.StreaksShown || p.ID == mama.ID && !p.StreaksShown {
			t.Errorf("after switching: %+v", p)
		}
	}
	if err := f.db.SetStreaksShown(f.h.ID, "nobody", true); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown person = %v", err)
	}
}

func TestRoutineRemembersTheDayItWasCreated(t *testing.T) {
	f := newTaskFixture(t)
	at := time.Date(2026, 11, 9, 23, 30, 0, 0, time.UTC)
	r, err := f.db.CreateRoutine(f.h.ID, task.Routine{PersonID: f.mia.ID, Name: "Morning", Part: task.Morning, Created: at})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := f.db.Routine(f.h.ID, r.ID)
	if !got.Created.Equal(at) {
		t.Fatalf("Created = %v, want %v", got.Created, at)
	}
	fresh := f.newRoutine(t, f.mama, "Evening", task.Evening)
	got, _ = f.db.Routine(f.h.ID, fresh.ID)
	if time.Since(got.Created) > time.Minute || got.Created.IsZero() {
		t.Errorf("a routine without a time is created now, got %v", got.Created)
	}
}

func TestRoutineDoneListsTheTickedDaysOfRoutineTasksOnly(t *testing.T) {
	f := newTaskFixture(t)
	r := f.newRoutine(t, f.mia, "Morning", task.Morning)
	ids := f.routineSteps(t, r, "Teeth")
	single, _ := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: "Cat", Repeat: task.Daily, Date: "2026-11-01"})
	for _, d := range []string{"2026-11-14", "2026-11-15"} {
		if err := f.db.SetTaskDone(f.h.ID, ids[0], d, true, "", taskNow); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.SetTaskDone(f.h.ID, single.ID, "2026-11-14", true, "", taskNow); err != nil {
		t.Fatal(err)
	}
	got, err := f.db.RoutineDone(f.h.ID)
	want := map[string]map[string]bool{ids[0]: {"2026-11-14": true, "2026-11-15": true}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("RoutineDone = %v, %v; want %v", got, err, want)
	}
	if err := f.db.SetTaskDone(f.h.ID, ids[0], "2026-11-15", false, "", taskNow); err != nil {
		t.Fatal(err)
	}
	got, _ = f.db.RoutineDone(f.h.ID)
	if got[ids[0]]["2026-11-15"] {
		t.Errorf("unticked day is still there: %v", got)
	}
}

func TestPausesAreStoredForPeopleOrTheWholeFamily(t *testing.T) {
	f := newTaskFixture(t)
	whole, err := f.db.SavePause(f.h.ID, streak.Pause{From: "2026-12-19", To: "2027-01-03"}, f.mama.ID)
	if err != nil || whole.ID == "" || len(whole.People) != 0 {
		t.Fatalf("whole family = %+v, %v", whole, err)
	}
	one, err := f.db.SavePause(f.h.ID, streak.Pause{From: "2026-11-25", To: "2026-11-26", People: []string{f.mia.ID}}, f.mama.ID)
	if err != nil {
		t.Fatal(err)
	}
	list, err := f.db.Pauses(f.h.ID)
	if err != nil || len(list) != 2 || list[0].ID != one.ID || list[1].ID != whole.ID {
		t.Fatalf("Pauses = %+v, %v (want by start day)", list, err)
	}
	if !reflect.DeepEqual(list[0].People, []string{f.mia.ID}) {
		t.Errorf("people = %v", list[0].People)
	}
	got, err := f.db.Pause(f.h.ID, one.ID)
	if err != nil || got.From != "2026-11-25" {
		t.Fatalf("Pause = %+v, %v", got, err)
	}
	if _, err := f.db.Pause(f.h.ID, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown = %v", err)
	}
}

func TestPauseInputIsValidated(t *testing.T) {
	f := newTaskFixture(t)
	_, err := f.db.SavePause(f.h.ID, streak.Pause{From: "2026-11-10", To: "2026-11-09"}, f.mama.ID)
	var pe *streak.PauseError
	if !errors.Is(err, store.ErrInvalid) || !errors.As(err, &pe) || pe.Field != "to" || pe.Code != "order" {
		t.Errorf("order = %v", err)
	}
	if _, err := f.db.SavePause(f.h.ID, streak.Pause{From: "2026-11-10", To: "2026-11-10", People: []string{"stranger"}}, f.mama.ID); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("unknown person = %v", err)
	}
	// Past days are fine.
	if _, err := f.db.SavePause(f.h.ID, streak.Pause{From: "2020-01-01", To: "2020-01-02"}, f.mama.ID); err != nil {
		t.Errorf("past = %v", err)
	}
}

func TestOverlappingPausesOfTheSamePeopleAreMerged(t *testing.T) {
	f := newTaskFixture(t)
	a, _ := f.db.SavePause(f.h.ID, streak.Pause{From: "2026-11-10", To: "2026-11-12", People: []string{f.mia.ID}}, f.mama.ID)
	b, err := f.db.SavePause(f.h.ID, streak.Pause{From: "2026-11-12", To: "2026-11-15", People: []string{f.mia.ID}}, f.mama.ID)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := f.db.Pauses(f.h.ID)
	if len(list) != 1 || list[0].From != "2026-11-10" || list[0].To != "2026-11-15" {
		t.Fatalf("merged = %+v (a=%s b=%s)", list, a.ID, b.ID)
	}
	if b.From != "2026-11-10" || b.To != "2026-11-15" {
		t.Errorf("result of the save = %+v", b)
	}
	// Another audience, or days that only touch, stay apart.
	f.db.SavePause(f.h.ID, streak.Pause{From: "2026-11-11", To: "2026-11-12"}, f.mama.ID)
	f.db.SavePause(f.h.ID, streak.Pause{From: "2026-11-16", To: "2026-11-17", People: []string{f.mia.ID}}, f.mama.ID)
	if list, _ := f.db.Pauses(f.h.ID); len(list) != 3 {
		t.Errorf("pauses = %+v, want 3", list)
	}
}

func TestChangingAPauseMergesItWithWhatItNowReaches(t *testing.T) {
	f := newTaskFixture(t)
	a, _ := f.db.SavePause(f.h.ID, streak.Pause{From: "2026-11-10", To: "2026-11-11"}, f.mama.ID)
	b, _ := f.db.SavePause(f.h.ID, streak.Pause{From: "2026-11-20", To: "2026-11-21"}, f.mama.ID)
	got, err := f.db.UpdatePause(f.h.ID, streak.Pause{ID: a.ID, From: "2026-11-10", To: "2026-11-20"})
	if err != nil || got.ID != a.ID || got.To != "2026-11-21" {
		t.Fatalf("UpdatePause = %+v, %v", got, err)
	}
	if list, _ := f.db.Pauses(f.h.ID); len(list) != 1 {
		t.Fatalf("pauses = %+v", list)
	}
	if _, err := f.db.Pause(f.h.ID, b.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("absorbed pause is still there: %v", err)
	}
	if _, err := f.db.UpdatePause(f.h.ID, streak.Pause{ID: "nope", From: "2026-11-10", To: "2026-11-11"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown = %v", err)
	}
	if got, err := f.db.UpdatePause(f.h.ID, streak.Pause{ID: a.ID, From: "2026-11-10", To: "2026-11-21", People: []string{f.mia.ID}}); err != nil || !reflect.DeepEqual(got.People, []string{f.mia.ID}) {
		t.Errorf("change of people = %+v, %v", got, err)
	}
}

func TestDeletingAPauseLeavesNothingBehind(t *testing.T) {
	f := newTaskFixture(t)
	a, _ := f.db.SavePause(f.h.ID, streak.Pause{From: "2026-11-10", To: "2026-11-11", People: []string{f.mia.ID}}, f.mama.ID)
	if err := f.db.DeletePause(f.h.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := f.db.Pauses(f.h.ID); len(list) != 0 {
		t.Fatalf("left: %+v", list)
	}
	var n int
	f.db.SQL().QueryRow(`SELECT COUNT(*) FROM pause_person`).Scan(&n)
	if n != 0 {
		t.Errorf("%d pause_person rows left", n)
	}
	if err := f.db.DeletePause(f.h.ID, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second delete = %v", err)
	}
}
