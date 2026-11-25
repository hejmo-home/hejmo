package server_test

import (
	"net/http"
	"testing"
)

// Ticket 281 (ADR 0021): a meal plan entry over the REST API is free text or a
// recipe with portions, contract-checked against openapi.yaml.

// withFamily adds Sebastian, Mia and Papa to the signed-in parent: four in the household.
func withFamily(t *testing.T, c *apiClient) {
	t.Helper()
	for _, n := range []string{"Papa", "Sebastian", "Mia"} {
		if status, p, _ := c.call(http.MethodPost, "/people", map[string]any{"name": n, "role": "child"}); status != http.StatusCreated {
			t.Fatalf("add %s = %d %v", n, status, p)
		}
	}
}

func putRecipeMeal(c *apiClient, day, mealtime string, body map[string]any) (int, map[string]any) {
	status, m, _ := c.call(http.MethodPut, "/meals/"+day+"/"+mealtime, body)
	return status, m
}

// Acceptance: portions default to the household size.
func TestARecipeMealDefaultsItsPortionsToTheHouseholdSize(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	withFamily(t, c)
	_, r := createRecipe(t, c, curryBody())
	status, m := putRecipeMeal(c, tuesday, "dinner", map[string]any{"recipe_id": r["id"]})
	if status != http.StatusOK || m["recipe_id"] != r["id"] || m["portions"] != float64(4) || m["dish"] != r["name"] || m["text"] != r["name"] || m["source"] != "manual" {
		t.Fatalf("put = %d %v: the household has four people", status, m)
	}
	status, m = putRecipeMeal(c, tuesday, "lunch", map[string]any{"recipe_id": r["id"], "portions": 7})
	if status != http.StatusOK || m["portions"] != float64(7) {
		t.Fatalf("put with portions = %d %v", status, m)
	}
	list := mealsBetween(t, c, tuesday, tuesday)
	if len(list) != 2 || list[0]["mealtime"] != "lunch" || list[0]["portions"] != float64(7) || list[1]["portions"] != float64(4) || list[1]["dish"] != r["name"] {
		t.Fatalf("list = %v", list)
	}
}

// Acceptance: existing free-text entries keep working.
func TestAFreeTextMealHasNoRecipeNoPortionsAndADish(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	status, m := putMeal(t, c, tuesday, "dinner", "Nudelauflauf")
	if status != http.StatusOK || m["text"] != "Nudelauflauf" || m["dish"] != "Nudelauflauf" {
		t.Fatalf("put = %d %v", status, m)
	}
	if _, has := m["recipe_id"]; has {
		t.Errorf("a text entry has a recipe_id: %v", m)
	}
	if _, has := m["portions"]; has {
		t.Errorf("a text entry has portions: %v", m)
	}
}

func TestARecipeMealIsRefusedForBadInput(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, r := createRecipe(t, c, curryBody())
	for name, body := range map[string]map[string]any{
		"both":          {"text": "Suppe", "recipe_id": r["id"]},
		"neither":       {},
		"unknown":       {"recipe_id": "nope"},
		"zero portions": {"recipe_id": r["id"], "portions": 0},
		"100 portions":  {"recipe_id": r["id"], "portions": 100},
		"portions text": {"text": "Suppe", "portions": 3},
	} {
		raw, status, _ := c.callRaw(http.MethodPut, "/meals/"+tuesday+"/dinner", body)
		if status != http.StatusUnprocessableEntity && status != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 422", name, status, raw)
		}
	}
	if got := mealsBetween(t, c, tuesday, tuesday); len(got) != 0 {
		t.Errorf("refused saves planned %v", got)
	}
}

func TestRenamingARecipeRenamesItsMealAndDeletingLeavesTheNameAsText(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, r := createRecipe(t, c, curryBody())
	putRecipeMeal(c, tuesday, "dinner", map[string]any{"recipe_id": r["id"], "portions": 5})
	r["name"] = "Erdnuss-Curry scharf"
	if status, got, _ := c.call(http.MethodPut, "/recipes/"+r["id"].(string), r); status != http.StatusOK {
		t.Fatalf("rename = %d %v", status, got)
	}
	list := mealsBetween(t, c, tuesday, tuesday)
	if len(list) != 1 || list[0]["dish"] != "Erdnuss-Curry scharf" || list[0]["text"] != "Erdnuss-Curry scharf" || list[0]["portions"] != float64(5) {
		t.Fatalf("after the rename = %v", list)
	}
	occs := c.occurrences(t, "from=2026-11-17T00:00:00%2B01:00&to=2026-11-18T00:00:00%2B01:00")
	if got := occTitles(occs); len(got) != 1 || got[0] != "11-17 Abendessen: Erdnuss-Curry scharf" {
		t.Fatalf("calendar = %v: only the dish name", got)
	}
	if status, _, _ := c.call(http.MethodDelete, "/recipes/"+r["id"].(string), nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
	list = mealsBetween(t, c, tuesday, tuesday)
	if _, has := list[0]["recipe_id"]; len(list) != 1 || has || list[0]["dish"] != "Erdnuss-Curry scharf" {
		t.Fatalf("after the delete = %v", list)
	}
	if _, has := list[0]["portions"]; has {
		t.Errorf("free text with portions: %v", list[0])
	}
}
