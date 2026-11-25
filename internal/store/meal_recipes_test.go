package store_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 281: a meal plan entry is free text or a recipe with portions.

type recipeMealFixture struct {
	calFixture
	curry store.Recipe
}

func newRecipeMealFixture(t *testing.T) recipeMealFixture {
	t.Helper()
	f := newCalFixture(t)
	r, err := f.db.CreateRecipe(f.h.ID, "", recipe.Recipe{Name: "Erdnuss-Curry", Category: recipe.Main, Portions: 4, Minutes: 45})
	if err != nil {
		t.Fatal(err)
	}
	return recipeMealFixture{f, r}
}

func TestARecipeEntryHoldsItsPortionsAndShowsTheRecipesName(t *testing.T) {
	f := newRecipeMealFixture(t)
	o := optionsOn("2026-11-14")
	if err := f.db.SetMeals(f.h.ID, []store.Meal{{Day: "2026-11-18", Mealtime: meal.Dinner, RecipeID: f.curry.ID, Portions: 6}, {Day: "2026-11-18", Mealtime: meal.Lunch, Text: "Soup"}}, o); err != nil {
		t.Fatal(err)
	}
	got := planOf(t, f.db, f.h, "2026-11-18", "2026-11-18", "2026-11-14")
	dinner, lunch := got["2026-11-18|dinner"], got["2026-11-18|lunch"]
	if dinner.RecipeID != f.curry.ID || dinner.Portions != 6 || dinner.Text != "Erdnuss-Curry" || dinner.Source != store.SourceManual {
		t.Errorf("recipe entry = %+v", dinner)
	}
	if lunch.RecipeID != "" || lunch.Portions != 0 || lunch.Text != "Soup" {
		t.Errorf("free text entry = %+v: a text entry has no recipe and no portions", lunch)
	}
	cal, _ := f.db.MealCalendar(f.h.ID)
	var titles []string
	for _, e := range mealEvents(t, f.db, f.h, cal) {
		titles = append(titles, e.Title)
	}
	slices.Sort(titles)
	if !reflect.DeepEqual(titles, []string{"Dinner: Erdnuss-Curry", "Lunch: Soup"}) {
		t.Errorf("event titles = %v", titles)
	}
	plain, err := f.db.Meals(f.h.ID, "2026-11-18", "2026-11-18")
	if err != nil || len(plain) != 2 || plain[1].Text != "Erdnuss-Curry" || plain[1].RecipeID != f.curry.ID || plain[1].Portions != 6 {
		t.Errorf("hand-typed entries = %+v, %v", plain, err)
	}
}

func TestTheCalendarEventOfARecipeEntryNamesOnlyTheDish(t *testing.T) {
	f := newRecipeMealFixture(t)
	if err := f.db.SetMeals(f.h.ID, []store.Meal{{Day: "2026-11-18", Mealtime: meal.Dinner, RecipeID: f.curry.ID, Portions: 6}}, optionsOn("2026-11-14")); err != nil {
		t.Fatal(err)
	}
	cal, _ := f.db.MealCalendar(f.h.ID)
	evs := mealEvents(t, f.db, f.h, cal)
	if len(evs) != 1 || evs[0].Title != "Dinner: Erdnuss-Curry" || evs[0].Description != "" || evs[0].Location != "" {
		t.Fatalf("events = %+v: only the mealtime and the name, no portions or warnings", evs)
	}
}

