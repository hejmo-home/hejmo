package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 281 (ADR 0015: HTML routes with API parity, preview-281.html): a
// recipe in the meal plan. The test clock stands at Saturday 14/11/2026.

const today = "2026-11-14"

var dinnerCell = mealField(today, "dinner")

// mealBox is the example family of the recipe pages with two more people, so the household has four.
type mealBox struct {
	recipeBox
}

func newMealBox(t *testing.T) mealBox {
	t.Helper()
	rb := newRecipeBox(t)
	for _, n := range []string{"Papa", "Sebastian"} {
		if _, err := rb.b.db.AddPerson(rb.hid, store.Person{Name: n, Role: store.RoleChild}); err != nil {
			t.Fatal(err)
		}
	}
	return mealBox{rb}
}

// plan stores meals like a save of the page does.
func (mb mealBox) plan(t *testing.T, ms ...store.Meal) {
	t.Helper()
	o := mealOptionsForTest()
	o.Today = today
	if err := mb.b.db.SetMeals(mb.hid, ms, o); err != nil {
		t.Fatal(err)
	}
}

func (mb mealBox) page(t *testing.T, header map[string]string) string {
	t.Helper()
	r := mb.b.do(http.MethodGet, "/meals", nil, header)
	if r.status != http.StatusOK {
		t.Fatalf("GET /meals = %d", r.status)
	}
	return r.body
}

// cellOf is the markup of one meal row of the page.
func cellOf(t *testing.T, body, day, mealtime string) string {
	t.Helper()
	fs := rcpBetween(t, body, `data-day="`+day+`"`, `</fieldset>`)
	start := regexp.MustCompile(`<div class="mrow[^"]*" data-mt="` + mealtime + `">`).FindStringIndex(fs)
	if start == nil {
		t.Fatalf("no %s row on %s", mealtime, day)
	}
	end := strings.Index(fs[start[1]:], `<div class="mrow`)
	if end < 0 {
		return fs[start[0]:]
	}
	return fs[start[0] : start[1]+end]
}

func form(week string, kv ...string) url.Values {
	v := url.Values{"week": {week}}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v
}

// Acceptance: a recipe is a framed box with its name as a link, its portions and its warnings.
func TestARecipeCellIsAFramedBoxWithNameLinkPortionsAndWarnings(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	curry := mb.curry(t)
	mb.plan(t, store.Meal{Day: today, Mealtime: meal.Dinner, RecipeID: curry.ID, Portions: 6}, store.Meal{Day: today, Mealtime: meal.Lunch, Text: "Nudeln mit Tomatensoße"})
	body := mb.page(t, enHeader)
	row := rcpBetween(t, body, `<div class="mrow has-rcp" data-mt="dinner">`, `Different dish</button>`)
	rcpHas(t, "the recipe row", row,
		`<span class="mt" id="`+dinnerCell+`-mt">`, `<div class="rcp-entry" role="group"`, `<input type="hidden" name="`+dinnerCell+`.recipe" value="`+curry.ID+`">`,
		`<div class="rcp-head"><span class="art-sym" aria-hidden="true">🍲</span>`,
		`<a class="rcp-name" id="`+dinnerCell+`-name" href="/meals/recipes/`+curry.ID+`?portions=6&amp;plan=`+today+`/dinner">Erdnuss-Curry mit Süßkartoffeln</a>`,
		`<span>Recipe · Main dish · 45 min</span>`,
		`<div class="portions amount"`, `<label for="`+dinnerCell+`.portions">Portions</label>`, `<div class="stepper">`, `data-step="down"`, `data-step="up"`,
		`name="`+dinnerCell+`.portions" type="text" inputmode="numeric" value="6"`, `you are 4`,
		`<span class="allergen-warn">`, "Contains peanuts: ", "</span>Mia</span> is allergic",
		`<span class="unchecked">`, "Not checked automatically: Currypaste",
		`<button class="button secondary" type="submit" name="other" value="`+dinnerCell+`" formnovalidate>`)
	rcpLacks(t, "the recipe row", row, "<textarea")
	// A text cell is the known text field; its hidden fields wait for a recipe.
	lunch := cellOf(t, body, today, "lunch")
	rcpHas(t, "the text row", lunch, `<textarea id="`+mealField(today, "lunch")+`" name="`+mealField(today, "lunch")+`" rows="1" maxlength="200">Nudeln mit Tomatensoße</textarea>`,
		`<input type="hidden" name="`+mealField(today, "lunch")+`.recipe" value="">`)
	rcpLacks(t, "the text row", lunch, "rcp-entry")
	if n := strings.Count(body, `class="rcp-entry"`); n != 1 {
		t.Errorf("%d recipe boxes, want 1", n)
	}
	rcpHas(t, "the page", body, `<button class="vh" type="submit" tabindex="-1" aria-hidden="true"></button>`, `class="steps wide meal-form"`)
	// Portions equal to the household show "for all".
	mb.plan(t, store.Meal{Day: today, Mealtime: meal.Dinner, RecipeID: curry.ID, Portions: 4})
	rcpHas(t, "four portions", mb.page(t, enHeader), "for all 4")
}

