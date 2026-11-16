package server_test

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// Ticket 182 (ADR 0017, preview 182): every page below Settings starts with a
// breadcrumb trail instead of a back link; the side navigation stays with
// Settings filled; an everyday page two levels deep gets the same trail.

type crumbPart struct{ Text, Href string }

var (
	crumbNavRe  = regexp.MustCompile(`(?s)<nav class="crumbs" aria-label="([^"]*)"><ol>(.*?)</ol></nav>`)
	crumbItemRe = regexp.MustCompile(`(?s)<li>(.*?)</li>`)
	crumbHrefRe = regexp.MustCompile(`^<a href="([^"]*)"`)
	crumbSvgRe  = regexp.MustCompile(`(?s)<svg.*?</svg>`)
	crumbTagRe  = regexp.MustCompile(`<[^>]+>`)
	crumbH1Re   = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
)

// trailOf returns the aria-label and the parts of the trail, or ok=false.
func trailOf(body string) (label string, parts []crumbPart, ok bool) {
	m := crumbNavRe.FindStringSubmatch(body)
	if m == nil {
		return "", nil, false
	}
	for _, li := range crumbItemRe.FindAllStringSubmatch(m[2], -1) {
		inner := strings.TrimSpace(crumbSvgRe.ReplaceAllString(li[1], ""))
		p := crumbPart{Text: html.UnescapeString(strings.TrimSpace(crumbTagRe.ReplaceAllString(inner, "")))}
		if h := crumbHrefRe.FindStringSubmatch(inner); h != nil {
			p.Href = h[1]
		}
		parts = append(parts, p)
	}
	return m[1], parts, true
}

func headingText(body string) string {
	m := crumbH1Re.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.Join(strings.Fields(html.UnescapeString(crumbTagRe.ReplaceAllString(crumbSvgRe.ReplaceAllString(m[1], ""), ""))), " ")
}

func wantTrail(t *testing.T, page, body string, want ...crumbPart) {
	t.Helper()
	_, got, ok := trailOf(body)
	if !ok {
		t.Errorf("%s has no breadcrumb trail\n%s", page, body)
		return
	}
	if len(got) != len(want) {
		t.Errorf("%s trail = %+v, want %+v", page, got, want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s trail = %+v, want %+v", page, got, want)
			return
		}
	}
}

func part(text, href string) crumbPart { return crumbPart{text, href} }

// A sub-page of Settings: trail with the hub, the hub entry and the page itself.
func TestOverviewPagesBelowSettingsStartWithATrail(t *testing.T) {
	b := signedInBox(t)
	for _, c := range []struct{ path, entry string }{
		{"/calendars", "Calendar connections"}, {"/caldav", "Calendar apps"}, {"/displays", "Displays"}, {"/phones", "Phones"},
		{"/mcp", "AI assistants (MCP)"}, {"/people", "People"}, {"/settings/household", "Household"}, {"/account", "My account"}, {"/sessions", "Signed-in devices"},
	} {
		body := getEN(b, c.path).body
		wantTrail(t, c.path, body, part("Settings", "/settings"), part(c.entry, ""))
		if strings.Contains(body, `class="back"`) {
			t.Errorf("%s still has a back link next to its trail", c.path)
		}
		if h := headingText(body); h != c.entry {
			t.Errorf("%s h1 = %q, want the hub entry %q", c.path, h, c.entry)
		}
	}
}

func TestTheTrailWordsAndLabelFollowTheLanguage(t *testing.T) {
	b := signedInBox(t)
	body := b.do(http.MethodGet, "/displays", nil, de).body
	if label, _, _ := trailOf(body); label != "Seitenpfad" {
		t.Errorf("German label = %q", label)
	}
	wantTrail(t, "/displays (de)", body, part("Einstellungen", "/settings"), part("Displays", ""))
	if label, _, _ := trailOf(getEN(b, "/displays").body); label != "Breadcrumb" {
		t.Errorf("English label = %q", label)
	}
}

func TestTheTrailMarksItsLastPartAsTheCurrentPageAndHidesTheSeparators(t *testing.T) {
	b := signedInBox(t)
	body := getEN(b, "/people").body
	for _, want := range []string{`<span aria-current="page">People</span>`, `<a href="/settings">Settings</a>`, `class="icon sep"`, `aria-hidden="true"`} {
		if !strings.Contains(body, want) {
			t.Errorf("trail lacks %q", want)
		}
	}
}

func TestTheSettingsHubHasNoTrail(t *testing.T) {
	b := signedInBox(t)
	body := getEN(b, "/settings").body
	if strings.Contains(body, `class="crumbs"`) {
		t.Error("the hub has a trail")
	}
	if !strings.Contains(body, `<a href="/settings" aria-current="page">Settings</a>`) {
		t.Errorf("the hub entry of the navigation is not the current page\n%s", body)
	}
}