func TestChangingPortionsKeepsTheEventAndChangingTheRecipeChangesIt(t *testing.T) {
	f := newRecipeMealFixture(t)
	o := optionsOn("2026-11-14")
	set := func(m store.Meal) {
		t.Helper()
		m.Day, m.Mealtime = "2026-11-18", meal.Dinner
		if err := f.db.SetMeals(f.h.ID, []store.Meal{m}, o); err != nil {
			t.Fatal(err)
		}
	}
	set(store.Meal{RecipeID: f.curry.ID, Portions: 4})
	cal, _ := f.db.MealCalendar(f.h.ID)
	before := ageEvents(t, f.db, f.h, cal)
	set(store.Meal{RecipeID: f.curry.ID, Portions: 8})
	if after := mealEvents(t, f.db, f.h, cal)[0]; !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Error("other portions touched the event: CalDAV clients would fetch it again")
	}
	if got := planOf(t, f.db, f.h, "2026-11-18", "2026-11-18", "2026-11-14")["2026-11-18|dinner"]; got.Portions != 8 {
		t.Errorf("portions = %d, want 8", got.Portions)
	}
	other, err := f.db.CreateRecipe(f.h.ID, "", recipe.Recipe{Name: "Linsen-Bolognese", Category: recipe.Main, Portions: 4})
	if err != nil {
		t.Fatal(err)
	}
	set(store.Meal{RecipeID: other.ID, Portions: 8})
	if evs := mealEvents(t, f.db, f.h, cal); evs[0].Title != "Dinner: Linsen-Bolognese" {
		t.Errorf("title = %q", evs[0].Title)
	}
	set(store.Meal{Text: "Pizza"}) // text again: no recipe, no portions
	got := planOf(t, f.db, f.h, "2026-11-18", "2026-11-18", "2026-11-14")["2026-11-18|dinner"]
	if got.RecipeID != "" || got.Portions != 0 || got.Text != "Pizza" {
		t.Errorf("back to text = %+v", got)
	}
}

