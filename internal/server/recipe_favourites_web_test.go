package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 287 (ADR 0015: HTML routes with API parity): the favourite marks and
// "last cooked" on the recipe page, in the recipe list and in the meal plan's
// suggestions. The test clock stands at Saturday 14/11/2026.

func (mb mealBox) like(t *testing.T, r store.Recipe, people ...store.Person) {
	t.Helper()
	var ids []string
	for _, p := range people {
		ids = append(ids, p.ID)
	}
	if err := mb.b.db.SetRecipeFavourites(mb.hid, r.ID, ids); err != nil {
		t.Fatal(err)
	}
}

// Acceptance: favourite per person, shown on the recipe.
func TestTheRecipePageSaysWhoLikesItAndWhenItWasLastCooked(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	curry := mb.curry(t)
	page := mb.b.do(http.MethodGet, "/meals/recipes/"+curry.ID, nil, enHeader).body
	rcpHas(t, "a new recipe", page, "<dt>Favourite of</dt>", "Nobody yet", "<dt>Last cooked</dt>", "Not cooked yet")
	mb.like(t, curry, mb.mia, mb.mama)
	mb.plan(t, store.Meal{Day: "2026-11-10", Mealtime: meal.Dinner, RecipeID: curry.ID, Portions: 4}, store.Meal{Day: "2026-11-14", Mealtime: meal.Dinner, RecipeID: curry.ID, Portions: 4})
	page = mb.b.do(http.MethodGet, "/meals/recipes/"+curry.ID, nil, enHeader).body
	rcpHas(t, "the recipe", page, "<dt>Favourite of</dt>", "</span>Mia</span>", "</span>Mama</span>", "<dt>Last cooked</dt>", "<dd>Tuesday, 10.11.</dd>")
	rcpLacks(t, "the recipe", page, "Nobody yet", "Not cooked yet")
	de := mb.b.do(http.MethodGet, "/meals/recipes/"+curry.ID, nil, rcpDE).body
	rcpHas(t, "the German page", de, "<dt>Lieblingsessen von</dt>", "<dt>Zuletzt gekocht</dt>", "<dd>Dienstag, 10.11.</dd>")
}

func TestAParentMarksWhoLikesARecipeAndAChildOnlyReadsFavourites(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	curry := mb.curry(t)
	page := mb.b.do(http.MethodGet, "/meals/recipes/"+curry.ID, nil, enHeader).body
	rcpHas(t, "the parent's page", page, `<form class="rcp-fav" method="post" action="/meals/recipes/`+curry.ID+`/favourites"`, `<legend>Who likes it?</legend>`,
		`<input type="checkbox" name="person" value="`+mb.mia.ID+`">`, `<input type="checkbox" name="person" value="`+mb.mama.ID+`">`, "Save who likes it")
	r := mb.b.do(http.MethodPost, "/meals/recipes/"+curry.ID+"/favourites", url.Values{"person": {mb.mia.ID}}, enHeader)
	if r.status != http.StatusSeeOther || r.location != "/meals/recipes/"+curry.ID {
		t.Fatalf("save = %d to %q", r.status, r.location)
	}
	got, _ := mb.b.db.Recipe(mb.hid, curry.ID)
	if len(got.FavouriteOf) != 1 || got.FavouriteOf[0] != mb.mia.ID {
		t.Fatalf("stored = %v", got.FavouriteOf)
	}
	page = mb.b.do(http.MethodGet, "/meals/recipes/"+curry.ID, nil, enHeader).body
	rcpHas(t, "after saving", page, `<input type="checkbox" name="person" value="`+mb.mia.ID+`" checked>`, "Saved who likes “Erdnuss-Curry mit Süßkartoffeln”.")
	rcpLacks(t, "after saving", page, `value="`+mb.mama.ID+`" checked`)
	// Nothing ticked clears the marks.
	if r := mb.b.do(http.MethodPost, "/meals/recipes/"+curry.ID+"/favourites", url.Values{}, enHeader); r.status != http.StatusSeeOther {
		t.Fatalf("clear = %d", r.status)
	}
	if got, _ := mb.b.db.Recipe(mb.hid, curry.ID); len(got.FavouriteOf) != 0 {
		t.Errorf("after clearing = %v", got.FavouriteOf)
	}
	// A child reads the marks and has no form; the route is for parents.
	mb.like(t, curry, mb.mia)
	kid := mb.child.do(http.MethodGet, "/meals/recipes/"+curry.ID, nil, enHeader).body
	rcpHas(t, "the child's page", kid, "<dt>Favourite of</dt>", "</span>Mia</span>")
	rcpLacks(t, "the child's page", kid, "/favourites", `name="person"`)
	if r := mb.child.do(http.MethodPost, "/meals/recipes/"+curry.ID+"/favourites", url.Values{"person": {mb.mama.ID}}, enHeader); r.status != http.StatusForbidden {
		t.Errorf("a child saves = %d, want 403", r.status)
	}
	if got, _ := mb.b.db.Recipe(mb.hid, curry.ID); len(got.FavouriteOf) != 1 || got.FavouriteOf[0] != mb.mia.ID {
		t.Errorf("the child changed the marks: %v", got.FavouriteOf)
	}
	// Unknown recipe and unknown person.
	if r := mb.b.do(http.MethodPost, "/meals/recipes/nope/favourites", url.Values{}, enHeader); r.status != http.StatusNotFound {
		t.Errorf("unknown recipe = %d, want 404", r.status)
	}
	if r := mb.b.do(http.MethodPost, "/meals/recipes/"+curry.ID+"/favourites", url.Values{"person": {"nobody"}}, enHeader); r.status != http.StatusUnprocessableEntity {
		t.Errorf("unknown person = %d, want 422", r.status)
	}
	if r := mb.b.do(http.MethodPost, "/meals/recipes/starter-rahmspinat/favourites", url.Values{}, enHeader); r.status != http.StatusNotFound {
		t.Errorf("starter recipe = %d, want 404: only own recipes are planned", r.status)
	}
	starter := mb.b.do(http.MethodGet, "/meals/recipes/starter-rahmspinat", nil, enHeader).body
	rcpLacks(t, "a starter recipe", starter, "/favourites", "Last cooked", "Favourite of")
}

