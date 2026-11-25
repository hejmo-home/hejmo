package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 288 (preview-288 screens 11 to 15): the optional section "Nährwerte"
// of the form of an own article: a food search that works without script, the
// chosen food, and the weight of a piece.

func (rb recipeBox) ownArticle(t *testing.T, name, foodID string, pieceG float64) store.OwnArticle {
	t.Helper()
	a, err := rb.b.db.CreateArticle(rb.hid, rb.mama.ID, store.OwnArticle{Name: name, Dept: "pantry", Kind: "vegan", Unit1: "piece", Food: foodID, PieceG: pieceG}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// foodForm is the form as the browser sends it after the page of an article.
func foodForm(name string, kv ...string) url.Values {
	v := url.Values{"name": {name}, "dept": {"pantry"}, "kind": {"vegan"}, "unit1": {"piece"}}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v
}

func articlePath(a store.OwnArticle) string { return "/settings/lists/library/" + a.ID }

func TestTheArticleFormHasTheOptionalSectionNaehrwerte(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	a := rb.ownArticle(t, "Omas Pflaumenmus", "", 0)
	rb.add(t, recipe.Recipe{Name: "Germknödel", Ingredients: []recipe.Ingredient{{Amount: 1, Unit: "piece", Text: "Omas Pflaumenmus", ArticleID: a.ID}}})
	rb.add(t, recipe.Recipe{Name: "Porridge mit Heidelbeeren", Ingredients: []recipe.Ingredient{{Amount: 1, Unit: "tbsp", Text: "Omas Pflaumenmus", ArticleID: a.ID}}})
	body := rb.b.do(http.MethodGet, articlePath(a), nil, rcpDE).body
	sec := rcpBetween(t, body, `<fieldset id="food" class="food-pick">`, "</fieldset>")
	rcpHas(t, "the section", sec, `<legend>Nährwerte <span class="tag hidden-tag">freiwillig</span></legend>`,
		"Wähle das Lebensmittel, das am besten passt. Damit rechnen Rezepte mit „Omas Pflaumenmus“ ihre Nährwerte. Ohne Lebensmittel zählt Hejmo diesen Artikel nicht mit und sagt das beim Rezept.",
		`<p class="food-none">Noch kein Lebensmittel gewählt. Rezepte zählen diesen Artikel nicht mit.</p>`,
		`<label>Lebensmittel suchen<input type="search" name="foodq" value="" placeholder="zum Beispiel Pflaumenmus">`,
		`<button class="button secondary" type="submit" name="foodsearch" value="1" formnovalidate>Suchen</button>`,
		`<label>Ein Stück wiegt etwa (Gramm)<input type="text" inputmode="decimal" name="piece_g" value=""`,
		"Nur für Rezepte, die diesen Artikel in Stück, Glas oder Becher angeben. Leer lassen, wenn du es nicht weißt: dann zählt Hejmo solche Zeilen nicht mit.")
	rcpHas(t, "the facts", body, "<dt>In Rezepten</dt>", "Germknödel</a>", "Porridge mit Heidelbeeren</a>")
	if !(strings.Index(body, `id="food"`) > strings.Index(body, `name="name"`)) {
		t.Error("the section comes after the name")
	}
	// The form of a new article has it too.
	rcpHas(t, "the new form", rb.b.do(http.MethodGet, "/settings/lists/library/new", nil, rcpDE).body, `<fieldset id="food" class="food-pick">`)
}

func TestTheFoodSearchShowsChoiceCardsWithValuesPer100g(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	a := rb.ownArticle(t, "Omas Pflaumenmus", "", 0)
	r := rb.b.do(http.MethodPost, articlePath(a), foodForm("Omas Pflaumenmus (neu)", "foodq", "Pflaumenmus", "foodsearch", "1"), rcpDE)
	if r.status != http.StatusOK {
		t.Fatalf("search = %d", r.status)
	}
	sec := rcpBetween(t, r.body, `<fieldset id="food" class="food-pick">`, `<div class="f short">`)
	rcpHas(t, "the results", sec, `name="foodq" value="Pflaumenmus"`, `<p class="results-head">`, " Lebensmittel zu „Pflaumenmus“ ", `<button class="linklike" type="submit" name="foodclearq" value="1" formnovalidate>Suche aufheben</button>`,
		`<fieldset class="choice"><legend class="sr">Lebensmittel</legend>`, `<label><input type="radio" name="food" value="bls:S161000"><b>Pflaumenmus</b><small>100 g: 195 kcal · `, " g Fett · ", " g Kohlenhydrate · davon ", " g Zucker</small></label>")
	rcpHas(t, "what was typed stays", r.body, "Omas Pflaumenmus (neu)")
	if got, _ := rb.b.db.Article(rb.hid, a.ID); got.Name != "Omas Pflaumenmus" || got.Food != "" {
		t.Errorf("a search saved something: %+v", got)
	}
	// "Suche aufheben" puts the field back to empty and lists nothing.
	r = rb.b.do(http.MethodPost, articlePath(a), foodForm("Omas Pflaumenmus", "foodq", "Pflaumenmus", "foodclearq", "1"), rcpDE)
	rcpHas(t, "cleared", r.body, `name="foodq" value=""`)
	rcpLacks(t, "cleared", r.body, "results-head", `name="food" value=`)
}

func TestAFoodSearchWithoutAHitSaysSo(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	a := rb.ownArticle(t, "Omas Pflaumenmus", "", 0)
	r := rb.b.do(http.MethodPost, articlePath(a), foodForm("Omas Pflaumenmus", "foodq", "Qzxwvu", "foodsearch", "1"), rcpDE)
	rcpHas(t, "no hit", r.body, `<div class="empty" data-empty><p>Kein Lebensmittel zu „Qzxwvu“. Versuche ein allgemeineres Wort.</p>`, `type="submit" name="foodclearq" value="1" formnovalidate>Suche aufheben</button>`)
	rcpLacks(t, "no hit", r.body, `name="food" value=`)
}

func TestTheChosenFoodIsSavedAndShownWithItsSource(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	a := rb.ownArticle(t, "Omas Pflaumenmus", "", 0)
	r := rb.b.do(http.MethodPost, articlePath(a), foodForm("Omas Pflaumenmus", "foodq", "Pflaumenmus", "food", "bls:S161000", "piece_g", "12,5"), rcpDE)
	if r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, rcpShown(r.body))
	}
	got, _ := rb.b.db.Article(rb.hid, a.ID)
	if got.Food != "bls:S161000" || got.PieceG != 12.5 {
		t.Fatalf("stored %q %v", got.Food, got.PieceG)
	}
	page := rb.b.do(http.MethodGet, articlePath(a), nil, rcpDE).body
	sec := rcpBetween(t, page, `<fieldset id="food" class="food-pick">`, "</fieldset>")
	rcpHas(t, "chosen", sec, `<p class="food-chosen" role="status">`, "Gewählt: Pflaumenmus", "100 g: 195 kcal · ", "· aus BLS 4.0</small>",
		`<button class="button secondary" type="submit" name="foodclear" value="1" formnovalidate>Kein Lebensmittel</button>`, `<input type="hidden" name="food_chosen" value="bls:S161000">`,
		`<label>Anderes Lebensmittel suchen<input type="search" name="foodq"`, `name="piece_g" value="12,5"`)
	rcpLacks(t, "chosen", sec, "food-none")
	// Saving again without touching the section keeps the food.
	rb.b.do(http.MethodPost, articlePath(a), foodForm("Omas Pflaumenmus", "food_chosen", "bls:S161000", "piece_g", "12,5"), rcpDE)
	if got, _ := rb.b.db.Article(rb.hid, a.ID); got.Food != "bls:S161000" {
		t.Errorf("the food was lost: %q", got.Food)
	}
}

