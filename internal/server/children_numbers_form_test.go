package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Ticket 301 (preview-301.html): the switch "Bei den Kindern zeigen" in the contact form and what the parents see of it.

// kidsBox is Mama with the children Sebastian and Mia and the groups of Hejmo by key.
type kidsBox struct {
	ctBox
	groups map[string]string
}

func newKidsBox(t *testing.T) kidsBox {
	t.Helper()
	c := newCtBox(t)
	c.addPerson(t, "Sebastian", "child")
	c.addPerson(t, "Mia", "child")
	groups := map[string]string{}
	_, _, list := c.api.call(http.MethodGet, "/contact-groups", nil)
	for _, g := range list {
		m := g.(map[string]any)
		if k, _ := m["shipped_key"].(string); k != "" {
			groups[k] = m["id"].(string)
		}
	}
	return kidsBox{c, groups}
}

// addFor makes a contact through the form; kids switches it on for the children; group keys put it in groups.
func (k kidsBox) addFor(t *testing.T, name string, kids bool, numbers [][3]string, groups ...string) string {
	t.Helper()
	f := contactForm(name, "", numbers, nil)
	if kids {
		f.Set("for_children", "1")
	}
	for _, g := range groups {
		f.Add("group", k.groups[g])
	}
	return k.add(t, f)
}

func TestContactFormHasTheKidCardAfterTheNumbersAndItIsOffForANewContact(t *testing.T) {
	t.Parallel()
	k := newKidsBox(t)
	page := k.page("/contacts/new", "de").body
	ctHas(t, "new contact", page, `<fieldset id="kids" class="kid-card"><legend>Wichtige Nummern für Kinder</legend>`, "Bei den Kindern zeigen",
		"Aus: Die Kinder sehen diesen Kontakt nicht.", "An: Sebastian und Mia sehen den Kontakt unter „Wichtige Nummern“ und können anrufen.",
		"Adresse, E-Mail-Adressen, Geburtstag und Notiz sehen Kinder nie. Eine Verbindung zu einem Kind, etwa „Freundin von Mia“, zeigt einen Kontakt nicht von selbst.")
	card := page[strings.Index(page, `id="kids"`):]
	card = card[:strings.Index(card, "</fieldset>")]
	ctHas(t, "switch", card, `<input type="checkbox" role="switch" name="for_children" value="1" aria-label="Bei den Kindern zeigen">`)
	ctHasNot(t, "a new contact", card, "checked", `class="state is-on"`)
	at := func(s string) int { return strings.Index(page, s) }
	if !(at(`id="rels"`) < at(`id="numbers"`) && at(`id="numbers"`) < at(`id="kids"`) && at(`id="kids"`) < at(`id="mails"`)) {
		t.Errorf("the card is not right after the numbers and before the e-mail addresses")
	}
	ctHas(t, "English", k.page("/contacts/new", "en").body, "Important numbers for children", "Show to the children", "Off: The children do not see this contact.")
	// A person's contact is off as well: parents choose on purpose.
	mama := k.page("/contacts", "de").body
	_ = mama
	_, _, list := k.api.call(http.MethodGet, "/contacts", nil)
	id := list[0].(map[string]any)["id"].(string)
	ctHasNot(t, "Mama's form", k.page("/contacts/"+id+"/edit", "de").body[strings.Index(k.page("/contacts/"+id+"/edit", "de").body, `id="kids"`):], `name="for_children" value="1" checked`)
}

func TestSwitchingOnShowsWhatTheChildrenSeeAndOffWithoutANumberTheNotice(t *testing.T) {
	t.Parallel()
	k := newKidsBox(t)
	id := k.addFor(t, "Oma Gerda", true, [][3]string{{"cell", "", "0171 3456 789"}, {"home", "", "08031 87 65 43"}, {"own", "Gartenhaus", "0160 111 2223"}})
	edit := k.page("/contacts/"+id+"/edit", "de").body
	ctHas(t, "edit", edit, `class="state is-on"`, `name="for_children" value="1" checked`,
		`<p class="kid-sees">Sie sehen: den Namen „Oma Gerda“ und die Nummern Mobil, Zuhause und Gartenhaus.</p>`)
	ctHasNot(t, "edit with numbers", edit, "Noch keine Telefonnummer")
	// On but without a number: a notice, saving still works.
	empty := k.addFor(t, "Tante Ilse", true, nil)
	ctHas(t, "no number", k.page("/contacts/"+empty+"/edit", "de").body, `<p class="notice" role="status">`,
		"Noch keine Telefonnummer. Die Kinder sehen „Tante Ilse“ erst, wenn du oben eine Nummer einträgst.")
}

