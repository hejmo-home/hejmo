package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// Ticket 301 (preview-301.html): the children's page "Wichtige Nummern" and what a parent sees of it.

const (
	uaIPhone = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"
	uaIPad   = "Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"
)

// putPerson sets numbers and the switch on the contact of a person through the API.
func (k kidsBox) putPerson(t *testing.T, name string, kids bool, numbers ...[2]string) {
	t.Helper()
	var lines []any
	for _, n := range numbers {
		lines = append(lines, map[string]any{"kind": n[0], "value": n[1]})
	}
	id := ""
	_, _, list := k.api.call(http.MethodGet, "/contacts", nil)
	for _, c := range list {
		if m := c.(map[string]any); m["name"] == name {
			id = m["id"].(string)
		}
	}
	status, got, _ := k.api.call(http.MethodPut, "/contacts/"+id, map[string]any{"name": name, "numbers": lines, "for_children": kids})
	if status != http.StatusOK {
		t.Fatalf("PUT %s = %d %v", name, status, got)
	}
}

// numbersFamily is the example household of the preview: contacts in every section, one hidden, one without a number.
func newNumbersBox(t *testing.T) kidsBox {
	t.Helper()
	k := newKidsBox(t)
	k.putPerson(t, "Mama", true, [2]string{"cell", "0176 2345 6789"}, [2]string{"work", "089 4455 6677-12"})
	k.putPerson(t, "Sebastian", true, [2]string{"cell", "0157 3322 1100"})
	k.putPerson(t, "Mia", true, [2]string{"cell", "0152 9999 0000"}) // the child who looks does not see themselves
	k.addFor(t, "Notruf", true, [][3]string{{"own", "Feuerwehr und Rettung", "112"}, {"own", "Polizei", "110"}}, "notfall")
	k.addFor(t, "Giftnotruf München", true, [][3]string{{"own", "Notruf", "089 19240"}}, "notfall")
	k.addFor(t, "Opa Heinz", true, [][3]string{{"home", "", "08031 87 65 43"}}, "familie")
	k.addFor(t, "Oma Gerda", true, [][3]string{{"cell", "", "0171 3456 789"}, {"home", "", "08031 87 65 43"}, {"own", "Gartenhaus", "0160 111 2223"}}, "familie")
	k.addFor(t, "Kita Sonnenschein", true, [][3]string{{"work", "", "089 7654 3210"}}, "kinderbetreuung_schule")
	k.addFor(t, "Kinderarzt", true, [][3]string{{"work", "", "089 1234 5670"}, {"own", "Notdienst", "0171 9988 7766"}}, "arztpraxen")
	k.addFor(t, "Anna (Mama von Lukas)", false, [][3]string{{"cell", "", "0151 1111 2222"}})
	k.addFor(t, "Tante Ilse", true, nil)
	return k
}

// device pairs a personal device for a child and returns it with an API client that uses its session.
func (k kidsBox) device(t *testing.T, child, ua string) (*screen, *apiClient) {
	t.Helper()
	scr := pairedPersonalDeviceAs(t, k.box, personNamed(t, k.box, child), child+"'s device", ua)
	pb := *k.box
	pb.client = scr.client
	return scr, &apiClient{b: &pb, router: k.api.router, cookie: true}
}

func kidsIndexes(page string, needles ...string) []int {
	var out []int
	for _, n := range needles {
		out = append(out, strings.Index(page, n))
	}
	return out
}

func kidsInOrder(at []int) bool {
	for i, v := range at {
		if v < 0 || (i > 0 && v <= at[i-1]) {
			return false
		}
	}
	return true
}