// Acceptance: portions default to household size (the page, without script).
func TestATextThatEqualsARecipeNameBecomesThatRecipeWithTheHouseholdsPortions(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	curry := mb.curry(t)
	r := mb.b.do(http.MethodPost, "/meals", form("2026-11-09", dinnerCell, "  erdnuss-CURRY mit süßkartoffeln "), enHeader)
	if r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, rcpShown(r.body))
	}
	list, _ := mb.b.db.MealPlan(mb.hid, today, today, today)
	if len(list) != 1 || list[0].RecipeID != curry.ID || list[0].Portions != 4 || list[0].Text != curry.Name {
		t.Fatalf("plan = %+v: the name is a recipe, for the four people of the household", list)
	}
	// A text that was saved before and did not change stays text, even if a recipe of that name came later.
	mb.plan(t, store.Meal{Day: today, Mealtime: meal.Lunch, Text: "Gurkensalat"})
	mb.add(t, recipe.Recipe{Name: "Gurkensalat", Category: recipe.Side})
	mb.b.post("/meals", form("2026-11-09", mealField(today, "lunch"), "Gurkensalat"))
	list, _ = mb.b.db.MealPlan(mb.hid, today, today, today)
	if len(list) != 2 || list[0].RecipeID != "" || list[0].Text != "Gurkensalat" {
		t.Errorf("unchanged text = %+v", list)
	}
	mb.b.post("/meals", form("2026-11-09", mealField(today, "lunch"), "Gurkensalat "+"extra"))
	mb.b.post("/meals", form("2026-11-09", mealField(today, "lunch"), "gurkensalat"))
	list, _ = mb.b.db.MealPlan(mb.hid, today, today, today)
	if list[0].RecipeID == "" {
		t.Errorf("changed text that equals a recipe = %+v", list[0])
	}
}

func TestARecipeWithPortionsIsSavedWithTheWeekAndShownAgain(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	curry := mb.curry(t)
	r := mb.b.do(http.MethodPost, "/meals", form("2026-11-09", dinnerCell+".recipe", curry.ID, dinnerCell+".portions", " 7 ", mealField(today, "lunch"), "Suppe"), enHeader)
	if r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, rcpShown(r.body))
	}
	list, _ := mb.b.db.MealPlan(mb.hid, today, today, today)
	if len(list) != 2 || list[1].RecipeID != curry.ID || list[1].Portions != 7 || list[0].Text != "Suppe" || list[0].RecipeID != "" {
		t.Fatalf("plan = %+v", list)
	}
	// Free text round-trips unchanged and a recipe goes back to text.
	mb.b.post("/meals", form("2026-11-09", dinnerCell, "Pizza", dinnerCell+".recipe", ""))
	list, _ = mb.b.db.MealPlan(mb.hid, today, today, today)
	if list[1].RecipeID != "" || list[1].Portions != 0 || list[1].Text != "Pizza" {
		t.Errorf("back to text = %+v", list[1])
	}
}

