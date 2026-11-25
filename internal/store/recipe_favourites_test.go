package store_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 287: a recipe is a favourite per person ("Mia likes it"); "last
// cooked" is the latest planned day of the recipe that is over.

func TestARecipeIsAFavouriteOfEachPersonMarked(t *testing.T) {
	f := newRecipeMealFixture(t)
	if got, _ := f.db.Recipe(f.h.ID, f.curry.ID); len(got.FavouriteOf) != 0 {
		t.Fatalf("a new recipe is nobody's favourite: %v", got.FavouriteOf)
	}
	if err := f.db.SetRecipeFavourites(f.h.ID, f.curry.ID, []string{f.mia.ID, f.mama.ID}); err != nil {
		t.Fatal(err)
	}
	got, err := f.db.Recipe(f.h.ID, f.curry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.FavouriteOf, []string{f.mama.ID, f.mia.ID}) {
		t.Errorf("favourite of = %v, want Mama and Mia in the order of the household", got.FavouriteOf)
	}
	list, _ := f.db.Recipes(f.h.ID)
	if len(list) != 1 || len(list[0].FavouriteOf) != 2 {
		t.Errorf("list = %+v: the list carries the marks too", list)
	}
	// The marks are replaced as a whole: an empty set clears them.
	if err := f.db.SetRecipeFavourites(f.h.ID, f.curry.ID, []string{f.mia.ID, f.mia.ID}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.Recipe(f.h.ID, f.curry.ID); !reflect.DeepEqual(got.FavouriteOf, []string{f.mia.ID}) {
		t.Errorf("after replacing = %v, want Mia once", got.FavouriteOf)
	}
	if err := f.db.SetRecipeFavourites(f.h.ID, f.curry.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.Recipe(f.h.ID, f.curry.ID); len(got.FavouriteOf) != 0 {
		t.Errorf("after clearing = %v", got.FavouriteOf)
	}
}

func TestFavouritesNameOnlyPeopleAndRecipesOfTheHousehold(t *testing.T) {
	f := newRecipeMealFixture(t)
	if err := f.db.SetRecipeFavourites(f.h.ID, f.curry.ID, []string{"nobody"}); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("unknown person = %v, want ErrInvalid", err)
	}
	if err := f.db.SetRecipeFavourites(f.h.ID, "nothing", []string{f.mia.ID}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown recipe = %v, want ErrNotFound", err)
	}
	if got, _ := f.db.Recipe(f.h.ID, f.curry.ID); len(got.FavouriteOf) != 0 {
		t.Errorf("a refused call changed the marks: %v", got.FavouriteOf)
	}
}

func TestAFavouriteGoesWhenItsPersonOrItsRecipeGoes(t *testing.T) {
	f := newRecipeMealFixture(t)
	if err := f.db.SetRecipeFavourites(f.h.ID, f.curry.ID, []string{f.mia.ID, f.mama.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.SQL().Exec(`DELETE FROM people WHERE id = ?`, f.mia.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.Recipe(f.h.ID, f.curry.ID); !reflect.DeepEqual(got.FavouriteOf, []string{f.mama.ID}) {
		t.Errorf("after Mia left = %v", got.FavouriteOf)
	}
	count := func() (n int) {
		t.Helper()
		if err := f.db.SQL().QueryRow(`SELECT COUNT(*) FROM recipe_favourites WHERE recipe_id = ?`, f.curry.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(); n != 1 {
		t.Fatalf("before deleting the recipe = %d rows, want 1", n)
	}
	if err := f.db.DeleteRecipe(f.h.ID, f.curry.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Errorf("after deleting the recipe = %d rows, want 0: the marks go with it", n)
	}
}

func TestLastCookedIsTheLatestPlannedDayThatIsOver(t *testing.T) {
	f := newRecipeMealFixture(t)
	o := optionsOn("2026-11-01")
	set := func(day string, mt meal.Mealtime, m store.Meal) {
		t.Helper()
		m.Day, m.Mealtime = day, mt
		if err := f.db.SetMeals(f.h.ID, []store.Meal{m}, o); err != nil {
			t.Fatal(err)
		}
	}
	set("2026-11-03", meal.Dinner, store.Meal{RecipeID: f.curry.ID, Portions: 4})
	set("2026-11-10", meal.Lunch, store.Meal{RecipeID: f.curry.ID, Portions: 4})
	set("2026-11-10", meal.Dinner, store.Meal{Text: "Soup"})
	set("2026-11-20", meal.Dinner, store.Meal{RecipeID: f.curry.ID, Portions: 4})
	cooked := func(today string) string {
		t.Helper()
		m, err := f.db.RecipesLastCooked(f.h.ID, today)
		if err != nil {
			t.Fatal(err)
		}
		return m[f.curry.ID]
	}
	if got := cooked("2026-11-03"); got != "" {
		t.Errorf("on the day itself = %q: a planned day passes at its end, so not yet cooked", got)
	}
	if got := cooked("2026-11-04"); got != "2026-11-03" {
		t.Errorf("the day after = %q, want 2026-11-03", got)
	}
	if got := cooked("2026-11-15"); got != "2026-11-10" {
		t.Errorf("mid-month = %q, want 2026-11-10: the latest day that is over, not the one to come", got)
	}
	if got := cooked("2026-11-21"); got != "2026-11-20" {
		t.Errorf("after the last = %q, want 2026-11-20", got)
	}
}

func TestLastCookedStaysAfterTheRecipeIsRenamed(t *testing.T) {
	f := newRecipeMealFixture(t)
	if err := f.db.SetMeals(f.h.ID, []store.Meal{{Day: "2026-11-03", Mealtime: meal.Dinner, RecipeID: f.curry.ID, Portions: 4}}, optionsOn("2026-11-01")); err != nil {
		t.Fatal(err)
	}
	renamed := f.curry.Recipe
	renamed.Name = "Curry"
	if _, err := f.db.UpdateRecipe(f.h.ID, f.mama.ID, renamed, optionsOn("2026-11-10")); err != nil {
		t.Fatal(err)
	}
	m, err := f.db.RecipesLastCooked(f.h.ID, "2026-11-10")
	if err != nil || m[f.curry.ID] != "2026-11-03" {
		t.Errorf("after a rename = %v, %v", m, err)
	}
}
