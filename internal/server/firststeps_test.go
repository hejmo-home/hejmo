package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

func homeDE(b *box) string { return de(b, "/").body }

// Ticket 133: after the wizard Home shows the success line with the parent's
// name and the first steps card; the stale placeholder is gone.
func TestHomeAfterSetupShowsWelcomeAndFirstSteps(t *testing.T) {
	b := newBox(t)
	b.post("/setup", validSetupForm())
	secret := secretOn(t, b.get("/setup/totp").body)
	b.post("/setup/totp", url.Values{"code": {totpNow(b, secret)}})
	page := homeDE(b)
	if m := successRe.FindStringSubmatch(page); m == nil || m[1] == "" || !strings.Contains(m[1], "Hejmo ist eingerichtet. Willkommen, Mama!") {
		t.Fatalf("no named welcome line: %v\n%s", m, page)
	}
	for _, want := range []string{
		`data-firststeps`, "<h2>Erste Schritte</h2>", "Drei Dinge, dann zeigt Hejmo euren Tag.", "Familie eintragen", "Kalender verbinden", "Display oder Handy verbinden",
		`class="button" href="/people">Personen eintragen`, `class="link" href="/calendars">Kalender verbinden`, `class="link" href="/displays">Display verbinden`, `class="link" href="/phones">Handy verbinden`,
		"Erste Schritte ausblenden", "Auf dem Handy hast du Hejmo immer dabei.",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("first steps misses %q", want)
		}
	}
	if n := strings.Count(page, `class="button" href=`); n != 1 {
		t.Errorf("%d main buttons in the card, want one", n)
	}
	if strings.Contains(page, "Familienansicht kommt") || strings.Contains(page, "arrives with the display") {
		t.Error("the stale placeholder is still on Home")
	}
	// The success line is shown once.
	if m := successRe.FindStringSubmatch(homeDE(b)); m != nil {
		t.Errorf("the welcome line comes back: %v", m)
	}
	en := b.do(http.MethodGet, "/", nil, map[string]string{"Accept-Language": "en"}).body
	if !strings.Contains(en, "Three things, then Hejmo shows your day.") {
		t.Error("the card is not English")
	}
}

// The steps become done as the box fills: family, calendar, display or phone.
// A done step lists everyone with marker and name (no "+4"), and the main
// button moves to the next open step; the card goes when all are done.
func TestFirstStepsDoneStatesAndMainButton(t *testing.T) {
	p := newPeopleBox(t) // a signed-in parent with one paired display
	// The display counts: one open step less, and the lead says so.
	page := de(p.box, "/").body
	if !strings.Contains(page, "Noch zwei Dinge, dann zeigt Hejmo euren Tag.") || !strings.Contains(page, `class="button" href="/people">Personen eintragen`) {
		t.Fatalf("with a display paired:\n%s", page)
	}
	if n := strings.Count(page, "<li data-done>"); n != 1 || !strings.Contains(page, "Ein Display oder Handy ist verbunden.") {
		t.Errorf("the display step is not done (%d done)", n)
	}
	// One more person: the family is entered.
	names := []string{"Papa", "Sebastian", "Mia", "Konstantin", "Charlotte-Sophie", "Maximilian", "Friederike"}
	p.add(t, names[0], "parent")
	page = de(p.box, "/").body
	if !strings.Contains(page, `<p class="done-who">`) || !strings.Contains(page, "Personen ändern") || !strings.Contains(page, `class="button" href="/calendars">Kalender verbinden`) {
		t.Fatalf("with two people:\n%s", page)
	}
	if strings.Contains(page, `class="button" href="/people"`) {
		t.Error("the main button stays on the done step")
	}
	for _, n := range names[1:] {
		p.add(t, n, "child")
	}
	page = de(p.box, "/").body
	who := page[strings.Index(page, `<p class="done-who">`):]
	who = who[:strings.Index(who, "</p>")]
	for _, n := range append([]string{"Mama"}, names...) {
		if !strings.Contains(who, n) {
			t.Errorf("done step does not list %s", n)
		}
	}
	if n := strings.Count(who, `class="marker"`); n != 8 {
		t.Errorf("%d markers for 8 people", n)
	}
	if strings.Contains(who, "+") {
		t.Error("the done step abbreviates people")
	}
	// A calendar: the last open step is the one with... none left open except
	// nothing: calendar and display are done, so the card is gone.
	p.feeds.set("https://example.org/a.ics", feedOf(), nil)
	p.post("/calendars", url.Values{"name": {"School"}, "url": {"https://example.org/a.ics"}, "color": {"#2F5E7A"}, "interval": {"60"}})
	page = de(p.box, "/").body
	if strings.Contains(page, "data-firststeps") {
		t.Errorf("the card stays although every step is done:\n%s", page)
	}
}

// "Hide first steps" is stored per household (parents only) and also
// reachable through the settings API.
func TestFirstStepsCanBeHiddenForTheHousehold(t *testing.T) {
	b := signedInBox(t)
	if !strings.Contains(homeDE(b), "data-firststeps") {
		t.Fatal("no card to hide")
	}
	if r := b.post("/first-steps/hide", url.Values{}); r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("hide = %d %q", r.status, r.location)
	}
	if strings.Contains(homeDE(b), "data-firststeps") {
		t.Error("the card is still there")
	}
	h, _ := b.db.HomeHousehold()
	if !h.FirstStepsHidden {
		t.Error("hiding is not stored")
	}
}

func TestFirstStepsAreNotForChildrenAndHideNeedsAParent(t *testing.T) {
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
	if r := phone.post("/first-steps/hide", url.Values{}); r.status != http.StatusForbidden {
		t.Errorf("a child's phone hides the card = %d, want 403", r.status)
	}
	if r := newFreshBox(t).post("/first-steps/hide", url.Values{}); r.status == http.StatusSeeOther && r.location == "/" {
		t.Error("anyone can hide the card")
	}
}

// API parity (ADR 0015): the same through GET and PATCH /settings, contract-checked.
func TestFirstStepsHiddenThroughTheAPI(t *testing.T) {
	c := signedInAPI(t)
	if _, body, _ := c.call(http.MethodGet, "/settings", nil); body["first_steps_hidden"] != false {
		t.Fatalf("settings = %v", body)
	}
	status, body, _ := c.call(http.MethodPatch, "/settings", map[string]any{"first_steps_hidden": true})
	if status != http.StatusOK || body["first_steps_hidden"] != true || body["time_format"] != "24h" {
		t.Fatalf("patch = %d %v", status, body)
	}
	if strings.Contains(homeDE(c.b), "data-firststeps") {
		t.Error("the card is still shown after hiding through the API")
	}
	if status, body, _ := c.call(http.MethodPatch, "/settings", map[string]any{"first_steps_hidden": false, "time_format": "12h"}); status != http.StatusOK || body["first_steps_hidden"] != false || body["time_format"] != "12h" {
		t.Fatalf("show again = %d %v", status, body)
	}
	if !strings.Contains(homeDE(c.b), "data-firststeps") {
		t.Error("the card does not come back")
	}
}
