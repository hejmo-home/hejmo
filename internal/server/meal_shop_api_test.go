package server_test

import (
	"net/http"
	"testing"
)

// Ticket 283 over the REST API, contract-checked against openapi.yaml: look
// first (preview), then add with the recipe versions, and take a run back.

const apiToday = "2026-11-14"

func shopAPIBox(t *testing.T) (*apiClient, map[string]any, string) {
	t.Helper()
	c := signedInAPI(t)
	_, r := createRecipe(t, c, curryBody())
	if status, m := putRecipeMeal(c, apiToday, "dinner", map[string]any{"recipe_id": r["id"], "portions": 8}); status != http.StatusOK {
		t.Fatalf("plan = %d %v", status, m)
	}
	_, _, lists := c.call(http.MethodGet, "/lists", nil)
	for _, l := range lists {
		if m := l.(map[string]any); m["suggestions"] == true {
			return c, r, m["id"].(string)
		}
	}
	t.Fatal("no list with suggestions")
	return nil, nil, ""
}

func rowsByName(p map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, r := range p["rows"].([]any) {
		m := r.(map[string]any)
		out[m["name"].(string)] = m
	}
	return out
}

func TestThePreviewScalesAndSaysWhatAddingDoes(t *testing.T) {
	t.Parallel()
	c, _, listID := shopAPIBox(t)
	status, p, _ := c.call(http.MethodGet, "/meal-shopping?meal="+apiToday+".dinner&list_id="+listID, nil)
	if status != http.StatusOK {
		t.Fatalf("preview = %d %v", status, p)
	}
	rows := rowsByName(p)
	if r := rows["Süßkartoffeln"]; r == nil || r["amount"] != 1.2 || r["unit"] != "kg" || r["outcome"] != "new" || r["group"] != "new" || r["tick"] != true {
		t.Errorf("sweet potatoes = %v", r)
	}
	if r := rows["Currypaste"]; r == nil || r["amount"] != nil || r["kitchen"] == "" {
		t.Errorf("a tablespoon has no quantity: %v", r)
	}
	if r := rows["Tomatoes"]; r == nil && rows["Tomaten"] == nil {
		t.Errorf("rows = %v", rows)
	}
	if rows["Salz"] != nil && rows["Salz"]["tick"] != false {
		t.Errorf("salt is pantry: %v", rows["Salz"])
	}
	if status, _, _ := c.call(http.MethodGet, "/meal-shopping", nil); status != http.StatusUnprocessableEntity {
		t.Errorf("no selection = %d", status)
	}
}

func TestAddingIsOnceAndUndoTakesItBack(t *testing.T) {
	t.Parallel()
	c, r, listID := shopAPIBox(t)
	_, p, _ := c.call(http.MethodGet, "/meal-shopping?meal="+apiToday+".dinner&list_id="+listID, nil)
	body := map[string]any{"meal": apiToday + ".dinner", "list_id": listID, "versions": p["versions"]}
	status, run, _ := c.call(http.MethodPost, "/meal-shopping", body)
	if status != http.StatusCreated || len(run["added"].([]any)) == 0 {
		t.Fatalf("add = %d %v", status, run)
	}
	_, l, _ := c.call(http.MethodGet, "/lists/"+listID, nil)
	entries := l["entries"].([]any)
	found := false
	for _, e := range entries {
		m := e.(map[string]any)
		if m["amount"] == 1.2 && m["unit"] == "kg" {
			src := m["sources"].([]any)
			found = len(src) == 1 && src[0].(map[string]any)["recipe_id"] == r["id"] && src[0].(map[string]any)["date"] == apiToday
		}
	}
	if !found {
		t.Errorf("an entry says which meal brought it: %v", entries)
	}
	if status, _, _ := c.call(http.MethodPost, "/meal-shopping", body); status != http.StatusUnprocessableEntity {
		t.Errorf("the same meal again = %d: nothing lands twice", status)
	}
	_, l2, _ := c.call(http.MethodGet, "/lists/"+listID, nil)
	if len(l2["entries"].([]any)) != len(entries) {
		t.Error("the second add changed the list")
	}
	status, u, _ := c.call(http.MethodPost, "/meal-shopping/runs/"+run["run_id"].(string)+"/undo", nil)
	if status != http.StatusOK || len(u["removed"].([]any)) == 0 {
		t.Fatalf("undo = %d %v", status, u)
	}
	_, l3, _ := c.call(http.MethodGet, "/lists/"+listID, nil)
	if n := len(l3["entries"].([]any)); n != 0 {
		t.Errorf("after undo %d entries", n)
	}
	if status, _, _ := c.call(http.MethodPost, "/meal-shopping/runs/"+run["run_id"].(string)+"/undo", nil); status != http.StatusConflict {
		t.Errorf("undo twice = %d", status)
	}
	if status, _, _ := c.call(http.MethodPost, "/meal-shopping/runs/nope/undo", nil); status != http.StatusNotFound {
		t.Errorf("unknown run = %d", status)
	}
}

