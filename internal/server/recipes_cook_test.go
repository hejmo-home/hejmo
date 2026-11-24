package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/recipe"
)

// Ticket 286 (ADR 0015, 0021; preview-286.html): the cooking view of a recipe.
// One GET form: portions, ticks, step and view travel in the address, so the
// page works without script; cook.js only handles the same form in place.

func cookPath(id string, q ...string) string {
	p := "/meals/recipes/" + id + "/cook"
	if len(q) > 0 {
		p += "?" + strings.Join(q, "&")
	}
	return p
}

// cookCurry is the curry of the example family: 4 portions, one article that
// Mia (peanuts) must look out for, one free text, one without a quantity, three steps.
func (rb recipeBox) cookCurry(t *testing.T) (id string) { return rb.curry(t).ID }

// cookChecked are the values of the ticked checkboxes of a page.
func cookChecked(body string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`<input type="checkbox" name="have" value="(\d+)"( checked)?>`).FindAllStringSubmatch(rcpLive(body), -1) {
		if m[2] != "" {
			out = append(out, m[1])
		}
	}
	return out
}

func cookView(body string) string {
	m := regexp.MustCompile(`<div class="cook" data-view="(\w+)"`).FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}

// Acceptance: the way in is one primary "Start cooking" for everybody; changing is a secondary button for parents.
func TestTheRecipePageHasStartCookingAsItsOnePrimaryButton(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	id := rb.cookCurry(t)
	parent := rb.b.do(http.MethodGet, "/meals/recipes/"+id, nil, enHeader).body
	act := rcpBetween(t, parent, `<p class="rcp-act">`, "</p>")
	rcpHas(t, "parent", act, `<a class="button" href="/meals/recipes/`+id+`/cook">`, "Start cooking", `<a class="button secondary" href="/meals/recipes/`+id+`/edit">`, "Change recipe")
	if strings.Index(act, "Start cooking") > strings.Index(act, "Change recipe") {
		t.Error("Start cooking comes first")
	}
	kid := rb.child.do(http.MethodGet, "/meals/recipes/"+id, nil, enHeader).body
	act = rcpBetween(t, kid, `<p class="rcp-act">`, "</p>")
	rcpHas(t, "child", act, `<a class="button" href="/meals/recipes/`+id+`/cook">`, "Start cooking")
	rcpLacks(t, "child", act, "Change recipe", "/edit")
	de := rb.b.do(http.MethodGet, "/meals/recipes/"+id, nil, rcpDE).body
	rcpHas(t, "German", de, "Jetzt kochen", "Rezept ändern")
}

// Acceptance: parents and children open it; it has its own frame without navigation, language, theme or sign-out.
func TestTheCookingViewHasItsOwnFrameWithoutNavigation(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	id := rb.cookCurry(t)
	for who, b := range map[string]*box{"parent": rb.b, "child": rb.child} {
		page := rb.b.do(http.MethodGet, cookPath(id), nil, enHeader)
		if who == "child" {
			page = b.do(http.MethodGet, cookPath(id), nil, enHeader)
		}
		if page.status != http.StatusOK {
			t.Fatalf("%s: GET = %d", who, page.status)
		}
		rcpHas(t, who, page.body, `<body class="web cook-page">`, `<title>Erdnuss-Curry mit Süßkartoffeln · Hejmo</title>`, `<div class="cook" data-view="ings">`, `<header class="cook-bar">`,
			`<a class="button secondary cook-exit" href="/meals/recipes/`+id+`">`, "Stop cooking", `<h1 class="cook-title">Erdnuss-Curry mit Süßkartoffeln</h1>`,
			`<form class="cook-form" method="get"`, `<script src="/static/cook.js"`)
		rcpLacks(t, who, page.body, "frame-nav", "frame-bar", `class="lang"`, "theme-pick", `class="signout"`, "crumbs", `class="frame`, "/static/form.js")
	}
	// The script is for this page only.
	rcpLacks(t, "the recipe page", rb.b.do(http.MethodGet, "/meals/recipes/"+id, nil, enHeader).body, "cook.js")
	// Not signed in: the sign-in page.
	b := signedInBox(t)
	b.logout()
	if got := b.do(http.MethodGet, cookPath(id), nil, enHeader); got.status != http.StatusSeeOther || !strings.HasPrefix(got.location, "/login") {
		t.Errorf("signed out = %d %s", got.status, got.location)
	}
}

