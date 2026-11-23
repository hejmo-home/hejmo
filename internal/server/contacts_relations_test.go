package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Ticket 298 (ADR 0021, preview-298.html): relations between contacts and people.

type relLine struct {
	word   string
	whole  bool
	people []string
}

// withRels adds the relation lines of the form: rel1_word, rel1_whole and rel1_person for each line.
func withRels(f url.Values, lines ...relLine) url.Values {
	for i, l := range lines {
		n := "rel" + string(rune('1'+i))
		f.Set(n+"_word", l.word)
		if l.whole {
			f.Set(n+"_whole", "1")
		}
		for _, p := range l.people {
			f.Add(n+"_person", p)
		}
	}
	return f
}

func (c ctBox) personID(t *testing.T, name string) string {
	t.Helper()
	return c.peopleBox.byName(t, name)["id"].(string)
}

func (c ctBox) groupID(t *testing.T, key string) string {
	t.Helper()
	_, _, list := c.api.call(http.MethodGet, "/contact-groups", nil)
	for _, g := range list {
		if m := g.(map[string]any); m["shipped_key"] == key {
			return m["id"].(string)
		}
	}
	t.Fatalf("no group %s", key)
	return ""
}

// family adds the example family of the preview: Mama exists, then Papa, Sebastian, Mia, Lotta and Ben.
func (c ctBox) family(t *testing.T) map[string]string {
	t.Helper()
	ids := map[string]string{"Mama": c.personID(t, "Mama")}
	for _, p := range [][2]string{{"Papa", "parent"}, {"Sebastian", "child"}, {"Mia", "child"}, {"Lotta", "child"}, {"Ben", "child"}} {
		ids[p[0]] = c.addPerson(t, p[0], p[1])
	}
	return ids
}

func section(page, from, to string) string {
	a := strings.Index(page, from)
	if a < 0 {
		return ""
	}
	b := strings.Index(page[a:], to)
	if b < 0 {
		return page[a:]
	}
	return page[a : a+b]
}

func TestTheContactFormHasARelationsCardAfterNameWithOneBlankLineForTheWholeFamily(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.family(t)
	page := c.page("/contacts/new", "de").body
	ctHas(t, "new form", page, `<fieldset id="rels" class="rels-card" data-of="von" data-whole="der ganzen Familie" data-and="und">`, `Beziehungen <span class="tag hidden-tag">freiwillig</span>`,
		`<li class="rel-line blank" id="rel1">`, `name="rel1_word" type="text" list="rel-words" maxlength="40"`, `<legend>Für wen?</legend>`,
		`name="rel1_whole" value="1" data-whole checked`, `<label class="pchip" data-person="1"><input type="checkbox" name="rel1_person"`,
		"Weitere Beziehung", "Kinder sehen den Kontakt dadurch nicht. Was Kinder sehen, legt ihr extra fest.",
		`<datalist id="rel-words">`, `<option value="Oma">`, `<option value="Kinderärztin">`, `<template data-line="rel">`, `name="more" value="rel"`)
	name, rels, numbers := strings.Index(page, `id="name"`), strings.Index(page, `id="rels"`), strings.Index(page, `id="numbers"`)
	if !(name > 0 && name < rels && rels < numbers) {
		t.Errorf("the card is not after Name and before the numbers: %d %d %d", name, rels, numbers)
	}
	if line := section(page, `<li class="rel-line blank"`, `</li>`); strings.Contains(line, `rm"`) || !strings.Contains(line, "rel-say") {
		t.Errorf("the blank line has an Entfernen button or no sentence element: %s", line)
	}
	ctHas(t, "English", c.page("/contacts/new", "en").body, "Relations", `<legend>For whom?</legend>`, "Another relation", "Children do not see the contact because of this.", `<option value="Grandma">`)
}

