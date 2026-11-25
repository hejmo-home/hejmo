package server_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 281: the recipe page scales its quantities on the server for the
// portions of the meal plan, links instead of script.

// pancakes is written for 3 portions: quantities that show the rounding.
func (mb mealBox) pancakes(t *testing.T) store.Recipe {
	return mb.add(t, recipe.Recipe{Name: "Pfannkuchen", Portions: 3, Category: recipe.Breakfast,
		Ingredients: []recipe.Ingredient{
			{Amount: 250, Unit: "g", Text: "Mehl", ArticleID: "wheat-flour", Variety: "Type 405"},
			{Amount: 3, Text: "Eier", ArticleID: "eggs"},
			{Amount: 500, Unit: "ml", Text: "Milch", ArticleID: "milk"},
			{Amount: 1, Unit: "pinch", Text: "Salz"},
			{Amount: 0.19, Unit: "tsp", Text: "Natron"},
			{Amount: 2, Unit: "tbsp", Text: "Öl"},
			{Text: "Butter zum Braten"},
		}})
}

func quantities(t *testing.T, body string) []string {
	t.Helper()
	var out []string
	for _, m := range regexp.MustCompile(`<span class="q">([^<]*)</span><span class="n">([^<]+)`).FindAllStringSubmatch(body, -1) {
		out = append(out, m[1]+"|"+m[2])
	}
	return out
}

// Acceptance: scaled quantities round to one decimal, never up to whole numbers.
func TestQuantitiesAreScaledOnTheServerAndRoundedToOneDecimalNeverToWholeNumbers(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	r := mb.pancakes(t)
	get := func(q string, header map[string]string) string {
		res := mb.b.do(http.MethodGet, "/meals/recipes/"+r.ID+q, nil, header)
		if res.status != http.StatusOK {
			t.Fatalf("GET %s = %d", q, res.status)
		}
		return res.body
	}
	want8 := []string{"666,7 g|Mehl", "8|Eier", "1333,3 ml|Milch", "2,7 Prisen|Salz", "0,5 TL|Natron", "5,3 EL|Öl", "|Butter zum Braten"}
	if got := quantities(t, get("?portions=8", rcpDE)); strings.Join(got, ",") != strings.Join(want8, ",") {
		t.Errorf("8 portions = %v, want %v", got, want8)
	}
	want1 := []string{"83,3 g|Mehl", "1|Eier", "166,7 ml|Milch", "0,3 Prisen|Salz", "0,1 TL|Natron", "0,7 EL|Öl", "|Butter zum Braten"}
	if got := quantities(t, get("?portions=1", rcpDE)); strings.Join(got, ",") != strings.Join(want1, ",") {
		t.Errorf("1 portion = %v, want %v: 0,19 TL / 3 shows as 0,1, never 0", got, want1)
	}
	if got := quantities(t, get("?portions=8", enHeader)); got[0] != "666.7 g|Mehl" || got[3] != "2.7 pinches|Salz" || got[4] != "0.5 tsp|Natron" {
		t.Errorf("English = %v: a decimal point, and the units of the kitchen are not converted", got)
	}
	// As written (no portions, or the recipe's own) keeps the quantities as typed, with two decimals.
	for _, q := range []string{"", "?portions=3"} {
		if got := quantities(t, get(q, rcpDE)); got[0] != "250 g|Mehl" || got[4] != "0,19 TL|Natron" || got[1] != "3|Eier" {
			t.Errorf("as written %q = %v", q, got)
		}
	}
	// Portions that are no number from 1 to 99 mean "as written".
	for _, q := range []string{"?portions=0", "?portions=100", "?portions=abc", "?portions=2.5", "?portions=-1"} {
		if got := quantities(t, get(q, rcpDE)); got[0] != "250 g|Mehl" {
			t.Errorf("%s = %v", q, got)
		}
	}
}

