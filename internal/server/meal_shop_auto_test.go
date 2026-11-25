package server_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/always"
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 285: every day at the household's time the ingredients of the meals of
// the next days go on the list, through the runner of "always on the list".
// The clock stands at Monday 23/11/2026, 03:00 in Berlin.

type autoShopFixture struct {
	*alwaysFixture
	rec store.Recipe
}

func newAutoShopFixture(t *testing.T) *autoShopFixture {
	t.Helper()
	f := &autoShopFixture{alwaysFixture: newAlwaysFixture(t)}
	rec, err := f.db.CreateRecipe(f.h.ID, "", recipe.Recipe{Name: "Süßkartoffel-Curry", Category: recipe.Main, Portions: 2, Minutes: 30,
		Ingredients: []recipe.Ingredient{
			{Amount: 300, Unit: "g", Text: "Süßkartoffeln", ArticleID: "sweet-potatoes"},
			{Amount: 1, Unit: "pack", Text: "Salz", ArticleID: "salt"},
		}})
	if err != nil {
		t.Fatal(err)
	}
	f.rec = rec
	return f
}

// plan puts the curry for two on the dinner of a day.
func (f *autoShopFixture) plan(t *testing.T, day string) {
	t.Helper()
	berlin, _ := time.LoadLocation("Europe/Berlin")
	o := store.MealOptions{CalendarName: "Meal plan", Location: berlin, Today: "2026-11-23", Title: func(m meal.Mealtime, text string) string { return string(m) + ": " + text }}
	if err := f.db.SetMeals(f.h.ID, []store.Meal{{Day: day, Mealtime: meal.Dinner, RecipeID: f.rec.ID, Portions: 2}}, o); err != nil {
		t.Fatal(err)
	}
}

func (f *autoShopFixture) auto(t *testing.T, a store.MealShopAuto) {
	t.Helper()
	if err := f.db.SetMealShopAuto(f.h.ID, a); err != nil {
		t.Fatal(err)
	}
}

// lines are the entries of the list as "text base" for the quantities in grams.
func (f *autoShopFixture) potatoes(t *testing.T) (float64, int) {
	t.Helper()
	n, base := 0, 0.0
	for _, e := range f.entries(t) {
		if e.ArticleID == "sweet-potatoes" {
			n++
			base = e.Qty.Base
		}
		if e.ArticleID == "salt" {
			t.Errorf("salt is a pantry article: left out, got %+v", e)
		}
	}
	return base, n
}

func TestByHandIsTheDefaultSoNothingComesByItself(t *testing.T) {
	t.Parallel()
	f := newAutoShopFixture(t)
	f.plan(t, "2026-11-23")
	f.tick(monday3)
	if got := f.entries(t); len(got) != 0 {
		t.Fatalf("by hand: %+v", got)
	}
}

func TestAtThreeTheMealsOfTheNextSevenDaysGoOnTheListWithoutPantryArticles(t *testing.T) {
	t.Parallel()
	f := newAutoShopFixture(t)
	f.auto(t, store.MealShopAuto{On: true, Days: store.DefaultShopDays, ListID: f.shop.ID})
	f.plan(t, "2026-11-23") // today
	f.plan(t, "2026-11-29") // the seventh day
	f.plan(t, "2026-11-30") // the eighth: not yet
	f.tick(f.now)
	if len(f.entries(t)) != 0 {
		t.Fatal("before 03:00 nothing comes")
	}
	f.tick(monday3)
	if base, n := f.potatoes(t); n != 1 || base != 600 {
		t.Fatalf("potatoes = %v in %d entries, want 600 g: today and the seventh day, once each", base, n)
	}
	on, err := f.db.ShopMeals(f.h.ID, "2026-11-23", "2026-12-31")
	if err != nil || len(on) != 2 || on[0].Day != "2026-11-23" || on[1].Day != "2026-11-29" || on[0].Portions != 2 {
		t.Errorf("meals on the list = %+v %v: the contributions are recorded", on, err)
	}
}

