package server_test

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Ticket 296 (ADR 0021, preview-296.html): the address book in the web interface and over REST.

type ctBox struct{ peopleBox }

func newCtBox(t *testing.T) ctBox {
	t.Helper()
	return ctBox{newPeopleBox(t)}
}

func (c ctBox) page(path, lang string) resp {
	return c.do(http.MethodGet, path, nil, map[string]string{"Accept-Language": lang})
}

func (c ctBox) send(path string, f url.Values) resp {
	return c.do(http.MethodPost, path, f, map[string]string{"Accept-Language": "en"})
}

// contactForm is a filled contact form: numbers are (kind, own label, number) lines.
func contactForm(name, full string, numbers [][3]string, emails [][3]string) url.Values {
	f := url.Values{"name": {name}, "full_name": {full}}
	for _, p := range numbers {
		f.Add("number_label", p[0])
		f.Add("number_own", p[1])
		f.Add("number", p[2])
	}
	for _, e := range emails {
		f.Add("email_label", e[0])
		f.Add("email_own", e[1])
		f.Add("email", e[2])
	}
	return f
}

// add makes a contact through the web form and returns its id.
func (c ctBox) add(t *testing.T, f url.Values) string {
	t.Helper()
	r := c.send("/contacts/new", f)
	m := regexp.MustCompile(`^/contacts/([0-9a-f-]{36})$`).FindStringSubmatch(r.location)
	if r.status != http.StatusSeeOther || m == nil {
		t.Fatalf("add = %d %q\n%s", r.status, r.location, r.body)
	}
	return m[1]
}

func ctHas(t *testing.T, what, page string, want ...string) {
	t.Helper()
	page = html.UnescapeString(page) // the templates escape + and quotes the way HTML wants; people read the text
	for _, w := range want {
		if !strings.Contains(page, w) {
			t.Errorf("%s lacks %q\n%s", what, w, page)
		}
	}
}

func ctHasNot(t *testing.T, what, page string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(page, w) {
			t.Errorf("%s holds %q\n%s", what, w, page)
		}
	}
}

func rowOf(page, name string) string {
	for _, r := range regexp.MustCompile(`(?s)<li><a href="/contacts/[^"]*">.*?</li>`).FindAllString(page, -1) {
		if strings.Contains(r, "<span>"+name+"</span>") || strings.Contains(r, "<b>"+name+"</b>") {
			return r
		}
	}
	return ""
}

func TestContactsNeedASignedInParent(t *testing.T) {
	t.Parallel()
	b, _ := configuredBox(t)
	if r := b.get("/contacts"); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Fatalf("signed out = %d %q", r.status, r.location)
	}
	child := childSession(t, signedInBox(t))
	for _, path := range []string{"/contacts", "/contacts/new", "/contacts/groups"} {
		if r := child.get(path); r.status != http.StatusForbidden {
			t.Errorf("child GET %s = %d, want 403", path, r.status)
		}
	}
	if r := child.post("/contacts/new", url.Values{"name": {"Oma"}}); r.status != http.StatusForbidden {
		t.Errorf("child POST = %d, want 403", r.status)
	}
	if r := child.get("/settings"); r.status == http.StatusOK && strings.Contains(r.body, `href="/contacts"`) {
		t.Error("a child sees the Contacts row")
	}
}

func TestSettingsHubHasContactsRightAfterPeopleWithTheStateInWords(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.add(t, contactForm("Oma Gerda", "", nil, nil))
	c.add(t, contactForm("Kinderarzt", "", nil, nil))
	de := c.page("/settings", "de").body
	ctHas(t, "hub", de, `href="/contacts"`, "<strong>Kontakte</strong>", "<b>1 aus eurer Familie, 2 weitere.</b>")
	if strings.Index(de, `href="/people"`) > strings.Index(de, `href="/contacts"`) || strings.Index(de, `href="/contacts"`) > strings.Index(de, `href="/settings/lists"`) {
		t.Errorf("Contacts is not right after People\n%s", de)
	}
	ctHas(t, "English hub", c.page("/settings", "en").body, "<strong>Contacts</strong>", "<b>1 from your family, 2 more.</b>")
}

