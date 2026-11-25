package server_test

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 279 (ADR 0015: HTML routes with API parity, preview-279.html): the
// recipe pages. Parents write recipes, everyone reads them.

var rcpDE = map[string]string{"Accept-Language": "de"}

type recipeBox struct {
	b     *box
	child *box
	hid   string
	mama  store.Person
	mia   store.Person
}

// newRecipeBox is the example family: Mama (a parent, allergic to nothing she
// knows of, but with kiwi in free text) and Mia, who is allergic to peanuts.
func newRecipeBox(t *testing.T) recipeBox {
	t.Helper()
	b := signedInBox(t)
	h, err := b.db.HomeHousehold()
	if err != nil {
		t.Fatal(err)
	}
	child := childSession(t, b)
	people, _ := b.db.People(h.ID)
	rb := recipeBox{b: b, child: child, hid: h.ID}
	for _, p := range people {
		if p.Name == "Mia" {
			rb.mia = p
		} else if p.Role == store.RoleParent {
			rb.mama = p
		}
	}
	if err := b.db.SetPersonFood(h.ID, rb.mia.ID, store.Food{Diet: "omnivore", Allergens: []string{"peanuts"}}); err != nil {
		t.Fatal(err)
	}
	if err := b.db.SetPersonFood(h.ID, rb.mama.ID, store.Food{Diet: "omnivore", Allergens: []string{}, Other: "Kiwi"}); err != nil {
		t.Fatal(err)
	}
	return rb
}

func (rb recipeBox) add(t *testing.T, r recipe.Recipe) store.Recipe {
	t.Helper()
	if r.Category == "" {
		r.Category = recipe.Main
	}
	if r.Portions == 0 {
		r.Portions = 4
	}
	got, err := rb.b.db.CreateRecipe(rb.hid, rb.mama.ID, r)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (rb recipeBox) curry(t *testing.T) store.Recipe {
	return rb.add(t, recipe.Recipe{
		Name: "Erdnuss-Curry mit Süßkartoffeln", Minutes: 45, Mealtimes: []string{"lunch", "dinner"}, Tags: []string{"schnell", "scharf"},
		Tips: "Schmeckt am nächsten Tag besser.", Source: "Kochbuch „Asiatisch für jeden Tag“, Seite 84",
		Ingredients: []recipe.Ingredient{
			{Amount: 600, Unit: "g", Text: "Süßkartoffeln", ArticleID: "sweet-potatoes"},
			{Amount: 3, Unit: "tbsp", Text: "Currypaste"},
			{Amount: 3, Unit: "tbsp", Text: "Erdnussbutter", ArticleID: "peanut-butter", Variety: "cremig"},
			{Text: "Salz", ArticleID: "salt"},
		},
		Steps: []string{"Süßkartoffeln schälen.", "Currypaste anrösten.", "Köcheln lassen."},
	})
}

func rcpBetween(t *testing.T, body, from, to string) string {
	t.Helper()
	i := strings.Index(body, from)
	if i < 0 {
		t.Fatalf("no %q in\n%s", from, body)
	}
	j := strings.Index(body[i:], to)
	if j < 0 {
		t.Fatalf("no %q after %q", to, from)
	}
	return body[i : i+j+len(to)]
}

// shown is the page without its frame: what the failure message prints.
func rcpShown(body string) string {
	if i := strings.Index(body, "<main"); i >= 0 {
		body = body[i:]
	}
	if len(body) > 6000 {
		body = body[:6000] + "..."
	}
	return body
}

// rcpLive is the page without its <template> parts, which no browser sends.
func rcpLive(body string) string {
	return regexp.MustCompile(`(?s)<template.*?</template>`).ReplaceAllString(body, "")
}

func rcpHas(t *testing.T, what, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("%s lacks %q\n%s", what, w, rcpShown(body))
		}
	}
}

func rcpLacks(t *testing.T, what, body string, not ...string) {
	t.Helper()
	for _, w := range not {
		if strings.Contains(body, w) {
			t.Errorf("%s has %q\n%s", what, w, rcpShown(body))
		}
	}
}

// ---- the list

// Acceptance: warnings appear in the recipe list, per person, never blocking.
func TestTheListShowsRecipesFromAToZWithWarningsThatNameThePerson(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	rb.curry(t)
	rb.add(t, recipe.Recipe{Name: "Pfannkuchen", Category: recipe.Breakfast, Minutes: 25, Tags: []string{"Kinder mögen es"},
		Ingredients: []recipe.Ingredient{{Amount: 4, Text: "Eier", ArticleID: "eggs"}}})
	rb.add(t, recipe.Recipe{Name: "Gurkensalat", Category: recipe.Side, Minutes: 150, Portions: 1,
		Ingredients: []recipe.Ingredient{{Amount: 2, Text: "Zwiebeln", ArticleID: "onions"}}})
	r := rb.b.do(http.MethodGet, "/meals/recipes", nil, enHeader)
	if r.status != http.StatusOK {
		t.Fatalf("GET = %d", r.status)
	}
	rcpHas(t, "the list", r.body,
		`<h1>Recipes</h1>`, `<a class="button" href="/meals/recipes/new">`, "New recipe", `<span aria-current="page">Recipes</span>`, `<a href="/meals">Meals</a>`,
		`role="search"`, `placeholder="Name, ingredient or tag"`, `class="lib-filter rcp-filter"`, `<a href="/meals/recipes?category=main"><span class="e" aria-hidden="true">🍲</span>Main dish</a>`,
		`<ul class="list-rows recipes">`, `<span class="art-sym rcp-pic" aria-hidden="true">🍲</span>`, "<b>Erdnuss-Curry mit Süßkartoffeln</b>",
		"<span>Main dish</span><span>·</span><span>45 min</span><span>·</span><span>4 servings</span>",
		"<span>Side</span><span>·</span><span>2 h 30 min</span><span>·</span><span>1 serving</span>",
		`<span class="tag grp">schnell</span>`, `<span class="tag grp">Kinder mögen es</span>`)
	var names []string
	for _, m := range regexp.MustCompile(`<b>([^<]+)</b></?[^>]*><span class="meta">|<a href="/meals/recipes/[^"]+"><span class="art-sym rcp-pic" aria-hidden="true">[^<]+</span><span class="t"><b>([^<]+)</b>`).FindAllStringSubmatch(r.body, -1) {
		names = append(names, m[2])
	}
	if strings.Join(names, "|") != "Erdnuss-Curry mit Süßkartoffeln|Gurkensalat|Pfannkuchen" {
		t.Errorf("order = %v", names)
	}
	curry := rcpBetween(t, r.body, "<b>Erdnuss-Curry", "</li>")
	rcpHas(t, "the curry row", curry, `<span class="allergen-warn">`, "Contains peanuts: ", `<span class="who"><span class="marker" data-person=`, "</span>Mia</span> is allergic", "Not checked automatically: Currypaste")
	rcpLacks(t, "the curry row", curry, "+1", "more")
	eggs := rcpBetween(t, r.body, "<b>Pfannkuchen</b>", "</li>")
	rcpLacks(t, "the pancake row", eggs, "allergen-warn", "Mia")
	// The free-text allergies of the people in the known box, once.
	rcpHas(t, "the list", r.body, `class="unchecked-box"`, "Not checked automatically, cannot be filtered", "Kiwi")
	if n := strings.Count(r.body, `class="unchecked-box"`); n != 1 {
		t.Errorf("%d boxes of free-text allergies", n)
	}
}

