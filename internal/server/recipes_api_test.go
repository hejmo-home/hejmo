package server_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/server"
)

// Ticket 279 (ADR 0019, 0021; ADR 0015: HTML routes with API parity): own
// recipes over the REST API, contract-checked against openapi.yaml.

func ingredient(quantity any, unit, name string) map[string]any {
	in := map[string]any{"name": name}
	if quantity != nil {
		in["quantity"] = quantity
	}
	if unit != "" {
		in["unit"] = unit
	}
	return in
}

func curryBody() map[string]any {
	return map[string]any{
		"name": "Erdnuss-Curry mit Süßkartoffeln", "category": "main", "portions": 4, "minutes": 45,
		"mealtimes": []string{"dinner", "lunch"}, "tags": []string{"schnell", "scharf"},
		"tips": "Schmeckt am nächsten Tag besser.", "source": "Kochbuch „Asiatisch für jeden Tag“, Seite 84",
		"ingredients": []any{
			ingredient(600, "g", "Süßkartoffeln"),
			ingredient(2, "", "Knoblauch Zehen"),
			ingredient(3, "tbsp", "Currypaste"),
			ingredient(3, "tbsp", "Erdnussbutter"),
			ingredient(nil, "", "Salz"),
			ingredient(2, "can", "Tomaten"),
			ingredient(1, "tsp", "Sambal Oelek"),
		},
		"steps": []string{"Süßkartoffeln schälen.", "Currypaste anrösten.", "Köcheln lassen."},
	}
}

func createRecipe(t *testing.T, c *apiClient, in map[string]any) (int, map[string]any) {
	t.Helper()
	status, body, _ := c.call(http.MethodPost, "/recipes", in)
	return status, body
}

// Acceptance: steps are stored as an ordered list, not inside notes (Y).
func TestStepsAreAnOrderedListAndTipsStayApart(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	status, r := createRecipe(t, c, curryBody())
	if status != http.StatusCreated {
		t.Fatalf("create = %d %v", status, r)
	}
	steps := r["steps"].([]any)
	if len(steps) != 3 || steps[0] != "Süßkartoffeln schälen." || steps[2] != "Köcheln lassen." {
		t.Errorf("steps = %v", steps)
	}
	if r["tips"] != "Schmeckt am nächsten Tag besser." || strings.Contains(r["tips"].(string), "schälen") {
		t.Errorf("tips = %v", r["tips"])
	}
	in := curryBody()
	in["steps"] = []string{"Köcheln lassen.", "Süßkartoffeln schälen.", "", "Servieren."}
	status, got, _ := c.call(http.MethodPut, "/recipes/"+r["id"].(string), in)
	steps = got["steps"].([]any)
	if status != http.StatusOK || len(steps) != 3 || steps[0] != "Köcheln lassen." || steps[2] != "Servieren." {
		t.Errorf("reordered = %d %v: the order is kept and the empty step is not saved", status, got["steps"])
	}
}

