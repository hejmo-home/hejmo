package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

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
	b, r, codes := wizardCodes(t)
	for _, want := range []string{
		`<form class="rc" method="post" action="/account/recovery-codes/saved" data-rc>`,
		"<h2>Your 10 codes</h2>",
		`<p class="rc-for">For <b>mama</b> on <b>hejmo.local</b>, created on <b>14.11.2026</b></p>`,
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