// "Anderes Gericht" turns the box into an empty text field and re-renders; nothing is saved.
func TestDifferentDishTurnsTheBoxBackIntoAnEmptyFieldAndSavesNothing(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	curry := mb.curry(t)
	mb.plan(t, store.Meal{Day: today, Mealtime: meal.Dinner, RecipeID: curry.ID, Portions: 6})
	r := mb.b.do(http.MethodPost, "/meals", form("2026-11-09", dinnerCell+".recipe", curry.ID, dinnerCell+".portions", "6", "other", dinnerCell, mealField(today, "lunch"), "Neue Suppe"), enHeader)
	if r.status != http.StatusOK {
		t.Fatalf("other = %d\n%s", r.status, rcpShown(r.body))
	}
	row := cellOf(t, r.body, today, "dinner")
	rcpHas(t, "the cell", row, `<textarea id="`+dinnerCell+`" name="`+dinnerCell+`" rows="1" maxlength="200"></textarea>`)
	rcpLacks(t, "the cell", row, "rcp-entry", "has-rcp")
	rcpHas(t, "the page", r.body, ">Neue Suppe</textarea>", "Changes not saved yet")
	rcpLacks(t, "the page", r.body, `class="dirty" hidden`)
	list, _ := mb.b.db.MealPlan(mb.hid, today, today, today)
	if len(list) != 1 || list[0].RecipeID != curry.ID {
		t.Errorf("a swap saved something: %+v", list)
	}
}

func TestPortionsThatAreNoWholeNumberFrom1To99GiveAFieldErrorAndKeepEverythingTyped(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	curry := mb.curry(t)
	for _, bad := range []string{"0", "100", "1.5", "viele", "", "-3", "1e1"} {
		r := mb.b.do(http.MethodPost, "/meals", form("2026-11-09", dinnerCell+".recipe", curry.ID, dinnerCell+".portions", bad, mealField(today, "lunch"), "Neue Suppe"), enHeader)
		if r.status != http.StatusUnprocessableEntity {
			t.Fatalf("portions %q = %d, want 422", bad, r.status)
		}
		rcpHas(t, "portions "+bad, r.body,
			`<div class="mrow has-rcp invalid" data-mt="dinner">`, `<div class="portions amount invalid"`, `<p class="ferr" id="err-`+dinnerCell+`" role="alert">`,
			`a whole number from 1 to 99; you are 4.`, `value="`+bad+`"`,
			`<p class="error form-error" role="alert">`, `Please check the portions of <a href="#`+dinnerCell+`.portions">Saturday, Dinner</a>`,
			">Neue Suppe</textarea>", `<input type="hidden" name="`+dinnerCell+`.recipe" value="`+curry.ID+`">`)
	}
	if n, _ := mb.b.db.CountMeals(mb.hid); n != 0 {
		t.Errorf("%d meals saved by a refused form", n)
	}
	de := mb.b.do(http.MethodPost, "/meals", form("2026-11-09", dinnerCell+".recipe", curry.ID, dinnerCell+".portions", "0"), rcpDE)
	rcpHas(t, "German", de.body, "Bitte prüfe die Portionen bei", "„0“ geht nicht. Portionen sind eine ganze Zahl von 1 bis 99; ihr seid 4.")
}

func TestARecipeAsAnAdjustmentOfATemplateKeepsItsAdjustedLine(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	curry := mb.curry(t)
	tpl := meal.Template{Name: "Schulwoche", Mealtime: meal.Dinner, Interval: 1, Start: "2026-11-09", Entries: map[meal.Slot]string{meal.At(6, meal.Dinner): "Gemüsesuppe"}}
	o := mealOptionsForTest()
	o.Today = today
	if _, err := mb.b.db.CreateMealTemplate(mb.hid, tpl, o); err != nil {
		t.Fatal(err)
	}
	mb.plan(t, store.Meal{Day: today, Mealtime: meal.Dinner, RecipeID: curry.ID, Portions: 5})
	row := cellOf(t, mb.page(t, enHeader), today, "dinner")
	rcpHas(t, "the adjusted row", row, `class="mrow adjusted has-rcp"`, `class="rcp-entry"`, `<p class="src"><span class="adj">Adjusted this week</span>`, `Gemüsesuppe`)
}