func TestTheDaysAreConfigurable(t *testing.T) {
	t.Parallel()
	f := newAutoShopFixture(t)
	f.auto(t, store.MealShopAuto{On: true, Days: 2, ListID: f.shop.ID})
	f.plan(t, "2026-11-24")
	f.plan(t, "2026-11-25")
	f.tick(monday3)
	if base, _ := f.potatoes(t); base != 300 {
		t.Errorf("two days ahead: %v g, want only the meal of tomorrow", base)
	}
}

// Acceptance: idempotent, a meal is never added twice (Y).
func TestARunnerThatRunsAgainNeverAddsAMealTwice(t *testing.T) {
	t.Parallel()
	f := newAutoShopFixture(t)
	f.auto(t, store.MealShopAuto{On: true, Days: 7, ListID: f.shop.ID})
	f.plan(t, "2026-11-23")
	f.tick(monday3)
	if err := f.db.SetAlwaysLastRun(f.h.ID, ""); err != nil { // as if the day's marker was lost
		t.Fatal(err)
	}
	f.tick(monday3.Add(time.Hour))
	if base, n := f.potatoes(t); n != 1 || base != 300 {
		t.Fatalf("after a second run: %v g in %d entries, want 300 g once", base, n)
	}
	f.plan(t, "2026-11-30") // the plan grows into the window the next day
	f.tick(monday3.Add(24 * time.Hour))
	if base, _ := f.potatoes(t); base != 600 {
		t.Errorf("the next day adds the new meal only: %v g, want 600", base)
	}
	f.tick(monday3.Add(48 * time.Hour))
	if base, _ := f.potatoes(t); base != 600 {
		t.Errorf("the day after: %v g, want 600", base)
	}
}

func TestAMealAlreadyOnTheListByHandIsNotAddedAgain(t *testing.T) {
	t.Parallel()
	f := newAutoShopFixture(t)
	f.plan(t, "2026-11-23")
	f.auto(t, store.MealShopAuto{On: true, Days: 7, ListID: f.shop.ID})
	f.tick(monday3)
	// someone puts the same meal on again by hand in the meantime: the contributions say it is there
	on, _ := f.db.ShopMeals(f.h.ID, "2026-11-23", "2026-11-23")
	if len(on) != 1 || on[0].Portions != 2 {
		t.Fatalf("on the list = %+v", on)
	}
	f.tick(monday3.Add(24 * time.Hour))
	if base, _ := f.potatoes(t); base != 300 {
		t.Errorf("a meal on the list is not added again: %v g", base)
	}
}

// Acceptance: nothing is added during holiday mode.
func TestNothingIsAddedDuringTheHoliday(t *testing.T) {
	t.Parallel()
	f := newAutoShopFixture(t)
	f.auto(t, store.MealShopAuto{On: true, Days: 7, ListID: f.shop.ID})
	f.plan(t, "2026-11-23")
	if err := f.db.SetAlwaysSettings(f.h.ID, always.Settings{At: "03:00", Holiday: always.Holiday{From: "2026-11-23", To: "2026-11-24"}}); err != nil {
		t.Fatal(err)
	}
	f.tick(monday3)
	f.tick(monday3.Add(24 * time.Hour))
	if got := f.entries(t); len(got) != 0 {
		t.Fatalf("during the holiday: %+v", got)
	}
	f.tick(monday3.Add(48 * time.Hour)) // Wednesday: the holiday is over; nothing is caught up, the meals still ahead are added
	if _, n := f.potatoes(t); n != 0 {
		t.Errorf("the meal of Monday is over: it is not caught up (%d entries)", n)
	}
	f.plan(t, "2026-11-26")
	f.tick(monday3.Add(72 * time.Hour))
	if base, _ := f.potatoes(t); base != 300 {
		t.Errorf("after the holiday the meals ahead come again: %v g", base)
	}
}

func TestWithoutAChosenListTheLastListOfARunIsUsedElseTheFirstWithSuggestions(t *testing.T) {
	t.Parallel()
	f := newAutoShopFixture(t)
	f.auto(t, store.MealShopAuto{On: true, Days: 7})
	f.plan(t, "2026-11-24")
	f.tick(monday3)
	if base, _ := f.potatoes(t); base != 300 {
		t.Errorf("first list with suggestions: %v g", base)
	}
}