func TestChildrenPageHasThreeSectionsInOrderEachContactOnceAndNeverTheChildsOwn(t *testing.T) {
	t.Parallel()
	k := newNumbersBox(t)
	scr, _ := k.device(t, "Mia", uaIPhone)
	r := scr.get("/numbers", "de")
	if r.status != http.StatusOK {
		t.Fatalf("GET /numbers = %d\n%s", r.status, r.body)
	}
	page := r.body
	ctHas(t, "page", page, "<h1>Wichtige Nummern</h1>", `<p class="lead">Tippe auf einen Knopf, dann ruft dein Telefon an.</p>`,
		`<h2 id="s-sos">`, "Im Notfall</h2>", `<h2 id="s-fam">`, "Familie</h2>", `<h2 id="s-other">`, "Weitere Nummern</h2>")
	order := []string{"Im Notfall</h2>", "<span>Notruf</span>", "<span>Giftnotruf München</span>", "Familie</h2>",
		">Mama</span>", ">Sebastian</span>", "<span>Oma Gerda</span>", "<span>Opa Heinz</span>", "Weitere Nummern</h2>",
		"<span>Kinderarzt</span>", "<span>Kita Sonnenschein</span>"}
	if at := kidsIndexes(page, order...); !kidsInOrder(at) {
		t.Errorf("order of the page = %v for %v", at, order)
	}
	// Notfall keeps the order of creation, the rest of the groups A to Z, people in the order of the people.
	ctHasNot(t, "page", page, "<span>Mia</span>", "Anna (Mama von Lukas)", "Tante Ilse", "0152 9999 0000", "0151 1111 2222")
	if n := strings.Count(page, "<li class=\"kn\">"); n != 8 {
		t.Errorf("%d cards, want 8 (each contact once)", n)
	}
	// A contact in Notfall and Familie is shown once, in the first section: Giftnotruf is not in a second one.
	if strings.Count(page, "Giftnotruf München</span>") != 1 {
		t.Errorf("a contact shows twice")
	}
	ctHas(t, "the nav", page, `<li><a href="/numbers" aria-current="page">Wichtige Nummern</a></li>`)
}

func TestEveryNumberIsOneTelButtonInTheParentsOrderWithTheKindInWords(t *testing.T) {
	t.Parallel()
	k := newNumbersBox(t)
	scr, _ := k.device(t, "Mia", uaIPhone)
	page := scr.get("/numbers", "de").body
	ctHas(t, "Oma Gerda", page,
		`<ul class="kn-calls"><li><a class="kn-call" href="tel:01713456789" aria-label="Oma Gerda, Mobil anrufen: 0171 3456 789">`,
		`<b>Mobil anrufen</b><span class="no">0171 3456 789</span>`,
		`<b>Gartenhaus anrufen</b><span class="no">0160 111 2223</span>`,
		`aria-label="Oma Gerda, Gartenhaus anrufen: 0160 111 2223"`)
	if at := kidsIndexes(page, "<b>Mobil anrufen</b><span class=\"no\">0171", "<b>Zuhause anrufen</b><span class=\"no\">08031 87 65 43", "<b>Gartenhaus anrufen</b>"); !kidsInOrder(at) {
		t.Errorf("the buttons are not in the order of the numbers: %v", at)
	}
	ctHas(t, "emergency", page, `href="tel:112" aria-label="Notruf, Feuerwehr und Rettung anrufen: 112"`, `href="tel:110"`)
	ctHas(t, "English", func() string { s, _ := k.device(t, "Mia", uaIPhone); return s.get("/numbers", "en").body }(),
		"<h1>Important numbers</h1>", "In an emergency", `<b>Call Feuerwehr und Rettung</b>`, `aria-label="Call Notruf, Feuerwehr und Rettung: 112"`, "More numbers")
}

func TestCardsShowPhotoOrInitialsAndPeopleTheirMarker(t *testing.T) {
	t.Parallel()
	k := newNumbersBox(t)
	_, _, list := k.api.call(http.MethodGet, "/contacts", nil)
	var oma string
	for _, c := range list {
		if m := c.(map[string]any); m["name"] == "Oma Gerda" {
			oma = m["id"].(string)
		}
	}
	if r := k.photoPost("/contacts/"+oma+"/edit", formWith("Oma Gerda", map[string][]string{"for_children": {"1"}, "number": {"0171 3456 789"}, "number_label": {"cell"}, "number_own": {""}}), "oma.jpg", testJPEG(t, 600, 600)); r.status != http.StatusSeeOther {
		t.Fatalf("photo = %d\n%s", r.status, r.body)
	}
	scr, _ := k.device(t, "Mia", uaIPhone)
	page := scr.get("/numbers", "de").body
	ctHas(t, "photo", page, `<span class="ct-photo kid"><img src="/numbers/`+oma+`/photo?v=`)
	ctHas(t, "initials", page, `<span class="ct-photo kid none" aria-hidden="true">N</span><h3><span>Notruf</span></h3>`, `>GM</span>`)
	ctHas(t, "person", page, `<h3><span class="marker" data-person="1" data-shape=`)
	// The child can load the photo of a visible contact and of nobody else.
	if r := scr.get("/numbers/"+oma+"/photo", ""); r.status != http.StatusOK || !strings.HasPrefix(r.body, "\xff\xd8") {
		t.Errorf("child GET photo = %d", r.status)
	}
	if r := scr.get("/contacts/"+oma+"/photo", ""); r.status != http.StatusForbidden {
		t.Errorf("child GET the contact photo = %d, want 403", r.status)
	}
}