func TestTheDietMarkIsAWordWithALeafAndOnlyWhenEveryArticleFits(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	rb.add(t, recipe.Recipe{Name: "Salat", Ingredients: []recipe.Ingredient{{Text: "Zwiebeln", ArticleID: "onions"}, {Text: "Currypaste"}}})
	rb.add(t, recipe.Recipe{Name: "Rührei", Ingredients: []recipe.Ingredient{{Text: "Eier", ArticleID: "eggs"}}})
	rb.add(t, recipe.Recipe{Name: "Pfanne", Ingredients: []recipe.Ingredient{{Text: "Hähnchenbrust", ArticleID: "chicken-breast"}}})
	body := rb.b.do(http.MethodGet, "/meals/recipes", nil, enHeader).body
	rcpHas(t, "salad", rcpBetween(t, body, "<b>Salat</b>", "</li>"), `<span class="tag diet"><svg`, "Vegan</span>")
	rcpHas(t, "scrambled eggs", rcpBetween(t, body, "<b>Rührei</b>", "</li>"), "Vegetarian</span>")
	rcpLacks(t, "the meat row", rcpBetween(t, body, "<b>Pfanne</b>", "</li>"), "tag diet")
}

func TestSeveralPeopleOfOneAllergenAreAllNamedInOneLine(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	extra := map[string]store.Food{}
	for _, name := range []string{"Jonas", "Charlotte-Sophie"} {
		p, err := rb.b.db.AddPerson(rb.hid, store.Person{Name: name, Role: store.RoleChild})
		if err != nil {
			t.Fatal(err)
		}
		extra[p.ID] = store.Food{Diet: "omnivore", Allergens: []string{"eggs"}}
		rb.b.db.SetPersonFood(rb.hid, p.ID, extra[p.ID])
	}
	rb.add(t, recipe.Recipe{Name: "Pfannkuchen", Ingredients: []recipe.Ingredient{{Text: "Eier", ArticleID: "eggs"}, {Text: "Erdnussbutter", ArticleID: "peanut-butter"}}})
	row := rcpBetween(t, rb.b.do(http.MethodGet, "/meals/recipes", nil, enHeader).body, "<b>Pfannkuchen</b>", "</li>")
	rcpHas(t, "row", row, "Contains eggs: ", "Jonas</span> and <span", "Charlotte-Sophie</span> are allergic", "Contains peanuts: ")
	if n := strings.Count(row, `class="allergen-warn"`); n != 2 {
		t.Errorf("%d warning lines, want one per allergen (2)", n)
	}
}

func TestTheSearchFindsNameIngredientAndTagAndTheCategoryFilters(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	rb.curry(t)
	rb.add(t, recipe.Recipe{Name: "Apfelkuchen", Category: recipe.Baking, Tags: []string{"Sonntag"}})
	get := func(q string) string { return rb.b.do(http.MethodGet, "/meals/recipes?"+q, nil, enHeader).body }
	for q, want := range map[string]string{"q=curry": "Erdnuss-Curry", "q=salz": "Erdnuss-Curry", "q=sonntag": "Apfelkuchen", "category=baking": "Apfelkuchen"} {
		body := get(q)
		rcpHas(t, q, body, want)
		other := map[string]string{"Erdnuss-Curry": "Apfelkuchen", "Apfelkuchen": "Erdnuss-Curry"}[want]
		rcpLacks(t, q, body, "<b>"+other)
	}
	none := get("q=Lasagne")
	rcpHas(t, "no hit", none, `<h2 id="res">Search for “Lasagne”</h2>`, "No recipe matches “Lasagne”. Hejmo searches names, ingredients and tags.", `href="/meals/recipes/new?name=Lasagne"`, "Write “Lasagne” as a recipe", "Clear the search")
	cat := get("category=dessert")
	rcpHas(t, "empty category", cat, `<h2 id="res">Dessert</h2>`, "No recipe in “Dessert” yet.", "Show all recipes")
	rcpLacks(t, "empty category for a child", rb.child.do(http.MethodGet, "/meals/recipes?category=dessert", nil, enHeader).body, `href="/meals/recipes/new`)
	rcpHas(t, "the filter keeps the current one", get("category=baking"), `<a href="/meals/recipes?category=baking" aria-current="true">`)
}

func TestTheEmptyListInvitesParentsAndTellsChildren(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	parent := rb.b.do(http.MethodGet, "/meals/recipes", nil, enHeader).body
	rcpHas(t, "parent", parent, "No recipes yet. Start with a dish you cook often", `<a class="button" href="/meals/recipes/new">New recipe</a>`)
	rcpLacks(t, "parent", parent, `role="search"`, "lib-filter")
	kid := rb.child.do(http.MethodGet, "/meals/recipes", nil, enHeader).body
	rcpHas(t, "child", kid, "No recipes yet. Adults write recipes; as soon as there are some, they appear here.")
	rcpLacks(t, "child", kid, "/meals/recipes/new", `role="search"`)
}

// ---- the recipe

