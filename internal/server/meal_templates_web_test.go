package server_test

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var en = map[string]string{"Accept-Language": "en"}
var de = map[string]string{"Accept-Language": "de"}

func tplField(weekday int, mealtime string) string {
	return "tpl_" + string(rune('0'+weekday)) + "_" + mealtime
}

// weekBForm is the form of a template that fills dinner Monday to Wednesday
// every 2 weeks from the week of Monday 16/11/2026.
func weekBForm(name, start string, every string) url.Values {
	return url.Values{
		"name": {name}, "fills": {"one"}, "mealtime": {"dinner"}, "every": {every}, "start": {start},
		tplField(1, "dinner"): {"Lentil soup"}, tplField(2, "dinner"): {"Fish fingers"}, tplField(3, "dinner"): {"Spaghetti bolognese"},
		tplField(1, "lunch"): {"Ignored"}, // a mealtime the template does not fill
	}
}

func saveTemplate(t *testing.T, c *apiClient, form url.Values, headers map[string]string) resp {
	t.Helper()
	return c.b.do(http.MethodPost, "/meals/templates", form, headers)
}

func templateID(t *testing.T, c *apiClient, name string) string {
	t.Helper()
	_, _, list := c.call(http.MethodGet, "/meal-templates", nil)
	for _, x := range list {
		if m := x.(map[string]any); m["name"] == name {
			return m["id"].(string)
		}
	}
	t.Fatalf("no template %q in %v", name, list)
	return ""
}

func TestTemplatePagesNeedAParent(t *testing.T) {
	c := signedInAPI(t)
	child := childSession(t, c.b)
	for _, path := range []string{"/meals/templates", "/meals/templates/new"} {
		if r := c.b.get(path); r.status != http.StatusOK {
			t.Errorf("parent %s = %d", path, r.status)
		}
		if r := child.get(path); r.status != http.StatusForbidden {
			t.Errorf("child %s = %d, want 403", path, r.status)
		}
	}
	if r := child.post("/meals/templates", weekBForm("Mine", "2026-11-16", "1")); r.status != http.StatusForbidden {
		t.Errorf("child create = %d, want 403", r.status)
	}
	b, _ := configuredBox(t)
	if r := b.get("/meals/templates"); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Errorf("signed out = %d %q", r.status, r.location)
	}
}

