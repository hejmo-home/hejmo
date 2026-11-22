package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 198 (Q47): on a child's phone page a parent gets "Pair for another
// person": one confirmation, the old device session ends, a new pairing code
// follows at once for the chosen person.

var radioRe = regexp.MustCompile(`<input type="radio" name="person" value="([^"]+)"( checked)?>`)

// repairFixture is a box with Sebastian's phone paired, next to Mama, Mia.
type repairFixture struct {
	b                     *box
	sebastian, mia, mama  store.Person
	phone                 *screen
	phoneID, repairAction string
}

func newRepairFixture(t *testing.T) repairFixture {
	t.Helper()
	b := signedInBox(t)
	f := repairFixture{b: b, mama: personNamed(t, b, "Mama"), mia: personNamed(t, b, "Mia"), sebastian: personNamed(t, b, "Sebastian")}
	f.phone = pairedPhone(t, b, f.sebastian, "Sebastian's phone")
	f.phoneID = phoneIDRe.FindStringSubmatch(b.get("/phones").body)[1]
	f.repairAction = "/phones/" + f.phoneID + "/repair"
	return f
}

func TestChildsPhonePageOffersPairingForAnotherPersonInOneConfirmation(t *testing.T) {
	t.Parallel()
	f := newRepairFixture(t)
	body := getEN(f.b, "/phones/"+f.phoneID).body
	for _, want := range []string{
		`<details class="repair">`, "<b>Pair for another person</b>", `action="` + f.repairAction + `"`,
		"Who is the phone for now?",
		"<b>Sebastian</b> is signed out on “Sebastian&#39;s phone” at once. Until you pair it again, it works for nobody.",
		"Sebastian’s events, tasks and stars stay as they are.",
		"Yes, pair “Sebastian&#39;s phone” again", "No, change nothing",
		`<details class="danger">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page lacks %q\n%s", want, body)
		}
	}
	// The #197 hint sits at the action, once, and says why the child cannot do it.
	if n := strings.Count(body, "Sebastian cannot sign out on this phone."); n != 1 {
		t.Errorf("hint for parents appears %d times, want once", n)
	}
	if strings.Contains(body, "remove it here and pair it again") {
		t.Error("the old hint is still on the page")
	}
	// A phone is for one person: no whole family, the current person is left out,
	// and nobody is preselected.
	var got []string
	for _, m := range radioRe.FindAllStringSubmatch(body, -1) {
		got = append(got, m[1])
		if m[2] != "" {
			t.Errorf("%s is preselected", m[1])
		}
	}
	if len(got) != 2 || !(got[0] == f.mama.ID && got[1] == f.mia.ID || got[0] == f.mia.ID && got[1] == f.mama.ID) {
		t.Errorf("chips = %v, want Mama and Mia only", got)
	}
	if strings.Contains(body, "whole_family") {
		t.Error("a phone is for exactly one person, but the page offers the whole family")
	}
}

func TestParentsPhonePageHasNoRepairBox(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	pairedPhone(t, b, personNamed(t, b, "Mama"), "Mama's phone")
	id := phoneIDRe.FindStringSubmatch(b.get("/phones").body)[1]
	body := getEN(b, "/phones/"+id).body
	if strings.Contains(body, `class="repair"`) || strings.Contains(body, "Pair for another person") {
		t.Errorf("a parent's phone offers re-pairing:\n%s", body)
	}
	if !strings.Contains(body, `<details class="danger">`) {
		t.Error("a parent's phone lost its remove box")
	}
}

func TestRepairWithoutAPersonChangesNothingAndSaysSo(t *testing.T) {
	t.Parallel()
	f := newRepairFixture(t)
	r := f.b.do(http.MethodPost, f.repairAction, url.Values{}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("repair without a person = %d, want 422\n%s", r.status, r.body)
	}
	for _, want := range []string{`<details class="repair" open>`, `id="err-person"`, "Please choose who the phone is for now. Nothing has changed yet."} {
		if !strings.Contains(r.body, want) {
			t.Errorf("error page lacks %q\n%s", want, r.body)
		}
	}
	if got := f.phone.get("/", "en"); got.status != http.StatusOK {
		t.Errorf("phone after a refused re-pairing = %d, want still signed in", got.status)
	}
	if !strings.Contains(f.b.get("/phones").body, "/phones/"+f.phoneID) {
		t.Error("the phone is gone from the list")
	}
}

func TestRepairRefusesTheCurrentAndUnknownPersons(t *testing.T) {
	t.Parallel()
	f := newRepairFixture(t)
	for name, person := range map[string]string{"current person": f.sebastian.ID, "unknown": "nobody"} {
		if r := f.b.post(f.repairAction, url.Values{"person": {person}}); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-person"`) {
			t.Errorf("%s = %d, want 422 with the error at the chips", name, r.status)
		}
	}
	if got := f.phone.get("/", "en"); got.status != http.StatusOK {
		t.Errorf("phone = %d, want still signed in", got.status)
	}
	if r := f.b.post("/phones/nope/repair", url.Values{"person": {f.mia.ID}}); r.status != http.StatusNotFound {
		t.Errorf("unknown phone = %d, want 404", r.status)
	}
}

