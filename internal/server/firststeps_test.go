package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/store"
)

func homeDE(b *box) string { return deGet(b, "/").body }

var stepTitles = []string{"Familie eintragen", "Zweites Elternteil einladen", "Kalender verbinden", "Aufgaben oder Routine", "Einkaufsliste und Essensplan", "Erstes Display aufhängen"}

// card is the first steps section of a page, "" without one.
func card(page string) string {
	i := strings.Index(page, `<section class="firststeps`)
	if i < 0 {
		return ""
	}
	j := strings.Index(page[i:], "</section>")
	return page[i : i+j]
}

// stepOf is the n-th (1..6) step of the card: from its heading to the next one.
func stepOf(t *testing.T, c string, n int) string {
	t.Helper()
	parts := strings.Split(c, `<p class="fs-head">`)
	if n >= len(parts) {
		t.Fatalf("only %d steps in\n%s", len(parts)-1, c)
	}
	return parts[n]
}

func setupHome(t *testing.T) *box {
	t.Helper()
	b := newBox(t)
	b.post("/setup", validSetupForm())
	secret := secretOn(t, b.get("/setup/totp").body)
	b.post("/setup/totp", url.Values{"code": {totpNow(b, secret)}})
	return b
}

// Ticket 133 design pass 2: after the wizard Home shows the success line with
// the parent's name and the first steps card with six steps in the designed
// order, each with its state in words, the progress in words and the footer.
func TestHomeAfterSetupShowsWelcomeAndTheSixFirstSteps(t *testing.T) {
	b := setupHome(t)
	page := homeDE(b)
	if m := successRe.FindStringSubmatch(page); m == nil || !strings.Contains(m[1], "Hejmo ist eingerichtet. Willkommen, Mama!") {
		t.Fatalf("no named welcome line: %v", m)
	}
	c := card(page)
	for _, want := range []string{
		`data-firststeps`, "<h2>Erste Schritte</h2>", "Die Reihenfolge hilft, jeder Schritt lässt sich überspringen.",
		`<div class="fs-progress"><span class="fs-bar" aria-hidden="true">` + strings.Repeat(`<span data-state="open"></span>`, 6) + `</span><span class="fs-count">0 von 6 erledigt</span></div>`,
		`<div class="fs-foot"><form method="post" action="/first-steps/hide"><button class="link quiet" type="submit">Erste Schritte ausblenden</button></form><p class="hint">Du findest sie danach unter Einstellungen.</p></div>`,
	} {
		if !strings.Contains(c, want) {
			t.Errorf("first steps misses %q\n%s", want, c)
		}
	}
	last := -1
	for i, title := range stepTitles {
		at := strings.Index(c, "<b>"+title+"</b>")
		if at < 0 || at < last {
			t.Errorf("step %d %q is missing or out of order", i+1, title)
		}
		last = at
		s := stepOf(t, c, i+1)
		if !strings.Contains(s, `<span class="fs-state" data-state="open">Noch offen</span>`) {
			t.Errorf("step %d does not say Noch offen in words: %s", i+1, s)
		}
		if !strings.Contains(s, `<form method="post" action="/first-steps/skip"><input type="hidden" name="step" value="`+store.FirstStepIDs[i]+`"><button class="link quiet" type="submit">Überspringen</button></form>`) {
			t.Errorf("step %d has no skip button with its id", i+1)
		}
	}
	// Only the next open step has the main button.
	if n := strings.Count(c, `class="button"`); n != 1 || !strings.Contains(stepOf(t, c, 1), `<a class="button" href="/people">Familie eintragen</a>`) {
		t.Errorf("%d main buttons, or not on the first step:\n%s", n, c)
	}
	if strings.Contains(page, "Familienansicht kommt") || strings.Contains(page, "arrives with the display") {
		t.Error("the stale placeholder is still on Home")
	}
	if m := successRe.FindStringSubmatch(homeDE(b)); m != nil {
		t.Errorf("the welcome line comes back: %v", m)
	}
	en := b.do(http.MethodGet, "/", nil, map[string]string{"Accept-Language": "en"}).body
	if c := card(en); !strings.Contains(c, "0 of 6 done") || !strings.Contains(c, "Hide first steps") || !strings.Contains(c, "Skip") || !strings.Contains(c, "Not done yet") {
		t.Errorf("the card is not English:\n%s", c)
	}
	// The old numbered "done" markers of the first design are gone.
	if strings.Contains(c, "done-who") {
		t.Error("done-who is still used")
	}
}