func TestTheTemplatesMenuInvitesToCreateTheFirstTemplate(t *testing.T) {
	c := signedInAPI(t)
	r := c.b.do(http.MethodGet, "/meals/templates", nil, en)
	for _, want := range []string{"<h1>Meal plan templates</h1>", `<a class="back" href="/meals">Back to meal plan</a>`, `class="empty"`, `href="/meals/templates/new"`, "New template", `<a href="/meals" aria-current="page"`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(r.body, "ahead") {
		t.Error("the coming weeks are listed without a template")
	}
}

// Acceptance: create, edit and delete templates in the Templates menu.
func TestParentCreatesATemplateAndTheMenuListsItWithTheComingWeeks(t *testing.T) {
	c := signedInAPI(t)
	r := saveTemplate(t, c, weekBForm("Week B", "2026-11-16", "2"), en)
	if r.status != http.StatusSeeOther || r.location != "/meals/templates" {
		t.Fatalf("save = %d %q\n%s", r.status, r.location, r.body)
	}
	page := c.b.do(http.MethodGet, r.location, nil, en)
	if got := successLine(page.body); got != "Template “Week B” saved. It fills the meal plan from today on." {
		t.Errorf("success line = %q", got)
	}
	for _, want := range []string{
		`<a class="tpl-row" href="/meals/templates/` + templateID(t, c, "Week B") + `">`, "Week B",
		"<b>Dinner</b> on Monday to Wednesday", "Every 2 weeks from week 47 (16/11/2026) · next in week 47 (16/11 – 22/11)",
		"<h2>The coming weeks</h2>", `class="ahead"`, "<b>Week 46</b>", "No template – plan it yourself", `<span class="today">This week</span>`,
		"<b>Week B</b>: dinner", `class="primary`,
	} {
		if !strings.Contains(strings.ReplaceAll(page.body, `class="button"`, `class="primary`), want) {
			t.Errorf("menu lacks %q:\n%s", want, page.body)
		}
	}
	if strings.Count(page.body, `<li><div class="wkn">`) != 6 {
		t.Errorf("the coming weeks are not six")
	}
	// The entry of a mealtime the template does not fill is ignored.
	got, _, _ := c.call(http.MethodGet, "/meal-templates/"+templateID(t, c, "Week B"), nil)
	_ = got
	_, tpl, _ := c.call(http.MethodGet, "/meal-templates/"+templateID(t, c, "Week B"), nil)
	if n := len(tpl["entries"].([]any)); n != 3 {
		t.Errorf("entries = %v", tpl["entries"])
	}
}

func TestTheTemplateFormListsTheWeeksItFillsAcrossTheTurnOfTheYear(t *testing.T) {
	c := signedInAPI(t)
	bad := weekBForm("", "2026-11-16", "2") // no name: the page comes back with the form
	r := saveTemplate(t, c, bad, en)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", r.status)
	}
	if !strings.Contains(r.body, `Fills week <b>47</b>, <b>49</b>, <b>51</b>, <b>53</b>, then week <b>2</b> of 2027 (11/01/2027), and so on.`) {
		t.Errorf("weeks hint missing:\n%s", r.body)
	}
	r = saveTemplate(t, c, bad, de)
	if !strings.Contains(r.body, `Füllt Woche <b>47</b>, <b>49</b>, <b>51</b>, <b>53</b>, dann Woche <b>2</b> 2027 (11/01/2027) und so weiter.`) {
		t.Errorf("German weeks hint missing:\n%s", r.body)
	}
	// This week's week: the first week is marked.
	bad.Set("start", "2026-11-09")
	if r = saveTemplate(t, c, bad, en); !strings.Contains(r.body, `Fills week <b>46</b> (this week, from today), <b>48</b>, <b>50</b>, <b>52</b>, then week <b>1</b> of 2027 (04/01/2027), and so on.`) {
		t.Errorf("this week's hint missing:\n%s", r.body)
	}
}

func TestTheTemplateFormHasEveryFieldOfTheDesign(t *testing.T) {
	c := signedInAPI(t)
	r := c.b.do(http.MethodGet, "/meals/templates/new", nil, en)
	for _, want := range []string{
		`<a class="back" href="/meals/templates">Back to templates</a>`, "<h1>New template</h1>", `class="steps wide tpl-form"`, `action="/meals/templates"`,
		`name="name"`, `maxlength="60"`, "Which meals does it fill?", `name="fills" value="one"`, `name="fills" value="all"`, `class="choice mtpick"`, `name="mealtime" value="dinner"`,
		`id="schedule"`, `class="every"`, `name="every"`, `min="1" max="52"`, `name="start"`, `class="weeks-hint"`, "<h2>Meals of the week</h2>",
		`data-mt="breakfast"`, `name="tpl_7_dinner"`, `name="tpl_1_breakfast"`, "Save template", `class="savebar"`, "Week 46 · 09/11 – 15/11", "Week 45 · 08/11 – 14/11",
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("form lacks %q", want)
		}
	}
	if n := strings.Count(r.body, "<textarea"); n != 28 {
		t.Errorf("%d fields, want 28", n)
	}
	if strings.Contains(r.body, "details") {
		t.Error("a new template cannot be deleted")
	}
	if n := strings.Count(r.body, `<option`); n != 53 {
		t.Errorf("%d start weeks, want the current week and 52 ahead", n)
	}
}