// Acceptance: warnings appear in the recipe view, per person, never blocking.
func TestTheRecipeShowsOneAllergyCardAndRepeatsTheWarningAtTheIngredient(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.curry(t)
	page := rb.b.do(http.MethodGet, "/meals/recipes/"+r.ID, nil, enHeader)
	if page.status != http.StatusOK {
		t.Fatalf("GET = %d", page.status)
	}
	rcpHas(t, "the recipe", page.body, `<h1 class="rcp-title"><span class="art-sym big" aria-hidden="true">🍲</span><span>Erdnuss-Curry mit Süßkartoffeln</span></h1>`,
		`<a href="/meals">Meals</a>`, `<a href="/meals/recipes">Recipes</a>`, `<span aria-current="page">Erdnuss-Curry mit Süßkartoffeln</span>`,
		`<dt>Category</dt><dd>Main dish</dd>`, `<dt>Servings</dt><dd>written for 4</dd>`, `<dt>Time</dt><dd>45 minutes</dd>`, "Lunch</span>", "Dinner</span>",
		`<span class="tag diet">`, "All ingredients from the library are vegan. Currypaste is free text; Hejmo does not check it.", `<span class="tag grp">schnell</span>`)
	card := rcpBetween(t, page.body, `<section class="rcp-check has-warn"`, "</section>")
	rcpHas(t, "the card", card, "Allergies in your family", "Contains peanuts: ", "</span>Mia</span> is allergic", "Not checked automatically: Currypaste. This is a free-text ingredient without an article.",
		"Hejmo does not check these allergies:", "Kiwi", "Experimental, no guarantee:")
	ings := rcpBetween(t, page.body, `<ul class="rcp-ings">`, "</ul>")
	rcpHas(t, "ingredients", ings, `<span class="q">600 g</span><span class="n">Süßkartoffeln</span>`, `<span class="q">3 tbsp</span><span class="n">Currypaste</span><span class="tag unchk">free text, not checked</span>`,
		`<span class="n">Erdnussbutter<span class="variety"> · cremig</span></span>`, `<span class="q"></span><span class="n">Salz</span>`)
	if n := strings.Count(rcpBetween(t, ings, "Erdnussbutter", "</li>"), `class="allergen-warn"`); n != 1 {
		t.Errorf("the peanut butter line has %d warnings", n)
	}
	steps := rcpBetween(t, page.body, `<ol class="rcp-steps">`, "</ol>")
	if strings.Count(steps, "<li>") != 3 || !strings.Contains(steps, "Süßkartoffeln schälen.") {
		t.Errorf("steps = %s", steps)
	}
	rcpHas(t, "tips and source", page.body, `<section class="rcp-tips"`, "Schmeckt am nächsten Tag besser.", "Kochbuch „Asiatisch für jeden Tag“, Seite 84")
	rcpLacks(t, "a source that is no address", page.body, `<a class="ext" href="Kochbuch`)
}

func TestTheCardIsThereWithoutAWarningAndSaysWhatHejmoFound(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.add(t, recipe.Recipe{Name: "Gurkensalat mit Dill", Category: recipe.Side, Ingredients: []recipe.Ingredient{{Amount: 2, Text: "Zwiebeln", ArticleID: "onions"}}})
	page := rb.b.do(http.MethodGet, "/meals/recipes/"+r.ID, nil, enHeader).body
	card := rcpBetween(t, page, `<section class="rcp-check"`, "</section>")
	rcpHas(t, "card", card, "No warning: nothing in the ingredients is something someone in your family is allergic to.", "Hejmo does not check these allergies:", "Kiwi")
	rcpLacks(t, "card", card, "has-warn", `class="allergen-warn"`)
	rcpLacks(t, "an empty part", page, `<dt>Tags</dt>`, `<dt>Source</dt>`, `rcp-tips`)
	rcpHas(t, "every meal", page, "<dt>Goes with</dt><dd>Every meal</dd>")
}

func TestADietRowNamesWhatPreventsVegan(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.add(t, recipe.Recipe{Name: "Spätzle", Ingredients: []recipe.Ingredient{{Text: "Eier", ArticleID: "eggs"}, {Text: "Butter", ArticleID: "butter"}, {Text: "Zwiebeln", ArticleID: "onions"}}})
	page := rb.b.do(http.MethodGet, "/meals/recipes/"+r.ID, nil, enHeader).body
	rcpHas(t, "diet", page, "Vegetarian</span>", "All ingredients from the library are vegetarian, not vegan: Eier, Butter.")
	m := rb.add(t, recipe.Recipe{Name: "Schnitzel", Ingredients: []recipe.Ingredient{{Text: "Hähnchenbrust", ArticleID: "chicken-breast"}}})
	meat := rb.b.do(http.MethodGet, "/meals/recipes/"+m.ID, nil, enHeader).body
	rcpHas(t, "meat", meat, "No mark, because not vegetarian: Hähnchenbrust.")
	rcpLacks(t, "meat", meat, `<span class="tag diet">`)
}

func TestASourceThatIsAWebAddressBecomesALink(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.add(t, recipe.Recipe{Name: "Kuchen", Source: "https://www.beispiel.de/kuchen?a=1&b=2"})
	page := rb.b.do(http.MethodGet, "/meals/recipes/"+r.ID, nil, enHeader).body
	rcpHas(t, "link", page, `<a class="ext" href="https://www.beispiel.de/kuchen?a=1&amp;b=2" rel="noopener noreferrer" target="_blank">`)
	j := rb.add(t, recipe.Recipe{Name: "Fies", Source: "javascript:alert(1)"})
	rcpLacks(t, "a script address", rb.b.do(http.MethodGet, "/meals/recipes/"+j.ID, nil, enHeader).body, `href="javascript:`)
}

func TestParentsSeeChangeDeleteAndWhoWroteIt(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.curry(t)
	page := rb.b.do(http.MethodGet, "/meals/recipes/"+r.ID, nil, enHeader).body
	rcpHas(t, "parent", page, `<a class="button secondary" href="/meals/recipes/`+r.ID+`/edit">`, "Change recipe", `<details class="danger">`, "Delete recipe",
		"“Erdnuss-Curry mit Süßkartoffeln” disappears with its ingredients, steps and tips for the whole family. This cannot be undone.",
		`action="/meals/recipes/`+r.ID+`/delete"`, "Yes, delete “Erdnuss-Curry mit Süßkartoffeln”", "No, change nothing", `class="hint rcp-who"`, "Added by")
}

