package store_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

// miaEvening is Mia's evening routine: three daily steps and a weekly one.
func (f taskFixture) miaEvening(t *testing.T) task.Routine {
	t.Helper()
	r := f.newRoutine(t, f.mia, "Abendroutine", task.Evening)
	add := func(title, emoji string, stars int, weekdays ...string) {
		k := task.Task{PersonID: f.mia.ID, Title: title, Emoji: emoji, Repeat: task.Daily, Date: "2026-11-14", Stars: stars, RoutineID: r.ID}
		if len(weekdays) > 0 {
			k.Repeat, k.Weekdays = task.Weekly, weekdays
		}
		if _, err := f.db.AddTask(f.h.ID, k); err != nil {
			t.Fatal(err)
		}
	}
	add("Zimmer aufräumen", "🧸", 1)
	add("Duschen", "🚿", 2, "MO", "WE", "FR")
	add("Zähne putzen", "🪥", 0)
	return r
}

func (f taskFixture) sebastian(t *testing.T) store.Person {
	t.Helper()
	p, err := f.db.AddPerson(f.h.ID, store.Person{Name: "Sebastian", Role: store.RoleChild})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func titlesOf(steps []task.TemplateStep) []string {
	var out []string
	for _, s := range steps {
		out = append(out, s.Title)
	}
	return out
}

// Acceptance (#92, 3): a parent saves Mia's evening routine as a template.
func TestSaveRoutineAsTemplateCopiesPartAndStepsInOrder(t *testing.T) {
	f := newTaskFixture(t)
	r := f.miaEvening(t)
	tpl, err := f.db.SaveRoutineAsTemplate(f.h.ID, r.ID, " Abendroutine von Mia ")
	if err != nil || tpl.ID == "" {
		t.Fatalf("SaveRoutineAsTemplate = %+v, %v", tpl, err)
	}
	got, err := f.db.RoutineTemplate(f.h.ID, tpl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Abendroutine von Mia" || got.Part != task.Evening || got.Shipped || got.OriginRoutine != "Abendroutine" || got.OriginPerson != "Mia" || got.SavedAt.IsZero() {
		t.Fatalf("template = %+v", got)
	}
	want := []task.TemplateStep{
		{Title: "Zimmer aufräumen", Emoji: "🧸", Stars: 1, Repeat: task.Daily},
		{Title: "Duschen", Emoji: "🚿", Stars: 2, Repeat: task.Weekly, Weekdays: []string{"MO", "WE", "FR"}},
		{Title: "Zähne putzen", Emoji: "🪥", Stars: 0, Repeat: task.Daily},
	}
	for i := range got.Steps {
		if got.Steps[i].ID == "" {
			t.Errorf("step %d has no id", i)
		}
		got.Steps[i].ID = ""
	}
	if !reflect.DeepEqual(got.Steps, want) {
		t.Fatalf("steps = %+v, want %+v", got.Steps, want)
	}
	list, err := f.db.RoutineTemplates(f.h.ID)
	if err != nil || len(list) != 1 || list[0].ID != tpl.ID || len(list[0].Steps) != 3 {
		t.Fatalf("RoutineTemplates = %+v, %v", list, err)
	}
}

// Acceptance (#92, 3): it can be applied to Sebastian. Acceptance (#92, 2): the copy is independent.
func TestApplyingATemplateMakesAnIndependentRoutine(t *testing.T) {
	f := newTaskFixture(t)
	seb := f.sebastian(t)
	tpl, err := f.db.SaveRoutineAsTemplate(f.h.ID, f.miaEvening(t).ID, "Abendroutine von Mia")
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.db.CreateRoutineFromSteps(f.h.ID, task.Routine{PersonID: seb.ID, Name: "Abendroutine", Part: task.Morning}, tpl.Steps, "2026-11-16")
	if err != nil || r.ID == "" {
		t.Fatalf("CreateRoutineFromSteps = %+v, %v", r, err)
	}
	if r.Part != task.Morning || r.PersonID != seb.ID {
		t.Fatalf("routine = %+v: the parent picks the part of the day", r)
	}
	steps, err := f.db.RoutineTasks(f.h.ID, r.ID)
	if err != nil || len(steps) != 3 {
		t.Fatalf("steps = %+v, %v", steps, err)
	}
	if steps[1].Title != "Duschen" || steps[1].Stars != 2 || steps[1].Repeat != task.Weekly || steps[1].Date != "2026-11-16" || steps[1].PersonID != seb.ID || steps[1].PartOfDay != task.Morning {
		t.Fatalf("second step = %+v", steps[1])
	}

	// Changing the copy does not change the template ...
	if err := f.db.SetTaskStars(f.h.ID, steps[0].ID, 9); err != nil {
		t.Fatal(err)
	}
	if err := f.db.RemoveTaskFromRoutine(f.h.ID, steps[2].ID); err != nil {
		t.Fatal(err)
	}
	after, _ := f.db.RoutineTemplate(f.h.ID, tpl.ID)
	if len(after.Steps) != 3 || after.Steps[0].Stars != 1 {
		t.Fatalf("template changed with the copy: %+v", after.Steps)
	}
	// ... and the other way round.
	if err := f.db.UpdateTemplateStep(f.h.ID, tpl.ID, task.TemplateStep{ID: tpl.Steps[1].ID, Title: "Baden", Emoji: "🛁", Stars: 5, Repeat: task.Daily}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.DeleteTemplateStep(f.h.ID, tpl.ID, tpl.Steps[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := f.db.DeleteRoutineTemplate(f.h.ID, tpl.ID); err != nil {
		t.Fatal(err)
	}
	steps, _ = f.db.RoutineTasks(f.h.ID, r.ID)
	if len(steps) != 2 || steps[0].Stars != 9 || steps[1].Title != "Duschen" || steps[1].Stars != 2 {
		t.Fatalf("routine changed with the template: %+v", steps)
	}
	// Mia's routine did not change either.
	if got := f.order(t, f.miaEvening2(t)); len(got) != 3 {
		t.Fatalf("Mia's routine = %v", got)
	}
}

// miaEvening2 finds Mia's evening routine again.
func (f taskFixture) miaEvening2(t *testing.T) string {
	t.Helper()
	list, err := f.db.Routines(f.h.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range list {
		if r.PersonID == f.mia.ID && r.Part == task.Evening {
			return r.ID
		}
	}
	t.Fatal("Mia has no evening routine")
	return ""
}

func TestOnceTasksStayOutOfATemplate(t *testing.T) {
	f := newTaskFixture(t)
	r := f.miaEvening(t)
	if _, err := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: "Bibliotheksausweis", Repeat: task.Once, Date: "2026-11-18", Stars: 1, RoutineID: r.ID}); err != nil {
		t.Fatal(err)
	}
	tpl, err := f.db.SaveRoutineAsTemplate(f.h.ID, r.ID, "Ohne Einmaliges")
	if err != nil || !equal(titlesOf(tpl.Steps), []string{"Zimmer aufräumen", "Duschen", "Zähne putzen"}) {
		t.Fatalf("template = %+v, %v", tpl, err)
	}
	// A routine of nothing but once-only tasks, or of no task, cannot become a template.
	only := f.newRoutine(t, f.mia, "Nur einmalig", task.Morning)
	if _, err := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: "Arzt", Repeat: task.Once, Date: "2026-11-18", RoutineID: only.ID}); err != nil {
		t.Fatal(err)
	}
	empty := f.newRoutine(t, f.mia, "Leer", task.AfterSchool)
	for _, id := range []string{only.ID, empty.ID} {
		if _, err := f.db.SaveRoutineAsTemplate(f.h.ID, id, "x"); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("routine %s: err = %v, want ErrInvalid", id, err)
		}
	}
	if _, err := f.db.SaveRoutineAsTemplate(f.h.ID, "nope", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown routine: err = %v", err)
	}
}