func TestATemplateNeedsANameAnIntervalAndAMeal(t *testing.T) {
	c := signedInAPI(t)
	r := saveTemplate(t, c, weekBForm(" ", "2026-11-16", "2"), en)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "Give the template a name, for example “Week A”.") || !strings.Contains(r.body, `id="err-name"`) ||
		!strings.Contains(r.body, "Lentil soup") || !strings.Contains(r.body, `value="2"`) {
		t.Fatalf("no name = %d\n%s", r.status, r.body)
	}
	r = saveTemplate(t, c, weekBForm("Week B", "2026-11-16", "0"), en)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "Choose a number from 1 to 52.") || !regexp.MustCompile(`id="schedule"[^>]*aria-invalid="true"`).MatchString(r.body) {
		t.Fatalf("interval 0 = %d\n%s", r.status, r.body)
	}
	for _, every := range []string{"53", "x", ""} {
		if r = saveTemplate(t, c, weekBForm("Week B", "2026-11-16", every), en); r.status != http.StatusUnprocessableEntity {
			t.Errorf("interval %q = %d", every, r.status)
		}
	}
	empty := weekBForm("Week B", "2026-11-16", "2")
	for _, k := range []string{tplField(1, "dinner"), tplField(2, "dinner"), tplField(3, "dinner")} {
		empty.Set(k, "")
	}
	r = saveTemplate(t, c, empty, en)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "Type at least one meal; an empty template fills nothing.") {
		t.Fatalf("empty = %d\n%s", r.status, r.body)
	}
	if _, _, list := c.call(http.MethodGet, "/meal-templates", nil); len(list) != 0 {
		t.Fatalf("%d templates saved", len(list))
	}
}

// Acceptance: overlapping templates are refused at save with a message naming the clash.
func TestAClashingTemplateIsRefusedNamingTheOtherEverythingTypedStaysAndNothingIsSaved(t *testing.T) {
	c := signedInAPI(t)
	saveTemplate(t, c, weekBForm("Week A", "2026-11-16", "2"), en)
	form := weekBForm("Every week", "2026-11-16", "1")
	r := saveTemplate(t, c, form, en)
	if r.status != http.StatusConflict {
		t.Fatalf("status = %d\n%s", r.status, r.body)
	}
	for _, want := range []string{
		`Nothing was saved: this template clashes with “Week A” (see <a href="#schedule">When does it repeat?</a>). Everything you typed is still here.`,
		`id="schedule" aria-invalid="true"`,
		`“Week A” also fills dinner on Monday, Tuesday and Wednesday, first in week 47 (16/11 – 22/11/2026). Choose another start week or interval, or leave those days empty in one of the two templates.`,
		"Lentil soup", "Fish fingers", `value="Every week"`,
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("page lacks %q\n%s", want, r.body)
		}
	}
	if _, _, list := c.call(http.MethodGet, "/meal-templates", nil); len(list) != 1 {
		t.Fatalf("%d templates after the refusal", len(list))
	}
	// Alternating weeks are fine.
	if r := saveTemplate(t, c, weekBForm("Week B", "2026-11-23", "2"), en); r.status != http.StatusSeeOther {
		t.Fatalf("alternating = %d", r.status)
	}
	de := saveTemplate(t, c, form, de)
	if !strings.Contains(de.body, "Nichts wurde gespeichert: Diese Vorlage überschneidet sich mit „Woche A“") && !strings.Contains(de.body, "„Week A“") {
		t.Errorf("German refusal:\n%s", de.body)
	}
}

