package server_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 288 (ADR 0021, preview-288.html): nutrition values per portion on the
// recipe page, calculated from the ingredients with BLS 4.0 and USDA.

// oatmeal is 100 g of rolled oats for one portion: BLS C133000, per 100 g
// 1465 kJ, 348 kcal, fat 6.65, saturates 1.34, carbohydrate 53.3, sugars 0.74,
// protein 13.22, salt 0.005.
func (rb recipeBox) oatmeal(t *testing.T) store.Recipe {
	return rb.add(t, recipe.Recipe{Name: "Haferbrei", Portions: 1, Ingredients: []recipe.Ingredient{{Amount: 100, Unit: "g", Text: "Haferflocken", ArticleID: "rolled-oats"}}})
}

func (rb recipeBox) page(t *testing.T, who *box, id string, header map[string]string) string {
	t.Helper()
	r := who.do(http.MethodGet, "/meals/recipes/"+id, nil, header)
	if r.status != http.StatusOK {
		t.Fatalf("GET recipe = %d", r.status)
	}
	return r.body
}

func nutCard(t *testing.T, body string) string {
	t.Helper()
	return rcpBetween(t, body, `<section class="rcp-nutri"`, "</section>")
}

func TestTheRecipeShowsTheSevenValuesPerPortionInWordsAndWithTheSource(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.oatmeal(t)
	card := nutCard(t, rb.page(t, rb.b, r.ID, rcpDE))
	rcpHas(t, "the card", card,
		`id="nut"`, `aria-labelledby="nut-h"`, `<h2 id="nut-h">`, "Nährwerte pro Portion",
		`<p class="nut-lead">Richtwerte, aus den Zutaten berechnet. Das Rezept ist für 1 Portion.</p>`,
		`<p class="none">`, "Alle Zutaten sind mitgezählt.",
		`<table class="nut-table"><caption class="sr">Nährwerte pro Portion</caption>`,
		`<tr><th scope="row">Energie</th><td><span class="kcal">348 kcal</span><span class="kj">1.465 kJ</span></td></tr>`,
		`<tr><th scope="row">Fett</th><td>6,7 g</td></tr>`,
		`<tr class="nut-sub"><th scope="row">davon gesättigte Fettsäuren</th><td>1,3 g</td></tr>`,
		`<tr><th scope="row">Kohlenhydrate</th><td>53 g</td></tr>`,
		`<tr class="nut-sub"><th scope="row">davon Zucker</th><td>0,7 g</td></tr>`,
		`<tr><th scope="row">Eiweiß</th><td>13 g</td></tr>`,
		`<tr><th scope="row">Salz</th><td>unter 0,1 g</td></tr>`,
		`<p class="hint nut-src">Quelle: Bundeslebensmittelschlüssel (BLS) 4.0, Max Rubner-Institut, Lizenz CC BY 4.0. <a href="/settings/licences">Lizenzen</a></p>`)
	rcpLacks(t, "the card", card, "nut-gap", "nut-na", "USDA")
}

func TestTheValuesAreForOnePortionHoweverManyPeopleCook(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.add(t, recipe.Recipe{Name: "Haferbrei für vier", Portions: 4, Ingredients: []recipe.Ingredient{{Amount: 400, Unit: "g", Text: "Haferflocken", ArticleID: "rolled-oats"}}})
	card := nutCard(t, rb.page(t, rb.b, r.ID, rcpDE))
	rcpHas(t, "the card", card, "Das Rezept ist für 4 Portionen.", `<span class="kcal">348 kcal</span>`)
}

func TestTheSourceIsNamedInEnglishToo(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	card := nutCard(t, rb.page(t, rb.b, rb.oatmeal(t).ID, enHeader))
	rcpHas(t, "the card", card, "Nutrition per portion", "Guide values, calculated from the ingredients. The recipe makes 1 portion.", "All ingredients are counted.",
		`<span class="kcal">348 kcal</span><span class="kj">1,465 kJ</span>`, `<tr><th scope="row">Fat</th><td>6.7 g</td></tr>`, `of which saturates`, "of which sugars", `<th scope="row">Carbohydrate</th>`, `<th scope="row">Protein</th>`,
		`<th scope="row">Salt</th><td>under 0.1 g</td>`, "Source: German Nutrient Database (BLS) 4.0, Max Rubner-Institut, licence CC BY 4.0. ", `<a href="/settings/licences">Licences</a>`)
}

