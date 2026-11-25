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

// Ticket 283: every add of a meal to a list is recorded per list entry and meal.

func shopRows(day string, portions int, ings ...recipe.Ingredient) ([]shop.Row, store.ShopMeal) {
	m := shop.Meal{Key: day + ".dinner", Day: day, Mealtime: meal.Dinner, RecipeID: "r1", RecipeName: "Curry", Portions: portions, Base: 2, Ingredients: ings}
	rows := shop.Rows([]shop.Meal{m}, func(in recipe.Ingredient) shop.Article {
		return shop.Article{ID: in.ArticleID, Text: in.Text}
	})
	return rows, store.ShopMeal{Day: day, Mealtime: meal.Dinner, RecipeID: "r1", RecipeName: "Curry", Portions: portions}
}

func addShop(t *testing.T, db *store.DB, h store.Household, l list.List, rows []shop.Row, ms ...store.ShopMeal) store.ShopRun {
	t.Helper()
	run, err := db.AddMealShop(h.ID, store.ShopInput{ListID: l.ID, Day: "2026-11-17", Rows: rows, Meals: ms,
		Show: func(shop.Row) list.Show { return show("piece", "g", "kg") }})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

var (
	potatoes = recipe.Ingredient{Amount: 300, Unit: "g", Text: "Süßkartoffeln", ArticleID: "sweet-potatoes"}
	coconut  = recipe.Ingredient{Amount: 1, Unit: "can", Text: "Kokosmilch", ArticleID: "coconut-milk"}
	peanut   = recipe.Ingredient{Amount: 1, Unit: "tbsp", Text: "Erdnussbutter", ArticleID: "peanut-butter"}
)

func TestAddingAMealRecordsContributionsAndWhatTheMealHasOnTheList(t *testing.T) {
	db, h, l := shopping(t)
	if _, err := db.AddItem(h.ID, l.ID, store.Item{Text: "Süßkartoffeln", ArticleID: "sweet-potatoes", Qty: list.NewQuantity(500, "g")}, show("g", "kg")); err != nil {
		t.Fatal(err)
	}
	rows, m := shopRows("2026-11-19", 4, potatoes, coconut, peanut)
	run := addShop(t, db, h, l, rows, m)
	if got := lines(t, db, h, l); !reflect.DeepEqual(got, []string{"Süßkartoffeln 1.1 kg", "Kokosmilch 2 can", "Erdnussbutter"}) {
		t.Fatalf("list = %v", got)
	}
	es, _ := db.Entries(h.ID, l.ID)
	src, err := db.EntrySources(h.ID, []string{es[0].ID, es[1].ID, es[2].ID})
	if err != nil {
		t.Fatal(err)
	}
	if s := src[es[0].ID]; s.Prior.Base != 500 || len(s.Parts) != 1 || s.Parts[0].Qty.Base != 600 || s.Parts[0].RecipeName != "Curry" || s.Parts[0].Day != "2026-11-19" {
		t.Errorf("potatoes source = %+v", s)
	}
	if s := src[es[2].ID]; len(s.Parts) != 1 || !s.Parts[0].Qty.None() || !s.Prior.None() {
		t.Errorf("peanut butter source = %+v", s)
	}
	on, err := db.ShopMeals(h.ID, "2026-11-19", "2026-11-19")
	if err != nil || len(on) != 1 || on[0].Portions != 4 || on[0].ListName != "Einkauf" || on[0].RunID != run.ID || on[0].RunDay != "2026-11-17" {
		t.Fatalf("meals on a list = %+v %v", on, err)
	}
}

// Acceptance: take back exactly that run; what was ticked off meanwhile stays.
func TestUndoTakesBackExactlyThatRunAndLeavesTickedOffEntries(t *testing.T) {
	db, h, l := shopping(t)
	if _, err := db.AddItem(h.ID, l.ID, store.Item{Text: "Süßkartoffeln", ArticleID: "sweet-potatoes", Qty: list.NewQuantity(500, "g")}, show("g", "kg")); err != nil {
		t.Fatal(err)
	}
	rows, m := shopRows("2026-11-19", 4, potatoes, coconut, peanut)
	run := addShop(t, db, h, l, rows, m)
	es, _ := db.Entries(h.ID, l.ID)
	if err := db.SetEntryDone(h.ID, l.ID, es[1].ID, true); err != nil { // someone ticked the coconut milk off
		t.Fatal(err)
	}
	res, err := db.UndoMealShop(h.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := lines(t, db, h, l); !reflect.DeepEqual(got, []string{"Süßkartoffeln 500 g", "Kokosmilch 2 can (done)"}) {
		t.Fatalf("list after undo = %v", got)
	}
	if !reflect.DeepEqual(res.Removed, []string{"Erdnussbutter"}) || !reflect.DeepEqual(res.Ticked, []string{"Kokosmilch"}) || len(res.Reverted) != 1 || res.Reverted[0].Text != "Süßkartoffeln" || res.Reverted[0].Qty.Base != 500 {
		t.Errorf("undo result = %+v", res)
	}
	if on, _ := db.ShopMeals(h.ID, "2026-11-19", "2026-11-19"); len(on) != 0 {
		t.Errorf("the meal is not on a list any more: %+v", on)
	}
	if _, err := db.UndoMealShop(h.ID, run.ID); err == nil {
		t.Error("a run is taken back once")
	}
}

func TestTheListLastUsedForMealsIsKept(t *testing.T) {
	db, h, l := shopping(t)
	if id, _ := db.ShopLastList(h.ID); id != "" {
		t.Fatalf("no list used yet: %q", id)
	}
	rows, m := shopRows("2026-11-19", 4, coconut)
	addShop(t, db, h, l, rows, m)
	if id, _ := db.ShopLastList(h.ID); id != l.ID {
		t.Errorf("last list = %q", id)
	}
	_ = store.ErrNotFound
}