// Acceptance: the Hejmo name is what is shown.
func TestContactOverviewShowsTheHejmoNameWithEveryNumberInWordsAndGroupsAsTags(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	groups := map[string]string{}
	_, _, list := c.api.call(http.MethodGet, "/contact-groups", nil)
	for _, g := range list {
		m := g.(map[string]any)
		if k, _ := m["shipped_key"].(string); k != "" {
			groups[k] = m["id"].(string)
		}
	}
	f := contactForm("Oma Gerda", "Gerda Müller",
		[][3]string{{"cell", "", "0171 3456 789"}, {"home", "", "08031 87 65 43"}, {"own", "Gartenhaus", "0160 111 2223"}}, nil)
	f["group"] = []string{groups["familie"], groups["notfall"]}
	c.add(t, f)
	c.add(t, contactForm("Kita Sonnenschein", "Kita Sonnenschein", [][3]string{{"work", "", "089 7654 3210"}}, nil))
	c.add(t, contactForm("Ärztlicher Bereitschaftsdienst", "", nil, nil))
	c.add(t, contactForm("Zahnarzt", "", [][3]string{{"own", "Notdienst", "112"}}, nil))
	page := c.page("/contacts", "de").body
	ctHas(t, "overview", page, "<h1>Kontakte</h1>", `<a class="button" href="/contacts/new">Kontakt anlegen</a>`, `class="lib-search"`, `class="lib-filter ct-filter"`, `href="/contacts/groups">Gruppen verwalten</a>`)
	gerda := rowOf(page, "Oma Gerda")
	ctHas(t, "Gerda's row", gerda, `<span class="meta">Gerda Müller</span>`, `<span class="lab">Mobil</span> 0171 3456 789`, `<span class="lab">Zuhause</span> 08031 87 65 43`, `<span class="lab">Gartenhaus</span> 0160 111 2223`,
		`<span class="tag grp">Familie</span>`, `<span class="tag grp">Notfall</span>`)
	ctHasNot(t, "a name equal to the Hejmo name", rowOf(page, "Kita Sonnenschein"), `class="meta"`)
	ctHas(t, "a contact without numbers", rowOf(page, "Ärztlicher Bereitschaftsdienst"), `<span class="none">Keine Telefonnummer</span>`)
	// A to Z by Hejmo name in the language's order (Ä sorts with A), then the family in the order of people.
	var at []int
	for _, name := range []string{"Weitere Kontakte", "Ärztlicher Bereitschaftsdienst", "Kita Sonnenschein", "Oma Gerda", "Zahnarzt", "Aus eurer Familie", "Mama"} {
		at = append(at, strings.Index(page, ">"+name+"<"))
	}
	if !sort.IntsAreSorted(at) || at[0] < 0 || at[len(at)-1] < 0 {
		t.Errorf("the order of the page is not A to Z, then the family: %v", at)
	}
	mama := rowOf(page, "Mama")
	ctHas(t, "Mama's row", mama, `<b class="who"><span class="marker" data-person="1"`, `<span class="none">Keine Telefonnummer</span>`)
	ctHasNot(t, "a family row", mama, `class="grps"`)
	ctHas(t, "filter", page, `aria-current="true">Alle</a>`, ">Familie</a>", ">Kinderbetreuung und Schule</a>", ">Arztpraxen</a>", ">Notfall</a>", ">Sonstige</a>")
}

