package server_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Ticket 300 (preview-300.html): contacts from a vCard file, duplicates offered for merging.

func vcf(s string) []byte { return []byte(strings.ReplaceAll(s, "\n", "\r\n")) }

// uploadVCF posts a contact file as the upload form does: multipart, the file in the part "file".
func (c ctBox) uploadVCF(name string, data []byte) resp {
	c.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, name))
	h.Set("Content-Type", "text/vcard")
	part, _ := mw.CreatePart(h)
	_, _ = part.Write(data)
	_ = mw.Close()
	return c.rawPost("/contacts/import", mw.FormDataContentType(), &buf)
}

var importPathRe = regexp.MustCompile(`^/contacts/import/([0-9a-f-]{36})$`)

// startImport uploads a file that must be accepted and returns the address of its review page.
func (c ctBox) startImport(name string, data []byte) string {
	c.t.Helper()
	r := c.uploadVCF(name, data)
	if r.status != http.StatusSeeOther || importPathRe.FindStringSubmatch(r.location) == nil {
		c.t.Fatalf("upload = %d %q\n%s", r.status, r.location, r.body)
	}
	return r.location
}

func (c ctBox) confirm(path string, f url.Values) resp {
	c.t.Helper()
	return c.do(http.MethodPost, path, f, map[string]string{"Accept-Language": "de"})
}

func (c ctBox) contactNamed(name string) map[string]any {
	c.t.Helper()
	_, _, list := c.api.call(http.MethodGet, "/contacts", nil)
	for _, x := range list {
		if m := x.(map[string]any); m["name"] == name {
			return m
		}
	}
	return nil
}

func lineValues(m map[string]any, key string) []string {
	var out []string
	for _, l := range m[key].([]any) {
		out = append(out, l.(map[string]any)["value"].(string))
	}
	return out
}

func (c ctBox) contactCount() int {
	_, _, list := c.api.call(http.MethodGet, "/contacts", nil)
	return len(list)
}

const threeVersions = `BEGIN:VCARD
VERSION:2.1
N;CHARSET=ISO-8859-1;ENCODING=QUOTED-PRINTABLE:M=FCller;Gerda;;;
FN;CHARSET=UTF-8;ENCODING=QUOTED-PRINTABLE:Gerda M=C3=BCller
TEL;CELL:0171 3456 789
TEL;HOME:08031 87 65 43
EMAIL;HOME:gerda.mueller@example.de
END:VCARD
BEGIN:VCARD
VERSION:3.0
FN:Familie Nguyễn
TEL;TYPE=HOME:089 5566 1234
TEL;TYPE=CELL:0151 2200 9911
TEL;TYPE=WORK:089 5566 0000
EMAIL;TYPE=HOME:nguyen@example.de
EMAIL;TYPE=WORK:nguyen@arbeit.example
END:VCARD
BEGIN:VCARD
VERSION:4.0
FN:Kita Sonnenschein
TEL;TYPE="work,voice":tel:+49-89-7654-3210
END:VCARD
`

// Acceptance: reads vCard 2.1 quoted-printable with a charset, 3.0 and 4.0; nothing is saved before confirming.
func TestAFileOfAllThreeVCardVersionsIsReadAndNothingIsSavedBeforeTheParentConfirms(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	before := c.contactCount()
	path := c.startImport("Kontakte Papa.vcf", vcf(threeVersions))
	if got := c.contactCount(); got != before {
		t.Fatalf("%d contacts after the upload, want %d: nothing is saved before the yes", got, before)
	}
	r := c.page(path, "de")
	ctHas(t, "review", r.body, "<h1>Kontakte prüfen</h1>", `<b class="fname">Kontakte Papa.vcf</b>`, "3 Karten", "<b>Gerda Müller</b>", "<b>Familie Nguyễn</b>", "<b>Kita Sonnenschein</b>",
		`type="checkbox" name="take" value="1" checked`, `class="savebar imp-save"`, "Kontakte übernehmen", "Abbrechen, nichts übernehmen", "3 neu.")
	if res := c.confirm(path, url.Values{"take": {"1", "2", "3"}}); res.status != http.StatusSeeOther || res.location != "/contacts" {
		t.Fatalf("confirm = %d %q\n%s", res.status, res.location, res.body)
	}
	g := c.contactNamed("Gerda Müller")
	if g == nil || g["full_name"] != "Gerda Müller" || strings.Join(lineValues(g, "numbers"), "|") != "0171 3456 789|08031 87 65 43" {
		t.Errorf("2.1 card = %v", g)
	}
	k := c.contactNamed("Kita Sonnenschein")
	if k == nil || strings.Join(lineValues(k, "numbers"), "|") != "+49-89-7654-3210" {
		t.Errorf("4.0 card = %v", k)
	}
	if after := c.contactCount(); after != before+3 {
		t.Errorf("%d contacts, want %d", after, before+3)
	}
	if r := c.page(path, "de"); r.status != http.StatusNotFound {
		t.Errorf("the review is still there after the import: %d", r.status)
	}
}