func TestTheDatalistOffersTheWordsTheHouseholdUsesAndAPersonsContactHasNoRelationsCard(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.add(t, withRels(contactForm("Frau Schmidt", "", nil, nil), relLine{word: "Klassenpatin", whole: true}))
	ctHas(t, "datalist", c.page("/contacts/new", "de").body, `<option value="Klassenpatin">`, `<option value="Oma">`)
	mama := c.personID(t, "Mama")
	page := c.page("/contacts/"+mama+"/edit", "de").body
	ctHasNot(t, "a person's contact form", page, `id="rels"`, "rel-lines", "Beziehungen")
}

func TestRelationsRoundTripThroughTheFormWithTwoLinesAndShowOnTheContactAndItsRow(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	ids := c.family(t)
	id := c.add(t, withRels(contactForm("Oma Gerda", "Gerda Müller", nil, nil),
		relLine{word: "Oma", people: []string{ids["Mia"], ids["Sebastian"]}}, relLine{word: " Mutter ", people: []string{ids["Papa"]}}))
	edit := c.page("/contacts/"+id+"/edit", "de").body
	ctHas(t, "edit form", edit, `<li class="rel-line" id="rel1">`, `<li class="rel-line" id="rel2">`, `name="rel1_word" type="text" list="rel-words" maxlength="40" autocomplete="off" value="Oma"`,
		`name="rel2_word" type="text" list="rel-words" maxlength="40" autocomplete="off" value="Mutter"`,
		`name="rel1_person" value="`+ids["Sebastian"]+`" checked`, `name="rel1_person" value="`+ids["Mia"]+`" checked`, `name="rel2_person" value="`+ids["Papa"]+`" checked`,
		`So steht es bei dem Kontakt: <b>Oma von Sebastian und Mia</b>`, `So steht es bei dem Kontakt: <b>Mutter von Papa</b>`,
		`name="remove" value="rel1"`, `aria-label="Beziehung 2 entfernen"`)
	ctHasNot(t, "edit form", edit, `name="rel1_whole" value="1" data-whole checked`, `name="rel1_person" value="`+ids["Papa"]+`" checked`)
	page := c.page("/contacts/"+id, "de").body
	head := section(page, `<div class="ct-head">`, `</div>`)
	ctHas(t, "contact head", head, `<h1>Oma Gerda</h1>`, `<p class="ct-full">Gerda Müller</p>`, `<ul class="ct-rels" aria-label="Beziehungen">`, `<li><b>Oma</b> von <span class="who"><span class="marker"`, `Sebastian</span> und <span class="who"><span class="marker"`, `Mia</span></li>`, `<li><b>Mutter</b> von <span class="who">`)
	if strings.Index(head, "ct-full") > strings.Index(head, "ct-rels") {
		t.Errorf("the relations are not after the full name: %s", head)
	}
	row := rowOf(c.page("/contacts", "de").body, "Oma Gerda")
	ctHas(t, "row", row, `<span class="meta rel">Oma von Sebastian und Mia</span><span class="meta rel">Mutter von Papa</span><span class="meta">Gerda Müller</span>`)
	ctHasNot(t, "row", row, `class="who"`, "<svg class=\"icon\" width=\"16\"")
	// Saving the form again changes nothing.
	f := withRels(contactForm("Oma Gerda", "Gerda Müller", nil, nil), relLine{word: "Oma", people: []string{ids["Sebastian"], ids["Mia"]}}, relLine{word: "Mutter", people: []string{ids["Papa"]}})
	if r := c.send("/contacts/"+id+"/edit", f); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	ctHas(t, "after saving", rowOf(c.page("/contacts", "de").body, "Oma Gerda"), `Oma von Sebastian und Mia</span><span class="meta rel">Mutter von Papa`)
}

