package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// Ticket 283 (docs/design/preview-283.html): ingredients of planned recipes on
// a shopping list, by hand. The test clock stands at Saturday 14/11/2026.

type shopBox struct {
	mealBox
	list list.List
	rec  store.Recipe
}

func newShopBox(t *testing.T) shopBox {
	t.Helper()
	mb := newMealBox(t)
	ls, err := mb.b.db.Lists(mb.hid)
	if err != nil || len(ls) == 0 || !ls[0].Suggestions {
		t.Fatalf("the household starts with a shopping list: %v %v", ls, err)
	}
	sb := shopBox{mealBox: mb, list: ls[0], rec: mb.curry(t)}
	sb.plan(t, store.Meal{Day: today, Mealtime: meal.Dinner, RecipeID: sb.rec.ID, Portions: 4})
	return sb
}

var shopMeal = today + ".dinner"

func (sb shopBox) shopPage(t *testing.T, query string) string {
	t.Helper()
	r := sb.b.do(http.MethodGet, "/meals/shop?"+query, nil, enHeader)
	if r.status != http.StatusOK {
		t.Fatalf("GET /meals/shop?%s = %d\n%s", query, r.status, r.body)
	}
	return r.body
}

var hiddenField = regexp.MustCompile(`<input type="hidden" name="(rv|was|for)" value="([^"]*)">`)

// formOf is what the page's form would post: its hidden fields, the list and the ticks.
func shopFormOf(t *testing.T, body string, unticked ...string) url.Values {
	t.Helper()
	v := url.Values{}
	for _, m := range hiddenField.FindAllStringSubmatch(body, -1) {
		v.Add(m[1], strings.NewReplacer("&#43;", "+", "&amp;", "&", "&#34;", `"`).Replace(m[2]))
	}
	if m := regexp.MustCompile(`name="list" value="([^"]+)" checked`).FindStringSubmatch(body); m != nil {
		v.Set("list", m[1])
	}
	for _, m := range regexp.MustCompile(`name="(pick|meal)" value="([^"]+)" checked`).FindAllStringSubmatch(body, -1) {
		skip := false
		for _, u := range unticked {
			skip = skip || strings.Contains(m[2], u)
		}
		if !skip {
			v.Add(m[1], m[2])
		}
	}
	return v
}

// Acceptance: every saved recipe box has the button, the week form has its id and the week card counts.
func TestARecipeBoxOffersItsIngredientsAndTheWeekHasACard(t *testing.T) {
	t.Parallel()
	sb := newShopBox(t)
	body := sb.page(t, enHeader)
	rcpHas(t, "the meal plan", body, `id="week"`, `class="shop-week"`, `form="week" name="shop" value="week"`, `name="shop" value="`+dinnerCell+`"`, "Ingredients onto a list")
	if !strings.Contains(body, "From today 1 recipe needs ingredients") {
		t.Errorf("the week card says nothing about the recipe:\n%s", rcpBetween(t, body, `class="shop-week"`, "</div>"))
	}
	rcpLacks(t, "a child's meal plan", sb.child.do(http.MethodGet, "/meals", nil, enHeader).body, "shop-week", `name="shop"`)
}

// The buttons save the week first, then open the tick list.
func TestTheButtonSavesTheWeekAndOpensTheTickList(t *testing.T) {
	t.Parallel()
	sb := newShopBox(t)
	f := form(today, "shop", dinnerCell, dinnerCell+".recipe", sb.rec.ID, dinnerCell+".portions", "6")
	r := sb.b.do(http.MethodPost, "/meals", f, enHeader)
	if r.status != http.StatusSeeOther || r.location != "/meals/shop?meal="+url.QueryEscape(shopMeal) {
		t.Fatalf("POST = %d %q", r.status, r.location)
	}
	plan, _ := sb.b.db.MealPlan(sb.hid, today, today, today)
	if len(plan) != 1 || plan[0].Portions != 6 {
		t.Errorf("the changed portions were not saved first: %+v", plan)
	}
	f = form(today, "shop", "week")
	if r := sb.b.do(http.MethodPost, "/meals", f, enHeader); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/meals/shop?week=2026-11-09") {
		t.Errorf("week = %d %q", r.status, r.location)
	}
}