// Acceptance: multi-card files keep all phone numbers and e-mail addresses per card.
func TestAMultiCardFileKeepsEveryNumberAndEveryEmailOfEveryCard(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	path := c.startImport("alle.vcf", vcf(threeVersions))
	c.confirm(path, url.Values{"take": {"1", "2", "3"}})
	n := c.contactNamed("Familie Nguyễn")
	if n == nil || strings.Join(lineValues(n, "numbers"), "|") != "089 5566 1234|0151 2200 9911|089 5566 0000" || strings.Join(lineValues(n, "emails"), "|") != "nguyen@example.de|nguyen@arbeit.example" {
		t.Errorf("Nguyễn = %v", n)
	}
	if g := c.contactNamed("Gerda Müller"); g == nil || strings.Join(lineValues(g, "emails"), "|") != "gerda.mueller@example.de" {
		t.Errorf("Gerda = %v", g)
	}
}

func existingFamilyContacts(t *testing.T, c ctBox) {
	t.Helper()
	c.add(t, contactForm("Oma Gerda", "Gerda Müller", [][3]string{{"cell", "", "0171 3456 789"}, {"home", "", "08031 87 65 43"}}, nil))
	c.add(t, contactForm("Kinderarzt", "Kinderarztpraxis Dr. Yılmaz", [][3]string{{"work", "", "089 1234 5670"}}, [][3]string{{"work", "", "Praxis@Kinderarzt.example"}}))
	c.add(t, contactForm("Frau Wöllenstein", "Hannelore Wöllenstein", [][3]string{{"home", "", "08031 22 33 44"}}, nil))
	c.add(t, contactForm("Ohne alles", "", nil, nil))
}

const duplicates = `BEGIN:VCARD
VERSION:3.0
FN:Gerda aus dem Garten
TEL;TYPE=CELL:+49 171 3456789
TEL;TYPE=WORK:08031 40 12 77
END:VCARD
BEGIN:VCARD
VERSION:3.0
FN:Dr. Elif Yılmaz
EMAIL;TYPE=WORK:praxis@kinderarzt.example
TEL;TYPE=FAX:089 1234 5679
END:VCARD
BEGIN:VCARD
VERSION:3.0
FN:Hannelore Wöllenstein
TEL;TYPE=CELL:0157 8800 4411
END:VCARD
BEGIN:VCARD
VERSION:3.0
FN:Jemand ohne Werte
TEL:
EMAIL:
END:VCARD
BEGIN:VCARD
VERSION:3.0
FN:Ganz Neu
TEL:0160 1
END:VCARD
`

// Acceptance: duplicates are found by phone number or e-mail, not only by name; empty values never match.
func TestDuplicatesAreFoundByNumberOrEmailNotOnlyByNameAndEmptyValuesNeverMatch(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	existingFamilyContacts(t, c)
	path := c.startImport("Kontakte Papa.vcf", vcf(duplicates))
	body := c.page(path, "de").body
	dups := regexp.MustCompile(`(?s)<li class="imp-dup[^"]*" id="d-(\d)".*?</li>\s*(?:<li class="imp-dup|</ul>)`).FindAllStringSubmatch(body, -1)
	ids := map[string]bool{}
	for _, d := range dups {
		ids[d[1]] = true
	}
	if !ids["1"] || !ids["2"] || !ids["3"] || ids["4"] || ids["5"] {
		t.Fatalf("duplicates found: %v, want cards 1, 2 and 3 only\n%s", ids, body)
	}
	byNumber := between(body, `id="d-1"`, `id="d-2"`)
	ctHas(t, "number match", byNumber, "Gleiche Nummer: <b>Mobil 0171 3456 789</b>", "in der Datei mit +49 geschrieben", "<b>Oma Gerda</b>", `name="d-1" value="merge" checked`, `class="merge-out"`, "Arbeit 08031 40 12 77")
	ctHasNot(t, "number match", byNumber, "Bitte wählen")
	byMail := between(body, `id="d-2"`, `id="d-3"`)
	ctHas(t, "e-mail match", byMail, "Gleiche E-Mail-Adresse: <b>Praxis@Kinderarzt.example</b>", `name="d-2" value="merge" checked`, "Fax 089 1234 5679")
	byName := between(body, `id="d-3"`, `<section aria-labelledby="new"`)
	ctHas(t, "name match", byName, "Nur der <b>Name</b> ist gleich", "Bitte wählen: Hejmo weiß nicht, ob das dieselbe Person ist.", `name="d-3" value="merge"`)
	ctHasNot(t, "name match", byName, `value="merge" checked`, `value="new" checked`, `value="skip" checked`)
	ctHas(t, "new", body, `name="take" value="4" checked`, `name="take" value="5" checked`, "<b>Jemand ohne Werte</b>", "<b>Ganz Neu</b>")
}

func between(s, from, to string) string {
	i := strings.Index(s, from)
	if i < 0 {
		return ""
	}
	j := strings.Index(s[i:], to)
	if j < 0 {
		return s[i:]
	}
	return s[i : i+j]
}