func TestTheFreeTextAllergiesAreListedOnceUnderAWeekThatHoldsARecipe(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	curry := mb.curry(t)
	week := func() string { return mb.page(t, enHeader) }
	if body := week(); strings.Contains(body, "meal-unch") {
		t.Error("the box of free-text allergies is shown for a week without a recipe")
	}
	mb.plan(t, store.Meal{Day: today, Mealtime: meal.Dinner, Text: "Suppe"})
	if strings.Contains(week(), "meal-unch") {
		t.Error("the box is shown for a week with free text only")
	}
	mb.plan(t, store.Meal{Day: today, Mealtime: meal.Dinner, RecipeID: curry.ID, Portions: 4}, store.Meal{Day: "2026-11-13", Mealtime: meal.Dinner, RecipeID: curry.ID, Portions: 4})
	body := week()
	box := rcpBetween(t, body, `<section class="unchecked-box meal-unch"`, `</section>`)
	rcpHas(t, "the box", box, "Hejmo does not check these allergies", "Mama:", "Kiwi")
	if n := strings.Count(body, "meal-unch"); n != 1 {
		t.Errorf("%d boxes", n)
	}
	if i, j := strings.Index(body, `class="unchecked-box meal-unch"`), strings.Index(body, `<div class="savebar">`); i < 0 || j < i {
		t.Errorf("the box is not between the week and the save bar")
	}
	// Nobody has a free-text allergy: no box.
	if err := mb.b.db.SetPersonFood(mb.hid, mb.mama.ID, store.Food{Diet: "omnivore", Allergens: []string{}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(week(), "meal-unch") {
		t.Error("a box without anyone in it")
	}
}

// Acceptance (children): a recipe is a link with its symbol and "View recipe", warnings, no portions.
func TestAChildSeesARecipeAsALinkWithItsWarningsAndNoPortions(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	curry := mb.curry(t)
	mb.plan(t, store.Meal{Day: today, Mealtime: meal.Dinner, RecipeID: curry.ID, Portions: 6}, store.Meal{Day: today, Mealtime: meal.Lunch, Text: "Nudeln"})
	body := mb.child.do(http.MethodGet, "/meals", nil, enHeader).body
	dd := rcpBetween(t, body, `<dd><a class="rcp-link"`, `</dd>`)
	rcpHas(t, "the recipe", dd, `href="/meals/recipes/`+curry.ID+`?portions=6&amp;plan=`+today+`/dinner"`, `<span class="art-sym" aria-hidden="true">🍲</span>`,
		`<span class="nm">Erdnuss-Curry mit Süßkartoffeln</span>`, "View recipe</small>", `<span class="allergen-warn">`, "Contains peanuts: ")
	rcpHas(t, "the free text", body, "<dd>Nudeln</dd>")
	rcpLacks(t, "the child's page", body, "<textarea", "rcp-entry", `name="other"`, "meal-unch", "for all", "you are")
}

// Acceptance: a recipe is suggested while typing.
func suggest(mb mealBox, header map[string]string, q string) (int, string) {
	r := mb.b.do(http.MethodGet, "/meals/suggest?q="+url.QueryEscape(q)+"&cell="+dinnerCell, nil, header)
	return r.status, r.body
}

func TestSuggestionsIncludeRecipesAndEarlierDishesAndTheTypedTextAsFreeText(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	curry := mb.curry(t)
	mb.add(t, recipe.Recipe{Name: "Kichererbsen-Curry mit Spinat", Minutes: 30})
	mb.add(t, recipe.Recipe{Name: "Linsensuppe"})
	mb.plan(t, store.Meal{Day: "2026-11-12", Mealtime: meal.Dinner, Text: "Papas Gemüsecurry mit Reis"}, store.Meal{Day: "2026-11-10", Mealtime: meal.Dinner, Text: "Curry vom Vortag"},
		store.Meal{Day: "2026-11-11", Mealtime: meal.Dinner, Text: "Linsensuppe"}, store.Meal{Day: "2026-11-20", Mealtime: meal.Dinner, Text: "Curry nächste Woche"})
	status, body := suggest(mb, enHeader, "CURRY")
	if status != http.StatusOK {
		t.Fatalf("suggest = %d %s", status, body)
	}
	rcpHas(t, "the suggestions", body, `<div class="sugg"`, `<p class="sugg-q"`, "2 recipes and 2 earlier dishes fit “CURRY”. Tap one to use it; a recipe gets portions for all 4.",
		`<ul class="place-list art-sugg" role="listbox"`, `<li role="option" data-recipe="`+curry.ID+`">`, "<b>Erdnuss-Curry mit Süßkartoffeln</b>", "<span>Recipe · Main dish · 45 min</span>",
		"Contains peanuts: ", "Not checked automatically: Currypaste", `<li role="option" class="old" data-text="Papas Gemüsecurry mit Reis">`, "Earlier dish, last on Thursday, 12.11. · free text",
		`<li role="option" class="free" data-free data-text="CURRY">`, "<b>Keep “CURRY”</b>", `<template class="rcp-box">`, `class="sugg-unchecked"`, "Hejmo does not check:", "Kiwi")
	rcpLacks(t, "the suggestions", body, "Linsensuppe", "Curry nächste Woche", "sugg-none")
	if strings.Index(body, "Erdnuss-Curry") > strings.Index(body, "Papas Gemüsecurry") || strings.Index(body, "Curry vom Vortag") > strings.Index(body, "Papas Gemüsecurry") && false {
		t.Error("recipes come before earlier dishes")
	}
	if i, j := strings.Index(body, "Papas Gemüsecurry"), strings.Index(body, "Curry vom Vortag"); i > j {
		t.Error("the most recent earlier dish comes first")
	}
	if i, j := strings.Index(body, `data-text="Curry vom Vortag"`), strings.Index(body, `class="free"`); i > j {
		t.Error("the typed text comes last")
	}
	// The box that choosing swaps in is the one the page shows, with the household's portions.
	box := rcpBetween(t, body, `<template class="rcp-box">`, `</template>`)
	rcpHas(t, "the box", box, `<div class="rcp-entry"`, `name="`+dinnerCell+`.recipe" value="`+curry.ID+`"`, `name="`+dinnerCell+`.portions" type="text" inputmode="numeric" value="4"`, "for all 4", "Different dish", "Contains peanuts")
}

func TestSuggestionsMatchUmlautsAndCaseAnywhereInTheNameAndNeedTwoLetters(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	mb.add(t, recipe.Recipe{Name: "Hähnchen-Curry"})
	if status, body := suggest(mb, enHeader, "hahn"); status != http.StatusOK || !strings.Contains(body, "<b>Hähnchen-Curry</b>") || !strings.Contains(body, "1 recipe fits “hahn”.") {
		t.Errorf("hahn = %d %s", status, body)
	}
	if status, body := suggest(mb, enHeader, "RY"); status != http.StatusOK || !strings.Contains(body, "Hähnchen-Curry") {
		t.Errorf("anywhere in the name = %d %s", status, body)
	}
	for _, q := range []string{"h", " ", ""} {
		if status, _ := suggest(mb, enHeader, q); status != http.StatusNoContent {
			t.Errorf("%q = %d, want 204", q, status)
		}
	}
}

func TestSuggestionsPutRecipesThatFitTheMealtimeFirstAndStopAtFive(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	for _, n := range []string{"A-Pfanne", "B-Pfanne", "C-Pfanne", "D-Pfanne", "E-Pfanne", "F-Pfanne"} {
		mt := []string{"breakfast"}
		if n == "F-Pfanne" {
			mt = []string{"dinner"}
		}
		mb.add(t, recipe.Recipe{Name: n, Mealtimes: mt})
	}
	_, body := suggest(mb, enHeader, "pfanne")
	if n := strings.Count(body, `data-recipe="`); n != 5 {
		t.Errorf("%d recipes, want 5", n)
	}
	if i, j := strings.Index(body, "F-Pfanne"), strings.Index(body, "A-Pfanne"); i < 0 || i > j {
		t.Errorf("the dinner recipe is not first:\n%s", body)
	}
	if strings.Contains(body, "E-Pfanne") {
		t.Error("a sixth recipe")
	}
}

func TestWithoutRecipesOnlyEarlierDishesAreSuggestedAndASentenceSaysWhereRecipesComeFrom(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	mb.plan(t, store.Meal{Day: "2026-11-10", Mealtime: meal.Dinner, Text: "Nudeln mit Tomatensoße"})
	_, body := suggest(mb, enHeader, "Nudel")
	rcpHas(t, "the suggestions", body, "1 earlier dish fits “Nudel”. Tap one to use it.", `class="old"`, `<p class="sugg-none">`, `No recipes yet. Write one under <a href="/meals/recipes">Recipes</a>`)
	rcpLacks(t, "the suggestions", body, `data-recipe=`, "sugg-unchecked", "portions for all")
}

func TestWhenNothingFitsOnlyOneSentenceIsShown(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	mb.curry(t)
	_, body := suggest(mb, enHeader, "Raclette")
	rcpHas(t, "the suggestions", body, `<p class="sugg-q"`, "No recipe and no earlier dish fits “Raclette”. It stays free text; Hejmo does not warn about allergens for it.")
	rcpLacks(t, "the suggestions", body, "<ul", "sugg-none", "sugg-unchecked")
	_, de := suggest(mb, rcpDE, "Raclette")
	rcpHas(t, "German", de, "Kein Rezept und kein früheres Gericht passt zu „Raclette“.")
}

func TestOnlyParentsAskForSuggestions(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	mb.curry(t)
	if r := mb.child.do(http.MethodGet, "/meals/suggest?q=Curry&cell="+dinnerCell, nil, enHeader); r.status != http.StatusForbidden && r.status != http.StatusSeeOther {
		t.Errorf("a child asking = %d", r.status)
	}
	signedOut := mb.b.bareClient()
	res, err := signedOut.Get(mb.b.srv.URL + "/meals/suggest?q=Curry&cell=" + dinnerCell)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.Request.URL.Path != "/login" {
		t.Errorf("signed out ends at %s", res.Request.URL.Path)
	}
}

// Acceptance: wall displays, the calendar and Home show only the dish name.
func TestADisplayTheCalendarAndHomeShowOnlyTheNameOfARecipe(t *testing.T) {
	t.Parallel()
	f := todayBox(t)
	curry, err := f.db.CreateRecipe(f.householdID, "", recipe.Recipe{Name: "Erdnuss-Curry mit Süßkartoffeln", Category: recipe.Main, Portions: 4, Minutes: 45,
		Ingredients: []recipe.Ingredient{{Amount: 3, Unit: "tbsp", Text: "Erdnussbutter", ArticleID: "peanut-butter"}, {Amount: 3, Unit: "tbsp", Text: "Currypaste"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetMeals(f.householdID, []store.Meal{{Day: "2026-11-14", Mealtime: meal.Dinner, RecipeID: curry.ID, Portions: 6}, {Day: "2026-11-14", Mealtime: meal.Lunch, Text: "Kartoffelsuppe"}},
		store.MealOptions{CalendarName: "Meal plan", Location: berlinTZ, Title: func(m meal.Mealtime, text string) string { return meal.EventTitle(string(m), text) }}); err != nil {
		t.Fatal(err)
	}
	card := todayCard(f.page("de", "/").body)
	if want := `<p class="today-meal"><b>Essen heute:</b> Mittag: Kartoffelsuppe · Abend: Erdnuss-Curry mit Süßkartoffeln</p>`; !strings.Contains(card, want) {
		t.Errorf("Home lacks %q\n%s", want, card)
	}
	footer := f.footer(t, "de", 14, 18, 30)
	if !strings.Contains(footer, "Erdnuss-Curry mit Süßkartoffeln") {
		t.Errorf("the meal tile lacks the dish:\n%s", footer)
	}
	for name, page := range map[string]string{"Home": card, "the meal tile": footer} {
		for _, not := range []string{"Portion", "portion", "allergen", "Enthält", "/meals/recipes", "Rezept", "Erdnussbutter", " 6"} {
			if strings.Contains(page, not) {
				t.Errorf("%s shows %q: only the dish name belongs there\n%s", name, not, page)
			}
		}
	}
	cal, err := f.db.MealCalendar(f.householdID)
	if err != nil {
		t.Fatal(err)
	}
	evs, _ := f.db.CalendarEvents(f.householdID, cal.ID)
	titles := map[string]bool{}
	for _, e := range evs {
		titles[e.Title] = true
		if e.Description != "" || e.Location != "" {
			t.Errorf("event %q has more than a title: %+v", e.Title, e)
		}
	}
	if !titles["dinner: Erdnuss-Curry mit Süßkartoffeln"] {
		t.Errorf("events = %v", titles)
	}
}

func TestARenamedRecipeShowsItsNewNameOnThePageAndAfterADeleteTheNameStaysAsText(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	curry := mb.curry(t)
	mb.plan(t, store.Meal{Day: today, Mealtime: meal.Dinner, RecipeID: curry.ID, Portions: 6})
	r := curry.Recipe
	r.Name = "Erdnuss-Curry scharf"
	if _, err := mb.b.db.UpdateRecipe(mb.hid, mb.mama.ID, r, mealOptionsForTest()); err != nil {
		t.Fatal(err)
	}
	rcpHas(t, "the plan", mb.page(t, enHeader), `>Erdnuss-Curry scharf</a>`)
	if res := mb.b.do(http.MethodPost, "/meals/recipes/"+curry.ID+"/delete", url.Values{}, enHeader); res.status != http.StatusSeeOther {
		t.Fatalf("delete = %d", res.status)
	}
	body := mb.page(t, enHeader)
	rcpHas(t, "the plan", body, `>Erdnuss-Curry scharf</textarea>`)
	rcpLacks(t, "the plan", body, "rcp-entry")
}
