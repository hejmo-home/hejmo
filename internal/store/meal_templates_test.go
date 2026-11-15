package store_test

import (
	"errors"
	"testing"

	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/store"
)

// weekB fills dinner Monday to Friday every 2 weeks from the week of 16/11/2026.
func weekB() meal.Template {
	return meal.Template{
		Name: "Week B", Mealtime: meal.Dinner, Interval: 2, Start: "2026-11-16",
		Entries: map[meal.Slot]string{
			meal.At(1, meal.Dinner): "Lentil soup", meal.At(2, meal.Dinner): "Fish fingers", meal.At(3, meal.Dinner): "Spaghetti bolognese",
			meal.At(4, meal.Dinner): "Vegetable curry", meal.At(5, meal.Dinner): "Homemade pizza",
		},
	}
}

// mealsOn returns the effective plan of a range as day|mealtime -> entry.
func planOf(t *testing.T, db *store.DB, h store.Household, from, to, today string) map[string]store.PlanMeal {
	t.Helper()
	list, err := db.MealPlan(h.ID, from, to, today)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]store.PlanMeal{}
	for _, m := range list {
		out[m.Day+"|"+string(m.Mealtime)] = m
	}
	return out
}

// eventTitlesByDay maps each day of the meal-plan calendar to the title of its event.
func eventTitlesByDay(t *testing.T, db *store.DB, h store.Household, cal store.Calendar) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, e := range mealEvents(t, db, h, cal) {
		out[e.Start.In(berlin).Format("2006-01-02")] = e.Title
	}
	return out
}

func optionsOn(today string) store.MealOptions {
	o := englishMeals()
	o.Today = today
	return o
}

func TestATemplateIsCreatedReadEditedAndDeleted(t *testing.T) {
	f := newCalFixture(t)
	o := optionsOn("2026-11-18")
	created, err := f.db.CreateMealTemplate(f.h.ID, weekB(), o)
	if err != nil || created.ID == "" {
		t.Fatalf("create = %+v, %v", created, err)
	}
	got, err := f.db.MealTemplate(f.h.ID, created.ID)
	if err != nil || got.Name != "Week B" || got.Interval != 2 || got.Start != "2026-11-16" || got.AllMealtimes || got.Mealtime != meal.Dinner ||
		got.Entries[meal.Slot{Weekday: 2, Mealtime: meal.Dinner}] != "Fish fingers" {
		t.Fatalf("read = %+v, %v", got, err)
	}
	got.Name, got.Interval = "Week B (school)", 3
	if _, err := f.db.UpdateMealTemplate(f.h.ID, got, o); err != nil {
		t.Fatal(err)
	}
	list, _ := f.db.MealTemplates(f.h.ID)
	if len(list) != 1 || list[0].Name != "Week B (school)" || list[0].Interval != 3 {
		t.Fatalf("list = %+v", list)
	}
	if err := f.db.DeleteMealTemplate(f.h.ID, created.ID, o); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.MealTemplate(f.h.ID, created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after delete = %v", err)
	}
	if _, err := f.db.UpdateMealTemplate(f.h.ID, got, o); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update after delete = %v", err)
	}
}

func TestAnInvalidTemplateIsRefused(t *testing.T) {
	f := newCalFixture(t)
	bad := weekB()
	bad.Interval = 0
	if _, err := f.db.CreateMealTemplate(f.h.ID, bad, optionsOn("2026-11-18")); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("interval 0 = %v", err)
	}
}

