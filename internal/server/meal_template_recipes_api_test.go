package server_test

import (
	"net/http"
	"testing"
)

// Ticket 282 (ADR 0021): a meal plan template slot over the REST API is free
// text or a recipe, contract-checked against openapi.yaml.

func curryTemplate(recipeID any) map[string]any {
	in := dinnerWeek("Weeks", weekStart, 1)
	in["entries"] = []any{
		map[string]any{"weekday": 1, "mealtime": "dinner", "text": "Lentil soup"},
		map[string]any{"weekday": 2, "mealtime": "dinner", "recipe_id": recipeID},
	}
	return in
}

// Acceptance: a template slot can hold a recipe.
func TestATemplateSlotCanHoldARecipeOverTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, r := createRecipe(t, c, curryBody())
	status, tpl := createTemplate(t, c, curryTemplate(r["id"]))
	if status != http.StatusCreated {
		t.Fatalf("create = %d %v", status, tpl)
	}
	entries := tpl["entries"].([]any)
	soup, curry := entries[0].(map[string]any), entries[1].(map[string]any)
	if soup["text"] != "Lentil soup" || soup["recipe_id"] != nil || curry["recipe_id"] != r["id"] || curry["text"] != r["name"] {
		t.Fatalf("entries = %v: a recipe slot has its recipe and the recipe's name as text", entries)
	}
	status, got, _ := c.call(http.MethodGet, "/meal-templates/"+tpl["id"].(string), nil)
	if status != http.StatusOK || got["entries"].([]any)[1].(map[string]any)["recipe_id"] != r["id"] {
		t.Fatalf("get = %d %v", status, got)
	}
}

// Acceptance: applying the template creates recipe entries with household-size portions.
func TestApplyingATemplateWithARecipePlansItWithHouseholdSizePortions(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	withFamily(t, c)
	_, r := createRecipe(t, c, curryBody())
	if status, tpl := createTemplate(t, c, curryTemplate(r["id"])); status != http.StatusCreated {
		t.Fatalf("create = %d %v", status, tpl)
	}
	list := mealsBetween(t, c, tuesday, tuesday)
	if len(list) != 1 {
		t.Fatalf("list = %v", list)
	}
	m := list[0]
	if m["recipe_id"] != r["id"] || m["portions"] != float64(4) || m["dish"] != r["name"] || m["source"] != "template" {
		t.Fatalf("meal = %v: the recipe for the four people of the household, from the template", m)
	}
}

// Acceptance: deleting a recipe leaves the template slot as its name in free text.
func TestDeletingARecipeLeavesTheTemplateSlotAsItsNameInFreeTextOverTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, r := createRecipe(t, c, curryBody())
	_, tpl := createTemplate(t, c, curryTemplate(r["id"]))
	if status, _, _ := c.call(http.MethodDelete, "/recipes/"+r["id"].(string), nil); status != http.StatusNoContent {
		t.Fatalf("delete recipe = %d", status)
	}
	_, got, _ := c.call(http.MethodGet, "/meal-templates/"+tpl["id"].(string), nil)
	curry := got["entries"].([]any)[1].(map[string]any)
	if curry["text"] != r["name"] || curry["recipe_id"] != nil {
		t.Fatalf("slot = %v: the recipe's name in free text", curry)
	}
	list := mealsBetween(t, c, tuesday, tuesday)
	if len(list) != 1 || list[0]["dish"] != r["name"] || list[0]["recipe_id"] != nil || list[0]["portions"] != nil {
		t.Fatalf("meal = %v", list)
	}
}

func TestATemplateSlotWithAnUnknownRecipeOrBothTextAndRecipeIsRefused(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, r := createRecipe(t, c, curryBody())
	if status, body := createTemplate(t, c, curryTemplate("nope")); status != http.StatusUnprocessableEntity {
		t.Errorf("unknown recipe = %d %v", status, body)
	}
	both := curryTemplate(r["id"])
	both["entries"].([]any)[1].(map[string]any)["text"] = "Curry"
	if status, body := createTemplate(t, c, both); status != http.StatusUnprocessableEntity {
		t.Errorf("text and recipe = %d %v", status, body)
	}
}