func TestARecipeIsCreatedReadChangedAndDeletedOverTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	status, r := createRecipe(t, c, curryBody())
	id, _ := r["id"].(string)
	if status != http.StatusCreated || id == "" || r["name"] != "Erdnuss-Curry mit Süßkartoffeln" || r["category"] != "main" || r["portions"] != float64(4) || r["minutes"] != float64(45) {
		t.Fatalf("create = %d %v", status, r)
	}
	if r["created_by"] == nil || r["created_at"] == nil || r["updated_at"] == nil || r["source_url"] != nil {
		t.Errorf("who and when: %v", r)
	}
	if times := r["mealtimes"].([]any); len(times) != 2 || times[0] != "lunch" || times[1] != "dinner" {
		t.Errorf("mealtimes = %v: in the order of the day", times)
	}
	status, got, _ := c.call(http.MethodGet, "/recipes/"+id, nil)
	if status != http.StatusOK || got["name"] != r["name"] || len(got["ingredients"].([]any)) != 7 {
		t.Fatalf("get = %d %v", status, got)
	}
	if _, _, list := c.call(http.MethodGet, "/recipes", nil); len(list) != 1 {
		t.Fatalf("list = %v", list)
	}
	in := curryBody()
	in["name"], in["portions"], in["source"] = "Erdnuss-Curry", 6, "https://www.beispiel.de/curry"
	status, got, _ = c.call(http.MethodPut, "/recipes/"+id, in)
	if status != http.StatusOK || got["name"] != "Erdnuss-Curry" || got["portions"] != float64(6) || got["source_url"] != "https://www.beispiel.de/curry" {
		t.Fatalf("put = %d %v: only a web address is a link", status, got)
	}
	if status, _, _ := c.call(http.MethodDelete, "/recipes/"+id, nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
	if status, _, _ := c.call(http.MethodGet, "/recipes/"+id, nil); status != http.StatusNotFound {
		t.Fatalf("get after delete = %d", status)
	}
	if status, _, _ := c.call(http.MethodDelete, "/recipes/"+id, nil); status != http.StatusNotFound {
		t.Fatalf("delete again = %d", status)
	}
}

func TestAnIngredientIsAnArticleWithAVarietyOrFreeText(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, r := createRecipe(t, c, curryBody())
	ings := r["ingredients"].([]any)
	at := func(i int) map[string]any { return ings[i].(map[string]any) }
	if at(0)["article_id"] != "sweet-potatoes" || at(0)["quantity"] != float64(600) || at(0)["unit"] != "g" || at(0)["variety"] != nil {
		t.Errorf("an article: %v", at(0))
	}
	if at(1)["article_id"] != "garlic" || at(1)["name"] != "Knoblauch" || at(1)["variety"] != "Zehen" {
		t.Errorf("Knoblauch Zehen is garlic, variety Zehen: %v", at(1))
	}
	if at(2)["article_id"] != nil || at(2)["name"] != "Currypaste" || at(2)["variety"] != nil || at(2)["unit"] != "tbsp" {
		t.Errorf("free text has no article: %v", at(2))
	}
	if at(4)["quantity"] != nil || at(4)["unit"] != nil || at(4)["article_id"] != "salt" {
		t.Errorf("salt without a quantity: %v", at(4))
	}
	if at(5)["unit"] != "can" || at(5)["quantity"] != float64(2) {
		t.Errorf("a container keeps its quantity: %v", at(5))
	}
	in := curryBody()
	in["ingredients"] = []any{map[string]any{"name": "Mehl", "article_id": "wheat-flour", "variety": "Type 405", "quantity": 500, "unit": "g"}}
	_, r = createRecipe(t, c, map[string]any{"name": "Mit Sorte", "ingredients": in["ingredients"]})
	if a := r["ingredients"].([]any)[0].(map[string]any); a["article_id"] != "wheat-flour" || a["name"] != "Weizenmehl" || a["variety"] != "Type 405" {
		t.Errorf("a chosen article and variety: %v", a)
	}
}

func TestAnArticleIdWithANameEndingInAVarietyKeepsTheVariety(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, r := createRecipe(t, c, map[string]any{"name": "Mit Sorte im Namen", "ingredients": []any{map[string]any{"name": "Erdnussbutter cremig", "article_id": "peanut-butter"}}})
	if a := r["ingredients"].([]any)[0].(map[string]any); a["article_id"] != "peanut-butter" || a["variety"] != "cremig" {
		t.Errorf("the variety of the name is kept: %v", a)
	}
}