func TestTheSentenceNamesEveryoneInTheOrderOfThePeopleAndThereIsOneForTheWholeFamily(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	ids := c.family(t)
	c.add(t, withRels(contactForm("Oma Gerda", "", nil, nil), relLine{word: "Oma", people: []string{ids["Ben"], ids["Lotta"], ids["Mia"], ids["Sebastian"], ids["Papa"], ids["Mama"]}}))
	c.add(t, withRels(contactForm("Frau Schmidt", "", nil, nil), relLine{word: "Lehrerin", people: []string{ids["Mia"]}}))
	c.add(t, withRels(contactForm("Tante Steffi", "", nil, nil), relLine{word: "Tante", people: []string{ids["Mia"], ids["Ben"]}}))
	c.add(t, withRels(contactForm("Frau Wöllenstein", "", nil, nil), relLine{word: "Nachbarin", whole: true}))
	de, en := c.page("/contacts", "de").body, c.page("/contacts", "en").body
	ctHas(t, "6 names", rowOf(de, "Oma Gerda"), `Oma von Mama, Papa, Sebastian, Mia, Lotta und Ben</span>`)
	ctHas(t, "1 name", rowOf(de, "Frau Schmidt"), `Lehrerin von Mia</span>`)
	ctHas(t, "2 names", rowOf(de, "Tante Steffi"), `Tante von Mia und Ben</span>`)
	ctHas(t, "whole family", rowOf(de, "Frau Wöllenstein"), `Nachbarin der ganzen Familie</span>`)
	ctHas(t, "English 6", rowOf(en, "Oma Gerda"), `Oma of Mama, Papa, Sebastian, Mia, Lotta and Ben</span>`)
	ctHas(t, "English 2", rowOf(en, "Tante Steffi"), `Tante of Mia and Ben</span>`)
	ctHas(t, "English whole", rowOf(en, "Frau Wöllenstein"), `Nachbarin of the whole family</span>`)
}

func TestTheWholeFamilyRelationShowsAsASentenceWithAHouseOnTheContactPage(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	id := c.add(t, withRels(contactForm("Frau Wöllenstein", "Hannelore Wöllenstein", nil, nil), relLine{word: "Nachbarin", whole: true}))
	page := c.page("/contacts/"+id, "de").body
	ctHas(t, "contact head", section(page, `<ul class="ct-rels"`, `</ul>`), `<li><svg class="icon"`, `<b>Nachbarin</b> der ganzen Familie</li>`)
	ctHasNot(t, "contact head", section(page, `<ul class="ct-rels"`, `</ul>`), `class="who"`)
	edit := c.page("/contacts/"+id+"/edit", "de").body
	ctHas(t, "edit", edit, `name="rel1_whole" value="1" data-whole checked`, `<b>Nachbarin der ganzen Familie</b>`)
	ctHasNot(t, "edit", edit, `class="rel-line blank"`)
}

func TestABlankWholeFamilyLineIsSkippedAndAContactWithoutRelationsShowsNone(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.family(t)
	id := c.add(t, withRels(contactForm("Kinderarzt", "", nil, nil), relLine{whole: true}, relLine{word: "  ", whole: true}))
	page := c.page("/contacts/"+id, "de").body
	ctHasNot(t, "contact page", page, "ct-rels")
	ctHasNot(t, "row", rowOf(c.page("/contacts", "de").body, "Kinderarzt"), "meta rel")
	ctHas(t, "edit form", c.page("/contacts/"+id+"/edit", "de").body, `<li class="rel-line blank" id="rel1">`)
}