// Acceptance: a missing or deleted recipe shows the normal frame with a way back.
func TestAnUnknownOrDeletedRecipeShowsNotFoundInTheNormalFrame(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	id := rb.cookCurry(t)
	if err := rb.b.db.DeleteRecipe(rb.hid, id); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{cookPath(id), cookPath("nope"), cookPath(id, "step=2", "done=1")} {
		got := rb.child.do(http.MethodGet, p, nil, enHeader)
		if got.status != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", p, got.status)
		}
		rcpHas(t, p, got.body, `class="frame framed has-nav"`, `<a href="/meals">Meals</a>`, `<a href="/meals/recipes">Recipes</a>`, `<span aria-current="page">Not found</span>`,
			`<h1>Recipe not found</h1>`, `<div class="empty">`, "This recipe does not exist any more. Maybe someone deleted it while you were cooking.", `<a class="button" href="/meals/recipes">To the recipes</a>`)
		rcpLacks(t, p, got.body, `class="cook"`, "cook.js")
	}
	de := rb.b.do(http.MethodGet, cookPath("nope"), nil, rcpDE).body
	rcpHas(t, "German", de, "<h1>Rezept nicht gefunden</h1>", "Nicht gefunden", "Zu den Rezepten", "Dieses Rezept gibt es nicht mehr. Vielleicht hat es jemand gelöscht, während du gekocht hast.")
}

// Acceptance: ingredients can be ticked, each with the symbol, the quantity in bold, the variety and the warning for every person.
func TestTheIngredientsAreRowsToTickWithSymbolQuantityVarietyAndWarning(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	id := rb.cookCurry(t)
	page := rb.b.do(http.MethodGet, cookPath(id), nil, enHeader).body
	part := rcpBetween(t, page, `<section class="cook-card cook-ings-part"`, "</section>")
	rcpHas(t, "ingredients", part, `<h2 id="h-ings">Ingredients<span class="cnt">0 of 4 ticked</span></h2>`, `<p class="hint">Tick what you have put out.</p>`, `<ul class="cook-ings">`)
	items := regexp.MustCompile(`(?s)<li><label class="cook-tick">.*?</label></li>`).FindAllString(part, -1)
	if len(items) != 4 {
		t.Fatalf("%d rows in\n%s", len(items), part)
	}
	rcpHas(t, "row 0", items[0], `<input type="checkbox" name="have" value="0">`, `<span class="box" aria-hidden="true">`, `<span class="e" aria-hidden="true">🍠</span>`,
		`<span class="t"><span class="q">600 g</span> Süßkartoffeln</span>`)
	rcpHas(t, "free text", items[1], `name="have" value="1"`, `<span class="q">3 tbsp</span> Currypaste`, `<span class="tag unchk">free text, not checked</span>`, `<path d="M5 7h14M5 12h14M5 17h8"/>`)
	rcpHas(t, "article with a variety and a warning", items[2], `name="have" value="2"`, `<span class="q">3 tbsp</span> Erdnussbutter<span class="variety"> · cremig</span>`, `class="allergen-warn"`, "Contains peanuts: ", "</span>Mia</span> is allergic")
	rcpHas(t, "no quantity", items[3], `name="have" value="3"`, `<span class="t">Salz</span>`)
	rcpLacks(t, "no quantity", items[3], `class="q"`)
	if strings.Count(part, "allergen-warn") != 1 {
		t.Errorf("one warning, at the peanut butter: %s", part)
	}
	de := rb.b.do(http.MethodGet, cookPath(id), nil, rcpDE).body
	rcpHas(t, "German", de, `<span class="q">3 EL</span> Currypaste`, "freier Text, nicht geprüft", "0 von 4 abgehakt", "Hak ab, was du bereitgelegt hast.", `<label for="portions">Portionen</label>`)
}

