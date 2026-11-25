package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// Ticket 318: name days in the Birthdays calendar.

func nameDays(t *testing.T, c ctBox, on bool, list string) resp {
	t.Helper()
	f := url.Values{}
	if on {
		f.Set("name_days", "1")
	}
	if list != "" {
		f.Set("name_day_list", list)
	}
	return c.do(http.MethodPost, "/contacts/birthdays", f, map[string]string{"Accept-Language": "de"}) // the titles of the events are in the language of who switched them on
}

// nameDaysOn switches name days on and returns the page that follows.
func nameDaysOn(t *testing.T, c ctBox, list string) string {
	t.Helper()
	if r := nameDays(t, c, true, list); r.status != http.StatusSeeOther || r.location != "/contacts/birthdays" {
		t.Fatalf("save = %d %q\n%s", r.status, r.location, r.body)
	}
	return c.page("/contacts/birthdays", "de").body
}

func TestBirthdaysPageIsForParents(t *testing.T) {
	t.Parallel()
	b, _ := configuredBox(t)
	if r := b.get("/contacts/birthdays"); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Fatalf("signed out = %d %q", r.status, r.location)
	}
	child := childSession(t, signedInBox(t))
	if r := child.get("/contacts/birthdays"); r.status != http.StatusForbidden {
		t.Errorf("child GET = %d, want 403", r.status)
	}
	if r := child.post("/contacts/birthdays", url.Values{"name_days": {"1"}, "name_day_list": {"catholic"}}); r.status != http.StatusForbidden {
		t.Errorf("child POST = %d, want 403", r.status)
	}
}

func TestBirthdaysPageStartsOffWithoutAChoice(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	page := c.page("/contacts/birthdays", "de").body
	ctHas(t, "page", page, `<h1>Geburtstagskalender</h1>`, `<li><a href="/settings">Einstellungen</a></li>`, `<a href="/contacts">Kontakte</a>`, `<span aria-current="page">Geburtstagskalender</span>`,
		`<dl class="facts bd-now">`, `<dt>Namenstage</dt><dd>Aus.</dd>`, `<fieldset class="nd">`, `name="name_days"`,
		`Aus: nur Geburtstage`, `<p class="grp">Katholisch und evangelisch</p>`, `<p class="grp">Orthodox</p>`,
		`value="catholic"`, `value="protestant"`, `value="romanian_orthodox"`, `value="greek_orthodox"`, `<button class="button" type="submit">Speichern</button>`)
	ctHasNot(t, "page", page, ` checked`, `nd-rows`, `nd-names`, `class="ferr"`)
	ctHas(t, "English", c.page("/contacts/birthdays", "en").body, "<h1>Birthdays calendar</h1>", "Off: birthdays only", "Greek Orthodox")
}

func TestSwitchingNameDaysOnNeedsAChurchAndShowsTheErrorAtTheChoice(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	r := nameDays(t, c, true, "")
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", r.status)
	}
	ctHas(t, "error", r.body, `class="ferr" id="err-name_day_list"`, "Wähle eine Kirche. Sonst weiß Hejmo nicht, an welchen Tagen eure Namenstage sind.", `name="name_days" value="1" checked`, `aria-describedby="err-name_day_list"`)
	if strings.Contains(c.page("/contacts/birthdays", "de").body, `name="name_days" value="1" checked`) {
		t.Error("the setting was saved without a church")
	}
}

func TestSavingNameDaysSaysHowManyHaveOne(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	f := contactForm("Opa", "Thomas Maier", nil, nil)
	c.add(t, f)
	c.add(t, contactForm("Kinderarzt", "", nil, nil))
	page := nameDaysOn(t, c, "catholic")
	ctHas(t, "page", page, `Namenstage an, nach den katholischen Namenstagen.`, `<dt>Namenstage</dt><dd>An, nach den katholischen Namenstagen.</dd>`, `name="name_day_list" value="catholic" checked`,
		`<h2 id="fam">Namenstage in eurer Familie</h2>`, `class="list-rows nd-rows"`)
	m := regexp.MustCompile(`(\d+) von (\d+) Personen und (\d+) weitere Kontakte haben jetzt ihren Namenstag im Kalender`).FindStringSubmatch(page)
	if m == nil || m[3] != "1" {
		t.Errorf("success line = %v\n%s", m, page)
	}
	// Further contacts without a name day are listed by name, never as a count.
	ctHas(t, "others", page, `<h2 id="miss">Weitere Kontakte ohne Namenstag</h2>`, `<ul class="nd-names">`, `>Kinderarzt</a>`, `Alle anderen Kontakte haben ihren Namenstag im Kalender.`)
	ctHasNot(t, "others", page, `>Opa</a>`)
}