func TestEditingATemplateShowsItsValuesAndDeletingKeepsPastAndAdjustedMeals(t *testing.T) {
	c := signedInAPI(t)
	saveTemplate(t, c, weekBForm("Week B", "2026-11-16", "2"), en)
	id := templateID(t, c, "Week B")
	r := c.b.do(http.MethodGet, "/meals/templates/"+id, nil, en)
	for _, want := range []string{"<h1>Week B</h1>", `value="Week B"`, "Changes reach today and all coming weeks.", `action="/meals/templates/` + id + `"`, ">Lentil soup</textarea>",
		`<option value="2026-11-16" selected>`, `name="fills" value="one" checked`, `name="mealtime" value="dinner" checked`, `<details class="danger"`, "Delete template", `action="/meals/templates/` + id + `/delete"`, "Delete “Week B”"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("edit page lacks %q\n%s", want, r.body)
		}
	}
	form := weekBForm("Week B (school)", "2026-11-16", "2")
	form.Set(tplField(1, "dinner"), "Lasagne")
	r = c.b.do(http.MethodPost, "/meals/templates/"+id, form, en)
	if r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	c.b.do(http.MethodGet, r.location, nil, en) // the success line is shown once
	if list := mealsBetween(t, c, "2026-11-16", "2026-11-16"); len(list) != 1 || list[0]["text"] != "Lasagne" {
		t.Fatalf("Monday = %v", list)
	}
	// An adjusted meal becomes a plain entry when the template is deleted.
	putMeal(t, c, "2026-11-17", "dinner", "Pizza")
	r = c.b.do(http.MethodPost, "/meals/templates/"+id+"/delete", url.Values{}, en)
	if r.status != http.StatusOK || successLine(r.body) != "Template “Week B (school)” deleted. Meals before today and meals you adjusted for a week stay in the meal plan." {
		t.Fatalf("delete = %d %q", r.status, successLine(r.body))
	}
	list := mealsBetween(t, c, "2026-11-16", "2026-11-18")
	if len(list) != 1 || list[0]["text"] != "Pizza" || list[0]["source"] != "manual" {
		t.Fatalf("after delete = %v", list)
	}
	if r := c.b.do(http.MethodGet, "/meals/templates/"+id, nil, en); r.status != http.StatusNotFound {
		t.Errorf("deleted template page = %d", r.status)
	}
}

func TestTheMealPlanSaysWhereMealsComeFromAndOffersBackToTemplate(t *testing.T) {
	c := signedInAPI(t)
	b := c.b
	// Without templates the card invites to create one.
	r := b.do(http.MethodGet, "/meals?week=2026-11-16", nil, en)
	if !strings.Contains(r.body, `class="tpl-sum"`) || !strings.Contains(r.body, `href="/meals/templates/new"`) || !strings.Contains(r.body, "Create a template") {
		t.Fatalf("no invitation:\n%s", r.body)
	}
	saveTemplate(t, c, weekBForm("Week B", "2026-11-16", "2"), en)
	saveTemplate(t, c, url.Values{"name": {"Weekend"}, "fills": {"all"}, "every": {"1"}, "start": {"2026-11-09"}, tplField(6, "breakfast"): {"Pancakes"}}, en)
	id := templateID(t, c, "Week B")
	r = b.do(http.MethodGet, "/meals?week=2026-11-16", nil, en)
	for _, want := range []string{
		`This week is filled by the templates <a href="/meals/templates/` + id + `">Week B</a> (dinner) and <a href="/meals/templates/`, `(all meals)`, "What you change here counts for this week only.",
		`href="/meals/templates"`, ">Templates</a>",
		`>Lentil soup</textarea><p class="src">`, `From template <a href="/meals/templates/` + id + `">Week B</a>`, `>Pancakes</textarea>`,
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("meal plan lacks %q\n%s", want, r.body)
		}
	}
	// Typing the template's own dish is no adjustment; another dish is.
	week := url.Values{"week": {"2026-11-16"}, mealField("2026-11-16", "dinner"): {"Lentil soup"}, mealField("2026-11-17", "dinner"): {"Pizza at Grandma's"}, mealField("2026-11-18", "dinner"): {""}}
	if r := b.do(http.MethodPost, "/meals", week, en); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	r = b.do(http.MethodGet, "/meals?week=2026-11-16", nil, en)
	for _, want := range []string{
		`<div class="mrow adjusted" data-mt="dinner"><label class="mt" for="meal_2026-11-17_dinner">`, `Pizza at Grandma&#39;s</textarea>`, `<span class="adj">Adjusted this week</span>`,
		`<span class="was">Template: <b>Fish fingers</b></span>`, `name="restore" value="meal_2026-11-17_dinner" formnovalidate`, "Back to template",
		`placeholder="Nothing this week"`, `<span class="was">Template: <b>Spaghetti bolognese</b></span>`,
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("adjusted meal lacks %q\n%s", want, r.body)
		}
	}
	if strings.Count(r.body, `class="mrow adjusted"`) != 2 {
		t.Errorf("adjusted rows = %d, want 2", strings.Count(r.body, `class="mrow adjusted"`))
	}
	// Back to template saves what was typed, restores that meal and names it.
	restore := url.Values{"week": {"2026-11-16"}, "restore": {"meal_2026-11-17_dinner"}, mealField("2026-11-17", "dinner"): {"Pizza at Grandma's"}, mealField("2026-11-18", "dinner"): {""}, mealField("2026-11-19", "dinner"): {"Takeaway"}}
	r = b.do(http.MethodPost, "/meals", restore, en)
	if r.status != http.StatusSeeOther || r.location != "/meals?week=2026-11-16" {
		t.Fatalf("restore = %d %q\n%s", r.status, r.location, r.body)
	}
	page := b.do(http.MethodGet, r.location, nil, en)
	if got := html.UnescapeString(successLine(page.body)); got != "Tuesday's dinner follows the template “Week B” again. Week 47 saved." {
		t.Errorf("success line = %q", got)
	}
	if list := mealsBetween(t, c, "2026-11-17", "2026-11-19"); len(list) != 2 || list[0]["text"] != "Fish fingers" || list[0]["source"] != "template" || list[1]["text"] != "Takeaway" {
		t.Errorf("after the restore = %v", list)
	}
	b.do(http.MethodPost, "/meals", url.Values{"week": {"2026-11-16"}, mealField("2026-11-17", "dinner"): {"Pizza again"}}, de)
	r = b.do(http.MethodPost, "/meals", restore, de)
	if got := successLine(b.do(http.MethodGet, r.location, nil, de).body); got != "Das Abendessen am Dienstag kommt wieder aus der Vorlage „Week B“. Woche 47 gespeichert." {
		t.Errorf("German success line = %q", got)
	}
}