func TestOnlyACompletelyVisibleContactsPhotoIsServedToTheChildren(t *testing.T) {
	t.Parallel()
	k := newNumbersBox(t)
	hidden := k.addFor(t, "Versteckt", false, [][3]string{{"cell", "", "0151 1"}})
	if r := k.photoPost("/contacts/"+hidden+"/edit", formWith("Versteckt", map[string][]string{"number": {"0151 111"}, "number_label": {"cell"}, "number_own": {""}}), "x.jpg", testJPEG(t, 300, 300)); r.status != http.StatusSeeOther {
		t.Fatalf("photo = %d", r.status)
	}
	scr, _ := k.device(t, "Mia", uaIPhone)
	if r := scr.get("/numbers/"+hidden+"/photo", ""); r.status != http.StatusNotFound {
		t.Errorf("photo of a hidden contact = %d, want 404", r.status)
	}
	if r := newScreen(k.box).get("/numbers/"+hidden+"/photo", ""); r.status != http.StatusSeeOther {
		t.Errorf("signed out = %d, want the login", r.status)
	}
}

func TestATabletGetsNoCallButtonsButTheNumbersAsTextAndOneSentenceWhy(t *testing.T) {
	t.Parallel()
	k := newNumbersBox(t)
	tablet, _ := k.device(t, "Mia", uaIPad)
	page := tablet.get("/numbers", "de").body
	ctHas(t, "tablet", page, `<div class="notice kn-why" role="note">`, "Mit diesem Tablet kannst du nicht anrufen. Zeig die Nummer einem Erwachsenen oder tippe sie in ein Telefon.",
		`<dl class="kn-nums"><dt>Mobil</dt><dd>0171 3456 789</dd><dt>Zuhause</dt><dd>08031 87 65 43</dd><dt>Gartenhaus</dt><dd>0160 111 2223</dd></dl>`)
	ctHasNot(t, "tablet", page, "kn-call", "tel:", "anrufen:", "Tippe auf einen Knopf")
	for name, ua := range map[string]string{"phone": uaIPhone, "unknown": ""} {
		scr, _ := k.device(t, "Mia", ua)
		p := scr.get("/numbers", "de").body
		ctHas(t, name, p, `class="kn-call"`, "Tippe auf einen Knopf")
		ctHasNot(t, name, p, "kn-why", "kn-nums")
	}
}

func TestWithoutNumbersTheChildIsToldWhomToAskAndTheNavItemStays(t *testing.T) {
	t.Parallel()
	k := newKidsBox(t)
	k.addPerson(t, "Papa", "parent")
	scr, _ := k.device(t, "Mia", uaIPhone)
	page := scr.get("/numbers", "de").body
	ctHas(t, "empty", page, `<div class="kn-empty">`, `<p class="kid">Hier stehen bald Nummern, die du anrufen kannst.</p>`, "<p>Frag Mama oder Papa.</p>",
		`<p class="hint">Für Eltern: Unter Einstellungen › Kontakte bei einem Kontakt „Bei den Kindern zeigen“ einschalten.</p>`, `href="/numbers" aria-current="page"`)
	ctHasNot(t, "empty", page, "kn-sec", `class="lead"`)
	if at := kidsIndexes(page, `href="/meals"`, `href="/numbers"`); !kidsInOrder(at) {
		t.Errorf("the item is not the last of the navigation: %v", at)
	}
}

