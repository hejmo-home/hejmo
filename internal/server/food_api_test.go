package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// Ticket 163 (ADR 0019): diet and allergies per person over the REST API.

func newFoodBox(t *testing.T) (*apiClient, string, string) {
	t.Helper()
	c := signedInAPI(t)
	_, mia, _ := c.call(http.MethodPost, "/people", map[string]any{"name": "Mia", "role": "child"})
	_, papa, _ := c.call(http.MethodPost, "/people", map[string]any{"name": "Papa", "role": "parent"})
	return c, mia["id"].(string), papa["id"].(string)
}

func TestFoodDefaultsToOmnivoreWithNoAllergies(t *testing.T) {
	c, mia, _ := newFoodBox(t)
	status, f, _ := c.call(http.MethodGet, "/people/"+mia+"/food", nil)
	if status != http.StatusOK || f["diet"] != "omnivore" || len(f["allergens"].([]any)) != 0 || len(f["other"].([]any)) != 0 || f["person_id"] != mia {
		t.Errorf("default = %d %v", status, f)
	}
}

func TestParentSetsDietAndAllergiesAndReadsThemBack(t *testing.T) {
	c, mia, _ := newFoodBox(t)
	status, f, _ := c.call(http.MethodPut, "/people/"+mia+"/food", map[string]any{"diet": "vegetarian", "allergens": []string{"peanuts", "nuts", "peanuts"}, "other": []string{"Kiwi"}})
	if status != http.StatusOK {
		t.Fatalf("put = %d %v", status, f)
	}
	status, f, _ = c.call(http.MethodGet, "/people/"+mia+"/food", nil)
	got := f["allergens"].([]any)
	if status != http.StatusOK || f["diet"] != "vegetarian" || len(got) != 2 || got[0] != "nuts" || got[1] != "peanuts" || f["other"].([]any)[0] != "Kiwi" {
		t.Errorf("read back = %d %v", status, f)
	}
	if note, _ := f["other_note"].(string); !strings.Contains(note, "nicht automatisch geprüft") || !strings.Contains(note, "nicht filterbar") {
		t.Errorf("the answer always says free text is not checked and not filterable: %q", note)
	}
	status, _, list := c.call(http.MethodGet, "/food", nil)
	if status != http.StatusOK || len(list) != 3 { // Mama, Mia, Papa
		t.Errorf("list = %d %v", status, list)
	}
}

func TestFoodRefusesWhatIsNotADietAnAllergenOrFitting(t *testing.T) {
	c, mia, _ := newFoodBox(t)
	for name, body := range map[string]map[string]any{
		"unknown diet":      {"diet": "keto", "allergens": []string{}, "other": []string{}},
		"unknown allergen":  {"diet": "vegan", "allergens": []string{"kiwi"}, "other": []string{}},
		"one of the 14":     {"diet": "vegan", "allergens": []string{}, "other": []string{"Laktose"}},
		"comma in an entry": {"diet": "vegan", "allergens": []string{}, "other": []string{"Kiwi, Erdbeeren"}},
		"too long":          {"diet": "vegan", "allergens": []string{}, "other": []string{strings.Repeat("a", 201)}},
	} {
		if status, _, _ := c.call(http.MethodPut, "/people/"+mia+"/food", body); status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, status)
		}
	}
	_, f, _ := c.call(http.MethodGet, "/people/"+mia+"/food", nil)
	if f["diet"] != "omnivore" {
		t.Errorf("a refused request changed something: %v", f)
	}
	if status, _, _ := c.call(http.MethodGet, "/people/00000000-0000-4000-8000-000000000000/food", nil); status != http.StatusNotFound {
		t.Errorf("unknown person = %d", status)
	}
}

func TestChildrenCannotReadOrChangeDietAndAllergies(t *testing.T) {
	tb := newTaskBox(t)
	body := map[string]any{"diet": "vegan", "allergens": []string{}, "other": []string{}}
	if status, _, _ := tb.child.call(http.MethodPut, "/people/"+tb.miaID+"/food", body); status != http.StatusForbidden {
		t.Errorf("child put = %d", status)
	}
	if status, _, _ := tb.child.call(http.MethodGet, "/people/"+tb.miaID+"/food", nil); status != http.StatusForbidden {
		t.Errorf("child get = %d", status)
	}
	if status, _, _ := tb.child.call(http.MethodGet, "/food", nil); status != http.StatusForbidden {
		t.Errorf("child list = %d", status)
	}
}

func TestOnlyTheExplicitHealthScopeReachesDietAndAllergies(t *testing.T) {
	c, mia, _ := newFoodBox(t)
	body := map[string]any{"diet": "vegan", "allergens": []string{}, "other": []string{}}
	for _, scopes := range []string{"read", "write", "people", "people:read events"} {
		tok := c.withToken(c.newToken(scopes))
		if status, _, _ := tok.call(http.MethodGet, "/people/"+mia+"/food", nil); status != http.StatusForbidden {
			t.Errorf("scopes %q read health data: %d", scopes, status)
		}
		if status, _, _ := tok.call(http.MethodPut, "/people/"+mia+"/food", body); status != http.StatusForbidden {
			t.Errorf("scopes %q changed health data: %d", scopes, status)
		}
	}
	tok := c.withToken(c.newToken("health:read"))
	if status, _, _ := tok.call(http.MethodGet, "/people/"+mia+"/food", nil); status != http.StatusOK {
		t.Errorf("health:read get = %d", status)
	}
	if status, _, _ := tok.call(http.MethodPut, "/people/"+mia+"/food", body); status != http.StatusForbidden {
		t.Errorf("health:read put = %d", status)
	}
}

func TestPeopleAnswersNeverCarryDietOrAllergies(t *testing.T) {
	c, mia, _ := newFoodBox(t)
	c.call(http.MethodPut, "/people/"+mia+"/food", map[string]any{"diet": "vegan", "allergens": []string{"peanuts"}, "other": []string{"Kiwi"}})
	for _, path := range []string{"/people", "/people/" + mia, "/me", "/groups"} {
		raw, _, _ := c.callRaw(http.MethodGet, path, nil)
		for _, leak := range []string{"vegan", "peanuts", "Kiwi", "allerg"} {
			if strings.Contains(raw, leak) {
				t.Errorf("%s leaks %q: %s", path, leak, raw)
			}
		}
	}
}
