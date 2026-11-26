package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Ticket 338: more Orthodox name days, the calendar style and the Serbian Slava.

func birthdaysForm(f url.Values) url.Values {
	if f == nil {
		f = url.Values{}
	}
	return f
}

func postBirthdays(c ctBox, f url.Values) resp {
	return c.do(http.MethodPost, "/contacts/birthdays", birthdaysForm(f), map[string]string{"Accept-Language": "de"})
}

func TestBirthdaysPageHasTheOrthodoxCardsTheStyleAndTheSlava(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	page := c.page("/contacts/birthdays", "de").body
	ctHas(t, "page", page,
		`<label class="orth"><input type="radio" name="name_day_list" value="bulgarian_orthodox">`, `<b>Bulgarisch-orthodox</b>`, `<b>Ukrainisch-orthodox</b>`,
		`Seit 1. September 2023 nach dem neuen Kalender; Gemeinden des Moskauer Patriarchats feiern nach dem alten.`,
		`<details class="more nd-style">`, `<span>Mehr Optionen</span><span class="sum">Kalender: neuer Kalender</span>`,
		`<fieldset class="choice"><legend>Nach welchem Kalender feiert eure orthodoxe Kirche?</legend>`,
		`name="orthodox_calendar" value="revised_julian" checked`, `name="orthodox_calendar" value="julian"`,
		`<b>Neuer Kalender: feste Feiertage an unseren Tagen</b>`, `<b>Alter Kalender: feste Feiertage 13 Tage später</b>`,
		`href="/settings/religious-holidays">Religiöse Feiertage</a>`,
		`<dt>Slava</dt><dd>Aus.</dd>`, `<fieldset class="slava">`, `name="slava_on"`, `Slava im Kalender „Geburtstage“`, `Aus: keine Slava`,
		`<option value="" disabled selected>– Bitte wählen –</option>`, `<optgroup label="Dezember">`, `<option value="nikola">19. Dezember · Nikoljdan</option>`,
		`<optgroup label="Nach dem orthodoxen Osterfest, jedes Jahr an einem anderen Tag">`, `<option value="spas">Spasovdan (Christi Himmelfahrt)</option>`,
		`<optgroup label="Nicht in der Liste"><option value="own">Ein anderer Heiliger …</option></optgroup>`,
		`name="slava_name" type="text" maxlength="60"`, `name="slava_day" type="number"`, `name="slava_month"`, `id="hint-slava"`)
	ctHasNot(t, "page", page, `<details class="more nd-style" open`, `class="slava-now"`, `class="ferr"`)
	// The Russian list has no readable source yet (ticket 374): it is not offered.
	ctHasNot(t, "russian", page, `russian_orthodox`, `Russisch-orthodox`, `Russischen Orthodoxen Kirche`)
	ctHasNot(t, "russian", c.page("/contacts/birthdays", "en").body, `russian_orthodox`, `Russian Orthodox`)
	// The order of the Orthodox cards is alphabetical by their German name.
	order := []string{"bulgarian_orthodox", "greek_orthodox", "romanian_orthodox", "ukrainian_orthodox"}
	last := -1
	for _, v := range order {
		i := strings.Index(page, `name="name_day_list" value="`+v+`"`)
		if i < last {
			t.Errorf("%s is out of order", v)
		}
		last = i
	}
	// The order inside fieldset.nd: cards, hint, mismatch notices, then the style; the slava fieldset follows; one submit button, last.
	idx := func(s string) int { return strings.Index(page, s) }
	if !(idx(`value="ukrainian_orthodox"`) < idx(`class="hint"`) && idx(`class="hint"`) < idx(`class="notice nd-mismatch"`) && idx(`class="notice nd-mismatch"`) < idx(`class="more nd-style"`) &&
		idx(`class="more nd-style"`) < idx(`<fieldset class="slava">`) && idx(`<fieldset class="slava">`) < idx(`class="button" type="submit"`)) || strings.Count(page, `class="button" type="submit"`) != 1 {
		t.Error("the parts of the form are in the wrong order")
	}
	for _, l := range []string{"bulgarian_orthodox", "greek_orthodox", "romanian_orthodox"} {
		ctHas(t, "mismatch "+l, page, `class="notice nd-mismatch" data-list="`+l+`"`)
	}
	ctHasNot(t, "mismatch", page, `data-list="ukrainian_orthodox"`)
	ctHas(t, "bulgarian notice", page, `Die Bulgarische Orthodoxe Kirche feiert nach dem <b>neuen Kalender</b>. Eingestellt ist der alte: So stehen eure Namenstage 13 Tage später als in eurer Kirche. Stimmt das für eure Gemeinde nicht, wähle unter „Mehr Optionen“ den neuen Kalender.`)
	ctHas(t, "English", c.page("/contacts/birthdays", "en").body, "Another saint …", "More options", "Calendar: new calendar", "– Please choose –", "Ukrainian Orthodox", "Slava in the calendar “Birthdays”")
}

