package store_test

import (
	"errors"
	"testing"

	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 282: a meal plan template slot can hold a recipe.

// curryOnTuesdays is a template that fills dinner on Tuesday with the recipe and
// on Monday with free text.
func curryOnTuesdays(f recipeMealFixture) meal.Template {
	return meal.Template{
		Name: "Weeks", Mealtime: meal.Dinner, Interval: 1, Start: "2026-11-16",
		Entries: map[meal.Slot]string{meal.At(1, meal.Dinner): "Lentil soup"},
		Recipes: map[meal.Slot]string{meal.At(2, meal.Dinner): f.curry.ID},
	}
}

func TestATemplateSlotHoldsARecipeAndIsReadBackWithItsName(t *testing.T) {
	f := newRecipeMealFixture(t)
	created, err := f.db.CreateMealTemplate(f.h.ID, curryOnTuesdays(f), optionsOn("2026-11-18"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.db.MealTemplate(f.h.ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	slot := meal.At(2, meal.Dinner)
	if got.Recipes[slot] != f.curry.ID || got.Entries[slot] != "Erdnuss-Curry" || got.Entries[meal.At(1, meal.Dinner)] != "Lentil soup" || len(got.Recipes) != 1 {
		t.Fatalf("read = %+v", got)
	}
}

func TestApplyingATemplateCreatesRecipeEntriesWithHouseholdSizePortions(t *testing.T) {
	f := newRecipeMealFixture(t) // Mama and Mia: two people
	if _, err := f.db.AddPerson(f.h.ID, store.Person{Name: "Papa", Role: store.RoleParent}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.CreateMealTemplate(f.h.ID, curryOnTuesdays(f), optionsOn("2026-11-18")); err != nil {
		t.Fatal(err)
	}
	got := planOf(t, f.db, f.h, "2026-11-23", "2026-12-01", "2026-11-18")
	for _, day := range []string{"2026-11-24", "2026-12-01"} {
		m := got[day+"|dinner"]
		if m.RecipeID != f.curry.ID || m.Portions != 3 || m.Text != "Erdnuss-Curry" || m.Source != store.SourceTemplate || m.TemplateDish != "Erdnuss-Curry" {
			t.Errorf("%s = %+v, want the recipe for 3 portions from the template", day, m)
		}
	}
	if m := got["2026-11-23|dinner"]; m.RecipeID != "" || m.Portions != 0 || m.Text != "Lentil soup" {
		t.Errorf("free text slot = %+v", m)
	}
	cal, _ := f.db.MealCalendar(f.h.ID)
	if title := eventTitlesByDay(t, f.db, f.h, cal)["2026-11-24"]; title != "Dinner: Erdnuss-Curry" {
		t.Errorf("event = %q", title)
	}
	rows, err := f.db.PlannedMealsOfRecipe(f.h.ID, f.curry.ID, "2026-11-18")
	if err != nil || len(rows) == 0 || rows[0].Day != "2026-11-24" {
		t.Errorf("planned meals of the recipe = %v, %v: the written plan rows hold the recipe", rows, err)
	}
}

func TestDeletingARecipeLeavesTheTemplateSlotAsItsNameInFreeText(t *testing.T) {
	f := newRecipeMealFixture(t)
	o := optionsOn("2026-11-18")
	created, err := f.db.CreateMealTemplate(f.h.ID, curryOnTuesdays(f), o)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.DeleteRecipe(f.h.ID, f.curry.ID); err != nil {
		t.Fatal(err)
	}
	got, err := f.db.MealTemplate(f.h.ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	slot := meal.At(2, meal.Dinner)
	if len(got.Recipes) != 0 || got.Entries[slot] != "Erdnuss-Curry" {
		t.Fatalf("template after delete = %+v: the slot is the name in free text", got)
	}
	if err := f.db.RefreshMealPlan(f.h.ID, o); err != nil {
		t.Fatal(err)
	}
	m := planOf(t, f.db, f.h, "2026-11-24", "2026-11-24", "2026-11-18")["2026-11-24|dinner"]
	if m.RecipeID != "" || m.Portions != 0 || m.Text != "Erdnuss-Curry" || m.Source != store.SourceTemplate {
		t.Errorf("plan after delete = %+v", m)
	}
}

func TestARenamedRecipeIsRenamedInTheTemplateSlotAndItsPlan(t *testing.T) {
	f := newRecipeMealFixture(t)
	o := optionsOn("2026-11-18")
	created, err := f.db.CreateMealTemplate(f.h.ID, curryOnTuesdays(f), o)
	if err != nil {
		t.Fatal(err)
	}
	r := f.curry.Recipe
	r.ID, r.Name = f.curry.ID, "Cashew-Curry"
	if _, err := f.db.UpdateRecipe(f.h.ID, "", r, o); err != nil {
		t.Fatal(err)
	}
	got, _ := f.db.MealTemplate(f.h.ID, created.ID)
	if got.Entries[meal.At(2, meal.Dinner)] != "Cashew-Curry" {
		t.Errorf("slot = %q", got.Entries[meal.At(2, meal.Dinner)])
	}
	m := planOf(t, f.db, f.h, "2026-11-24", "2026-11-24", "2026-11-18")["2026-11-24|dinner"]
	if m.Text != "Cashew-Curry" || m.TemplateDish != "Cashew-Curry" || m.RecipeID != f.curry.ID {
		t.Errorf("plan = %+v", m)
	}
}

func TestATemplateSlotNeedsARecipeOfTheHousehold(t *testing.T) {
	f := newRecipeMealFixture(t)
	bad := curryOnTuesdays(f)
	bad.Recipes[meal.At(2, meal.Dinner)] = "no-such-recipe"
	if _, err := f.db.CreateMealTemplate(f.h.ID, bad, optionsOn("2026-11-18")); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("unknown recipe = %v, want ErrInvalid", err)
	}
}

func TestAWeeksChangeToATemplateRecipeStaysAnAdjustment(t *testing.T) {
	f := newRecipeMealFixture(t)
	o := optionsOn("2026-11-18")
	if _, err := f.db.CreateMealTemplate(f.h.ID, curryOnTuesdays(f), o); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetMeals(f.h.ID, []store.Meal{{Day: "2026-11-24", Mealtime: meal.Dinner, Text: "Pizza"}}, o); err != nil {
		t.Fatal(err)
	}
	m := planOf(t, f.db, f.h, "2026-11-24", "2026-11-24", "2026-11-18")["2026-11-24|dinner"]
	if m.Text != "Pizza" || m.Source != store.SourceAdjusted || m.TemplateDish != "Erdnuss-Curry" || m.RecipeID != "" {
		t.Fatalf("adjusted = %+v", m)
	}
	if err := f.db.RestoreMeal(f.h.ID, "2026-11-24", meal.Dinner, o); err != nil {
		t.Fatal(err)
	}
	m = planOf(t, f.db, f.h, "2026-11-24", "2026-11-24", "2026-11-18")["2026-11-24|dinner"]
	if m.RecipeID != f.curry.ID || m.Portions != 2 || m.Source != store.SourceTemplate {
		t.Fatalf("back to template = %+v", m)
	}
}
