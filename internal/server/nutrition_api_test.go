package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Ticket 288, ADR 0015: the API twin of the nutrition card, the food search
// and the food of an own article.

func postRecipe(t *testing.T, c *apiClient, name string, portions int, ings ...map[string]any) string {
	t.Helper()
	status, got, _ := c.call(http.MethodPost, "/recipes", map[string]any{"name": name, "portions": portions, "ingredients": ings})
	if status != http.StatusCreated {
		t.Fatalf("create recipe = %d %v", status, got)
	}
	return got["id"].(string)
}

func ing(articleID string, qty float64, unit string) map[string]any {
	m := map[string]any{"name": articleID, "article_id": articleID}
	if qty > 0 {
		m["quantity"] = qty
	}
	if unit != "" {
		m["unit"] = unit
	}
	return m
}

func TestTheAPIGivesTheNutritionValuesPerPortion(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	id := postRecipe(t, tb.parent, "Haferbrei", 1, ing("rolled-oats", 100, "g"))
	status, got, _ := tb.parent.call(http.MethodGet, "/recipes/"+id+"/nutrition", nil)
	if status != http.StatusOK {
		t.Fatalf("GET = %d %v", status, got)
	}
	per := got["per_portion"].(map[string]any)
	if per["energy_kcal"] != 348.0 || per["energy_kj"] != 1465.0 || per["fat_g"] != 6.65 || per["protein_g"] != 13.22 || per["sugars_g"] != 0.74 {
		t.Errorf("per portion = %v", per)
	}
	if got["recipe_id"] != id || got["portions"] != 1.0 || got["counted"] != 1.0 || got["complete"] != true {
		t.Errorf("answer = %v", got)
	}
	if src := got["sources"].([]any); len(src) != 1 || src[0] != "bls" {
		t.Errorf("sources = %v", got["sources"])
	}
	if nc := got["not_counted"].([]any); len(nc) != 0 {
		t.Errorf("not counted = %v", nc)
	}
}

func TestTheAPINamesWhatIsNotCountedAndNeverAnswersZeroForUnknown(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	id := postRecipe(t, tb.parent, "Saft", 2,
		ing("apple-juice", 200, "ml"),
		map[string]any{"name": "Currypaste", "quantity": 1, "unit": "tbsp"},
		ing("blueberries", 1, "handful"),
		ing("salt", 0, ""),
	)
	status, got, _ := tb.parent.call(http.MethodGet, "/recipes/"+id+"/nutrition", nil)
	if status != http.StatusOK {
		t.Fatalf("GET = %d %v", status, got)
	}
	per := got["per_portion"].(map[string]any)
	if v, ok := per["fat_g"]; !ok || v != nil {
		t.Errorf("fat is unknown for BLS Apfelsaft: want null, got %v (present %v)", v, ok)
	}
	if per["energy_kcal"] == nil || per["energy_kcal"] == 0.0 {
		t.Errorf("energy = %v", per["energy_kcal"])
	}
	if got["complete"] != false {
		t.Error("not complete")
	}
	reasons := map[string]string{}
	for _, x := range got["not_counted"].([]any) {
		m := x.(map[string]any)
		reasons[m["name"].(string)] = m["reason"].(string)
	}
	want := map[string]string{"Currypaste": "free_text", "Heidelbeeren": "no_weight", "Salz": "no_quantity"}
	for k, v := range want {
		if reasons[k] != v {
			t.Errorf("%s: reason %q, want %q (all: %v)", k, reasons[k], v, reasons)
		}
	}
	miss := got["missing_values"].([]any)
	if len(miss) != 1 || miss[0].(map[string]any)["value"] != "fat_g" {
		t.Fatalf("missing values = %v", miss)
	}
	foods := miss[0].(map[string]any)["foods"].([]any)
	if len(foods) != 1 || foods[0].(map[string]any)["id"] != "bls:F110600" {
		t.Errorf("foods = %v", foods)
	}
}

func TestTheAPINutritionIsHiddenFromAChildUntilAParentSwitchesItOn(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	id := postRecipe(t, tb.parent, "Haferbrei", 1, ing("rolled-oats", 100, "g"))
	if status, _, _ := tb.child.call(http.MethodGet, "/recipes/"+id+"/nutrition", nil); status != http.StatusNotFound {
		t.Fatalf("a child without the switch = %d, want 404", status)
	}
	if status, _, _ := tb.parent.call(http.MethodPatch, "/people/"+tb.miaID, map[string]any{"nutrition_shown": true}); status != http.StatusOK {
		t.Fatalf("patch = %d", status)
	}
	if status, got, _ := tb.child.call(http.MethodGet, "/recipes/"+id+"/nutrition", nil); status != http.StatusOK || got["per_portion"] == nil {
		t.Fatalf("a child with the switch = %d %v", status, got)
	}
	if status, _, _ := tb.parent.call(http.MethodGet, "/recipes/00000000-0000-4000-8000-000000000000/nutrition", nil); status != http.StatusNotFound {
		t.Errorf("unknown recipe = %d", status)
	}
}