func TestSavingAnOrthodoxChurchWithTheOldCalendarAndASlavaSaysItInWords(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.add(t, contactForm("Mama K", "Katharina Maier", nil, nil))
	r := postBirthdays(c, url.Values{"name_days": {"1"}, "name_day_list": {"ukrainian_orthodox"}, "orthodox_calendar": {"julian"}, "slava_on": {"1"}, "slava": {"nikola"}})
	if r.status != http.StatusSeeOther || r.location != "/contacts/birthdays" {
		t.Fatalf("save = %d %q\n%s", r.status, r.location, r.body)
	}
	page := c.page("/contacts/birthdays", "de").body
	ctHas(t, "page", page, `Gespeichert: ukrainisch-orthodoxe Namenstage im alten Kalender und eure Slava Nikoljdan am 19. Dezember.`,
		`<dt>Namenstage</dt><dd>An, nach den ukrainisch-orthodoxen Namenstagen im alten Kalender.</dd>`, `<dt>Slava</dt><dd>Nikoljdan, jedes Jahr am 19. Dezember.</dd>`,
		`<details class="more nd-style" open>`, `<span class="sum">Kalender: alter Kalender</span>`, `name="orthodox_calendar" value="julian" checked`,
		`name="name_day_list" value="ukrainian_orthodox" checked`, `name="slava_on" value="1" checked`, `<option value="nikola" selected>19. Dezember · Nikoljdan</option>`,
		`<p class="slava-now"><b>Nikoljdan</b>: Sveti Nikola (Nikolaus von Myra), jedes Jahr am 19. Dezember für die ganze Familie.</p>`)
	ctHasNot(t, "page", page, `class="ferr"`)
	// The notice for the church that fits the style is in the page but only the live rule shows it; nothing is saved wrongly.
	ctHas(t, "English", c.page("/contacts/birthdays", "en").body, "Nikoljdan, every year on 19 December.", "On, from the Ukrainian Orthodox name days in the old calendar.")
}

