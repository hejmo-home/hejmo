package unit_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/hejmo-home/hejmo/internal/unit"
)

// The product owner's decision of 24/11/2026 (ticket 279): recipes offer the
// household's units plus kitchen measures and containers and piece measures.
func TestRecipesHaveKitchenMeasuresAndContainers(t *testing.T) {
	wantKitchen := []string{"tbsp", "tsp", "pinch", "knifetip", "dash", "splash", "handful", "cup"}
	if !reflect.DeepEqual(unit.Kitchen, wantKitchen) {
		t.Errorf("kitchen measures = %v", unit.Kitchen)
	}
	wantContainers := []string{"can", "packet", "tub", "bunch", "jar", "bottle", "stick", "clove", "slice", "cube", "sprig", "stalk", "ball", "leaf", "punnet", "pot"}
	if !reflect.DeepEqual(unit.Containers, wantContainers) {
		t.Errorf("containers = %v", unit.Containers)
	}
}

func TestKitchenMeasuresNeverConvertOrAddUpWithGramsOrMillilitres(t *testing.T) {
	for _, k := range unit.Kitchen {
		if !unit.IsKitchen(k) || unit.IsContainer(k) {
			t.Errorf("%s is a kitchen measure", k)
		}
		kind := unit.KindOf(k)
		if kind == "" || kind == unit.Weight || kind == unit.Volume {
			t.Errorf("%s has kind %q, which could add up with g or ml", k, kind)
		}
		for _, other := range append(slices.Clone(unit.All), unit.Kitchen...) {
			if other != k && unit.KindOf(other) == kind {
				t.Errorf("%s and %s share the kind %q", k, other, kind)
			}
		}
		if unit.KeepsQuantityOnList(k) {
			t.Errorf("%s goes onto a shopping list without a quantity", k)
		}
	}
}

func TestContainersKeepTheirQuantityAndAddUpOnlyWithThemselves(t *testing.T) {
	for _, k := range unit.Containers {
		if !unit.IsContainer(k) || unit.IsKitchen(k) {
			t.Errorf("%s is a container or piece measure", k)
		}
		if !unit.KeepsQuantityOnList(k) {
			t.Errorf("%s keeps its quantity on a shopping list", k)
		}
		for _, other := range slices.Concat(unit.All, unit.Kitchen, unit.Containers) {
			if other != k && unit.KindOf(other) == unit.KindOf(k) {
				t.Errorf("%s and %s share the kind %q", k, other, unit.KindOf(k))
			}
		}
	}
	for _, k := range unit.All {
		if !slices.Contains(unit.Containers, k) && !unit.KeepsQuantityOnList(k) {
			t.Errorf("household unit %s keeps its quantity", k)
		}
	}
}

func TestRecipeUnitsAreTheHouseholdsPlusBothGroups(t *testing.T) {
	got := unit.ForRecipe([]string{"piece", "g", "kg", "ml", "l", "pack", "bunch", "dozen"})
	want := []string{"piece", "dozen", "pack", "bunch", "g", "kg", "ml", "l",
		"tbsp", "tsp", "pinch", "knifetip", "dash", "splash", "handful", "cup",
		"can", "packet", "tub", "jar", "bottle", "stick", "clove", "slice", "cube", "sprig", "stalk", "ball", "leaf", "punnet", "pot"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("units for a recipe = %v", got)
	}
	// A household that switched bunch off still gets it: it is a container.
	if got := unit.ForRecipe([]string{"piece", "g", "ml"}); !slices.Contains(got, "bunch") {
		t.Errorf("bunch missing: %v", got)
	}
	for _, k := range got {
		if !unit.ValidForRecipe(k) {
			t.Errorf("%s is offered but not valid", k)
		}
	}
	if unit.ValidForRecipe("stone") || unit.ValidForRecipe("") || unit.Valid("tbsp") {
		t.Error("only recipes know kitchen measures; the lists' catalogue is unchanged")
	}
}