// Acceptance: last cooked shows in the recipe list.
func TestTheListShowsWhoLikesARecipeAndWhenItWasLastCooked(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	curry := mb.curry(t)
	soup := mb.add(t, recipe.Recipe{Name: "Linsensuppe"})
	mb.like(t, curry, mb.mia)
	mb.like(t, soup, mb.mia, mb.mama)
	mb.plan(t, store.Meal{Day: "2026-11-03", Mealtime: meal.Dinner, RecipeID: curry.ID, Portions: 4}, store.Meal{Day: "2026-11-09", Mealtime: meal.Dinner, RecipeID: curry.ID, Portions: 4},
		store.Meal{Day: "2026-11-20", Mealtime: meal.Dinner, RecipeID: soup.ID, Portions: 4})
	list := mb.b.do(http.MethodGet, "/meals/recipes", nil, enHeader).body
	curryRow := rcpBetween(t, list, `<a href="/meals/recipes/`+curry.ID+`">`, `</a>`)
	soupRow := rcpBetween(t, list, `<a href="/meals/recipes/`+soup.ID+`">`, `</a>`)
	rcpHas(t, "the curry row", curryRow, "Last cooked Monday, 09.11.", "</span>Mia</span> likes it")
	rcpHas(t, "the soup row", soupRow, "</span>Mama</span> and <span", "</span>Mia</span> like it")
	rcpLacks(t, "the soup row", soupRow, "Last cooked")
	de := mb.b.do(http.MethodGet, "/meals/recipes", nil, rcpDE).body
	rcpHas(t, "the German list", de, "Zuletzt gekocht Montag, 09.11.", "mag das", "mögen das")
	// The starter collection is nobody's favourite and was never cooked.
	rcpLacks(t, "the starter list", mb.b.do(http.MethodGet, "/meals/recipes/starter", nil, enHeader).body, "Last cooked", "likes it", "like it")
}

// Acceptance: the meal plan picker shows favourites first.
func TestTheMealPlanSuggestsFavouritesFirstAndNamesWhoLikesThem(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	a := mb.add(t, recipe.Recipe{Name: "Curry mit Reis"})
	b := mb.add(t, recipe.Recipe{Name: "Curry mit Kartoffeln"})
	z := mb.add(t, recipe.Recipe{Name: "Zucchini-Curry"})
	_, body := suggest(mb, enHeader, "curry")
	if !(strings.Index(body, a.Name) > strings.Index(body, b.Name) && strings.Index(body, a.Name) < strings.Index(body, z.Name)) {
		t.Fatalf("without favourites the order is A to Z:\n%s", body)
	}
	rcpLacks(t, "no favourites", body, "likes it", "like it")
	mb.like(t, z, mb.mia)
	_, body = suggest(mb, enHeader, "curry")
	if !(strings.Index(body, z.Name) < strings.Index(body, b.Name) && strings.Index(body, b.Name) < strings.Index(body, a.Name)) {
		t.Errorf("a favourite comes first, the rest A to Z:\n%s", body)
	}
	row := rcpBetween(t, body, `data-recipe="`+z.ID+`"`, `</li>`)
	rcpHas(t, "the favourite's row", row, "</span>Mia</span> likes it")
	rcpLacks(t, "the others", rcpBetween(t, body, `data-recipe="`+a.ID+`"`, `</li>`), "likes it")
	// The mealtime still counts after the favourites: a favourite for another mealtime stays first.
	br := mb.add(t, recipe.Recipe{Name: "Curry-Frühstück", Mealtimes: []string{"breakfast"}})
	mb.like(t, br, mb.mama)
	_, body = suggest(mb, enHeader, "curry")
	if !(strings.Index(body, br.Name) < strings.Index(body, a.Name) && strings.Index(body, z.Name) < strings.Index(body, br.Name)) {
		t.Errorf("favourites first, then by mealtime and name within them:\n%s", body)
	}
}