// Acceptance: the meal plan shows template entries for every week a template applies to.
func TestTheMealPlanShowsTemplateEntriesInEveryWeekTheTemplateAppliesTo(t *testing.T) {
	f := newCalFixture(t)
	tpl, _ := f.db.CreateMealTemplate(f.h.ID, weekB(), optionsOn("2026-11-18"))
	plan := planOf(t, f.db, f.h, "2026-11-09", "2026-12-06", "2026-11-18")
	for day, want := range map[string]string{"2026-11-18": "Spaghetti bolognese", "2026-11-20": "Homemade pizza", "2026-11-30": "Lentil soup", "2026-12-04": "Homemade pizza"} {
		m, ok := plan[day+"|dinner"]
		if !ok || m.Text != want || m.Source != store.SourceTemplate || m.TemplateID != tpl.ID || m.TemplateName != "Week B" {
			t.Errorf("%s = %+v %v, want %q from the template", day, m, ok, want)
		}
	}
	// Week 48 is the off week; the week before the start week has nothing either. Saturday has no entry.
	for _, day := range []string{"2026-11-23", "2026-11-24", "2026-11-09", "2026-11-21"} {
		if m, ok := plan[day+"|dinner"]; ok {
			t.Errorf("%s = %+v, want nothing", day, m)
		}
	}
	// The same days are events of the meal-plan calendar, for the days from today on.
	cal, err := f.db.MealCalendar(f.h.ID)
	if err != nil {
		t.Fatal(err)
	}
	titles := eventTitlesByDay(t, f.db, f.h, cal)
	if titles["2026-11-18"] != "Dinner: Spaghetti bolognese" || titles["2026-11-30"] != "Dinner: Lentil soup" || titles["2026-12-04"] != "Dinner: Homemade pizza" || titles["2026-11-24"] != "" || titles["2026-11-17"] != "" {
		t.Fatalf("events = %v", titles)
	}
	if titles["2027-11-05"] == "" {
		t.Error("the calendar reaches a year ahead")
	}
}

func TestATemplateReachesTodayAndLaterButNotDaysBeforeToday(t *testing.T) {
	f := newCalFixture(t)
	// Today is Wednesday 18/11: Monday and Tuesday of the week are over.
	if _, err := f.db.CreateMealTemplate(f.h.ID, weekB(), optionsOn("2026-11-18")); err != nil {
		t.Fatal(err)
	}
	plan := planOf(t, f.db, f.h, "2026-11-16", "2026-11-20", "2026-11-18")
	if _, ok := plan["2026-11-16|dinner"]; ok {
		t.Error("Monday 16/11 is over and must not be filled")
	}
	if _, ok := plan["2026-11-17|dinner"]; ok {
		t.Error("Tuesday 17/11 is over and must not be filled")
	}
	if m := plan["2026-11-18|dinner"]; m.Text != "Spaghetti bolognese" {
		t.Errorf("today = %+v", m)
	}
}

// Acceptance: overlapping templates are refused at save with a message naming the clash.
func TestOverlappingTemplatesAreRefusedAndNothingIsSaved(t *testing.T) {
	f := newCalFixture(t)
	o := optionsOn("2026-11-18")
	a := weekB()
	a.Name = "Week A"
	if _, err := f.db.CreateMealTemplate(f.h.ID, a, o); err != nil {
		t.Fatal(err)
	}
	// Alternating weeks are fine.
	b := weekB()
	b.Start = "2026-11-23"
	if _, err := f.db.CreateMealTemplate(f.h.ID, b, o); err != nil {
		t.Fatalf("alternating = %v", err)
	}
	// Every week from 16/11 meets both.
	c := weekB()
	c.Name, c.Interval = "Every week", 1
	_, err := f.db.CreateMealTemplate(f.h.ID, c, o)
	var clash *store.ClashError
	if !errors.Is(err, store.ErrConflict) || !errors.As(err, &clash) || len(clash.Clashes) != 2 {
		t.Fatalf("clash = %v", err)
	}
	first := clash.Clashes[0]
	if first.Template.Name != "Week A" || first.Clash.Mealtime != meal.Dinner || first.Clash.Week.Format("2006-01-02") != "2026-11-16" || len(first.Clash.Weekdays) != 3 {
		t.Fatalf("first clash = %+v", first) // Monday and Tuesday are over: Wednesday to Friday count
	}
	list, _ := f.db.MealTemplates(f.h.ID)
	if len(list) != 2 {
		t.Fatalf("templates after the refusal = %d", len(list))
	}
	// Editing a template does not clash with itself.
	a.ID = list[0].ID
	if _, err := f.db.UpdateMealTemplate(f.h.ID, a, o); err != nil {
		t.Fatalf("update itself = %v", err)
	}
}