// Acceptance: ticked ingredients come back from the address and are counted; all ticked says so.
func TestTicksTravelInTheAddressAndAreCounted(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	id := rb.cookCurry(t)
	page := rb.b.do(http.MethodGet, cookPath(id, "have=0", "have=2", "have=2", "have=9", "have=x", "have=-1"), nil, enHeader).body
	if got := cookChecked(page); strings.Join(got, ",") != "0,2" {
		t.Errorf("ticked = %v, want 0 and 2 (a repeat, an unknown or a crafted index is ignored)", got)
	}
	rcpHas(t, "count", page, `<span class="cnt">2 of 4 ticked</span>`, `<small>2 of 4 ticked</small>`)
	all := rb.b.do(http.MethodGet, cookPath(id, "have=0", "have=1", "have=2", "have=3"), nil, enHeader).body
	rcpHas(t, "all", all, `<span class="cnt"><svg class="icon"`, "All 4 ready")
	rcpLacks(t, "all", all, `<span class="cnt">4 of 4 ticked`)
	de := rb.b.do(http.MethodGet, cookPath(id, "have=1"), nil, rcpDE).body
	rcpHas(t, "German", de, "1 von 4 abgehakt")
}

// Acceptance: steps advance one by one; the step is clamped; the buttons say where they lead.
func TestStepsAdvanceOneByOneAndTheStepIsClamped(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	id := rb.cookCurry(t)
	one := rb.b.do(http.MethodGet, cookPath(id), nil, enHeader).body
	card := rcpBetween(t, one, `<section class="cook-card cook-step"`, "</section>")
	rcpHas(t, "step 1", card, `<h2 id="h-step">Step 1 of 3</h2>`, `<ol class="cook-dots" aria-label="Steps">`, `<button type="submit" name="step" value="1" aria-current="step" aria-label="Step 1">1</button>`,
		`<button type="submit" name="step" value="2" aria-label="Step 2">2</button>`, `<p class="cook-text">Süßkartoffeln schälen.</p>`, `<p class="cook-next"><b>Next:</b> Currypaste anrösten.</p>`)
	nav := rcpBetween(t, one, `<div class="cook-nav">`, "</div>")
	rcpHas(t, "step 1 buttons", nav, `<button class="button secondary back for-steps phone-only" type="submit" name="view" value="ings">`, "To the ingredients",
		`<button class="button for-steps" type="submit" name="step" value="2">Next: step 2`, `<button class="button for-ings" type="submit" name="view" value="steps">Go: step 1`)
	rcpLacks(t, "step 1 buttons", nav, `name="done"`)

	two := rb.b.do(http.MethodGet, cookPath(id, "step=2"), nil, enHeader).body
	card = rcpBetween(t, two, `<section class="cook-card cook-step"`, "</section>")
	rcpHas(t, "step 2", card, "Step 2 of 3", `<button class="done" type="submit" name="step" value="1" aria-label="Step 1, already read"><svg class="icon"`,
		`name="step" value="2" aria-current="step" aria-label="Step 2">2</button>`, `<p class="cook-text">Currypaste anrösten.</p>`, `<b>Next:</b> Köcheln lassen.`)
	nav = rcpBetween(t, two, `<div class="cook-nav">`, "</div>")
	rcpHas(t, "step 2 buttons", nav, `<button class="button secondary back for-steps" type="submit" name="step" value="1">`, "Back", `name="step" value="3">Next: step 3`, `<button class="button for-ings" type="submit" name="view" value="steps">Continue with step 2`)
	rcpLacks(t, "step 2 buttons", nav, "phone-only")
	if cookView(two) != "steps" {
		t.Errorf("view = %q: a step in the address means the steps part", cookView(two))
	}

	last := rb.b.do(http.MethodGet, cookPath(id, "step=3"), nil, enHeader).body
	card = rcpBetween(t, last, `<section class="cook-card cook-step"`, "</section>")
	rcpHas(t, "last step", card, "Step 3 of 3", `<p class="cook-next cook-done-line"><svg class="icon"`, "That is the last step.")
	rcpLacks(t, "last step", card, "Next:")
	nav = rcpBetween(t, last, `<div class="cook-nav">`, "</div>")
	rcpHas(t, "last buttons", nav, `<button class="button for-steps" type="submit" name="done" value="1"><svg class="icon"`, "Done</button>")

	for _, s := range []string{"99", "4", "3000000000000000000000"} {
		if got := rb.b.do(http.MethodGet, cookPath(id, "step="+s), nil, enHeader).body; !strings.Contains(got, "Step 3 of 3") {
			t.Errorf("step=%s is not clamped to the last step", s)
		}
	}
	for _, s := range []string{"0", "-4", "x", ""} {
		if got := rb.b.do(http.MethodGet, cookPath(id, "step="+s), nil, enHeader).body; !strings.Contains(got, "Step 1 of 3") {
			t.Errorf("step=%q is not clamped to the first step", s)
		}
	}
	de := rb.b.do(http.MethodGet, cookPath(id, "step=2"), nil, rcpDE).body
	rcpHas(t, "German", de, "Schritt 2 von 3", "Schritt 1, schon gelesen", "<b>Danach:</b>", "Weiter zu Schritt 3", "Weiter mit Schritt 2", "Zurück", "Schritte")
	rcpHas(t, "German last", rb.b.do(http.MethodGet, cookPath(id, "step=3"), nil, rcpDE).body, "Das ist der letzte Schritt.", "Fertig</button>")
}