// Acceptance: children can open recipes but not change them.
func TestChildrenOpenRecipesWithTheSameWarningsButCannotChangeThem(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.curry(t)
	list := rb.child.do(http.MethodGet, "/meals/recipes", nil, enHeader)
	if list.status != http.StatusOK {
		t.Fatalf("child list = %d", list.status)
	}
	rcpHas(t, "child list", list.body, "What the family likes to cook.", "Contains peanuts: ")
	rcpLacks(t, "child list", list.body, "/meals/recipes/new", "New recipe")
	page := rb.child.do(http.MethodGet, "/meals/recipes/"+r.ID, nil, enHeader)
	if page.status != http.StatusOK {
		t.Fatalf("child page = %d", page.status)
	}
	rcpHas(t, "child page", page.body, "Contains peanuts: ", "Allergies in your family", "Süßkartoffeln schälen.")
	rcpLacks(t, "child page", page.body, "/edit", "Change recipe", "Delete recipe", `class="danger"`, "Added by", "rcp-who")
	for _, c := range []struct {
		method, path string
		form         url.Values
	}{
		{http.MethodGet, "/meals/recipes/new", nil},
		{http.MethodGet, "/meals/recipes/" + r.ID + "/edit", nil},
		{http.MethodPost, "/meals/recipes", url.Values{"name": {"Eis"}}},
		{http.MethodPost, "/meals/recipes/" + r.ID, url.Values{"name": {"Eis"}}},
		{http.MethodPost, "/meals/recipes/" + r.ID + "/delete", url.Values{}},
	} {
		if got := rb.child.do(c.method, c.path, c.form, enHeader); got.status != http.StatusForbidden {
			t.Errorf("child %s %s = %d, want 403", c.method, c.path, got.status)
		}
	}
	if got, err := rb.b.db.Recipe(rb.hid, r.ID); err != nil || got.Name != "Erdnuss-Curry mit Süßkartoffeln" {
		t.Errorf("the recipe was changed: %+v %v", got, err)
	}
}

func TestAnUnknownRecipeIs404(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	for _, p := range []string{"/meals/recipes/nope", "/meals/recipes/nope/edit"} {
		if got := rb.b.do(http.MethodGet, p, nil, enHeader); got.status != http.StatusNotFound {
			t.Errorf("GET %s = %d", p, got.status)
		}
	}
	if got := rb.b.do(http.MethodPost, "/meals/recipes/nope/delete", url.Values{}, enHeader); got.status != http.StatusNotFound {
		t.Errorf("delete = %d", got.status)
	}
}

func TestTheMealPlanLinksToTheRecipes(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	for _, b := range []*box{rb.b, rb.child} {
		rcpHas(t, "meals page", b.do(http.MethodGet, "/meals", nil, rcpDE).body, `<a class="button secondary" href="/meals/recipes">`, "Rezepte</a>")
	}
}

// ---- the form

func TestTheNewFormHasOnePageThreeIngredientLinesTwoStepsAndPreselectsMainDish(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.b.do(http.MethodGet, "/meals/recipes/new", nil, enHeader)
	if r.status != http.StatusOK {
		t.Fatalf("GET = %d", r.status)
	}
	rcpHas(t, "form", r.body, `<h1>New recipe</h1>`, `<span aria-current="page">New recipe</span>`, `class="steps rcp-form"`, `name="name"`, `maxlength="120"`,
		`<input type="radio" name="category" value="main" checked>`, `id="portions"`, `value="2"`, // Mama and Mia
		`id="mins"`, "minutes", `<summary><svg class="icon chev"`, "Nothing entered yet", `<button class="button" type="submit">Save recipe</button>`, `class="cancel"`)
	if n := strings.Count(rcpLive(r.body), `name="ing_name"`); n != 3 {
		t.Errorf("%d ingredient lines, want 3", n)
	}
	if n := strings.Count(rcpLive(r.body), `name="step"`); n != 2 {
		t.Errorf("%d step fields, want 2", n)
	}
	if n := strings.Count(r.body, `type="radio" name="category"`); n != 6 {
		t.Errorf("%d categories", n)
	}
	opts := rcpBetween(t, r.body, `<select class="iu"`, "</select>")
	rcpHas(t, "units", opts, `<option value="" selected>no unit</option>`, `value="g"`, `value="tbsp">tbsp</option>`, `value="tsp"`, `value="pinch"`, `value="can">can</option>`, `value="clove"`, `value="punnet"`, `value="pot">pot</option>`, `value="bunch"`)
	if strings.Count(opts, `value="bunch"`) != 1 {
		t.Errorf("bunch is offered twice")
	}
	rcpHas(t, "steps and ingredients hold only the first button", r.body, `<button class="vh" type="submit" tabindex="-1" aria-hidden="true"></button>`)
	if i, j := strings.Index(r.body, `class="vh" type="submit"`), strings.Index(r.body, `name="act"`); i < 0 || j < 0 || i > j {
		t.Errorf("Enter must press save, not a line button")
	}
	kids := rb.b.do(http.MethodGet, "/meals/recipes/new?name=Lasagne", nil, enHeader).body
	rcpHas(t, "name from the search", kids, `value="Lasagne"`)
}

func rcpFormOf(name string, extra url.Values) url.Values {
	f := url.Values{"name": {name}, "category": {"main"}, "portions": {"4"}, "minutes": {""}, "new_tag": {""}, "tips": {""}, "source": {""}}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

func lines(amount, unit, name []string) url.Values {
	n := len(name)
	v := url.Values{"ing_amount": amount, "ing_unit": unit, "ing_name": name, "ing_variety": make([]string, n), "ing_article": make([]string, n)}
	return v
}

func (rb recipeBox) save(t *testing.T, path string, f url.Values) resp {
	t.Helper()
	return rb.b.do(http.MethodPost, path, f, enHeader)
}

func (rb recipeBox) onlyRecipe(t *testing.T) store.Recipe {
	t.Helper()
	list, err := rb.b.db.Recipes(rb.hid)
	if err != nil || len(list) != 1 {
		t.Fatalf("recipes = %v, %v", list, err)
	}
	return list[0]
}

func TestAParentWritesARecipeWithOnlyAName(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	f := rcpFormOf("Pfannkuchen", lines([]string{"", "", ""}, []string{"", "", ""}, []string{"", "", ""}))
	f["step"] = []string{"", ""}
	r := rb.save(t, "/meals/recipes", f)
	if r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/meals/recipes/") {
		t.Fatalf("save = %d %s\n%s", r.status, r.location, r.body)
	}
	got := rb.onlyRecipe(t)
	if got.Name != "Pfannkuchen" || got.Category != recipe.Main || got.Portions != 4 || len(got.Ingredients) != 0 || len(got.Steps) != 0 || got.CreatedBy != rb.mama.ID {
		t.Errorf("stored = %+v", got)
	}
	page := rb.b.do(http.MethodGet, r.location, nil, enHeader).body
	rcpHas(t, "after saving", page, "“Pfannkuchen” saved. The whole family sees the recipe now.", `role="status"`)
}