func TestContactSearchLooksInNamesNumbersAndEmailsAndFiltersByGroup(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.add(t, contactForm("Oma Gerda", "Gerda Müller", [][3]string{{"cell", "", "0171 3456 789"}}, [][3]string{{"home", "", "gerda.mueller@example.de"}}))
	c.add(t, contactForm("Kita Sonnenschein", "", [][3]string{{"work", "", "089 7654 3210"}}, nil))
	for q, want := range map[string]string{"gerda": "Oma Gerda", "müller": "Oma Gerda", "muller": "Oma Gerda", "34567": "Oma Gerda", "0171": "Oma Gerda", "example.de": "Oma Gerda", "7654": "Kita Sonnenschein"} {
		page := c.page("/contacts?q="+url.QueryEscape(q), "de").body
		ctHas(t, "search "+q, page, "<b>"+want+"</b>")
	}
	if page := c.page("/contacts?q=gerda", "de").body; strings.Contains(page, "Kita Sonnenschein") {
		t.Error("the search keeps a contact that does not match")
	}
	none := c.page("/contacts?q="+url.QueryEscape("Bäckerei Huber"), "de").body
	ctHas(t, "no match", none, "Kein Kontakt passt zu „Bäckerei Huber“. Gesucht wird in Namen, Nummern und E-Mail-Adressen.", `href="/contacts/new?name=B%C3%A4ckerei+Huber"`, "„Bäckerei Huber“ als Kontakt anlegen", "Suche aufheben")
	// The new form has the name prefilled.
	ctHas(t, "prefilled form", c.page("/contacts/new?name=B%C3%A4ckerei+Huber", "de").body, `value="Bäckerei Huber"`)
}

func TestWithoutOtherContactsThereIsNoSearchAndTheEmptyStateSaysWhatBelongsHere(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	page := c.page("/contacts", "de").body
	ctHas(t, "empty overview", page, "Noch keine weiteren Kontakte. Leg zum Beispiel Großeltern, Kita oder Kinderarztpraxis an.", "Aus eurer Familie", "Mama")
	ctHasNot(t, "empty overview", page, `class="lib-search"`, `class="lib-filter`, "Gruppen verwalten")
}

// Acceptance: the number stays as typed; empty lines and an empty address are never stored.
func TestTypedNumberIsKeptAndEmptyLinesAndAddressAreNeverStored(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	f := contactForm("Oma Gerda", "", [][3]string{{"cell", "", "  +49 (0)171/3456-789 "}, {"home", "", "   "}, {"work", "", ""}}, [][3]string{{"home", "", ""}})
	f.Set("street", "  ")
	f.Set("city", "\t")
	id := c.add(t, f)
	_, got, _ := c.api.call(http.MethodGet, "/contacts/"+id, nil)
	numbers := got["numbers"].([]any)
	if len(numbers) != 1 || numbers[0].(map[string]any)["value"] != "+49 (0)171/3456-789" || numbers[0].(map[string]any)["kind"] != "cell" {
		t.Errorf("numbers = %v", numbers)
	}
	if len(got["emails"].([]any)) != 0 {
		t.Errorf("emails = %v", got["emails"])
	}
	if a := got["address"].(map[string]any); a["street"] != "" || a["city"] != "" || a["postcode"] != "" || a["country"] != "" {
		t.Errorf("an empty address was stored: %v", a)
	}
	page := c.page("/contacts/"+id, "en").body
	ctHas(t, "contact page", page, `href="tel:+491713456789"`, "<span>+49 (0)171/3456-789</span>")
	ctHasNot(t, "contact page", page, "<address", "Address")
}

