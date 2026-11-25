package store_test

import (
	"reflect"
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/shop"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 284: a recipe that leaves the plan, or whose portions shrink, takes
// its quantities back from the entries not yet ticked, and its note with them.

type takeBackFixture struct {
	recipeMealFixture
	l list.List
	o store.MealOptions
}

func newTakeBackFixture(t *testing.T) takeBackFixture {
	t.Helper()
	f := newRecipeMealFixture(t)
	l, err := f.db.CreateList(f.h.ID, list.List{Name: "Einkauf", Color: "green", Symbol: "square-filled", Sort: list.Manual, Suggestions: true})
	if err != nil {
		t.Fatal(err)
	}
	return takeBackFixture{f, l, optionsOn("2026-11-14")}
}

func (f takeBackFixture) plan(t *testing.T, recipeID, day string, portions int) {
	t.Helper()
	if err := f.db.SetMeals(f.h.ID, []store.Meal{{Day: day, Mealtime: meal.Dinner, RecipeID: recipeID, Portions: portions}}, f.o); err != nil {
		t.Fatal(err)
	}
}

func (f takeBackFixture) clear(t *testing.T, day string) {
	t.Helper()
	if err := f.db.SetMeals(f.h.ID, []store.Meal{{Day: day, Mealtime: meal.Dinner}}, f.o); err != nil {
		t.Fatal(err)
	}
}

// shop puts the meal on the list as the run of a tap does: the recipe is written for 2 portions.
func (f takeBackFixture) shop(t *testing.T, day string, portions int, ings ...recipe.Ingredient) store.ShopRun {
	t.Helper()
	m := shop.Meal{Key: day + ".dinner", Day: day, Mealtime: meal.Dinner, RecipeID: f.curry.ID, RecipeName: f.curry.Name, Portions: portions, Base: 2, Ingredients: ings}
	rows := shop.Rows([]shop.Meal{m}, func(in recipe.Ingredient) shop.Article { return shop.Article{ID: in.ArticleID, Text: in.Text} })
	run, err := f.db.AddMealShop(f.h.ID, store.ShopInput{ListID: f.l.ID, Day: "2026-11-14", Rows: rows,
		Meals: []store.ShopMeal{{Day: day, Mealtime: meal.Dinner, RecipeID: f.curry.ID, RecipeName: f.curry.Name, Portions: portions}},
		Show:  func(shop.Row) list.Show { return show("piece", "g", "kg") }})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func (f takeBackFixture) lines(t *testing.T) []string { return lines(t, f.db, f.h, f.l) }

func (f takeBackFixture) addByHand(t *testing.T, text, article string, q list.Quantity) {
	t.Helper()
	if _, err := f.db.AddItem(f.h.ID, f.l.ID, store.Item{Text: text, ArticleID: article, Qty: q}, show("g", "kg")); err != nil {
		t.Fatal(err)
	}
}

func (f takeBackFixture) tick(t *testing.T, text string) {
	t.Helper()
	es, _ := f.db.Entries(f.h.ID, f.l.ID)
	for _, e := range es {
		if e.Text == text {
			if err := f.db.SetEntryDone(f.h.ID, f.l.ID, e.ID, true); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("no entry %q", text)
}

func (f takeBackFixture) onList(t *testing.T) []store.ShopOnList {
	t.Helper()
	on, err := f.db.ShopMeals(f.h.ID, "2026-11-01", "2026-12-31")
	if err != nil {
		t.Fatal(err)
	}
	return on
}

func (f takeBackFixture) notes(t *testing.T) int {
	t.Helper()
	es, _ := f.db.Entries(f.h.ID, f.l.ID)
	var ids []string
	for _, e := range es {
		ids = append(ids, e.ID)
	}
	src, err := f.db.EntrySources(f.h.ID, ids)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range src {
		n += len(s.Parts)
	}
	return n
}

// Acceptance: entries reduced to zero disappear, the note goes, quantities that stood before stay.
func TestARecipeThatLeavesThePlanTakesItsQuantitiesBack(t *testing.T) {
	f := newTakeBackFixture(t)
	f.addByHand(t, "Süßkartoffeln", "sweet-potatoes", list.NewQuantity(500, "g"))
	f.plan(t, f.curry.ID, "2026-11-19", 4)
	f.shop(t, "2026-11-19", 4, potatoes, coconut, peanut)
	if got := f.lines(t); !reflect.DeepEqual(got, []string{"Süßkartoffeln 1.1 kg", "Kokosmilch 2 can", "Erdnussbutter"}) {
		t.Fatalf("list = %v", got)
	}
	f.clear(t, "2026-11-19")
	if got := f.lines(t); !reflect.DeepEqual(got, []string{"Süßkartoffeln 0.5 kg"}) { // the unit the run chose stays
		t.Errorf("after the recipe left = %v", got)
	}
	if n := f.notes(t); n != 0 {
		t.Errorf("%d notes left", n)
	}
	if on := f.onList(t); len(on) != 0 {
		t.Errorf("the meal is not on a list any more: %+v", on)
	}
}

func TestShrinkingPortionsTakesBackTheDifference(t *testing.T) {
	f := newTakeBackFixture(t)
	f.addByHand(t, "Süßkartoffeln", "sweet-potatoes", list.NewQuantity(500, "g"))
	f.plan(t, f.curry.ID, "2026-11-19", 4)
	f.shop(t, "2026-11-19", 4, potatoes, coconut, peanut)
	f.plan(t, f.curry.ID, "2026-11-19", 2)
	if got := f.lines(t); !reflect.DeepEqual(got, []string{"Süßkartoffeln 0.8 kg", "Kokosmilch 1 can", "Erdnussbutter"}) {
		t.Fatalf("after 4 to 2 portions = %v", got)
	}
	on := f.onList(t)
	if len(on) != 1 || on[0].Portions != 2 {
		t.Errorf("the meal has 2 portions on the list now: %+v", on)
	}
	// Growing again is not a take-back, and the list is left alone.
	f.plan(t, f.curry.ID, "2026-11-19", 3)
	if got := f.lines(t); !reflect.DeepEqual(got, []string{"Süßkartoffeln 0.8 kg", "Kokosmilch 1 can", "Erdnussbutter"}) {
		t.Errorf("after growing = %v", got)
	}
	f.plan(t, f.curry.ID, "2026-11-19", 1)
	if got := f.lines(t); !reflect.DeepEqual(got, []string{"Süßkartoffeln 0.65 kg", "Kokosmilch 0.5 can", "Erdnussbutter"}) {
		t.Errorf("after 2 to 1 portions = %v", got)
	}
}

// Acceptance: ticked entries are never touched.
func TestTickedEntriesAreNeverTouched(t *testing.T) {
	f := newTakeBackFixture(t)
	f.plan(t, f.curry.ID, "2026-11-19", 4)
	f.shop(t, "2026-11-19", 4, potatoes, coconut, peanut)
	f.tick(t, "Kokosmilch")
	f.plan(t, f.curry.ID, "2026-11-19", 2)
	if got := f.lines(t); !reflect.DeepEqual(got, []string{"Süßkartoffeln 300 g", "Kokosmilch 2 can (done)", "Erdnussbutter"}) {
		t.Fatalf("after shrinking = %v", got)
	}
	f.clear(t, "2026-11-19")
	if got := f.lines(t); !reflect.DeepEqual(got, []string{"Kokosmilch 2 can (done)"}) {
		t.Errorf("after leaving = %v: the ticked one stays, the rest goes", got)
	}
}

// Acceptance: two meals contributing to one entry: removing one keeps the other's quantity.
func TestRemovingOneOfTwoMealsKeepsTheOthersQuantity(t *testing.T) {
	f := newTakeBackFixture(t)
	f.plan(t, f.curry.ID, "2026-11-19", 4)
	f.plan(t, f.curry.ID, "2026-11-20", 2)
	f.shop(t, "2026-11-19", 4, potatoes, coconut)
	f.shop(t, "2026-11-20", 2, potatoes)
	if got := f.lines(t); !reflect.DeepEqual(got, []string{"Süßkartoffeln 900 g", "Kokosmilch 2 can"}) {
		t.Fatalf("list = %v", got)
	}
	f.clear(t, "2026-11-19")
	if got := f.lines(t); !reflect.DeepEqual(got, []string{"Süßkartoffeln 300 g"}) {
		t.Errorf("after the first meal left = %v", got)
	}
	es, _ := f.db.Entries(f.h.ID, f.l.ID)
	src, _ := f.db.EntrySources(f.h.ID, []string{es[0].ID})
	if s := src[es[0].ID]; len(s.Parts) != 1 || s.Parts[0].Day != "2026-11-20" || s.Parts[0].Qty.Base != 300 {
		t.Errorf("the note is the other meal's only: %+v", s)
	}
	f.clear(t, "2026-11-20")
	if got := f.lines(t); len(got) != 0 {
		t.Errorf("both gone = %v", got)
	}
}

func TestAnotherRecipeOnTheSameMealTakesTheFirstBack(t *testing.T) {
	f := newTakeBackFixture(t)
	other, err := f.db.CreateRecipe(f.h.ID, "", recipe.Recipe{Name: "Gemüse-Lasagne", Category: recipe.Main, Portions: 4, Minutes: 45})
	if err != nil {
		t.Fatal(err)
	}
	f.plan(t, f.curry.ID, "2026-11-19", 4)
	f.shop(t, "2026-11-19", 4, potatoes)
	f.plan(t, other.ID, "2026-11-19", 4)
	if got := f.lines(t); len(got) != 0 {
		t.Errorf("list = %v", got)
	}
}

func TestADeletedRecipeTakesItsQuantitiesBack(t *testing.T) {
	f := newTakeBackFixture(t)
	f.plan(t, f.curry.ID, "2026-11-19", 4)
	f.shop(t, "2026-11-19", 4, potatoes, peanut)
	if err := f.db.DeleteRecipe(f.h.ID, f.curry.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.lines(t); len(got) != 0 {
		t.Errorf("list = %v", got)
	}
}

// What a run made by itself is gone with its meal; a taken-back run is not taken back twice.
func TestUndoAfterATakeBackLeavesWhatIsLeft(t *testing.T) {
	f := newTakeBackFixture(t)
	f.addByHand(t, "Süßkartoffeln", "sweet-potatoes", list.NewQuantity(500, "g"))
	f.plan(t, f.curry.ID, "2026-11-19", 4)
	run := f.shop(t, "2026-11-19", 4, potatoes, coconut)
	f.plan(t, f.curry.ID, "2026-11-19", 2)
	if _, err := f.db.UndoMealShop(f.h.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.lines(t); !reflect.DeepEqual(got, []string{"Süßkartoffeln 0.5 kg"}) {
		t.Errorf("list = %v", got)
	}
}