func TestTheFormReadsQuantitiesTheWayPeopleWriteThem(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	f := rcpFormOf("Suppe", lines(
		[]string{"½", "1 1/2", "0,5", "2", "", ""},
		[]string{"l", "kg", "l", "", "pinch", ""},
		[]string{"Milch", "Mehl", "Wasser", "Eier", "Salz", ""}))
	f["step"] = []string{"Kochen.", ""}
	if r := rb.save(t, "/meals/recipes", f); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	got := rb.onlyRecipe(t)
	want := []struct {
		amount float64
		unit   string
		text   string
	}{{0.5, "l", "Milch"}, {1.5, "kg", "Mehl"}, {0.5, "l", "Wasser"}, {2, "", "Eier"}, {1, "pinch", "Salz"}}
	if len(got.Ingredients) != len(want) {
		t.Fatalf("ingredients = %+v: the empty line is not saved", got.Ingredients)
	}
	for i, w := range want {
		if in := got.Ingredients[i]; in.Amount != w.amount || in.Unit != w.unit || in.Text != w.text {
			t.Errorf("ingredient %d = %+v, want %+v (a unit without a quantity means one)", i, in, w)
		}
	}
	if len(got.Steps) != 1 || got.Steps[0] != "Kochen." {
		t.Errorf("steps = %q: the empty step is not saved", got.Steps)
	}
}

func TestWithoutScriptAnIngredientIsMatchedWithTheLibraryOnSave(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	f := rcpFormOf("Curry", lines([]string{"3", "2", "1"}, []string{"tbsp", "", "can"}, []string{"Erdnussbutter cremig", "Knoblauch Zehen", "Currypaste scharf"}))
	if r := rb.save(t, "/meals/recipes", f); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	got := rb.onlyRecipe(t)
	in := got.Ingredients
	if in[0].ArticleID != "peanut-butter" || in[0].Variety != "cremig" || in[0].Text != "Erdnussbutter" {
		t.Errorf("peanut butter = %+v", in[0])
	}
	if in[1].ArticleID != "garlic" || in[1].Variety != "Zehen" {
		t.Errorf("garlic = %+v", in[1])
	}
	if in[2].ArticleID != "" || in[2].Text != "Currypaste scharf" || in[2].Variety != "" {
		t.Errorf("free text = %+v", in[2])
	}
	// A warning never blocks saving: Mia is allergic to peanuts.
	page := rb.b.do(http.MethodGet, "/meals/recipes/"+got.ID, nil, enHeader).body
	rcpHas(t, "page", page, "Contains peanuts: ", "3 tbsp", "1 can")
}

func TestAChosenSuggestionKeepsItsVarietyWhenTheScriptFillsNameAndArticle(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	// What the script posts for a chosen option: the name with the variety after a middle dot, the article id, and the variety it also sets.
	l := lines([]string{"3", "2"}, []string{"tbsp", ""}, []string{"Erdnussbutter · cremig", "Erdnussbutter · cremig"})
	l["ing_article"] = []string{"peanut-butter", "peanut-butter"}
	l["ing_variety"] = []string{"", "cremig"}
	if r := rb.save(t, "/meals/recipes", rcpFormOf("Curry", l)); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	for i, in := range rb.onlyRecipe(t).Ingredients {
		if in.ArticleID != "peanut-butter" || in.Variety != "cremig" || in.Text != "Peanut butter" {
			t.Errorf("line %d = %+v, want peanut butter, variety cremig", i, in)
		}
	}
}

func TestARetypedNameDoesNotKeepTheVarietyOfTheOldArticleWithoutTheScript(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	// What the form sends without script: the line was "Weizenmehl · Type 405", the name was retyped to Zucker, the hidden fields kept the old article and variety.
	l := lines([]string{"200"}, []string{"g"}, []string{"Zucker"})
	l["ing_article"] = []string{"wheat-flour"}
	l["ing_variety"] = []string{"Type 405"}
	if r := rb.save(t, "/meals/recipes", rcpFormOf("Kuchen", l)); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	for _, in := range rb.onlyRecipe(t).Ingredients {
		if in.Variety != "" {
			t.Errorf("a retyped name kept the old variety: %+v", in)
		}
	}
}

func TestAVarietyTypedAfterTheDotFillsAnEmptyVarietyField(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	l := lines([]string{"3", "3"}, []string{"tbsp", "tbsp"}, []string{"Erdnussbutter · stückig", "Erdnussbutter · stückig"})
	l["ing_article"] = []string{"", "peanut-butter"}
	l["ing_variety"] = []string{"", ""}
	if r := rb.save(t, "/meals/recipes", rcpFormOf("Curry", l)); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	for i, in := range rb.onlyRecipe(t).Ingredients {
		if in.ArticleID != "peanut-butter" || in.Variety != "stückig" {
			t.Errorf("line %d = %+v, want peanut butter, variety stückig", i, in)
		}
	}
}

func TestAVarietyTypedInTheNameWinsOverADifferentOneInTheField(t *testing.T) {
	t.Parallel()
	for _, article := range []string{"", "wheat-flour"} {
		for _, name := range []string{"Weizenmehl · Type 550", "Weizenmehl Type 550"} {
			rb := newRecipeBox(t)
			l := lines([]string{"200"}, []string{"g"}, []string{name})
			l["ing_article"] = []string{article}
			l["ing_variety"] = []string{"Type 405"}
			if r := rb.save(t, "/meals/recipes", rcpFormOf("Kuchen", l)); r.status != http.StatusSeeOther {
				t.Fatalf("%q/%q: save = %d\n%s", name, article, r.status, r.body)
			}
			if in := rb.onlyRecipe(t).Ingredients[0]; in.ArticleID != "wheat-flour" || in.Variety != "Type 550" {
				t.Errorf("%q with article %q: line = %+v, want wheat flour, variety Type 550 (what was typed)", name, article, in)
			}
		}
	}
}

func TestAChangeOnlyInTheVarietyFieldIsKept(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	l := lines([]string{"200"}, []string{"g"}, []string{"Weizenmehl"})
	l["ing_article"] = []string{"wheat-flour"}
	l["ing_variety"] = []string{"Type 550"}
	if r := rb.save(t, "/meals/recipes", rcpFormOf("Kuchen", l)); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	if in := rb.onlyRecipe(t).Ingredients[0]; in.ArticleID != "wheat-flour" || in.Variety != "Type 550" {
		t.Errorf("line = %+v, want wheat flour, variety Type 550", in)
	}
}

