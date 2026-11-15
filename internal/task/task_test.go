package task_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/task"
)

func TestOnceTaskIsDueOnlyOnItsDate(t *testing.T) {
	k := task.Task{Title: "Dentist forms", Repeat: task.Once, Date: "2026-11-16"}
	if !k.DueOn("2026-11-16") || k.DueOn("2026-11-15") || k.DueOn("2026-11-17") {
		t.Fatal("once task must be due exactly on its date")
	}
}

func TestDailyTaskIsDueEveryDayFromItsStart(t *testing.T) {
	k := task.Task{Title: "Brush teeth", Repeat: task.Daily, Date: "2026-11-14"}
	if k.DueOn("2026-11-13") || !k.DueOn("2026-11-14") || !k.DueOn("2026-12-31") {
		t.Fatal("daily task is due every day from its start date")
	}
}

func TestWeeklyTaskIsDueOnChosenWeekdays(t *testing.T) {
	k := task.Task{Title: "Piano", Repeat: task.Weekly, Weekdays: []string{"MO", "WE"}, Date: "2026-11-01"}
	// 16/11/2026 is a Monday, 17/11 a Tuesday, 18/11 a Wednesday.
	if !k.DueOn("2026-11-16") || k.DueOn("2026-11-17") || !k.DueOn("2026-11-18") {
		t.Fatal("weekly task is due on its weekdays only")
	}
	if k.DueOn("2026-10-26") {
		t.Fatal("not due before its start date")
	}
}

func TestRRuleRoundTrip(t *testing.T) {
	for _, k := range []task.Task{
		{Repeat: task.Once},
		{Repeat: task.Daily},
		{Repeat: task.Weekly, Weekdays: []string{"WE", "MO"}},
	} {
		want := map[task.Repeat]string{task.Once: "", task.Daily: "FREQ=DAILY", task.Weekly: "FREQ=WEEKLY;BYDAY=MO,WE"}[k.Repeat]
		if got := k.RRule(); got != want {
			t.Errorf("RRule(%v) = %q, want %q", k, got, want)
		}
		back, err := task.ParseRRule(want)
		if err != nil || back.Repeat != k.Repeat {
			t.Errorf("ParseRRule(%q) = %+v, %v", want, back, err)
		}
	}
	if _, err := task.ParseRRule("FREQ=MONTHLY"); err == nil {
		t.Error("monthly must be refused")
	}
}

func TestValidate(t *testing.T) {
	bad := []task.Task{
		{Title: " ", PersonID: "p", Repeat: task.Daily, Date: "2026-11-14"},
		{Title: "x", Repeat: task.Daily, Date: "2026-11-14"},
		{Title: "x", PersonID: "p", Repeat: task.Once},
		{Title: "x", PersonID: "p", Repeat: task.Once, Date: "14/11/2026"},
		{Title: "x", PersonID: "p", Repeat: task.Weekly, Date: "2026-11-14"},
		{Title: "x", PersonID: "p", Repeat: task.Weekly, Weekdays: []string{"XX"}, Date: "2026-11-14"},
		{Title: "x", PersonID: "p", Repeat: "monthly", Date: "2026-11-14"},
		{Title: "x", PersonID: "p", Repeat: task.Daily, Date: "2026-11-14", PartOfDay: "noon"},
		{Title: strings.Repeat("x", 121), PersonID: "p", Repeat: task.Daily, Date: "2026-11-14"},
		{Title: "x", Emoji: strings.Repeat("🪥", 17), PersonID: "p", Repeat: task.Daily, Date: "2026-11-14"},
		{Title: "x", PersonID: "p", Repeat: task.Weekly, Weekdays: []string{"MO", "MO"}, Date: "2026-11-14"},
	}
	for _, k := range bad {
		if err := k.Validate(); !errors.Is(err, task.ErrInvalid) {
			t.Errorf("Validate(%+v) = %v, want ErrInvalid", k, err)
		}
	}
	limits := task.Task{Title: strings.Repeat("ä", 120), Emoji: strings.Repeat("🪥", 16), PersonID: "p", Repeat: task.Daily, Date: "2026-11-14"}
	if err := limits.Validate(); err != nil {
		t.Errorf("title of 120 and emoji of 16 characters: %v", err)
	}
	ok := task.Task{Title: "x", PersonID: "p", Repeat: task.Weekly, Weekdays: []string{"SA"}, Date: "2026-11-14", PartOfDay: task.Morning}
	if err := ok.Validate(); err != nil {
		t.Errorf("valid task: %v", err)
	}
}

