package store_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/store"
)

func englishMeals() store.MealOptions {
	labels := map[meal.Mealtime]string{meal.Breakfast: "Breakfast", meal.Lunch: "Lunch", meal.Snack: "Snack", meal.Dinner: "Dinner"}
	return store.MealOptions{
		CalendarName: "Meal plan", Location: berlin,
		Title: func(m meal.Mealtime, text string) string { return meal.EventTitle(labels[m], text) },
	}
}

func mealEvents(t *testing.T, db *store.DB, h store.Household, cal store.Calendar) []calendar.Event {
	t.Helper()
	evs, err := db.CalendarEvents(h.ID, cal.ID)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

// ageEvents sets the events' updated time far back, so that a later write shows
// as a change however fast the test runs, and returns the first event so aged.
func ageEvents(t *testing.T, db *store.DB, h store.Household, cal store.Calendar) calendar.Event {
	t.Helper()
	if _, err := db.SQL().Exec(`UPDATE events SET updated_at = '2000-01-01T00:00:00Z' WHERE calendar_id = ?`, cal.ID); err != nil {
		t.Fatal(err)
	}
	return mealEvents(t, db, h, cal)[0]
}

func saveCutoffs(db *store.DB, id string, c meal.Cutoffs) error {
	in := store.HouseholdSettings{TimeFormat: store.Time24h, TemperatureUnit: store.Celsius, AutoUpdate: store.UpdateOn, MealCutoffs: c}
	return db.SaveHouseholdSettings(id, in)
}

func TestHouseholdMealCutoffsDefaultToADR0016AndPersist(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	got, _ := db.Household(h.ID)
	if got.MealCutoffs != meal.DefaultCutoffs() {
		t.Fatalf("defaults = %+v", got.MealCutoffs)
	}
	want := meal.Cutoffs{Breakfast: "09:30", Lunch: "", Snack: "17:00", Dinner: "21:00"}
	if err := saveCutoffs(db, h.ID, want); err != nil {
		t.Fatal(err)
	}
	got, _ = db.Household(h.ID)
	if got.MealCutoffs != want {
		t.Fatalf("saved = %+v", got.MealCutoffs)
	}
	if err := saveCutoffs(db, h.ID, meal.Cutoffs{Lunch: "25:00"}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("bad time = %v, want ErrInvalid", err)
	}
	if got, _ = db.Household(h.ID); got.MealCutoffs != want {
		t.Fatalf("a refused save changed the cut-offs: %+v", got.MealCutoffs)
	}
	if err := saveCutoffs(db, "missing", want); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown household = %v", err)
	}
}