func TestUnitsWithAKnownWeightCountForNutritionOnly(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	// 3 eggs of 50 g, 2 tbsp olive oil (2 x 15 ml x 0.91 g/ml = 27.3 g), 1 pinch of salt (0.3 ml x 1.2 g/ml).
	r := rb.add(t, recipe.Recipe{Name: "Rührei", Portions: 1, Ingredients: []recipe.Ingredient{
		{Amount: 3, Unit: "piece", Text: "Eier", ArticleID: "eggs"},
		{Amount: 2, Unit: "tbsp", Text: "Olivenöl", ArticleID: "olive-oil"},
		{Amount: 1, Unit: "pinch", Text: "Salz", ArticleID: "salt"},
	}})
	card := nutCard(t, rb.page(t, rb.b, r.ID, rcpDE))
	rcpLacks(t, "the card", card, "nut-gap", "Unvollständig", "lässt sich nicht")
	// 150 g egg (135 kcal/100 g) + 27.3 g oil (899 kcal/100 g) = 202.5 + 245.4 = 448 kcal.
	rcpHas(t, "the card", card, `<span class="kcal">448 kcal</span>`)
}

func TestWhatHejmoCannotCountIsNamedWithTheReason(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.add(t, recipe.Recipe{Name: "Erdnuss-Curry", Portions: 2, Ingredients: []recipe.Ingredient{
		{Amount: 600, Unit: "g", Text: "Süßkartoffeln", ArticleID: "sweet-potatoes"},
		{Amount: 3, Unit: "tbsp", Text: "Currypaste"},
		{Text: "Gemüsebrühe"},
		{Text: "Salz", ArticleID: "salt"},
		{Amount: 1, Unit: "handful", Text: "Heidelbeeren", ArticleID: "blueberries"},
		{Amount: 2, Unit: "clove", Text: "Knoblauch", ArticleID: "garlic"},
	}})
	card := nutCard(t, rb.page(t, rb.b, r.ID, rcpDE))
	gap := rcpBetween(t, card, `<div class="nut-gap" role="note">`, "</div>")
	rcpHas(t, "the gap", gap, `<span class="flag warn">`, "<span>Unvollständig</span>", "Ohne diese Zutaten gerechnet, die echten Werte sind also höher:",
		"<li><b>Currypaste</b>: freier Text, Hejmo kennt kein Lebensmittel dazu</li>",
		"<li><b>Gemüsebrühe</b>: freier Text, ohne Menge</li>",
		"<li><b>Salz</b>: ohne Menge</li>",
		"<li><b>Heidelbeeren</b>: „Handvoll“ lässt sich nicht in Gramm umrechnen</li>",
		"<li><b>Knoblauch</b>: „Zehe“ lässt sich nicht in Gramm umrechnen</li>")
	rcpLacks(t, "the gap", gap, "Süßkartoffeln")
	// Only the sweet potatoes count: 300 g per portion, 119 kcal per 100 g.
	rcpHas(t, "the card", card, `<span class="kcal">357 kcal</span>`)
	rcpLacks(t, "the card", card, "Alle Zutaten sind mitgezählt")
}

func TestAMissingValueOfAFoodIsNeverShownAsZero(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	// BLS Apfelsaft has no fat value: the fat row says so, the other rows are numbers.
	r := rb.add(t, recipe.Recipe{Name: "Apfelsaft", Portions: 1, Ingredients: []recipe.Ingredient{{Amount: 200, Unit: "ml", Text: "Apfelsaft", ArticleID: "apple-juice"}}})
	card := nutCard(t, rb.page(t, rb.b, r.ID, rcpDE))
	rcpHas(t, "the fat row", card, `<tr><th scope="row">Fett</th><td><span class="nut-na">unvollständig</span></td></tr>`)
	rcpHas(t, "the gap", card, "Für diese Werte fehlt eine Angabe:", "<li><b>Fett</b>: kein Wert für Apfelsaft (BLS 4.0)</li>", "Unvollständig")
	rcpLacks(t, "the fat row", card, `<th scope="row">Fett</th><td>0`, `<th scope="row">Fett</th><td>unter`)
	if n := strings.Count(card, "nut-na"); n != 1 {
		t.Errorf("%d unknown rows, want only fat", n)
	}
	rcpHas(t, "the other rows", card, `<span class="kcal">`, `<th scope="row">Kohlenhydrate</th><td>`)
}

func TestNothingCountableSaysSoAndShowsNoTable(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.add(t, recipe.Recipe{Name: "Gulasch", Portions: 6, Ingredients: []recipe.Ingredient{{Text: "ordentlich Rindfleisch vom Metzger"}, {Text: "Paprikapulver edelsüß"}}})
	card := nutCard(t, rb.page(t, rb.b, r.ID, rcpDE))
	rcpHas(t, "the card", card, "<span>Keine Nährwerte</span>", "Hejmo kann keine Zutat dieses Rezepts mitzählen:", "<li><b>ordentlich Rindfleisch vom Metzger</b>: freier Text, ohne Menge</li>", "Das Rezept ist für 6 Portionen.")
	rcpLacks(t, "the card", card, "nut-table", "nut-src", "Quelle", "kcal", "Unvollständig")
}