func TestAMismatchOfChurchAndStyleSavesWithoutAnError(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	r := postBirthdays(c, url.Values{"name_days": {"1"}, "name_day_list": {"bulgarian_orthodox"}, "orthodox_calendar": {"julian"}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("a mismatch is not an error: %d\n%s", r.status, r.body)
	}
	page := c.page("/contacts/birthdays", "de").body
	ctHas(t, "page", page, `Gespeichert: bulgarisch-orthodoxe Namenstage im alten Kalender.`, `class="notice nd-mismatch" data-list="bulgarian_orthodox"`, `<details class="more nd-style" open>`,
		`name="orthodox_calendar" value="julian" checked`)
	// A church that fits the style leaves "Mehr Optionen" closed.
	postBirthdays(c, url.Values{"name_days": {"1"}, "name_day_list": {"greek_orthodox"}, "orthodox_calendar": {"revised_julian"}})
	ctHasNot(t, "fitting", c.page("/contacts/birthdays", "de").body, `<details class="more nd-style" open`)
	// The Ukrainian church has no fixed style: neither style opens the details by itself except Julian.
	postBirthdays(c, url.Values{"name_days": {"1"}, "name_day_list": {"ukrainian_orthodox"}, "orthodox_calendar": {"revised_julian"}})
	ctHasNot(t, "ukrainian", c.page("/contacts/birthdays", "de").body, `<details class="more nd-style" open`)
	postBirthdays(c, url.Values{"name_days": {"1"}, "name_day_list": {"ukrainian_orthodox"}, "orthodox_calendar": {"julian"}})
	ctHas(t, "ukrainian julian", c.page("/contacts/birthdays", "de").body, `<details class="more nd-style" open>`)
}

func TestTheSlavaErrorsAppearAtTheirFieldsAndKeepWhatWasTyped(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	r := postBirthdays(c, url.Values{"slava_on": {"1"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("no choice = %d", r.status)
	}
	ctHas(t, "no choice", r.body, `class="ferr" id="err-slava"`, `Wähle eure Slava aus der Liste. Steht sie nicht darin, wähle ganz unten „Ein anderer Heiliger …“ und trag Name und Tag ein.`,
		`name="slava_on" value="1" checked`, `aria-describedby="err-slava hint-slava src-slava"`)
	r = postBirthdays(c, url.Values{"slava_on": {"1"}, "slava": {"own"}, "slava_name": {""}, "slava_day": {"25"}, "slava_month": {"5"}})
	ctHas(t, "no name", r.body, `class="ferr" id="err-slava_name"`, `<option value="own" selected>`, `name="slava_day" type="number" inputmode="numeric" min="1" max="31" value="25"`)
	r = postBirthdays(c, url.Values{"slava_on": {"1"}, "slava": {"own"}, "slava_name": {"Sveti Vasilije Ostroški"}, "slava_day": {"31"}, "slava_month": {"6"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("31 June = %d", r.status)
	}
	ctHas(t, "31 June", r.body, `class="ferr" id="err-slava_day"`, `Den 31. Juni gibt es nicht. Wähle einen Tag, den es im Monat gibt.`, `value="Sveti Vasilije Ostroški"`, `<option value="6" selected>Juni</option>`, `aria-invalid="true"`)
	r = postBirthdays(c, url.Values{"slava_on": {"1"}, "slava": {"own"}, "slava_name": {"Sveti X"}, "slava_day": {"abc"}, "slava_month": {""}})
	ctHas(t, "no day", r.body, `id="err-slava_day"`)
	r = postBirthdays(c, url.Values{"slava_on": {"1"}, "slava": {"own"}, "slava_name": {strings.Repeat("x", 61)}, "slava_day": {"1"}, "slava_month": {"1"}})
	ctHas(t, "long", r.body, `id="err-slava_name"`)
	// Nothing was saved, name days stay as they were.
	if strings.Contains(c.page("/contacts/birthdays", "de").body, `name="slava_on" value="1" checked`) {
		t.Error("a wrong Slava was saved")
	}
	// A wrong Slava does not save the name days that came with it.
	postBirthdays(c, url.Values{"name_days": {"1"}, "name_day_list": {"catholic"}, "slava_on": {"1"}})
	ctHasNot(t, "name days", c.page("/contacts/birthdays", "de").body, `name="name_day_list" value="catholic" checked`)
}

func TestAnUnknownCalendarStyleIsRefusedAtItsFieldAndSavesNothing(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	r := postBirthdays(c, url.Values{"name_days": {"1"}, "name_day_list": {"catholic"}, "orthodox_calendar": {"gregorian"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown style = %d, want 422 as PATCH /settings answers", r.status)
	}
	ctHas(t, "unknown style", r.body, `class="ferr" id="err-orthodox_calendar"`, `Wähle den neuen oder den alten Kalender.`, `<details class="more nd-style" open>`, `aria-describedby="err-orthodox_calendar"`)
	ctHasNot(t, "name days", c.page("/contacts/birthdays", "de").body, `name="name_day_list" value="catholic" checked`)
}

func TestAnOwnSlavaIsSavedWithItsNameAndDay(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	r := postBirthdays(c, url.Values{"slava_on": {"1"}, "slava": {"own"}, "slava_name": {"Sveti Vasilije Ostroški"}, "slava_day": {"25"}, "slava_month": {"5"}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("own = %d\n%s", r.status, r.body)
	}
	page := c.page("/contacts/birthdays", "de").body
	ctHas(t, "page", page, `Gespeichert: eure Slava Sveti Vasilije Ostroški am 25. Mai.`, `<dt>Slava</dt><dd>Sveti Vasilije Ostroški, jedes Jahr am 25. Mai.</dd>`,
		`<option value="own" selected>`, `value="Sveti Vasilije Ostroški"`, `value="25"`, `<option value="5" selected>Mai</option>`)
	href := nameDayEventHref(t, c, "2026-05-25", "Slava: Sveti Vasilije Ostroški")
	ctHas(t, "event", c.page(href, "de").body, `<dt>Quelle</dt><dd>Von euch eingetragen</dd>`)
}

func TestSwitchingTheSlavaOffKeepsTheChoiceAndRemovesTheEvent(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	postBirthdays(c, url.Values{"slava_on": {"1"}, "slava": {"nikola"}})
	nameDayEventHref(t, c, "2026-12-14", "Slava: Nikoljdan")
	if r := postBirthdays(c, url.Values{"slava": {"nikola"}}); r.status != http.StatusSeeOther {
		t.Fatalf("off = %d", r.status)
	}
	page := c.page("/contacts/birthdays", "de").body
	ctHas(t, "page", page, `<dt>Slava</dt><dd>Aus.</dd>`, `<option value="nikola" selected>`)
	ctHasNot(t, "page", page, `name="slava_on" value="1" checked`)
	if strings.Contains(c.page("/calendar?week=2026-12-14", "de").body, "Slava: Nikoljdan") {
		t.Error("the Slava is still in the calendar")
	}
}

func TestSlavaIsJulianWhateverTheHouseholdStyleAndNamesTheChurchCalendar(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	postBirthdays(c, url.Values{"name_days": {"1"}, "name_day_list": {"romanian_orthodox"}, "orthodox_calendar": {"revised_julian"}, "slava_on": {"1"}, "slava": {"nikola"}})
	href := nameDayEventHref(t, c, "2026-12-14", "Slava: Nikoljdan")
	page := c.page(href, "de").body
	ctHas(t, "slava page", page, `<div class="ev-gen">`, `<h1>Slava: Nikoljdan</h1>`, `Samstag, 19.12.2026 · ganztägig`,
		`Diesen Termin macht Hejmo aus eurer Slava im Geburtstagskalender. Ändern kannst du ihn nur dort.`, `data-readonly`,
		`<dt>Kalender</dt><dd>Geburtstage</dd>`, `<dt>Für</dt><dd>Ganze Familie</dd>`, `<dt>Slava</dt><dd>Sveti Nikola (Nikolaus von Myra)</dd>`,
		`<dt>Wiederholung</dt><dd>Jedes Jahr am 19. Dezember. Das ist der 6. Dezember im Kirchenkalender; die Serbische Orthodoxe Kirche feiert nach dem alten Kalender.</dd>`,
		`<dt>Quelle</dt>`, `Kalender der Serbischen Orthodoxen Kirche`,
		`<a class="button secondary" href="/contacts/birthdays">Geburtstagskalender öffnen</a>`)
	ctHasNot(t, "slava page", page, `data-event-form`, `Kontakt „`, `src-unchecked`, `Noch nicht mit der Quelle abgeglichen`)
	ctHas(t, "English", c.page(href, "en").body, "Slava: Nikoljdan")
	ctHasNot(t, "English", c.page(href, "en").body, "Not yet checked against the source")
}

func TestANameDayInTheOldCalendarNamesTheChurchDay(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.add(t, contactForm("Mama K", "Katharina Maier", nil, nil))
	postBirthdays(c, url.Values{"name_days": {"1"}, "name_day_list": {"ukrainian_orthodox"}, "orthodox_calendar": {"julian"}})
	href := nameDayEventHref(t, c, "2026-12-07", "Namenstag Mama K (Kateryna)")
	page := c.page(href, "de").body
	ctHas(t, "event", page, `Montag, 07.12.2026 · ganztägig`, `<dt>Wiederholung</dt><dd>Jedes Jahr am 7. Dezember. Das ist der 24. November im Kirchenkalender, nach dem alten Kalender 13 Tage später.</dd>`,
		`Katharina von Alexandrien`, `Orthodoxen Kirche der Ukraine`)
	ctHasNot(t, "event", page, `src-unchecked`)
	// In the revised style the page says nothing about a church day.
	postBirthdays(c, url.Values{"name_days": {"1"}, "name_day_list": {"ukrainian_orthodox"}, "orthodox_calendar": {"revised_julian"}})
	page = c.page(nameDayEventHref(t, c, "2026-11-24", "Namenstag Mama K (Kateryna)"), "de").body
	ctHas(t, "revised", page, `Jedes Jahr am 24. November</dd>`)
	ctHasNot(t, "revised", page, `im Kirchenkalender`)
}

func TestTheContactFormAndPageNameTheStyle(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	id := c.add(t, contactForm("Opa", "Thomas Maier", nil, nil))
	postBirthdays(c, url.Values{"name_days": {"1"}, "name_day_list": {"ukrainian_orthodox"}, "orthodox_calendar": {"julian"}})
	form := c.page("/contacts/"+id+"/edit", "de").body
	ctHas(t, "form", form, `Nach den ukrainisch-orthodoxen Namenstagen im alten Kalender. Kirche und Kalender stellst du im `, `href="/contacts/birthdays">Geburtstagskalender</a>`)
	postBirthdays(c, url.Values{"name_days": {"1"}, "name_day_list": {"ukrainian_orthodox"}, "orthodox_calendar": {"revised_julian"}})
	ctHas(t, "form", c.page("/contacts/"+id+"/edit", "de").body, `Nach den ukrainisch-orthodoxen Namenstagen im neuen Kalender. Kirche und Kalender stellst du im `)
	postBirthdays(c, url.Values{"name_days": {"1"}, "name_day_list": {"catholic"}, "orthodox_calendar": {"julian"}})
	ctHasNot(t, "catholic", c.page("/contacts/"+id+"/edit", "de").body, `im alten Kalender`, `im neuen Kalender`)
}

func TestLicencesNameTheSourceEditionEveryListWasCheckedAgainst(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	page := c.page("/settings/licences", "de").body
	ctHas(t, "licences", page, `data-licence="namedays"`, `<h2>Namenstage und Slava</h2>`, `<ul class="nd-lic">`,
		`<li><b>Bulgarisch-orthodox</b>`, `<li><b>Ukrainisch-orthodox</b>`, `<li><b>Slava</b>`,
		`<span>Kalender der Bulgarischen Orthodoxen Kirche · Българска Патриаршия (https://www.bg-patriarshia.bg): Православен календар, geprüft an den Monatsseiten 2026 (bg-patriarshia.bg/calendar/2026-1 bis 2026-12)</span>`,
		`<span>Kalender der Orthodoxen Kirche der Ukraine · Календар Православної церкви України, geprüft an der Ausgabe 2026 (kalendar_2026.pdf, pomisna.info)</span>`,
		`<span>Kalender der Serbischen Orthodoxen Kirche · Календар Српске православне цркве, geprüft an den Heiligenseiten von spc.rs/sr/news/kalendar/ (Stand 26/11/2026)</span>`,
		`Kalender der Serbischen Orthodoxen Kirche`)
	// Every entry is checked, so no row says otherwise; the Russian list is not offered.
	ctHasNot(t, "licences", page, `src-unchecked`, `Noch nicht alle Einträge`, `Russisch-orthodox`, `Russischen Orthodoxen Kirche`)
	if strings.Count(page, `<ul class="nd-lic">`) != 1 || strings.Count(page, `<li><b>`) != 7 {
		t.Error("one row per list (six) and the Slava")
	}
	ctHas(t, "English", c.page("/settings/licences", "en").body, `Calendar of the Serbian Orthodox Church`, `Календар Српске православне цркве, geprüft an`)
}

func TestSettingsAPITakesTheCalendarStyleAndTheSlava(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	_, got, _ := c.api.call(http.MethodGet, "/settings", nil)
	sl, _ := got["slava"].(map[string]any)
	rel, _ := got["religious_holidays"].(map[string]any)
	if _, twice := got["orthodox_calendar"]; twice || rel["orthodox_calendar"] != "revised_julian" || sl == nil || sl["on"] != false || sl["saint"] != nil || sl["own_name"] != "" || sl["own_day"] != "" {
		t.Fatalf("defaults: %v", got)
	}
	status, got, _ := c.api.call(http.MethodPatch, "/settings", map[string]any{"religious_holidays": map[string]any{"orthodox_calendar": "julian"},
		"name_days": map[string]any{"on": true, "list": "ukrainian_orthodox"}, "slava": map[string]any{"on": true, "saint": "nikola"}})
	sl, _ = got["slava"].(map[string]any)
	rel, _ = got["religious_holidays"].(map[string]any)
	if status != http.StatusOK || rel["orthodox_calendar"] != "julian" || sl["on"] != true || sl["saint"] != "nikola" {
		t.Fatalf("patch = %d %v", status, got)
	}
	status, got, _ = c.api.call(http.MethodPatch, "/settings", map[string]any{"slava": map[string]any{"saint": "own", "own_name": "Sveti X", "own_day": "05-25"}})
	sl, _ = got["slava"].(map[string]any)
	if status != http.StatusOK || sl["saint"] != "own" || sl["own_name"] != "Sveti X" || sl["own_day"] != "05-25" || sl["on"] != true {
		t.Fatalf("own = %d %v", status, got)
	}
	_, got, _ = c.api.call(http.MethodPatch, "/settings", map[string]any{"slava": map[string]any{"on": false}})
	if sl, _ = got["slava"].(map[string]any); sl["on"] != false || sl["saint"] != "own" {
		t.Fatalf("off keeps the choice: %v", sl)
	}
	// The generated events are read only and carry their marker for MCP.
	c.api.call(http.MethodPatch, "/settings", map[string]any{"slava": map[string]any{"on": true, "saint": "nikola"}})
	_, _, list := c.api.call(http.MethodGet, "/events?from=2026-12-19T00:00:00Z&to=2026-12-20T00:00:00Z", nil)
	found := false
	for _, x := range list {
		if m := x.(map[string]any); m["title"] == "Slava: Nikoljdan" {
			found = true
			if m["read_only"] != true || m["generated"] != "slava" {
				t.Errorf("slava event = %v", m)
			}
		}
	}
	if !found {
		t.Fatalf("no Slava among %v", list)
	}
}

func TestSettingsAPIRefusesABadStyleOrSlavaAndSavesNothing(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	for why, body := range map[string]map[string]any{
		"an unknown style":    {"religious_holidays": map[string]any{"orthodox_calendar": "gregorian"}},
		"slava without saint": {"slava": map[string]any{"on": true}},
		"an unknown saint":    {"slava": map[string]any{"on": true, "saint": "nope"}},
		"own without a name":  {"slava": map[string]any{"on": true, "saint": "own", "own_day": "05-25"}},
		"own with a bad day":  {"slava": map[string]any{"on": true, "saint": "own", "own_name": "X", "own_day": "06-31"}},
		"own day format":      {"slava": map[string]any{"on": true, "saint": "own", "own_name": "X", "own_day": "31.06."}},
		"a name over 60":      {"slava": map[string]any{"on": true, "saint": "own", "own_name": strings.Repeat("x", 61), "own_day": "05-25"}},
	} {
		body["date_format"] = "mdy"
		if status, _, _ := c.api.call(http.MethodPatch, "/settings", body); status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", why, status)
		}
		if _, got, _ := c.api.call(http.MethodGet, "/settings", nil); got["date_format"] == "mdy" {
			t.Fatalf("%s: refused, yet saved", why)
		}
	}
}

func TestNameDayListsOfTheAPIIncludeTheNewChurches(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	for _, l := range []string{"bulgarian_orthodox", "ukrainian_orthodox"} {
		if status, got, _ := c.api.call(http.MethodPatch, "/settings", map[string]any{"name_days": map[string]any{"on": true, "list": l}}); status != http.StatusOK || got["name_days"].(map[string]any)["list"] != l {
			t.Errorf("%s = %d %v", l, status, got)
		}
	}
}

// The Russian list is withheld until ticket 374 has checked it: the API does not take it, the page does not offer it.
func TestTheRussianListIsRefusedByTheAPI(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	if status, _, _ := c.api.call(http.MethodPatch, "/settings", map[string]any{"name_days": map[string]any{"on": true, "list": "russian_orthodox"}}); status != http.StatusUnprocessableEntity {
		t.Errorf("russian_orthodox = %d, want 422", status)
	}
	if r := postBirthdays(c, url.Values{"name_days": {"1"}, "name_day_list": {"russian_orthodox"}}); r.status != http.StatusUnprocessableEntity {
		t.Errorf("the form took the Russian list: %d", r.status)
	}
	if _, got, _ := c.api.call(http.MethodGet, "/settings", nil); got["name_days"].(map[string]any)["on"] != false {
		t.Errorf("a refused list was saved: %v", got["name_days"])
	}
}

// eventTitles lists the titles of the events of a window of the API.
func eventTitles(t *testing.T, c ctBox, from, to string) []string {
	t.Helper()
	_, _, list := c.api.call(http.MethodGet, "/events?from="+from+"T00:00:00Z&to="+to+"T00:00:00Z", nil)
	var out []string
	for _, x := range list {
		out = append(out, x.(map[string]any)["title"].(string))
	}
	return out
}

func TestTheStyleIsOneValueAtSettingsReligiousHolidaysAndMovesTheNameDays(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.add(t, contactForm("Mama K", "Katharina Maier", nil, nil))
	c.api.call(http.MethodPatch, "/settings", map[string]any{"name_days": map[string]any{"on": true, "list": "ukrainian_orthodox"}})
	if got := eventTitles(t, c, "2026-11-24", "2026-11-25"); len(got) != 1 || !strings.Contains(got[0], "Mama K") {
		t.Fatalf("revised name day: %v", got)
	}
	// The style alone, with the religious holidays off, moves the name days.
	status, got, _ := c.api.call(http.MethodPatch, "/settings", map[string]any{"religious_holidays": map[string]any{"orthodox_calendar": "julian"}})
	if rel, _ := got["religious_holidays"].(map[string]any); status != http.StatusOK || rel["orthodox_calendar"] != "julian" || rel["on"] != false {
		t.Fatalf("patch = %d %v", status, got)
	}
	if _, twice := got["orthodox_calendar"]; twice {
		t.Error("the style is also at the top level of the settings")
	}
	if a, b := eventTitles(t, c, "2026-11-24", "2026-11-25"), eventTitles(t, c, "2026-12-07", "2026-12-08"); len(a) != 0 || len(b) != 1 {
		t.Fatalf("julian name day: %v on 24.11., %v on 07.12.", a, b)
	}
}

func TestTheBirthdaysPageStyleMovesTheReligiousHolidays(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.api.call(http.MethodPatch, "/settings", map[string]any{"religious_holidays": map[string]any{"on": true, "religions": []string{"orthodox"}}})
	if got := eventTitles(t, c, "2027-01-19", "2027-01-20"); len(got) != 0 {
		t.Fatalf("revised: %v", got)
	}
	postBirthdays(c, url.Values{"name_days": {"1"}, "name_day_list": {"ukrainian_orthodox"}, "orthodox_calendar": {"julian"}})
	if got := eventTitles(t, c, "2027-01-19", "2027-01-20"); len(got) != 1 {
		t.Fatalf("julian: %v", got)
	}
	_, got, _ := c.api.call(http.MethodGet, "/settings", nil)
	if rel, _ := got["religious_holidays"].(map[string]any); rel["orthodox_calendar"] != "julian" {
		t.Fatalf("the REST shape does not follow the page: %v", got)
	}
}