func TestSwitchingNameDaysOffKeepsBirthdays(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	nameDaysOn(t, c, "catholic")
	if r := nameDays(t, c, false, "catholic"); r.status != http.StatusSeeOther {
		t.Fatalf("off = %d\n%s", r.status, r.body)
	}
	page := c.page("/contacts/birthdays", "de").body
	ctHas(t, "page", page, "Namenstage aus. Die Geburtstage bleiben im Kalender.", `<dt>Namenstage</dt><dd>Aus.</dd>`, `name="name_day_list" value="catholic" checked`)
	ctHasNot(t, "page", page, "nd-rows")
}

func TestChangingTheChurchNamesTheContactsWhoseChoiceWentAway(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	f := contactForm("Ben", "Ben Maier", nil, nil)
	id := c.add(t, f)
	nameDaysOn(t, c, "catholic")
	f.Set("name_day", "name")
	f.Set("name_day_name", "Benedikt")
	if r := c.send("/contacts/"+id+"/edit", f); r.status != http.StatusSeeOther {
		t.Fatalf("choose = %d\n%s", r.status, r.body)
	}
	page := nameDaysOn(t, c, "greek_orthodox")
	ctHas(t, "page", page, "Namenstage jetzt nach den griechisch-orthodoxen Namenstagen.", "stehen wieder am üblichen Tag ihres Namens: Ben.")
	// The same church again changes nothing.
	page = nameDaysOn(t, c, "greek_orthodox")
	ctHasNot(t, "page", page, "stehen wieder am üblichen Tag ihres Namens")
}

func TestContactFormHasTheCardNamenstagOnlyWhileNameDaysAreOn(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	id := c.add(t, contactForm("Opa", "Thomas Maier", nil, nil))
	if strings.Contains(c.page("/contacts/"+id+"/edit", "de").body, `id="nameday"`) {
		t.Error("the card is there while name days are off")
	}
	nameDaysOn(t, c, "catholic")
	page := c.page("/contacts/"+id+"/edit", "de").body
	ctHas(t, "form", page, `<fieldset id="nameday" class="nd-pick">`, `<legend>Namenstag</legend>`, `Aus dem Vornamen in „Thomas Maier“.`,
		`name="name_day" value="auto" checked`, `<span class="tdate">3. Juli</span> · Thomas`, `Der übliche Tag`, `name="name_day" value="entry:thomas-01-28"`,
		`Nach einem anderen Namen`, `Kein Namenstag`, `name="name_day_name" type="text" list="nd-list" maxlength="40"`, `<datalist id="nd-list">`, `<option value="Benedikt">`,
		`class="nd-src"`, `href="/contacts/birthdays"`)
	if strings.Index(page, `id="birthday"`) > strings.Index(page, `id="nameday"`) || strings.Index(page, `id="nameday"`) > strings.Index(page, `id="groups"`) {
		t.Error("the card does not stand after Geburtstag")
	}
}

func TestContactWithoutAMatchStartsOnNoNameDayAndSaysWhy(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	id := c.add(t, contactForm("Mia2", "Mia Maier", nil, nil))
	nameDaysOn(t, c, "catholic")
	page := c.page("/contacts/"+id+"/edit", "de").body
	ctHas(t, "form", page, `name="name_day" value="none" checked`, `Die katholischen Namenstage kennen „Mia“ nicht. Darum hat Mia2 noch keinen Namenstag.`, `class="notice"`, `„Mia“ ist oft eine Kurzform von „Maria“`)
	ctHasNot(t, "form", page, `name="name_day" value="auto"`)
	// Saving it as it stands does not pin the contact to "none": a new name finds its day again.
	f := contactForm("Mia2", "Mia Maier", nil, nil)
	f.Set("name_day", "none")
	c.send("/contacts/"+id+"/edit", f)
	f = contactForm("Mia2", "Maria Maier", nil, nil)
	c.send("/contacts/"+id+"/edit", f)
	ctHas(t, "contact", c.page("/contacts/"+id, "de").body, "15.08. · Maria")
}