func TestRecipeUnitsAreTheHouseholdsPlusKitchenMeasuresAndContainers(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	for _, u := range []string{"g", "piece", "tbsp", "tsp", "pinch", "knifetip", "dash", "splash", "handful", "cup", "can", "packet", "tub", "bunch", "jar", "bottle", "stick", "clove", "slice", "cube", "sprig", "stalk", "ball", "leaf", "punnet"} {
		in := map[string]any{"name": "Probe " + u, "ingredients": []any{ingredient(1, u, "Wasser")}}
		if status, r := createRecipe(t, c, in); status != http.StatusCreated || r["ingredients"].([]any)[0].(map[string]any)["unit"] != u {
			t.Errorf("unit %s = %d %v", u, status, r)
		}
	}
	status, body := createRecipe(t, c, map[string]any{"name": "Falsch", "ingredients": []any{ingredient(1, "stone", "Wasser")}})
	if status != http.StatusUnprocessableEntity || body["field"] != "ingredients[0].unit" {
		t.Errorf("unknown unit = %d %v", status, body)
	}
	_, _, units := c.call(http.MethodGet, "/recipe-units", nil)
	if len(units) < 30 || units[len(units)-1] != "punnet" {
		t.Errorf("units offered = %v", units)
	}
}

func TestARecipeThatBreaksTheRulesIsRefusedWithItsField(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	for name, c2 := range map[string]struct {
		mut   func(map[string]any)
		field string
	}{
		"no name":                 {func(m map[string]any) { m["name"] = " " }, "name"},
		"a quantity needs a name": {func(m map[string]any) { m["ingredients"] = []any{ingredient(2, "g", " ")} }, "ingredients[0].name"},
		"unknown article":         {func(m map[string]any) { m["ingredients"] = []any{map[string]any{"name": "x", "article_id": "nope"}} }, "ingredients[0].article_id"},
	} {
		in := map[string]any{"name": "Probe", "category": "main", "portions": 4}
		c2.mut(in)
		status, body := createRecipe(t, c, in)
		if (status != http.StatusUnprocessableEntity && status != http.StatusBadRequest) || (status == http.StatusUnprocessableEntity && body["field"] != c2.field) {
			t.Errorf("%s = %d %v, want 422 naming %s", name, status, body, c2.field)
		}
	}
	if _, _, list := c.call(http.MethodGet, "/recipes", nil); len(list) != 0 {
		t.Errorf("a refused recipe was stored: %v", list)
	}
}

func TestAQuantityTooSmallToShowIsRefused(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	status, body := createRecipe(t, c, map[string]any{"name": "Winzig", "ingredients": []any{ingredient(0.001, "g", "Salz")}})
	if status != http.StatusUnprocessableEntity || body["field"] != "ingredients[0].amount" {
		t.Errorf("0.001 = %d %v, want 422 naming the quantity", status, body)
	}
}

func TestANameIsTakenIgnoringCase(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, first := createRecipe(t, c, map[string]any{"name": "Pfannkuchen"})
	status, body := createRecipe(t, c, map[string]any{"name": "pfannkuchen"})
	if status != http.StatusUnprocessableEntity || body["field"] != "name" || !strings.Contains(body["error"].(string), first["id"].(string)) {
		t.Errorf("second = %d %v: names the existing recipe", status, body)
	}
}

func TestTheListSearchesNameIngredientAndTagAndFiltersByCategory(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	createRecipe(t, c, curryBody())
	createRecipe(t, c, map[string]any{"name": "Apfelkuchen", "category": "baking", "tags": []string{"Sonntag"}})
	names := func(q string) []string {
		_, _, list := c.call(http.MethodGet, "/recipes"+q, nil)
		var out []string
		for _, x := range list {
			out = append(out, x.(map[string]any)["name"].(string))
		}
		return out
	}
	if got := names(""); strings.Join(got, "|") != "Apfelkuchen|Erdnuss-Curry mit Süßkartoffeln" {
		t.Errorf("A to Z = %v", got)
	}
	for q, want := range map[string]string{"?q=curry": "Erdnuss-Curry mit Süßkartoffeln", "?q=erdnussbutter": "Erdnuss-Curry mit Süßkartoffeln", "?q=sonntag": "Apfelkuchen", "?category=baking": "Apfelkuchen", "?q=zzz": ""} {
		if got := strings.Join(names(q), "|"); got != want {
			t.Errorf("%s = %q, want %q", q, got, want)
		}
	}
}

