package server_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
)

// Ticket 176. Everything below goes through the HTTP seam; the PDF itself is
// made in the browser and tested in web/test.

// wizardCodes runs the first setup and returns the recovery codes page (English).
func wizardCodes(t *testing.T) (*box, resp, []string) {
	t.Helper()
	b := newBox(t)
	b.post("/setup", validSetupForm())
	secret := secretOn(t, b.get("/setup/totp").body)
	code, _ := auth.TOTPCode(secret, b.now)
	r := b.do(http.MethodPost, "/setup/totp", url.Values{"code": {code}}, enHeader)
	codes := recoveryCodesOn(t, r.body)
	if len(codes) != 10 {
		t.Fatalf("setup shows %d codes\n%s", len(codes), r.body)
	}
	return b, r, codes
}

func savedForm(flow string, codes []string, tick bool) url.Values {
	f := url.Values{"flow": {flow}, "codes": codes}
	if tick {
		f.Set("saved", "1")
	}
	return f
}

// The card: who and where, the two ways to keep the codes, and the gate.
func TestRecoveryCodesCardHasPDFPrintAndTheGate(t *testing.T) {
	t.Parallel()
	b, r, codes := wizardCodes(t)
	berlin, _ := time.LoadLocation("Europe/Berlin")
	acc, _ := b.db.AccountByUsername("mama")
	set, _ := b.db.RecoverySet(acc.PersonID)
	madeOn := set.CreatedAt.In(berlin).Format("02.01.2006") // the set's own date, not the box clock's
	for _, want := range []string{
		`<form class="rc" method="post" action="/account/recovery-codes/saved" data-rc>`,
		"<h2>Your 10 codes</h2>",
		`<p class="rc-for">For <b>mama</b> on <b>hejmo.local</b>, created on <b>` + madeOn + `</b></p>`,
		"Download as PDF", "data-rc-pdf", `data-print>`, "Print",
		"Hejmo keeps neither the codes nor the PDF.",
		`<input type="checkbox" name="saved" value="1">`, "I have saved my recovery codes.", "PDF, printout, photo or password manager",
		`type="submit" aria-disabled="true">Continue to the first steps`, "You can continue once you have ticked the box.",
		`<input type="hidden" name="flow" value="wizard">`, `<script type="application/json" data-rc-doc>`, `/static/recovery-pdf.js`,
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("card misses %q\n%s", want, r.body)
		}
	}
	for _, c := range codes {
		if !strings.Contains(r.body, `<input type="hidden" name="codes" value="`+c+`">`) {
			t.Errorf("code %s is not carried for a refused submit", c)
		}
	}
	if strings.Contains(r.body, "required") {
		t.Error("the checkbox is required by the browser: the server and the script must explain, not the browser bubble")
	}
	if !strings.Contains(b.get("/static/recovery-pdf.js").body, "hejmoRecoveryPdf") {
		t.Error("the PDF script is not served")
	}
}

// Without the tick the box refuses, shows the same codes and says why at the field.
func TestContinueWithoutTheTickIsRefusedAtTheField(t *testing.T) {
	t.Parallel()
	b, _, codes := wizardCodes(t)
	r := b.do(http.MethodPost, "/account/recovery-codes/saved", savedForm("wizard", codes, false), enHeader)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("no tick = %d, want 422", r.status)
	}
	for _, want := range []string{
		`aria-invalid="true"`, `id="err-saved"`, "Tick the box once you have saved the codes.",
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("refusal misses %q\n%s", want, r.body)
		}
	}
	if got := recoveryCodesOn(t, r.body); strings.Join(got, ",") != strings.Join(codes, ",") {
		t.Errorf("the same codes must stay on screen: %v vs %v", got, codes)
	}
	acc, _ := b.db.AccountByUsername("mama")
	if set, _ := b.db.RecoverySet(acc.PersonID); !set.SavedAt.IsZero() {
		t.Error("the set counts as saved without the tick")
	}
}