// Acceptance: given a shared meal plan, when a dinner is entered for Tuesday,
// then it appears in the calendar on Tuesday.
func TestADinnerAppearsInTheGeneratedCalendarOnItsDay(t *testing.T) {
	f := newCalFixture(t)
	if _, err := f.db.MealCalendar(f.h.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("calendar before any meal = %v, want not found", err)
	}
	err := f.db.SetMeals(f.h.ID, []store.Meal{{Day: "2026-11-17", Mealtime: meal.Dinner, Text: "Pasta bake"}}, englishMeals())
	if err != nil {
		t.Fatal(err)
	}
	cal, err := f.db.MealCalendar(f.h.ID)
	if err != nil || cal.Kind != "meals" || cal.Name != "Meal plan" || !cal.ReadOnly || !cal.Shared || cal.Family {
		t.Fatalf("calendar = %+v, %v", cal, err)
	}
	if len(cal.Audience.People)+len(cal.Audience.Groups) != 0 {
		t.Fatalf("audience = %+v, want the whole household", cal.Audience)
	}
	evs := mealEvents(t, f.db, f.h, cal)
	if len(evs) != 1 {
		t.Fatalf("events = %+v", evs)
	}
	e := evs[0]
	if e.Title != "Dinner: Pasta bake" || !e.AllDay || e.RRule != "" || len(e.People)+len(e.Groups) != 0 ||
		!e.Start.Equal(at(2026, 11, 17, 0, 0)) || !e.End.Equal(at(2026, 11, 18, 0, 0)) {
		t.Fatalf("event = %+v", e)
	}
	// It is part of the household's events for Tuesday, like any other calendar.
	between, err := f.db.EventsBetween(f.h.ID, at(2026, 11, 17, 0, 0), at(2026, 11, 18, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	occ, _ := calendar.Expand(between, at(2026, 11, 17, 0, 0), at(2026, 11, 18, 0, 0))
	if len(occ) != 1 || occ[0].Title != "Dinner: Pasta bake" || occ[0].CalendarID != cal.ID {
		t.Fatalf("occurrences = %+v", occ)
	}
}

// Acceptance: given a meal changed, then the calendar shows the new meal.
func TestChangingAMealChangesItsEventInPlace(t *testing.T) {
	f := newCalFixture(t)
	in := store.Meal{Day: "2026-11-17", Mealtime: meal.Dinner, Text: "Pasta bake"}
	_ = f.db.SetMeals(f.h.ID, []store.Meal{in}, englishMeals())
	cal, _ := f.db.MealCalendar(f.h.ID)
	first := ageEvents(t, f.db, f.h, cal)

	in.Text = "Soup"
	if err := f.db.SetMeals(f.h.ID, []store.Meal{in}, englishMeals()); err != nil {
		t.Fatal(err)
	}
	evs := mealEvents(t, f.db, f.h, cal)
	if len(evs) != 1 || evs[0].Title != "Dinner: Soup" || evs[0].ID != first.ID {
		t.Fatalf("events = %+v, want the same event retitled", evs)
	}
	if !evs[0].UpdatedAt.After(first.UpdatedAt) {
		t.Error("a changed meal must change the event's updated time, so CalDAV clients fetch it again")
	}
	got, _ := f.db.Meals(f.h.ID, "2026-11-17", "2026-11-17")
	if len(got) != 1 || got[0].Text != "Soup" {
		t.Fatalf("meals = %+v", got)
	}
}

func TestSavingTheSameMealAgainLeavesItsEventAlone(t *testing.T) {
	f := newCalFixture(t)
	in := []store.Meal{{Day: "2026-11-17", Mealtime: meal.Dinner, Text: "Pasta bake"}}
	_ = f.db.SetMeals(f.h.ID, in, englishMeals())
	cal, _ := f.db.MealCalendar(f.h.ID)
	first := ageEvents(t, f.db, f.h, cal)
	_ = f.db.SetMeals(f.h.ID, in, englishMeals())
	if again := mealEvents(t, f.db, f.h, cal)[0]; !again.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatal("an unchanged meal touched its event, which would make every client sync it again")
	}
}

func TestEmptyTextRemovesTheMealAndItsEvent(t *testing.T) {
	f := newCalFixture(t)
	_ = f.db.SetMeals(f.h.ID, []store.Meal{
		{Day: "2026-11-17", Mealtime: meal.Lunch, Text: "Soup"}, {Day: "2026-11-17", Mealtime: meal.Dinner, Text: "Pasta bake"}}, englishMeals())
	if err := f.db.SetMeals(f.h.ID, []store.Meal{{Day: "2026-11-17", Mealtime: meal.Dinner, Text: ""}}, englishMeals()); err != nil {
		t.Fatal(err)
	}
	cal, _ := f.db.MealCalendar(f.h.ID)
	if evs := mealEvents(t, f.db, f.h, cal); len(evs) != 1 || evs[0].Title != "Lunch: Soup" {
		t.Fatalf("events = %+v", evs)
	}
	if got, _ := f.db.Meals(f.h.ID, "2026-11-01", "2026-11-30"); len(got) != 1 || got[0].Mealtime != meal.Lunch {
		t.Fatalf("meals = %+v", got)
	}
	// Removing what is not there is not an error.
	if err := f.db.SetMeals(f.h.ID, []store.Meal{{Day: "2026-11-18", Mealtime: meal.Dinner}}, englishMeals()); err != nil {
		t.Fatal(err)
	}
}

func TestMealsAreListedByDayAndMealtimeWithinTheRange(t *testing.T) {
	f := newCalFixture(t)
	_ = f.db.SetMeals(f.h.ID, []store.Meal{
		{Day: "2026-11-18", Mealtime: meal.Dinner, Text: "d3"}, {Day: "2026-11-17", Mealtime: meal.Dinner, Text: "d2"},
		{Day: "2026-11-17", Mealtime: meal.Breakfast, Text: "b2"}, {Day: "2026-11-17", Mealtime: meal.Snack, Text: "s2"},
		{Day: "2026-11-16", Mealtime: meal.Dinner, Text: "d1"}, {Day: "2026-11-30", Mealtime: meal.Dinner, Text: "later"}}, englishMeals())
	got, err := f.db.Meals(f.h.ID, "2026-11-17", "2026-11-18")
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, m := range got {
		texts = append(texts, m.Text)
	}
	want := []string{"b2", "s2", "d2", "d3"}
	if len(texts) != len(want) {
		t.Fatalf("meals = %v, want %v", texts, want)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Fatalf("meals = %v, want %v", texts, want)
		}
	}
}

func TestMealInputIsValidatedAsAWhole(t *testing.T) {
	f := newCalFixture(t)
	for name, in := range map[string]store.Meal{
		"unknown mealtime": {Day: "2026-11-17", Mealtime: "brunch", Text: "x"},
		"bad day":          {Day: "17/11/2026", Mealtime: meal.Dinner, Text: "x"},
		"long text":        {Day: "2026-11-17", Mealtime: meal.Dinner, Text: strings.Repeat("a", 201)},
	} {
		good := store.Meal{Day: "2026-11-16", Mealtime: meal.Lunch, Text: "kept out"}
		err := f.db.SetMeals(f.h.ID, []store.Meal{good, in}, englishMeals())
		if !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if got, _ := f.db.Meals(f.h.ID, "2026-01-01", "2026-12-31"); len(got) != 0 {
		t.Fatalf("a refused change stored %+v: a save is all or nothing", got)
	}
}

func TestLaterEntryForTheSameSlotWins(t *testing.T) {
	f := newCalFixture(t)
	err := f.db.SetMeals(f.h.ID, []store.Meal{
		{Day: "2026-11-17", Mealtime: meal.Dinner, Text: "first"}, {Day: "2026-11-17", Mealtime: meal.Dinner, Text: "second"}}, englishMeals())
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.Meals(f.h.ID, "2026-11-17", "2026-11-17"); len(got) != 1 || got[0].Text != "second" {
		t.Fatalf("meals = %+v", got)
	}
}

func TestMealPlanBelongsToOneHousehold(t *testing.T) {
	f := newCalFixture(t)
	other, _ := f.db.CreateHousehold(store.Household{Name: "Other"})
	_ = f.db.SetMeals(f.h.ID, []store.Meal{{Day: "2026-11-17", Mealtime: meal.Dinner, Text: "mine"}}, englishMeals())
	if got, _ := f.db.Meals(other.ID, "2026-11-01", "2026-11-30"); len(got) != 0 {
		t.Fatalf("other household sees %+v", got)
	}
}