func TestAMissingChoiceStopsTheImportAtThatCardAndSavesNothing(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	existingFamilyContacts(t, c)
	path := c.startImport("Kontakte Papa.vcf", vcf(duplicates))
	before := c.contactCount()
	r := c.confirm(path, url.Values{"d-1": {"merge"}, "d-2": {"merge"}, "take": {"4", "5"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", r.status)
	}
	ctHas(t, "error page", r.body, `<p class="error form-error" role="alert">`, `Noch nichts übernommen. Bei einem Kontakt fehlt deine Wahl:`, `<a href="#d-3">Hannelore Wöllenstein</a>`,
		`class="imp-dup invalid" id="d-3"`, `aria-invalid="true"`, "Wähle, was mit „Hannelore Wöllenstein“ passieren soll.", `name="d-1" value="merge" checked`, "2 ergänzt", "2 neu", "Bei 1 fehlt deine Wahl.")
	if c.contactCount() != before {
		t.Error("contacts were saved although a choice was missing")
	}
	if r := c.page(path, "de"); r.status != http.StatusOK {
		t.Error("the file is gone after a refused confirm")
	}
}

func TestMergingAddsAndFillsAndNeverOverwritesOrDeletesAndKeepsRelationsAndGroups(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	notfall := c.groupID(t, "notfall")
	mia := c.addPerson(t, "Mia", "child")
	f := contactForm("Oma Gerda", "Gerda Müller", [][3]string{{"cell", "", "0171 3456 789"}, {"home", "", "08031 87 65 43"}}, [][3]string{{"home", "", "gerda@example.de"}})
	f["group"] = []string{notfall}
	f.Set("street", "Am Erlenbach 7")
	f.Set("city", "Rosenheim")
	f.Set("bday_day", "14")
	f.Set("bday_month", "3")
	id := c.add(t, withRels(f, relLine{word: "Oma", people: []string{mia}}))
	path := c.startImport("Kontakte Papa.vcf", vcf(`BEGIN:VCARD
VERSION:3.0
FN:Gerda Schmidt
NICKNAME:Omi
TEL;TYPE=CELL:0171 3456 789
TEL;TYPE=WORK:08031 40 12 77
EMAIL;TYPE=HOME:GERDA@example.de
EMAIL;TYPE=WORK:gerda@verein.example
ADR;TYPE=HOME:;;Am Erlenbach 9;Rosenheim;;83022;
BDAY:1950-03-14
NOTE:Mag Rosen
END:VCARD
`))
	body := c.page(path, "de").body
	ctHas(t, "merge-out", body, "Kommt dazu", "Arbeit 08031 40 12 77", "E-Mail Verein"[:0]+"gerda@verein.example", "Geburtsjahr: aus „14.03. · Jahr unbekannt“ wird 14.03.1950", `Notiz „Mag Rosen“`,
		"Bleibt, wie es in Hejmo ist", `Name „Oma Gerda“`, "Beziehung „Oma von Mia“", "Adresse Am Erlenbach 7, Rosenheim (in der Datei: Am Erlenbach 9, 83022 Rosenheim)", "Vollständiger Name „Gerda Müller“ (in der Datei: „Gerda Schmidt“)")
	if r := c.confirm(path, url.Values{"d-1": {"merge"}}); r.status != http.StatusSeeOther {
		t.Fatalf("confirm = %d\n%s", r.status, r.body)
	}
	_, got, _ := c.api.call(http.MethodGet, "/contacts/"+id, nil)
	if got["name"] != "Oma Gerda" || got["full_name"] != "Gerda Müller" {
		t.Errorf("names changed: %v %v", got["name"], got["full_name"])
	}
	if strings.Join(lineValues(got, "numbers"), "|") != "0171 3456 789|08031 87 65 43|08031 40 12 77" || strings.Join(lineValues(got, "emails"), "|") != "gerda@example.de|gerda@verein.example" {
		t.Errorf("lines = %v / %v", lineValues(got, "numbers"), lineValues(got, "emails"))
	}
	addr := got["address"].(map[string]any)
	if addr["street"] != "Am Erlenbach 7" || addr["postcode"] != "" || got["note"] != "Mag Rosen" {
		t.Errorf("address %v, note %v", addr, got["note"])
	}
	if b := got["birthday"].(map[string]any); b["year"] != float64(1950) || b["day"] != float64(14) {
		t.Errorf("birthday = %v", b)
	}
	if rels := got["relations"].([]any); len(rels) != 1 || rels[0].(map[string]any)["word"] != "Oma" {
		t.Errorf("relations = %v", got["relations"])
	}
	if g := got["groups"].([]any); len(g) != 1 || g[0] != notfall {
		t.Errorf("groups = %v", got["groups"])
	}
}

func TestAHouseholdPersonOnlyGetsNumbersAndNeverChangesNameBirthdayOrEmail(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	mama := c.peopleBox.byName(t, "Mama")
	id := mama["id"].(string)
	if status, _, _ := c.api.call(http.MethodPut, "/contacts/"+id, map[string]any{"name": mama["name"], "full_name": "Katharina Maier-Schmidt",
		"numbers": []any{map[string]any{"kind": "cell", "value": "0176 2345 6789"}}, "emails": []any{map[string]any{"kind": "home", "value": "kathi@example.de"}}, "birthday": map[string]any{"day": 17, "month": 5, "year": 1984}}); status != http.StatusOK {
		t.Fatalf("PUT = %d", status)
	}
	path := c.startImport("Kontakte Papa.vcf", vcf(`BEGIN:VCARD
VERSION:3.0
FN:Kathi M.
TEL;TYPE=CELL:0176 23456789
TEL;TYPE=HOME:089 8899 0011
EMAIL:kathi.neu@example.de
BDAY:1990-01-01
END:VCARD
`))
	body := c.page(path, "de").body
	ctHas(t, "person", body, "Schon in Hejmo, aus eurer Familie", `value="merge" checked`, `value="skip"`, "Bei Personen aus eurer Familie ändert die Datei sie nicht", "Das geht nur unter Personen")
	ctHasNot(t, "person", body, `value="new"`)
	if r := c.confirm(path, url.Values{"d-1": {"new"}}); r.status != http.StatusUnprocessableEntity {
		t.Errorf("an own contact of a person = %d, want 422", r.status)
	}
	c.confirm(path, url.Values{"d-1": {"merge"}})
	_, got, _ := c.api.call(http.MethodGet, "/contacts/"+id, nil)
	if got["name"] != mama["name"] || got["full_name"] != "Katharina Maier-Schmidt" || strings.Join(lineValues(got, "emails"), "|") != "kathi@example.de" {
		t.Errorf("a person changed: %v", got)
	}
	if b := got["birthday"].(map[string]any); b["year"] != float64(1984) || strings.Join(lineValues(got, "numbers"), "|") != "0176 2345 6789|089 8899 0011" {
		t.Errorf("birthday %v, numbers %v", b, lineValues(got, "numbers"))
	}
}

func TestTwoCardsOfTheFileThatLookTheSameAreOneQuestionAndOnlyMergeWhenAsked(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	data := vcf(`BEGIN:VCARD
VERSION:3.0
FN:Herr Brandl
TEL;TYPE=CELL:0160 5544 3322
END:VCARD
BEGIN:VCARD
VERSION:3.0
FN:Sepp Brandl
TEL;TYPE=CELL:0160 5544 3322
TEL;TYPE=WORK:089 700 12 34
END:VCARD
`)
	path := c.startImport("zwei.vcf", data)
	body := c.page(path, "de").body
	ctHas(t, "twice", body, "Steht zweimal in der Datei", "Zweite Karte in der Datei", "Zu einem Kontakt „Herr Brandl“ zusammenführen", "Als zwei Kontakte anlegen", "Beide nicht übernehmen", "Ein Kontakt mit")
	before := c.contactCount()
	if c.confirm(path, url.Values{"d-1": {"merge"}}); c.contactCount() != before+1 {
		t.Errorf("one contact expected, have %d new", c.contactCount()-before)
	}
	if b := c.contactNamed("Herr Brandl"); b == nil || strings.Join(lineValues(b, "numbers"), "|") != "0160 5544 3322|089 700 12 34" {
		t.Errorf("merged = %v", b)
	}
	other := newCtBox(t)
	before = other.contactCount()
	if other.confirm(other.startImport("zwei.vcf", data), url.Values{"d-1": {"new"}}); other.contactCount() != before+2 {
		t.Errorf("two contacts expected, have %d new", other.contactCount()-before)
	}
}

func TestNewContactsAreNamedByNicknameThenFullNameAndSayWhyAChangedName(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.addPerson(t, "Ben", "child")
	path := c.startImport("neu.vcf", vcf(`BEGIN:VCARD
VERSION:3.0
FN:Ben Krüger
NICKNAME:Ben
TEL:0151 2200 9911
END:VCARD
BEGIN:VCARD
VERSION:3.0
FN:Zahnarztpraxis Dr. Römer & Partner, Kieferorthopädie und Prophylaxe
TEL:089 3322 1100
END:VCARD
`))
	body := c.page(path, "de").body
	ctHas(t, "names", body, "<b>Ben Krüger</b>", `„Ben“ gibt es schon: Ben aus eurer Familie. Darum heißt dieser Kontakt „Ben Krüger“.`, "<b>Zahnarztpraxis Dr. Römer & Partner</b>", "Name für Hejmo gekürzt (höchstens 40 Zeichen). Der vollständige Name bleibt.")
	c.confirm(path, url.Values{"take": {"1", "2"}})
	if c.contactNamed("Ben Krüger") == nil || c.contactNamed("Zahnarztpraxis Dr. Römer & Partner") == nil {
		t.Error("the contacts were not made under the names the page showed")
	}
}

func TestOnlyTheTickedNewContactsAreTakenAndTheyComeIntoTheChosenGroups(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	sonstige := c.groupID(t, "sonstige")
	path := c.startImport("zwei.vcf", vcf("BEGIN:VCARD\nVERSION:3.0\nFN:Eins\nTEL:0171 1\nEND:VCARD\nBEGIN:VCARD\nVERSION:3.0\nFN:Zwei\nTEL:0171 2\nEND:VCARD\n"))
	c.confirm(path, url.Values{"take": {"2"}, "group": {sonstige}})
	if c.contactNamed("Eins") != nil {
		t.Error("an unticked contact was taken")
	}
	if z := c.contactNamed("Zwei"); z == nil || len(z["groups"].([]any)) != 1 || z["groups"].([]any)[0] != sonstige {
		t.Errorf("Zwei = %v", z)
	}
}

func TestTheResultNamesTheFileTheNewAndTheCompletedContactsAndMarksTheRowsForOnePageLoad(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	id := c.add(t, contactForm("Oma Gerda", "Gerda Müller", [][3]string{{"cell", "", "0171 3456 789"}}, nil))
	path := c.startImport("Kontakte Papa.vcf", vcf("BEGIN:VCARD\nVERSION:3.0\nFN:Gerda Müller\nTEL:0171 3456 789\nTEL;TYPE=WORK:08031 40 12 77\nEND:VCARD\nBEGIN:VCARD\nVERSION:3.0\nFN:Familie Nguyễn\nTEL:089 5566 1234\nEND:VCARD\n"))
	r := c.confirm(path, url.Values{"d-1": {"merge"}, "take": {"2"}})
	if r.status != http.StatusSeeOther || r.location != "/contacts" {
		t.Fatalf("confirm = %d %q", r.status, r.location)
	}
	over := c.page("/contacts", "de").body
	ctHas(t, "overview", over, `<p class="msg ok" role="status"`, "<b>Aus „Kontakte Papa.vcf“ übernommen:</b> 1 neuer Kontakt. Ergänzt: Oma Gerda. Die Datei ist gelöscht.",
		`<span class="tag fresh">Gerade ergänzt</span>`, `<span class="tag fresh">Gerade übernommen</span>`)
	if again := c.page("/contacts", "de").body; strings.Contains(again, "tag fresh") || strings.Contains(again, "Aus „Kontakte Papa.vcf“ übernommen") {
		t.Error("the marks and the line stay for a second page load")
	}
	ctHas(t, "contact page", c.page("/contacts/"+id, "de").body, "ergänzt aus „Kontakte Papa.vcf“ von ")
	nu := c.contactNamed("Familie Nguyễn")["id"].(string)
	ctHas(t, "new contact page", c.page("/contacts/"+nu, "de").body, "Aus „Kontakte Papa.vcf“ übernommen von ")
	ctHas(t, "English", c.page("/contacts/"+nu, "en").body, "Taken over from “Kontakte Papa.vcf” by ")
}

func TestTheOverviewOffersTheImportBesideTheNewButtonAndInTheEmptyBox(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	empty := c.page("/contacts", "de").body
	ctHas(t, "empty overview", empty, `<p class="ct-acts"><a class="button" href="/contacts/new">Kontakt anlegen</a></p>`, `<div class="empty" data-empty><p>Noch keine weiteren Kontakte. Hierher gehören Großeltern, Kita, Arztpraxen und alle, die ihr anrufen wollt.</p>`,
		"Stehen sie schon in deinem Adressbuch?", `<a class="button secondary" href="/contacts/import">`, "Kontakte aus Datei übernehmen")
	c.add(t, contactForm("Oma Gerda", "", nil, nil))
	full := c.page("/contacts", "de").body
	ctHas(t, "overview", full, `<p class="ct-acts"><a class="button" href="/contacts/new">Kontakt anlegen</a><a class="button secondary" href="/contacts/import">`)
	if strings.Count(full, "Kontakte aus Datei übernehmen") != 1 {
		t.Error("the import is offered twice")
	}
	up := c.page("/contacts/import", "de").body
	ctHas(t, "upload", up, "<h1>Kontakte aus Datei übernehmen</h1>", `type="file" name="file" accept=".vcf,.vcard,text/vcard,text/x-vcard"`, `class="upd imp-how"`, `<ol class="howto">`, `<details class="more imp-where">`,
		`<ul class="plain imp-ways">`, "höchstens 25 MB", `enctype="multipart/form-data"`, "So bekommst du die Datei")
}

func TestARefusedFileIsExplainedAtTheFileFieldAndNothingIsKept(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	for name, tc := range map[string]struct {
		file   string
		data   []byte
		status int
		want   string
	}{
		"picture": {"Urlaub Gardasee.jpg", testJPEG(t, 40, 30), http.StatusUnprocessableEntity, "„Urlaub Gardasee.jpg“ ist keine Kontaktdatei. Wähle eine Datei, die auf .vcf endet."},
		"empty":   {"Kontakte.vcf", nil, http.StatusUnprocessableEntity, "In „Kontakte.vcf“ steht kein Kontakt. Die Datei ist leer."},
		"cards":   {"kaputt.vcf", vcf("BEGIN:VCARD\nVERSION:3.0\nNOTE:nur Notiz\nEND:VCARD\n"), http.StatusUnprocessableEntity, "steht kein Kontakt, den Hejmo lesen kann"},
		"big":     {"gross.vcf", bytes.Repeat([]byte("x"), 25<<20+1), http.StatusRequestEntityTooLarge, "„gross.vcf“ ist 25,0 MB groß. Hejmo nimmt Dateien bis 25 MB."},
	} {
		r := c.uploadVCF(tc.file, tc.data)
		if r.status != tc.status {
			t.Errorf("%s: status = %d, want %d", name, r.status, tc.status)
		}
		ctHas(t, name, r.body, `<p class="error form-error" role="alert">`, `<a href="#file">Kontaktdatei</a>`, `<div class="f invalid">`, `aria-invalid="true"`, `<p class="ferr" id="err-file" role="alert">`, tc.want)
	}
	var none bytes.Buffer
	mw := multipart.NewWriter(&none)
	_ = mw.Close()
	if r := c.rawPost("/contacts/import", mw.FormDataContentType(), &none); r.status != http.StatusUnprocessableEntity {
		t.Errorf("no file = %d", r.status)
	} else {
		ctHas(t, "no file", r.body, "Wähle zuerst eine Kontaktdatei.")
	}
}

func TestAFileWithNothingNewIsNotKeptAndTheCardsHejmoCannotReadAreListed(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.add(t, contactForm("Oma Gerda", "Gerda Müller", [][3]string{{"cell", "", "0171 3456 789"}}, nil))
	r := c.uploadVCF("Kontakte Papa.vcf", vcf("BEGIN:VCARD\nVERSION:3.0\nFN:Gerda Müller\nTEL:+49 171 3456789\nEND:VCARD\nBEGIN:VCARD\nVERSION:3.0\nNOTE:nichts\nEND:VCARD\nBEGIN:VCARD\nVERSION:3.0\nFN:Rudi Wagn\nTEL:0171"))
	if r.status != http.StatusOK {
		t.Fatalf("status = %d %q", r.status, r.location)
	}
	ctHas(t, "nothing new", r.body, "<h1>Kontakte prüfen</h1>", "<b>Alles aus der Datei ist schon in Hejmo.</b> Oma Gerda steht mit allen Nummern und E-Mail-Adressen schon da.", `href="/contacts">Zurück zu Kontakte</a>`, "Die Datei ist schon gelöscht.",
		"<b>Karte 2</b> Kein Name, keine Nummer, keine E-Mail-Adresse.", "<b>Karte 3 „Rudi Wagn…“</b> Die Karte bricht mitten im Text ab (Zeile 10).")
	ctHasNot(t, "nothing new", r.body, `class="savebar`)
}

func TestTheFileWaitsOnlyForTheParentWhoUploadedItAndAnHourAtMost(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	path := c.startImport("Kontakte Papa.vcf", vcf(threeVersions))
	id := importPathRe.FindStringSubmatch(path)[1]
	// A file of another parent is nobody else's business.
	h, _ := c.db.HomeHousehold()
	papa := c.addPerson(t, "Papa", "parent")
	foreign, err := c.db.AddContactImport(h.ID, papa, "Papas Datei.vcf", vcf(threeVersions), nil, c.now)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/contacts/import/" + foreign.ID} {
		if r := c.page(p, "de"); r.status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, r.status)
		}
		if r := c.confirm(p, url.Values{"take": {"1"}}); r.status != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", p, r.status)
		}
	}
	if status, _, _ := c.getPhoto("/contacts/import/" + foreign.ID + "/photo/1"); status != http.StatusNotFound {
		t.Errorf("photo of another parent = %d", status)
	}
	// After an hour the file is gone and the page says so.
	c.now = c.now.Add(59 * time.Minute)
	if r := c.page(path, "de"); r.status != http.StatusOK || !strings.Contains(r.body, "Kontakte prüfen") {
		t.Fatalf("after 59 minutes = %d", r.status)
	}
	c.now = c.now.Add(2 * time.Minute)
	r := c.page(path, "de")
	ctHas(t, "expired", r.body, `<div class="empty" data-empty>`, "<b>Diese Prüfung ist abgelaufen.</b> „Kontakte Papa.vcf“ lag eine Stunde auf der Box und ist jetzt gelöscht. Übernommen wurde nichts.", "Datei noch einmal hochladen", `href="/contacts/import"`)
	if r := c.confirm(path, url.Values{"take": {"1", "2", "3"}}); !strings.Contains(r.body, "abgelaufen") {
		t.Errorf("confirming an expired file: %d", r.status)
	}
	if got, err := c.db.ContactImport(h.ID, id); err != nil || len(got.Data) != 0 {
		t.Errorf("the file of an expired upload stays: %v", err)
	}
}