// Acceptance: a birthday without a year shows no age.
func TestContactPageShowsTheBirthdayInTheHouseholdFormatWithAgeOnlyWhenTheYearIsKnown(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	f := contactForm("Oma Gerda", "Gerda Müller", [][3]string{{"cell", "", "0171 3456 789"}}, [][3]string{{"home", "", "gerda.mueller@example.de"}, {"own", "Verein", "gerda@gartenfreunde.example"}})
	f.Set("bday_day", "14")
	f.Set("bday_month", "3")
	f.Set("street", "Am Erlenbach 7")
	f.Set("postcode", "83022")
	f.Set("city", "Rosenheim")
	f.Set("note", "Hat einen Schlüssel.\nHolt dienstags ab.")
	id := c.add(t, f)
	de := c.page("/contacts/"+id, "de").body
	ctHas(t, "contact page", de, "<h1>Oma Gerda</h1>", `<p class="ct-full">Gerda Müller</p>`, `<dl class="facts ct">`, `<dt class="l">Mobil</dt>`, `<a class="call" href="tel:01713456789" aria-label="Mobil anrufen: 0171 3456 789">`,
		`<dt class="l">E-Mail privat</dt>`, `<a class="mail" href="mailto:gerda.mueller@example.de">`, `<dt class="l">E-Mail Verein</dt>`,
		`<address class="adr"><span>Am Erlenbach 7</span><span>83022 Rosenheim</span></address>`, "<dd>14.03. · Jahr unbekannt</dd>", `<dd class="note">Hat einen Schlüssel.`+"\n"+`Holt dienstags ab.</dd>`,
		"Angelegt von Mama am", `href="/contacts/`+id+`/edit">Kontakt ändern</a>`, `<details class="danger">`, "Kontakt löschen", "Ja, „Oma Gerda“ löschen")
	ctHasNot(t, "contact page", de, " Jahre", "Noch keine Nummer")
	f2 := contactForm("Opa Heinz", "", nil, nil)
	f2.Set("bday_day", "2")
	f2.Set("bday_month", "9")
	f2.Set("bday_year", "1950")
	id2 := c.add(t, f2)
	page := c.page("/contacts/"+id2, "de").body
	ctHas(t, "contact with a year", page, "<dd>02.09.1950 · 76 Jahre</dd>", "<dt>Telefon</dt><dd class=\"none\">Noch keine Nummer</dd>")
	ctHas(t, "English", c.page("/contacts/"+id2, "en").body, "<dd>02.09.1950 · 76 years</dd>", "No number yet")
}

func TestAPersonsContactShowsTheNameFromThePersonAndHasNoDeleteBox(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	mia := c.addPerson(t, "Mia", "child")
	page := c.page("/contacts/"+mia, "de").body
	ctHas(t, "Mia's contact", page, `<h1><span class="marker"`, "Mia</h1>", `class="why fam"`, "Gehört zu eurer Familie.", `href="/people/`+mia+`">Personenseite</a>`, `<span class="tag grp">Familie</span>`, "Kontakt ändern")
	ctHasNot(t, "Mia's contact", page, "Kontakt löschen", `class="danger"`)
	if r := c.send("/contacts/"+mia+"/delete", url.Values{}); r.status != http.StatusUnprocessableEntity {
		t.Errorf("deleting a person's contact = %d, want 422", r.status)
	}
	form := c.page("/contacts/"+mia+"/edit", "de").body
	ctHas(t, "Mia's form", form, `class="lab-static"`, `class="invitee"`, `Personenseite von Mia</a>`, `<span class="pchip plain static">Familie`)
	ctHasNot(t, "Mia's form", form, `name="name"`)
	// Saving keeps the name and Familie even if the form is tampered with.
	f := contactForm("Mia Schulz", "Mia Maier", [][3]string{{"cell", "", "0151 1234567"}}, nil)
	if r := c.send("/contacts/"+mia+"/edit", f); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	_, got, _ := c.api.call(http.MethodGet, "/contacts/"+mia, nil)
	if got["name"] != "Mia" || got["full_name"] != "Mia Maier" || len(got["groups"].([]any)) != 1 {
		t.Errorf("contact = %v", got)
	}
}

// addPerson adds a person and returns the id, which is the id of their contact too.
func (c ctBox) addPerson(t *testing.T, name, role string) string {
	t.Helper()
	return c.peopleBox.add(t, name, role)["id"].(string)
}

func TestPersonPageHasAContactDataRowThatOpensTheContact(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	mia := c.addPerson(t, "Mia", "child")
	page := c.page("/people/"+mia, "de").body
	ctHas(t, "empty row", page, `<h2 class="rows-head">Kontaktdaten</h2>`, `href="/contacts/`+mia+`"`, "Noch nichts eingetragen", "Tippe hier für Telefonnummer, E-Mail-Adresse, Adresse und Geburtstag.")
	if strings.Index(page, "Kontaktdaten") > strings.Index(page, "Essen und Allergien") {
		t.Error("the row is not before the food row")
	}
	f := contactForm("Mia", "Mia Maier", nil, nil)
	f.Set("bday_day", "21")
	f.Set("bday_month", "6")
	f.Set("bday_year", "2018")
	c.send("/contacts/"+mia+"/edit", f)
	ctHas(t, "filled row", c.page("/people/"+mia, "de").body, "<b>Mia Maier</b>", `<span class="meta">Keine Telefonnummer</span>`, "Geburtstag 21.06.2018 · 8 Jahre")
}