func TestPeopleChosenWithoutAWordIsAnErrorAtTheLineAndEverythingTypedStays(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	ids := c.family(t)
	f := withRels(contactForm("Oma Gerda", "Gerda Müller", [][3]string{{"cell", "", "0171 3456 789"}}, nil),
		relLine{word: "Oma", people: []string{ids["Sebastian"]}}, relLine{word: "", people: []string{ids["Mama"]}}, relLine{word: " ", people: []string{ids["Mia"], ids["Ben"]}})
	r := c.do(http.MethodPost, "/contacts/new", f, map[string]string{"Accept-Language": "de"})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d\n%s", r.status, r.body)
	}
	ctHas(t, "error page", r.body, `<a href="#rel2">Beziehung 2</a>`, `<a href="#rel3">Beziehung 3</a>`,
		`<p class="ferr" id="err-rel2" role="alert">`, `Schreib, wer „Oma Gerda“ für Mama ist, zum Beispiel „Freundin“.`, `Schreib, wer „Oma Gerda“ für Mia und Ben ist, zum Beispiel „Freundin“.`,
		`value="Oma Gerda"`, `value="Gerda Müller"`, `value="0171 3456 789"`, `name="rel1_word" type="text" list="rel-words" maxlength="40" autocomplete="off" value="Oma"`,
		`name="rel2_person" value="`+ids["Mama"]+`" checked`, `name="rel3_person" value="`+ids["Ben"]+`" checked`, `<div class="f invalid">`)
	ctHasNot(t, "error page", r.body, `id="err-rel1"`)
	if strings.Contains(c.page("/contacts", "de").body, "Oma Gerda") {
		t.Error("the contact was saved")
	}
}

func TestAWordOfMoreThan40CharactersIsRefusedAtTheLine(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	r := c.send("/contacts/new", withRels(contactForm("Oma Gerda", "", nil, nil), relLine{word: strings.Repeat("ä", 41), whole: true}))
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("41 characters = %d", r.status)
	}
	ctHas(t, "error", r.body, `id="err-rel1"`, `<a href="#rel1">Relation 1</a>`, "The word can be at most 40 characters long.")
	if r := c.send("/contacts/new", withRels(contactForm("Oma Gerda", "", nil, nil), relLine{word: strings.Repeat("ä", 40), whole: true})); r.status != http.StatusSeeOther {
		t.Errorf("40 characters = %d", r.status)
	}
}

func TestTheSameWordForTheSamePeopleIsMergedInSilence(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	ids := c.family(t)
	c.add(t, withRels(contactForm("Frau Schmidt", "", nil, nil),
		relLine{word: "Lehrerin", people: []string{ids["Mia"], ids["Ben"]}}, relLine{word: "lehrerin", people: []string{ids["Ben"], ids["Mia"]}}, relLine{word: "Lehrerin", people: []string{ids["Mia"]}}))
	row := rowOf(c.page("/contacts", "de").body, "Frau Schmidt")
	if n := strings.Count(row, `class="meta rel"`); n != 2 {
		t.Errorf("%d relations, want 2\n%s", n, row)
	}
	ctHas(t, "row", row, `Lehrerin von Mia und Ben</span><span class="meta rel">Lehrerin von Mia</span>`)
}

func TestWithoutJavaScriptMoreAddsALineAndRemoveTakesOneAwayKeepingWhatWasTyped(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	ids := c.family(t)
	f := withRels(contactForm("Oma Gerda", "Gerda", nil, nil), relLine{word: "Oma", people: []string{ids["Mia"]}})
	f.Set("more", "rel")
	r := c.send("/contacts/new", f)
	if r.status != http.StatusOK {
		t.Fatalf("more = %d", r.status)
	}
	ctHas(t, "two lines", r.body, `id="rel1"`, `id="rel2"`, `value="Oma"`, `value="Gerda"`, `name="rel2_whole" value="1" data-whole checked`)
	f = withRels(contactForm("Oma Gerda", "Gerda", nil, nil), relLine{word: "Oma", people: []string{ids["Mia"]}}, relLine{word: "Mutter", people: []string{ids["Papa"]}})
	f.Set("remove", "rel1")
	r = c.send("/contacts/new", f)
	ctHas(t, "one line", r.body, `value="Mutter"`, `id="rel1"`)
	ctHasNot(t, "one line", r.body, `value="Oma"`, `id="rel2"`)
}

