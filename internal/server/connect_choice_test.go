package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/outbound"
)

// Ticket #231 (product owner decision Q156): one choice page for every way to
// add a calendar. Preview: docs/design/preview-231.html.

var (
	wayRadioRe = regexp.MustCompile(`<input type="radio" name="service" value="(\w+)"( checked)?( disabled)?>`)
	wayGuideRe = regexp.MustCompile(`<form class="guide" data-service="(\w+)"`)
)

// ways lists the radio values of the cards in page order, and which are checked.
func ways(t *testing.T, page string) (all, checked []string) {
	t.Helper()
	for _, m := range wayRadioRe.FindAllStringSubmatch(connectBlock(t, page), -1) {
		all = append(all, m[1])
		if m[2] != "" {
			checked = append(checked, m[1])
		}
	}
	return all, checked
}

func listEq(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

func TestChoicePageHasOneEqualCardPerWayAndNoneChosen(t *testing.T) {
	t.Parallel()
	b := signedInAPI(t).b
	for _, path := range []string{"/connections/new", "/connections/new?service=bogus"} {
		r := b.get(path)
		if r.status != http.StatusOK {
			t.Fatalf("%s = %d", path, r.status)
		}
		all, checked := ways(t, r.body)
		// The order of Q156; the Google card comes with #18, Outlook joined with #177.
		if !listEq(all, []string{"icloud", "outlook", "caldav", "ics"}) {
			t.Errorf("%s: cards = %v", path, all)
		}
		if len(checked) != 0 {
			t.Errorf("%s: preselected %v, want no card chosen", path, checked)
		}
		mustContain(t, r.body, "<legend>Woher kommen eure Termine?</legend>",
			"<b>Kalender-Link abonnieren</b>", "die Box liest ihn nur.",
			`<p class="hint pick-first">Wähle einen Weg. Darunter steht dann, was du dafür brauchst und wie es geht.</p>`,
			"Dort wird nichts gelöscht.")
		var guides []string
		for _, m := range wayGuideRe.FindAllStringSubmatch(r.body, -1) {
			guides = append(guides, m[1])
		}
		if !listEq(guides, all) {
			t.Errorf("%s: guides %v do not match the cards %v", path, guides, all)
		}
	}
}

func TestChoicePageChoosesTheWayOfTheQuery(t *testing.T) {
	t.Parallel()
	b := signedInAPI(t).b
	for way, want := range map[string]string{"icloud": "icloud", "outlook": "outlook", "caldav": "caldav", "ics": "ics"} {
		_, checked := ways(t, b.get("/connections/new?service="+way).body)
		if !listEq(checked, []string{want}) {
			t.Errorf("service=%s: checked = %v", way, checked)
		}
	}
}

func TestLinkGuideIsTheThreeStepFormThatReplacesTheOldAddForm(t *testing.T) {
	t.Parallel()
	b := signedInAPI(t).b
	page := b.get("/connections/new?service=ics").body
	form := formOf(t, page, "/calendars")
	mustContain(t, page, `<form class="guide" data-service="ics" method="post" action="/calendars"`)
	mustContain(t, form, "Was heißt abonnieren?", `<ol class="howto">`,
		"Den Link kopieren", "Wo finde ich den Link?", `name="url"`, "Beginnt mit https://, http:// oder webcal://.",
		"Einen Namen geben", `name="name"`, "Personen wählen", `class="pchip all"`, `name="whole_family" value="1" data-whole checked`,
		"Weitere Optionen", `name="color"`, `name="interval"`, `<option value="60" selected>60</option>`,
		`>Kalender abonnieren</button>`, `<p class="done-hint">`)
	if n := strings.Count(form, "<li>"); n < 3 {
		t.Errorf("%d steps, want the link, the name and the people", n)
	}
	// Fields sit inside their steps: link, name, people.
	u, n, p := strings.Index(form, `name="url"`), strings.Index(form, `name="name"`), strings.Index(form, `class="chips"`)
	if !(u < n && n < p) {
		t.Errorf("order link %d, name %d, people %d", u, n, p)
	}
	// The addresses in the help are words, not links to follow.
	if strings.Contains(connectBlock(t, page), `name="provider" value="ics"`) {
		t.Error("the link guide is not a connection")
	}
}

func TestSubscribingFromTheChoicePageStartsTheFirstSyncAndNamesTheCalendar(t *testing.T) {
	t.Parallel()
	b := signedInAPI(t).b
	b.feeds.set(schoolFeed, feedOf("UID:a\r\nSUMMARY:Choir\r\nDTSTART:20261120T170000"), nil)
	r := b.post("/calendars", url.Values{"name": {"Grundschule"}, "url": {"webcal://example.org/school.ics"}, "interval": {"60"}, "color": {"#2F5E7A"}, "whole_family": {"1"}})
	if r.status != http.StatusSeeOther || r.location != "/calendars" {
		t.Fatalf("subscribe = %d %q", r.status, r.location)
	}
	if got := b.feeds.asked; len(got) != 1 || got[0] != schoolFeed {
		t.Fatalf("the feed was asked %v: the first sync did not start before the success line", got)
	}
	de := b.do(http.MethodGet, "/calendars", nil, map[string]string{"Accept-Language": "de"}).body
	if got := successLine(de); got != "„Grundschule“ abonniert. Die Box holt die Termine jetzt zum ersten Mal." {
		t.Errorf("success line = %q", got)
	}
	mustContain(t, de, "<b>Grundschule</b>", "Zuletzt abgeglichen")
	mustNotContain(t, de, "Noch nicht abgeglichen")
}

func TestSubscribingEnglishSuccessLine(t *testing.T) {
	t.Parallel()
	b := signedInAPI(t).b
	b.feeds.set(schoolFeed, feedOf(), nil)
	b.post("/calendars", url.Values{"name": {"School"}, "url": {schoolFeed}, "interval": {"60"}, "whole_family": {"1"}})
	if got := successLine(getEN(b, "/calendars").body); got != "“School” subscribed. The box fetches the events for the first time now." {
		t.Errorf("success line = %q", got)
	}
}

func TestSubscribingWithBadInputStaysOnTheChoicePageWithTheLinkCardChosen(t *testing.T) {
	t.Parallel()
	b := signedInAPI(t).b
	r := b.post("/calendars", url.Values{"name": {""}, "url": {"grundschule-am-lindenpark.de Termine"}, "interval": {"60"}, "color": {"#4E7D52"}, "people": {}, "whole_family": {"1"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d\n%s", r.status, r.body)
	}
	all, checked := ways(t, r.body)
	if len(all) != 4 || !listEq(checked, []string{"ics"}) {
		t.Errorf("cards %v, checked %v: the link card must stay chosen", all, checked)
	}
	mustContain(t, r.body, "<h1>Kalender verbinden</h1>", `<a href="/calendars">Kalender-Verbindungen</a>`,
		`id="err-url"`, "Das ist kein Link. Ein Kalender-Link beginnt mit https://, http:// oder webcal://.",
		`value="grundschule-am-lindenpark.de Termine"`)
	// Both mistakes show at once, each at its field.
	mustContain(t, r.body, `id="err-name"`, "Bitte gib einen Namen ein.")
	// Nothing typed is lost.
	r = b.post("/calendars", url.Values{"name": {""}, "url": {"https://example.org/a.ics"}, "interval": {"60"}, "people": {}, "whole_family": {"1"}})
	mustContain(t, r.body, `id="err-name"`, "Bitte gib einen Namen ein.", `value="https://example.org/a.ics"`)
	if _, checked := ways(t, r.body); !listEq(checked, []string{"ics"}) {
		t.Errorf("checked = %v", checked)
	}
}

func TestSubscribingKeepsThePeopleChosenAfterAnError(t *testing.T) {
	t.Parallel()
	b := signedInAPI(t).b
	h, _ := b.db.HomeHousehold()
	people, err := b.db.People(h.ID)
	if err != nil || len(people) == 0 {
		t.Fatalf("people %v %v", people, err)
	}
	r := b.post("/calendars", url.Values{"name": {""}, "url": {schoolFeed}, "interval": {"60"}, "people": {people[0].ID}})
	mustContain(t, r.body, `name="people" value="`+people[0].ID+`" checked`)
	mustNotContain(t, r.body, `name="whole_family" value="1" data-whole checked`)
}

func TestOverviewHasNoAddFormOnlyOneWayIn(t *testing.T) {
	t.Parallel()
	b := signedInAPI(t).b
	empty := b.get("/calendars").body
	mustContain(t, empty, `data-empty`, `href="/connections/new">Kalender verbinden</a>`, "Noch kein Kalender auf der Box.", "Verbinde iCloud, Outlook oder einen Kalender-Server, oder abonniere einen Kalender-Link, zum Beispiel von der Schule.")
	mustNotContain(t, empty, `action="/calendars"`, `name="url"`, `aria-labelledby="add"`, "Kalender hinzufügen", "Verbundene Dienste", "Abonnierte Kalender</h2>")

	b.feeds.set(schoolFeed, feedOf(), nil)
	b.post("/calendars", url.Values{"name": {"Grundschule"}, "url": {schoolFeed}, "interval": {"60"}, "whole_family": {"1"}})
	page := b.get("/calendars").body
	mustContain(t, page, `<p class="add-way"><a class="button" href="/connections/new">Kalender verbinden</a></p>`,
		"<h2 id=\"connections\">Verbundene Dienste</h2>", "Konten bei iCloud, Outlook oder einem Kalender-Server.", `<p class="hint none">Noch kein Dienst verbunden.</p>`,
		"<h2 id=\"synced\">Abonnierte Kalender</h2>", "Kalender-Links, zum Beispiel von Schule oder Verein. Die Box liest sie nur.", "<b>Grundschule</b>")
	mustNotContain(t, page, `action="/calendars"`, `name="url"`, "data-empty", "Kalender hinzufügen")
	if strings.Index(page, `class="add-way"`) > strings.Index(page, `id="connections"`) {
		t.Error("the button belongs right under the lead, before the sections")
	}
}

func TestOverviewWithOnlyAServiceSaysNoLinkYet(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	connectedBox(t, b)
	page := b.do(http.MethodGet, "/calendars", nil, map[string]string{"Accept-Language": "de"}).body
	mustContain(t, page, "Verbundene Dienste", `<p class="hint none">Noch kein Kalender-Link abonniert.</p>`, `class="add-way"`)
	mustNotContain(t, page, "data-empty")
}

func TestOnlyCalendarServicesOffLocksTheirCardsAndLeavesTheLinkChosen(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	if err := b.db.SetServiceEnabled(outbound.Calendars, false); err != nil {
		t.Fatal(err)
	}
	page := getEN(b, "/connections/new").body
	all, checked := ways(t, page)
	if len(all) != 4 || !listEq(checked, []string{"ics"}) {
		t.Errorf("cards %v, checked %v: with one way left it is chosen", all, checked)
	}
	for _, way := range []string{"icloud", "outlook", "caldav"} {
		mustContain(t, page, `<label class="locked"><input type="radio" name="service" value="`+way+`" disabled>`)
	}
	mustNotContain(t, page, `<label class="locked"><input type="radio" name="service" value="ics"`)
	// A locked way has no guide to fill in: nothing on the page could be sent.
	if got := wayGuideRe.FindAllStringSubmatch(page, -1); len(got) != 1 || got[0][1] != "ics" {
		t.Errorf("guides = %v, want only the link guide", got)
	}
	if n := strings.Count(page, `<small class="lock">`); n != 3 {
		t.Errorf("%d lock lines, want one on each locked card", n)
	}
	mustContain(t, page, "Locked: Calendar services are off", `class="offline-note"`, "Calendar services are off.", `href="/settings/connections#calendars"`)
}

func TestOnlyLinksOffLocksTheLinkCardAndChoosesNothing(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	if err := b.db.SetServiceEnabled(outbound.ICS, false); err != nil {
		t.Fatal(err)
	}
	page := getEN(b, "/connections/new").body
	all, checked := ways(t, page)
	if len(all) != 4 || len(checked) != 0 {
		t.Errorf("cards %v, checked %v", all, checked)
	}
	mustContain(t, page, `<label class="locked"><input type="radio" name="service" value="ics" disabled>`, "Locked: ICS subscriptions are off", "ICS subscriptions are off.", `href="/settings/connections#ics"`)
	mustNotContain(t, page, `<label class="locked"><input type="radio" name="service" value="icloud"`)
}

func TestOfflineModeShowsTheNoticeAndWhatWorksAtHomeInsteadOfTheCards(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	offlineOn(t, b)
	page := getEN(b, "/connections/new").body
	mustContain(t, page, `class="offline-note"`, "Offline mode is on.", "Connecting a calendar and subscribing to a calendar link need a connection to the internet.",
		"In the home network this still works", `href="/caldav"`)
	mustNotContain(t, page, `name="service"`, `class="guide"`, `class="pick-first"`, `class="lead"`)
	if n := strings.Count(page, `class="offline-note"`); n != 1 {
		t.Errorf("%d notes, want one", n)
	}
}

func TestEveryWayOffShowsEachNoteAndWhatWorksAtHome(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	for _, s := range []outbound.Service{outbound.Calendars, outbound.ICS} {
		if err := b.db.SetServiceEnabled(s, false); err != nil {
			t.Fatal(err)
		}
	}
	page := getEN(b, "/connections/new").body
	if n := strings.Count(page, `class="offline-note"`); n != 2 {
		t.Errorf("%d notes, want one per service", n)
	}
	mustContain(t, page, "In the home network this still works")
	mustNotContain(t, page, `name="service"`)
}

func TestSubscribingIsRefusedWhileLinksAreOffAndSendsBackToThePageThatSaysWhy(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	offlineOn(t, b)
	r := b.post("/calendars", url.Values{"name": {"Waste"}, "url": {"https://www.example.org/waste.ics"}, "interval": {"360"}, "whole_family": {"1"}})
	if r.status != http.StatusSeeOther || r.location != "/connections/new" {
		t.Fatalf("subscribe = %d %q, want back to the choice page", r.status, r.location)
	}
}

func TestOverviewHidesTheButtonInOfflineMode(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	offlineOn(t, b)
	page := getEN(b, "/calendars").body
	mustContain(t, page, `class="offline-note"`)
	mustNotContain(t, page, `class="add-way"`, `href="/connections/new"`)
}

func TestEmptyOverviewWithCalendarServicesOffStillSaysSo(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	if err := b.db.SetServiceEnabled(outbound.Calendars, false); err != nil {
		t.Fatal(err)
	}
	page := getEN(b, "/calendars").body
	mustContain(t, page, "data-empty", `class="offline-note"`, "Calendar services are off.")
}

// Until #18 adds its card, no text invites users to connect Google.
func TestTextsNameOnlyTheWaysThatHaveACard(t *testing.T) {
	t.Parallel()
	bundle := i18n.MustLoad()
	for _, key := range []string{"connections.about", "calendars.empty", "connect.note.calendars", "connect.note.ics"} {
		for _, lang := range []string{"en", "de"} {
			if text := bundle.T(lang, key); strings.Contains(text, "Google") {
				t.Errorf("%s (%s) names a service without a card: %q", key, lang, text)
			}
		}
	}
}