func TestRepairEndsTheOldSessionAndShowsTheNewCodeForThePerson(t *testing.T) {
	t.Parallel()
	f := newRepairFixture(t)
	r := f.b.do(http.MethodPost, f.repairAction, url.Values{"person": {f.mia.ID}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusOK {
		t.Fatalf("repair = %d\n%s", r.status, r.body)
	}
	for _, want := range []string{
		`data-success`, "“Sebastian&#39;s phone” is signed out. Scan the code now.",
		`<section class="once"`, "Scan this code with the camera of the phone that used to be called “Sebastian&#39;s phone”.",
		"Afterwards it is for", `data-person="`, ">Mia<", `data-join-url=`, "Works once, until",
		"Check for the phone",
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("answer lacks %q\n%s", want, r.body)
		}
	}
	// One main action: no "Add a phone" while the code is shown, and the old phone is gone.
	for _, gone := range []string{"Show QR code", "/phones/" + f.phoneID} {
		if strings.Contains(r.body, gone) {
			t.Errorf("answer still has %q", gone)
		}
	}
	if got := f.phone.get("/", "en"); got.status != http.StatusSeeOther || got.location != "/login" {
		t.Errorf("old phone = %d %q, want signed out", got.status, got.location)
	}
	if got := f.phone.get("/api/v1/me", "en"); got.status != http.StatusUnauthorized {
		t.Errorf("old phone's API call = %d, want 401", got.status)
	}
	// A reload shows the ordinary page, not the code again.
	if again := f.b.get("/phones").body; strings.Contains(again, "data-join-url") {
		t.Error("the code is still on the page after a reload")
	}
}

func TestRepairedPhoneShowsWhoItIsForAndTheParentConfirmsWithThemPreselected(t *testing.T) {
	t.Parallel()
	f := newRepairFixture(t)
	r := f.b.post(f.repairAction, url.Values{"person": {f.mia.ID}})
	path := joinPath(t, r.body)
	phone := newScreen(f.b)
	waiting := phone.get(path, "en")
	if waiting.status != http.StatusOK || !strings.Contains(waiting.body, `class="invitee join-for"`) || !strings.Contains(waiting.body, ">Mia<") {
		t.Fatalf("waiting phone does not say it is for Mia:\n%s", waiting.body)
	}
	page := getEN(f.b, "/phones").body
	checked := ""
	for _, m := range radioRe.FindAllStringSubmatch(page, -1) {
		if m[2] != "" {
			checked = m[1]
		}
	}
	if checked != f.mia.ID {
		t.Errorf("the confirmation preselects %q, want Mia", checked)
	}
	if !strings.Contains(page, `value="Mia’s phone"`) {
		t.Errorf("the name is not prefilled with Mia’s phone:\n%s", page)
	}
	// The parent only compares the number and confirms.
	m := pairingIDRe.FindStringSubmatch(page)
	if c := f.b.post("/phones/pairings/"+m[1]+"/confirm", url.Values{"person": {f.mia.ID}, "name": {"Mia’s phone"}}); c.status != http.StatusSeeOther {
		t.Fatalf("confirm = %d\n%s", c.status, c.body)
	}
	if done := phone.get(path, "en"); done.status != http.StatusSeeOther {
		t.Fatalf("phone after confirmation = %d", done.status)
	}
	if me := phone.get("/api/v1/me", "en"); !strings.Contains(me.body, `"Mia"`) {
		t.Errorf("phone is not Mia's: %s", me.body)
	}
}

// The ordinary pairing keeps asking: nobody is preselected, no name is filled in.
func TestOrdinaryPairingIsNotPrefilled(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	path := showCode(t, b)
	newScreen(b).get(path, "en")
	page := getEN(b, "/phones").body
	for _, m := range radioRe.FindAllStringSubmatch(page, -1) {
		if m[2] != "" {
			t.Errorf("%s is preselected", m[1])
		}
	}
	if !strings.Contains(page, `class="chips"`) {
		t.Error("the waiting form does not use person chips")
	}
	if strings.Contains(page, `name="name" type="text" value="`) && !strings.Contains(page, `value=""`) {
		t.Error("the name is prefilled for an ordinary pairing")
	}
}

func TestPhoneNameFollowsTheLanguagesRuleForTheOwner(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ person, en, de string }{
		{"Mia", "Mia’s phone", "Mias Handy"},
		{"Jonas", "Jonas’s phone", "Jonas’ Handy"},
		{"Max", "Max’s phone", "Max’ Handy"},
		{"Moritz", "Moritz’s phone", "Moritz’ Handy"},
	} {
		f := newRepairFixture(t)
		who := personNamed(t, f.b, tc.person)
		path := joinPath(t, f.b.post(f.repairAction, url.Values{"person": {who.ID}}).body)
		newScreen(f.b).get(path, "en")
		if page := getEN(f.b, "/phones").body; !strings.Contains(page, `value="`+tc.en+`"`) {
			t.Errorf("%s: English name is not %q", tc.person, tc.en)
		}
		if page := f.b.do(http.MethodGet, "/phones", nil, map[string]string{"Accept-Language": "de"}).body; !strings.Contains(page, `value="`+tc.de+`"`) {
			t.Errorf("%s: German name is not %q", tc.person, tc.de)
		}
	}
}