func TestKeinLebensmittelTakesTheFoodAwayWhenSaved(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	a := rb.ownArticle(t, "Omas Pflaumenmus", "bls:S161000", 300)
	r := rb.b.do(http.MethodPost, articlePath(a), foodForm("Omas Pflaumenmus", "food_chosen", "bls:S161000", "foodclear", "1", "piece_g", "300"), rcpDE)
	if r.status != http.StatusOK {
		t.Fatalf("clear = %d", r.status)
	}
	rcpHas(t, "after clearing", r.body, `class="food-none"`)
	rcpLacks(t, "after clearing", r.body, "food-chosen", `name="food_chosen"`)
	if got, _ := rb.b.db.Article(rb.hid, a.ID); got.Food != "bls:S161000" {
		t.Error("the button only changes the page; saving stores it")
	}
	rb.b.do(http.MethodPost, articlePath(a), foodForm("Omas Pflaumenmus", "piece_g", "300"), rcpDE)
	if got, _ := rb.b.db.Article(rb.hid, a.ID); got.Food != "" || got.PieceG != 300 {
		t.Errorf("saved %q %v: the food goes, the piece weight stays", got.Food, got.PieceG)
	}
}

func TestASearchWithoutAChoiceIsAnErrorAtTheCardsAndNothingIsSaved(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	a := rb.ownArticle(t, "Omas Pflaumenmus", "", 0)
	r := rb.b.do(http.MethodPost, articlePath(a), foodForm("Ein anderer Name", "foodq", "Pflaumenmus"), rcpDE)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("save = %d", r.status)
	}
	sec := rcpBetween(t, r.body, `<fieldset id="food" class="food-pick">`, `<div class="f short">`)
	rcpHas(t, "the error", sec, `<fieldset class="choice" aria-invalid="true" aria-describedby="err-food">`, `<p class="ferr" id="err-food" role="alert">`,
		"Wähle eines der Lebensmittel oder hebe die Suche auf. Dein Artikel bleibt sonst ohne Nährwerte.", `name="food" value="bls:S161000"`)
	rcpHas(t, "what was typed stays", r.body, "Ein anderer Name")
	if got, _ := rb.b.db.Article(rb.hid, a.ID); got.Name != "Omas Pflaumenmus" {
		t.Error("a refused form saved something")
	}
	// A food that is already chosen makes the leftover search no error.
	b := rb.ownArticle(t, "Apfelmus", "bls:Y843143", 0)
	if r := rb.b.do(http.MethodPost, articlePath(b), foodForm("Apfelmus", "food_chosen", "bls:Y843143", "foodq", "Pflaume"), rcpDE); r.status != http.StatusSeeOther {
		t.Errorf("save with a chosen food and a leftover search = %d", r.status)
	}
}