// Acceptance: scaled quantities, kitchen measures without quantity, pantry unticked, the list last used is preselected.
func TestTheTickListScalesAndLeavesPantryAndKitchenMeasuresOut(t *testing.T) {
	t.Parallel()
	sb := newShopBox(t)
	sb.plan(t, store.Meal{Day: today, Mealtime: meal.Dinner, RecipeID: sb.rec.ID, Portions: 8})
	body := sb.shopPage(t, "meal="+shopMeal)
	rcpHas(t, "the tick list", body, "<h1>Ingredients onto a list</h1>", "Erdnuss-Curry", `class="shop-meal-head"`, "recipe for 4, quantities scaled",
		`class="qty">1.2 kg</span>`, `name="list" value="`+sb.list.ID+`" checked`, `class="shop-group pantry"`, "Usually at home", "Without a quantity: the recipe says")
	if strings.Contains(rcpBetween(t, body, `name="pick" value="a:salt`, "</label>"), "qty") {
		t.Error("salt has no quantity")
	}
	if regexp.MustCompile(`name="pick" value="a:salt[^"]*" checked`).MatchString(body) {
		t.Error("a pantry article is ticked")
	}
	peanut := rcpBetween(t, body, `name="pick" value="a:peanut-butter`, "</label>")
	if strings.Contains(peanut, `class="qty"`) || !strings.Contains(peanut, "checked") {
		t.Errorf("a tablespoon goes on ticked and without a quantity: %s", peanut)
	}
	if !strings.Contains(body, "<b>3 ingredients go onto “Einkaufen”:</b> 3 new, 0 already on the list. Hejmo leaves out 1 pantry article.") {
		t.Errorf("sentence = %s", rcpBetween(t, body, `class="shop-count"`, "</p>"))
	}
}

func addMeal(t *testing.T, sb shopBox, body string) resp {
	t.Helper()
	return sb.b.do(http.MethodPost, "/meals/shop", shopFormOf(t, body), enHeader)
}

// Acceptance: the same meal is never added twice; success line with undo.
func TestAddingPutsTheTicksOnTheListOnceAndSaysSoWithUndo(t *testing.T) {
	t.Parallel()
	sb := newShopBox(t)
	body := sb.shopPage(t, "meal="+shopMeal)
	r := addMeal(t, sb, body)
	if r.status != http.StatusSeeOther || r.location != "/meals?week=2026-11-09" {
		t.Fatalf("add = %d %q", r.status, r.location)
	}
	es, _ := sb.b.db.Entries(sb.hid, sb.list.ID)
	var names []string
	for _, e := range es {
		names = append(names, e.Text)
	}
	if len(es) != 3 {
		t.Fatalf("entries = %v", names)
	}
	page := sb.b.do(http.MethodGet, r.location, nil, enHeader).body
	rcpHas(t, "the meal plan after adding", page, `class="msg ok"`, `class="msg-body"`, "put on “Einkaufen”", "Open list “Einkaufen”", `action="/meals/shop/undo"`, "Undo", "Hejmo left out Salt",
		"Ingredients put on “Einkaufen”, today for 4 portions.")
	if strings.Contains(cellOf(t, page, today, "dinner"), `name="shop"`) {
		t.Error("the meal is on the list: the button is gone")
	}
	again := sb.b.do(http.MethodGet, "/meals?week=2026-11-09", nil, enHeader).body
	if strings.Contains(again, `class="msg ok"`) {
		t.Error("the line shows once")
	}
	// the same meal again: nothing new
	if r := sb.b.do(http.MethodGet, "/meals/shop?meal="+shopMeal, nil, enHeader); r.status != http.StatusOK || !strings.Contains(r.body, "Already on “Einkaufen” since") {
		t.Errorf("the meal is shown as on the list: %d", r.status)
	}
	if es2, _ := sb.b.db.Entries(sb.hid, sb.list.ID); len(es2) != 3 {
		t.Errorf("the page changed the list")
	}
}