func TestTemplateNamesAreUniqueAmongOwnTemplates(t *testing.T) {
	f := newTaskFixture(t)
	r := f.miaEvening(t)
	if _, err := f.db.SaveRoutineAsTemplate(f.h.ID, r.ID, "Abendroutine von Mia"); err != nil {
		t.Fatal(err)
	}
	_, err := f.db.SaveRoutineAsTemplate(f.h.ID, r.ID, "abendroutine VON mia")
	var exists *store.TemplateExistsError
	if !errors.Is(err, store.ErrConflict) || !errors.As(err, &exists) || exists.Name != "Abendroutine von Mia" {
		t.Fatalf("second save = %v, want ErrConflict naming the first", err)
	}
	if _, err := f.db.SaveRoutineAsTemplate(f.h.ID, r.ID, "  "); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("blank name: err = %v", err)
	}
}

func TestEditingAnOwnTemplate(t *testing.T) {
	f := newTaskFixture(t)
	tpl, _ := f.db.SaveRoutineAsTemplate(f.h.ID, f.miaEvening(t).ID, "Abend")
	other, _ := f.db.SaveRoutineAsTemplate(f.h.ID, f.miaEvening2Routine(t), "Anderer")

	if err := f.db.UpdateRoutineTemplate(f.h.ID, tpl.ID, " Abend neu ", task.AfterSchool); err != nil {
		t.Fatal(err)
	}
	if err := f.db.UpdateRoutineTemplate(f.h.ID, tpl.ID, "anderer", task.Morning); !errors.Is(err, store.ErrConflict) {
		t.Errorf("rename to a taken name: err = %v", err)
	}
	if err := f.db.UpdateRoutineTemplate(f.h.ID, tpl.ID, "", task.Morning); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("blank name: err = %v", err)
	}
	if got, _ := f.db.RoutineTemplate(f.h.ID, tpl.ID); got.Name != "Abend neu" || got.Part != task.AfterSchool {
		t.Fatalf("template = %+v", got)
	}

	added, err := f.db.AddTemplateStep(f.h.ID, tpl.ID, task.TemplateStep{Title: "Vorlesen", Emoji: "📖", Stars: 1, Repeat: task.Daily})
	if err != nil || added.ID == "" {
		t.Fatalf("AddTemplateStep = %+v, %v", added, err)
	}
	if _, err := f.db.AddTemplateStep(f.h.ID, tpl.ID, task.TemplateStep{Title: "x", Repeat: task.Once}); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("once-only step: err = %v", err)
	}
	got, _ := f.db.RoutineTemplate(f.h.ID, tpl.ID)
	if !equal(titlesOf(got.Steps), []string{"Zimmer aufräumen", "Duschen", "Zähne putzen", "Vorlesen"}) {
		t.Fatalf("steps = %v", titlesOf(got.Steps))
	}

	ids := []string{got.Steps[3].ID, got.Steps[0].ID, got.Steps[1].ID, got.Steps[2].ID}
	if err := f.db.SetTemplateOrder(f.h.ID, tpl.ID, ids); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetTemplateOrder(f.h.ID, tpl.ID, ids[:2]); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("partial order: err = %v", err)
	}
	got, _ = f.db.RoutineTemplate(f.h.ID, tpl.ID)
	if !equal(titlesOf(got.Steps), []string{"Vorlesen", "Zimmer aufräumen", "Duschen", "Zähne putzen"}) {
		t.Fatalf("ordered = %v", titlesOf(got.Steps))
	}

	if err := f.db.UpdateTemplateStep(f.h.ID, tpl.ID, task.TemplateStep{ID: got.Steps[0].ID, Title: " Lesen ", Emoji: "📚", Stars: 3, Repeat: task.Weekly, Weekdays: []string{"SA", "SU"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.UpdateTemplateStep(f.h.ID, tpl.ID, task.TemplateStep{ID: got.Steps[0].ID, Title: "", Repeat: task.Daily}); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("blank title: err = %v", err)
	}
	if err := f.db.UpdateTemplateStep(f.h.ID, tpl.ID, task.TemplateStep{ID: "nope", Title: "x", Repeat: task.Daily}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown step: err = %v", err)
	}
	// A step of another template is not found through this one.
	if err := f.db.DeleteTemplateStep(f.h.ID, tpl.ID, other.Steps[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("foreign step: err = %v", err)
	}
	if err := f.db.DeleteTemplateStep(f.h.ID, tpl.ID, got.Steps[0].ID); err != nil {
		t.Fatal(err)
	}
	got, _ = f.db.RoutineTemplate(f.h.ID, tpl.ID)
	if len(got.Steps) != 3 || got.Steps[0].Title != "Zimmer aufräumen" {
		t.Fatalf("after delete = %+v", got.Steps)
	}

	if err := f.db.DeleteRoutineTemplate(f.h.ID, tpl.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.RoutineTemplate(f.h.ID, tpl.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleted template: err = %v", err)
	}
	if err := f.db.DeleteRoutineTemplate(f.h.ID, tpl.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second delete: err = %v", err)
	}
}

// miaEvening2Routine makes a second routine of Mia's, for the second template.
func (f taskFixture) miaEvening2Routine(t *testing.T) string {
	t.Helper()
	r := f.newRoutine(t, f.mia, "Morgen", task.Morning)
	f.routineSteps(t, r, "Anziehen")
	return r.ID
}

func TestApplyingRefusesWhatARoutineRefusesAndCreatesNothing(t *testing.T) {
	f := newTaskFixture(t)
	f.newRoutine(t, f.mia, "Morgenroutine", task.Morning)
	steps := []task.TemplateStep{{Title: "Anziehen", Emoji: "👕", Stars: 1, Repeat: task.Daily}}
	_, err := f.db.CreateRoutineFromSteps(f.h.ID, task.Routine{PersonID: f.mia.ID, Name: "Noch eine", Part: task.Morning}, steps, "2026-11-16")
	var exists *store.RoutineExistsError
	if !errors.As(err, &exists) {
		t.Fatalf("err = %v, want RoutineExistsError", err)
	}
	if _, err := f.db.CreateRoutineFromSteps(f.h.ID, task.Routine{PersonID: "nobody", Name: "x", Part: task.Evening}, steps, "2026-11-16"); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("unknown person: err = %v", err)
	}
	if _, err := f.db.CreateRoutineFromSteps(f.h.ID, task.Routine{PersonID: f.mia.ID, Name: "x", Part: task.Evening}, []task.TemplateStep{{Title: "", Repeat: task.Daily}}, "2026-11-16"); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("bad step: err = %v", err)
	}
	if all, _ := f.db.Tasks(f.h.ID); len(all) != 0 {
		t.Fatalf("tasks = %+v, want none", all)
	}
	if list, _ := f.db.Routines(f.h.ID); len(list) != 1 {
		t.Fatalf("routines = %+v, want only the first", list)
	}
}