func TestARecipeWithoutIngredientsHasOneSentence(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.add(t, recipe.Recipe{Name: "Idee", Portions: 4})
	card := nutCard(t, rb.page(t, rb.b, r.ID, rcpDE))
	rcpHas(t, "the card", card, "Noch keine Zutaten. Hejmo rechnet die Nährwerte, sobald das Rezept Zutaten hat.")
	rcpLacks(t, "the card", card, "nut-table", "nut-src", "nut-gap")
}

func TestAnOwnArticleCountsWithItsFoodAndItsPieceWeight(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	with, err := rb.b.db.CreateArticle(rb.hid, rb.mama.ID, store.OwnArticle{Name: "Omas Apfelmus", Dept: "pantry", Kind: "vegan", Unit1: "pack", Food: "bls:F110100", PieceG: 200}, nil)
	if err != nil {
		t.Fatal(err)
	}
	without, err := rb.b.db.CreateArticle(rb.hid, rb.mama.ID, store.OwnArticle{Name: "Omas Pflaumenmus", Dept: "pantry", Kind: "vegan", Unit1: "pack"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := rb.add(t, recipe.Recipe{Name: "Mus", Portions: 1, Ingredients: []recipe.Ingredient{
		{Amount: 1, Unit: "piece", Text: "Omas Apfelmus", ArticleID: with.ID},
		{Amount: 1, Unit: "piece", Text: "Omas Pflaumenmus", ArticleID: without.ID},
	}})
	parent := nutCard(t, rb.page(t, rb.b, r.ID, rcpDE))
	// 200 g apple (BLS F110100, 58 kcal per 100 g).
	rcpHas(t, "parent", parent, `<span class="kcal">116 kcal</span>`, "<li><b>Omas Pflaumenmus</b>: eigener Artikel ohne Lebensmittel. ", `<a href="/settings/lists/library/`+without.ID+`#food">Lebensmittel wählen</a>`)
	rb.b.db.SetNutritionShown(rb.hid, rb.mia.ID, true)
	kid := nutCard(t, rb.page(t, rb.child, r.ID, rcpDE))
	rcpHas(t, "child", kid, "<li><b>Omas Pflaumenmus</b>: eigener Artikel ohne Lebensmittel</li>")
	rcpLacks(t, "child", kid, "Lebensmittel wählen", "/settings/")
}

// Acceptance: hidden for children unless switched on.
func TestChildrenSeeNoCardAndNoHintUntilAParentSwitchesItOn(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.oatmeal(t)
	off := rb.page(t, rb.child, r.ID, rcpDE)
	rcpLacks(t, "the child's page", off, "rcp-nutri", "Nährwerte", "kcal", "nut-")
	rcpHas(t, "the child's page", off, "Haferbrei", "Allergien in eurer Familie") // the rest of the recipe stays
	rb.b.db.SetNutritionShown(rb.hid, rb.mia.ID, true)
	on := rb.page(t, rb.child, r.ID, rcpDE)
	card := nutCard(t, on)
	rcpHas(t, "the child's card", card, "Nährwerte pro Portion", `<span class="kcal">348 kcal</span>`, "Quelle: Bundeslebensmittelschlüssel (BLS) 4.0, Max Rubner-Institut, Lizenz CC BY 4.0.")
	rcpLacks(t, "the child's card", card, `href="/settings/licences"`, "nut-who", "Eltern sehen", "Kinder:", "Bei den Personen")
	rb.b.db.SetNutritionShown(rb.hid, rb.mia.ID, false)
	rcpLacks(t, "switched off again", rb.page(t, rb.child, r.ID, rcpDE), "rcp-nutri")
}

func TestParentsSeeWhichChildrenSeeTheValuesByName(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	seb, err := rb.b.db.AddPerson(rb.hid, store.Person{Name: "Sebastian", Role: store.RoleChild})
	if err != nil {
		t.Fatal(err)
	}
	r := rb.oatmeal(t)
	who := func(header map[string]string) string {
		return rcpBetween(t, nutCard(t, rb.page(t, rb.b, r.ID, header)), `<p class="nut-who">`, "</p>")
	}
	// Both off: one sentence, both named.
	rcpHas(t, "all off", who(rcpDE), "Eltern sehen die Nährwerte immer, Wandbildschirme nie. ", "Eure Kinder sehen sie nicht: aus für ", "</span>Mia</span> und <span", "</span>Sebastian</span>", `<a href="/people">Bei den Personen ändern</a>`)
	rb.b.db.SetNutritionShown(rb.hid, seb.ID, true)
	mixed := who(rcpDE)
	rcpHas(t, "mixed", mixed, "Kinder: an für <span", "</span>Sebastian</span>, aus für <span", "</span>Mia</span>. ")
	rb.b.db.SetNutritionShown(rb.hid, rb.mia.ID, true)
	rcpHas(t, "all on", who(rcpDE), "Eure Kinder sehen sie: an für ", "Mia</span> und <span")
	rcpHas(t, "English", who(enHeader), "Parents always see the values, wall displays never. ", "Your children see them: on for ", `Change on the person pages`)
	if strings.Contains(who(rcpDE), "+") {
		t.Error("people are named, never counted")
	}
}

func TestParentsInAHouseholdWithoutChildrenGetNoChildrenSentence(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	hh, _ := b.db.HomeHousehold()
	people, _ := b.db.People(hh.ID)
	var mama store.Person
	for _, p := range people {
		if p.Role == store.RoleChild {
			t.Fatalf("the box has a child: %s", p.Name)
		}
		mama = p
	}
	r, _ := b.db.CreateRecipe(hh.ID, mama.ID, recipe.Recipe{Name: "Haferbrei", Category: recipe.Main, Portions: 1, Ingredients: []recipe.Ingredient{{Amount: 100, Unit: "g", Text: "Haferflocken", ArticleID: "rolled-oats"}}})
	card := nutCard(t, b.do(http.MethodGet, "/meals/recipes/"+r.ID, nil, rcpDE).body)
	rcpHas(t, "the card", card, "Eltern sehen die Nährwerte immer, Wandbildschirme nie.")
	rcpLacks(t, "the card", card, "Kinder", "Bei den Personen")
}

// Acceptance: never on displays; only on the recipe page.
func TestNutritionIsOnlyOnTheRecipePage(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.oatmeal(t)
	rb.b.db.SetNutritionShown(rb.hid, rb.mia.ID, true)
	for _, path := range []string{"/meals/recipes", "/meals/recipes/starter", "/meals", "/meals/recipes/" + r.ID + "/edit"} {
		for _, who := range []*box{rb.b, rb.child} {
			body := who.do(http.MethodGet, path, nil, rcpDE).body
			if strings.Contains(body, "rcp-nutri") || strings.Contains(body, "kcal") {
				t.Errorf("%s shows nutrition values", path)
			}
		}
	}
}

func TestAStarterRecipeGetsItsValuesToo(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	body := rb.b.do(http.MethodGet, "/meals/recipes/starter-spaghetti-bolognese", nil, rcpDE)
	if body.status != http.StatusOK {
		t.Fatalf("GET = %d", body.status)
	}
	card := nutCard(t, body.body)
	rcpHas(t, "the card", card, "Nährwerte pro Portion", "Eltern sehen die Nährwerte immer", `<span class="kcal">`)
	if !regexp.MustCompile(`<span class="kcal">\d+ kcal</span>`).MatchString(card) {
		t.Errorf("no energy value in\n%s", card)
	}
}

func TestOnlyTheSourcesUsedAreNamed(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.add(t, recipe.Recipe{Name: "Mit Parmesan", Portions: 1, Ingredients: []recipe.Ingredient{{Amount: 30, Unit: "g", Text: "Parmesan", ArticleID: "parmesan"}}})
	card := nutCard(t, rb.page(t, rb.b, r.ID, rcpDE))
	rcpHas(t, "USDA only", card, "Quelle: USDA FoodData Central, gemeinfrei (CC0). ")
	rcpLacks(t, "USDA only", card, "Bundeslebensmittelschlüssel", "Max Rubner")
	r2 := rb.add(t, recipe.Recipe{Name: "Beides", Portions: 1, Ingredients: []recipe.Ingredient{{Amount: 30, Unit: "g", Text: "Parmesan", ArticleID: "parmesan"}, {Amount: 100, Unit: "g", Text: "Haferflocken", ArticleID: "rolled-oats"}}})
	rcpHas(t, "both", nutCard(t, rb.page(t, rb.b, r2.ID, rcpDE)), "Quelle: Bundeslebensmittelschlüssel (BLS) 4.0, Max Rubner-Institut, Lizenz CC BY 4.0; USDA FoodData Central, gemeinfrei (CC0). ")
}

func TestTheCardComesAfterTipsAndBeforeWhoWroteIt(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.curry(t)
	body := rb.page(t, rb.b, r.ID, rcpDE)
	tips, card, who := strings.Index(body, `class="rcp-tips"`), strings.Index(body, `class="rcp-nutri"`), strings.Index(body, `class="hint rcp-who"`)
	if !(tips > 0 && tips < card && card < who) {
		t.Errorf("order tips %d, card %d, who %d", tips, card, who)
	}
}