func TestAnUnknownOtherNameIsAFieldErrorAndKeepsWhatWasTyped(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	id := c.add(t, contactForm("Mia2", "Mia Maier", nil, nil))
	nameDaysOn(t, c, "catholic")
	f := contactForm("Mia2", "Mia Maier", nil, nil)
	f.Set("name_day", "name")
	f.Set("name_day_name", "Mariella")
	r := c.send("/contacts/"+id+"/edit", f)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", r.status)
	}
	r.body = strings.ReplaceAll(r.body, "&#34;", `"`)
	ctHas(t, "form", r.body, `<a href="#nameday">Name day</a>`, `id="err-nd_name"`, `The Catholic name days do not know the name “Mariella”.`, `value="Mariella"`, `name="name_day" value="name" checked`)
	f.Set("name_day_name", "")
	if r := c.send("/contacts/"+id+"/edit", f); r.status != http.StatusUnprocessableEntity {
		t.Errorf("an empty name = %d", r.status)
	}
	f.Set("name_day_name", strings.Repeat("a", 41))
	if r := c.send("/contacts/"+id+"/edit", f); r.status != http.StatusUnprocessableEntity {
		t.Errorf("a long name = %d", r.status)
	}
}

func TestContactPageShowsTheNameDayAfterTheBirthday(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	f := contactForm("Oma Gerda", "Gerda Müller", nil, nil)
	f.Set("bday_day", "14")
	f.Set("bday_month", "3")
	id := c.add(t, f)
	ctHasNot(t, "page", c.page("/contacts/"+id, "de").body, "<dt>Namenstag</dt>")
	nameDaysOn(t, c, "catholic")
	ctHasNot(t, "no match", c.page("/contacts/"+id, "de").body, "<dt>Namenstag</dt>")
	f.Set("name_day", "name")
	f.Set("name_day_name", "Gertrud")
	if r := c.send("/contacts/"+id+"/edit", f); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	page := c.page("/contacts/"+id, "de").body
	ctHas(t, "page", page, "<dt>Namenstag</dt><dd>17.03. · Gertrud, von ")
	if strings.Index(page, "<dt>Geburtstag</dt>") > strings.Index(page, "<dt>Namenstag</dt>") {
		t.Error("the row Namenstag does not stand after Geburtstag")
	}
	nameDays(t, c, false, "catholic")
	ctHasNot(t, "off", c.page("/contacts/"+id, "de").body, "<dt>Namenstag</dt>")
}

func TestContactsPageLinksTheBirthdaysCalendar(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	ctHas(t, "without others", c.page("/contacts", "de").body, `<a href="/contacts/birthdays">Geburtstagskalender</a>`)
	c.add(t, contactForm("Opa", "", nil, nil))
	page := c.page("/contacts", "de").body
	ctHas(t, "overview", page, `<p class="hint grp-link"><a href="/contacts/groups">Gruppen verwalten</a>`, `<a href="/contacts/birthdays">Geburtstagskalender</a>`)
	ctHas(t, "hub", c.page("/settings", "de").body, "Geburtstage und Namenstage eurer Familie")
}

// nameDayEventHref finds the link of a name-day event in the week of the calendar page that holds the day.
func nameDayEventHref(t *testing.T, c ctBox, week, title string) string {
	t.Helper()
	page := c.page("/calendar?week="+week, "de").body
	m := regexp.MustCompile(`<a class="title" href="(/calendar/events/[^"]+)">` + regexp.QuoteMeta(title) + `</a>`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no link for %q in the week of %s", title, week)
	}
	return strings.ReplaceAll(m[1], "&amp;", "&")
}

func TestNameDayEventPageSaysWhereItComesFrom(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.add(t, contactForm("Mama K", "Katharina Maier", nil, nil))
	nameDaysOn(t, c, "catholic")
	href := nameDayEventHref(t, c, "2026-11-25", "Namenstag Mama K (Katharina)")
	page := c.page(href, "de").body
	ctHas(t, "event page", page, `<div class="ev-gen">`, `<h1>Namenstag Mama K (Katharina)</h1>`, `Mittwoch, 25.11.2026 · ganztägig`,
		`Diesen Termin macht Hejmo aus dem Kontakt „Mama K“ und den katholischen Namenstagen. Ändern kannst du ihn nur dort.`,
		`<dt>Kalender</dt><dd>Geburtstage</dd>`, `<dt>Für</dt><dd>Ganze Familie</dd>`, `<dt>Namenstag</dt><dd>Katharina von Alexandrien</dd>`,
		`<dt>Wiederholung</dt><dd>Jedes Jahr am 25. November</dd>`, `<dt>Quelle</dt><dd>Regionalkalender für das deutsche Sprachgebiet`,
		`Kontakt „Mama K“ öffnen</a>`, `href="/contacts/birthdays">Geburtstagskalender</a>`, `data-readonly`)
	ctHasNot(t, "event page", page, `ev-moved`, `data-event-form`)
	ctHas(t, "English", c.page(href, "en").body, "Hejmo makes this event from the contact “Mama K” and the Catholic name days.", "Every year on November 25")
}