func TestOnlyAChildsPersonalDeviceHasTheNavItemAndParentsAreSentToTheirOwnPage(t *testing.T) {
	t.Parallel()
	k := newNumbersBox(t)
	if p := k.page("/", "de").body; strings.Contains(p, `href="/numbers"`) {
		t.Error("the parent's navigation has Wichtige Nummern")
	}
	if r := k.get("/numbers"); r.status != http.StatusSeeOther || r.location != "/contacts/for-children" {
		t.Errorf("parent GET /numbers = %d %q", r.status, r.location)
	}
	child := childSession(t, k.box) // a child's session that is no personal device
	if r := child.get("/contacts/for-children"); r.status != http.StatusForbidden {
		t.Errorf("child GET /contacts/for-children = %d, want 403", r.status)
	}
	if r := newScreen(k.box).get("/numbers", "de"); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Errorf("signed out GET /numbers = %d %q", r.status, r.location)
	}
}

func TestNumbersOverRESTHaveTheSectionsAndLeaveOutTheChildsOwnContact(t *testing.T) {
	t.Parallel()
	k := newNumbersBox(t)
	_, child := k.device(t, "Mia", uaIPhone)
	status, got, _ := child.call(http.MethodGet, "/numbers", nil)
	if status != http.StatusOK {
		t.Fatalf("child GET /numbers = %d %v", status, got)
	}
	var keys []string
	var names []string
	for _, s := range got["sections"].([]any) {
		sec := s.(map[string]any)
		keys = append(keys, sec["key"].(string))
		for _, c := range sec["contacts"].([]any) {
			names = append(names, c.(map[string]any)["name"].(string))
		}
	}
	if strings.Join(keys, ",") != "emergency,family,other" || strings.Join(names, ",") != "Notruf,Giftnotruf München,Mama,Sebastian,Oma Gerda,Opa Heinz,Kinderarzt,Kita Sonnenschein" {
		t.Errorf("sections %v, contacts %v", keys, names)
	}
	first := got["sections"].([]any)[0].(map[string]any)["contacts"].([]any)[0].(map[string]any)
	if n := first["numbers"].([]any)[0].(map[string]any); n["kind"] != "custom" || n["label"] != "Feuerwehr und Rettung" || n["value"] != "112" || first["has_photo"] != false {
		t.Errorf("first contact = %v", first)
	}
	// A parent calling it sees everyone, Mia included.
	_, all, _ := k.api.call(http.MethodGet, "/numbers", nil)
	if !strings.Contains(sectionNames(all), "Mia") {
		t.Errorf("a parent's list lacks Mia: %s", sectionNames(all))
	}
	// Nothing but photo, name and numbers.
	for _, s := range got["sections"].([]any) {
		for _, c := range s.(map[string]any)["contacts"].([]any) {
			for f := range c.(map[string]any) {
				if f != "id" && f != "name" && f != "person_id" && f != "has_photo" && f != "numbers" {
					t.Errorf("the children's contact has the field %q", f)
				}
			}
		}
	}
	if status, _, _ := child.call(http.MethodGet, "/contacts", nil); status != http.StatusForbidden {
		t.Errorf("child GET /contacts = %d, want 403", status)
	}
}

func sectionNames(got map[string]any) string {
	var names []string
	for _, s := range got["sections"].([]any) {
		for _, c := range s.(map[string]any)["contacts"].([]any) {
			names = append(names, c.(map[string]any)["name"].(string))
		}
	}
	return strings.Join(names, ",")
}