func TestAWholeFamilyRelationIncludesAPersonAddedLater(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.add(t, withRels(contactForm("Frau Wöllenstein", "", [][3]string{{"home", "", "08031 22 33 44"}}, nil), relLine{word: "Nachbarin", whole: true}))
	lotta := c.addPerson(t, "Lotta", "child")
	page := c.page("/people/"+lotta, "de").body
	ctHas(t, "Lotta's page", page, `<h2 class="rows-head" id="contacts">Kontakte von Lotta</h2>`, `<b>Frau Wöllenstein</b>`, `<span class="meta rel">Nachbarin der ganzen Familie</span>`)
}

// Acceptance: relations shown on the person's page and filterable.
func TestThePersonPageListsTheContactsOfThePersonAtoZWithTheirOwnRelationAndFiltersByTheGroupsThatOccur(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	ids := c.family(t)
	school, doctor, other, fam := c.groupID(t, "kinderbetreuung_schule"), c.groupID(t, "arztpraxen"), c.groupID(t, "sonstige"), c.groupID(t, "familie")
	mk := func(name, full, num string, group string, rels ...relLine) string {
		f := withRels(contactForm(name, full, [][3]string{{"cell", "", num}}, nil), rels...)
		f.Set("group", group)
		return c.add(t, f)
	}
	mk("Frau Schmidt", "Birgit Schmidt", "089 2233 4455", school, relLine{word: "Klassenlehrerin", people: []string{ids["Mia"]}})
	mk("Kinderarzt", "Dr. Yılmaz", "089 1234 5670", doctor, relLine{word: "Kinderärztin", people: []string{ids["Mia"], ids["Lotta"]}})
	mk("Frau Wöllenstein", "", "08031 22 33 44", other, relLine{word: "Nachbarin", whole: true})
	mk("Oma Gerda", "Gerda Müller", "0171 3456 789", fam, relLine{word: "Oma", people: []string{ids["Mia"]}}, relLine{word: "Mutter", people: []string{ids["Papa"]}})
	mk("Opa Heinz", "", "08031 87 65 43", fam, relLine{word: "Opa", people: []string{ids["Ben"]}})
	c.page("/contacts", "de") // takes the success line of the last save
	page := c.page("/people/"+ids["Mia"], "de").body
	ctHas(t, "Mia's page", page, `<h2 class="rows-head" id="contacts">Kontakte von Mia</h2>`, `<p class="lead-s">Alle mit einer Beziehung zu Mia. Nur Eltern sehen diese Liste.</p>`,
		`<nav class="lib-filter ct-filter pc-filter" aria-label="Kontakte von Mia nach Gruppe">`, `aria-current="true">Alle</a>`,
		`href="?g=`+school+`#contacts">Kinderbetreuung und Schule</a>`, `href="?g=`+doctor+`#contacts">Arztpraxen</a>`, `href="?g=`+other+`#contacts">Sonstige</a>`, `href="?g=`+fam+`#contacts">Familie</a>`,
		`<span class="meta rel">Klassenlehrerin von Mia</span><span class="meta">Birgit Schmidt</span>`, `<span class="lab">Mobil</span> 089 2233 4455`, `<span class="tag grp">Kinderbetreuung und Schule</span>`,
		`<span class="meta rel">Kinderärztin von Mia</span>`, `<span class="meta rel">Nachbarin der ganzen Familie</span>`, `<span class="meta rel">Oma von Mia</span>`,
		`<p class="pc-actions"><a class="button secondary" href="/contacts/new?for=`+ids["Mia"]+`">`, `Kontakt für Mia anlegen</a><a href="/contacts">Alle Kontakte</a></p>`)
	ctHasNot(t, "Mia's page", page, "Opa Heinz", "Mutter von Papa", "Kinderärztin von Mia und Lotta", ">Notfall</a>")
	list := section(page, `id="contacts"`, `pc-actions`)
	at := []int{strings.Index(list, "<b>Frau Schmidt</b>"), strings.Index(list, "<b>Frau Wöllenstein</b>"), strings.Index(list, "<b>Kinderarzt</b>"), strings.Index(list, "<b>Oma Gerda</b>")}
	if !(at[0] > 0 && at[0] < at[1] && at[1] < at[2] && at[2] < at[3]) {
		t.Errorf("the contacts are not A to Z: %v", at)
	}
	filtered := c.page("/people/"+ids["Mia"]+"?g="+doctor, "de").body
	ctHas(t, "filtered", filtered, "<b>Kinderarzt</b>", `href="?g=`+doctor+`#contacts" aria-current="true">Arztpraxen</a>`)
	ctHasNot(t, "filtered", filtered, "<b>Frau Schmidt</b>", "<b>Oma Gerda</b>")
	// A group none of her contacts is in is no filter.
	if p := c.page("/people/"+ids["Mia"]+"?g="+c.groupID(t, "notfall"), "de").body; !strings.Contains(p, "<b>Frau Schmidt</b>") || !strings.Contains(p, "<b>Oma Gerda</b>") {
		t.Error("a group without contacts of Mia filters the list")
	}
	// Ben has the whole-family neighbour and Opa Heinz, in two groups; Lotta only the doctor and the neighbour.
	ctHas(t, "Ben", c.page("/people/"+ids["Ben"], "de").body, "<b>Opa Heinz</b>", `class="lib-filter ct-filter pc-filter"`)
	ctHasNot(t, "Mama's contacts", c.page("/people/"+ids["Mama"], "en").body, "pc-filter", "<b>Opa Heinz</b>") // one group (Sonstige) only
	ctHas(t, "English", c.page("/people/"+ids["Mia"], "en").body, "Contacts of Mia", "Everyone related to Mia. Only parents see this list.", "Create contact for Mia", "All contacts", "Klassenlehrerin of Mia")
}

