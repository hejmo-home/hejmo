package recipe_test

import (
	"reflect"
	"testing"

	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/recipe"
)

// Ticket 281: a recipe on the meal plan, scaled to its portions.

func TestScalingRoundsToOneDecimalNeverToWholeNumbers(t *testing.T) {
	for _, c := range []struct {
		amount  float64
		base, n int
		want    float64
	}{
		{250, 3, 8, 666.7},    // 666.666...
		{3, 3, 8, 8},          // eggs
		{1, 3, 8, 2.7},        // a pinch of salt: 2.666... is not 3
		{0.19, 3, 8, 0.5},     // 0.506...
		{300, 4, 6, 450},      // exact
		{2.5, 1, 1, 2.5},      // 2.5 stays, it is not rounded up to 3
		{1, 4, 6, 1.5},        // 1.5 stays
		{1, 8, 5, 0.6},        // 0.625
		{0.19, 3, 1, 0.1},     // 0.063 shows as 0.1, never 0
		{0.01, 99, 1, 0.1},    // the smallest quantity
		{9999, 1, 99, 989901}, // the largest ends in a whole number
		{0, 4, 8, 0},          // no quantity stays none
	} {
		if got := recipe.Scale(c.amount, c.base, c.n); got != c.want {
			t.Errorf("Scale(%v, %d, %d) = %v, want %v", c.amount, c.base, c.n, got, c.want)
		}
	}
}

func TestScaledQuantitiesUseTheDecimalOfTheLanguage(t *testing.T) {
	for _, c := range []struct {
		v      float64
		de, en string
	}{{666.7, "666,7", "666.7"}, {8, "8", "8"}, {0.1, "0,1", "0.1"}, {1333.3, "1333,3", "1333.3"}, {0.3, "0,3", "0.3"}} {
		if got := recipe.FormatScaled(c.v, "de"); got != c.de {
			t.Errorf("de %v = %q, want %q", c.v, got, c.de)
		}
		if got := recipe.FormatScaled(c.v, "en"); got != c.en {
			t.Errorf("en %v = %q, want %q", c.v, got, c.en)
		}
	}
}

func named(names ...string) []recipe.Recipe {
	var out []recipe.Recipe
	for _, n := range names {
		out = append(out, recipe.Recipe{ID: n, Name: n, Category: recipe.Main})
	}
	return out
}

func names(list []recipe.Recipe) []string {
	var out []string
	for _, r := range list {
		out = append(out, r.Name)
	}
	return out
}

func TestSuggestionsMatchAnywhereInTheNameIgnoringCaseAndUmlauts(t *testing.T) {
	list := named("Hähnchen-Curry mit Reis", "Erdnuss-Curry", "Kichererbsen-CURRY", "Nudelauflauf", "Gemüsesuppe")
	got := recipe.SuggestRecipes(list, "curry", "", 5)
	if want := []string{"Erdnuss-Curry", "Hähnchen-Curry mit Reis", "Kichererbsen-CURRY"}; !reflect.DeepEqual(names(got), want) {
		t.Errorf("curry = %v, want %v (A to Z)", names(got), want)
	}
	if got := names(recipe.SuggestRecipes(list, "haehn", "", 5)); len(got) != 0 {
		t.Errorf("ae is no umlaut spelling here: %v", got)
	}
	if got := names(recipe.SuggestRecipes(list, "HAHN", "", 5)); !reflect.DeepEqual(got, []string{"Hähnchen-Curry mit Reis"}) {
		t.Errorf("umlaut ignored: %v", got)
	}
	if got := names(recipe.SuggestRecipes(list, "gemuse", "", 5)); !reflect.DeepEqual(got, []string{"Gemüsesuppe"}) {
		t.Errorf("gemuse: %v", got)
	}
	if got := recipe.SuggestRecipes(list, "e", "", 5); len(got) != 0 {
		t.Errorf("one letter suggests nothing: %v", names(got))
	}
	if got := recipe.SuggestRecipes(list, "  r ", "", 5); len(got) != 0 {
		t.Errorf("spaces are no letters: %v", names(got))
	}
}