func TestNameDayMovedByOrthodoxEasterSaysBothDates(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.add(t, contactForm("Onkel Giorgos", "Georg Papadopoulos", nil, nil))
	nameDaysOn(t, c, "greek_orthodox")
	// 2027: Orthodox Easter is on 2 May, so 23 April moves to Easter Monday, 3 May.
	href := nameDayEventHref(t, c, "2027-05-03", "Namenstag Onkel Giorgos (Georgios)")
	page := c.page(href, "de").body
	ctHas(t, "event page", page, `<p class="notice ev-moved"`, `Sonst am 23. April. 2027 ist das orthodoxe Osterfest erst am 2. Mai, darum feiert man den Namenstag am Ostermontag, dem 3. Mai.`,
		`Montag, 03.05.2027 · ganztägig`, `Jedes Jahr am 23. April; liegt er vor dem orthodoxen Osterfest, am Ostermontag`, `griechisch-orthodoxen Namenstagen`, `Georg, Großmärtyrer`)
	// 2026: Easter is on 12 April: nothing moves, and no notice says so.
	page = c.page(nameDayEventHref(t, c, "2026-04-20", "Namenstag Onkel Giorgos (Georgios)"), "de").body
	ctHasNot(t, "2026", page, `ev-moved`)
	ctHas(t, "2026", page, `Donnerstag, 23.04.2026`)
}

func TestOtherReadOnlyEventsKeepNoLink(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	f := contactForm("Oma Gerda", "", nil, nil)
	f.Set("bday_day", "25")
	f.Set("bday_month", "11")
	c.add(t, f)
	page := c.page("/calendar?week=2026-11-25", "de").body
	ctHas(t, "week", page, `Oma Gerda`)
	ctHasNot(t, "week", page, `<a class="title" href="/calendar/events/`+"")
}

func TestLicencesCreditTheNameDayCalendars(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	page := c.page("/settings/licences", "de").body
	ctHas(t, "licences", page, `data-licence="namedays"`, `<h2>Namenstage</h2>`, "nichts ist von Namenstag-Webseiten kopiert", "Katholisch: Regionalkalender für das deutsche Sprachgebiet",
		"Evangelisch: Evangelischer Namenkalender", "Rumänisch-orthodox:", "Griechisch-orthodox:", "Patriarhia Română")
}

func TestSettingsAPIHasTheNameDaysOffByDefaultAndTakesAChurch(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	status, got, _ := c.api.call(http.MethodGet, "/settings", nil)
	nd, _ := got["name_days"].(map[string]any)
	if status != http.StatusOK || nd == nil || nd["on"] != false || nd["list"] != nil {
		t.Fatalf("settings = %d %v, want name_days off with no list", status, got)
	}
	if status, _, _ := c.api.call(http.MethodPatch, "/settings", map[string]any{"name_days": map[string]any{"on": true}}); status != http.StatusUnprocessableEntity {
		t.Errorf("on without a church = %d, want 422", status)
	}
	if status, _, _ := c.api.call(http.MethodPatch, "/settings", map[string]any{"name_days": map[string]any{"on": true, "list": "anglican"}}); status != http.StatusUnprocessableEntity {
		t.Errorf("an unknown church = %d, want 422", status)
	}
	status, got, _ = c.api.call(http.MethodPatch, "/settings", map[string]any{"name_days": map[string]any{"on": true, "list": "protestant"}})
	nd, _ = got["name_days"].(map[string]any)
	if status != http.StatusOK || nd["on"] != true || nd["list"] != "protestant" {
		t.Fatalf("on = %d %v", status, got)
	}
	_, got, _ = c.api.call(http.MethodPatch, "/settings", map[string]any{"name_days": map[string]any{"on": false}})
	if nd, _ = got["name_days"].(map[string]any); nd["on"] != false || nd["list"] != "protestant" {
		t.Errorf("off keeps the church: %v", got["name_days"])
	}
}