// Acceptance: children can open recipes but not change them.
func TestChildrenReadRecipesButCannotChangeThem(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	_, r := createRecipe(t, tb.parent, curryBody())
	id := r["id"].(string)
	if status, _, list := tb.child.call(http.MethodGet, "/recipes", nil); status != http.StatusOK || len(list) != 1 {
		t.Errorf("child list = %d %v", status, list)
	}
	if status, _, _ := tb.child.call(http.MethodGet, "/recipes/"+id, nil); status != http.StatusOK {
		t.Errorf("child get = %d", status)
	}
	if status, _ := createRecipe(t, tb.child, map[string]any{"name": "Eis"}); status != http.StatusForbidden {
		t.Errorf("child create = %d, want 403", status)
	}
	if status, _, _ := tb.child.call(http.MethodPut, "/recipes/"+id, curryBody()); status != http.StatusForbidden {
		t.Errorf("child put = %d, want 403", status)
	}
	if status, _, _ := tb.child.call(http.MethodDelete, "/recipes/"+id, nil); status != http.StatusForbidden {
		t.Errorf("child delete = %d, want 403", status)
	}
	if status, _, _ := tb.parent.call(http.MethodGet, "/recipes/"+id, nil); status != http.StatusOK {
		t.Errorf("the recipe is still there: %d", status)
	}
}

func TestMealScopesAlsoCoverRecipes(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	createRecipe(t, c, curryBody())
	read := c.withToken(c.newToken("meals:read"))
	if status, _, list := read.call(http.MethodGet, "/recipes", nil); status != http.StatusOK || len(list) != 1 {
		t.Errorf("meals:read = %d %v", status, list)
	}
	if status, _ := createRecipe(t, read, map[string]any{"name": "Eis"}); status != http.StatusForbidden {
		t.Errorf("meals:read create = %d", status)
	}
	if status, _, _ := c.withToken(c.newToken("tasks")).call(http.MethodGet, "/recipes", nil); status != http.StatusForbidden {
		t.Errorf("tasks token = %d", status)
	}
}

// Acceptance: warnings name the person and never block; free text is "not checked automatically".
func TestWarningsNameThePersonAndNeverBlockSaving(t *testing.T) {
	t.Parallel()
	c, mia := newArticleAPI(t)
	if status, _, _ := c.call(http.MethodPut, "/people/"+mia+"/food", map[string]any{"diet": "omnivore", "allergens": []string{"peanuts"}, "other": []string{"Kiwi"}}); status != http.StatusOK {
		t.Fatalf("food = %d", status)
	}
	status, r := createRecipe(t, c, curryBody())
	if status != http.StatusCreated {
		t.Fatalf("a recipe with peanuts for a child allergic to them is saved: %d %v", status, r)
	}
	status, w, _ := c.call(http.MethodGet, "/recipes/"+r["id"].(string)+"/warnings", nil)
	if status != http.StatusOK {
		t.Fatalf("warnings = %d %v", status, w)
	}
	ws := w["warnings"].([]any)
	if len(ws) != 1 || ws[0].(map[string]any)["allergen"] != "peanuts" || ws[0].(map[string]any)["people"].([]any)[0] != mia {
		t.Errorf("warnings = %v", ws)
	}
	if un := w["not_checked_ingredients"].([]any); len(un) != 2 || un[0] != "Currypaste" || un[1] != "Sambal Oelek" {
		t.Errorf("free-text ingredients are named: %v", un)
	}
	free := w["not_checked_allergies"].([]any)
	if len(free) != 1 || free[0].(map[string]any)["person_id"] != mia || free[0].(map[string]any)["text"] != "Kiwi" {
		t.Errorf("free-text allergies: %v", w["not_checked_allergies"])
	}
	if w["note"] == nil {
		t.Errorf("the answer says what Hejmo cannot check: %v", w)
	}
	// Health data: the broad scopes do not reach it.
	if status, _, _ := c.withToken(c.newToken("write")).call(http.MethodGet, "/recipes/"+r["id"].(string)+"/warnings", nil); status != http.StatusForbidden {
		t.Errorf("write token = %d, want 403", status)
	}
	if status, _, _ := c.withToken(c.newToken("meals:read health:read")).call(http.MethodGet, "/recipes/"+r["id"].(string)+"/warnings", nil); status != http.StatusOK {
		t.Errorf("meals and health = %d", status)
	}
}