// The side navigation stays on every page below Settings, with Settings filled.
func TestTheNavigationKeepsSettingsFilledOnEveryPageBelowIt(t *testing.T) {
	b := signedInBox(t)
	id := pairedDisplay(t, b, "Hallway", "screen")
	for _, path := range append([]string{"/connections/new", "/displays/" + id, "/groups/new"}, hubPages...) {
		body := getEN(b, path).body
		if !strings.Contains(body, `<a href="/settings" aria-current="true">Settings</a>`) {
			t.Errorf("%s: Settings is not filled with aria-current=\"true\" in the navigation", path)
		}
		if strings.Contains(body, `<a href="/settings" aria-current="page">`) {
			t.Errorf("%s: Settings is marked as the page itself", path)
		}
		if got := navTargets(t, body); strings.Join(got, " ") != "/ /calendar /tasks /meals /lists /settings" {
			t.Errorf("%s: navigation = %v", path, got)
		}
	}
}

func TestADetailPageShowsTheTrailThroughItsOverviewAndNameOnly(t *testing.T) {
	b := signedInBox(t)
	id := pairedDisplay(t, b, "Hallway", "eink_color")
	r := getEN(b, "/displays/"+id)
	wantTrail(t, "display", r.body, part("Settings", "/settings"), part("Displays", "/displays"), part("Hallway", ""))
	if strings.Contains(r.body, `class="back"`) || strings.Contains(r.body, "All displays") {
		t.Error("the back link is still there")
	}
}

// Order inside main: trail, success line, heading.
func TestTrailComesBeforeTheSuccessLineAndTheHeading(t *testing.T) {
	b := signedInBox(t)
	id := pairedDisplay(t, b, "Hallway", "screen")
	b.post("/displays/"+id+"/settings", url.Values{"name": {"Hallway"}, "type": {"screen"}, "theme": {"auto"}})
	body := getEN(b, "/displays/"+id).body
	nav, ok, h1 := strings.Index(body, `class="crumbs"`), strings.Index(body, `data-success`), strings.Index(body, "<h1>")
	if ok < 0 {
		t.Fatalf("no success line after saving\n%s", body)
	}
	if !(nav >= 0 && nav < ok && ok < h1) {
		t.Errorf("order trail=%d success=%d h1=%d, want trail < success < h1", nav, ok, h1)
	}
}

func TestTheLastPartIsTheHeadingOfThePageOnEveryPageBelowSettings(t *testing.T) {
	b := signedInBox(t)
	dispID := pairedDisplay(t, b, "Hallway", "screen")
	connID := connectedBox(t, b)
	switchMCPOnPage(t, b, true)
	b.post("/caldav/tokens", url.Values{"name": {"Phone"}})
	h, _ := b.db.HomeHousehold()
	tokens, _ := b.db.APITokens(h.ID)
	people, _ := b.db.People(h.ID)
	pages := []string{"/connections/new", "/connections/" + connID, "/displays/" + dispID, "/groups/new", "/mcp/tokens/new", "/people/" + people[0].ID}
	for _, tok := range tokens {
		if tok.Name == "Phone" {
			pages = append(pages, "/caldav/tokens/"+tok.ID)
		}
	}
	pages = append(pages, hubPages...)
	for _, path := range pages {
		body := getEN(b, path).body
		_, parts, ok := trailOf(body)
		if !ok {
			t.Errorf("%s has no trail", path)
			continue
		}
		last := parts[len(parts)-1]
		if last.Href != "" || last.Text != headingText(body) {
			t.Errorf("%s: last part %+v, h1 %q", path, last, headingText(body))
		}
		for _, p := range parts[:len(parts)-1] {
			if p.Href == "" {
				t.Errorf("%s: part %q is not a link", path, p.Text)
			}
		}
	}
}

func TestACalendarConnectionTrailHangsOffTheCalendarConnectionsEntry(t *testing.T) {
	b := signedInBox(t)
	connID := connectedBox(t, b)
	wantTrail(t, "connection", getEN(b, "/connections/"+connID).body, part("Settings", "/settings"), part("Calendar connections", "/calendars"), part("u", ""))
	wantTrail(t, "connect", getEN(b, "/connections/new").body, part("Settings", "/settings"), part("Calendar connections", "/calendars"), part("Connect a calendar", ""))
}

// Q93: the page "Calendars" is called "Calendar connections".
func TestTheCalendarConnectionsPageHasItsHubEntryAsHeading(t *testing.T) {
	b := signedInBox(t)
	if h := headingText(getEN(b, "/calendars").body); h != "Calendar connections" {
		t.Errorf("English h1 = %q", h)
	}
	if h := headingText(b.do(http.MethodGet, "/calendars", nil, de).body); h != "Kalender-Verbindungen" {
		t.Errorf("German h1 = %q", h)
	}
}