// Steps are all in the page, so script can move between them without the box.
func TestEveryStepIsInThePageForTheScript(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	id := rb.cookCurry(t)
	page := rb.b.do(http.MethodGet, cookPath(id, "step=2"), nil, enHeader).body
	for k, text := range []string{"Süßkartoffeln schälen.", "Currypaste anrösten.", "Köcheln lassen."} {
		tpl := rcpBetween(t, page, `<template data-cook-step="`+string(rune('1'+k))+`">`, "</template>")
		rcpHas(t, "step template", tpl, `<section class="cook-card cook-step"`, `<p class="cook-text">`+text+`</p>`, `<div class="cook-nav">`)
	}
	live := rcpLive(page)
	if strings.Count(live, `class="cook-text"`) != 1 {
		t.Errorf("the page shows one step at a time")
	}
}

// The view: phones show one part; it is ingredients unless a step is asked for.
func TestThePhoneViewIsIngredientsUnlessAStepOrTheViewSaysOtherwise(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	id := rb.cookCurry(t)
	for _, c := range []struct {
		q    []string
		want string
	}{
		{nil, "ings"}, {[]string{"step=1"}, "steps"}, {[]string{"step=2", "view=ings"}, "ings"}, {[]string{"view=steps"}, "steps"},
		{[]string{"view=ings", "view=steps"}, "steps"}, {[]string{"view=zzz"}, "ings"}, {[]string{"step=2", "view=zzz"}, "steps"},
	} {
		page := rb.b.do(http.MethodGet, cookPath(id, c.q...), nil, enHeader).body
		if got := cookView(page); got != c.want {
			t.Errorf("%v: view = %q, want %q", c.q, got, c.want)
		}
	}
	page := rb.b.do(http.MethodGet, cookPath(id, "step=2", "view=steps", "have=1"), nil, enHeader).body
	tabs := rcpBetween(t, page, `<nav class="cook-tabs"`, "</nav>")
	rcpHas(t, "tabs", tabs, `aria-label="View"`, `<button type="submit" name="view" value="ings">Ingredients<small>1 of 4 ticked</small></button>`,
		`<button type="submit" name="view" value="steps" aria-current="true">Steps<small>Step 2 of 3</small></button>`)
	// What a form without script needs to keep: the view, the step and the numbers the buttons are measured from.
	form := rcpBetween(t, page, `<form class="cook-form"`, `<nav class="cook-tabs"`)
	rcpHas(t, "state in the form", form, `<input type="hidden" name="view" value="steps">`, `<input type="hidden" name="step" value="2">`)
}