func TestARecipeEntryNeedsARecipeOfTheHouseholdAndPortionsFrom1To99(t *testing.T) {
	f := newRecipeMealFixture(t)
	o := optionsOn("2026-11-14")
	h2, err := f.db.CreateHousehold(store.Household{Name: "Neighbours"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := f.db.CreateRecipe(h2.ID, "", recipe.Recipe{Name: "Erdnuss-Curry", Category: recipe.Main, Portions: 4})
	if err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]store.Meal{
		"unknown recipe": {RecipeID: "nope", Portions: 4},
		"another's":      {RecipeID: foreign.ID, Portions: 4},
		"no portions":    {RecipeID: f.curry.ID},
		"zero portions":  {RecipeID: f.curry.ID, Portions: 0},
		"too many":       {RecipeID: f.curry.ID, Portions: 100},
		"negative":       {RecipeID: f.curry.ID, Portions: -2},
	} {
		m.Day, m.Mealtime = "2026-11-18", meal.Dinner
		if err := f.db.SetMeals(f.h.ID, []store.Meal{m}, o); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
	if n, _ := f.db.CountMeals(f.h.ID); n != 0 {
		t.Errorf("%d entries after refused saves", n)
	}
	for _, p := range []int{1, 99} {
		if err := f.db.SetMeals(f.h.ID, []store.Meal{{Day: "2026-11-18", Mealtime: meal.Dinner, RecipeID: f.curry.ID, Portions: p}}, o); err != nil {
			t.Errorf("portions %d: %v", p, err)
		}
	}
}

func TestARecipeCanBeAnAdjustmentOfATemplateAndGoesBackToTheTemplate(t *testing.T) {
	f := newRecipeMealFixture(t)
	o := optionsOn("2026-11-18")
	if _, err := f.db.CreateMealTemplate(f.h.ID, weekB(), o); err != nil {
		t.Fatal(err)
	}
	// Tuesday 17.11. is "Fish fingers"; today is 18.11., so use Thursday 19.11. ("Vegetable curry").
	if err := f.db.SetMeals(f.h.ID, []store.Meal{{Day: "2026-11-19", Mealtime: meal.Dinner, RecipeID: f.curry.ID, Portions: 5}}, o); err != nil {
		t.Fatal(err)
	}
	got := planOf(t, f.db, f.h, "2026-11-19", "2026-11-19", "2026-11-18")["2026-11-19|dinner"]
	if got.Source != store.SourceAdjusted || got.RecipeID != f.curry.ID || got.Portions != 5 || got.Text != "Erdnuss-Curry" || got.TemplateDish != "Vegetable curry" {
		t.Errorf("adjusted = %+v", got)
	}
	if err := f.db.RestoreMeal(f.h.ID, "2026-11-19", meal.Dinner, o); err != nil {
		t.Fatal(err)
	}
	got = planOf(t, f.db, f.h, "2026-11-19", "2026-11-19", "2026-11-18")["2026-11-19|dinner"]
	if got.Source != store.SourceTemplate || got.RecipeID != "" || got.Portions != 0 || got.Text != "Vegetable curry" {
		t.Errorf("restored = %+v", got)
	}
	// A recipe that is named like the template's dish is still a recipe, not "the template again".
	same, err := f.db.CreateRecipe(f.h.ID, "", recipe.Recipe{Name: "Vegetable curry", Category: recipe.Main, Portions: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetMeals(f.h.ID, []store.Meal{{Day: "2026-11-19", Mealtime: meal.Dinner, RecipeID: same.ID, Portions: 3}}, o); err != nil {
		t.Fatal(err)
	}
	got = planOf(t, f.db, f.h, "2026-11-19", "2026-11-19", "2026-11-18")["2026-11-19|dinner"]
	if got.Source != store.SourceAdjusted || got.RecipeID != same.ID || got.Portions != 3 {
		t.Errorf("recipe named like the dish = %+v", got)
	}
}

func TestAnAdjustmentWithARecipeBecomesAPlainRecipeEntryWhenItsTemplateIsDeleted(t *testing.T) {
	f := newRecipeMealFixture(t)
	o := optionsOn("2026-11-18")
	tpl, err := f.db.CreateMealTemplate(f.h.ID, weekB(), o)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetMeals(f.h.ID, []store.Meal{{Day: "2026-11-19", Mealtime: meal.Dinner, RecipeID: f.curry.ID, Portions: 5}}, o); err != nil {
		t.Fatal(err)
	}
	if err := f.db.DeleteMealTemplate(f.h.ID, tpl.ID, o); err != nil {
		t.Fatal(err)
	}
	got := planOf(t, f.db, f.h, "2026-11-19", "2026-11-19", "2026-11-18")["2026-11-19|dinner"]
	if got.Source != store.SourceManual || got.RecipeID != f.curry.ID || got.Portions != 5 || got.Text != "Erdnuss-Curry" {
		t.Errorf("after the template went = %+v", got)
	}
}

func TestARenamedRecipeShowsItsNewNameInThePlanAndTheCalendar(t *testing.T) {
	f := newRecipeMealFixture(t)
	o := optionsOn("2026-11-14")
	if err := f.db.SetMeals(f.h.ID, []store.Meal{
		{Day: "2026-11-10", Mealtime: meal.Dinner, RecipeID: f.curry.ID, Portions: 4}, // before today
		{Day: "2026-11-18", Mealtime: meal.Dinner, RecipeID: f.curry.ID, Portions: 6},
	}, o); err != nil {
		t.Fatal(err)
	}
	r := f.curry.Recipe
	r.Name = "Erdnuss-Curry scharf"
	if _, err := f.db.UpdateRecipe(f.h.ID, "", r, o); err != nil {
		t.Fatal(err)
	}
	got := planOf(t, f.db, f.h, "2026-11-10", "2026-11-18", "2026-11-14")
	for _, k := range []string{"2026-11-10|dinner", "2026-11-18|dinner"} {
		if got[k].Text != "Erdnuss-Curry scharf" || got[k].RecipeID != f.curry.ID {
			t.Errorf("%s = %+v", k, got[k])
		}
	}
	cal, _ := f.db.MealCalendar(f.h.ID)
	titles := eventTitlesByDay(t, f.db, f.h, cal)
	if titles["2026-11-18"] != "Dinner: Erdnuss-Curry scharf" || titles["2026-11-10"] != "Dinner: Erdnuss-Curry scharf" {
		t.Errorf("titles = %v", titles)
	}
	if plain, _ := f.db.Meals(f.h.ID, "2026-11-18", "2026-11-18"); len(plain) != 1 || plain[0].Text != "Erdnuss-Curry scharf" {
		t.Errorf("hand-typed = %+v", plain)
	}
}

func TestADeletedRecipeLeavesItsNameAsFreeTextWithoutPortions(t *testing.T) {
	f := newRecipeMealFixture(t)
	o := optionsOn("2026-11-14")
	tpl, err := f.db.CreateMealTemplate(f.h.ID, weekB(), o)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetMeals(f.h.ID, []store.Meal{
		{Day: "2026-11-10", Mealtime: meal.Dinner, RecipeID: f.curry.ID, Portions: 4}, // a day that is over
		{Day: "2026-11-15", Mealtime: meal.Lunch, RecipeID: f.curry.ID, Portions: 6},  // a plain entry
		{Day: "2026-11-19", Mealtime: meal.Dinner, RecipeID: f.curry.ID, Portions: 5}, // an adjustment
	}, o); err != nil {
		t.Fatal(err)
	}
	_ = tpl
	if err := f.db.DeleteRecipe(f.h.ID, f.curry.ID); err != nil {
		t.Fatal(err)
	}
	got := planOf(t, f.db, f.h, "2026-11-10", "2026-11-19", "2026-11-14")
	for _, k := range []string{"2026-11-10|dinner", "2026-11-15|lunch", "2026-11-19|dinner"} {
		if got[k].RecipeID != "" || got[k].Portions != 0 || got[k].Text != "Erdnuss-Curry" {
			t.Errorf("%s = %+v: the name stays as free text", k, got[k])
		}
	}
	if got["2026-11-19|dinner"].Source != store.SourceAdjusted {
		t.Errorf("the adjustment stays an adjustment: %+v", got["2026-11-19|dinner"])
	}
	cal, _ := f.db.MealCalendar(f.h.ID)
	if titles := eventTitlesByDay(t, f.db, f.h, cal); titles["2026-11-15"] != "Lunch: Erdnuss-Curry" {
		t.Errorf("titles = %v", titles)
	}
	// The same name typed again later is plain text.
	if err := f.db.SetMeals(f.h.ID, []store.Meal{{Day: "2026-11-15", Mealtime: meal.Lunch, Text: "Erdnuss-Curry"}}, o); err != nil {
		t.Fatal(err)
	}
}

func TestPlannedMealsOfARecipeListEveryComingEntryByDayAndMealtime(t *testing.T) {
	f := newRecipeMealFixture(t)
	o := optionsOn("2026-11-14")
	other, _ := f.db.CreateRecipe(f.h.ID, "", recipe.Recipe{Name: "Suppe", Category: recipe.Main, Portions: 4})
	if err := f.db.SetMeals(f.h.ID, []store.Meal{
		{Day: "2026-11-10", Mealtime: meal.Dinner, RecipeID: f.curry.ID, Portions: 4}, // over
		{Day: "2026-12-03", Mealtime: meal.Dinner, RecipeID: f.curry.ID, Portions: 4},
		{Day: "2026-11-14", Mealtime: meal.Dinner, RecipeID: f.curry.ID, Portions: 4}, // today counts
		{Day: "2026-11-14", Mealtime: meal.Lunch, RecipeID: f.curry.ID, Portions: 4},
		{Day: "2026-11-15", Mealtime: meal.Dinner, RecipeID: other.ID, Portions: 4},
		{Day: "2026-11-16", Mealtime: meal.Dinner, Text: "Erdnuss-Curry"},
	}, o); err != nil {
		t.Fatal(err)
	}
	got, err := f.db.PlannedMealsOfRecipe(f.h.ID, f.curry.ID, "2026-11-14")
	if err != nil {
		t.Fatal(err)
	}
	want := []store.PlannedMeal{{Day: "2026-11-14", Mealtime: meal.Lunch}, {Day: "2026-11-14", Mealtime: meal.Dinner}, {Day: "2026-12-03", Mealtime: meal.Dinner}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("planned = %+v, want %+v", got, want)
	}
}

func TestEarlierDishesAreTheFreeTextMealsOfDaysUpToTodayMostRecentFirst(t *testing.T) {
	f := newRecipeMealFixture(t)
	o := optionsOn("2026-11-14")
	if err := f.db.SetMeals(f.h.ID, []store.Meal{
		{Day: "2026-11-02", Mealtime: meal.Dinner, Text: "Pasta bake"},
		{Day: "2026-11-09", Mealtime: meal.Dinner, Text: "Pasta bake"},
		{Day: "2026-11-10", Mealtime: meal.Lunch, Text: "Soup"},
		{Day: "2026-11-11", Mealtime: meal.Dinner, RecipeID: f.curry.ID, Portions: 4},
		{Day: "2026-11-20", Mealtime: meal.Dinner, Text: "Next week"}, // not earlier
	}, o); err != nil {
		t.Fatal(err)
	}
	got, err := f.db.EarlierDishes(f.h.ID, "2026-11-14")
	if err != nil {
		t.Fatal(err)
	}
	want := []recipe.Dish{{Text: "Soup", Day: "2026-11-10"}, {Text: "Pasta bake", Day: "2026-11-09"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("earlier = %+v, want %+v", got, want)
	}
}