func TestSuggestionsPutRecipesThatFitTheMealtimeFirstAndStopAtFive(t *testing.T) {
	list := []recipe.Recipe{
		{ID: "a", Name: "Apfel-Pfannkuchen", Category: recipe.Main, Mealtimes: []string{"breakfast"}},
		{ID: "b", Name: "Bohnen-Pfanne", Category: recipe.Main, Mealtimes: []string{"lunch", "dinner"}},
		{ID: "c", Name: "Champignon-Pfanne", Category: recipe.Main}, // every mealtime
		{ID: "d", Name: "Dinkel-Pfanne", Category: recipe.Main, Mealtimes: []string{"dinner"}},
		{ID: "e", Name: "Eier-Pfanne", Category: recipe.Main, Mealtimes: []string{"breakfast"}},
		{ID: "f", Name: "Fenchel-Pfanne", Category: recipe.Main, Mealtimes: []string{"lunch"}},
		{ID: "g", Name: "Gnocchi-Pfanne", Category: recipe.Main, Mealtimes: []string{"dinner"}},
	}
	got := names(recipe.SuggestRecipes(list, "pfann", meal.Dinner, 5))
	want := []string{"Bohnen-Pfanne", "Champignon-Pfanne", "Dinkel-Pfanne", "Gnocchi-Pfanne", "Apfel-Pfannkuchen"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dinner = %v, want %v", got, want)
	}
	got = names(recipe.SuggestRecipes(list, "pfann", meal.Breakfast, 5))
	want = []string{"Apfel-Pfannkuchen", "Champignon-Pfanne", "Eier-Pfanne", "Bohnen-Pfanne", "Dinkel-Pfanne"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("breakfast = %v, want %v", got, want)
	}
}

func TestEarlierDishesAreDistinctMostRecentFirstWithoutRecipeNamesAndAtMostThree(t *testing.T) {
	rows := []recipe.Dish{
		{Text: "Papas Gemüsecurry mit Reis", Day: "2026-11-12"},
		{Text: "Curry vom Vortag", Day: "2026-11-20"},
		{Text: "papas gemüsecurry mit reis", Day: "2026-10-01"},      // the same dish, older
		{Text: "Erdnuss-Curry mit Süßkartoffeln", Day: "2026-11-22"}, // a recipe's name: the recipe is suggested
		{Text: "Currywurst", Day: "2026-09-01"},
		{Text: "Curry-Reste", Day: "2026-08-01"},
		{Text: "Nudeln", Day: "2026-11-23"},
	}
	got := recipe.EarlierDishes(rows, "curry", []string{"Erdnuss-Curry mit Süßkartoffeln"}, 3)
	want := []recipe.Dish{{Text: "Curry vom Vortag", Day: "2026-11-20"}, {Text: "Papas Gemüsecurry mit Reis", Day: "2026-11-12"}, {Text: "Currywurst", Day: "2026-09-01"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("earlier = %v, want %v", got, want)
	}
	if got := recipe.EarlierDishes(rows, "x", nil, 3); len(got) != 0 {
		t.Errorf("one letter: %v", got)
	}
}

// Ticket 287: favourites come first in the suggestions, then the mealtime, then A to Z.
func TestSuggestionsPutFavouritesFirstThenTheMealtimeThenAToZ(t *testing.T) {
	list := []recipe.Recipe{
		{ID: "a", Name: "Curry A"},
		{ID: "b", Name: "Curry B", Mealtimes: []string{"breakfast"}},
		{ID: "c", Name: "Curry C", Mealtimes: []string{"breakfast"}},
		{ID: "d", Name: "Curry D"},
		{ID: "e", Name: "Curry E"},
	}
	liked := map[string]bool{"c": true, "d": true}
	if got := names(recipe.SuggestRecipesLiked(list, "curry", meal.Dinner, 5, liked)); !reflect.DeepEqual(got, []string{"Curry D", "Curry C", "Curry A", "Curry E", "Curry B"}) {
		t.Errorf("dinner = %v: favourites first (those that fit dinner, then the rest), then the others the same way", got)
	}
	if got := names(recipe.SuggestRecipesLiked(list, "curry", meal.Dinner, 2, liked)); !reflect.DeepEqual(got, []string{"Curry D", "Curry C"}) {
		t.Errorf("limit 2 = %v: the limit cuts after the ordering, so favourites are never cut for others", got)
	}
	if got, want := names(recipe.SuggestRecipesLiked(list, "curry", meal.Dinner, 5, nil)), names(recipe.SuggestRecipes(list, "curry", meal.Dinner, 5)); !reflect.DeepEqual(got, want) {
		t.Errorf("without favourites = %v, want %v", got, want)
	}
}
