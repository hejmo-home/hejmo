package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

// patternBox is a signed-in parent with the example family: Mama, Papa, Sebastian, Mia.
func patternBox(t *testing.T) (*box, map[string]string) {
	t.Helper()
	b := signedInBox(t)
	h, _ := b.db.HomeHousehold()
	ids := map[string]string{}
	for _, p := range []struct{ name, role string }{{"Papa", "parent"}, {"Sebastian", "child"}, {"Mia", "child"}} {
		got, err := b.db.AddPerson(h.ID, store.Person{Name: p.name, Role: store.Role(p.role)})
		if err != nil {
			t.Fatal(err)
		}
		ids[p.name] = got.ID
	}
	return b, ids
}

var personChipRe = regexp.MustCompile(`(?s)<label class="pchip[^"]*"[^>]*>.*?</label>`)

// chips returns the person chips of a page: label text mapped to whether the chip is selected.
func chips(page string) map[string]bool {
	out := map[string]bool{}
	tag := regexp.MustCompile(`(?s)<[^>]*>`)
	for _, c := range personChipRe.FindAllString(page, -1) {
		out[strings.TrimSpace(tag.ReplaceAllString(c, ""))] = regexp.MustCompile(`<input [^>]*\bchecked\b`).MatchString(c)
	}
	return out
}

// Ticket 5.16: with no person selected the audience is the whole family,
// also without JavaScript, so the "Whole family" chip is a plain checkbox that
// the server renders selected and that people can only add to.
func TestAudienceChipsDefaultToTheWholeFamilyWithoutJavaScript(t *testing.T) {
	b, ids := patternBox(t)
	b.feeds.set("https://example.org/a.ics", feedOf(), nil)
	got := chips(getEN(b, "/calendars").body)
	want := map[string]bool{"Whole family": true, "Mama": false, "Papa": false, "Sebastian": false, "Mia": false}
	for name, sel := range want {
		if v, ok := got[name]; !ok || v != sel {
			t.Errorf("chip %q selected = %v (present %v), want %v", name, v, ok, sel)
		}
	}
	page := getEN(b, "/calendars").body
	if !strings.Contains(page, `class="marker" data-person="2"`) || !strings.Contains(page, `data-shape="circle-empty"`) {
		t.Errorf("person chips lack the person marker:\n%s", page)
	}
	if strings.Contains(page, "Leave empty") {
		t.Error("the hint 'leave empty for the whole family' is still there")
	}

	// No person chosen: the calendar is for the whole family, also if the box
	// gets another person later.
	if r := b.post("/calendars", url.Values{"name": {"Holidays"}, "url": {"https://example.org/a.ics"}, "color": {"#B4583A"}, "interval": {"60"}, "whole_family": {"1"}}); r.status != http.StatusSeeOther {
		t.Fatalf("add = %d\n%s", r.status, r.body)
	}
	h, _ := b.db.HomeHousehold()
	cals, _ := b.db.SyncedCalendars(h.ID)
	if len(cals[0].Audience.People) != 0 || len(cals[0].Audience.Groups) != 0 {
		t.Fatalf("audience = %+v, want empty (whole family)", cals[0].Audience)
	}
	// A chosen person wins over the Whole family chip.
	b.post("/calendars", url.Values{"name": {"Swimming"}, "url": {"https://example.org/a.ics"}, "color": {"#B4583A"}, "interval": {"60"}, "whole_family": {"1"}, "people": {ids["Mia"]}})
	cals, _ = b.db.SyncedCalendars(h.ID)
	if len(cals) != 2 || len(cals[1].Audience.People) != 1 || cals[1].Audience.People[0] != ids["Mia"] {
		t.Fatalf("audience = %+v, want Mia only", cals)
	}
}

func TestChosenPeopleStayChosenAfterAnError(t *testing.T) {
	b, ids := patternBox(t)
	r := b.do(http.MethodPost, "/calendars", url.Values{"name": {""}, "url": {"https://example.org/a.ics"}, "color": {"#B4583A"}, "interval": {"60"}, "people": {ids["Mia"], ids["Sebastian"]}}, map[string]string{"Accept-Language": "en"})
	got := chips(r.body)
	if !got["Mia"] || !got["Sebastian"] || got["Whole family"] || got["Papa"] {
		t.Fatalf("chips after error = %v", got)
	}
}