func TestThePersonPageWithoutRelatedContactsSaysWhatBelongsThereAndOffersTheButton(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	ids := c.family(t)
	c.add(t, withRels(contactForm("Frau Schmidt", "", nil, nil), relLine{word: "Klassenlehrerin", people: []string{ids["Mia"]}}))
	page := c.page("/people/"+ids["Ben"], "de").body
	ctHas(t, "empty", page, `<h2 class="rows-head" id="contacts">Kontakte von Ben</h2>`, `<div class="empty" data-empty>`,
		"Noch kein Kontakt hat eine Beziehung zu Ben. Hier erscheinen zum Beispiel Großeltern, die Kita oder Bens Freunde, sobald du bei einem Kontakt unter „Beziehungen“ Ben wählst.",
		`href="/contacts/new?for=`+ids["Ben"]+`">`, "Kontakt für Ben anlegen", `<a href="/contacts">Alle Kontakte</a>`)
	ctHasNot(t, "empty", page, "pc-filter", "lead-s")
}

func TestNewContactForAPersonHasThePersonChosenAndSavingReturnsToTheirPageWithTheRelationInTheSuccessLine(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	ids := c.family(t)
	form := c.page("/contacts/new?for="+ids["Mia"], "de").body
	ctHas(t, "form", form, "<h1>Neuer Kontakt für Mia</h1>",
		"Nur der Name in Hejmo muss sein. Unter „Beziehungen“ ist Mia schon gewählt; schreib dazu, wer das für Mia ist.",
		`href="/people/`+ids["Mia"]+`">Mia</a>`, `name="rel1_person" value="`+ids["Mia"]+`" checked`, `name="for" value="`+ids["Mia"]+`"`)
	ctHasNot(t, "form", form, `name="rel1_whole" value="1" data-whole checked`, `class="rel-line blank"`)
	f := withRels(contactForm("Frau Schmidt", "", nil, nil), relLine{word: "Klassenlehrerin", people: []string{ids["Mia"]}})
	f.Set("for", ids["Mia"])
	r := c.do(http.MethodPost, "/contacts/new", f, map[string]string{"Accept-Language": "de"})
	if r.status != http.StatusSeeOther || r.location != "/people/"+ids["Mia"]+"#contacts" {
		t.Fatalf("save = %d %q\n%s", r.status, r.location, r.body)
	}
	ctHas(t, "Mia's page", c.page("/people/"+ids["Mia"], "de").body, "„Frau Schmidt“ angelegt, als Klassenlehrerin von Mia.", "<b>Frau Schmidt</b>")
	// Without Mia in any relation the contact is shown as after any other save; an unknown person is ignored.
	f = withRels(contactForm("Kinderarzt", "", nil, nil), relLine{word: "Kinderarzt", people: []string{ids["Ben"]}})
	f.Set("for", ids["Mia"])
	if r := c.send("/contacts/new", f); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/contacts/") {
		t.Errorf("save without Mia = %d %q", r.status, r.location)
	}
	ctHas(t, "unknown person", c.page("/contacts/new?for=00000000-0000-4000-8000-000000000000", "de").body, "<h1>Neuer Kontakt</h1>")
}

