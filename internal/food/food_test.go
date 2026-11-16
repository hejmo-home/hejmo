package food_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/food"
)

func TestThereAreFourteenAllergensAndFourDiets(t *testing.T) {
	if n := len(food.Allergens); n != 14 {
		t.Errorf("%d allergens, want the 14 of EU regulation 1169/2011 annex II", n)
	}
	if n := len(food.Diets); n != 4 || food.Diets[0] != food.Omnivore {
		t.Errorf("diets = %v, want four with omnivore first", food.Diets)
	}
	if !food.ValidDiet("vegan") || food.ValidDiet("keto") || food.ValidDiet("") {
		t.Error("ValidDiet accepts the four diets only")
	}
}

func TestNormalizeAllergensKeepsKnownOnesOnceInOrder(t *testing.T) {
	got, ok := food.NormalizeAllergens([]string{"soy", "eggs", "soy"})
	if !ok || !reflect.DeepEqual(got, []string{"eggs", "soy"}) {
		t.Errorf("got %v %v", got, ok)
	}
	if _, ok := food.NormalizeAllergens([]string{"eggs", "kiwi"}); ok {
		t.Error("an unknown allergen was accepted")
	}
}

func TestOtherIsSplitAtCommasTrimmedAndDeduplicated(t *testing.T) {
	items, c := food.ParseOther(" Kiwi, ,Erdbeeren ,kiwi")
	if c != nil || !reflect.DeepEqual(items, []string{"Kiwi", "Erdbeeren"}) {
		t.Errorf("got %v %v", items, c)
	}
}

func TestOtherNamingOneOfTheFourteenIsRefusedWithTheCard(t *testing.T) {
	for in, want := range map[string]string{
		"Laktose, Kiwi": "milk", "gluten": "gluten", "Nüsse": "nuts", "Ei": "eggs",
		"Peanuts": "peanuts", "Soja": "soy", "Weichtiere": "molluscs", "Schalenfrüchte (Nüsse)": "nuts",
	} {
		_, c := food.ParseOther(in)
		if c == nil || c.Allergen != want {
			t.Errorf("%q: conflict = %+v, want %s", in, c, want)
		}
	}
	if _, c := food.ParseOther("Laktose, Kiwi"); c == nil || c.Word != "Laktose" {
		t.Errorf("the word as typed is named: %+v", c)
	}
}

func TestSingleKindsAreAllowed(t *testing.T) {
	for _, in := range []string{"Haselnuss", "Erdbeeren", "Kiwi, Pfirsich", "Weizen"} {
		if _, c := food.ParseOther(in); c != nil {
			t.Errorf("%q refused: %+v", in, c)
		}
	}
}

func TestOtherHasAMaximumOf200Characters(t *testing.T) {
	if !food.OtherFits(strings.Repeat("ä", 200)) || food.OtherFits(strings.Repeat("ä", 201)) {
		t.Error("200 characters (not bytes) is the limit")
	}
}