func TestTheRecipeItselfCarriesNoNutritionValues(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	id := postRecipe(t, tb.parent, "Haferbrei", 1, ing("rolled-oats", 100, "g"))
	_, got, _ := tb.parent.call(http.MethodGet, "/recipes/"+id, nil)
	for k := range got {
		if strings.Contains(k, "nutri") || strings.Contains(k, "kcal") {
			t.Errorf("recipe has %q: nutrition has its own endpoint, which children may not read", k)
		}
	}
}

func TestTheFoodSearchFindsFoodsWithTheirValuesPer100g(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	status, _, list := tb.parent.call(http.MethodGet, "/foods?q=Hafer%20Flocken", nil)
	if status != http.StatusOK || len(list) == 0 {
		t.Fatalf("search = %d %v", status, list)
	}
	first := list[0].(map[string]any)
	if first["source"] != "bls" || !strings.Contains(strings.ToLower(first["name_de"].(string)), "hafer") {
		t.Errorf("first = %v", first)
	}
	per := first["per_100g"].(map[string]any)
	if per["energy_kcal"] == nil {
		t.Errorf("per 100 g = %v", per)
	}
	_, _, few := tb.parent.call(http.MethodGet, "/foods?q=brot&limit=3", nil)
	if len(few) != 3 {
		t.Errorf("limit 3 gave %d", len(few))
	}
	if _, _, none := tb.parent.call(http.MethodGet, "/foods?q=a", nil); len(none) != 0 {
		t.Error("one letter finds nothing")
	}
	status, one, _ := tb.parent.call(http.MethodGet, "/foods/bls:C133000", nil)
	if status != http.StatusOK || one["id"] != "bls:C133000" || one["per_100g"].(map[string]any)["energy_kcal"] != 348.0 {
		t.Errorf("get = %d %v", status, one)
	}
	// A value the source lacks is null, not 0.
	_, juice, _ := tb.parent.call(http.MethodGet, "/foods/bls:F110600", nil)
	if v, ok := juice["per_100g"].(map[string]any)["fat_g"]; !ok || v != nil {
		t.Errorf("Apfelsaft fat = %v (present %v), want null", v, ok)
	}
	if status, _, _ := tb.parent.call(http.MethodGet, "/foods/bls:NOPE", nil); status != http.StatusNotFound {
		t.Errorf("unknown food = %d", status)
	}
}

func TestAnOwnArticleTakesAFoodAndAPieceWeightOverTheAPI(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	c := tb.parent
	status, got, _ := c.call(http.MethodPost, "/articles", map[string]any{"name": "Omas Pflaumenmus", "dept": "pantry", "kind": "vegan", "unit1": "piece", "food": "bls:S161000", "piece_g": 350})
	if status != http.StatusCreated {
		t.Fatalf("create = %d %v", status, got)
	}
	food := got["food"].(map[string]any)
	if food["id"] != "bls:S161000" || food["source"] != "bls" || food["per_100g"].(map[string]any)["energy_kcal"] != 195.0 || got["piece_g"] != 350.0 {
		t.Errorf("article = %v", got)
	}
	id := got["id"].(string)
	// The recipe counts it.
	rid := postRecipe(t, c, "Mus", 1, ing(id, 1, "piece"))
	_, n, _ := c.call(http.MethodGet, "/recipes/"+rid+"/nutrition", nil)
	if n["per_portion"].(map[string]any)["energy_kcal"] != 682.5 {
		t.Errorf("1 piece of 350 g at 195 kcal = 682.5, got %v", n["per_portion"])
	}
	// A change that sends nothing about the food keeps it; "" clears it.
	_, got, _ = c.call(http.MethodPatch, "/articles/"+id, map[string]any{"name": "Omas Pflaumenmus 2"})
	if got["food"] == nil || got["piece_g"] != 350.0 {
		t.Errorf("a change without food lost it: %v", got)
	}
	_, got, _ = c.call(http.MethodPatch, "/articles/"+id, map[string]any{"food": "", "piece_g": 0})
	if got["food"] != nil || got["piece_g"] != nil {
		t.Errorf("cleared = %v %v", got["food"], got["piece_g"])
	}
	_, n, _ = c.call(http.MethodGet, "/recipes/"+rid+"/nutrition", nil)
	nc := n["not_counted"].([]any)
	if len(nc) != 1 || nc[0].(map[string]any)["reason"] != "no_food" || nc[0].(map[string]any)["article_id"] != id {
		t.Errorf("not counted = %v", nc)
	}
	for field, body := range map[string]map[string]any{"food": {"food": "bls:NOPE"}, "piece_g": {"piece_g": -3}} {
		status, e, _ := c.call(http.MethodPatch, "/articles/"+id, body)
		if status != http.StatusUnprocessableEntity || e["field"] != field {
			t.Errorf("%s: %d %v", field, status, e)
		}
	}
}