func TestADietMarkComesFromTheArticles(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, veg := createRecipe(t, c, map[string]any{"name": "Salat", "ingredients": []any{ingredient(1, "", "Zwiebeln"), ingredient(nil, "", "Currypaste")}})
	_, vegetarian := createRecipe(t, c, map[string]any{"name": "Rührei", "ingredients": []any{ingredient(4, "", "Eier"), ingredient(1, "tbsp", "Butter")}})
	_, meat := createRecipe(t, c, map[string]any{"name": "Pfanne", "ingredients": []any{ingredient(500, "g", "Hähnchenbrust"), ingredient(1, "", "Zwiebeln")}})
	_, free := createRecipe(t, c, map[string]any{"name": "Geheim", "ingredients": []any{ingredient(1, "", "Currypaste")}})
	for name, want := range map[string]any{"vegan": "vegan", "vegetarian": "vegetarian", "meat": nil, "free": nil} {
		r := map[string]map[string]any{"vegan": veg, "vegetarian": vegetarian, "meat": meat, "free": free}[name]
		if r["diet"] != want {
			t.Errorf("%s diet = %v, want %v", name, r["diet"], want)
		}
	}
}

// Acceptance: REST/OpenAPI and MCP under the meals area.
func TestMCPToolsWriteAndReadRecipesUnderTheMealsArea(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	c.switchMCP(t, true)
	areas := map[string]string{"events": "write", "lists": "write", "tasks": "write", "meals": "write", "health": "read"}
	s := c.mcpSession(t, c.newMCPToken(t, "Claude", areas))
	want := map[string]string{"list_recipes": "meals", "get_recipe": "meals", "create_recipe": "meals", "update_recipe": "meals", "delete_recipe": "meals", "list_recipe_units": "meals", "get_recipe_warnings": "health"}
	found := 0
	for _, tool := range server.MCPTools() {
		if area, ok := want[tool.Name]; ok {
			found++
			if tool.Permission.Resource != area {
				t.Errorf("%s needs %s, want the %s area", tool.Name, tool.Permission, area)
			}
		}
	}
	if found != len(want) {
		t.Fatalf("found %d of %d recipe tools", found, len(want))
	}
	out, failed := callTool(s, "create_recipe", map[string]any{"name": "Pfannkuchen", "category": "breakfast", "portions": 4,
		"ingredients": []any{map[string]any{"quantity": 250, "unit": "g", "name": "Weizenmehl"}, map[string]any{"quantity": 1, "unit": "pinch", "name": "Salz"}},
		"steps":       []string{"Alles verrühren.", "Backen."}})
	if failed || !strings.Contains(out, "Pfannkuchen") {
		t.Fatalf("create_recipe = %v %s", failed, out)
	}
	_, _, list := c.call(http.MethodGet, "/recipes", nil)
	if len(list) != 1 {
		t.Fatalf("REST sees %v", list)
	}
	id := list[0].(map[string]any)["id"].(string)
	if out, failed := callTool(s, "list_recipes", map[string]any{"q": "mehl"}); failed || !strings.Contains(out, "Pfannkuchen") {
		t.Errorf("list_recipes = %v %s", failed, out)
	}
	if out, failed := callTool(s, "get_recipe", map[string]any{"id": id}); failed || !strings.Contains(out, "Backen.") {
		t.Errorf("get_recipe = %v %s", failed, out)
	}
	if out, failed := callTool(s, "update_recipe", map[string]any{"id": id, "name": "Pfannkuchen von Oma", "steps": []string{"Backen."}}); failed || !strings.Contains(out, "von Oma") {
		t.Errorf("update_recipe = %v %s", failed, out)
	}
	if out, failed := callTool(s, "get_recipe_warnings", map[string]any{"id": id}); failed || !strings.Contains(out, "not_checked_ingredients") {
		t.Errorf("get_recipe_warnings = %v %s", failed, out)
	}
	if out, failed := callTool(s, "list_recipe_units", nil); failed || !strings.Contains(out, "tbsp") {
		t.Errorf("list_recipe_units = %v %s", failed, out)
	}
	if out, failed := callTool(s, "delete_recipe", map[string]any{"id": id}); failed {
		t.Errorf("delete_recipe: %s", out)
	}
	if _, _, list := c.call(http.MethodGet, "/recipes", nil); len(list) != 0 {
		t.Errorf("REST still sees %v", list)
	}
	// A token without the health area gets no warnings tool.
	read := c.mcpSession(t, c.newMCPToken(t, "Nur Essen", map[string]string{"meals": "write"}))
	for _, n := range toolNames(t, read) {
		if n == "get_recipe_warnings" {
			t.Errorf("the meals area does not reach health data")
		}
	}
}