func TestTheTrailOfANewContactForAPersonLeadsBackToThePerson(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	ids := c.family(t)
	page := c.page("/contacts/new?for="+ids["Mia"], "de").body
	crumbs := section(page, `<nav class="crumbs"`, `</nav>`)
	ctHas(t, "trail", crumbs, `href="/settings">Einstellungen</a>`, `href="/people">Personen</a>`, `href="/people/`+ids["Mia"]+`">Mia</a>`, "Neuer Kontakt für Mia")
	ctHasNot(t, "trail", crumbs, `href="/contacts"`)
}

func TestTheDeleteBoxNamesTheRelationsThatGoAndTheyGoWithTheContact(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	ids := c.family(t)
	whole := c.add(t, withRels(contactForm("Frau Wöllenstein", "", [][3]string{{"home", "", "08031 22 33 44"}}, nil), relLine{word: "Nachbarin", whole: true}))
	two := c.add(t, withRels(contactForm("Tante Steffi", "", nil, nil), relLine{word: "Tante", people: []string{ids["Mia"], ids["Ben"]}}))
	one := c.add(t, withRels(contactForm("Lea", "", nil, nil), relLine{word: "Freundin", people: []string{ids["Mia"]}}))
	none := c.add(t, contactForm("Kinderarzt", "", nil, nil))
	ctHas(t, "whole", c.page("/contacts/"+whole, "de").body, "„Frau Wöllenstein“ verschwindet aus Hejmo, mit den Nummern, der Notiz und der Beziehung zur ganzen Familie. Das lässt sich nicht rückgängig machen.")
	ctHas(t, "two people", c.page("/contacts/"+two, "de").body, "mit den Nummern, der Notiz und den Beziehungen zu Mia und Ben. Das lässt sich nicht rückgängig machen.")
	ctHas(t, "one person", c.page("/contacts/"+one, "de").body, "mit den Nummern, der Notiz und der Beziehung zu Mia.")
	ctHas(t, "none", c.page("/contacts/"+none, "de").body, "„Kinderarzt“ verschwindet aus Hejmo, mit den Nummern und der Notiz. Das lässt sich nicht rückgängig machen.")
	ctHas(t, "English", c.page("/contacts/"+two, "en").body, "with its numbers, note and relations to Mia and Ben.")
	if r := c.send("/contacts/"+two+"/delete", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("delete = %d", r.status)
	}
	c.page("/contacts", "de") // takes the success line
	page := c.page("/people/"+ids["Ben"], "de").body
	ctHasNot(t, "Ben's page", page, "Tante Steffi")
	ctHas(t, "Ben's page", page, "Frau Wöllenstein")
	if r := c.page("/contacts/"+two, "de"); r.status != http.StatusNotFound {
		t.Errorf("deleted contact = %d", r.status)
	}
}