func TestRenamingAPersonRenamesTheContact(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	mia := c.addPerson(t, "Mia", "child")
	if r := c.save(mia, url.Values{"name": {"Mia Maier"}}); r.status != http.StatusSeeOther {
		t.Fatalf("rename = %d\n%s", r.status, r.body)
	}
	ctHas(t, "overview", c.page("/contacts", "de").body, "<span>Mia Maier</span>")
}

func TestContactFormShowsErrorsAtTheirFieldsAndKeepsEverythingTyped(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.addPerson(t, "Mia", "child")
	f := contactForm("Mia", "Mia Schulz",
		[][3]string{{"cell", "", "0172 4455 6677"}, {"home", "", "089 23x4 5678"}, {"own", "", "0160 2223 334"}},
		[][3]string{{"home", "", "familie.schulz@example"}})
	f.Set("bday_day", "31")
	f.Set("bday_month", "2")
	f.Set("bday_year", "2018")
	r := c.send("/contacts/new", f)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", r.status)
	}
	ctHas(t, "form with errors", r.body,
		`class="error form-error" role="alert"`, "Not saved. Please check these 5 items:", `<a href="#name">Name in Hejmo</a>`, `<a href="#number2">Number 2</a>`, `<a href="#number3">Number 3</a>`, `<a href="#email1">E-mail address 1</a>`, `<a href="#birthday">Birthday</a>`,
		`id="err-name"`, "“Mia” exists already: Mia from your family.", "“Mia (daycare)” or “Mia Schulz”",
		`id="err-number2"`, "There is a “x” here.", `id="err-numberown3"`, "Write a label",
		`id="err-email1"`, "After the @ the dot and the ending are missing", `id="err-birthday"`, "There is no February 31.",
		`value="Mia Schulz"`, `value="0172 4455 6677"`, `value="089 23x4 5678"`, `value="0160 2223 334"`, `value="familie.schulz@example"`, `value="31"`, `<option value="2" selected>February</option>`, `value="2018"`)
	ctHas(t, "ids", r.body, `<li class="line" id="number1">`, `<li class="line invalid" id="number2">`, `id="number3"`, `id="email1"`, `id="birthday"`, `id="name"`)
	// Nothing was stored.
	if _, _, list := c.api.call(http.MethodGet, "/contacts?q=Schulz", nil); len(list) != 0 {
		t.Errorf("a refused contact was stored: %v", list)
	}
}

func TestContactFormRulesOfNameNumberEmailAndBirthday(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.add(t, contactForm("Kinderarzt", "", nil, nil))
	bad := func(what string, f url.Values, want ...string) {
		t.Helper()
		r := c.send("/contacts/new", f)
		if r.status != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want 422", what, r.status)
			return
		}
		ctHas(t, what, r.body, want...)
	}
	bad("name taken by a contact, ignoring case", contactForm("KINDERARZT", "", nil, nil), "exists already as the contact “Kinderarzt”")
	bad("empty name", contactForm("  ", "", nil, nil), "Write the name Hejmo should show")
	bad("41 characters", contactForm(strings.Repeat("a", 41), "", nil, nil), "40 characters at most")
	bad("plus inside", contactForm("A", "", [][3]string{{"cell", "", "089 +12"}}, nil), "There is a “+” here.")
	bad("two digits", contactForm("A", "", [][3]string{{"cell", "", "12"}}, nil), "at least 3 digits")
	bad("no at", contactForm("A", "", nil, [][3]string{{"home", "", "nodomain"}}), "exactly one @")
	f := contactForm("A", "", nil, nil)
	f.Set("bday_day", "14")
	bad("day without month", f, "Give day and month together")
	f.Set("bday_month", "3")
	f.Set("bday_year", "1899")
	bad("year 1899", f, "The year is between 1900 and 2026")
	f.Set("bday_year", "2999")
	bad("future year", f, "The year is between 1900 and 2026")
	f.Set("bday_day", "29")
	f.Set("bday_month", "2")
	f.Set("bday_year", "2025")
	bad("29 February in a common year", f, "There is no February 29, 2025.")
	f.Set("bday_year", "")
	if r := c.send("/contacts/new", f); r.status != http.StatusSeeOther {
		t.Errorf("29 February without a year = %d, want it saved", r.status)
	}
	long := contactForm("B", "", nil, nil)
	long.Set("note", strings.Repeat("n", 1001))
	bad("long note", long, "1000 characters at most")
	many := contactForm("C", "", nil, nil)
	for i := 0; i < 11; i++ {
		many.Add("number_label", "cell")
		many.Add("number_own", "")
		many.Add("number", "0171 000")
	}
	bad("eleven numbers", many, "More than 10 numbers")
}