func TestContactAPIHasTheSwitchOffByDefault(t *testing.T) {
	t.Parallel()
	k := newKidsBox(t)
	status, got, _ := k.api.call(http.MethodPost, "/contacts", map[string]any{"name": "Oma"})
	if status != http.StatusCreated || got["for_children"] != false {
		t.Fatalf("create = %d %v, want for_children false", status, got)
	}
	id := got["id"].(string)
	status, got, _ = k.api.call(http.MethodPut, "/contacts/"+id, map[string]any{"name": "Oma", "for_children": true})
	if status != http.StatusOK || got["for_children"] != true {
		t.Fatalf("switch on = %d %v", status, got)
	}
	status, got, _ = k.api.call(http.MethodPut, "/contacts/"+id, map[string]any{"name": "Oma"})
	if status != http.StatusOK || got["for_children"] != false {
		t.Fatalf("a replace without the field = %d %v, want it off again", status, got)
	}
}

func TestContactPageSaysForChildrenAndTheSuccessLineSaysWhatChanged(t *testing.T) {
	t.Parallel()
	k := newKidsBox(t)
	numbers := [][3]string{{"cell", "", "0171 3456 789"}, {"home", "", "08031 87 65 43"}, {"own", "Gartenhaus", "0160 111 2223"}}
	f := contactForm("Oma Gerda", "", numbers, nil)
	f.Set("for_children", "1")
	r := k.photoPost("/contacts/new", f, "oma.jpg", testJPEG(t, 600, 600))
	if r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	page := k.page(r.location, "de").body
	ctHas(t, "after switching on", page, "„Oma Gerda“ gespeichert, mit neuem Foto. Die Kinder sehen den Kontakt jetzt unter „Wichtige Nummern“.",
		`<dt>Für Kinder</dt><dd><span class="kid-fact">`, "Sichtbar unter „Wichtige Nummern“, mit Foto und allen drei Nummern</span>")
	ctHas(t, "edit with a photo", k.page(r.location+"/edit", "de").body, `Sie sehen: das Foto, den Namen „Oma Gerda“ und die Nummern Mobil, Zuhause und Gartenhaus.`)
	// Off again: the line says so, and the fact says not visible.
	f.Del("for_children")
	r = k.send(r.location+"/edit", f)
	page = k.page(r.location, "de").body
	ctHas(t, "after switching off", page, "„Oma Gerda“ gespeichert. Die Kinder sehen den Kontakt nicht mehr.", "Nicht sichtbar")
	// On without a number.
	g := contactForm("Tante Ilse", "", nil, nil)
	g.Set("for_children", "1")
	r = k.send("/contacts/new", g)
	ctHas(t, "no number", k.page(r.location, "de").body, "Die Kinder sehen den Kontakt, sobald eine Telefonnummer da ist.")
	// A save that changes nothing for the children says nothing about them.
	r = k.send(r.location+"/edit", g)
	ctHasNot(t, "unchanged", k.page(r.location, "de").body, "gespeichert. Die Kinder sehen")
}

func TestContactOverviewTagsFilterAndLinkForChildren(t *testing.T) {
	t.Parallel()
	k := newKidsBox(t)
	k.addFor(t, "Notruf", true, [][3]string{{"own", "Feuerwehr", "112"}}, "notfall")
	k.addFor(t, "Tante Ilse", true, nil, "sonstige")
	k.addFor(t, "Kinderarzt", false, [][3]string{{"work", "", "089 1234 5670"}}, "arztpraxen")
	page := k.page("/contacts", "de").body
	ctHas(t, "Notruf", rowOf(page, "Notruf"), `<span class="tag kid">`, "Für Kinder sichtbar</span>", `<span class="tag grp">Notfall</span>`)
	if r := rowOf(page, "Notruf"); strings.Index(r, "tag kid") > strings.Index(r, "tag grp") {
		t.Error("the tag for children comes after the group tags")
	}
	ctHas(t, "Ilse", rowOf(page, "Tante Ilse"), `<span class="tag kid wait">`, "Für Kinder an, noch ohne Nummer")
	ctHasNot(t, "Kinderarzt", rowOf(page, "Kinderarzt"), "tag kid")
	ctHas(t, "filter and link", page, `href="?g=kids">Für Kinder sichtbar</a>`, `<a href="/contacts/for-children">So sehen es die Kinder</a>`)
	if strings.Index(page, "Gruppen verwalten") > strings.Index(page, "So sehen es die Kinder") {
		t.Error("the link is not next to, after, Gruppen verwalten")
	}
	only := k.page("/contacts?g=kids", "de").body
	ctHas(t, "filtered", only, `aria-current="true">Für Kinder sichtbar</a>`, "<b>Notruf</b>", "<b>Tante Ilse</b>")
	ctHasNot(t, "filtered", only, "<b>Kinderarzt</b>")
}

func TestKidSwitchPostRoundTripKeepsTheSwitchWhenALineIsAdded(t *testing.T) {
	t.Parallel()
	k := newKidsBox(t)
	f := contactForm("Oma", "", nil, nil)
	f.Set("for_children", "1")
	f.Set("more", "number")
	r := k.send("/contacts/new", f)
	ctHas(t, "form again", r.body, `name="for_children" value="1" checked`)
	_ = url.Values{}
}