// Acceptance: the server scales the quantities and rounds them to one decimal; a sentence says so.
func TestPortionsScaleTheQuantitiesOnTheServerAndASentenceSaysSo(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	id := rb.cookCurry(t)
	base := rb.b.do(http.MethodGet, cookPath(id), nil, enHeader).body
	rcpHas(t, "base", base, `<label for="portions">Servings</label>`, `<input id="portions" name="portions" type="text" inputmode="numeric" value="4" autocomplete="off">`,
		`<button class="button secondary step" type="submit" name="nudge" value="3" aria-label="One serving less">`, `<button class="button secondary step" type="submit" name="nudge" value="5" aria-label="One serving more">`)
	rcpLacks(t, "base", base, "converted", `class="ferr"`, "invalid")
	six := rb.b.do(http.MethodGet, cookPath(id, "portions=6"), nil, enHeader).body
	rcpHas(t, "six", six, `<span class="q">900 g</span> Süßkartoffeln`, `<span class="q">4.5 tbsp</span> Currypaste`, `<span class="q">4.5 tbsp</span> Erdnussbutter`, `value="6" autocomplete="off"`,
		`<p class="hint">Quantities converted from 4 to 6 servings, rounded to one decimal.</p>`, `name="nudge" value="5" aria-label="One serving less"`, `name="nudge" value="7" aria-label="One serving more"`)
	one := rb.b.do(http.MethodGet, cookPath(id, "portions=1"), nil, enHeader).body
	rcpHas(t, "one", one, `<span class="q">150 g</span> Süßkartoffeln`, `<span class="q">0.8 tbsp</span> Currypaste`, "Quantities converted from 4 to 1 serving, rounded to one decimal.")
	rcpHas(t, "one, German", rb.b.do(http.MethodGet, cookPath(id, "portions=1"), nil, rcpDE).body, `<span class="q">0,8 EL</span> Currypaste`, "Mengen umgerechnet von 4 auf 1 Portion, auf eine Nachkommastelle gerundet.")
	rcpHas(t, "six, German", rb.b.do(http.MethodGet, cookPath(id, "portions=6"), nil, rcpDE).body, `<span class="q">4,5 EL</span> Currypaste`, "Mengen umgerechnet von 4 auf 6 Portionen, auf eine Nachkommastelle gerundet.")
	same := rb.b.do(http.MethodGet, cookPath(id, "portions=4"), nil, enHeader).body
	rcpLacks(t, "the base portions", same, "converted")
	// The stepper stays within 1 and 99.
	rcpHas(t, "1", rb.b.do(http.MethodGet, cookPath(id, "portions=1"), nil, enHeader).body, `name="nudge" value="1" aria-label="One serving less"`, `name="nudge" value="2" aria-label="One serving more"`)
	rcpHas(t, "99", rb.b.do(http.MethodGet, cookPath(id, "portions=99"), nil, enHeader).body, `name="nudge" value="98" aria-label="One serving less"`, `name="nudge" value="99" aria-label="One serving more"`)
	// The recipe itself did not change.
	if r, err := rb.b.db.Recipe(rb.hid, id); err != nil || r.Portions != 4 || r.Ingredients[0].Amount != 600 {
		t.Errorf("recipe = %+v %v", r, err)
	}
}