// Steps become done as the box fills. A done step says what exists; the main
// button moves on. The card stays until a parent closes it.
func TestFirstStepsDoneContentAndTheMovingMainButton(t *testing.T) {
	p := newPeopleBox(t) // a signed-in parent with one paired display "Hallway"
	c := card(homeDE(p.box))
	if !strings.Contains(c, "1 von 6 erledigt") || !strings.Contains(stepOf(t, c, 6), `data-state="done">Erledigt</span>`) || !strings.Contains(stepOf(t, c, 6), "Display „Hallway“ ist verbunden.") {
		t.Fatalf("the display step is not done:\n%s", c)
	}
	if !strings.Contains(c, `<li data-done><div class="s"><p class="fs-head"><b>Erstes Display aufhängen</b>`) {
		t.Error("the done step has no data-done")
	}
	names := []string{"Papa", "Sebastian", "Mia", "Konstantin", "Charlotte-Sophie", "Maximilian", "Friederike"}
	p.add(t, names[0], "parent")
	for _, n := range names[1:] {
		p.add(t, n, "child")
	}
	c = card(homeDE(p.box))
	family := stepOf(t, c, 1)
	if !strings.Contains(family, "8 Personen:") || !strings.Contains(family, `<ul class="who-chips">`) || !strings.Contains(family, `<a class="link" href="/people">Familie ändern</a>`) {
		t.Fatalf("family step:\n%s", family)
	}
	chips := family[strings.Index(family, `<ul class="who-chips">`):]
	chips = chips[:strings.Index(chips, "</ul>")]
	for _, n := range append([]string{"Mama"}, names...) {
		if !strings.Contains(chips, "</span>"+n+"</li>") {
			t.Errorf("no chip for %s", n)
		}
	}
	if n := strings.Count(chips, `class="marker"`); n != 8 || strings.Contains(chips, "+") {
		t.Errorf("%d markers, or an abbreviation", n)
	}
	// Second parent entered, no phone yet: a partly done step lists its parts.
	parents := stepOf(t, c, 2)
	if !strings.Contains(parents, `<li data-state="done">`) || !strings.Contains(parents, "Papa") || !strings.Contains(parents, `<li data-state="open"><span class="dot" aria-hidden="true"></span><span>Noch kein Handy verbunden.<span class="vh"> (noch offen)</span>`) || !strings.Contains(parents, `<span class="vh"> (erledigt)</span>`) {
		t.Errorf("parents step:\n%s", parents)
	}
	if !strings.Contains(parents, `data-state="open">Noch offen`) {
		t.Error("a half done step must not say Erledigt")
	}
	if !strings.Contains(stepOf(t, c, 2), `<a class="button" href="/phones">Handy verbinden</a>`) {
		t.Errorf("the main button did not move to the phone step:\n%s", parents)
	}
	if strings.Count(c, `class="button"`) != 1 {
		t.Error("more than one main button")
	}
	// A phone for Papa completes the step.
	papa := personNamed(t, p.box, "Papa")
	pairedPhone(t, p.box, papa, "Android")
	parents = stepOf(t, card(homeDE(p.box)), 2)
	if !strings.Contains(parents, `data-state="done">Erledigt</span>`) || !strings.Contains(parents, "Papa (Android)") {
		t.Errorf("parents step is not done with a phone:\n%s", parents)
	}
	// Calendars: one for everyone, one for Papa and Mia.
	p.feeds.set("https://example.org/a.ics", feedOf(), nil)
	p.post("/calendars", url.Values{"name": {"School"}, "url": {"https://example.org/a.ics"}, "color": {"#2F5E7A"}, "interval": {"60"}})
	h, _ := p.db.HomeHousehold()
	if _, err := p.db.CreateCalendar(store.Calendar{HouseholdID: h.ID, Name: "Work", Kind: "ics", Color: "#B4583A", Audience: calendar.Audience{People: []string{papa.ID, personNamed(t, p.box, "Mia").ID}}}); err != nil {
		t.Fatal(err)
	}
	cals := stepOf(t, card(homeDE(p.box)), 3)
	for _, want := range []string{`data-state="done">Erledigt</span>`, `<ul class="fs-rows">`, `<li><b>School</b> <span class="src">`, `<span class="for">für die ganze Familie</span>`, `<li><b>Work</b> <span class="src">`, `<span class="for">für Papa, Mia</span>`, `<a class="link" href="/calendars">Kalender ändern</a>`} {
		if !strings.Contains(cals, want) {
			t.Errorf("calendars step misses %q\n%s", want, cals)
		}
	}
	// Tasks and the meal plan.
	p.post("/tasks", url.Values{"person": {personNamed(t, p.box, "Mia").ID}, "title": {"Brush teeth"}, "repeat": {"daily"}, "stars": {"1"}})
	tasks := stepOf(t, card(homeDE(p.box)), 4)
	if !strings.Contains(tasks, `data-state="done">Erledigt</span>`) || !strings.Contains(tasks, "1 Aufgabe für Mia.") || !strings.Contains(tasks, `<a class="link" href="/tasks">Aufgaben ansehen</a>`) {
		t.Errorf("tasks step:\n%s", tasks)
	}
	meals := stepOf(t, card(homeDE(p.box)), 5)
	// Scope note on #133: #32 and #131 are on main, so step 5 links the meal plan,
	// the shopping list and the meal plan templates.
	if strings.Contains(meals, "Erledigt") || !strings.Contains(meals, `<a class="button" href="/meals">Essensplan öffnen</a>`) {
		t.Errorf("meals step before a plan exists:\n%s", meals)
	}
	for _, want := range []string{`<a class="link" href="/lists">Einkaufsliste öffnen</a>`, `<a class="link" href="/meals/templates">Essensplan-Vorlagen</a>`} {
		if !strings.Contains(meals, want) {
			t.Errorf("meals step misses %q\n%s", want, meals)
		}
	}
	if strings.Contains(meals, "späteren Version") {
		t.Errorf("meals step still says the shopping list comes later:\n%s", meals)
	}
	p.post("/meals", url.Values{"week": {"2026-11-09"}, mealField("2026-11-14", "dinner"): {"Brotzeit"}})
	meals = stepOf(t, card(homeDE(p.box)), 5)
	if !strings.Contains(meals, `data-state="done">Erledigt</span>`) || !strings.Contains(meals, `href="/lists"`) || !strings.Contains(meals, `href="/meals/templates"`) {
		t.Errorf("meals step is not done with a plan or lost its links:\n%s", meals)
	}
	c = card(homeDE(p.box))
	if !strings.Contains(c, "6 von 6 erledigt") {
		t.Errorf("not everything done:\n%s", c)
	}
}