func TestThePortionsToViewAreLinksTheServerScalesAndSayWhereTheNumberComesFrom(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	r := mb.pancakes(t)
	body := mb.b.do(http.MethodGet, "/meals/recipes/"+r.ID+"?portions=6&plan=2026-12-03/dinner", nil, rcpDE).body
	view := rcpBetween(t, body, `<div class="portions-view"`, `</p>`)
	rcpHas(t, "the portions", view,
		`<div class="portions-view" role="group" aria-labelledby="pv-l"><span id="pv-l" class="pv-l">Für</span>`,
		`<a class="button secondary step" href="/meals/recipes/`+r.ID+`?portions=5&amp;plan=2026-12-03/dinner" aria-label="Eine Portion weniger">`, `<strong class="pv-n">6</strong>`,
		`<a class="button secondary step" href="/meals/recipes/`+r.ID+`?portions=7&amp;plan=2026-12-03/dinner" aria-label="Eine Portion mehr">`, `<span class="pv-l">Portionen</span>`,
		`<p class="hint pv-hint">So wie im Essensplan am Donnerstag, 03.12., zum Abendessen. Geschrieben ist es für 3 Portionen; Hejmo rechnet die Mengen um und rundet auf eine Stelle nach dem Komma. `,
		`<a href="/meals/recipes/`+r.ID+`">Wie geschrieben: 3 Portionen</a>`)
	rcpHas(t, "the facts", body, `<dt>Portionen</dt><dd>geschrieben für 3</dd>`)
	rcpLacks(t, "the page", body, "Für 3 Portionen")
	// Without a plan: no origin, the way back only when the portions differ.
	own := mb.b.do(http.MethodGet, "/meals/recipes/"+r.ID, nil, rcpDE).body
	rcpHas(t, "as written", own, `<strong class="pv-n">3</strong>`, `?portions=2"`, `?portions=4"`, `<p class="hint pv-hint">Geschrieben ist es für 3 Portionen; Hejmo rechnet`)
	rcpLacks(t, "as written", own, "So wie im Essensplan", "Wie geschrieben")
	// A bad plan is ignored; one portion cannot go lower.
	one := mb.b.do(http.MethodGet, "/meals/recipes/"+r.ID+"?portions=1&plan=bogus", nil, rcpDE).body
	rcpHas(t, "one portion", one, `<a class="button secondary step" href="/meals/recipes/`+r.ID+`?portions=1" aria-label="Eine Portion weniger" aria-disabled="true">`, `<strong class="pv-n">1</strong>`)
	rcpLacks(t, "one portion", rcpBetween(t, one, `<div class="portions-view"`, `</ul>`), "So wie im Essensplan", "plan=")
	top := mb.b.do(http.MethodGet, "/meals/recipes/"+r.ID+"?portions=99", nil, rcpDE).body
	rcpHas(t, "99 portions", top, `<strong class="pv-n">99</strong>`, `aria-label="Eine Portion mehr" aria-disabled="true"`)
	en := mb.b.do(http.MethodGet, "/meals/recipes/"+r.ID+"?portions=6&plan=2026-12-03/lunch", nil, enHeader).body
	rcpHas(t, "English", en, `<span id="pv-l" class="pv-l">For</span>`, "As in the meal plan on Thursday, 03.12., for lunch.", "As written: 3 servings", "written for 3")
}

func TestChildrenOpenTheRecipeFromThePlanWithThePortionsOfTheEntry(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	r := mb.pancakes(t)
	body := mb.child.do(http.MethodGet, "/meals/recipes/"+r.ID+"?portions=6&plan="+today+"/lunch", nil, rcpDE).body
	if got := quantities(t, body); got[0] != "500 g|Mehl" {
		t.Errorf("a child sees %v", got)
	}
	rcpHas(t, "child", body, `<strong class="pv-n">6</strong>`, "So wie im Essensplan am Samstag, 14.11., zum Mittagessen.")
}

func TestARecipeThatIsOnThePlanDoesNotChangeThePlanWhenViewedWithOtherPortions(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	r := mb.pancakes(t)
	mb.plan(t, store.Meal{Day: today, Mealtime: meal.Dinner, RecipeID: r.ID, Portions: 4})
	mb.b.do(http.MethodGet, "/meals/recipes/"+r.ID+"?portions=9&plan="+today+"/dinner", nil, rcpDE)
	list, _ := mb.b.db.MealPlan(mb.hid, today, today, today)
	if len(list) != 1 || list[0].Portions != 4 {
		t.Errorf("plan = %+v", list)
	}
}

// Deleting a recipe that is planned: the box lists every coming entry and says what becomes of it.
func TestTheDeleteBoxListsTheComingEntriesAndTheNameStaysThereAsText(t *testing.T) {
	t.Parallel()
	mb := newMealBox(t)
	curry := mb.curry(t)
	mb.plan(t,
		store.Meal{Day: "2026-11-10", Mealtime: meal.Dinner, RecipeID: curry.ID, Portions: 4}, // over
		store.Meal{Day: today, Mealtime: meal.Dinner, RecipeID: curry.ID, Portions: 4},
		store.Meal{Day: "2026-12-03", Mealtime: meal.Lunch, RecipeID: curry.ID, Portions: 4},
		store.Meal{Day: "2026-12-04", Mealtime: meal.Lunch, Text: "Erdnuss-Curry"})
	box := rcpBetween(t, mb.b.do(http.MethodGet, "/meals/recipes/"+curry.ID, nil, rcpDE).body, `<details class="danger">`, `</details>`)
	rcpHas(t, "the delete box", box, `Im Essensplan steht es noch am <b>Samstag, 14.11., Abendessen</b> und am <b>Donnerstag, 03.12., Mittagessen</b>. Dort bleibt der Name als freier Text stehen, ohne Portionen und ohne Warnungen.`)
	rcpLacks(t, "the delete box", box, "10.11.", "04.12.")
	en := rcpBetween(t, mb.b.do(http.MethodGet, "/meals/recipes/"+curry.ID, nil, enHeader).body, `<details class="danger">`, `</details>`)
	rcpHas(t, "English", en, "It is still in the meal plan on <b>Saturday, 14.11., Dinner</b> and on <b>Thursday, 03.12., Lunch</b>.")
	// A recipe that is not planned says nothing of the plan.
	other := mb.add(t, recipe.Recipe{Name: "Suppe"})
	rcpLacks(t, "an unplanned recipe", rcpBetween(t, mb.b.do(http.MethodGet, "/meals/recipes/"+other.ID, nil, rcpDE).body, `<details class="danger">`, `</details>`), "Essensplan")
}