// Acceptance: a wrong number stays in the field, the error is at the field, the quantities stay as they were.
func TestAWrongPortionsNumberStaysInTheFieldWithTheErrorAtIt(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	id := rb.cookCurry(t)
	for _, typed := range []string{"0", "100", "abc", "-2", "1.5", "", "٣"} {
		got := rb.b.do(http.MethodGet, cookPath(id, "portions="+url.QueryEscape(typed), "step=2"), nil, enHeader)
		if got.status != http.StatusOK {
			t.Fatalf("portions=%q = %d", typed, got.status)
		}
		page := got.body
		rcpHas(t, "portions="+typed, page, `<div class="cook-portions amount invalid">`, `value="`+typed+`" autocomplete="off" aria-invalid="true" aria-describedby="err-portions"`,
			`<p class="ferr" id="err-portions" role="alert">`, "Enter a number from 1 to 99. The quantities still apply to 4 servings.", `<span class="q">600 g</span> Süßkartoffeln`)
		rcpLacks(t, "portions="+typed, page, "converted")
		if !strings.Contains(page, "Step 2 of 3") {
			t.Errorf("portions=%q lost the step", typed)
		}
	}
	rcpHas(t, "German", rb.b.do(http.MethodGet, cookPath(id, "portions=0"), nil, rcpDE).body, "Gib eine Zahl von 1 bis 99 ein. Die Mengen gelten weiter für 4 Portionen.")
	// Typed with spaces, a number is a number; a typed value is shown escaped.
	if got := rb.b.do(http.MethodGet, cookPath(id, "portions=+6+"), nil, enHeader).body; !strings.Contains(got, "converted from 4 to 6") {
		t.Errorf("spaces around a number are ignored")
	}
	xss := rb.b.do(http.MethodGet, cookPath(id, "portions="+url.QueryEscape(`"><script>alert(1)</script>`)), nil, enHeader).body
	rcpLacks(t, "typed text", xss, "<script>alert(1)</script>")
}

// The stepper buttons are submit buttons; without script the two numbers they send with the typed one are told apart.
func TestWithoutScriptThePortionButtonsAndTheTypedNumberAreToldApart(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	id := rb.cookCurry(t)
	for _, c := range []struct {
		name string
		q    []string
		want string
	}{
		{"enter after typing 6", []string{"portions=6"}, "value=\"6\" autocomplete"},
		{"minus", []string{"portions=4", "nudge=3"}, "value=\"3\" autocomplete"},
		{"plus", []string{"portions=4", "nudge=5"}, "value=\"5\" autocomplete"},
		{"typed 3 then plus", []string{"portions=3", "nudge=5"}, "value=\"5\" autocomplete"},
		{"typed 5 then minus", []string{"portions=5", "nudge=3"}, "value=\"3\" autocomplete"},
		{"typed 7 then plus", []string{"portions=7", "nudge=5"}, "value=\"5\" autocomplete"},
		{"minus at 1", []string{"portions=1", "nudge=1"}, "value=\"1\" autocomplete"},
		{"plus at 99", []string{"portions=99", "nudge=99"}, "value=\"99\" autocomplete"},
		{"nudge that is no number is ignored", []string{"portions=6", "nudge=x"}, "value=\"6\" autocomplete"},
	} {
		page := rb.b.do(http.MethodGet, cookPath(id, c.q...), nil, enHeader).body
		if !strings.Contains(page, c.want) {
			t.Errorf("%s: %v does not show %s\n%s", c.name, c.q, c.want, rcpBetween(t, page, `<div class="cook-portions`, "</div>"))
		}
	}
	page := rb.b.do(http.MethodGet, cookPath(id, "portions=6"), nil, enHeader).body
	form := rcpBetween(t, page, `<div class="cook-portions`, `</div>`)
	rcpHas(t, "buttons", form, `name="nudge" value="5"`, `name="nudge" value="7"`)
}