func TestAVarietyClearedInItsFieldIsGone(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	l := lines([]string{"200"}, []string{"g"}, []string{"Weizenmehl"})
	l["ing_article"] = []string{"wheat-flour"}
	l["ing_variety"] = []string{""}
	if r := rb.save(t, "/meals/recipes", rcpFormOf("Kuchen", l)); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	if in := rb.onlyRecipe(t).Ingredients[0]; in.Variety != "" {
		t.Errorf("a cleared variety stayed: %+v", in)
	}
}

func TestAVarietyDeletedFromTheNameStaysInItsField(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	l := lines([]string{"200"}, []string{"g"}, []string{"Weizenmehl"})
	l["ing_article"] = []string{"wheat-flour"}
	l["ing_variety"] = []string{"Type 405"}
	if r := rb.save(t, "/meals/recipes", rcpFormOf("Kuchen", l)); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	if in := rb.onlyRecipe(t).Ingredients[0]; in.Variety != "Type 405" {
		t.Errorf("line = %+v, want variety Type 405", in)
	}
}

// inputValue is the value of the first <input> named name on the page, whatever the order of its attributes.
func inputValue(t *testing.T, page, name string) string {
	t.Helper()
	tag, attr := regexp.MustCompile(`<input\b[^>]*>`), regexp.MustCompile(`\b(\w[\w-]*)="([^"]*)"`)
	for _, in := range tag.FindAllString(page, -1) {
		got := map[string]string{}
		for _, m := range attr.FindAllStringSubmatch(in, -1) {
			got[m[1]] = html.UnescapeString(m[2])
		}
		if got["name"] == name {
			return got["value"]
		}
	}
	t.Fatalf("no input %q on the page", name)
	return ""
}

func TestTheEditFormKeepsTheVarietyOutOfTheNameField(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	l := lines([]string{"200"}, []string{"g"}, []string{"Weizenmehl"})
	l["ing_article"] = []string{"wheat-flour"}
	l["ing_variety"] = []string{"Type 405"}
	if r := rb.save(t, "/meals/recipes", rcpFormOf("Kuchen", l)); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	page := rb.b.do(http.MethodGet, "/meals/recipes/"+rb.onlyRecipe(t).ID+"/edit", nil, rcpDE).body
	if got := inputValue(t, page, "ing_name"); got != "Wheat flour" {
		t.Errorf("name field = %q, want the name alone", got)
	}
	if got := inputValue(t, page, "ing_variety"); got != "Type 405" {
		t.Errorf("variety field = %q, want Type 405", got)
	}
}

func TestACraftedCategoryIsRefusedInWordsNotWithTheStoresMessage(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	for _, extra := range []url.Values{{"category": {"bogus"}}} {
		f := rcpFormOf("Crafted", lines(nil, nil, nil))
		for k, v := range extra {
			f[k] = v
		}
		r := rb.save(t, "/meals/recipes", f)
		if r.status != http.StatusUnprocessableEntity {
			t.Fatalf("%v = %d", extra, r.status)
		}
		for _, raw := range []string{"unknown category", ">category<"} {
			if strings.Contains(r.body, raw) {
				t.Errorf("%v shows the raw %q", extra, raw)
			}
		}
	}
	if list, _ := rb.b.db.Recipes(rb.hid); len(list) != 0 {
		t.Errorf("a refused recipe was stored: %v", list)
	}
}

func TestACraftedMealtimeIsNamedInTheSummaryLink(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	f := rcpFormOf("Crafted", lines(nil, nil, nil))
	f["mealtime"] = []string{"midnight"}
	r := rb.save(t, "/meals/recipes", f)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("save = %d", r.status)
	}
	if !strings.Contains(r.body, "the mealtimes") || strings.Contains(r.body, "unknown mealtime") {
		t.Errorf("the summary does not name the mealtimes in words:\n%s", r.body)
	}
	if !strings.Contains(r.body, `id="err-mealtimes"`) {
		t.Errorf("the error is not shown at the mealtimes field:\n%s", r.body)
	}
	if !strings.Contains(r.body, `<fieldset class="tag-pick" aria-invalid="true" aria-describedby="err-mealtimes">`) {
		t.Errorf("the mealtimes checkboxes are not linked to their error:\n%s", r.body)
	}
}

func TestTheEditFormTellsInWordsWhatHejmoMadeOfEachIngredient(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.curry(t)
	page := rb.b.do(http.MethodGet, "/meals/recipes/"+r.ID+"/edit", nil, enHeader)
	if page.status != http.StatusOK {
		t.Fatalf("GET = %d", page.status)
	}
	rcpHas(t, "edit form", page.body, `<h1>Change recipe</h1>`, "Changes apply to everyone as soon as you save.", `<a href="/meals/recipes/`+r.ID+`">Erdnuss-Curry mit Süßkartoffeln</a>`, `<span aria-current="page">Change recipe</span>`,
		`<p class="ing-state ok">`, "From the library: <b>Sweet potatoes</b>", `<b>Peanut butter<span class="variety"> · cremig</span></b>`, `<span class="v-state chosen">cremig</span>`,
		`<p class="ing-state free">`, "Free text: Hejmo does not know “Currypaste” and does not check it for allergens.",
		`value="Süßkartoffeln"`, `value="600"`, `value="Currypaste"`, `<option value="tbsp" selected>tbsp</option>`, "Contains peanuts: ",
		`<button class="button" type="submit">Save</button>`, `href="/meals/recipes/`+r.ID+`"`)
	if n := strings.Count(rcpLive(page.body), `name="ing_name"`); n != 5 { // four ingredients and one blank line
		t.Errorf("%d ingredient lines, want 5", n)
	}
	if n := strings.Count(rcpLive(page.body), `name="step"`); n != 4 { // three steps and one blank
		t.Errorf("%d step fields, want 4", n)
	}
	// steps in order, each its own field
	first, second := strings.Index(page.body, "Süßkartoffeln schälen."), strings.Index(page.body, "Currypaste anrösten.")
	if first < 0 || second < first {
		t.Errorf("steps are not in order")
	}
	rcpHas(t, "more details", page.body, "Goes with, tags, tips and source entered", `<input type="checkbox" name="mealtime" value="lunch" checked>`, `<input type="checkbox" name="tag" value="schnell" checked>`)
}