func TestWithoutJavaScriptMoreAndRemoveReShowTheFormWithEverythingKept(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	f := contactForm("Oma Gerda", "", [][3]string{{"cell", "", "0171 1"}, {"home", "", "0172 2"}}, nil)
	f.Set("more", "number")
	r := c.send("/contacts/new", f)
	if r.status != http.StatusOK {
		t.Fatalf("more = %d", r.status)
	}
	ctHas(t, "after more", r.body, `value="Oma Gerda"`, `value="0171 1"`, `value="0172 2"`, `id="number3"`)
	if n := strings.Count(r.body, `<li class="line`); n != 3+1 { // three numbers and the one e-mail line; the templates are inert but counted: see below
		// The two <template> lines are in the page too.
		if n != 3+1+2 {
			t.Errorf("%d lines", n)
		}
	}
	f2 := contactForm("Oma Gerda", "", [][3]string{{"cell", "", "0171 1"}, {"home", "", "0172 2"}, {"work", "", "0173 3"}}, nil)
	f2.Set("remove", "number2")
	r = c.send("/contacts/new", f2)
	ctHas(t, "after remove", r.body, `value="0171 1"`, `value="0173 3"`)
	ctHasNot(t, "after remove", r.body, `value="0172 2"`)
	if _, _, list := c.api.call(http.MethodGet, "/contacts?q=Oma", nil); len(list) != 0 {
		t.Error("the buttons saved the contact")
	}
}

func TestEditFormShowsSavedOwnLabelsAsOptionsAndSavesThemAgain(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	id := c.add(t, contactForm("Oma Gerda", "", [][3]string{{"own", "Gartenhaus", "0160 111 2223"}}, nil))
	form := c.page("/contacts/"+id+"/edit", "de").body
	ctHas(t, "edit form", form, `<option value="label:Gartenhaus" selected>Gartenhaus</option>`, `<option value="own">Eigene Bezeichnung …</option>`, `value="0160 111 2223"`, `<span class="dirty" hidden>Noch nicht gespeichert</span>`, `class="steps ct-form"`)
	f := contactForm("Oma Gerda", "", [][3]string{{"label:Gartenhaus", "", "0160 111 2223"}}, nil)
	if r := c.send("/contacts/"+id+"/edit", f); r.status != http.StatusSeeOther || r.location != "/contacts/"+id {
		t.Fatalf("save = %d %q\n%s", r.status, r.location, r.body)
	}
	ctHas(t, "after save", c.page("/contacts/"+id, "de").body, "„Oma Gerda“ gespeichert.", `<dt class="l">Gartenhaus</dt>`)
}

func TestDeletingAContactShowsTheLineAndRemovesIt(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	id := c.add(t, contactForm("Kita Sonnenschein", "", nil, nil))
	r := c.send("/contacts/"+id+"/delete", url.Values{})
	if r.status != http.StatusSeeOther || r.location != "/contacts" {
		t.Fatalf("delete = %d %q", r.status, r.location)
	}
	ctHas(t, "after delete", c.page("/contacts", "de").body, "„Kita Sonnenschein“ gelöscht.")
	if r := c.page("/contacts/"+id, "de"); r.status != http.StatusNotFound {
		t.Errorf("deleted contact = %d", r.status)
	}
}

