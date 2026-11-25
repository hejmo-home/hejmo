package server_test

import (
	"html"
	"net/http"
	"strings"
	"testing"
)

// Ticket 282 (ADR 0015, 0021): the template form without script. A meal field
// whose text is the name of a recipe (ignoring case) is that recipe, as in the
// week of the meal plan; the form shows a recipe slot with the recipe's name.

func TestATemplateFieldThatEqualsARecipeNameHoldsThatRecipe(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	withFamily(t, c)
	_, r := createRecipe(t, c, curryBody())
	form := weekBForm("Weeks", "2026-11-16", "1")
	form.Set(tplField(2, "dinner"), strings.ToUpper(r["name"].(string)))
	if got := saveTemplate(t, c, form, en); got.status != http.StatusSeeOther {
		t.Fatalf("save = %d %q", got.status, got.body)
	}
	id := templateID(t, c, "Weeks")
	_, tpl, _ := c.call(http.MethodGet, "/meal-templates/"+id, nil)
	var found map[string]any
	for _, e := range tpl["entries"].([]any) {
		if m := e.(map[string]any); m["weekday"] == float64(2) {
			found = m
		}
	}
	if found["recipe_id"] != r["id"] || found["text"] != r["name"] {
		t.Fatalf("Tuesday = %v: the recipe, not the typed text", found)
	}
	list := mealsBetween(t, c, tuesday, tuesday)
	if len(list) != 1 || list[0]["recipe_id"] != r["id"] || list[0]["portions"] != float64(4) {
		t.Fatalf("plan = %v: four portions for the four people", list)
	}
	page := c.b.do(http.MethodGet, "/meals/templates/"+id, nil, en)
	if !strings.Contains(page.body, ">"+html.EscapeString(r["name"].(string))+"</textarea>") {
		t.Errorf("the form shows the recipe's name in its field")
	}
	if !strings.Contains(page.body, "name of a recipe") {
		t.Errorf("the form lacks the hint that a recipe's name plans the recipe")
	}
	if got := c.b.do(http.MethodPost, "/meals/templates/"+id, form, en); got.status != http.StatusSeeOther {
		t.Fatalf("saving the form again = %d", got.status)
	}
	_, again, _ := c.call(http.MethodGet, "/meal-templates/"+id, nil)
	if again["entries"].([]any)[1].(map[string]any)["recipe_id"] != r["id"] {
		t.Errorf("the slot is no longer the recipe after saving the form again: %v", again)
	}
}