func TestAShippedArticleShowsItsFoodAndANonFoodShowsNone(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	_, flour, _ := tb.parent.call(http.MethodGet, "/articles/wheat-flour", nil)
	if f, ok := flour["food"].(map[string]any); !ok || f["id"] != "bls:C214100" {
		t.Errorf("flour food = %v", flour["food"])
	}
	_, eggs, _ := tb.parent.call(http.MethodGet, "/articles/eggs", nil)
	if eggs["piece_g"] != 50.0 {
		t.Errorf("an egg weighs %v", eggs["piece_g"])
	}
	_, brush, _ := tb.parent.call(http.MethodGet, "/articles/toothbrush", nil)
	if brush["food"] != nil || brush["piece_g"] != nil {
		t.Errorf("a toothbrush has no food: %v", brush["food"])
	}
	// A shipped article's food cannot be changed (it is not an own article).
	if status, _, _ := tb.parent.call(http.MethodPatch, "/articles/wheat-flour", map[string]any{"food": "bls:C214200"}); status != http.StatusUnprocessableEntity {
		t.Errorf("patch shipped = %d", status)
	}
}

func TestMCPHasTheNutritionOfARecipe(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	c := tb.parent
	id := postRecipe(t, c, "Haferbrei", 1, ing("rolled-oats", 100, "g"))
	c.switchMCP(t, true)
	s := c.mcpSession(t, c.newMCPToken(t, "Claude", allAreas))
	if !hasName(toolNames(t, s), "get_recipe_nutrition") {
		t.Fatalf("no get_recipe_nutrition in %v", toolNames(t, s))
	}
	out, isErr := callTool(s, "get_recipe_nutrition", map[string]any{"id": id})
	if isErr || !strings.Contains(out, `"energy_kcal"`) || !strings.Contains(out, "348") {
		t.Errorf("get_recipe_nutrition = %v %s", isErr, out)
	}
}

// Acceptance: never on displays. A display holds a display token: it reaches
// its own pages and state, not the API of the household, and none of what it
// shows carries nutrition values, whatever a parent switched on.
func TestNutritionNeverReachesADisplay(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	c := tb.parent
	id := postRecipe(t, c, "Haferbrei", 1, ing("rolled-oats", 100, "g"))
	c.call(http.MethodPatch, "/people/"+tb.miaID, map[string]any{"nutrition_shown": true})
	anon := newAPIClient(t, c.b)
	_, opened := openAPIPairing(anon)
	if status, _, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": opened["code"], "name": "Kitchen", "type": "screen"}); status != http.StatusCreated {
		t.Fatalf("pair = %d", status)
	}
	token := opened["token"].(string)
	d := anon.withToken(token)
	for _, path := range []string{"/recipes/" + id + "/nutrition", "/recipes/" + id, "/foods?q=hafer"} {
		if status, _, _ := d.call(http.MethodGet, path, nil); status != http.StatusUnauthorized {
			t.Errorf("a display reached %s: %d", path, status)
		}
	}
	for _, path := range []string{"/display/view", "/display"} {
		req, _ := http.NewRequest(http.MethodGet, c.b.srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := c.b.bareClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if low := strings.ToLower(string(body)); strings.Contains(low, "kcal") || strings.Contains(low, "nutri") || strings.Contains(low, "nährwert") {
			t.Errorf("%s shows nutrition values", path)
		}
	}
	_, st, _ := d.call(http.MethodGet, "/display/state", nil)
	raw, _ := json.Marshal(st)
	if strings.Contains(strings.ToLower(string(raw)), "nutri") || strings.Contains(string(raw), "kcal") {
		t.Errorf("the display state carries nutrition values: %s", raw)
	}
}