func TestCancellingDeletesTheFileAndSavesNothing(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	path := c.startImport("Kontakte Papa.vcf", vcf(threeVersions))
	before := c.contactCount()
	r := c.confirm(path, url.Values{"cancel": {"1"}, "take": {"1", "2", "3"}})
	if r.status != http.StatusSeeOther || r.location != "/contacts" {
		t.Fatalf("cancel = %d %q", r.status, r.location)
	}
	if c.contactCount() != before || c.page(path, "de").status != http.StatusNotFound {
		t.Error("a cancelled import saved something or kept the file")
	}
	ctHas(t, "line", c.page("/contacts", "de").body, "Nichts übernommen. Die Datei ist gelöscht.")
}

func TestOnlyParentsImportContacts(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	child := childSession(t, b)
	for _, path := range []string{"/contacts/import", "/contacts/import/" + strings.Repeat("a", 36)} {
		if r := child.get(path); r.status != http.StatusForbidden {
			t.Errorf("child GET %s = %d, want 403", path, r.status)
		}
	}
	if r := child.post("/contacts/import", url.Values{}); r.status != http.StatusForbidden {
		t.Errorf("child POST = %d", r.status)
	}
	out, _ := configuredBox(t)
	if r := out.get("/contacts/import"); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Errorf("signed out = %d %q", r.status, r.location)
	}
}