func TestChildrenSeeTheEffectiveMealWithoutTemplateMarks(t *testing.T) {
	c := signedInAPI(t)
	saveTemplate(t, c, weekBForm("Week B", "2026-11-16", "2"), en)
	r := childSession(t, c.b).do(http.MethodGet, "/meals?week=2026-11-16", nil, en)
	if r.status != http.StatusOK || !strings.Contains(r.body, "Lentil soup") {
		t.Fatalf("child page = %d\n%s", r.status, r.body)
	}
	for _, not := range []string{"tpl-sum", "From template", "Templates", "restore"} {
		if strings.Contains(r.body, not) {
			t.Errorf("the read view shows %q", not)
		}
	}
}

func TestTemplatePagesAreGermanInDuForm(t *testing.T) {
	c := signedInAPI(t)
	saveTemplate(t, c, weekBForm("Woche B", "2026-11-16", "2"), de)
	for path, wants := range map[string][]string{
		"/meals/templates":       {"<h1>Essensplan-Vorlagen</h1>", "Neue Vorlage", "Die nächsten Wochen", "<b>Abendessen</b> von Montag bis Mittwoch", "Alle 2 Wochen ab Woche 47 (16/11/2026)", "Zurück zum Essensplan"},
		"/meals/templates/new":   {"Welche Mahlzeiten füllt sie?", "Eine Mahlzeit am Tag", "Alle Mahlzeiten des Tages", "Welche Mahlzeit?", "Wann kommt sie dran?", "Startwoche", "Mahlzeiten der Woche", "Vorlage speichern", "Zurück zu den Vorlagen"},
		"/meals?week=2026-11-16": {"Vorlagen</a>", "Aus Vorlage"},
	} {
		body := c.b.do(http.MethodGet, path, nil, de).body
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q", path, want)
			}
		}
	}
	id := templateID(t, c, "Woche B")
	c.b.do(http.MethodPost, "/meals", url.Values{"week": {"2026-11-16"}, mealField("2026-11-17", "dinner"): {"Pizza"}}, de)
	body := c.b.do(http.MethodGet, "/meals?week=2026-11-16", nil, de).body
	for _, want := range []string{"Diese Woche angepasst", "Vorlage:", "Zurück zur Vorlage"} {
		if !strings.Contains(body, want) {
			t.Errorf("meal plan lacks %q", want)
		}
	}
	if r := c.b.do(http.MethodPost, "/meals/templates/"+id+"/delete", url.Values{}, de); !strings.Contains(successLine(r.body), "Vorlage „Woche B“ gelöscht.") {
		t.Errorf("delete line = %q", successLine(r.body))
	}
}