// ADR 0018: a new task's default star value is 1 for a child and 0 for an adult.
func TestDefaultStarsIsOneForAChildAndZeroForAnAdult(t *testing.T) {
	if task.DefaultStars(true) != 1 || task.DefaultStars(false) != 0 {
		t.Fatalf("defaults = %d / %d, want 1 / 0", task.DefaultStars(true), task.DefaultStars(false))
	}
}

func TestStarValueIsWithinLimits(t *testing.T) {
	base := task.Task{PersonID: "p", Title: "x", Repeat: task.Daily, Date: "2026-11-14"}
	for _, ok := range []int{0, 1, 3, task.MaxStars} {
		k := base
		k.Stars = ok
		if err := k.Validate(); err != nil {
			t.Errorf("stars %d refused: %v", ok, err)
		}
	}
	for _, bad := range []int{-1, task.MaxStars + 1} {
		k := base
		k.Stars = bad
		if err := k.Validate(); !errors.Is(err, task.ErrInvalid) {
			t.Errorf("stars %d = %v, want ErrInvalid", bad, err)
		}
	}
}

// A parent's manual booking needs a non-zero amount and a short note.
func TestManualBookingNeedsAmountAndNote(t *testing.T) {
	if err := task.ValidateBooking(-5, "redeemed: ice cream"); err != nil {
		t.Fatalf("valid booking refused: %v", err)
	}
	for _, c := range []struct {
		delta int
		note  string
	}{{0, "x"}, {5, " "}, {5, ""}, {task.MaxBooking + 1, "x"}, {-task.MaxBooking - 1, "x"}, {1, strings.Repeat("a", task.MaxNote+1)}} {
		if err := task.ValidateBooking(c.delta, c.note); !errors.Is(err, task.ErrInvalid) {
			t.Errorf("ValidateBooking(%d, %q) = %v, want ErrInvalid", c.delta, c.note, err)
		}
	}
}

// Every validation failure names the field the form must mark.
func TestValidateNamesTheWrongField(t *testing.T) {
	base := task.Task{Title: "x", PersonID: "p", Repeat: task.Daily, Date: "2026-11-14"}
	with := func(f func(*task.Task)) task.Task { k := base; f(&k); return k }
	for _, c := range []struct {
		name  string
		task  task.Task
		field string
	}{
		{"empty title", with(func(k *task.Task) { k.Title = " " }), "title"},
		{"long title", with(func(k *task.Task) { k.Title = strings.Repeat("x", 121) }), "title"},
		{"long emoji", with(func(k *task.Task) { k.Emoji = strings.Repeat("🪥", 17) }), "emoji"},
		{"no person", with(func(k *task.Task) { k.PersonID = "" }), "person"},
		{"bad date", with(func(k *task.Task) { k.Date = "14/11/2026" }), "date"},
		{"weekdays on daily", with(func(k *task.Task) { k.Weekdays = []string{"MO"} }), "weekdays"},
		{"weekly without weekdays", with(func(k *task.Task) { k.Repeat = task.Weekly }), "weekdays"},
		{"unknown weekday", with(func(k *task.Task) { k.Repeat, k.Weekdays = task.Weekly, []string{"XX"} }), "weekdays"},
		{"double weekday", with(func(k *task.Task) { k.Repeat, k.Weekdays = task.Weekly, []string{"MO", "MO"} }), "weekdays"},
		{"unknown repeat", with(func(k *task.Task) { k.Repeat = "monthly" }), "repeat"},
		{"unknown part", with(func(k *task.Task) { k.PartOfDay = "noon" }), "part"},
	} {
		var ie *task.InvalidError
		err := c.task.Validate()
		if !errors.As(err, &ie) || ie.Field != c.field || !errors.Is(err, task.ErrInvalid) {
			t.Errorf("%s: Validate = %v, want InvalidError for %q", c.name, err, c.field)
		}
	}
}