// Acceptance: raising the portions offers only the difference.
func TestRaisedPortionsOfferOnlyTheMissingOnes(t *testing.T) {
	t.Parallel()
	sb := newShopBox(t)
	addMeal(t, sb, sb.shopPage(t, "meal="+shopMeal))
	sb.plan(t, store.Meal{Day: today, Mealtime: meal.Dinner, RecipeID: sb.rec.ID, Portions: 6})
	page := sb.b.do(http.MethodGet, "/meals?week=2026-11-09", nil, enHeader).body
	rcpHas(t, "the box", cellOf(t, page, today, "dinner"), "Now it is 6.", "Missing 2 portions onto the list")
	body := sb.shopPage(t, "meal="+shopMeal)
	rcpHas(t, "the tick list", body, "only the missing 2 of 6 portions", `class="qty">300 g</span>`, "600 g", "900 g")
	addMeal(t, sb, body)
	es, _ := sb.b.db.Entries(sb.hid, sb.list.ID)
	for _, e := range es {
		if e.Text == "Sweet potatoes" && e.Qty.Base != 900 {
			t.Errorf("sweet potatoes = %v, want 900 g: 600 g and then the 300 g of the two missing portions", e.Qty)
		}
	}
}

// Acceptance: undo takes back exactly that run and names what was ticked off meanwhile.
func TestUndoTakesBackTheRunAndNamesWhatWasTickedOff(t *testing.T) {
	t.Parallel()
	sb := newShopBox(t)
	r := addMeal(t, sb, sb.shopPage(t, "meal="+shopMeal))
	page := sb.b.do(http.MethodGet, r.location, nil, enHeader).body
	run := regexp.MustCompile(`name="run" value="([^"]+)"`).FindStringSubmatch(page)[1]
	es, _ := sb.b.db.Entries(sb.hid, sb.list.ID)
	if err := sb.b.db.SetEntryDone(sb.hid, sb.list.ID, es[0].ID, true); err != nil {
		t.Fatal(err)
	}
	u := sb.b.do(http.MethodPost, "/meals/shop/undo", url.Values{"run": {run}}, enHeader)
	if u.status != http.StatusSeeOther {
		t.Fatalf("undo = %d", u.status)
	}
	body := sb.b.do(http.MethodGet, u.location, nil, enHeader).body
	rcpHas(t, "the undo line", body, "Undone.", "ticked off", es[0].Text)
	left, _ := sb.b.db.Entries(sb.hid, sb.list.ID)
	if len(left) != 1 || !left[0].Done {
		t.Errorf("only the ticked-off entry stays: %+v", left)
	}
	if again := sb.b.do(http.MethodPost, "/meals/shop/undo", url.Values{"run": {run}}, enHeader); again.status != http.StatusSeeOther {
		t.Errorf("undoing twice = %d", again.status)
	}
	rcpHas(t, "the meal can go on the list again", sb.shopPage(t, "meal="+shopMeal), "Erdnuss-Curry")
}

// Acceptance: if the recipe changed since the page was made, nothing is added and the new ingredients show.
func TestAChangedRecipeAddsNothingAndShowsTheNewIngredients(t *testing.T) {
	t.Parallel()
	sb := newShopBox(t)
	body := sb.shopPage(t, "meal="+shopMeal)
	f := shopFormOf(t, body)
	rec := sb.rec.Recipe
	rec.Ingredients = append([]recipe.Ingredient{{Amount: 2, Unit: "piece", Text: "Limetten", ArticleID: "lime"}}, rec.Ingredients[1:]...)
	if _, err := sb.b.db.UpdateRecipe(sb.hid, sb.mama.ID, rec, mealOptionsForTest()); err != nil {
		t.Fatal(err)
	}
	r := sb.b.do(http.MethodPost, "/meals/shop", f, enHeader)
	if r.status != http.StatusConflict {
		t.Fatalf("POST = %d, want 409\n%s", r.status, r.body)
	}
	rcpHas(t, "the refusal", r.body, `class="notice shop-changed" role="alert"`, "Nothing is on the list yet", "New in the recipe", `class="shop-gone"`, "Sweet potatoes")
	if es, _ := sb.b.db.Entries(sb.hid, sb.list.ID); len(es) != 0 {
		t.Errorf("a changed recipe added %d entries", len(es))
	}
	// the ticks of the unchanged rows stay, and the new page goes through
	if r2 := sb.b.do(http.MethodPost, "/meals/shop", shopFormOf(t, r.body), enHeader); r2.status != http.StatusSeeOther {
		t.Errorf("after looking: %d\n%s", r2.status, r2.body)
	}
}