// Acceptance: "Done" goes back to the recipe with a success line and saves nothing.
func TestDoneGoesBackToTheRecipeWithASuccessLineAndWritesNothing(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	id := rb.cookCurry(t)
	before, _ := rb.b.db.Recipe(rb.hid, id)
	for who, b := range map[string]*box{"parent": rb.b, "child": rb.child} {
		done := b.do(http.MethodGet, cookPath(id, "step=3", "done=1", "have=1"), nil, rcpDE)
		if done.status != http.StatusSeeOther || done.location != "/meals/recipes/"+id {
			t.Fatalf("%s: done = %d %s", who, done.status, done.location)
		}
		page := b.do(http.MethodGet, done.location, nil, rcpDE).body
		rcpHas(t, who, page, `<p class="msg ok" role="status"`, "Fertig gekocht: „Erdnuss-Curry mit Süßkartoffeln“. Guten Appetit!")
		if again := b.do(http.MethodGet, done.location, nil, rcpDE).body; strings.Contains(again, "Guten Appetit") {
			t.Errorf("%s: the line shows once", who)
		}
	}
	en := rb.b.do(http.MethodGet, cookPath(id, "done=1"), nil, enHeader)
	rcpHas(t, "English", rb.b.do(http.MethodGet, en.location, nil, enHeader).body, "Finished cooking: “Erdnuss-Curry mit Süßkartoffeln”. Enjoy your meal!")
	after, _ := rb.b.db.Recipe(rb.hid, id)
	if !after.UpdatedAt.Equal(before.UpdatedAt) || after.Portions != before.Portions {
		t.Errorf("cooking wrote something: %+v", after)
	}
	// "done" without the value 1 does nothing.
	if got := rb.b.do(http.MethodGet, cookPath(id, "done=0"), nil, enHeader); got.status != http.StatusOK {
		t.Errorf("done=0 = %d", got.status)
	}
}

// Acceptance: the screen switch and its notice: rendered hidden, shown by script; without script the notice says what to do.
func TestTheWakeLockSwitchIsHiddenUntilTheScriptKnowsTheBrowserCan(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	id := rb.cookCurry(t)
	page := rb.b.do(http.MethodGet, cookPath(id), nil, enHeader).body
	bar := rcpBetween(t, page, `<header class="cook-bar">`, "</header>")
	rcpHas(t, "bar", bar, `<label class="cook-wake" hidden><span>Keep screen on</span><b class="state" data-label-on="On" data-label-off="Off">On</b><span class="switch"><input type="checkbox" role="switch" checked></span></label>`)
	notice := "This device does not let Hejmo keep the screen on. Set a longer auto-lock in the device’s settings while you cook."
	rcpHas(t, "notice", page, `<p class="notice" role="status" hidden>`, notice, `<noscript><p class="notice" role="status">`)
	de := rb.b.do(http.MethodGet, cookPath(id), nil, rcpDE).body
	rcpHas(t, "German", de, "Bildschirm wach halten", "Dieses Gerät lässt Hejmo den Bildschirm nicht wach halten. Stell die automatische Sperre in den Einstellungen des Geräts länger, solange du kochst.", `data-label-on="An" data-label-off="Aus"`)
}

// Acceptance: the texts the script needs come with the page, in the page's language.
func TestThePageCarriesTheTextsForTheScript(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	id := rb.cookCurry(t)
	form := rcpBetween(t, rb.b.do(http.MethodGet, cookPath(id, "portions=6"), nil, enHeader).body, `<form class="cook-form"`, ">")
	rcpHas(t, "form", form, `data-recipe="`+id+`"`, `data-base="4"`, `data-ticked="{k} of {n} ticked"`, `data-allready="All {n} ready"`,
		`data-portions-err="Enter a number from 1 to 99. The quantities still apply to {n} servings."`, `data-portions-err-one="Enter a number from 1 to 99. The quantities still apply to 1 serving."`,
		`data-portions-offline="Hejmo cannot be reached right now. The quantities still apply to {n} servings."`, `data-portions-offline-one="Hejmo cannot be reached right now. The quantities still apply to 1 serving."`)
}