// Ticket 286 (ADR 0015: API parity with the cooking view; ADR 0021): the recipe
// for other portions has its quantities scaled and rounded to one decimal.
func TestARecipeForOtherPortionsHasScaledQuantitiesWithOneDecimal(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	in := curryBody() // 4 portions: 600 g, 2 (none), 3 tbsp, 3 tbsp, salt, 2 can, 1 tsp
	_, r := createRecipe(t, c, in)
	id := r["id"].(string)
	status, got, _ := c.call(http.MethodGet, "/recipes/"+id+"?portions=6", nil)
	if status != http.StatusOK || got["portions"] != float64(6) {
		t.Fatalf("get for 6 = %d %v", status, got)
	}
	ings := got["ingredients"].([]any)
	want := []any{float64(900), float64(3), 4.5, 4.5, nil, float64(3), 1.5}
	for i, w := range want {
		if q := ings[i].(map[string]any)["quantity"]; q != w {
			t.Errorf("ingredient %d quantity for 6 portions = %v, want %v", i, q, w)
		}
	}
	// 3 of 4 is 0.75 per portion: one decimal, never a whole number.
	_, one, _ := c.call(http.MethodGet, "/recipes/"+id+"?portions=1", nil)
	if q := one["ingredients"].([]any)[2].(map[string]any)["quantity"]; q != 0.8 {
		t.Errorf("3 tbsp for 1 of 4 portions = %v, want 0.8", q)
	}
	// Without portions, and with the base portions, the stored quantities.
	_, plain, _ := c.call(http.MethodGet, "/recipes/"+id, nil)
	_, same, _ := c.call(http.MethodGet, "/recipes/"+id+"?portions=4", nil)
	for _, g := range []map[string]any{plain, same} {
		if g["portions"] != float64(4) || g["ingredients"].([]any)[0].(map[string]any)["quantity"] != float64(600) {
			t.Errorf("stored recipe = %v", g)
		}
	}
	// The stored recipe did not change.
	if _, again, _ := c.call(http.MethodGet, "/recipes/"+id, nil); again["portions"] != float64(4) {
		t.Errorf("scaling changed the recipe: %v", again["portions"])
	}
	// What the contract forbids, a crafted request can still send: refused, field named.
	for _, bad := range []string{"0", "100", "-1", "x", "1.5", "%2B3", ""} {
		req, _ := http.NewRequest(http.MethodGet, c.b.srv.URL+"/api/v1/recipes/"+id+"?portions="+bad, nil)
		res, err := c.b.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), `"field":"portions"`) {
			t.Errorf("portions=%q = %d %s, want 422 at portions (an empty value too)", bad, res.StatusCode, body)
		}
	}
}
