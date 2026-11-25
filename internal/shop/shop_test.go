package shop_test

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/shop"
	"github.com/hejmo-home/hejmo/internal/unit"
)

func resolve(in recipe.Ingredient) shop.Article {
	return shop.Article{ID: in.ArticleID, Text: in.Text, Pantry: in.ArticleID == "salt" || in.ArticleID == "oil"}
}

func show(shop.Row) list.Show {
	return func(base float64, kind unit.Kind) string {
		if kind == unit.Weight {
			if base >= 1000 {
				return "kg"
			}
			return "g"
		}
		return "piece"
	}
}

func curry() shop.Meal {
	return shop.Meal{Key: "2026-11-17.dinner", Day: "2026-11-17", Mealtime: meal.Dinner, RecipeID: "r1", RecipeName: "Curry", Portions: 4, Base: 2, Ingredients: []recipe.Ingredient{
		{Amount: 300, Unit: "g", Text: "Süßkartoffeln", ArticleID: "sweet-potatoes"},
		{Amount: 1, Unit: "tbsp", Text: "Erdnussbutter", ArticleID: "peanut-butter"},
		{Amount: 1, Unit: "can", Text: "Kokosmilch", ArticleID: "coconut-milk"},
		{Amount: 1, Unit: "pinch", Text: "Salz", ArticleID: "salt"},
		{Amount: 2, Unit: "tbsp", Text: "Currypaste"},
	}}
}

// Acceptance: quantities are scaled to the planned portions, kitchen measures go without a quantity, containers keep it.
func TestRowsScaleToPortionsAndDropKitchenMeasures(t *testing.T) {
	t.Parallel()
	rows := shop.Rows([]shop.Meal{curry()}, resolve)
	by := map[string]shop.Row{}
	for _, r := range rows {
		by[r.Text] = r
	}
	if q := by["Süßkartoffeln"].Qty; q.Unit != "g" || q.Base != 600 {
		t.Errorf("sweet potatoes = %+v, want 600 g", q)
	}
	if q := by["Erdnussbutter"].Qty; !q.None() {
		t.Errorf("a tablespoon goes on without a quantity, got %+v", q)
	}
	if p := by["Erdnussbutter"].Parts[0]; p.Kitchen != "tbsp" || p.KitchenAmount != 2 {
		t.Errorf("the kitchen measure is kept for the sentence: %+v", p)
	}
	if q := by["Kokosmilch"].Qty; q.Unit != "can" || q.Amount() != 2 {
		t.Errorf("coconut milk = %+v, want 2 cans", q)
	}
	if !by["Salz"].Pantry || by["Süßkartoffeln"].Pantry {
		t.Errorf("pantry flags: %+v", rows)
	}
	if by["Currypaste"].ArticleID != "" {
		t.Errorf("free text has no article")
	}
}

// Rows of the same article, variety and kind add up across meals, and each meal's share is kept.
func TestRowsAddUpAcrossMealsAndKeepEachShare(t *testing.T) {
	t.Parallel()
	a, b := curry(), curry()
	b.Key, b.Day, b.Portions = "2026-11-19.dinner", "2026-11-19", 2
	b.Ingredients = []recipe.Ingredient{
		{Amount: 1, Unit: "kg", Text: "Süßkartoffeln", ArticleID: "sweet-potatoes"},
		{Amount: 2, Unit: "can", Text: "Süßkartoffeln", ArticleID: "sweet-potatoes"},
	}
	b.Base = 2
	rows := shop.Rows([]shop.Meal{a, b}, resolve)
	var weight, cans *shop.Row
	for i, r := range rows {
		if r.ArticleID == "sweet-potatoes" && r.Qty.Unit == "g" {
			weight = &rows[i]
		}
		if r.ArticleID == "sweet-potatoes" && r.Qty.Unit == "can" {
			cans = &rows[i]
		}
	}
	if weight == nil || cans == nil || weight.Key == cans.Key {
		t.Fatalf("a weight and a can of the same article are two rows: %+v", rows)
	}
	if weight.Qty.Base != 1600 || len(weight.Parts) != 2 || weight.Parts[0].Meal != a.Key || weight.Parts[1].Qty.Base != 1000 {
		t.Errorf("weight row = %+v", weight)
	}
}

// Acceptance: everything goes through list.PlanAdd, so nothing lands twice: the second run only sees what exists.
func TestPlanUsesPlanAddInOrderSoNothingLandsTwice(t *testing.T) {
	t.Parallel()
	rows := shop.Rows([]shop.Meal{curry()}, resolve)
	entries := []list.Entry{{ID: "e1", Text: "Süßkartoffeln", ArticleID: "sweet-potatoes", Qty: list.Quantity{Base: 500, Unit: "g"}}}
	steps := shop.Plan(entries, rows, show)
	got := map[string]list.AddAction{}
	for _, s := range steps {
		got[s.Row.Text] = s.Plan.Action
	}
	if got["Süßkartoffeln"] != list.AddedTo || got["Kokosmilch"] != list.AddedNew {
		t.Errorf("plan = %v", got)
	}
	if steps[0].Plan.Result.Base != 1100 {
		t.Errorf("500 g and 600 g come to %v", steps[0].Plan.Result)
	}
	if got["Erdnussbutter"] != list.AddedNew {
		t.Errorf("peanut butter: %v", got["Erdnussbutter"])
	}
	entries = append(entries, list.Entry{ID: "e2", Text: "Erdnussbutter", ArticleID: "peanut-butter"})
	for _, s := range shop.Plan(entries, rows, show) {
		if s.Row.Text == "Erdnussbutter" && s.Plan.Action != list.Exists {
			t.Errorf("an entry without quantity is enough for a kitchen measure: %v", s.Plan.Action)
		}
	}
}