// Every open step can be skipped; a skipped step has a dashed ring, says so in
// words and offers "Doch machen". The count says how many were skipped.
func TestFirstStepsCanBeSkippedAndTakenBack(t *testing.T) {
	b := signedInBox(t)
	if r := b.post("/first-steps/skip", url.Values{"step": {"tasks"}}); r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("skip = %d %q", r.status, r.location)
	}
	c := card(homeDE(b))
	tasks := stepOf(t, c, 4)
	if !strings.Contains(c, `<li data-skipped><div class="s"><p class="fs-head"><b>Aufgaben oder Routine</b>`) {
		t.Error("the skipped step has no data-skipped")
	}
	for _, want := range []string{`<span class="fs-state" data-state="skipped">Übersprungen</span>`, `<form method="post" action="/first-steps/undo"><input type="hidden" name="step" value="tasks"><button class="link quiet" type="submit">Doch machen</button></form>`} {
		if !strings.Contains(tasks, want) {
			t.Errorf("skipped step misses %q\n%s", want, tasks)
		}
	}
	if strings.Contains(tasks, "/first-steps/skip") {
		t.Error("a skipped step offers to skip again")
	}
	if !strings.Contains(c, `<span data-state="skipped"></span>`) || !strings.Contains(c, "0 von 6 erledigt, 1 übersprungen") {
		t.Errorf("progress does not count the skipped step:\n%s", c)
	}
	if strings.Count(c, `class="button"`) != 1 || !strings.Contains(stepOf(t, c, 1), `class="button"`) {
		t.Error("the main button is not on the first open step")
	}
	// Skipping the first step moves the main button to the next open one.
	b.post("/first-steps/skip", url.Values{"step": {"family"}})
	c = card(homeDE(b))
	if !strings.Contains(c, "0 von 6 erledigt, 2 übersprungen") || !strings.Contains(stepOf(t, c, 2), `class="button"`) || strings.Contains(stepOf(t, c, 1), `class="button"`) {
		t.Errorf("main button did not move on:\n%s", c)
	}
	if r := b.post("/first-steps/undo", url.Values{"step": {"tasks"}}); r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("undo = %d %q", r.status, r.location)
	}
	if c := card(homeDE(b)); !strings.Contains(c, "0 von 6 erledigt, 1 übersprungen") || strings.Contains(c, `<li data-skipped><div class="s"><p class="fs-head"><b>Aufgaben`) {
		t.Errorf("undo did not work:\n%s", c)
	}
	h, _ := b.db.HomeHousehold()
	if strings.Join(h.FirstStepsSkipped, ",") != "family" {
		t.Errorf("stored skipped = %v", h.FirstStepsSkipped)
	}
	if r := b.post("/first-steps/skip", url.Values{"step": {"bogus"}}); r.status != http.StatusUnprocessableEntity {
		t.Errorf("an unknown step = %d, want 422", r.status)
	}
	if r := b.post("/first-steps/skip", url.Values{}); r.status != http.StatusUnprocessableEntity {
		t.Errorf("no step = %d, want 422", r.status)
	}
}