func TestEmbeddedPhotosComeUnderThePictureRulesAndAWebLinkIsNeverFetched(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	t.Cleanup(web.Close)
	c := newCtBox(t)
	pic := base64.StdEncoding.EncodeToString(testJPEG(t, 900, 600))
	path := c.startImport("Fotos.vcf", vcf("BEGIN:VCARD\nVERSION:3.0\nFN:Tante Susanne\nTEL:0172 4455 6600\nPHOTO;ENCODING=b;TYPE=JPEG:"+pic+"\nEND:VCARD\n"+
		"BEGIN:VCARD\nVERSION:3.0\nFN:Opa Konstantin\nTEL:0172 1\nPHOTO;VALUE=uri:"+web.URL+"/opa.jpg\nEND:VCARD\n"))
	body := c.page(path, "de").body
	ctHas(t, "review", body, `/photo/1?v=1`, "Foto steht nur als Internet-Link in der Datei. Hejmo lädt nichts aus dem Internet, darum kommt dieser Kontakt ohne Foto.")
	status, hdr, data := c.getPhoto(path + "/photo/1")
	if status != http.StatusOK || hdr.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("review photo = %d %s", status, hdr.Get("Content-Type"))
	}
	if w, h := jpegSide(t, data); w != 512 || h != 512 {
		t.Errorf("review photo is %d x %d, want 512 x 512 (square, at most 512 px: it was 900 x 600)", w, h)
	}
	if status, _, _ := c.getPhoto(path + "/photo/2"); status != http.StatusNotFound {
		t.Errorf("photo of a card without one = %d", status)
	}
	c.confirm(path, url.Values{"take": {"1", "2"}})
	susi := c.contactNamed("Tante Susanne")
	if susi == nil || susi["photo"] == nil {
		t.Fatalf("Susanne = %v", susi)
	}
	_, _, stored := c.getPhoto("/contacts/" + susi["id"].(string) + "/photo?v=1")
	if w, h := jpegSide(t, stored); w > 512 || w != h {
		t.Errorf("stored photo %d x %d", w, h)
	}
	if opa := c.contactNamed("Opa Konstantin"); opa == nil || opa["photo"] != nil {
		t.Errorf("Opa = %v", opa)
	}
	if hits.Load() != 0 {
		t.Errorf("the box fetched a photo link %d times (ADR 0024)", hits.Load())
	}
	ctHas(t, "photo origin", c.page("/contacts/"+susi["id"].(string), "de").body, "Foto aus „Fotos.vcf“, übernommen von ")
}