// Empty states: no steps, no ingredients; parents get a button to write them, children read who can.
func TestARecipeWithoutStepsOrIngredientsSaysSoAndOnlyParentsGetTheButton(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	noSteps := rb.add(t, recipe.Recipe{Name: "Nur Zutaten", Ingredients: []recipe.Ingredient{{Amount: 2, Text: "Eier", ArticleID: "eggs"}}}).ID
	noIngs := rb.add(t, recipe.Recipe{Name: "Nur Schritte", Steps: []string{"Wasser kochen.", "Nudeln dazu."}}).ID
	bare := rb.add(t, recipe.Recipe{Name: "Gurkensalat"}).ID

	parent := rb.b.do(http.MethodGet, cookPath(noSteps, "view=steps"), nil, enHeader).body
	card := rcpBetween(t, parent, `<section class="cook-card cook-step"`, "</section>")
	rcpHas(t, "parent, no steps", card, `<h2 id="h-step">Steps</h2>`, `<div class="empty"><p>This recipe has no steps yet.</p><a class="button secondary" href="/meals/recipes/`+noSteps+`/edit">`, "Add steps")
	rcpLacks(t, "parent, no steps", parent, `class="cook-nav"`, `class="cook-dots"`, "<small>Step")
	rcpHas(t, "parent, no steps", parent, `<button type="submit" name="view" value="steps" aria-current="true">Steps<small>none</small></button>`, `name="have" value="0"`)
	kid := rb.child.do(http.MethodGet, cookPath(noSteps), nil, enHeader).body
	rcpHas(t, "child, no steps", rcpBetween(t, kid, `<section class="cook-card cook-step"`, "</section>"), "This recipe has no steps yet. Your parents can add them.")
	rcpLacks(t, "child, no steps", kid, "/edit", "Add steps", `class="cook-nav"`)

	parent = rb.b.do(http.MethodGet, cookPath(noIngs), nil, enHeader).body
	part := rcpBetween(t, parent, `<section class="cook-card cook-ings-part"`, "</section>")
	rcpHas(t, "parent, no ingredients", part, `<h2 id="h-ings">Ingredients</h2>`, `<div class="empty"><p>This recipe has no ingredients.</p><a class="button secondary" href="/meals/recipes/`+noIngs+`/edit">`, "Add ingredients")
	rcpLacks(t, "parent, no ingredients", part, "cook-portions", `class="cnt"`, "cook-ings\"", "hint")
	rcpHas(t, "parent, no ingredients", parent, `Ingredients<small>none</small>`, `Step 1 of 2`)
	kid = rb.child.do(http.MethodGet, cookPath(noIngs), nil, rcpDE).body
	part = rcpBetween(t, kid, `<section class="cook-card cook-ings-part"`, "</section>")
	rcpHas(t, "child, no ingredients", part, "Dieses Rezept hat keine Zutaten. Deine Eltern können sie eintragen.")
	rcpLacks(t, "child, no ingredients", part, "/edit", "Zutaten eintragen")

	both := rb.b.do(http.MethodGet, cookPath(bare), nil, enHeader)
	if both.status != http.StatusOK {
		t.Fatalf("a recipe with only a name = %d", both.status)
	}
	rcpHas(t, "bare", both.body, "This recipe has no ingredients.", "This recipe has no steps yet.")
}

// The portions field is the recipe's own number when nothing is asked for.
func TestTheCookingViewStartsWithTheRecipesOwnPortions(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	id := rb.add(t, recipe.Recipe{Name: "Suppe für viele", Portions: 12, Ingredients: []recipe.Ingredient{{Amount: 1.5, Unit: "l", Text: "Brühe"}}}).ID
	page := rb.b.do(http.MethodGet, cookPath(id), nil, enHeader).body
	rcpHas(t, "12", page, `value="12" autocomplete="off"`, `<span class="q">1.5 l</span> Brühe`)
	rcpHas(t, "6", rb.b.do(http.MethodGet, cookPath(id, "portions=6"), nil, enHeader).body, `<span class="q">0.8 l</span> Brühe`) // 0.75 is 0.8
}