// When every step is done or skipped the card says so with the success line and
// one main action, "Erste Schritte schließen".
func TestFirstStepsAllSettledShowASuccessLineAndClose(t *testing.T) {
	b := signedInBox(t)
	for _, id := range store.FirstStepIDs[:5] {
		b.post("/first-steps/skip", url.Values{"step": {id}})
	}
	if c := card(homeDE(b)); strings.Contains(c, "Alles erledigt") || strings.Contains(c, "firststeps done") {
		t.Fatalf("settled with one step still open:\n%s", c)
	}
	b.post("/first-steps/skip", url.Values{"step": {"display"}})
	c := card(homeDE(b))
	for _, want := range []string{
		`<section class="firststeps done" data-firststeps>`, `<p class="msg ok" role="status">`, "Alles erledigt. Hejmo kennt jetzt eure Familie.", "0 von 6 erledigt, 6 übersprungen",
		`<div class="fs-foot"><form method="post" action="/first-steps/hide"><button class="button" type="submit">Erste Schritte schließen</button></form><p class="hint">Danach zeigt die Startseite euren Tag. Die Schritte findest du unter Einstellungen.</p></div>`,
	} {
		if !strings.Contains(c, want) {
			t.Errorf("settled card misses %q\n%s", want, c)
		}
	}
	if n := strings.Count(c, `class="button"`); n != 1 {
		t.Errorf("%d main buttons on the settled card, want only Close", n)
	}
	if strings.Contains(c, "Erste Schritte ausblenden") {
		t.Error("the settled card also offers to hide")
	}
}

// Hiding is stored per household (parents only). Home then says where the
// steps are; Settings offers to show them again, and the API has parity.
func TestFirstStepsCanBeHiddenAndShownAgain(t *testing.T) {
	b := signedInBox(t)
	if card(homeDE(b)) == "" {
		t.Fatal("no card to hide")
	}
	if strings.Contains(deGet(b, "/settings").body, "/first-steps/show") {
		t.Error("Settings offers to show steps that are shown")
	}
	if r := b.post("/first-steps/hide", url.Values{}); r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("hide = %d %q", r.status, r.location)
	}
	page := homeDE(b)
	if card(page) != "" {
		t.Error("the card is still there")
	}
	if m := successRe.FindStringSubmatch(page); m == nil || !strings.Contains(m[1], "Erste Schritte ausgeblendet. Du findest sie unter Einstellungen.") {
		t.Errorf("no line saying where the steps went: %v", m)
	}
	if h, _ := b.db.HomeHousehold(); !h.FirstStepsHidden {
		t.Error("hiding is not stored")
	}
	hub := deGet(b, "/settings").body
	if !strings.Contains(hub, `<form method="post" action="/first-steps/show">`) || !strings.Contains(hub, "Erste Schritte anzeigen") {
		t.Errorf("Settings has no entry to show the first steps again:\n%s", hub)
	}
	if r := b.post("/first-steps/show", url.Values{}); r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("show = %d %q", r.status, r.location)
	}
	if card(homeDE(b)) == "" {
		t.Error("the card does not come back")
	}
	if h, _ := b.db.HomeHousehold(); h.FirstStepsHidden {
		t.Error("showing is not stored")
	}
	if strings.Contains(deGet(b, "/settings").body, "/first-steps/show") {
		t.Error("Settings still offers to show them")
	}
}

