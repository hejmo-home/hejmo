package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 214 (product owner, 26/11/2026): the allergen tags of the library are
// accepted as they are; every screen that shows allergen information or an
// allergen warning says in one calm line that it comes without guarantee.
const (
	noticeEN = "Allergen information without guarantee. The ingredient list on the pack is what counts."
	noticeDE = "Allergen-Angaben ohne Gewähr. Maßgeblich ist die Zutatenliste auf der Verpackung."
)

// wantNotice fails when a page lacks the line, or when it is not in the
// existing small-print style (a hint), or when a page says it twice.
func wantNotice(t *testing.T, what, body, line string) {
	t.Helper()
	if n := strings.Count(body, `<p class="hint allergen-notice">`+line+`</p>`); n != 1 {
		t.Errorf("%s has the allergen notice %d times, want once\n%s", what, n, rcpShown(body))
	}
}

func TestEveryScreenWithAllergenInformationSaysItComesWithoutGuarantee(t *testing.T) {
	t.Parallel()

	t.Run("library and article page", func(t *testing.T) {
		l := newLibBox(t)
		mia := l.person("Mia", "child", "omnivore", "peanuts")
		_ = mia
		wantNotice(t, "library", l.en("/settings/lists/library").body, noticeEN)
		wantNotice(t, "library de", l.de("/settings/lists/library").body, noticeDE)
		wantNotice(t, "article", l.en("/settings/lists/library/peanut-butter").body, noticeEN)
		wantNotice(t, "article de", l.de("/settings/lists/library/peanut-butter").body, noticeDE)
		// An article that is no food shows no allergen information.
		if strings.Contains(l.en("/settings/lists/library/washing-up-liquid").body, noticeEN) {
			t.Error("a non-food article says it")
		}
	})

	t.Run("shopping list", func(t *testing.T) {
		s := newShop(t)
		s.person("Mia", "child", "omnivore", "peanuts")
		s.add(s.item("peanut-butter"))
		wantNotice(t, "list", s.en(s.path("")).body, noticeEN)
	})

	t.Run("list in tile view", func(t *testing.T) {
		b := signedInBox(t)
		setFood(t, b, "Mia", store.Food{Diet: "omnivore", Allergens: []string{"milk"}})
		id, _ := tileList(t, b, nil, "Milk", "Bananas")
		wantNotice(t, "tile list", getEN(b, "/lists/"+id).body, noticeEN)
		wantNotice(t, "tile list de", getDE(b, "/lists/"+id).body, noticeDE)
	})

	t.Run("display list", func(t *testing.T) {
		f := newFamilyBox(t, "eink_color")
		if err := f.db.SetPersonFood(f.householdID, f.people["Mia"].ID, store.Food{Diet: "omnivore", Allergens: []string{"milk"}}); err != nil {
			t.Fatal(err)
		}
		id, _ := f.onlyList(t, "Shopping", nil, "Milk", "Bananas")
		wantNotice(t, "display list", f.listsPage(t, "en", "/display/lists/"+id).body, noticeEN)
	})

	t.Run("recipes", func(t *testing.T) {
		rb := newRecipeBox(t)
		r := rb.curry(t)
		wantNotice(t, "recipe list", rb.b.do(http.MethodGet, "/meals/recipes", nil, enHeader).body, noticeEN)
		wantNotice(t, "recipe list de", rb.b.do(http.MethodGet, "/meals/recipes", nil, rcpDE).body, noticeDE)
		wantNotice(t, "cooking view", rb.b.do(http.MethodGet, "/meals/recipes/"+r.ID+"/cook", nil, enHeader).body, noticeEN)
		wantNotice(t, "recipe form", rb.b.do(http.MethodGet, "/meals/recipes/"+r.ID+"/edit", nil, enHeader).body, noticeEN)
		// The recipe page, the article form, the person's diet page and the
		// lists settings keep their longer "Experimental, no guarantee" line.
		if page := rb.b.do(http.MethodGet, "/meals/recipes/"+r.ID, nil, enHeader).body; !strings.Contains(page, "Experimental, no guarantee:") || strings.Contains(page, noticeEN) {
			t.Error("the recipe page has one caveat line, not two")
		}
	})

	t.Run("meal plan", func(t *testing.T) {
		mb := newMealBox(t)
		curry := mb.curry(t)
		mb.plan(t, store.Meal{Day: today, Mealtime: meal.Dinner, RecipeID: curry.ID, Portions: 6})
		wantNotice(t, "week", mb.page(t, enHeader), noticeEN)
		wantNotice(t, "week of a child", mb.child.do(http.MethodGet, "/meals", nil, enHeader).body, noticeEN)
	})
}

// The same sentence, in English, on what REST and MCP return about allergens.
func TestRESTAndMCPDescribeAllergenInformationAsWithoutGuarantee(t *testing.T) {
	t.Parallel()
	doc := loadSpec(t)
	for _, c := range []struct{ path, method string }{
		{"/articles", http.MethodGet}, {"/articles/{id}", http.MethodGet}, {"/articles/{id}/warnings", http.MethodGet},
		{"/recipes/{id}/warnings", http.MethodGet}, {"/people/{id}/food", http.MethodGet}, {"/food", http.MethodGet},
	} {
		item := doc.Paths.Find(c.path)
		if item == nil || item.GetOperation(c.method) == nil {
			t.Errorf("%s %s is not in the spec", c.method, c.path)
			continue
		}
		if d := item.GetOperation(c.method).Description; !strings.Contains(d, noticeEN) {
			t.Errorf("%s %s: description lacks the sentence:\n%s", c.method, c.path, d)
		}
	}
	if d := doc.Components.Schemas["Article"].Value.Properties["allergens"].Value.Description; !strings.Contains(d, noticeEN) {
		t.Errorf("Article.allergens description lacks the sentence: %q", d)
	}

	tb := newTaskBox(t)
	c := tb.parent
	c.switchMCP(t, true)
	s := c.mcpSession(t, c.newMCPToken(t, "Mit Gesundheit", map[string]string{"meals": "read", "health": "read"}))
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tool := range res.Tools {
		switch tool.Name {
		case "get_recipe_warnings", "get_diet_and_allergies", "list_diets_and_allergies":
			seen[tool.Name] = true
			if !strings.Contains(tool.Description, noticeEN) {
				t.Errorf("tool %s: description lacks the sentence: %s", tool.Name, tool.Description)
			}
		}
	}
	if len(seen) != 3 {
		t.Errorf("tools seen: %v", seen)
	}
}