func TestAParentChangesARecipeAndTheNameKeepsItsCaseRulesWithItself(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.curry(t)
	f := rcpFormOf("ERDNUSS-CURRY MIT SÜSSKARTOFFELN", lines([]string{"2"}, []string{"kg"}, []string{"Süßkartoffeln"}))
	f["step"] = []string{"Nur ein Schritt."}
	f["mealtime"] = []string{"dinner"}
	f["tag"] = []string{"schnell"}
	f["new_tag"] = []string{"Sonntag, Schnell"}
	f["tips"] = []string{"Neuer Tipp."}
	f["source"] = []string{"https://www.beispiel.de/curry"}
	rb.b.db.CreateRecipe(rb.hid, rb.mama.ID, recipe.Recipe{Name: "Anderes", Category: recipe.Main, Portions: 2})
	// ... the name of the recipe itself is not taken, whatever its case
	if got := rb.save(t, "/meals/recipes/"+r.ID, f); got.status != http.StatusSeeOther || got.location != "/meals/recipes/"+r.ID {
		t.Fatalf("save = %d %s\n%s", got.status, got.location, got.body)
	}
	got, _ := rb.b.db.Recipe(rb.hid, r.ID)
	if got.Name != "ERDNUSS-CURRY MIT SÜSSKARTOFFELN" || len(got.Steps) != 1 || got.Tips != "Neuer Tipp." || got.UpdatedBy != rb.mama.ID {
		t.Errorf("stored = %+v", got)
	}
	if strings.Join(got.Tags, "|") != "schnell|Sonntag" {
		t.Errorf("tags = %q: a new tag takes the spelling already in use", got.Tags)
	}
	if len(got.Ingredients) != 1 || got.Ingredients[0].Amount != 2 || got.Ingredients[0].Unit != "kg" {
		t.Errorf("ingredients = %+v", got.Ingredients)
	}
}

// ---- errors at the field

func TestAnErrorIsShownAtTheFieldWithALinkAndEverythingTypedStays(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	rb.add(t, recipe.Recipe{Name: "Pfannkuchen"})
	f := rcpFormOf("pfannkuchen", lines(
		[]string{"250", "zwei", "500", ""},
		[]string{"g", "", "ml", ""},
		[]string{"Weizenmehl", "Eier", "", "Salz"}))
	f["step"] = []string{"Alles verrühren.", "Backen."}
	f["tips"] = []string{"Ein Tipp."}
	r := rb.save(t, "/meals/recipes", f)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("save = %d", r.status)
	}
	rcpHas(t, "errors", r.body, `<p class="error form-error" role="alert">`, "Not saved yet. Please check ", `<a href="#rcp">the name</a>`, `<a href="#ing2">ingredient 2</a>`, `<a href="#ing3">ingredient 3</a>`, "Everything you typed is still there.",
		"There is already a recipe “pfannkuchen”.", "Grandma’s pfannkuchen", "open the existing one", `<p class="ferr" id="err-ing2" role="alert">`, "Write the quantity as a number, for example 2, 0.5 or ½.",
		"“500 ml” has no ingredient. Type it in or clear the quantity.", `aria-invalid="true"`)
	rcpHas(t, "typed text stays", r.body, `value="pfannkuchen"`, `value="zwei"`, `value="250"`, `value="Weizenmehl"`, `value="Salz"`, ">Alles verrühren.</textarea>", ">Backen.</textarea>", "Ein Tipp.")
	if list, _ := rb.b.db.Recipes(rb.hid); len(list) != 1 {
		t.Errorf("a refused recipe was stored: %d recipes", len(list))
	}
	existing, _ := rb.b.db.Recipes(rb.hid)
	rcpHas(t, "link to the existing recipe", r.body, `href="/meals/recipes/`+existing[0].ID+`"`)
}

func TestAMissingNameIsAnErrorAtTheNameAndNothingElseIsLost(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	f := rcpFormOf("  ", lines([]string{"1"}, []string{"g"}, []string{"Salz"}))
	f["step"] = []string{"Salzen."}
	r := rb.save(t, "/meals/recipes", f)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("save = %d", r.status)
	}
	rcpHas(t, "error", r.body, "Give the recipe a name.", `<a href="#rcp">the name</a>`, `value="Salz"`, ">Salzen.</textarea>")
}

func TestPortionsAndTimeMustBeNumbers(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	f := rcpFormOf("Suppe", nil)
	f["portions"], f["minutes"] = []string{"viele"}, []string{"-5"}
	r := rb.save(t, "/meals/recipes", f)
	rcpHas(t, "errors", r.body, "Write the servings as a number from 1 to 99.", "Write the time as a number of minutes", `<a href="#rcp">the servings</a>`, `<a href="#rcp">the time</a>`, `value="viele"`)
}

// ---- lines without script

func TestTheLineButtonsChangeTheFormAndNeverSaveIt(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	base := func() url.Values {
		f := rcpFormOf("Suppe", lines([]string{"1", "", ""}, []string{"l", "", ""}, []string{"Wasser", "", ""}))
		f["step"] = []string{"Eins.", "Zwei.", "Drei."}
		return f
	}
	post := func(act string) string {
		f := base()
		f.Set("act", act)
		r := rb.save(t, "/meals/recipes", f)
		if r.status != http.StatusOK {
			t.Fatalf("%s = %d\n%s", act, r.status, r.body)
		}
		return r.body
	}
	stepsOf := func(body string) string {
		var out []string
		for _, m := range regexp.MustCompile(`(?s)<textarea name="step"[^>]*>(.*?)</textarea>`).FindAllStringSubmatch(rcpLive(body), -1) {
			out = append(out, m[1])
		}
		return strings.Join(out, "|")
	}
	if got := stepsOf(post("down-step-1")); got != "Zwei.|Eins.|Drei." {
		t.Errorf("down = %s", got)
	}
	if got := stepsOf(post("up-step-3")); got != "Eins.|Drei.|Zwei." {
		t.Errorf("up = %s", got)
	}
	if got := stepsOf(post("rm-step-2")); got != "Eins.|Drei." {
		t.Errorf("remove = %s", got)
	}
	if got := stepsOf(post("more-step")); got != "Eins.|Zwei.|Drei.|" {
		t.Errorf("more steps = %s", got)
	}
	if n := strings.Count(rcpLive(post("more-ing")), `name="ing_name"`); n != 4 {
		t.Errorf("more ingredients: %d lines, want the three posted and a new one", n)
	}
	if n := strings.Count(post("rm-ing-1"), `value="Wasser"`); n != 0 {
		t.Errorf("remove ingredient: Wasser is still there")
	}
	if list, _ := rb.b.db.Recipes(rb.hid); len(list) != 0 {
		t.Errorf("a line button saved the recipe")
	}
}