func TestTheTickLetsTheWizardContinueAndMarksTheSetSaved(t *testing.T) {
	t.Parallel()
	b, _, codes := wizardCodes(t)
	r := b.do(http.MethodPost, "/account/recovery-codes/saved", savedForm("wizard", codes, true), enHeader)
	if r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("ticked = %d %q, want 303 to /", r.status, r.location)
	}
	acc, _ := b.db.AccountByUsername("mama")
	if set, _ := b.db.RecoverySet(acc.PersonID); set.SavedAt.IsZero() {
		t.Error("the set is not marked as saved")
	}
	if home := b.do(http.MethodGet, "/", nil, enHeader); !strings.Contains(home.body, "Welcome, Mama!") {
		t.Errorf("the welcome line is gone after the gate\n%s", home.body)
	}
}

// Outside the wizard the page is in the frame, has no back link, and leads back to My account.
func TestNewCodesOnMyAccountHaveNoBackLinkAndLeadBackWithASuccessLine(t *testing.T) {
	t.Parallel()
	b, _, codes := wizardCodes(t)
	b.do(http.MethodPost, "/account/recovery-codes/saved", savedForm("wizard", codes, true), enHeader)
	r := b.do(http.MethodPost, "/account/recovery-codes", url.Values{}, enHeader)
	for _, want := range []string{`class="frame-nav"`, "New recovery codes created. The old ones no longer work.", `type="submit" aria-disabled="true">Done, back to My account`, `name="flow" value="account"`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("frame card misses %q\n%s", want, r.body)
		}
	}
	if strings.Contains(r.body, `class="back"`) {
		t.Error("a back link would skip the gate")
	}
	fresh := recoveryCodesOn(t, r.body)
	// Not ticked: the same page, no success line of "saved".
	if r := b.do(http.MethodPost, "/account/recovery-codes/saved", savedForm("account", fresh, false), enHeader); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `class="frame-nav"`) {
		t.Fatalf("frame without tick = %d", r.status)
	}
	done := b.do(http.MethodPost, "/account/recovery-codes/saved", savedForm("account", fresh, true), enHeader)
	if done.status != http.StatusSeeOther || done.location != "/account" {
		t.Fatalf("ticked in the frame = %d %q", done.status, done.location)
	}
	acct := b.do(http.MethodGet, "/account", nil, enHeader)
	if !strings.Contains(acct.body, "Recovery codes saved. The old ones no longer work.") || strings.Contains(acct.body, "Not confirmed as saved") {
		t.Errorf("My account after the tick\n%s", acct.body)
	}
}

// Leaving without the tick is caught later, in words, on My account.
func TestMyAccountSaysWhenTheCodesWereNeverConfirmed(t *testing.T) {
	t.Parallel()
	b, _, _ := wizardCodes(t)
	acct := b.do(http.MethodGet, "/account", nil, enHeader)
	for _, want := range []string{`<span class="flag warn">`, "Not confirmed as saved", "Did you not save the codes from ", "? Create new ones; the current ones become invalid.", `name="fresh" value="1"`} {
		if !strings.Contains(acct.body, want) {
			t.Errorf("My account misses %q\n%s", want, acct.body)
		}
	}
}

// A reload before the tick gives a new set and says so; a deliberate "new codes"
// from My account (which carries "fresh") does not claim a reload.
func TestReloadBeforeTheTickMakesANewSetAndSaysSo(t *testing.T) {
	t.Parallel()
	b, _, first := wizardCodes(t)
	again := b.do(http.MethodPost, "/account/recovery-codes", url.Values{}, enHeader)
	second := recoveryCodesOn(t, again.body)
	if len(second) != 10 || second[0] == first[0] {
		t.Fatalf("no new set after a reload: %v", second)
	}
	if !strings.Contains(again.body, `<p class="notice top"`) || !strings.Contains(again.body, "The page was reloaded.") {
		t.Errorf("no reload notice\n%s", again.body)
	}
	deliberate := b.do(http.MethodPost, "/account/recovery-codes", url.Values{"fresh": {"1"}}, enHeader)
	if strings.Contains(deliberate.body, "notice top") {
		t.Error("a deliberate new set claims the page was reloaded")
	}
	// The old set is void.
	acc, _ := b.db.AccountByUsername("mama")
	set, _ := b.db.RecoverySet(acc.PersonID)
	if set.Unused != 10 {
		t.Errorf("%d codes valid, want only the newest 10", set.Unused)
	}
}

