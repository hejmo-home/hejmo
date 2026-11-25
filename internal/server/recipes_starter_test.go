package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/recipe"
)

// Ticket 290 (ADR 0021, ADR 0015: HTML routes with API parity): the starter
// collection of 200 recipes, shown in the language of the person viewing.

func TestTheStarterCollectionListsAllTwoHundredInTheLanguageOfTheViewer(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	en := rb.b.do(http.MethodGet, "/meals/recipes/starter", nil, enHeader)
	if en.status != http.StatusOK {
		t.Fatalf("GET = %d", en.status)
	}
	if n := strings.Count(en.body, `<li><a href="/meals/recipes/starter-`); n != 200 {
		t.Errorf("%d recipes listed, want 200", n)
	}
	rcpHas(t, "the English collection", en.body, "<h1>Starter recipes</h1>", `<a href="/meals/recipes/starter-rahmspinat">`, "<b>Creamed spinach</b>", `<span aria-current="page">Starter recipes</span>`)
	rcpLacks(t, "the English collection", en.body, "<b>Rahmspinat</b>")
	de := rb.b.do(http.MethodGet, "/meals/recipes/starter", nil, rcpDE)
	rcpHas(t, "the German collection", de.body, "<b>Rahmspinat</b>", "Startrezepte")
	// Search and categories work as on the own recipes.
	soup := rb.b.do(http.MethodGet, "/meals/recipes/starter?q=creamed+spinach", nil, enHeader)
	rcpHas(t, "the search", soup.body, "<b>Creamed spinach</b>")
	if n := strings.Count(soup.body, `<li><a href="/meals/recipes/starter-`); n < 1 || n > 5 {
		t.Errorf("search found %d", n)
	}
	cat := rb.b.do(http.MethodGet, "/meals/recipes/starter?category=snack", nil, enHeader)
	if n := strings.Count(cat.body, `<li><a href="/meals/recipes/starter-`); n != 10 {
		t.Errorf("%d snacks, want 10", n)
	}
	// The own list stays the family's: no starter recipe in it, but a way to the collection.
	own := rb.b.do(http.MethodGet, "/meals/recipes", nil, enHeader)
	rcpHas(t, "the own list", own.body, `href="/meals/recipes/starter"`, "Starter recipes")
	rcpLacks(t, "the own list", own.body, "Creamed spinach")
}

func TestAStarterRecipeNamesItsSourceWithALinkAndShowsAllergyWarnings(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.b.do(http.MethodGet, "/meals/recipes/starter-rahmspinat", nil, enHeader)
	if r.status != http.StatusOK {
		t.Fatalf("GET = %d", r.status)
	}
	rcpHas(t, "the page", r.body, "Creamed spinach", "Ingredients after: Chefkoch", `<a class="ext" href="https://www.chefkoch.de/rezepte/321101114007445/Rahmspinat.html" rel="noopener noreferrer" target="_blank">`,
		"Wash the spinach", "Serve with boiled potatoes and fried eggs.", "Allergies in your family", "25 g", "1 tbsp")
	rcpLacks(t, "the page", r.body, "/edit", "Change recipe", "Delete recipe", "rcp-who")
	de := rb.b.do(http.MethodGet, "/meals/recipes/starter-rahmspinat", nil, rcpDE).body
	rcpHas(t, "the German page", de, "Rahmspinat", "Zutaten nach: Chefkoch", "Den Spinat waschen", "Dazu passen Salzkartoffeln und Spiegelei.", "1 EL")
	// Mia is allergic to peanuts: the dish with peanuts warns her.
	nuts := rb.b.do(http.MethodGet, "/meals/recipes/starter?q=erdnuss", nil, rcpDE).body
	rcpHas(t, "the collection", nuts, "Enthält Erdnüsse: ")
}

func TestUnitsOfAStarterRecipeAreWordsNotKeys(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	body := rb.b.do(http.MethodGet, "/meals/recipes/starter-rahmspinat", nil, rcpDE).body
	rcpLacks(t, "the page", body, "tbsp", "knifetip", "unit.")
}

func TestAStarterRecipeCannotBeChangedOrDeletedByAnyone(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	for _, c := range []struct {
		method, path string
		form         url.Values
	}{
		{http.MethodGet, "/meals/recipes/starter-rahmspinat/edit", nil},
		{http.MethodPost, "/meals/recipes/starter-rahmspinat", url.Values{"name": {"Eis"}}},
		{http.MethodPost, "/meals/recipes/starter-rahmspinat/delete", url.Values{}},
	} {
		if got := rb.b.do(c.method, c.path, c.form, enHeader); got.status != http.StatusNotFound {
			t.Errorf("parent %s %s = %d, want 404", c.method, c.path, got.status)
		}
	}
	if got := rb.b.do(http.MethodGet, "/meals/recipes/starter-gibt-es-nicht", nil, enHeader); got.status != http.StatusNotFound {
		t.Errorf("unknown starter recipe = %d", got.status)
	}
}

func TestChildrenReadTheStarterCollection(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	for _, p := range []string{"/meals/recipes/starter", "/meals/recipes/starter-rahmspinat"} {
		if got := rb.child.do(http.MethodGet, p, nil, enHeader); got.status != http.StatusOK {
			t.Errorf("child GET %s = %d", p, got.status)
		}
	}
}

func TestARecipeOfTheFamilyMayShareTheNameOfAStarterRecipe(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	rb.add(t, recipe.Recipe{Name: "Rahmspinat"})
	own := rb.b.do(http.MethodGet, "/meals/recipes", nil, rcpDE)
	rcpHas(t, "the own list", own.body, "<b>Rahmspinat</b>")
}