func TestWhenBothHaveAPhotoTheParentChoosesAndTheCurrentOneIsPreselected(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	id := c.add(t, contactForm("Oma Gerda", "Gerda Müller", [][3]string{{"cell", "", "0171 3456 789"}}, nil))
	c.photoPost("/contacts/"+id+"/edit", contactForm("Oma Gerda", "Gerda Müller", [][3]string{{"cell", "", "0171 3456 789"}}, nil), "alt.jpg", testJPEG(t, 300, 300))
	if c.contactNamed("Oma Gerda")["photo"] == nil {
		t.Fatal("no photo to start with")
	}
	pic := base64.StdEncoding.EncodeToString(testJPEG(t, 100, 100))
	card := func(extra string) []byte {
		return vcf("BEGIN:VCARD\nVERSION:3.0\nFN:Gerda Müller\nTEL:0171 3456 789\nTEL;TYPE=WORK:" + extra + "\nPHOTO;ENCODING=b;TYPE=JPEG:" + pic + "\nEND:VCARD\n")
	}
	path := c.startImport("a.vcf", card("08031 1"))
	ctHas(t, "choice", c.page(path, "de").body, `<fieldset class="choice ph-choose">`, `name="ph-1" value="keep" checked`, `name="ph-1" value="file"`, "Foto: beide haben eins. Welches soll „Oma Gerda“ zeigen?")
	again := c.startImport("b.vcf", card("08031 2"))
	before := c.contactNamed("Oma Gerda")["photo"].(map[string]any)["at"]
	c.confirm(path, url.Values{"d-1": {"merge"}, "ph-1": {"keep"}})
	if after := c.contactNamed("Oma Gerda")["photo"].(map[string]any)["at"]; after != before {
		t.Error("the photo changed although the parent kept the current one")
	}
	c.confirm(again, url.Values{"d-1": {"merge"}, "ph-1": {"file"}})
	_, _, stored := c.getPhoto("/contacts/" + id + "/photo")
	if w, _ := jpegSide(t, stored); w != 100 {
		t.Errorf("the photo of the file was not taken: %d px", w)
	}
}