func TestRepairPagesAreGerman(t *testing.T) {
	t.Parallel()
	f := newRepairFixture(t)
	de := map[string]string{"Accept-Language": "de"}
	body := f.b.do(http.MethodGet, "/phones/"+f.phoneID, nil, de).body
	for _, want := range []string{
		"<b>Für eine andere Person koppeln</b>",
		"Bekommt jemand anderes dieses Handy? Hier koppelst du es in einem Schritt neu. Sebastian kann sich auf dem Handy nicht selbst abmelden.",
		"Für wen ist das Handy jetzt?",
		"<b>Sebastian</b> ist auf „Sebastian&#39;s phone“ sofort abgemeldet. Bis du es neu koppelst, funktioniert es für niemanden.",
		"Sebastians Termine, Aufgaben und Sterne bleiben, wie sie sind.",
		"Ja, „Sebastian&#39;s phone“ neu koppeln", "Nein, nichts ändern",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("German detail page lacks %q", want)
		}
	}
	r := f.b.do(http.MethodPost, f.repairAction, url.Values{"person": {f.mia.ID}}, de)
	for _, want := range []string{
		"„Sebastian&#39;s phone“ ist abgemeldet. Scanne jetzt den Code.",
		"Scanne diesen Code mit der Kamera des Handys, das bisher „Sebastian&#39;s phone“ hieß.",
		"Danach ist es für", "Funktioniert einmal, bis", "Das Handy zeigt dann eine Zahl. Bestätige es hier, wenn sie gleich ist.",
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("German answer lacks %q\n%s", want, r.body)
		}
	}
	path := joinPath(t, r.body)
	if w := newScreen(f.b).get(path, "de"); !strings.Contains(w.body, ">Für<") {
		t.Errorf("German waiting page lacks the 'Für' label:\n%s", w.body)
	}
}