// Acceptance: editing a week's entry overrides only that week; "Back to template" restores it.
func TestAWeeksEntryOverridesOnlyThatWeekAndBackToTemplateRestoresIt(t *testing.T) {
	f := newCalFixture(t)
	o := optionsOn("2026-11-18")
	tpl, _ := f.db.CreateMealTemplate(f.h.ID, weekB(), o)
	day := "2026-11-18"
	if err := f.db.SetMeals(f.h.ID, []store.Meal{{Day: day, Mealtime: meal.Dinner, Text: "Pizza at Grandma's"}}, o); err != nil {
		t.Fatal(err)
	}
	plan := planOf(t, f.db, f.h, "2026-11-16", "2026-12-06", "2026-11-18")
	m := plan[day+"|dinner"]
	if m.Text != "Pizza at Grandma's" || m.Source != store.SourceAdjusted || m.TemplateID != tpl.ID || m.TemplateDish != "Spaghetti bolognese" {
		t.Fatalf("adjusted = %+v", m)
	}
	if plan["2026-11-19|dinner"].Source != store.SourceTemplate || plan["2026-11-30|dinner"].Text != "Lentil soup" {
		t.Fatal("other days must stay with the template")
	}
	// The calendar shows the adjusted meal.
	cal, _ := f.db.MealCalendar(f.h.ID)
	if got := eventTitlesByDay(t, f.db, f.h, cal)[day]; got != "Dinner: Pizza at Grandma's" {
		t.Errorf("the calendar shows %q", got)
	}
	if err := f.db.RestoreMeal(f.h.ID, day, meal.Dinner, o); err != nil {
		t.Fatal(err)
	}
	m = planOf(t, f.db, f.h, day, day, "2026-11-18")[day+"|dinner"]
	if m.Text != "Spaghetti bolognese" || m.Source != store.SourceTemplate {
		t.Fatalf("restored = %+v", m)
	}
}

func TestADishTypedLikeTheTemplatesIsNoAdjustmentAndAnEmptyOneIs(t *testing.T) {
	f := newCalFixture(t)
	o := optionsOn("2026-11-18")
	_, _ = f.db.CreateMealTemplate(f.h.ID, weekB(), o)
	day := "2026-11-18"
	set := func(text string) store.PlanMeal {
		t.Helper()
		if err := f.db.SetMeals(f.h.ID, []store.Meal{{Day: day, Mealtime: meal.Dinner, Text: text}}, o); err != nil {
			t.Fatal(err)
		}
		return planOf(t, f.db, f.h, day, day, "2026-11-18")[day+"|dinner"]
	}
	if m := set("Spaghetti bolognese"); m.Source != store.SourceTemplate {
		t.Fatalf("same as template = %+v", m)
	}
	if m := set(""); m.Source != store.SourceAdjusted || m.Text != "" || m.TemplateDish != "Spaghetti bolognese" {
		t.Fatalf("emptied = %+v", m)
	}
	cal, _ := f.db.MealCalendar(f.h.ID)
	if got, ok := eventTitlesByDay(t, f.db, f.h, cal)[day]; ok {
		t.Fatalf("an emptied meal must leave the calendar, got %q", got)
	}
	// Typing the template's dish again drops the adjustment.
	if m := set("Spaghetti bolognese"); m.Source != store.SourceTemplate {
		t.Fatalf("typed again = %+v", m)
	}
}