func TestReloadOfTheOneTimeCodeStepInTheWizardGivesANewSet(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	b.post("/setup", validSetupForm())
	secret := secretOn(t, b.get("/setup/totp").body)
	code, _ := auth.TOTPCode(secret, b.now)
	first := recoveryCodesOn(t, b.do(http.MethodPost, "/setup/totp", url.Values{"code": {code}}, enHeader).body)
	again := b.do(http.MethodPost, "/setup/totp", url.Values{"code": {code}}, enHeader)
	second := recoveryCodesOn(t, again.body)
	if again.status != http.StatusOK || len(second) != 10 || second[0] == first[0] || !strings.Contains(again.body, "The page was reloaded.") || !strings.Contains(again.body, "Step 3 of 4") {
		t.Fatalf("reload in the wizard = %d\n%s", again.status, again.body)
	}
	// Once saved, a repeated POST goes on like before.
	b.do(http.MethodPost, "/account/recovery-codes/saved", savedForm("wizard", second, true), enHeader)
	if r := b.post("/setup/totp", url.Values{"code": {code}}); r.status != http.StatusSeeOther || r.location != "/" {
		t.Errorf("after the tick = %d %q", r.status, r.location)
	}
}

// Codes that do not look like codes are never echoed: a crafted POST gets a new set instead.
func TestARefusedSubmitNeverEchoesAnythingButCodes(t *testing.T) {
	t.Parallel()
	b, _, _ := wizardCodes(t)
	r := b.do(http.MethodPost, "/account/recovery-codes/saved", url.Values{"flow": {"wizard"}, "codes": {"<script>alert(1)</script>"}}, enHeader)
	if strings.Contains(r.body, "alert(1)") {
		t.Fatal("a crafted value is echoed")
	}
	if r.status != http.StatusUnprocessableEntity || len(recoveryCodesOn(t, r.body)) != 10 {
		t.Errorf("crafted POST = %d with %d codes", r.status, len(recoveryCodesOn(t, r.body)))
	}
}

// API parity (ADR 0015): the same confirmation over the API.
func TestAPIConfirmsTheRecoveryCodesAsSaved(t *testing.T) {
	t.Parallel()
	b, secret, _ := setupWithCodes(t)
	loginWith(b, "mama", "correct horse battery", totpNow(b, secret))
	c := newAPIClient(t, b)
	c.cookie = true
	if status, body, _ := c.call("POST", "/me/recovery-codes", nil); status != http.StatusCreated {
		t.Fatalf("new codes = %d %v", status, body)
	}
	if status, _, _ := c.call("POST", "/me/recovery-codes/saved", map[string]any{"saved": false}); status != http.StatusUnprocessableEntity {
		t.Fatalf("saved:false = %d, want 422", status)
	}
	acc, _ := b.db.AccountByUsername("mama")
	if set, _ := b.db.RecoverySet(acc.PersonID); !set.SavedAt.IsZero() {
		t.Fatal("saved before the confirmation")
	}
	if status, body, _ := c.call("POST", "/me/recovery-codes/saved", map[string]any{"saved": true}); status != http.StatusNoContent {
		t.Fatalf("saved:true = %d %v, want 204", status, body)
	}
	if set, _ := b.db.RecoverySet(acc.PersonID); set.SavedAt.IsZero() {
		t.Fatal("not saved after the confirmation")
	}
}

// docOn parses the JSON the card carries for the PDF script.
func docOn(t *testing.T, page string) map[string]any {
	t.Helper()
	m := regexp.MustCompile(`(?s)<script type="application/json" data-rc-doc>(.*?)</script>`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the card carries no data-rc-doc\n%s", page)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(m[1]), &doc); err != nil {
		t.Fatalf("data-rc-doc is no JSON: %v\n%s", err, m[1])
	}
	return doc
}