func TestTheImportPagesAreInGermanAndEnglish(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	ctHas(t, "English upload", c.page("/contacts/import", "en").body, "<h1>Import contacts from a file</h1>", "Check file", "At most 25 MB"[:0]+"at most 25 MB")
	path := c.startImport("Kontakte Papa.vcf", vcf(threeVersions))
	ctHas(t, "English review", c.page(path, "en").body, "<h1>Check contacts</h1>", "Uploaded by ", "3 cards", "Take over contacts", "Cancel, take over nothing", "Tick all")
}

// ---- over REST

func TestTheImportOverRESTHasTheSameRulesAndIsCheckedAgainstTheContract(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	existingFamilyContacts(t, c)
	upload := func(name string, data []byte) (string, int) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		part, _ := mw.CreateFormFile("file", name)
		_, _ = part.Write(data)
		_ = mw.Close()
		body, status, _ := c.api.send(http.MethodPost, "/contact-imports", mw.FormDataContentType(), buf.Bytes())
		return body, status
	}
	if _, status := upload("bild.jpg", testJPEG(t, 10, 10)); status != http.StatusUnprocessableEntity {
		t.Errorf("a picture = %d, want 422", status)
	}
	if _, status := upload("leer.vcf", nil); status != http.StatusUnprocessableEntity {
		t.Errorf("an empty file = %d, want 422", status)
	}
	body, status := upload("Kontakte Papa.vcf", vcf(duplicates))
	if status != http.StatusCreated {
		t.Fatalf("upload = %d %s", status, body)
	}
	var imp struct {
		ID         string
		Cards      int
		Duplicates []struct {
			Key       string
			ContactID *string `json:"contact_id"`
			Preselect *string
			Choices   []string
		}
		New []struct{ Key, Name string }
	}
	decodeJSON(t, body, &imp)
	if imp.Cards != 5 || len(imp.Duplicates) != 3 || len(imp.New) != 2 {
		t.Fatalf("review = %+v", imp)
	}
	if imp.Duplicates[0].Preselect == nil || *imp.Duplicates[0].Preselect != "merge" || imp.Duplicates[2].Preselect != nil {
		t.Errorf("preselect = %+v", imp.Duplicates)
	}
	if status, got, _ := c.api.call(http.MethodGet, "/contact-imports/"+imp.ID, nil); status != http.StatusOK || got["file_name"] != "Kontakte Papa.vcf" {
		t.Errorf("GET = %d %v", status, got)
	}
	before := c.contactCount()
	if status, got, _ := c.api.call(http.MethodPost, "/contact-imports/"+imp.ID+"/confirm", map[string]any{}); status != http.StatusUnprocessableEntity || !strings.Contains(got["error"].(string), "d3") {
		t.Errorf("confirm without the name-only choice = %d %v", status, got)
	}
	if c.contactCount() != before {
		t.Error("saved although a choice was missing")
	}
	status, got, _ := c.api.call(http.MethodPost, "/contact-imports/"+imp.ID+"/confirm", map[string]any{"duplicates": map[string]any{"d3": "skip"}, "skip": []any{"n5"}})
	if status != http.StatusOK || len(got["created"].([]any)) != 1 || len(got["completed"].([]any)) != 2 {
		t.Fatalf("confirm = %d %v", status, got)
	}
	if c.contactCount() != before+1 {
		t.Errorf("contacts = %d, want %d", c.contactCount(), before+1)
	}
	if status, _, _ := c.api.call(http.MethodGet, "/contact-imports/"+imp.ID, nil); status != http.StatusNotFound {
		t.Errorf("GET after the import = %d, want 404", status)
	}
	// Cancel, and the hour.
	body, _ = upload("Kontakte Papa.vcf", vcf(threeVersions))
	decodeJSON(t, body, &imp)
	if _, status, _ := c.api.callRaw(http.MethodDelete, "/contact-imports/"+imp.ID, nil); status != http.StatusNoContent {
		t.Errorf("DELETE = %d", status)
	}
	body, _ = upload("Kontakte Papa.vcf", vcf(threeVersions))
	decodeJSON(t, body, &imp)
	c.now = c.now.Add(61 * time.Minute)
	if status, _, _ := c.api.call(http.MethodGet, "/contact-imports/"+imp.ID, nil); status != http.StatusGone {
		t.Errorf("GET after an hour = %d, want 410", status)
	}
	// Children have no access.
	kc := newAPIClient(t, childSession(t, c.box))
	kc.cookie = true
	if _, status, _ := kc.callRaw(http.MethodGet, "/contact-imports/"+imp.ID, nil); status != http.StatusForbidden {
		t.Errorf("child GET = %d", status)
	}
}

func TestTheLicencesPageNamesTheLibraryThatReadsPhoneNumbers(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	body := b.do(http.MethodGet, "/settings/licences", nil, map[string]string{"Accept-Language": "en"}).body
	ctHas(t, "licences", body, `data-licence="numbers"`, "Go library of Nyaruka (MIT licence", "libphonenumber", "nothing is looked up on the internet")
}