func TestContactAPIHasTheNameDay(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	_, made, _ := c.api.call(http.MethodPost, "/contacts", map[string]any{"name": "Oma Gerda", "full_name": "Gerda Müller"})
	id := made["id"].(string)
	nd := made["name_day"].(map[string]any)
	if nd["mode"] != "auto" || nd["entry"] != nil || nd["name"] != nil || nd["resolved"] != nil {
		t.Fatalf("name_day = %v, want auto without a resolved day while name days are off", nd)
	}
	c.api.call(http.MethodPatch, "/settings", map[string]any{"name_days": map[string]any{"on": true, "list": "catholic"}})
	_, got, _ := c.api.call(http.MethodGet, "/contacts/"+id, nil)
	if got["name_day"].(map[string]any)["resolved"] != nil {
		t.Errorf("Gerda is no name in the Catholic list: %v", got["name_day"])
	}
	status, got, _ := c.api.call(http.MethodPut, "/contacts/"+id, map[string]any{"name": "Oma Gerda", "full_name": "Gerda Müller", "name_day": map[string]any{"mode": "name", "name": "Gertrud"}})
	nd = got["name_day"].(map[string]any)
	res, _ := nd["resolved"].(map[string]any)
	if status != http.StatusOK || nd["mode"] != "name" || nd["name"] != "Gertrud" || res == nil || !strings.HasSuffix(res["date"].(string), "-03-17") || res["name"] != "Gertrud" || res["saint"] != "Gertrud von Nivelles" {
		t.Fatalf("PUT = %d %v", status, nd)
	}
	for body, why := range map[string]map[string]any{
		"an unknown name": {"mode": "name", "name": "Mariella"}, "an empty name": {"mode": "name"}, "an unknown entry": {"mode": "entry", "entry": "nope"},
	} {
		if status, _, _ := c.api.call(http.MethodPut, "/contacts/"+id, map[string]any{"name": "Oma Gerda", "name_day": why}); status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", body, status)
		}
	}
	// A PUT without name_day keeps the choice.
	_, got, _ = c.api.call(http.MethodPut, "/contacts/"+id, map[string]any{"name": "Oma Gerda", "full_name": "Gerda Müller"})
	if got["name_day"].(map[string]any)["name"] != "Gertrud" {
		t.Errorf("a PUT without name_day dropped the choice: %v", got["name_day"])
	}
}

func TestAPIRefusesToEditOrDeleteANameDayEvent(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.add(t, contactForm("Mama K", "Katharina Maier", nil, nil))
	nameDaysOn(t, c, "catholic")
	status, _, list := c.api.call(http.MethodGet, "/events?from=2026-11-25T00:00:00Z&to=2026-11-26T00:00:00Z", nil)
	id := ""
	for _, x := range list {
		if m := x.(map[string]any); strings.HasPrefix(m["title"].(string), "Namenstag") {
			id = m["event_id"].(string)
		}
	}
	if status != http.StatusOK || id == "" {
		t.Fatalf("events = %d, no name day among %v", status, list)
	}
	if status, _, _ := c.api.call(http.MethodPatch, "/events/"+id, map[string]any{"title": "Anders"}); status != http.StatusUnprocessableEntity {
		t.Errorf("editing a name day = %d, want 422", status)
	}
	if status, _, _ := c.api.call(http.MethodDelete, "/events/"+id, nil); status != http.StatusUnprocessableEntity {
		t.Errorf("deleting a name day = %d, want 422", status)
	}
	_, _, again := c.api.call(http.MethodGet, "/events?from=2026-11-25T00:00:00Z&to=2026-11-26T00:00:00Z", nil)
	if len(again) != len(list) {
		t.Errorf("the name day went away: %d events before, %d after", len(list), len(again))
	}
}

func TestSettingsPatchWithABadNameDayChoiceSavesNothing(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	for why, nd := range map[string]map[string]any{
		"an unknown church":     {"on": true, "list": "anglican"},
		"on without a church":   {"on": true},
		"an unknown list alone": {"list": "anglican"},
	} {
		if status, _, _ := c.api.call(http.MethodPatch, "/settings", map[string]any{"date_format": "mdy", "name_days": nd}); status != http.StatusUnprocessableEntity {
			t.Fatalf("%s = %d, want 422", why, status)
		}
		_, got, _ := c.api.call(http.MethodGet, "/settings", nil)
		if got["date_format"] == "mdy" {
			t.Fatalf("%s: 422, yet date_format was saved", why)
		}
	}
}