// Acceptance: template edits reach coming, non-adjusted weeks only; past days unchanged.
func TestEditingATemplateKeepsAdjustmentsAndLeavesDaysBeforeTodayAlone(t *testing.T) {
	f := newCalFixture(t)
	o := optionsOn("2026-11-18")
	tpl, _ := f.db.CreateMealTemplate(f.h.ID, weekB(), o)
	_ = f.db.SetMeals(f.h.ID, []store.Meal{{Day: "2026-11-19", Mealtime: meal.Dinner, Text: "Leftovers"}}, o)

	// A day passes: on Friday 20/11 the template changes.
	later := optionsOn("2026-11-20")
	tpl.Entries[meal.Slot{Weekday: 3, Mealtime: meal.Dinner}] = "Lasagne"
	tpl.Entries[meal.Slot{Weekday: 4, Mealtime: meal.Dinner}] = "Chili"
	tpl.Entries[meal.Slot{Weekday: 5, Mealtime: meal.Dinner}] = "Burgers"
	if _, err := f.db.UpdateMealTemplate(f.h.ID, tpl, later); err != nil {
		t.Fatal(err)
	}
	plan := planOf(t, f.db, f.h, "2026-11-16", "2026-12-04", "2026-11-20")
	if m := plan["2026-11-18|dinner"]; m.Text != "Spaghetti bolognese" {
		t.Errorf("Wednesday (over) = %+v, want what was shown", m)
	}
	if m := plan["2026-11-19|dinner"]; m.Text != "Leftovers" || m.Source != store.SourceAdjusted || m.TemplateDish != "Vegetable curry" { // the dish the template had when the day was still to come
		t.Errorf("Thursday (adjusted, over) = %+v", m)
	}
	if m := plan["2026-11-20|dinner"]; m.Text != "Burgers" {
		t.Errorf("Friday (today) = %+v", m)
	}
	if m := plan["2026-12-02|dinner"]; m.Text != "Lasagne" {
		t.Errorf("a coming week = %+v", m)
	}

	// An adjustment in a coming week keeps its change when the template changes.
	_ = f.db.SetMeals(f.h.ID, []store.Meal{{Day: "2026-12-02", Mealtime: meal.Dinner, Text: "Takeaway"}}, later)
	tpl.Entries[meal.Slot{Weekday: 3, Mealtime: meal.Dinner}] = "Gnocchi"
	_, _ = f.db.UpdateMealTemplate(f.h.ID, tpl, later)
	m := planOf(t, f.db, f.h, "2026-12-02", "2026-12-02", "2026-11-20")["2026-12-02|dinner"]
	if m.Text != "Takeaway" || m.TemplateDish != "Gnocchi" {
		t.Fatalf("adjusted week after the template changed = %+v", m)
	}
}

func TestDeletingATemplateTurnsAdjustmentsIntoPlainEntriesAndKeepsDaysBeforeToday(t *testing.T) {
	f := newCalFixture(t)
	o := optionsOn("2026-11-18")
	tpl, _ := f.db.CreateMealTemplate(f.h.ID, weekB(), o)
	_ = f.db.SetMeals(f.h.ID, []store.Meal{{Day: "2026-11-19", Mealtime: meal.Dinner, Text: "Leftovers"}, {Day: "2026-11-20", Mealtime: meal.Dinner, Text: ""}}, o)
	later := optionsOn("2026-11-19")
	if err := f.db.DeleteMealTemplate(f.h.ID, tpl.ID, later); err != nil {
		t.Fatal(err)
	}
	plan := planOf(t, f.db, f.h, "2026-11-16", "2026-12-04", "2026-11-19")
	if m := plan["2026-11-18|dinner"]; m.Text != "Spaghetti bolognese" || m.Source != store.SourceManual || m.TemplateID != "" {
		t.Errorf("a day before today = %+v, want it to stay as a plain entry", m)
	}
	if m := plan["2026-11-19|dinner"]; m.Text != "Leftovers" || m.Source != store.SourceManual {
		t.Errorf("adjusted day = %+v, want a plain entry", m)
	}
	if m, ok := plan["2026-11-20|dinner"]; ok && m.Text != "" {
		t.Errorf("an emptied day = %+v, want nothing", m)
	}
	if m, ok := plan["2026-11-30|dinner"]; ok && m.Text != "" {
		t.Errorf("a coming week = %+v, want nothing", m)
	}
	cal, _ := f.db.MealCalendar(f.h.ID)
	titles := eventTitlesByDay(t, f.db, f.h, cal)
	if titles["2026-11-30"] != "" || titles["2026-11-18"] != "Dinner: Spaghetti bolognese" || titles["2026-11-19"] != "Dinner: Leftovers" {
		t.Fatalf("events = %v", titles)
	}
}

func TestAHandTypedMealComesBackWhenNoTemplateFillsItAnymore(t *testing.T) {
	f := newCalFixture(t)
	o := optionsOn("2026-11-18")
	_ = f.db.SetMeals(f.h.ID, []store.Meal{{Day: "2026-11-30", Mealtime: meal.Dinner, Text: "Hand typed"}}, o)
	tpl, _ := f.db.CreateMealTemplate(f.h.ID, weekB(), o)
	if m := planOf(t, f.db, f.h, "2026-11-30", "2026-11-30", "2026-11-18")["2026-11-30|dinner"]; m.Text != "Lentil soup" {
		t.Fatalf("with template = %+v", m)
	}
	_ = f.db.DeleteMealTemplate(f.h.ID, tpl.ID, o)
	if m := planOf(t, f.db, f.h, "2026-11-30", "2026-11-30", "2026-11-18")["2026-11-30|dinner"]; m.Text != "Hand typed" || m.Source != store.SourceManual {
		t.Fatalf("after delete = %+v", m)
	}
}

