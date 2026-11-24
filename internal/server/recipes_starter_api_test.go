package server_test

import (
	"net/http"
	"testing"
)

// Ticket 290: the starter collection over the REST API (ADR 0015: parity with
// the pages), contract-checked against openapi.yaml.

func TestTheAPIListsTheStarterCollectionAndReadsOneRecipe(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	status, _, list := c.call(http.MethodGet, "/starter-recipes", nil)
	if status != http.StatusOK || len(list) != 200 {
		t.Fatalf("list = %d with %d recipes", status, len(list))
	}
	_, _, own := c.call(http.MethodGet, "/recipes", nil)
	if len(own) != 0 {
		t.Errorf("the family's own list holds %d starter recipes", len(own))
	}
	status, got, _ := c.call(http.MethodGet, "/recipes/starter-rahmspinat", nil)
	if status != http.StatusOK || got["name"] != "Rahmspinat" || got["starter"] != true || got["source_name"] != "Chefkoch" ||
		got["source_url"] != "https://www.chefkoch.de/rezepte/321101114007445/Rahmspinat.html" || got["tips"] != "Dazu passen Salzkartoffeln und Spiegelei." ||
		got["created_by"] != nil || got["created_at"] != nil || got["minutes"] != float64(20) || got["portions"] != float64(4) {
		t.Fatalf("get = %d %v", status, got)
	}
	if steps := got["steps"].([]any); len(steps) != 7 {
		t.Errorf("%d steps", len(steps))
	}
	ings := got["ingredients"].([]any)
	if butter := ings[1].(map[string]any); butter["article_id"] != "butter" || butter["quantity"] != float64(25) || butter["unit"] != "g" {
		t.Errorf("butter = %v", butter)
	}
	if flour := ings[2].(map[string]any); flour["unit"] != "tbsp" || flour["article_id"] != "wheat-flour" {
		t.Errorf("flour = %v", flour)
	}
	if status, _, list := c.call(http.MethodGet, "/starter-recipes?category=snack", nil); status != http.StatusOK || len(list) != 10 {
		t.Errorf("snacks = %d %d", status, len(list))
	}
	if status, _, list := c.call(http.MethodGet, "/starter-recipes?q=rahmspinat", nil); status != http.StatusOK || len(list) < 1 {
		t.Errorf("search = %d %d", status, len(list))
	}
	if status, _, _ := c.call(http.MethodGet, "/recipes/starter-gibt-es-nicht", nil); status != http.StatusNotFound {
		t.Errorf("unknown = %d", status)
	}
	if status, w, _ := c.call(http.MethodGet, "/recipes/starter-rahmspinat/warnings", nil); status != http.StatusOK || w["recipe_id"] != "starter-rahmspinat" {
		t.Errorf("warnings = %d %v", status, w)
	}
}

func TestAStarterRecipeIsNeverReplacedOrDeletedOverTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	if status, _, _ := c.call(http.MethodPut, "/recipes/starter-rahmspinat", curryBody()); status != http.StatusForbidden {
		t.Errorf("put = %d, want 403", status)
	}
	if status, _, _ := c.call(http.MethodDelete, "/recipes/starter-rahmspinat", nil); status != http.StatusForbidden {
		t.Errorf("delete = %d, want 403", status)
	}
	if status, _, _ := c.call(http.MethodGet, "/recipes/starter-rahmspinat", nil); status != http.StatusOK {
		t.Errorf("still there = %d", status)
	}
}