func TestContactGroupsAreMadeInTheFormRenamedAndDeletedWhileContactsStay(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	f := contactForm("Anna (Mama von Lukas)", "Anna Schulz", nil, nil)
	f.Set("new_group", "Fußball-Eltern U11")
	id := c.add(t, f)
	page := c.page("/contacts", "de").body
	ctHas(t, "overview", page, `<span class="tag grp">Fußball-Eltern U11</span>`, ">Fußball-Eltern U11</a>")
	list := c.page("/contacts/groups", "de").body
	ctHas(t, "groups", list, "<h1>Kontaktgruppen</h1>", "Eigene Gruppen", "Anna (Mama von Lukas)", "Diese fünf bleiben immer da: Familie, Kinderbetreuung und Schule, Arztpraxen, Notfall, Sonstige.", "Gruppe anlegen")
	gid := regexp.MustCompile(`/contacts/groups/([0-9a-f-]{36})`).FindStringSubmatch(list)[1]
	// A duplicate is an error at the field, also against the groups of Hejmo and in the other language.
	for _, name := range []string{"fußball-eltern u11", "Familie", "Family"} {
		r := c.send("/contacts/groups", url.Values{"name": {name}})
		if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-name"`) || !strings.Contains(r.body, "exists already") {
			t.Errorf("group %q = %d\n%s", name, r.status, r.body)
		}
	}
	if r := c.send("/contacts/groups", url.Values{"name": {"Nachbarschaft Erlenweg"}}); r.status != http.StatusSeeOther || r.location != "/contacts/groups" {
		t.Fatalf("create group = %d %q", r.status, r.location)
	}
	ctHas(t, "after create", c.page("/contacts/groups", "de").body, "Gruppe „Nachbarschaft Erlenweg“ angelegt.", "Noch kein Kontakt")
	page = c.page("/contacts/groups/"+gid, "de").body
	ctHas(t, "group page", page, "<h1>Fußball-Eltern U11</h1>", `href="/contacts/`+id+`">Anna (Mama von Lukas)</a>`, "Name ändern", `<details class="danger">`, "Gruppe löschen", "<li><strong>Anna (Mama von Lukas)</strong></li>", "Ja, „Fußball-Eltern U11“ löschen")
	if r := c.send("/contacts/groups/"+gid, url.Values{"name": {"Fußball U12"}}); r.status != http.StatusSeeOther {
		t.Fatalf("rename = %d\n%s", r.status, r.body)
	}
	if r := c.send("/contacts/groups/"+gid+"/delete", url.Values{}); r.status != http.StatusSeeOther || r.location != "/contacts/groups" {
		t.Fatalf("delete group = %d %q", r.status, r.location)
	}
	ctHas(t, "after delete", c.page("/contacts/groups", "de").body, "Gruppe „Fußball U12“ gelöscht.")
	_, got, _ := c.api.call(http.MethodGet, "/contacts/"+id, nil)
	if got == nil || len(got["groups"].([]any)) != 0 {
		t.Errorf("the contact after the group went: %v", got)
	}
	// The groups of Hejmo have no page of their own and cannot be deleted.
	_, _, groups := c.api.call(http.MethodGet, "/contact-groups", nil)
	fam := groups[0].(map[string]any)["id"].(string)
	if r := c.page("/contacts/groups/"+fam, "de"); r.status != http.StatusNotFound {
		t.Errorf("page of a group of Hejmo = %d", r.status)
	}
	if r := c.send("/contacts/groups/"+fam+"/delete", url.Values{}); r.status != http.StatusNotFound {
		t.Errorf("deleting a group of Hejmo = %d", r.status)
	}
}

