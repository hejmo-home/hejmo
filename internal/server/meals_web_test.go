package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

const nextTuesday = "2026-11-17" // the week of Monday 16/11/2026 follows the week of the test clock (Saturday 14/11/2026)

func mealField(day, mealtime string) string { return "meal_" + day + "_" + mealtime }

func TestMealsPageNeedsASignedInPerson(t *testing.T) {
	b, _ := configuredBox(t)
	if r := b.get("/meals"); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Fatalf("signed out = %d %q", r.status, r.location)
	}
}

func TestMealsPageShowsTheWeekOfTodayWithAFieldPerDayAndMealtime(t *testing.T) {
	b := signedInBox(t)
	r := b.do(http.MethodGet, "/meals", nil, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusOK {
		t.Fatalf("page = %d", r.status)
	}
	// Saturday 14/11/2026 lies in the week of Monday 09/11 to Sunday 15/11.
	for _, want := range []string{"09/11 – 15/11/2026", "<strong>Week 46</strong>", "Previous week", "Next week", "Monday", "Sunday", "Breakfast", "Lunch", "Snack", "Dinner",
		`name="week" value="2026-11-09"`, `href="/meals?week=2026-11-02"`, `href="/meals?week=2026-11-16"`, `data-today`, `class="weeknav"`, `class="wbtn"`, `class="wk"`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, day := range []string{"2026-11-09", "2026-11-12", "2026-11-15"} {
		for _, m := range []string{"breakfast", "lunch", "snack", "dinner"} {
			if !strings.Contains(r.body, `name="`+mealField(day, m)+`"`) {
				t.Errorf("no field for %s %s", day, m)
			}
			// A row per mealtime: the label points at the field, the field is a one-line textarea.
			if !strings.Contains(r.body, `<label class="mt" for="`+mealField(day, m)+`">`) || !strings.Contains(r.body, `<textarea id="`+mealField(day, m)+`" name="`+mealField(day, m)+`" rows="1" maxlength="200"`) {
				t.Errorf("no labelled textarea for %s %s", day, m)
			}
		}
	}
	if n := strings.Count(r.body, "<textarea"); n != 28 {
		t.Errorf("%d meal fields, want 28", n)
	}
	if strings.Contains(r.body, "<input name=\"meal_") {
		t.Error("a meal field is still a single-line input")
	}
	if n := strings.Count(r.body, `class="mrow"`); n != 28 {
		t.Errorf("%d meal rows, want 28", n)
	}
	if strings.Contains(r.body, "Back to this week") {
		t.Error("\"Back to this week\" is offered on this week")
	}
	if !regexp.MustCompile(`<fieldset[^>]*data-day="2026-11-14"[^>]*data-today`).MatchString(r.body) {
		t.Error("today is not marked")
	}
	if got := navTargets(t, r.body); !strings.Contains(strings.Join(got, " "), "/meals") {
		t.Errorf("navigation = %v lacks the meal plan", got)
	}
	if !strings.Contains(r.body, `<a href="/meals" aria-current="page"`) {
		t.Error("navigation does not mark the meal plan as current")
	}
}

func TestMealsPageTakesAnyDayOfAWeekAndFallsBackToTodaysWeek(t *testing.T) {
	b := signedInBox(t)
	for _, q := range []string{"2026-11-18", "2026-11-16", "2026-11-22"} { // Wednesday, Monday, Sunday of the same week
		if r := b.get("/meals?week=" + q); !strings.Contains(r.body, `name="week" value="2026-11-16"`) {
			t.Errorf("week=%s does not show the week of 16/11:\n%s", q, r.body)
		}
	}
	for _, q := range []string{"", "soon", "2026-13-40", "../etc"} {
		if r := b.get("/meals?week=" + q); r.status != http.StatusOK || !strings.Contains(r.body, `name="week" value="2026-11-09"`) {
			t.Errorf("week=%q = %d, want today's week", q, r.status)
		}
	}
}

// Acceptance: Given a shared meal plan, when a dinner is entered for Tuesday,
// then it appears in the calendar on Tuesday (web interface, with API parity).
func TestParentPlansADinnerInTheWebInterfaceAndItAppearsInTheCalendar(t *testing.T) {
	c := signedInAPI(t)
	b := c.b
	r := b.do(http.MethodPost, "/meals", url.Values{"week": {"2026-11-16"}, mealField(nextTuesday, "dinner"): {"  Pasta bake "}, mealField(nextTuesday, "lunch"): {""}},
		map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusSeeOther || r.location != "/meals?week=2026-11-16" {
		t.Fatalf("save = %d %q\n%s", r.status, r.location, r.body)
	}
	page := b.do(http.MethodGet, r.location, nil, map[string]string{"Accept-Language": "en"})
	if !strings.Contains(page.body, "Meal plan for week 47 saved.") || !strings.Contains(page.body, `>Pasta bake</textarea>`) {
		t.Fatalf("page after the save:\n%s", page.body)
	}
	occs := c.occurrences(t, "from=2026-11-17T00:00:00%2B01:00&to=2026-11-18T00:00:00%2B01:00")
	if len(occs) != 1 || occs[0]["title"] != "Dinner: Pasta bake" || occs[0]["all_day"] != true {
		t.Fatalf("calendar on Tuesday = %v", occs)
	}
	if list := mealsBetween(t, c, nextTuesday, nextTuesday); len(list) != 1 || list[0]["text"] != "Pasta bake" || list[0]["mealtime"] != "dinner" {
		t.Fatalf("API = %v", list)
	}
}

// Acceptance: Given a meal changed, then the calendar shows the new meal.
func TestChangingAndClearingMealsInTheWebInterfaceUpdatesTheCalendar(t *testing.T) {
	c := signedInAPI(t)
	b := c.b
	en := map[string]string{"Accept-Language": "en"}
	b.do(http.MethodPost, "/meals", url.Values{"week": {"2026-11-16"}, mealField(nextTuesday, "dinner"): {"Pasta bake"}, mealField(nextTuesday, "lunch"): {"Soup"}}, en)
	b.do(http.MethodPost, "/meals", url.Values{"week": {"2026-11-16"}, mealField(nextTuesday, "dinner"): {"Pizza"}, mealField(nextTuesday, "lunch"): {""}}, en)
	got := occTitles(c.occurrences(t, "from=2026-11-17T00:00:00%2B01:00&to=2026-11-18T00:00:00%2B01:00"))
	if len(got) != 1 || got[0] != "11-17 Dinner: Pizza" {
		t.Fatalf("calendar = %v", got)
	}
}

func TestAFormMissingFieldsLeavesThoseMealsAlone(t *testing.T) {
	c := signedInAPI(t)
	putMeal(t, c, nextTuesday, "dinner", "Pasta bake")
	c.b.post("/meals", url.Values{"week": {"2026-11-16"}, mealField(nextTuesday, "lunch"): {"Soup"}})
	if list := mealsBetween(t, c, nextTuesday, nextTuesday); len(list) != 2 {
		t.Fatalf("meals = %v", list)
	}
	// Fields of another week are ignored, so a stale form cannot touch it.
	c.b.post("/meals", url.Values{"week": {"2026-11-16"}, mealField("2026-11-24", "dinner"): {"Elsewhere"}})
	if list := mealsBetween(t, c, "2026-11-24", "2026-11-24"); len(list) != 0 {
		t.Fatalf("a field outside the week was stored: %v", list)
	}
}

func TestTooLongMealShowsAnErrorAtItsFieldKeepsWhatWasTypedAndSavesNothing(t *testing.T) {
	c := signedInAPI(t)
	b := c.b
	r := b.do(http.MethodPost, "/meals", url.Values{"week": {"2026-11-16"}, mealField(nextTuesday, "lunch"): {"Soup"},
		mealField("2026-11-18", "dinner"): {strings.Repeat("x", 201)}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `role="alert"`) || !strings.Contains(r.body, `id="err-`+mealField("2026-11-18", "dinner")+`"`) {
		t.Fatalf("status = %d\n%s", r.status, r.body)
	}
	if !strings.Contains(r.body, `>Soup</textarea>`) || !strings.Contains(r.body, strings.Repeat("x", 201)) {
		t.Errorf("typed values were not kept:\n%s", r.body)
	}
	if list := mealsBetween(t, c, "2026-11-01", "2026-11-30"); len(list) != 0 {
		t.Fatalf("a refused save stored %v", list)
	}
	// The row is marked, the field points at its error and takes the focus, and a line above
	// the week says nothing was saved and links to the field.
	field := mealField("2026-11-18", "dinner")
	for _, want := range []string{`<div class="mrow invalid">`, `aria-invalid="true"`, `aria-describedby="err-` + field + `"`, ` autofocus`,
		`class="error form-error" role="alert"`, "Nothing was saved. Please shorten", `<a href="#` + field + `">Wednesday, Dinner</a>`, "everything else you typed is still here."} {
		if !strings.Contains(r.body, want) {
			t.Errorf("error page lacks %q:\n%s", want, r.body)
		}
	}
	if n := strings.Count(r.body, ` autofocus`); n != 1 {
		t.Errorf("%d fields take the focus, want 1", n)
	}
	if strings.Index(r.body, "form-error") > strings.Index(r.body, `<form method="post" action="/meals"`) {
		t.Error("the form error does not sit above the week")
	}
}

func TestFormErrorIsGermanInDuForm(t *testing.T) {
	c := signedInAPI(t)
	r := c.b.do(http.MethodPost, "/meals", url.Values{"week": {"2026-11-16"}, mealField("2026-11-18", "dinner"): {strings.Repeat("x", 201)}}, map[string]string{"Accept-Language": "de"})
	for _, want := range []string{"Nichts wurde gespeichert. Bitte kürze", `>Mittwoch, Abendessen</a>`, "alles andere, was du eingetragen hast, ist noch da."} {
		if !strings.Contains(r.body, want) {
			t.Errorf("German error page lacks %q", want)
		}
	}
}

func TestMealPlanTitlesFollowTheLanguageOfTheBrowserThatSaves(t *testing.T) {
	c := signedInAPI(t)
	c.b.do(http.MethodPost, "/meals", url.Values{"week": {"2026-11-16"}, mealField(nextTuesday, "dinner"): {"Nudelauflauf"}}, map[string]string{"Accept-Language": "de"})
	if got := occTitles(c.occurrences(t, "from=2026-11-17T00:00:00%2B01:00&to=2026-11-18T00:00:00%2B01:00")); len(got) != 1 || got[0] != "11-17 Abendessen: Nudelauflauf" {
		t.Fatalf("calendar = %v", got)
	}
	page := c.b.do(http.MethodGet, "/meals?week=2026-11-16", nil, map[string]string{"Accept-Language": "de"})
	for _, want := range []string{"Dienstag", "Abendessen", "Frühstück", "Woche 47 speichern", "<strong>Woche 47</strong>", "16/11 – 22/11/2026", "Vorherige Woche", "Zurück zu dieser Woche",
		"Rezepte-Bibliothek kommt in einer späteren Version – bis dahin einfach das Gericht eintragen.", "Änderungen noch nicht gespeichert", "Plane die Mahlzeiten für die Woche."} {
		if !strings.Contains(page.body, want) {
			t.Errorf("German page lacks %q", want)
		}
	}
}

func TestChildSeesTheMealPlanButCannotChangeIt(t *testing.T) {
	b := signedInBox(t)
	b.post("/meals", url.Values{"week": {"2026-11-16"}, mealField(nextTuesday, "dinner"): {"Pasta bake"}})
	child := childSession(t, b)
	r := child.do(http.MethodGet, "/meals?week=2026-11-16", nil, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusOK || !strings.Contains(r.body, "Pasta bake") {
		t.Fatalf("child page = %d\n%s", r.status, r.body)
	}
	if strings.Contains(r.body, `<form method="post" action="/meals"`) || strings.Contains(r.body, "<input name=\"meal_") || strings.Contains(r.body, "<textarea") || strings.Contains(r.body, `class="savebar"`) {
		t.Errorf("a child's page offers editing:\n%s", r.body)
	}
	if strings.Contains(r.body, `class="hint recipe"`) || strings.Contains(r.body, "recipe library") {
		t.Error("a child sees the recipe note")
	}
	// Read view: the mealtime has its icon, the dish is large, an empty day says so.
	for _, want := range []string{`<dt class="mt"><svg class="icon"`, "Dinner</dt><dd>Pasta bake</dd>", `<p class="none">Nothing planned.</p>`, "What the family is eating this week."} {
		if !strings.Contains(r.body, want) {
			t.Errorf("child page lacks %q:\n%s", want, r.body)
		}
	}
	if strings.Contains(r.body, `class="hint">Nothing planned.`) {
		t.Error("the empty day is still a hint")
	}
	if got := child.post("/meals", url.Values{"week": {"2026-11-16"}, mealField(nextTuesday, "dinner"): {"Pizza"}}); got.status != http.StatusForbidden {
		t.Fatalf("child post = %d, want 403", got.status)
	}
}

func TestMealsPageUsesSharedPatternsAndNoInlineStyles(t *testing.T) {
	b := signedInBox(t)
	body := b.get("/meals").body
	for _, want := range []string{`class="f`, `href="/static/hejmo.css"`, `type="submit"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "style=") || strings.Contains(body, "<style") {
		t.Error("inline styles")
	}
	if n := strings.Count(body, `class="button"`); n != 1 {
		t.Errorf("%d primary buttons, want exactly one per screen (ADR 0017)", n)
	}
}

func TestMealsPageShowsAnEmptyHintWhileNothingIsPlanned(t *testing.T) {
	b := signedInBox(t)
	if body := b.do(http.MethodGet, "/meals", nil, map[string]string{"Accept-Language": "en"}).body; !strings.Contains(body, "Nothing planned for week 46 yet") {
		t.Errorf("no hint:\n%s", body)
	}
	b.post("/meals", url.Values{"week": {"2026-11-09"}, mealField("2026-11-10", "dinner"): {"x"}})
	if body := b.do(http.MethodGet, "/meals", nil, map[string]string{"Accept-Language": "en"}).body; strings.Contains(body, "Nothing planned for week 46 yet") {
		t.Errorf("hint although something is planned")
	}
}

func TestParentSeesTheRecipeNoteSaveBarAndDayHeaders(t *testing.T) {
	b := signedInBox(t)
	body := b.do(http.MethodGet, "/meals", nil, map[string]string{"Accept-Language": "en"}).body
	for _, want := range []string{`<p class="hint recipe"><svg class="icon"`, "A recipe library comes in a later version – until then, just type the dish.",
		`<div class="savebar"><button class="button" type="submit">Save week 46</button><span class="dirty" hidden>Changes not saved yet</span></div>`,
		"Plan the meals for the week. Every meal also shows in the calendar, and displays show today&#39;s meals until their time is over."} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if !regexp.MustCompile(`<legend>Saturday <span class="date">14/11</span> <span class="today">Today</span></legend>`).MatchString(body) {
		t.Error("today's day header lacks the date and the Today label")
	}
	if !regexp.MustCompile(`<legend>Monday <span class="date">09/11</span></legend>`).MatchString(body) {
		t.Error("a day header lacks the date")
	}
}

func TestOtherWeeksOfferBackToThisWeekAndTheEmptyHintNamesTheWeek(t *testing.T) {
	b := signedInBox(t)
	body := b.do(http.MethodGet, "/meals?week=2026-11-23", nil, map[string]string{"Accept-Language": "en"}).body
	for _, want := range []string{`<a class="wbtn this" href="/meals">Back to this week</a>`, "<strong>Week 48</strong>", "23/11 – 29/11/2026",
		"Nothing planned for week 48 yet. Type a dish into any field, then save the week.", "Save week 48"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	de := b.do(http.MethodGet, "/meals?week=2026-11-23", nil, map[string]string{"Accept-Language": "de"}).body
	if !strings.Contains(de, "Für Woche 48 ist noch nichts geplant. Trag ein Gericht in ein Feld ein und speichere die Woche.") {
		t.Errorf("German empty hint missing:\n%s", de)
	}
}

func TestMealsSuccessLineNamesTheWeekAndTheCalendarIsCalledLikeThePage(t *testing.T) {
	c := signedInAPI(t)
	r := c.b.do(http.MethodPost, "/meals", url.Values{"week": {"2026-11-09"}, mealField("2026-11-10", "dinner"): {"x"}}, map[string]string{"Accept-Language": "de"})
	if got := successLine(c.b.do(http.MethodGet, r.location, nil, map[string]string{"Accept-Language": "de"}).body); got != "Essensplan für Woche 46 gespeichert." {
		t.Errorf("success line = %q", got)
	}
}