func TestNoTickAndAGoneListAreErrorsAtTheirField(t *testing.T) {
	t.Parallel()
	sb := newShopBox(t)
	body := sb.shopPage(t, "meal="+shopMeal)
	none := shopFormOf(t, body)
	none.Del("pick")
	r := sb.b.do(http.MethodPost, "/meals/shop", none, enHeader)
	rcpHas(t, "no tick", r.body, `class="shop-group shop-sum invalid"`, `id="err-pick"`, "Tick at least one")
	if r.status != http.StatusUnprocessableEntity {
		t.Errorf("status = %d", r.status)
	}
	gone := shopFormOf(t, body)
	gone.Set("list", "unknown")
	r = sb.b.do(http.MethodPost, "/meals/shop", gone, enHeader)
	rcpHas(t, "a gone list", r.body, `class="ferr" id="err-list"`, "shop-lists invalid", "Pick a list above.")
	if regexp.MustCompile(`name="list" value="[^"]+" checked`).MatchString(r.body) || !strings.Contains(r.body, `name="pick"`) {
		t.Error("no list is preselected, the ticks stay")
	}
	if es, _ := sb.b.db.Entries(sb.hid, sb.list.ID); len(es) != 0 {
		t.Error("nothing is added")
	}
}

func TestParentsOnlyAndEmptyStates(t *testing.T) {
	t.Parallel()
	sb := newShopBox(t)
	if r := sb.child.do(http.MethodGet, "/meals/shop?meal="+shopMeal, nil, enHeader); r.status != http.StatusNotFound {
		t.Errorf("a child = %d, want 404", r.status)
	}
	empty := sb.rec.Recipe
	empty.Name, empty.Ingredients = "Kürbissuppe", nil
	bare := sb.add(t, empty)
	sb.plan(t, store.Meal{Day: today, Mealtime: meal.Lunch, RecipeID: bare.ID, Portions: 4})
	body := sb.shopPage(t, "meal="+today+".lunch")
	rcpHas(t, "a recipe without ingredients", body, `class="empty"`, "has no ingredients yet", "Enter ingredients", "To the lists")
	rcpLacks(t, "a recipe without ingredients", body, `<form class="shop-form"`)
	if _, err := sb.b.db.SQL().Exec(`DELETE FROM lists`); err != nil {
		t.Fatal(err)
	}
	rcpHas(t, "no list", sb.shopPage(t, "meal="+shopMeal), `class="empty"`, "There is no list yet.", `href="/lists#new"`, "Make a new list")
}