func TestTaskPersonIsASingleChoiceChip(t *testing.T) {
	b, ids := patternBox(t)
	page := getEN(b, "/tasks").body
	if !strings.Contains(page, `type="radio" name="person"`) || strings.Contains(page, "<select name=\"person\"") {
		t.Fatalf("person is not a radio chip:\n%s", page)
	}
	if _, ok := chips(page)["Whole family"]; ok {
		t.Error("a task belongs to one person: no Whole family chip")
	}
	r := b.do(http.MethodPost, "/tasks", url.Values{"title": {"Read"}, "repeat": {"daily"}, "date": {"2026-11-14"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-person"`) {
		t.Fatalf("no person = %d, want the error at the person chips", r.status)
	}
	r = b.do(http.MethodPost, "/tasks", url.Values{"person": {ids["Mia"]}, "title": {" "}, "repeat": {"daily"}}, map[string]string{"Accept-Language": "en"})
	if !chips(r.body)["Mia"] {
		t.Errorf("Mia not kept after an error: %v", chips(r.body))
	}
}

func TestSettingsUseSegmentedControls(t *testing.T) {
	b := signedInBox(t)
	page := getEN(b, "/settings/household").body
	for _, want := range []string{`<fieldset class="seg">`, `type="radio" name="time_format" value="24h" checked`, `type="radio" name="temperature_unit" value="c" checked`} {
		if !strings.Contains(page, want) {
			t.Errorf("settings lack %q:\n%s", want, page)
		}
	}
}

func TestTaskFormShowsDependentFieldsOnlyWhenTheyApply(t *testing.T) {
	b := signedInBox(t)
	page := getEN(b, "/tasks").body
	for _, want := range []string{`<fieldset class="seg">`, `name="repeat" value="daily" checked`, `class="when when-once"`, `class="days when-weekly"`} {
		if !strings.Contains(page, want) {
			t.Errorf("task form lacks %q", want)
		}
	}
}

func TestDisplaySwitchAndChoiceCards(t *testing.T) {
	b := signedInBox(t)
	d := newScreen(b)
	b.post("/displays", pairForm(codeOn(t, d.get("/display", "en").body)))
	page := getEN(b, "/displays").body
	if !strings.Contains(page, `role="switch" name="show_tomorrow"`) || !strings.Contains(page, "The tomorrow block is hidden") || !strings.Contains(page, "The tomorrow block is shown") {
		t.Errorf("tomorrow block is not a switch with state text:\n%s", page)
	}
	if !strings.Contains(page, `<fieldset class="choice">`) || !strings.Contains(page, `type="radio" name="type" value="eink_bw"`) || !strings.Contains(page, "<small>") {
		t.Errorf("display type is not a choice card:\n%s", page)
	}
}

func TestOptionsWithGoodDefaultsSitUnderMoreOptions(t *testing.T) {
	b := signedInBox(t)
	page := getEN(b, "/calendars").body
	i := strings.Index(page, `<details class="more"`)
	if i < 0 {
		t.Fatalf("no More options on the add form:\n%s", page)
	}
	rest := page[i:]
	end := strings.Index(rest, "</details>")
	for _, name := range []string{"color", "interval"} {
		if !strings.Contains(rest[:end], `name="`+name+`"`) {
			t.Errorf("%s is not under More options", name)
		}
	}
	// An error at a hidden field opens the disclosure.
	r := b.do(http.MethodPost, "/calendars", url.Values{"name": {"x"}, "url": {"https://example.org/a.ics"}, "color": {"nope"}, "interval": {"60"}}, map[string]string{"Accept-Language": "en"})
	if !strings.Contains(r.body, `<details class="more" open`) {
		t.Error("More options stays closed although the error is inside")
	}
}

func TestEmptyStatesExplainAndOfferTheFirstAction(t *testing.T) {
	b := signedInBox(t)
	for _, path := range []string{"/tasks", "/displays", "/caldav", "/calendars"} {
		page := getEN(b, path).body
		if !regexp.MustCompile(`(?s)<div class="empty" data-empty>\s*<p>[^<]+</p>\s*<a class="button" href="#[a-z]+">[^<]+</a>`).MatchString(page) {
			t.Errorf("%s has no empty state with a first action:\n%s", path, page)
		}
	}
}

// ADR 0017: destructive actions live on the detail page, behind a confirmation.
func TestDeletingATaskHappensOnItsDetailPageAfterConfirming(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	id := tb.newDaily(t, tb.miaID)
	list := getEN(b, "/tasks").body
	if strings.Contains(list, "/tasks/"+id+"/delete") {
		t.Error("the list still carries a delete button")
	}
	if !strings.Contains(list, `href="/tasks/`+id+`"`) {
		t.Fatal("task row does not open its detail page")
	}
	page := getEN(b, "/tasks/"+id)
	if page.status != http.StatusOK || !strings.Contains(page.body, "Brush teeth") {
		t.Fatalf("detail = %d\n%s", page.status, page.body)
	}
	for _, want := range []string{`<details class="danger">`, `action="/tasks/` + id + `/delete"`, "Yes, delete"} {
		if !strings.Contains(page.body, want) {
			t.Errorf("detail lacks %q", want)
		}
	}
	if strings.Index(page.body, `<details class="danger">`) > strings.Index(page.body, `action="/tasks/`+id+`/delete"`) {
		t.Error("the delete button sits outside the closed danger disclosure")
	}
	if r := getEN(b, "/tasks/00000000-0000-4000-8000-000000000000"); r.status != http.StatusNotFound {
		t.Errorf("unknown task = %d", r.status)
	}
	child := childSession(t, b)
	if r := child.get("/tasks/" + id); r.status != http.StatusForbidden {
		t.Errorf("child opens the detail page = %d", r.status)
	}
}

// ADR 0017: a destructive button never sits loose on a list. Revoking a token,
// removing a calendar and unpairing a display each sit behind a confirmation.
func TestDestructiveButtonsOnListsSitBehindAConfirmation(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	b.feeds.set("https://example.org/a.ics", feedOf(), nil)
	b.post("/calendars", url.Values{"name": {"Holidays"}, "url": {"https://example.org/a.ics"}, "color": {"#B4583A"}, "interval": {"60"}})
	b.post("/displays", pairForm(codeOn(t, newScreen(b).get("/display", "en").body)))
	b.post("/caldav/tokens", url.Values{"name": {"Phone"}})
	connID := connectedBox(t, b)
	for _, c := range []struct{ page, action string }{
		{"/calendars", "/connections/" + connID + "/delete"}, {"/caldav", "/revoke"}, {"/calendars", "/delete"}, {"/displays", "/unpair"},
	} {
		body := getEN(b, c.page).body
		at := strings.Index(body, c.action+`"`)
		open := strings.LastIndex(body[:max(at, 0)], `<details class="danger">`)
		closed := strings.LastIndex(body[:max(at, 0)], `</details>`)
		if at < 0 || open < 0 || closed > open {
			t.Errorf("%s: the %s button is not inside a danger disclosure", c.page, c.action)
		}
	}
}