func TestTheMealPlanCalendarRollsForwardOncePerDayWhenAPageIsOpened(t *testing.T) {
	c := signedInAPI(t) // the clock stands at Saturday 14/11/2026
	form := weekBForm("Every day", "2026-11-16", "1")
	for wd := 4; wd <= 7; wd++ {
		form.Set(tplField(wd, "dinner"), "Soup")
	}
	if r := saveTemplate(t, c, form, en); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d", r.status)
	}
	h, _ := c.b.db.HomeHousehold()
	cal, err := c.b.db.MealCalendar(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	titleOn := func(day string) string {
		evs, err := c.b.db.CalendarEvents(h.ID, cal.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range evs {
			if e.Start.In(berlinTZ).Format("2006-01-02") == day {
				return e.Title
			}
		}
		return ""
	}
	const reached, next = "2027-11-19", "2027-11-20" // the last day in reach on 14/11/2026, and the one after
	if titleOn(reached) == "" || titleOn(next) != "" {
		t.Fatalf("after saving: %q, %q", titleOn(reached), titleOn(next))
	}

	c.b.now = c.b.now.AddDate(0, 0, 1) // Sunday 15/11/2026
	if r := c.b.do(http.MethodGet, "/meals", nil, en); r.status != http.StatusOK {
		t.Fatalf("meals = %d", r.status)
	}
	if got := titleOn(next); got != "Dinner: Soup" {
		t.Fatalf("the day that came into reach = %q, want its meal", got)
	}
	if got := titleOn("2026-11-14"); got != "" { // a day before today is not rewritten
		t.Errorf("a day before today = %q", got)
	}

	// Once a day: a second page on the same day does not write again.
	if _, err := c.b.db.SQL().Exec(`DELETE FROM events WHERE calendar_id = ? AND title = ?`, cal.ID, "Dinner: Soup"); err != nil {
		t.Fatal(err)
	}
	c.b.do(http.MethodGet, "/meals", nil, en)
	if got := titleOn(next); got != "" {
		t.Errorf("same day, second page wrote again: %q", got)
	}
	c.b.now = c.b.now.AddDate(0, 0, 1)
	c.b.do(http.MethodGet, "/meals", nil, en)
	if got := titleOn("2027-11-21"); got != "Dinner: Soup" {
		t.Errorf("next day = %q, want the roll to run again", got)
	}
}

func TestBackToTemplateOnAMealThatIsNotAdjustedAnyMoreStillSavesWhatWasTyped(t *testing.T) {
	c := signedInAPI(t)
	saveTemplate(t, c, weekBForm("Week B", "2026-11-16", "2"), en)
	// Tuesday follows the template (another tab restored it); the form still offers "Back to template".
	form := url.Values{"week": {"2026-11-16"}, "restore": {"meal_2026-11-17_dinner"}, mealField("2026-11-17", "dinner"): {"Lasagne"}}
	if r := c.b.do(http.MethodPost, "/meals", form, en); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	if list := mealsBetween(t, c, "2026-11-17", "2026-11-17"); len(list) != 1 || list[0]["text"] != "Lasagne" {
		t.Fatalf("what was typed = %v", list)
	}
}
