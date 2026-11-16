package familyview_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/meal"
)

var planned = map[meal.Mealtime]string{
	meal.Breakfast: "Porridge", meal.Lunch: "Soup", meal.Snack: "Apple", meal.Dinner: "Pasta bake",
}

func buildMeals(now time.Time, lang string, meals map[meal.Mealtime]string, c meal.Cutoffs) familyview.View {
	return familyview.Build(familyview.Input{Now: now, Lang: lang, Region: "DE-BY", People: family(), I18n: i18n.MustLoad(), Meals: meals, MealCutoffs: c})
}

func dishes(v familyview.View) []string {
	var out []string
	for _, e := range v.Meal.Entries {
		out = append(out, string(e.Mealtime)+"="+e.Dish)
	}
	return out
}

// 6.4: every mealtime planned for today is shown, in the order of the day.
func TestMealTileListsTodaysMealsInTheOrderOfTheDay(t *testing.T) {
	v := buildMeals(at(14, 7, 15), "en", planned, meal.DefaultCutoffs())
	if v.Meal == nil || v.Meal.Label != "Meals today" {
		t.Fatalf("tile = %+v", v.Meal)
	}
	got := dishes(v)
	want := []string{"breakfast=Porridge", "lunch=Soup", "snack=Apple", "dinner=Pasta bake"}
	if len(got) != 4 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Errorf("entries = %v", got)
	}
	if de := buildMeals(at(14, 7, 15), "de", planned, meal.DefaultCutoffs()); de.Meal.Label != "Essen heute" {
		t.Errorf("de label = %q", de.Meal.Label)
	}
}

// 6.4: a mealtime past its cut-off is gone; one mealtime left is labelled with its name.
func TestMealTileDropsMealtimesPastTheirCutoff(t *testing.T) {
	v := buildMeals(at(14, 14, 0), "en", planned, meal.DefaultCutoffs())
	if got := dishes(v); len(got) != 2 || got[0] != "snack=Apple" || got[1] != "dinner=Pasta bake" {
		t.Errorf("14:00 entries = %v", got)
	}
	if v := buildMeals(at(14, 13, 29), "en", planned, meal.DefaultCutoffs()); len(v.Meal.Entries) != 3 {
		t.Errorf("13:29 keeps lunch, got %v", dishes(v))
	}
	if v := buildMeals(at(14, 13, 30), "en", planned, meal.DefaultCutoffs()); len(v.Meal.Entries) != 2 {
		t.Errorf("at the cut-off minute lunch is gone, got %v", dishes(v))
	}
	one := buildMeals(at(14, 18, 30), "en", planned, meal.DefaultCutoffs())
	if len(one.Meal.Entries) != 1 || one.Meal.Label != "Dinner" || one.Meal.Entries[0].Dish != "Pasta bake" {
		t.Errorf("one left = %+v", one.Meal)
	}
	if de := buildMeals(at(14, 18, 30), "de", planned, meal.DefaultCutoffs()); de.Meal.Label != "Abendessen" {
		t.Errorf("de label = %q", de.Meal.Label)
	}
}

// 6.4: nothing left, or nothing planned: no tile and no placeholder.
func TestMealTileIsNilWhenNothingIsLeft(t *testing.T) {
	c := meal.DefaultCutoffs().With(meal.Dinner, "20:00")
	if v := buildMeals(at(14, 21, 0), "en", planned, c); v.Meal != nil {
		t.Errorf("after every cut-off: %+v", v.Meal)
	}
	if v := buildMeals(at(14, 8, 0), "en", nil, c); v.Meal != nil {
		t.Errorf("nothing planned: %+v", v.Meal)
	}
	if v := buildMeals(at(14, 8, 0), "en", map[meal.Mealtime]string{meal.Lunch: ""}, c); v.Meal != nil {
		t.Errorf("empty entry: %+v", v.Meal)
	}
}