// The PDF's file name has the box name and the date; the texts and the document
// language follow the page language; a box name with unsafe characters is made file-safe.
func TestPDFDocHasFileNameTitleLabelsAndLanguageInGermanAndEnglish(t *testing.T) {
	t.Parallel()
	b, _, _ := wizardCodes(t)
	b.public.name = "Küche Box/..\\x:y\"<z>.example"
	// The dates are the set's own created_at (as My account shows it), not the
	// box clock, which is on another day here.
	berlin, _ := time.LoadLocation("Europe/Berlin")
	acc, _ := b.db.AccountByUsername("mama")
	set, _ := b.db.RecoverySet(acc.PersonID)
	created := set.CreatedAt.In(berlin)
	iso, shown := created.Format("2006-01-02"), created.Format("02.01.2006")
	for _, tc := range []struct {
		lang, file, title, box, user, created, page, prefixOK string
	}{
		{"en", "Hejmo-Recovery-codes-k-che-box-..-x-y--z-.example-mama-" + iso, "Recovery codes for Mama", "Box", "Username", "Created on", "Recovery codes · page 1 of 1", "Hejmo-Recovery-codes-"},
		{"de", "Hejmo-Wiederherstellungscodes-k-che-box-..-x-y--z-.example-mama-" + iso, "Wiederherstellungscodes für Mama", "Box", "Benutzername", "Erstellt am", "Wiederherstellungscodes · Seite 1 von 1", "Hejmo-Wiederherstellungscodes-"},
	} {
		r := b.do(http.MethodPost, "/account/recovery-codes", url.Values{"fresh": {"1"}}, map[string]string{"Accept-Language": tc.lang})
		if r.status != http.StatusOK {
			t.Fatalf("%s: new codes = %d\n%s", tc.lang, r.status, r.body)
		}
		doc := docOn(t, r.body)
		want := map[string]string{"file": tc.file, "title": tc.title, "boxLabel": tc.box, "userLabel": tc.user, "dateLabel": tc.created, "page": tc.page, "username": "mama", "date": shown, "lang": tc.lang}
		for k, v := range want {
			if doc[k] != v {
				t.Errorf("%s: doc[%q] = %v, want %q", tc.lang, k, doc[k], v)
			}
		}
		if file, _ := doc["file"].(string); strings.ContainsAny(file, "/\\:\"<>? ") || !strings.HasPrefix(file, tc.prefixOK) {
			t.Errorf("%s: file name %q is not file-safe", tc.lang, file)
		}
		if how, _ := doc["how"].([]any); len(how) != 5 {
			t.Errorf("%s: %d how-to lines, want 5", tc.lang, len(how))
		}
	}
}

var voidCodes = []string{"AAAAA-BBBBB", "CCCCC-DDDDD"}