func TestParentsSeeTheChildrensPageWithNamesToContactsAndEveryHiddenContactByName(t *testing.T) {
	t.Parallel()
	k := newNumbersBox(t)
	page := k.page("/contacts/for-children", "de").body
	ctHas(t, "parent page", page, "<h1>Wichtige Nummern für Kinder</h1>", "So sehen Sebastian und Mia diese Seite auf ihren Geräten unter „Wichtige Nummern“.",
		`<div class="kn-off wait"><h2>Für Kinder an, noch ohne Nummer</h2>`, `<a class="nm" href="/contacts/`, ">Tante Ilse</a>: Die Kinder sehen den Kontakt erst mit einer Telefonnummer.",
		`<div class="kn-off"><h2>Nicht für Kinder sichtbar</h2>`, ">Anna (Mama von Lukas)</a>", `<div class="kn-parent">`,
		`<a class="kn-call" href="tel:112"`, "Im Notfall</h2>", "Weitere Nummern</h2>")
	// Everyone on the list, the children themselves included, and the names link to the contact.
	ctHas(t, "names", page, ">Mia</a>", ">Sebastian</a>", ">Notruf</a>")
	ctHasNot(t, "the page", page, "Hier stehen bald", "Frag ")
	ctHas(t, "crumbs", page, `<span aria-current="page">Wichtige Nummern für Kinder</span>`)
	// No contact is visible yet: the page says how to switch one on, and names every contact as a link.
	e := newKidsBox(t)
	e.addFor(t, "Kinderarzt", false, [][3]string{{"work", "", "089 1234 5670"}})
	empty := e.page("/contacts/for-children", "de").body
	ctHas(t, "empty", empty, "Die Kinder sehen noch keine Nummer.", "schalte „Bei den Kindern zeigen“ ein", `<div class="kn-off"><h2>Nicht für Kinder sichtbar</h2>`, ">Kinderarzt</a>", ">Mama</a>")
	ctHasNot(t, "empty", empty, "kn-sec")
}

// Acceptance (#301): a relation alone does not make a contact visible. "Freundin von Mia" has a number and a
// relation to Mia, but the switch is off, so Mia's device sees nothing of her.
func TestARelationToAChildAloneDoesNotMakeAContactVisibleToTheChildren(t *testing.T) {
	t.Parallel()
	k := newNumbersBox(t)
	mia := k.personID(t, "Mia")
	f := withRels(contactForm("Lenas Freundin", "", [][3]string{{"cell", "", "0151 4040 5050"}}, nil), relLine{word: "Freundin", people: []string{mia}})
	id := k.add(t, f)
	_, got, _ := k.api.call(http.MethodGet, "/contacts/"+id, nil)
	if rels, _ := got["relations"].([]any); len(rels) != 1 || got["for_children"] != false {
		t.Fatalf("the contact has relations %v and for_children %v, want one relation and false", got["relations"], got["for_children"])
	}
	scr, child := k.device(t, "Mia", uaIPhone)
	if page := scr.get("/numbers", "").body; strings.Contains(page, "Lenas Freundin") || strings.Contains(page, "0151 4040 5050") {
		t.Errorf("the page shows the contact because of its relation")
	}
	if _, list, _ := child.call(http.MethodGet, "/numbers", nil); strings.Contains(sectionNames(list), "Lenas Freundin") {
		t.Errorf("GET /api/numbers lists the contact: %s", sectionNames(list))
	}
}

// A token with only the scope numbers reads what the children read, photos included: not the photo of a hidden contact.
func TestANumbersTokenGetsNoPhotoOfAHiddenContactButAContactsTokenDoes(t *testing.T) {
	t.Parallel()
	k := newNumbersBox(t)
	hidden := k.addFor(t, "Versteckt", false, [][3]string{{"cell", "", "0151 1"}})
	if r := k.photoPost("/contacts/"+hidden+"/edit", formWith("Versteckt", map[string][]string{"number": {"0151 111"}, "number_label": {"cell"}, "number_own": {""}}), "x.jpg", testJPEG(t, 300, 300)); r.status != http.StatusSeeOther {
		t.Fatalf("photo = %d", r.status)
	}
	if _, status, _ := k.api.withToken(k.api.newToken("numbers")).callRaw(http.MethodGet, "/numbers/"+hidden+"/photo", nil); status != http.StatusNotFound {
		t.Errorf("numbers token, hidden contact's photo = %d, want 404", status)
	}
	if _, status, _ := k.api.withToken(k.api.newToken("read")).callRaw(http.MethodGet, "/numbers/"+hidden+"/photo", nil); status != http.StatusOK {
		t.Errorf("read token, hidden contact's photo = %d, want 200", status)
	}
}