func TestTrailsOfTheContactPages(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	id := c.add(t, contactForm("Oma Gerda", "", nil, nil))
	_, _, groups := c.api.call(http.MethodPost, "/contact-groups", map[string]any{"name": "Nachbarschaft"})
	_ = groups
	_, g, _ := c.api.call(http.MethodPost, "/contact-groups", map[string]any{"name": "Fußball"})
	settings, contacts := part("Settings", "/settings"), part("Contacts", "/contacts")
	for _, tc := range []struct {
		path string
		want []crumbPart
	}{
		{"/contacts", []crumbPart{settings, part("Contacts", "")}},
		{"/contacts/" + id, []crumbPart{settings, contacts, part("Oma Gerda", "")}},
		{"/contacts/" + id + "/edit", []crumbPart{settings, contacts, part("Oma Gerda", "/contacts/"+id), part("Change contact", "")}},
		{"/contacts/new", []crumbPart{settings, contacts, part("New contact", "")}},
		{"/contacts/groups", []crumbPart{settings, contacts, part("Contact groups", "")}},
		{"/contacts/groups/" + g["id"].(string), []crumbPart{settings, contacts, part("Contact groups", "/contacts/groups"), part("Fußball", "")}},
	} {
		wantTrail(t, tc.path, c.page(tc.path, "en").body, tc.want...)
	}
}

// Acceptance: a group that vanished while the form was open (or never existed) is no reason to lose what was typed.
func TestAGroupThatVanishedWhileTheFormWasOpenShowsTheFormAgainWithEverythingKept(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	f := contactForm("Kita Sonnenschein", "", [][3]string{{"cell", "", "0172 4455 6677"}}, nil)
	f.Set("note", "Abholung bis 16 Uhr")
	f.Add("group", "00000000-0000-0000-0000-000000000000")
	r := c.send("/contacts/new", f)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422\n%s", r.status, r.body)
	}
	ctHas(t, "form", r.body, `<a href="#groups">`, "no longer exists", `value="Kita Sonnenschein"`, `value="0172 4455 6677"`, "Abholung bis 16 Uhr")
	if _, _, list := c.api.call(http.MethodGet, "/contacts?q=Sonnenschein", nil); len(list) != 0 {
		t.Errorf("a refused contact was stored: %v", list)
	}
}

// raceOnce runs f once, when the contact form has been checked and is about to be saved: a write by
// someone else between the form's check and the save, with no seam in production code. The clock is
// read there for the household's today (contacts_form.go), after the groups were loaded and checked.
func (c ctBox) raceOnce(f func()) {
	done := false
	c.nowHook = func(file string) {
		if file == "contacts_form.go" && !done {
			done = true
			f()
		}
	}
}

func TestAGroupDeletedBetweenTheCheckAndTheSaveIsGoneFromTheFormAndUntickedThere(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	if r := c.send("/contacts/groups", url.Values{"name": {"Nachbarschaft Erlenweg"}}); r.status != http.StatusSeeOther {
		t.Fatalf("make group = %d", r.status)
	}
	var gid string
	if err := c.db.SQL().QueryRow(`SELECT id FROM contact_groups WHERE name = 'Nachbarschaft Erlenweg'`).Scan(&gid); err != nil {
		t.Fatal(err)
	}
	c.raceOnce(func() {
		if _, err := c.db.SQL().Exec(`DELETE FROM contact_groups WHERE id = ?`, gid); err != nil {
			t.Error(err)
		}
	})
	f := contactForm("Kita Sonnenschein", "", nil, nil)
	f.Add("group", gid)
	r := c.send("/contacts/new", f)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422\n%s", r.status, r.body)
	}
	ctHas(t, "form", r.body, "no longer exists", `value="Kita Sonnenschein"`)
	if strings.Contains(r.body, "Nachbarschaft Erlenweg") || strings.Contains(r.body, gid) {
		t.Errorf("the deleted group is still offered or ticked in the form\n%s", r.body)
	}
}

func TestAGroupMadeBetweenTheCheckAndTheSaveShowsAnErrorAtTheNewGroupField(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.raceOnce(func() {
		if _, err := c.db.SQL().Exec(`INSERT INTO contact_groups (id, household_id, name, created_at) SELECT '11111111-1111-1111-1111-111111111111', id, 'Fußball-Eltern', 'now' FROM households`); err != nil {
			t.Error(err)
		}
	})
	f := contactForm("Kita Sonnenschein", "", nil, nil)
	f.Set("new_group", "fußball-eltern")
	r := c.send("/contacts/new", f)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422\n%s", r.status, r.body)
	}
	ctHas(t, "form", r.body, `id="err-new_group"`, "exists already", `value="Kita Sonnenschein"`)
}