func TestFirstStepsAreNotForChildrenAndNeedAParent(t *testing.T) {
	p := newPeopleBox(t)
	child := p.add(t, "Mia", "child")
	h, _ := p.db.HomeHousehold()
	var mia store.Person
	people, _ := p.db.People(h.ID)
	for _, x := range people {
		if x.ID == child["id"] {
			mia = x
		}
	}
	phone := pairedPhone(t, p.box, mia, "Mia's phone")
	if r := phone.get("/", "de"); strings.Contains(r.body, "data-firststeps") {
		t.Error("a child sees the first steps")
	}
	for _, path := range []string{"/first-steps/hide", "/first-steps/show", "/first-steps/skip", "/first-steps/undo"} {
		if r := phone.post(path, url.Values{"step": {"family"}}); r.status != http.StatusForbidden {
			t.Errorf("a child's phone posts %s = %d, want 403", path, r.status)
		}
		if r := newFreshBox(t).post(path, url.Values{"step": {"family"}}); r.status == http.StatusSeeOther && r.location == "/" {
			t.Errorf("anyone can post %s", path)
		}
	}
}

// API parity (ADR 0015): the same through GET and PATCH /settings, contract-checked.
func TestFirstStepsStateThroughTheAPI(t *testing.T) {
	c := signedInAPI(t)
	if _, body, _ := c.call(http.MethodGet, "/settings", nil); body["first_steps_hidden"] != false || len(body["first_steps_skipped"].([]any)) != 0 {
		t.Fatalf("settings = %v", body)
	}
	status, body, _ := c.call(http.MethodPatch, "/settings", map[string]any{"first_steps_hidden": true, "first_steps_skipped": []string{"display", "family"}})
	if status != http.StatusOK || body["first_steps_hidden"] != true || body["time_format"] != "24h" || strings.Join(strs(body["first_steps_skipped"]), ",") != "family,display" {
		t.Fatalf("patch = %d %v", status, body)
	}
	if card(homeDE(c.b)) != "" {
		t.Error("the card is still shown after hiding through the API")
	}
	if status, body, _ := c.call(http.MethodPatch, "/settings", map[string]any{"first_steps_hidden": false, "time_format": "12h"}); status != http.StatusOK || body["first_steps_hidden"] != false || body["time_format"] != "12h" || len(strs(body["first_steps_skipped"])) != 2 {
		t.Fatalf("show again = %d %v", status, body)
	}
	if !strings.Contains(card(homeDE(c.b)), "0 von 6 erledigt, 2 übersprungen") {
		t.Error("the card does not come back with the skipped steps")
	}
	if status, body, _ := c.call(http.MethodPatch, "/settings", map[string]any{"first_steps_skipped": []string{}}); status != http.StatusOK || len(strs(body["first_steps_skipped"])) != 0 {
		t.Errorf("clearing the skipped steps = %d %v", status, body)
	}
}

func strs(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// The API refuses an unknown first step with 422 (openapi.yaml) and says which
// field is wrong; other invalid values keep their own messages.
func TestSettingsAPIRefusesAnUnknownFirstStepAndSaysWhy(t *testing.T) {
	c := signedInAPI(t)
	status, body, _ := c.call(http.MethodPatch, "/settings", map[string]any{"first_steps_skipped": []string{"nonsense"}})
	if msg, _ := body["error"].(string); status != http.StatusUnprocessableEntity || !strings.HasPrefix(msg, "first_steps_skipped:") || !strings.Contains(msg, `"nonsense"`) || !strings.Contains(msg, "family, parents, calendars, tasks, meals, display") {
		t.Fatalf("unknown step = %d %v", status, body)
	}
	status, body, _ = c.call(http.MethodPatch, "/settings", map[string]any{"time_format": "25h"})
	if msg, _ := body["error"].(string); status != http.StatusUnprocessableEntity || strings.Contains(msg, "first_steps_skipped") {
		t.Fatalf("bad time format = %d %v", status, body)
	}
	if _, got, _ := c.call(http.MethodGet, "/settings", nil); len(strs(got["first_steps_skipped"])) != 0 {
		t.Errorf("a refused save stored steps: %v", got)
	}
}

// The second parent is a parent other than the one looking at Home, whatever
// the order of the people is.
func TestFirstStepsParentsStepNamesTheOtherParentNotTheViewer(t *testing.T) {
	p := newPeopleBox(t)
	p.add(t, "Papa", "parent")
	papa, mama := personNamed(t, p.box, "Papa").ID, personNamed(t, p.box, "Mama").ID
	if status, _, _ := p.api.call(http.MethodPut, "/people/order", map[string]any{"ids": []string{papa, mama}}); status != http.StatusOK {
		t.Fatalf("order = %d", status)
	}
	parents := stepOf(t, card(homeDE(p.box)), 2) // signed in as Mama
	if !strings.Contains(parents, "Papa") || strings.Contains(parents, "Zweites Elternteil: Mama") {
		t.Errorf("the step names the viewer:\n%s", parents)
	}
}