func TestThePieceWeightIsAGramNumber(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	a := rb.ownArticle(t, "Omas Pflaumenmus", "", 0)
	for _, bad := range []string{"viel", "-5", "5001", "0,0001x"} {
		r := rb.b.do(http.MethodPost, articlePath(a), foodForm("Omas Pflaumenmus", "piece_g", bad), rcpDE)
		if r.status != http.StatusUnprocessableEntity {
			t.Errorf("%q: save = %d, want 422", bad, r.status)
			continue
		}
		rcpHas(t, bad, r.body, `<p class="ferr" id="err-piece_g" role="alert">`, "Gib das Gewicht in Gramm als Zahl an, zum Beispiel 120 oder 12,5 (höchstens 5.000).", `name="piece_g" value="`+bad+`"`)
	}
	for in, want := range map[string]float64{"120": 120, "12,5": 12.5, "12.5": 12.5, "": 0, " 80 ": 80} {
		if r := rb.b.do(http.MethodPost, articlePath(a), foodForm("Omas Pflaumenmus", "piece_g", in), rcpDE); r.status != http.StatusSeeOther {
			t.Fatalf("%q: save = %d", in, r.status)
		}
		if got, _ := rb.b.db.Article(rb.hid, a.ID); got.PieceG != want {
			t.Errorf("%q stored as %v, want %v", in, got.PieceG, want)
		}
	}
}

func TestANewArticleCanBeCreatedWithAFood(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.b.do(http.MethodPost, "/settings/lists/library", foodForm("Powidl", "food", "bls:S161000", "piece_g", "350"), rcpDE)
	if r.status != http.StatusSeeOther {
		t.Fatalf("create = %d\n%s", r.status, rcpShown(r.body))
	}
	arts, _ := rb.b.db.Articles(rb.hid)
	if len(arts) != 1 || arts[0].Food != "bls:S161000" || arts[0].PieceG != 350 {
		t.Fatalf("articles = %+v", arts)
	}
}

func TestOnlyParentsReachTheFoodSearch(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	a := rb.ownArticle(t, "Omas Pflaumenmus", "", 0)
	r := rb.child.do(http.MethodPost, articlePath(a), foodForm("Omas Pflaumenmus", "foodq", "Pflaumenmus", "foodsearch", "1"), rcpDE)
	if r.status == http.StatusOK {
		t.Errorf("a child got the search page: %d", r.status)
	}
}

func TestAShippedArticleHasNoFoodForm(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	body := rb.b.do(http.MethodGet, "/settings/lists/library/wheat-flour", nil, rcpDE).body
	rcpLacks(t, "a shipped article", body, `id="food"`)
}