func TestTheDeepestSettingsPageHasFourParts(t *testing.T) {
	e := newInviteBox(t)
	wantTrail(t, "invitation page", e.en(http.MethodGet, e.page(), nil).body,
		part("Settings", "/settings"), part("People", "/people"), part("Papa", "/people/"+e.papa.ID), part("Invite Papa", ""))
	e.create()
	wantTrail(t, "open invitation", e.en(http.MethodGet, e.page(), nil).body,
		part("Settings", "/settings"), part("People", "/people"), part("Papa", "/people/"+e.papa.ID), part("Invitation for Papa", ""))
	wantTrail(t, "person", e.en(http.MethodGet, "/people/"+e.papa.ID, nil).body,
		part("Settings", "/settings"), part("People", "/people"), part("Papa", ""))
}

func TestNamesInTheTrailAreEscaped(t *testing.T) {
	b := signedInBox(t)
	id := pairedDisplay(t, b, `<b>Hall</b> & "co"`, "screen")
	body := getEN(b, "/displays/"+id).body
	m := crumbNavRe.FindStringSubmatch(body)
	if m == nil || strings.Contains(m[2], "<b>Hall") || !strings.Contains(m[2], "&lt;b&gt;Hall") {
		t.Errorf("name not escaped in the trail: %v", m)
	}
}

// Q93: everyday pages one level deep keep their back link; two levels deep they show the trail.
func TestEverydayPagesKeepTheBackLinkOneLevelDeepAndShowTheTrailFromTwoLevels(t *testing.T) {
	c := signedInAPI(t)
	body := c.b.do(http.MethodGet, "/meals/templates", nil, en).body
	if !strings.Contains(body, `<a class="back" href="/meals">`) || strings.Contains(body, `class="crumbs"`) {
		t.Errorf("the templates overview must keep its back link and have no trail\n%s", body)
	}
	if r := saveTemplate(t, c, weekBForm("Weekend", "2026-11-16", "2"), en); r.status >= 400 {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	id := templateID(t, c, "Weekend")
	for _, path := range []string{"/meals/templates/" + id, "/meals/templates/new"} {
		page := c.b.do(http.MethodGet, path, nil, en).body
		name := headingText(page)
		wantTrail(t, path, page, part("Meals", "/meals"), part("Meal plan templates", "/meals/templates"), part(name, ""))
		if strings.Contains(page, `class="back"`) {
			t.Errorf("%s has a back link next to its trail", path)
		}
		if !strings.Contains(page, `<a href="/meals" aria-current="page">Meals</a>`) {
			t.Errorf("%s: Meals is not the current screen in the navigation", path)
		}
	}
	// German words
	wantTrail(t, "de", c.b.do(http.MethodGet, "/meals/templates/"+id, nil, de).body, part("Essen", "/meals"), part("Essensplan-Vorlagen", "/meals/templates"), part("Weekend", ""))
}

func TestRoutineTemplatePagesShowTheTrailFromTwoLevels(t *testing.T) {
	tb := newTaskBox(t)
	tpl := tb.saveTemplate(t, tb.miaEveningRoutine(t), "Evening routine of Mia")
	id := tpl["id"].(string)
	step := tpl["steps"].([]any)[0].(map[string]any)
	over := tb.page(t, "/routines/templates")
	has(t, over, `<a class="back" href="/tasks">`)
	lacks(t, over, `class="crumbs"`)
	tplPage := tb.page(t, "/routines/templates/"+id)
	wantTrail(t, "template", tplPage, part("Tasks", "/tasks"), part("Routine templates", "/routines/templates"), part("Evening routine of Mia", ""))
	stepPage := tb.page(t, "/routines/templates/"+id+"/steps/"+step["id"].(string))
	wantTrail(t, "step", stepPage, part("Tasks", "/tasks"), part("Routine templates", "/routines/templates"), part("Evening routine of Mia", "/routines/templates/"+id), part(step["title"].(string), ""))
	// One level below Tasks keeps the link.
	has(t, tb.page(t, "/routines/new"), `class="back"`)
}

// After a field error the trail and the heading keep the saved name until a save succeeds.
func TestAfterAFieldErrorTrailAndHeadingKeepTheSavedName(t *testing.T) {
	b := signedInBox(t)
	id := pairedDisplay(t, b, "Hallway", "screen")
	r := b.do(http.MethodPost, "/displays/"+id+"/settings", url.Values{"name": {"Corridor"}, "type": {"screen"}, "theme": {"auto"}, "quiet": {"on"}, "quiet_from": {"x"}, "quiet_to": {"y"}}, en)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422\n%s", r.status, r.body)
	}
	wantTrail(t, "refused save", r.body, part("Settings", "/settings"), part("Displays", "/displays"), part("Hallway", ""))
	if h := headingText(r.body); h != "Hallway" {
		t.Errorf("h1 = %q, want the saved name", h)
	}
	if !strings.Contains(r.body, `value="Corridor"`) {
		t.Error("what was typed is lost at the field")
	}
}