func sameCodes(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

// Codes that are well-formed but not the box's current set (a stale tab, the
// Back button) are never shown as "the same codes": the box makes a new set
// and says so. This holds without and with the tick; the tick never confirms
// a set the person did not see.
func TestCodesThatAreNotTheCurrentSetGetANewSetAndNeverCountAsSaved(t *testing.T) {
	t.Parallel()
	for _, tick := range []bool{false, true} {
		b, _, current := wizardCodes(t)
		r := b.do(http.MethodPost, "/account/recovery-codes/saved", savedForm("wizard", voidCodes, tick), enHeader)
		shown := recoveryCodesOn(t, r.body)
		if len(shown) != 10 || strings.Contains(r.body, voidCodes[0]) || sameCodes(shown, current) {
			t.Errorf("tick=%v: void codes were shown or the old set was kept: %v", tick, shown)
		}
		if !strings.Contains(r.body, "These codes are no longer valid, here is a new set.") || strings.Contains(r.body, "The page was reloaded.") {
			t.Errorf("tick=%v: no stale notice\n%s", tick, r.body)
		}
		if r.status == http.StatusSeeOther {
			t.Errorf("tick=%v: void codes let the wizard continue", tick)
		}
		acc, _ := b.db.AccountByUsername("mama")
		set, _ := b.db.RecoverySet(acc.PersonID)
		if !set.SavedAt.IsZero() || set.Unused != 10 {
			t.Errorf("tick=%v: saved=%v unused=%d after void codes", tick, set.SavedAt, set.Unused)
		}
		// The new set is the one on screen: ticking it works.
		if done := b.do(http.MethodPost, "/account/recovery-codes/saved", savedForm("wizard", shown, true), enHeader); done.status != http.StatusSeeOther {
			t.Errorf("tick=%v: ticking the new set = %d", tick, done.status)
		}
	}
}

// A tab with the previous set, ticked after a newer set was made, confirms nothing.
func TestTheTickOfAnOlderSetDoesNotConfirmTheNewerOne(t *testing.T) {
	t.Parallel()
	b, _, first := wizardCodes(t)
	second := recoveryCodesOn(t, b.do(http.MethodPost, "/account/recovery-codes", url.Values{}, enHeader).body)
	r := b.do(http.MethodPost, "/account/recovery-codes/saved", savedForm("wizard", first, true), enHeader)
	if r.status == http.StatusSeeOther {
		t.Fatalf("the old set's tick let the wizard continue")
	}
	acc, _ := b.db.AccountByUsername("mama")
	if set, _ := b.db.RecoverySet(acc.PersonID); !set.SavedAt.IsZero() {
		t.Error("the newer set counts as saved though the person saw the older one")
	}
	third := recoveryCodesOn(t, r.body)
	if sameCodes(third, first) || sameCodes(third, second) {
		t.Errorf("the page shows a set that is void or was not made new: %v", third)
	}
}

// The refused card on My account says nothing about success.
func TestRefusedCardOnMyAccountShowsNoSuccessLine(t *testing.T) {
	t.Parallel()
	b, _, codes := wizardCodes(t)
	b.do(http.MethodPost, "/account/recovery-codes/saved", savedForm("wizard", codes, true), enHeader)
	fresh := recoveryCodesOn(t, b.do(http.MethodPost, "/account/recovery-codes", url.Values{"fresh": {"1"}}, enHeader).body)
	r := b.do(http.MethodPost, "/account/recovery-codes/saved", savedForm("account", fresh, false), enHeader)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "Tick the box once you have saved the codes.") {
		t.Fatalf("refused = %d", r.status)
	}
	if strings.Contains(r.body, "New recovery codes created") {
		t.Errorf("a refused card shows the success line\n%s", r.body)
	}
}

// The invited parent's last step has the same gate.
func TestTheGateAlsoHoldsInTheInviteFlow(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	token := e.create()
	e.b.logout()
	e.en(http.MethodPost, "/invite/"+token+"/account", acceptForm("papa", "papas long password"))
	secret := secretOn(t, e.en(http.MethodGet, "/setup/totp", nil).body)
	code, _ := auth.TOTPCode(secret, e.b.now)
	card := e.en(http.MethodPost, "/setup/totp", url.Values{"code": {code}})
	codes := recoveryCodesOn(t, card.body)
	if len(codes) != 10 || !strings.Contains(card.body, `name="flow" value="invite"`) {
		t.Fatalf("invite card has %d codes\n%s", len(codes), card.body)
	}
	r := e.en(http.MethodPost, "/account/recovery-codes/saved", savedForm("invite", codes, false))
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-saved"`) || !sameCodes(recoveryCodesOn(t, r.body), codes) {
		t.Fatalf("invite without tick = %d\n%s", r.status, r.body)
	}
	done := e.en(http.MethodPost, "/account/recovery-codes/saved", savedForm("invite", codes, true))
	if done.status != http.StatusSeeOther || done.location != "/" {
		t.Fatalf("invite with tick = %d %q", done.status, done.location)
	}
	acc, _ := e.b.db.AccountByUsername("papa")
	if set, _ := e.b.db.RecoverySet(acc.PersonID); set.SavedAt.IsZero() {
		t.Error("the invited parent's set is not marked as saved")
	}
}