func TestEditingATemplateThatStopsFillingACellMovesItsAdjustmentToAPlainEntry(t *testing.T) {
	f := newCalFixture(t)
	o := optionsOn("2026-11-18")
	tpl, _ := f.db.CreateMealTemplate(f.h.ID, weekB(), o)
	_ = f.db.SetMeals(f.h.ID, []store.Meal{
		{Day: "2026-11-30", Mealtime: meal.Dinner, Text: "Pizza"}, // adjusted: Monday of a week the template fills
		{Day: "2026-12-01", Mealtime: meal.Dinner, Text: ""},      // adjusted to nothing
	}, o)
	delete(tpl.Entries, meal.Slot{Weekday: 1, Mealtime: meal.Dinner})
	delete(tpl.Entries, meal.Slot{Weekday: 2, Mealtime: meal.Dinner})
	if _, err := f.db.UpdateMealTemplate(f.h.ID, tpl, o); err != nil {
		t.Fatal(err)
	}
	plan := planOf(t, f.db, f.h, "2026-11-30", "2026-12-01", "2026-11-18")
	if m := plan["2026-11-30|dinner"]; m.Text != "Pizza" || m.Source != store.SourceManual {
		t.Fatalf("after the edit = %+v, want a plain entry", m)
	}
	if m, ok := plan["2026-12-01|dinner"]; ok && m.Text != "" {
		t.Fatalf("emptied day after the edit = %+v, want nothing", m)
	}

	// A later template that fills the cell must not show the old text as an adjustment.
	other := meal.Template{Name: "C", Mealtime: meal.Dinner, Interval: 1, Start: "2026-11-16",
		Entries: map[meal.Slot]string{meal.At(1, meal.Dinner): "Rice", meal.At(2, meal.Dinner): "Soup"}}
	if _, err := f.db.CreateMealTemplate(f.h.ID, other, o); err != nil {
		t.Fatal(err)
	}
	plan = planOf(t, f.db, f.h, "2026-11-30", "2026-12-01", "2026-11-18")
	if m := plan["2026-11-30|dinner"]; m.Text != "Rice" || m.Source != store.SourceTemplate || m.TemplateName != "C" {
		t.Errorf("with the later template = %+v, want its own dish", m)
	}
	if m := plan["2026-12-01|dinner"]; m.Text != "Soup" || m.Source != store.SourceTemplate {
		t.Errorf("emptied day with the later template = %+v, want its own dish", m)
	}
}

func TestRefreshingTheMealPlanAsDaysGoByReachesTheNewDayAndLeavesThePastAlone(t *testing.T) {
	f := newCalFixture(t)
	o := optionsOn("2026-11-18")
	daily := meal.Template{Name: "Every day", Mealtime: meal.Dinner, Interval: 1, Start: "2026-11-16", Entries: map[meal.Slot]string{}}
	for wd := 0; wd < 7; wd++ {
		daily.Entries[meal.At(wd, meal.Dinner)] = "Soup"
	}
	if _, err := f.db.CreateMealTemplate(f.h.ID, daily, o); err != nil {
		t.Fatal(err)
	}
	cal, _ := f.db.MealCalendar(f.h.ID)
	before := eventTitlesByDay(t, f.db, f.h, cal)
	last := "2027-11-24" // 18/11/2026 plus 371 days is out of reach; the day before is the last one
	if before["2026-11-18"] == "" || before[last] != "" {
		t.Fatalf("before the roll: today %q, %s %q", before["2026-11-18"], last, before[last])
	}
	if err := f.db.RefreshMealPlan(f.h.ID, optionsOn("2026-11-19")); err != nil {
		t.Fatal(err)
	}
	after := eventTitlesByDay(t, f.db, f.h, cal)
	if after[last] != "Dinner: Soup" {
		t.Errorf("newly reachable day = %q, want its meal", after[last])
	}
	if after["2026-11-18"] != before["2026-11-18"] {
		t.Errorf("a day before today = %q, want it left alone (%q)", after["2026-11-18"], before["2026-11-18"])
	}
}