func TestOnlyParentsRepairPhones(t *testing.T) {
	t.Parallel()
	cp := newChildPhone(t)
	h, _ := cp.parent.b.db.HomeHousehold()
	phones, _ := cp.parent.b.db.Phones(h.ID)
	mama := personNamed(t, cp.parent.b, "Mama")
	if r := cp.screen.post("/phones/"+phones[0].ID+"/repair", url.Values{"person": {mama.ID}}); r.status != http.StatusForbidden {
		t.Errorf("a child's phone re-pairs itself = %d, want 403", r.status)
	}
	if status, _, _ := cp.phone.call(http.MethodPost, "/phones/"+phones[0].ID+"/repair", map[string]any{"person_id": mama.ID}); status != http.StatusForbidden {
		t.Errorf("a child's phone re-pairs itself through the API = %d, want 403", status)
	}
	if r := cp.screen.get("/", "en"); r.status != http.StatusOK {
		t.Errorf("the child's phone lost its session: %d", r.status)
	}
}

// ADR 0015: everything the web interface can do is also possible through the API.
func TestRepairingAPhoneWorksThroughTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	mia, sebastian := personNamed(t, c.b, "Mia"), personNamed(t, c.b, "Sebastian")
	phone := pairedPhone(t, c.b, sebastian, "Sebastian's phone")
	_, _, list := c.call(http.MethodGet, "/phones", nil)
	id := list[0].(map[string]any)["id"].(string)

	if status, _, _ := c.call(http.MethodPost, "/phones/"+id+"/repair", map[string]any{"person_id": "00000000-0000-4000-8000-000000000000"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown person = %d, want 422", status)
	}
	if status, _, _ := c.call(http.MethodPost, "/phones/"+id+"/repair", map[string]any{"person_id": sebastian.ID}); status != http.StatusUnprocessableEntity {
		t.Fatalf("same person = %d, want 422", status)
	}
	if status, _, _ := c.call(http.MethodPost, "/phones/00000000-0000-4000-8000-000000000000/repair", map[string]any{"person_id": mia.ID}); status != http.StatusNotFound {
		t.Fatalf("unknown phone = %d, want 404", status)
	}
	if r := phone.get("/api/v1/me", "en"); r.status != http.StatusOK {
		t.Fatalf("refused re-pairings ended the session: %d", r.status)
	}
	status, shown, _ := c.call(http.MethodPost, "/phones/"+id+"/repair", map[string]any{"person_id": mia.ID})
	if status != http.StatusCreated || shown["id"] == "" || shown["url"] == "" || shown["expires_at"] == "" || shown["person_id"] != mia.ID {
		t.Fatalf("repair = %d %v", status, shown)
	}
	if r := phone.get("/api/v1/me", "en"); r.status != http.StatusUnauthorized {
		t.Fatalf("old session = %d, want 401", r.status)
	}
	if _, _, list := c.call(http.MethodGet, "/phones", nil); len(list) != 0 {
		t.Fatalf("phones after re-pairing = %v", list)
	}
	newScreen(c.b).get(joinPath(t, `data-join-url="`+shown["url"].(string)+`"`), "en")
	_, _, waiting := c.call(http.MethodGet, "/phones/pairings", nil)
	if len(waiting) != 1 || waiting[0].(map[string]any)["for_person_id"] != mia.ID {
		t.Fatalf("waiting = %v, want one for Mia", waiting)
	}
}

func TestOnlyScopedParentsRepairPhonesThroughTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	ro := c.withToken(c.newToken("read"))
	if status, _, _ := ro.call(http.MethodPost, "/phones/00000000-0000-4000-8000-000000000000/repair", map[string]any{"person_id": "x"}); status != http.StatusForbidden {
		t.Fatalf("read token re-pairs = %d, want 403", status)
	}
}