// Acceptance: a recipe that changed since the versions were read adds nothing.
func TestAChangedRecipeIsRefusedWith409(t *testing.T) {
	t.Parallel()
	c, r, listID := shopAPIBox(t)
	_, p, _ := c.call(http.MethodGet, "/meal-shopping?meal="+apiToday+".dinner&list_id="+listID, nil)
	r["ingredients"] = append(r["ingredients"].([]any), ingredient(2, "", "Limetten"))
	if status, got, _ := c.call(http.MethodPut, "/recipes/"+r["id"].(string), r); status != http.StatusOK {
		t.Fatalf("edit = %d %v", status, got)
	}
	status, got, _ := c.call(http.MethodPost, "/meal-shopping", map[string]any{"meal": apiToday + ".dinner", "list_id": listID, "versions": p["versions"]})
	if status != http.StatusConflict || len(got["changed"].([]any)) != 1 {
		t.Fatalf("add = %d %v", status, got)
	}
	_, l, _ := c.call(http.MethodGet, "/lists/"+listID, nil)
	if len(l["entries"].([]any)) != 0 {
		t.Error("something was added")
	}
}

// Ticket 284 over the REST API: a recipe that shrinks or leaves the plan takes its quantities back.
func TestAMealThatShrinksOrLeavesTheListTakesItsQuantitiesBack(t *testing.T) {
	t.Parallel()
	c, r, listID := shopAPIBox(t)
	_, p, _ := c.call(http.MethodGet, "/meal-shopping?meal="+apiToday+".dinner&list_id="+listID, nil)
	if status, run, _ := c.call(http.MethodPost, "/meal-shopping", map[string]any{"meal": apiToday + ".dinner", "list_id": listID, "versions": p["versions"]}); status != http.StatusCreated {
		t.Fatalf("add = %d %v", status, run)
	}
	potatoes := func() (float64, int) {
		_, l, _ := c.call(http.MethodGet, "/lists/"+listID, nil)
		entries := l["entries"].([]any)
		for _, e := range entries {
			if m := e.(map[string]any); m["unit"] == "kg" || m["unit"] == "g" {
				if a, ok := m["amount"].(float64); ok && m["text"] == "Süßkartoffeln" {
					if m["unit"] == "kg" {
						a *= 1000
					}
					return a, len(entries)
				}
			}
		}
		return 0, len(entries)
	}
	if a, _ := potatoes(); a != 1200 {
		t.Fatalf("sweet potatoes for 8 portions = %v g", a)
	}
	if status, m := putRecipeMeal(c, apiToday, "dinner", map[string]any{"recipe_id": r["id"], "portions": 4}); status != http.StatusOK {
		t.Fatalf("shrink = %d %v", status, m)
	}
	if a, _ := potatoes(); a != 600 {
		t.Errorf("sweet potatoes for 4 portions = %v g", a)
	}
	_, p2, _ := c.call(http.MethodGet, "/meal-shopping?meal="+apiToday+".dinner&list_id="+listID, nil)
	ms, _ := p2["meals"].([]any)
	if len(ms) != 1 {
		t.Fatalf("one meal in the preview: %v", p2)
	}
	if m := ms[0].(map[string]any); m["portions"] != 4.0 || m["on_list"] != 4.0 || m["to_add"] != 0.0 || m["status"] != "done" {
		t.Errorf("4 portions are planned and on the list, nothing is missing: %v", m)
	}
	if status, m, _ := c.call(http.MethodDelete, "/meals/"+apiToday+"/dinner", nil); status != http.StatusNoContent {
		t.Fatalf("remove = %d %v", status, m)
	}
	if a, n := potatoes(); a != 0 || n != 0 {
		t.Errorf("after the recipe left: %v g, %d entries", a, n)
	}
}