// Acceptance: the week includes recipe meals from today on, sums articles across meals and says each meal's share.
func TestTheWeekSumsTheSameArticleAcrossMealsAndKeepsEachShare(t *testing.T) {
	t.Parallel()
	sb := newShopBox(t)
	sb.plan(t, store.Meal{Day: "2026-11-15", Mealtime: meal.Dinner, RecipeID: sb.rec.ID, Portions: 2},
		store.Meal{Day: "2026-11-10", Mealtime: meal.Dinner, RecipeID: sb.rec.ID, Portions: 2})
	body := sb.shopPage(t, "week=2026-11-09")
	rcpHas(t, "the week", body, "<h1>The week&#39;s ingredients onto a list</h1>", "Which meals?", `name="meal" value="`+shopMeal+`" checked`, `name="meal" value="2026-11-15.dinner" checked`, "<noscript>", "Add up again", "Week 46, from today")
	rcpLacks(t, "the week", body, `value="2026-11-10.dinner"`)
	body = strings.ReplaceAll(body, "&#43;", "+")
	rcpHas(t, "a shared row", body, `class="qty">900 g</span>`, "600 g for Erdnuss-Curry mit Süßkartoffeln (Sat)", "300 g for Erdnuss-Curry mit Süßkartoffeln (Sun)")
	addMeal(t, sb, body)
	es, _ := sb.b.db.Entries(sb.hid, sb.list.ID)
	for _, e := range es {
		if e.Text == "Sweet potatoes" && e.Qty.Base != 900 {
			t.Errorf("sweet potatoes = %v", e.Qty)
		}
	}
	page := sb.b.do(http.MethodGet, "/meals?week=2026-11-09", nil, enHeader).body
	rcpHas(t, "the week", page, "Ingredients put on “Einkaufen”, today for 4 portions.", "today for 2 portions")
}

// Acceptance: the list says under each entry which meal contributed how much.
func TestTheListSaysWhichMealContributedHowMuch(t *testing.T) {
	t.Parallel()
	sb := newShopBox(t)
	if _, err := sb.b.db.AddItem(sb.hid, sb.list.ID, store.Item{Text: "Sweet potatoes", ArticleID: "sweet-potatoes", Qty: list.NewQuantity(500, "g")}, showFor()); err != nil {
		t.Fatal(err)
	}
	addMeal(t, sb, sb.shopPage(t, "meal="+shopMeal))
	body := strings.ReplaceAll(sb.b.do(http.MethodGet, "/lists/"+sb.list.ID, nil, enHeader).body, "&#43;", "+")
	rcpHas(t, "the list", body, `class="auto-src rcp-src"`, "500 g already entered + 600 g for Erdnuss-Curry mit Süßkartoffeln (Sat)", "For Erdnuss-Curry mit Süßkartoffeln (Sat)")
	es, _ := sb.b.db.Entries(sb.hid, sb.list.ID)
	if err := sb.b.db.SetEntryDone(sb.hid, sb.list.ID, es[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if strings.Count(sb.b.do(http.MethodGet, "/lists/"+sb.list.ID, nil, enHeader).body, "rcp-src") != 2 {
		t.Error("the line goes when the entry is ticked off")
	}
}

func showFor() list.Show {
	return func(base float64, kind unit.Kind) string {
		return unit.ShowUnit(base, kind, []string{"g", "kg"}, unit.DefaultActive)
	}
}

// The "recipe changed at" line shows the time without seconds, also for a 12-hour household.
func TestTheChangedAtTimeIsAClockTimeInA12HourHousehold(t *testing.T) {
	t.Parallel()
	sb := newShopBox(t)
	h, err := sb.b.db.HomeHousehold()
	if err != nil {
		t.Fatal(err)
	}
	if err := sb.b.db.SaveHouseholdSettings(sb.hid, store.HouseholdSettings{DateFormat: h.DateFormat, TimeFormat: store.Time12h, TemperatureUnit: h.TemperatureUnit, AutoUpdate: h.AutoUpdate, MealCutoffs: h.MealCutoffs}); err != nil {
		t.Fatal(err)
	}
	f := shopFormOf(t, sb.shopPage(t, "meal="+shopMeal))
	rec := sb.rec.Recipe
	rec.Ingredients = append([]recipe.Ingredient{{Amount: 2, Unit: "piece", Text: "Limetten", ArticleID: "lime"}}, rec.Ingredients[1:]...)
	if _, err := sb.b.db.UpdateRecipe(sb.hid, sb.mama.ID, rec, mealOptionsForTest()); err != nil {
		t.Fatal(err)
	}
	r := sb.b.do(http.MethodPost, "/meals/shop", f, enHeader)
	if !regexp.MustCompile(`was changed at \d{1,2}:\d{2}[ \x{a0}\x{202f}](AM|PM) while`).MatchString(r.body) {
		t.Errorf("the changed-at time is not a 12-hour clock time:\n%s", r.body)
	}
}
