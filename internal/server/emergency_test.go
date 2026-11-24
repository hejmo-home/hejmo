package server_test

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 303 (preview-303.html, ADR 0021 Q121): the layout block "Emergency numbers".

// sosFamily is a family box whose group "Notfall" can be filled.
type sosFamily struct {
	*familyBox
	group string
}

func newSOSFamily(t *testing.T) sosFamily {
	t.Helper()
	f := newFamilyBox(t, "eink_color")
	gs, err := f.db.ContactGroups(f.householdID)
	if err != nil {
		t.Fatal(err)
	}
	s := sosFamily{familyBox: f}
	for _, g := range gs {
		if g.ShippedKey == "notfall" {
			s.group = g.ID
		}
	}
	return s
}

func cell(v string) contact.Line { return contact.Line{Kind: contact.Cell, Value: v} }

// add makes a contact of the group "Notfall" with the numbers given.
func (s sosFamily) add(t *testing.T, name string, nums ...contact.Line) store.Contact {
	t.Helper()
	c, err := s.db.CreateContact(s.householdID, s.people["Mama"].ID, store.ContactInput{HejmoName: name, Numbers: nums, GroupIDs: []string{s.group}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// addPerson puts the contact of a person into the group "Notfall" with numbers.
func (s sosFamily) addPerson(t *testing.T, person string, nums ...contact.Line) {
	t.Helper()
	id := s.people[person].ID
	cur, err := s.db.Contact(s.householdID, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.UpdateContact(s.householdID, id, id, store.ContactInput{Numbers: nums, GroupIDs: append(cur.GroupIDs, s.group)}); err != nil {
		t.Fatal(err)
	}
}

func (s sosFamily) switchOn(t *testing.T, on ...string) {
	t.Helper()
	s.saveLayout(t, s.layoutForm(true, []string{"Mama", "Papa", "Sebastian", "Mia"}, append(defaultOn, append(on, "emergency")...)...))
}

var sosTileRe = regexp.MustCompile(`(?s)<div class="tile sos" data-tile="emergency">.*?</ul></div>`)

// The tile lists contacts of the group with a number: further contacts A to Z, then the family in people order;
// every number in its digit groups, the kind only when there are several; a person keeps their marker.
func TestTheEmergencyTileShowsTheGroupInTheOrderOfContactsWithEveryNumber(t *testing.T) {
	t.Parallel()
	f := newSOSFamily(t)
	f.add(t, "Oma Gerda", contact.Line{Kind: contact.Cell, Value: "0171 3456 789"}, contact.Line{Kind: contact.Home, Value: "08031 87 65 43"})
	f.add(t, "Notruf", contact.Line{Kind: contact.Custom, Label: "Feuerwehr und Rettung", Value: "112"}, contact.Line{Kind: contact.Custom, Label: "Polizei", Value: "110"})
	f.add(t, "Frau Huber") // no number: not on the tile
	f.add(t, "Giftnotruf", cell("089 19240"))
	f.addPerson(t, "Papa", cell("0151 9876 5432"))
	f.addPerson(t, "Mama", cell("0160 111 222"))
	f.switchOn(t)
	page := f.view(t, "de", f.at(14, 9, 0))
	tile := sosTileRe.FindString(page)
	if tile == "" {
		t.Fatalf("no emergency tile:\n%s", page)
	}
	var names []string
	for _, m := range regexp.MustCompile(`<p class="nm">(?:<span class="marker"[^>]*></span>)?<span>([^<]*)</span></p>`).FindAllStringSubmatch(tile, -1) {
		names = append(names, m[1])
	}
	if got := strings.Join(names, ","); got != "Giftnotruf,Notruf,Oma Gerda,Mama,Papa" {
		t.Errorf("order = %s", got)
	}
	for _, want := range []string{
		`<span class="kind">Feuerwehr und Rettung</span><span class="no"><span>112</span></span>`,
		`<span class="kind">Polizei</span><span class="no"><span>110</span></span>`,
		`<span class="kind">Mobil</span><span class="no"><span>0171</span> <span>3456</span> <span>789</span></span>`,
		`<span class="kind">Zuhause</span><span class="no"><span>08031</span> <span>87</span> <span>65</span> <span>43</span></span>`,
		`<li><span class="no"><span>089</span> <span>19240</span></span></li>`, // one number: no kind
		`<span class="marker" data-person="`,
		`Notfallnummern</span>`,
		`<ul class="sos-list">`,
	} {
		if !strings.Contains(tile, want) {
			t.Errorf("tile lacks %q:\n%s", want, tile)
		}
	}
	if strings.Contains(tile, "Huber") || strings.Contains(tile, "tel:") || strings.Contains(tile, "<a ") {
		t.Errorf("a contact without number, or a link, on the tile:\n%s", tile)
	}
	if !strings.Contains(tile, `<span class="ct-photo none" aria-hidden="true">N</span>`) {
		t.Errorf("initials without a photo:\n%s", tile)
	}
	if !strings.Contains(page, `<b>Notfallnummern</b>`) {
		t.Errorf("the line of a phone names the tile")
	}
	if en := f.view(t, "en", f.at(14, 9, 0)); !strings.Contains(en, `Emergency numbers</span>`) || !strings.Contains(en, `<span class="kind">Mobile</span>`) {
		t.Errorf("English tile")
	}
}

// Off by default; without a contact with a number the tile stays away; one person chosen has no footer.
func TestTheEmergencyTileIsOffByDefaultHiddenWhenEmptyAndNotInThePersonalView(t *testing.T) {
	t.Parallel()
	f := newSOSFamily(t)
	f.add(t, "Notruf", cell("112"))
	if page := f.view(t, "de", f.at(14, 9, 0)); strings.Contains(page, `data-tile="emergency"`) {
		t.Errorf("tile with the block off")
	}
	f.switchOn(t)
	if page := f.view(t, "de", f.at(14, 9, 0)); !strings.Contains(page, `data-tile="emergency"`) {
		t.Errorf("no tile with the block on")
	}
	f.saveLayout(t, f.layoutForm(false, []string{"Mia"}, append(defaultOn, "emergency")...))
	if page := f.view(t, "de", f.at(14, 9, 0)); strings.Contains(page, `data-tile="emergency"`) || !strings.Contains(page, `data-personal="Mia"`) {
		t.Errorf("personal view shows the footer block:\n%s", page)
	}
	g := newSOSFamily(t)
	g.add(t, "Frau Huber")
	g.switchOn(t)
	if page := g.view(t, "de", g.at(14, 9, 0)); strings.Contains(page, `data-tile="emergency"`) || strings.Contains(page, "Notfallnummern") {
		t.Errorf("an empty tile is shown or named:\n%s", page)
	}
}

// The photo of a contact of the group reaches the display; no other contact's photo does.
func TestADisplayGetsThePhotosOfEmergencyContactsOnly(t *testing.T) {
	t.Parallel()
	f := newSOSFamily(t)
	in := f.add(t, "Notruf", cell("112"))
	out, err := f.db.CreateContact(f.householdID, f.people["Mama"].ID, store.ContactInput{HejmoName: "Nachbarin", Numbers: []contact.Line{cell("0170 1")}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []store.Contact{in, out} {
		if _, err := f.db.SetContactPhoto(f.householdID, c.ID, f.people["Mama"].ID, store.SetPicture, &store.ContactPhoto{JPEG: []byte("\xff\xd8photo")}); err != nil {
			t.Fatal(err)
		}
	}
	f.switchOn(t)
	page := f.view(t, "de", f.at(14, 9, 0))
	m := regexp.MustCompile(`<span class="ct-photo"><img src="(/display/contacts/` + in.ID + `/photo\?v=[^"]+)"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no photo on the tile:\n%s", sosTileRe.FindString(page))
	}
	if r := f.display.get(m[1], "de"); r.status != http.StatusOK || !strings.HasPrefix(r.body, "\xff\xd8") {
		t.Errorf("photo = %d", r.status)
	}
	if r := f.display.get("/display/contacts/"+out.ID+"/photo", "de"); r.status != http.StatusNotFound {
		t.Errorf("a contact outside the group: %d", r.status)
	}
	if r := newScreen(f.box).get("/display/contacts/"+in.ID+"/photo", "de"); r.status != http.StatusForbidden {
		t.Errorf("a display that is not paired: %d", r.status)
	}
	f.saveLayout(t, f.layoutForm(true, []string{"Mama", "Papa", "Sebastian", "Mia"}, defaultOn...))
	if r := f.display.get(m[1], "de"); r.status != http.StatusNotFound {
		t.Errorf("a photo with the block off: %d", r.status)
	}
}

func sosSection(t *testing.T, f sosFamily, lang map[string]string) string {
	t.Helper()
	page := squash(f.settingsPage(t, lang))
	i := strings.Index(page, `<div class="zone z-foot"`)
	if i < 0 {
		t.Fatal("no footer zone")
	}
	return page[i : i+strings.Index(page[i:], "</fieldset>")]
}

// The settings say what will come, in words, whether the block is on or off.
func TestTheSettingsNameWhoAppearsAndWhoLacksANumberWhetherOnOrOff(t *testing.T) {
	t.Parallel()
	f := newSOSFamily(t)
	f.add(t, "Oma Gerda", cell("0171 1"))
	f.add(t, "Notruf", cell("112"))
	f.add(t, "Frau Huber")
	f.addPerson(t, "Papa", cell("0151 2"))
	for _, on := range []bool{false, true} {
		if on {
			f.switchOn(t)
		}
		sec := sosSection(t, f, en)
		text := inText(sec)
		for _, want := range []string{
			"Emergency numbers",
			"From the group “Emergency”: Notruf, Oma Gerda and Papa.",
			"Order as under Contacts: further contacts from A to Z, then your family.",
			"Without a number and so not included: Frau Huber.",
			"Open the group “Emergency” under Contacts",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("on=%v: lacks %q:\n%s", on, want, text)
			}
		}
		if !strings.Contains(sec, `name="b_emergency"`) || !strings.Contains(sec, `data-block="emergency"`) || strings.Contains(sec, "flag warn many") {
			t.Errorf("on=%v: switch missing or a warning for 3 contacts:\n%s", on, sec)
		}
		if on != strings.Contains(sec, `name="b_emergency" checked`) {
			t.Errorf("on=%v: switch state", on)
		}
		if !strings.Contains(sec, `<a class="sos-link" href="/contacts?g=`+f.group+`">`) {
			t.Errorf("link to the group:\n%s", sec)
		}
	}
	if de := inText(sosSection(t, f, map[string]string{"Accept-Language": "de"})); !strings.Contains(de, "Aus der Gruppe „Notfall“: Notruf, Oma Gerda und Papa.") || !strings.Contains(de, "Ohne Nummer und darum nicht dabei: Frau Huber.") {
		t.Errorf("German: %s", de)
	}
}

// More contacts than fit (landscape 3, portrait 4) get a warning in words: nothing is cut.
func TestTheSettingsWarnWhenMoreContactsThanFitInTheRows(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		portrait bool
		n        int
		warn     bool
		want     string
	}{
		{false, 3, false, ""},
		{false, 4, true, "On “Hallway” (landscape) 3 contacts fit large. With 4 the tile needs two rows, and the columns show only about one event. Nothing is cut off."},
		{false, 7, true, "With 7 the tile needs three rows, and the columns show only about one event. If it no longer fits on the screen, you swipe down on the display. Nothing is cut off."},
		{true, 4, false, ""},
		{true, 5, true, "On “Hallway” (portrait) 4 contacts fit large. With 5 the tile needs three rows, and the columns show only about one event. If it no longer fits on the screen, you swipe down on the display. Nothing is cut off."},
	} {
		f := newSOSFamily(t)
		if c.portrait {
			f.display.get("/display/state?orientation=portrait", "en")
		}
		for i := 1; i <= c.n; i++ {
			f.add(t, fmt.Sprintf("Kontakt %d", i), cell(fmt.Sprintf("0170 %d", i)))
		}
		f.add(t, "Ohne Nummer") // does not count: it takes no room
		sec := sosSection(t, f, en)
		if got := strings.Contains(sec, `class="flag warn many"`); got != c.warn {
			t.Errorf("portrait=%v n=%d: warning = %v", c.portrait, c.n, got)
		}
		if c.warn && !strings.Contains(inText(sec), c.want) {
			t.Errorf("portrait=%v n=%d: lacks %q:\n%s", c.portrait, c.n, c.want, inText(sec))
		}
		if c.warn && !strings.Contains(inText(sec), "Fewer contacts in “Emergency” give the columns more room.") {
			t.Errorf("what helps is missing")
		}
	}
}

// Empty: the switch works, one sentence says the tile stays away, with a link to Contacts.
func TestTheSettingsSayTheTileStaysAwayWithoutAContactWithANumber(t *testing.T) {
	t.Parallel()
	f := newSOSFamily(t)
	sec := sosSection(t, f, en)
	if !strings.Contains(inText(sec), "No contact in the group “Emergency” has a number yet. Until then the tile stays off the display. Open contacts") || !strings.Contains(sec, `class="why nosos"`) {
		t.Errorf("empty state:\n%s", sec)
	}
	if strings.Contains(sec, `name="b_emergency" disabled`) || strings.Contains(sec, "sos-sub") {
		t.Errorf("the switch must stay enabled and nothing is listed:\n%s", sec)
	}
	if !strings.Contains(sec, `<a href="/contacts">Open contacts</a>`) {
		t.Errorf("link: %s", sec)
	}
}

// The miniature shows the tile as a row of its own with one square per contact.
func TestTheMiniatureShowsOneSquarePerContact(t *testing.T) {
	t.Parallel()
	f := newSOSFamily(t)
	f.add(t, "A", cell("1"))
	f.add(t, "B", cell("2"))
	f.add(t, "C")
	page := f.settingsPage(t, en)
	m := regexp.MustCompile(`(?s)<div class="m-tile m-sos".*?</span></span></div>`).FindString(page)
	if m == "" || strings.Count(m, "<b></b><s></s>") != 2 || !strings.Contains(m, "Emergency numbers") {
		t.Errorf("miniature:\n%s", m)
	}
}
