package server_test

import (
	"net/http"
	"testing"
)

// Ticket 287 (ADR 0021, ADR 0015: HTML routes with API parity): a recipe is a
// favourite per person and shows when it was last cooked, over the REST API,
// contract-checked against openapi.yaml. The test clock stands at 14/11/2026.

func apiPersonID(t *testing.T, c *apiClient, name string) string {
	t.Helper()
	_, _, list := c.call(http.MethodGet, "/people", nil)
	for _, x := range list {
		if p := x.(map[string]any); p["name"] == name {
			return p["id"].(string)
		}
	}
	t.Fatalf("no person %s in %v", name, list)
	return ""
}

// Acceptance: favourite per person, over REST/OpenAPI.
func TestARecipeIsMarkedAsFavouriteOfEachPersonOverTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	withFamily(t, c)
	_, r := createRecipe(t, c, curryBody())
	id := r["id"].(string)
	if fav, ok := r["favourite_of"].([]any); !ok || len(fav) != 0 {
		t.Fatalf("a new recipe = %v: favourite_of is an empty list, never missing", r["favourite_of"])
	}
	if lc, has := r["last_cooked"]; !has || lc != nil {
		t.Errorf("a new recipe last_cooked = %v, %v: null, never missing", lc, has)
	}
	mia, papa := apiPersonID(t, c, "Mia"), apiPersonID(t, c, "Papa")
	status, got, _ := c.call(http.MethodPut, "/recipes/"+id+"/favourites", map[string]any{"person_ids": []string{mia, papa}})
	fav, _ := got["favourite_of"].([]any)
	if status != http.StatusOK || len(fav) != 2 {
		t.Fatalf("put favourites = %d %v", status, got)
	}
	_, one, _ := c.call(http.MethodGet, "/recipes/"+id, nil)
	_, _, list := c.call(http.MethodGet, "/recipes", nil)
	if f, _ := one["favourite_of"].([]any); len(f) != 2 || len(list) != 1 || len(list[0].(map[string]any)["favourite_of"].([]any)) != 2 {
		t.Errorf("get = %v, list = %v: the marks show in both", one["favourite_of"], list)
	}
	// The marks are replaced as a whole; an empty list clears them.
	status, got, _ = c.call(http.MethodPut, "/recipes/"+id+"/favourites", map[string]any{"person_ids": []string{mia}})
	if f, _ := got["favourite_of"].([]any); status != http.StatusOK || len(f) != 1 || f[0] != mia {
		t.Errorf("replace = %d %v", status, got)
	}
	status, got, _ = c.call(http.MethodPut, "/recipes/"+id+"/favourites", map[string]any{"person_ids": []string{}})
	if f, _ := got["favourite_of"].([]any); status != http.StatusOK || len(f) != 0 {
		t.Errorf("clear = %d %v", status, got)
	}
}

func TestFavouritesAreRefusedForUnknownPeopleRecipesAndStarterRecipes(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, r := createRecipe(t, c, curryBody())
	id := r["id"].(string)
	if status, _, _ := c.call(http.MethodPut, "/recipes/"+id+"/favourites", map[string]any{"person_ids": []string{"nobody"}}); status != http.StatusUnprocessableEntity {
		t.Errorf("unknown person = %d, want 422", status)
	}
	if status, _, _ := c.call(http.MethodPut, "/recipes/nothing/favourites", map[string]any{"person_ids": []string{}}); status != http.StatusNotFound {
		t.Errorf("unknown recipe = %d, want 404", status)
	}
	if status, _, _ := c.call(http.MethodPut, "/recipes/starter-rahmspinat/favourites", map[string]any{"person_ids": []string{}}); status != http.StatusForbidden {
		t.Errorf("starter recipe = %d, want 403: only own recipes can be planned", status)
	}
	// Reading never needs the write scope; marking does.
	read := c.withToken(c.newToken("meals:read"))
	if status, _, _ := read.call(http.MethodPut, "/recipes/"+id+"/favourites", map[string]any{"person_ids": []string{}}); status != http.StatusForbidden {
		t.Errorf("read-only token = %d, want 403", status)
	}
	if status, got, _ := read.call(http.MethodGet, "/recipes/"+id, nil); status != http.StatusOK || got["favourite_of"] == nil {
		t.Errorf("read-only get = %d %v", status, got)
	}
}

// Acceptance: last cooked updates when a planned day passes.
func TestLastCookedIsTheLatestPlannedDayThatIsOverOverTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, r := createRecipe(t, c, curryBody())
	id := r["id"].(string)
	_, other := createRecipe(t, c, map[string]any{"name": "Linsensuppe"})
	if status, _ := putRecipeMeal(c, "2026-11-20", "dinner", map[string]any{"recipe_id": id, "portions": 4}); status != http.StatusOK {
		t.Fatalf("plan a day to come = %d", status)
	}
	_, one, _ := c.call(http.MethodGet, "/recipes/"+id, nil)
	if one["last_cooked"] != nil {
		t.Errorf("planned only for a day to come: last_cooked = %v, want null", one["last_cooked"])
	}
	for _, day := range []string{"2026-11-03", "2026-11-10", "2026-11-14"} {
		if status, _ := putRecipeMeal(c, day, "dinner", map[string]any{"recipe_id": id, "portions": 4}); status != http.StatusOK {
			t.Fatalf("plan %s = %d", day, status)
		}
	}
	_, one, _ = c.call(http.MethodGet, "/recipes/"+id, nil)
	if one["last_cooked"] != "2026-11-10" {
		t.Errorf("last_cooked = %v, want 2026-11-10: today (14/11) is not over yet and 20/11 is to come", one["last_cooked"])
	}
	_, _, list := c.call(http.MethodGet, "/recipes", nil)
	got := map[string]any{}
	for _, x := range list {
		m := x.(map[string]any)
		got[m["id"].(string)] = m["last_cooked"]
	}
	if got[id] != "2026-11-10" || got[other["id"].(string)] != nil {
		t.Errorf("list last_cooked = %v: a recipe never planned has null", got)
	}
	_, starter, _ := c.call(http.MethodGet, "/recipes/starter-rahmspinat", nil)
	if fav, ok := starter["favourite_of"].([]any); !ok || len(fav) != 0 || starter["last_cooked"] != nil {
		t.Errorf("starter recipe = %v %v: nobody's favourite, never cooked", starter["favourite_of"], starter["last_cooked"])
	}
}