// ---- deleting

func TestDeletingARecipeNamesItAndAnswersOnTheList(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.curry(t)
	del := rb.b.do(http.MethodPost, "/meals/recipes/"+r.ID+"/delete", url.Values{}, rcpDE)
	if del.status != http.StatusSeeOther || del.location != "/meals/recipes" {
		t.Fatalf("delete = %d %s", del.status, del.location)
	}
	list := rb.b.do(http.MethodGet, del.location, nil, rcpDE).body
	rcpHas(t, "list", list, "„Erdnuss-Curry mit Süßkartoffeln“ gelöscht.")
	if _, err := rb.b.db.Recipe(rb.hid, r.ID); err == nil {
		t.Error("the recipe is still there")
	}
}

func TestTheGermanPagesSpeakGerman(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.curry(t)
	rcpHas(t, "list", rb.b.do(http.MethodGet, "/meals/recipes", nil, rcpDE).body, "<h1>Rezepte</h1>", "Neues Rezept", "Enthält Erdnüsse: ", "ist allergisch", "Nicht automatisch geprüft: Currypaste", "45 Min.", "4 Portionen", "Hauptgericht")
	rcpHas(t, "page", rb.b.do(http.MethodGet, "/meals/recipes/"+r.ID, nil, rcpDE).body, "Allergien in eurer Familie", "3 EL", "Zubereitung", "Rezept löschen", "freier Text, nicht geprüft", "45 Minuten")
	rcpHas(t, "form", rb.b.do(http.MethodGet, "/meals/recipes/"+r.ID+"/edit", nil, rcpDE).body, "Rezept ändern", "ohne Einheit", "Hoch", "Runter", "Entfernen", "Weitere Zutat", "Weiterer Schritt", `label="Küchenmaße"`, `label="Packungen und Stückmaße"`)
}

// The script of the form asks for suggestions while an ingredient is typed; the warning is in the row already.
func TestSuggestionsUnderAnIngredientLineCarryTheWarningAndEndWithFreeText(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	if _, err := rb.b.db.AddItem(rb.hid, mustList(t, rb), store.Item{Text: "Erdnussbutter", ArticleID: "peanut-butter", Variety: "cremig"}, nil); err != nil {
		t.Fatal(err)
	}
	r := rb.b.do(http.MethodGet, "/meals/recipes/suggest?q=Erdnuss&n=3", nil, enHeader)
	if r.status != http.StatusOK || strings.Contains(r.body, "<main") {
		t.Fatalf("GET = %d, a fragment without the frame:\n%s", r.status, r.body)
	}
	rcpHas(t, "suggestions", r.body, `id="sq3"`, "articles match “Erdnuss”", `role="listbox"`, `data-article="peanuts-salted" data-name="Salted peanuts"`, `data-article="peanut-butter" data-name="Peanut butter"`,
		`data-variety="cremig"`, "Variety of Peanut butter, used before", "Contains peanuts: ", "</span>Mia</span> is allergic", `class="free" data-free`, "Use “Erdnuss” as free text", "does not warn about allergens")
	if i, j := strings.Index(r.body, "data-free"), strings.LastIndex(r.body, `role="option"`); i < 0 || j > i {
		t.Errorf("free text is the last option")
	}
	if short := rb.b.do(http.MethodGet, "/meals/recipes/suggest?q=E&n=3", nil, enHeader); short.status != http.StatusNoContent {
		t.Errorf("one letter = %d, want 204", short.status)
	}
	none := rb.b.do(http.MethodGet, "/meals/recipes/suggest?q=Currypaste&n=1", nil, rcpDE)
	rcpHas(t, "nothing matches", none.body, "Kein Artikel passt zu „Currypaste“.", "„Currypaste“ als freien Text verwenden")
	if got := rb.child.do(http.MethodGet, "/meals/recipes/suggest?q=Erdnuss&n=3", nil, enHeader); got.status != http.StatusForbidden {
		t.Errorf("child = %d, want 403", got.status)
	}
}

func TestTheFormLoadsItsScriptAndKeepsTemplatesForNewLines(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	body := rb.b.do(http.MethodGet, "/meals/recipes/new", nil, enHeader).body
	rcpHas(t, "form", body, `<script src="/static/recipe.js" defer></script>`, `data-suggest="/meals/recipes/suggest"`, `<template data-line="ing">`, `<template data-line="step">`,
		`id="ing{n}"`, `value="rm-step-{n}"`, `aria-label="Step {n}"`, `data-format="%d of %d characters"`)
	live := rcpLive(body)
	rcpLacks(t, "live form", live, "{n}")
}

func mustList(t *testing.T, rb recipeBox) string {
	t.Helper()
	l, err := rb.b.db.CreateList(rb.hid, list.List{Name: "Einkauf", Color: "blue", Symbol: "circle-empty", Sort: list.Manual, Suggestions: true})
	if err != nil {
		t.Fatal(err)
	}
	return l.ID
}

// The trail of the preview: "Essen › Rezepte › Name", and "› Rezept ändern" below a recipe.
func TestTheTrailLeadsFromMealsThroughTheRecipes(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.curry(t)
	wantTrail(t, "list", rb.b.do(http.MethodGet, "/meals/recipes", nil, rcpDE).body, part("Essen", "/meals"), part("Rezepte", ""))
	wantTrail(t, "recipe", rb.b.do(http.MethodGet, "/meals/recipes/"+r.ID, nil, rcpDE).body, part("Essen", "/meals"), part("Rezepte", "/meals/recipes"), part("Erdnuss-Curry mit Süßkartoffeln", ""))
	wantTrail(t, "new", rb.b.do(http.MethodGet, "/meals/recipes/new", nil, rcpDE).body, part("Essen", "/meals"), part("Rezepte", "/meals/recipes"), part("Neues Rezept", ""))
	wantTrail(t, "edit", rb.b.do(http.MethodGet, "/meals/recipes/"+r.ID+"/edit", nil, rcpDE).body, part("Essen", "/meals"), part("Rezepte", "/meals/recipes"), part("Erdnuss-Curry mit Süßkartoffeln", "/meals/recipes/"+r.ID), part("Rezept ändern", ""))
	wantTrail(t, "child", rb.child.do(http.MethodGet, "/meals/recipes/"+r.ID, nil, rcpDE).body, part("Essen", "/meals"), part("Rezepte", "/meals/recipes"), part("Erdnuss-Curry mit Süßkartoffeln", ""))
}