func TestSearchFindsTheWordOfARelationAndTheFieldSaysSo(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	ids := c.family(t)
	c.add(t, withRels(contactForm("Frau Schmidt", "Birgit", nil, nil), relLine{word: "Klassenlehrerin", people: []string{ids["Mia"]}}))
	c.add(t, contactForm("Kita Sonnenschein", "", nil, nil))
	c.page("/contacts", "de") // takes the success line of the last save
	page := c.page("/contacts?q=lehrerin", "de").body
	ctHas(t, "search", page, "<b>Frau Schmidt</b>")
	ctHasNot(t, "search", page, "Kita Sonnenschein")
	ctHas(t, "placeholder", page, `placeholder="Name, Nummer, E-Mail oder Beziehung"`)
	ctHas(t, "English placeholder", c.page("/contacts?q=x", "en").body, `placeholder="Name, number, e-mail or relation"`)
}

func TestContactsAPIReadsAndWritesRelationsAndChecksThem(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	ids := c.family(t)
	status, got, _ := c.api.call(http.MethodPost, "/contacts", map[string]any{
		"name": "Oma Gerda",
		"relations": []any{
			map[string]any{"word": "Oma", "people": []any{ids["Mia"], ids["Sebastian"]}},
			map[string]any{"word": "Nachbarin", "whole_family": true},
			map[string]any{"word": "oma", "people": []any{ids["Sebastian"], ids["Mia"]}},
			map[string]any{"word": "", "whole_family": true},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("create = %d %v", status, got)
	}
	rels := got["relations"].([]any)
	if len(rels) != 2 {
		t.Fatalf("relations = %v", rels)
	}
	first := rels[0].(map[string]any)
	if first["word"] != "Oma" || first["whole_family"] != false || len(first["people"].([]any)) != 2 || first["people"].([]any)[0] != ids["Sebastian"] {
		t.Errorf("first = %v", first)
	}
	if second := rels[1].(map[string]any); second["whole_family"] != true || len(second["people"].([]any)) != 0 {
		t.Errorf("second = %v", second)
	}
	id := got["id"].(string)
	if status, got, _ := c.api.call(http.MethodGet, "/contacts/"+id, nil); status != http.StatusOK || len(got["relations"].([]any)) != 2 {
		t.Errorf("get = %d %v", status, got)
	}
	if status, _, list := c.api.call(http.MethodGet, "/contacts?q=nachbarin", nil); status != http.StatusOK || len(list) != 1 {
		t.Errorf("search = %d %v", status, list)
	}
	for name, rel := range map[string]map[string]any{
		"people but no word": {"word": " ", "people": []any{ids["Mia"]}},
		"41 characters":      {"word": strings.Repeat("ä", 41), "whole_family": true},
		"unknown person":     {"word": "Oma", "people": []any{"00000000-0000-4000-8000-000000000000"}},
		"nobody":             {"word": "Oma"},
	} {
		if status, _, _ := c.api.call(http.MethodPost, "/contacts", map[string]any{"name": "X " + name, "relations": []any{rel}}); status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, status)
		}
	}
	// A person's contact has none; a replace without "relations" takes them away.
	if status, _, _ := c.api.call(http.MethodPut, "/contacts/"+ids["Mia"], map[string]any{"name": "Mia", "relations": []any{map[string]any{"word": "Oma", "whole_family": true}}}); status != http.StatusUnprocessableEntity {
		t.Errorf("a person's contact = %d, want 422", status)
	}
	if status, got, _ := c.api.call(http.MethodPut, "/contacts/"+id, map[string]any{"name": "Oma Gerda"}); status != http.StatusOK || len(got["relations"].([]any)) != 0 {
		t.Errorf("replace = %d %v", status, got)
	}
}
