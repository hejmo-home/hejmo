package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/publiccert"
	"github.com/hejmo-home/hejmo/internal/server"
)

func deGet(b *box, path string) resp {
	return b.do(http.MethodGet, path, nil, map[string]string{"Accept-Language": "de"})
}

func inText(page string) string {
	return strings.Join(strings.Fields(tagRe.ReplaceAllString(page, " ")), " ")
}

// Ticket 133: the first page lists the four steps, says what you need and has
// one main button to the HTTPS step. It has no step number yet.
func TestWelcomePageListsTheFourStepsAndWhatYouNeed(t *testing.T) {
	b := newFreshBox(t)
	r := deGet(b, "/setup")
	text := inText(r.body)
	for _, want := range []string{
		"Willkommen bei Hejmo", "etwa 15 Minuten", "So läuft die Einrichtung", "Sichere Verbin\u00addung", "Dein Konto", "Einmal\u00adcode", "Erste Schritte",
		"Das brauchst du", "Authenticator-App", "deSEC", "Einrichtung starten",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("welcome page misses %q", want)
		}
	}
	if !strings.Contains(r.body, `<ol class="howto">`) || strings.Count(r.body, `class="button"`) != 1 || !strings.Contains(r.body, `class="button" href="/setup/https">Einrichtung starten`) {
		t.Error("the welcome page does not have its steps list and one main button to /setup/https")
	}
	if strings.Contains(r.body, "Schritt 1 von 4") || strings.Contains(r.body, `class="wsteps"`) || strings.Contains(r.body, `class="frame-nav"`) {
		t.Error("the welcome page has a step number, progress or navigation")
	}
	if strings.Contains(r.body, "wiz-count") {
		t.Error("the welcome page has a step count")
	}
	if !strings.Contains(r.body, `<div class="frame-tools"><details class="lang">`) {
		t.Error("the welcome page brand bar has no language switcher")
	}
	en := b.do(http.MethodGet, "/setup", nil, map[string]string{"Accept-Language": "en"})
	if !strings.Contains(en.body, "Start setup") || !strings.Contains(en.body, "Welcome to Hejmo") {
		t.Error("the welcome page is not English")
	}
	// Once HTTPS works the page is the account form, and a set-up box leaves the wizard.
	if r := newBox(t).get("/setup"); !strings.Contains(r.body, `name="password"`) {
		t.Error("with HTTPS ready, /setup is not the account form")
	}
}

// The wizard shell: brand bar with the language switcher, "Setup: Step n of 4" above the progress, the four named
// steps (done ones with a hidden "(erledigt)"), no navigation.
func TestWizardShellShowsStepNumberAndProgress(t *testing.T) {
	b := newBox(t)
	page := deGet(b, "/setup").body
	for _, want := range []string{
		`class="wizard"`, `<nav class="wsteps" aria-label="Fortschritt der Einrichtung"><p class="wiz-count">Einrichtung: Schritt 2 von 4</p>`,
		`<li data-state="done"><span class="lab">`, `<span class="sr"> (erledigt)</span>`, `<li aria-current="step">`,
		"Sichere Verbin\u00addung", "Einmal\u00adcode",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("step 2 lacks %q", want)
		}
	}
	if strings.Contains(page, `class="frame-nav"`) || strings.Contains(page, "Abmelden") {
		t.Error("the wizard shows the frame navigation or sign-out")
	}
	// The count is not in the brand bar any more: the switcher sits there.
	if bar := page[:strings.Index(page, "</header>")]; strings.Contains(bar, "wiz-count") || !strings.Contains(bar, `class="frame-tools"`) {
		t.Error("the brand bar still holds the step count or lacks the switcher")
	}
	if n := strings.Count(page, `aria-current="step"`); n != 1 {
		t.Errorf("%d current steps", n)
	}
	// The name check, trust page, account, one-time code and recovery codes
	// are the four steps; the shell counts them 1, 1, 2, 3, 3.
	fresh := newFreshBox(t)
	fresh.post("/setup/https/public", publicForm())
	if r := deGet(fresh, "/setup/https/check"); !strings.Contains(r.body, "Schritt 1 von 4") {
		t.Error("the name check is not step 1")
	}
	if r := deGet(newFreshBox(t), "/setup/https"); !strings.Contains(r.body, "Schritt 1 von 4") {
		t.Error("the HTTPS step is not step 1")
	}
	b.post("/setup", validSetupForm())
	totp := deGet(b, "/setup/totp")
	if !strings.Contains(totp.body, "Schritt 3 von 4") {
		t.Errorf("the one-time code is not step 3:\n%s", totp.body)
	}
	if n := strings.Count(totp.body, `<li data-state="done">`); n != 2 {
		t.Errorf("%d done steps on step 3, want 2", n)
	}
}

// Ticket 133: a choice page first (none selected, Q62 a), submitted as a GET;
// then only the form of the chosen way.
func TestHTTPSWayIsChosenFirstThenOneForm(t *testing.T) {
	b := newFreshBox(t)
	choice := deGet(b, "/setup/https")
	for _, want := range []string{`<form class="tile" method="get" action="/setup/https">`, `<fieldset class="choice">`, `name="way" value="public"`, `name="way" value="own"`, "Mit kostenlosem Namen bei deSEC", "Nur im Heimnetz"} {
		if !strings.Contains(choice.body, want) {
			t.Errorf("choice page misses %q", want)
		}
	}
	if strings.Contains(choice.body, `name="token"`) || strings.Contains(choice.body, `action="/setup/https"`+` method="post"`) {
		t.Error("the choice page shows a form already")
	}
	if strings.Contains(choice.body, " checked") {
		t.Error("a way is selected by default")
	}

	public := deGet(b, "/setup/https?way=public")
	for _, want := range []string{`action="/setup/https/public"`, `<ol class="howto">`, `href="https://desec.io/signup" target="_blank"`, `name="name"`, `name="token"`, `type="password"`, "Mehr Optionen", "Zertifikat holen", "Anderen Weg wählen", `href="/setup/https"`} {
		if !strings.Contains(public.body, want) {
			t.Errorf("public form misses %q", want)
		}
	}
	if strings.Contains(public.body, `action="/setup/https" `) && strings.Contains(public.body, "Zertifikat erstellen") {
		t.Error("the public form also shows the own CA form")
	}
	// The address, issuer and email sit in "More options", closed when the
	// address is known, with their values in the summary.
	if !regexp.MustCompile(`<details class="more">\s*<summary>[^<]*<svg[^>]*>.*?</svg>Mehr Optionen<span class="sum">127.0.0.1 · Let&#39;s Encrypt</span>`).MatchString(strings.ReplaceAll(public.body, "\n", "")) {
		t.Errorf("More options is not closed with a summary of address and issuer:\n%s", public.body)
	}
	own := deGet(b, "/setup/https?way=own")
	for _, want := range []string{`<ul class="certnames"><li>hejmo.local</li><li>127.0.0.1</li></ul>`, `action="/setup/https"`, "Zertifikat erstellen", "Anderen Weg wählen"} {
		if !strings.Contains(own.body, want) {
			t.Errorf("own CA page misses %q", want)
		}
	}
	if strings.Contains(own.body, `name="token"`) {
		t.Error("the own CA page shows the public form")
	}
}

// Without a certificate manager there is no choice: straight to the own CA.
func TestWithoutAPublicNameOnlyTheOwnCAIsOffered(t *testing.T) {
	b := newFreshBox(t)
	b.handler = server.New(server.Options{DB: b.db, PKI: b.pki, Now: func() time.Time { return b.now }})
	b.srv.Close()
	b.srv = b.plainServer()
	for _, path := range []string{"/setup/https", "/setup/https?way=public", "/setup/https?way=own"} {
		r := deGet(b, path)
		if !strings.Contains(r.body, "Nur im Heimnetz einrichten") || strings.Contains(r.body, `name="token"`) || strings.Contains(r.body, "Anderen Weg wählen") {
			t.Errorf("%s: want the own CA page without a way back:\n%s", path, r.body)
		}
	}
}

// The address is the box's default; with no IP address in the URL (the box
// opened as hejmo.local) the field is empty and "More options" is open.
func TestMoreOptionsAreOpenWhenTheAddressIsMissingOrRefused(t *testing.T) {
	b := newFreshBox(t)
	r := b.do(http.MethodGet, "/setup/https?way=public", nil, map[string]string{"Host": "hejmo.local", "Accept-Language": "de"})
	if !strings.Contains(r.body, `<details class="more" open>`) {
		t.Errorf("More options are closed although the address is empty:\n%s", r.body)
	}
	// A refused address opens them again, and the error sits at the field.
	b.public.failure = &publiccert.InvalidError{Field: "lan_ip"}
	f := publicForm()
	f.Set("lan_ip", "999.1.1.1")
	post := b.do(http.MethodPost, "/setup/https/public", f, map[string]string{"Accept-Language": "de", "Origin": b.srv.URL})
	if post.status != http.StatusUnprocessableEntity || !strings.Contains(post.body, `<details class="more" open>`) || !strings.Contains(post.body, "keine Adresse im Heimnetz") {
		t.Errorf("a refused address: %d\n%s", post.status, post.body)
	}
}

// Each 422 goes to its field with its own text; everything else stays filled.
func TestPublicFormErrorsGoToTheirFields(t *testing.T) {
	cases := map[string]struct{ field, text string }{
		"name":   {"name", "Dieser Name geht nicht"},
		"lan_ip": {"lan_ip", "keine Adresse im Heimnetz"},
		"eab":    {"eab", "Schlüssel-ID und HMAC-Schlüssel"},
		"token":  {"token", "Füge den Token ein"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			b := newFreshBox(t)
			b.public.failure = &publiccert.InvalidError{Field: c.field}
			f := publicForm()
			f.Set("issuer", "actalis")
			f.Set("eab_key_id", "kid-7")
			r := b.do(http.MethodPost, "/setup/https/public", f, map[string]string{"Accept-Language": "de", "Origin": b.srv.URL})
			if r.status != http.StatusUnprocessableEntity {
				t.Fatalf("status %d", r.status)
			}
			if !strings.Contains(r.body, c.text) || !strings.Contains(r.body, `class="ferr"`) {
				t.Errorf("no field error with %q:\n%s", c.text, r.body)
			}
			if strings.Contains(r.body, `<p class="error"`) {
				t.Error("a field error is also a page-level error")
			}
			for _, kept := range []string{`value="mia.dedyn.io"`, `value="192.168.178.20"`, `value="papa@example.org"`, `value="kid-7"`, `value="actalis" checked`} {
				if !strings.Contains(r.body, kept) {
					t.Errorf("the form lost %s", kept)
				}
			}
			if c.field != "token" && strings.Contains(r.body, "id=\"err-token\"") {
				t.Error("the error is at the token too")
			}
		})
	}
}

// The 502: a page-level error with the two usual causes, and the token field
// says why it is empty. Name, address, issuer and email stay.
func TestPublicFailureShowsPageErrorAndAskForTheTokenAgain(t *testing.T) {
	b := newFreshBox(t)
	b.public.failure = errString("obtain certificate: dns says no")
	f := publicForm()
	f.Set("issuer", "actalis")
	f.Set("eab_key_id", "kid-7")
	r := b.do(http.MethodPost, "/setup/https/public", f, map[string]string{"Accept-Language": "de", "Origin": b.srv.URL})
	if r.status != http.StatusBadGateway {
		t.Fatalf("status %d", r.status)
	}
	for _, want := range []string{
		`<p class="error" role="alert">Die Box konnte das Zertifikat nicht holen. Meist stimmt der Token nicht, oder der Name gehört nicht zu diesem deSEC-Konto.`,
		`id="err-token"`, "Füge den Token bitte noch einmal ein.", `value="mia.dedyn.io"`, `value="192.168.178.20"`, `value="actalis" checked`, `value="papa@example.org"`,
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("failure page misses %q", want)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// While the box gets the certificate (up to 3 minutes) the button is greyed
// out by the form script, with a line in words. Nothing animates.
func TestPublicFormHasABusyStateInWords(t *testing.T) {
	b := newFreshBox(t)
	r := deGet(b, "/setup/https?way=public")
	for _, want := range []string{
		`<form method="post" action="/setup/https/public" class="tile" data-busy>`, `data-busy-text="Zertifikat wird geholt …"`,
		`data-hook="busy"`, "Das dauert meist unter einer Minute, höchstens 3 Minuten. Lass diese Seite offen.", "Die Box holt das Zertifikat …",
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("busy state misses %q", want)
		}
	}
	if !regexp.MustCompile(`data-hook="busy" hidden`).MatchString(r.body) {
		t.Error("the busy line is visible before the form is submitted")
	}
	js := b.get("/static/form.js").body
	for _, want := range []string{"data-busy", "button.disabled = true", "data-busy-text", `[data-hook="busy"]`} {
		if !strings.Contains(js, want) {
			t.Errorf("form.js lacks %q", want)
		}
	}
	css := b.get("/static/hejmo.css").body
	if strings.Contains(css, "@keyframes") || strings.Contains(css, "animation:") {
		t.Error("the stylesheet animates")
	}
}

// The Actalis keys only show for Actalis (the form's CSS keys off the choice).
func TestActalisKeysShowOnlyForActalis(t *testing.T) {
	b := newFreshBox(t)
	r := deGet(b, "/setup/https?way=public")
	if !strings.Contains(r.body, `<div class="when-actalis">`) || !strings.Contains(r.body, `name="eab_key_id"`) || !strings.Contains(r.body, `name="eab_hmac"`) {
		t.Errorf("the Actalis fields are missing:\n%s", r.body)
	}
	css := b.get("/static/hejmo.css").body
	if !strings.Contains(css, `.when-actalis { display: none; }`) || !strings.Contains(css, `input[name="issuer"][value="actalis"]:checked) .when-actalis`) {
		t.Error("the Actalis fields are not hidden for Let's Encrypt")
	}
}

// Ticket 133: the name check shows its state as check lines, the router help
// as a numbered tile, and the own-CA switch under "Router lässt sich nicht
// ändern?" as a secondary button.
func TestNameCheckShowsStatesAsCheckLines(t *testing.T) {
	b := newFreshBox(t)
	b.post("/setup/https/public", publicForm())
	r := deGet(b, "/setup/https/check")
	for _, want := range []string{
		`data-state="waiting"`, "Prüfe …", `data-state="fail"`, "Dein Browser erreicht <b>mia.dedyn.io</b> noch nicht.", "Diese Seite prüft alle 5 Sekunden neu. Zuletzt: –",
		`data-state="ok"`, `Dein Browser erreicht die Box unter <b>mia.dedyn.io</b>.`,
		`<ol class="howto">`, `class="addr">mia.dedyn.io</p>`, `class="button" type="button" data-check-again>Jetzt prüfen`,
		`<details class="more">`, "Router lässt sich nicht ändern?", `action="/setup/https/own-ca"`, `class="button secondary" type="submit">Zertifikat der Box nutzen`,
		`data-continue-link hidden>Weiter zu Schritt 2: Dein Konto`,
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("name check misses %q", want)
		}
	}
	for _, hidden := range []string{`data-hook="fail" hidden`, `data-hook="ok" hidden`, `data-rebind-help hidden`} {
		if !strings.Contains(r.body, hidden) {
			t.Errorf("%q is not hidden at the start", hidden)
		}
	}
	js := b.get("/static/https-check.js").body
	if !strings.Contains(js, "5000") && !strings.Contains(js, `"5"`) && !strings.Contains(js, "data-retry-seconds") {
		t.Error("the script does not retry every 5 seconds")
	}
	if !strings.Contains(js, "stamp(fail)") || !strings.Contains(js, "window.location.href = next") {
		t.Error("the script neither stamps the last try nor goes on by itself")
	}
}

// Ticket 133: account page: hints under the fields, regions grouped by country
// with the state name only, "Show" for the password, the error at the field.
func TestAccountPageHintsGroupedRegionsAndPasswordReveal(t *testing.T) {
	b := newBox(t)
	r := deGet(b, "/setup")
	for _, want := range []string{
		"<h1>Dein Konto</h1>", "Damit meldest du dich bei Hejmo an.", "<legend>Heimatort</legend>", `<p class="hint">Der Ort gibt Wetter, Sonnenauf- und -untergang, das Bundesland die Feiertage.</p>`, "<legend>Du</legend>", "<legend>Eltern-PIN</legend>",
		"Ort oder Stadtteil", "Kommt mit dem gewählten Ort", "Die Liste der Orte in Deutschland, Österreich und der Schweiz liegt auf der Box.", "Für die Feiertage.", "So erscheinst du auf dem Display.", "Damit meldest du dich an.", "Mindestens 10 Zeichen.",
		"Die PIN brauchen Eltern am Display", "PIN (4 bis 8 Ziffern)", `<optgroup label="Deutschland">`, `<optgroup label="Österreich">`, `<optgroup label="Schweiz">`,
		`<option value="" selected>Kommt mit dem gewählten Ort</option>`, `<option value="DE-BY">Bayern</option>`, `<span class="pw">`, `data-pw-toggle data-show-text="Anzeigen" data-hide-text="Verbergen"`, "Konto anlegen",
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("account page misses %q", want)
		}
	}
	if strings.Contains(r.body, "Deutschland ·") || strings.Contains(r.body, ">Deutschland · Bayern<") {
		t.Error("the options still repeat the country")
	}
	if n := strings.Count(r.body, `<optgroup`); n != 3 {
		t.Errorf("%d country groups", n)
	}
	js := b.get("/static/form.js").body
	if !strings.Contains(js, "data-pw-toggle") || !strings.Contains(js, `input.type = show ? "text" : "password"`) {
		t.Error("the script does not toggle the password")
	}
}

func TestAccountPagePasswordErrorKeepsTheHintAndTheRest(t *testing.T) {
	b := newBox(t)
	f := validSetupForm()
	f.Set("password", "short")
	f.Set("place_id", "2922530") // Garmisch-Partenkirchen
	r := b.do(http.MethodPost, "/setup", f, map[string]string{"Accept-Language": "de"})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", r.status)
	}
	for _, want := range []string{`class="ferr" id="err-password"`, "Das Passwort ist zu kurz. Es braucht mindestens 10 Zeichen.", `<b>Garmisch-Partenkirchen</b>`, `value="mama"`, `value="Mama"`, `aria-invalid="true"`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("password error page misses %q", want)
		}
	}
	// The shared field component of main (#150) keeps the hint next to the error
	// and names both in aria-describedby.
	if !strings.Contains(r.body, "Mindestens 10 Zeichen.") || !strings.Contains(r.body, `aria-describedby="err-password hint-password"`) {
		t.Error("the hint is gone or not described next to the error")
	}
	if strings.Contains(r.body, "short") {
		t.Error("the password is echoed")
	}
	if !strings.Contains(r.body, "Damit meldest du dich an.") {
		t.Error("the other hints disappeared")
	}
	// A PIN error leaves the password hint as it is.
	g := validSetupForm()
	g.Set("pin", "12")
	r = b.do(http.MethodPost, "/setup", g, map[string]string{"Accept-Language": "de"})
	if !strings.Contains(r.body, "Mindestens 10 Zeichen.") || !strings.Contains(r.body, `id="err-pin"`) {
		t.Errorf("the PIN error does not leave the password hint:\n%s", r.body)
	}
}

// Ticket 133: one-time code: on a computer the QR code comes first and the
// key, in groups of four, sits under "More options"; on a phone the button
// that opens the app comes first and the QR code is for another phone.
func TestTOTPPageDesktopAndPhoneOrder(t *testing.T) {
	b := newBox(t)
	b.post("/setup", validSetupForm())
	desk := deGet(b, "/setup/totp").body
	secret := secretOn(t, desk)
	grouped := regexp.MustCompile(`(.{4})`).ReplaceAllString(secret, "$1 ")
	grouped = strings.TrimSpace(grouped)
	for _, want := range []string{
		"QR-Code scannen", `class="qr"`, "QR-Code geht nicht? Schlüssel abtippen", `class="secret grouped" data-totp-secret="` + secret + `">` + grouped,
		"Name: Hejmo (mama). Zeitbasiert, 6 Ziffern.", "Code aus der App", `class="button" type="submit">Bestätigen`, "Schritt 3 von 4",
	} {
		if !strings.Contains(desk, want) {
			t.Errorf("computer page misses %q", want)
		}
	}
	if strings.Contains(desk, "otpauth://") || strings.Contains(desk, "In Authenticator-App öffnen") {
		t.Error("the computer page offers the app link")
	}
	if strings.Count(desk, `<li><div class="s">`) != 3 {
		t.Error("not three steps")
	}
	if strings.Index(desk, "Authenticator-App öffnen") > strings.Index(desk, "QR-Code scannen") {
		t.Error("steps out of order")
	}
	phone := b.do(http.MethodGet, "/setup/totp", nil, map[string]string{"Accept-Language": "de", "User-Agent": androidUA}).body
	open := strings.Index(phone, `class="button secondary" href="otpauth://totp/Hejmo:mama`)
	qr := strings.Index(phone, `class="qr"`)
	if open < 0 || qr < 0 || open > qr {
		t.Errorf("on a phone the app button must come before the QR code (%d, %d)", open, qr)
	}
	for _, want := range []string{"In Authenticator-App öffnen", "Die App ist auf einem anderen Handy?", "Code aus der App"} {
		if !strings.Contains(phone, want) {
			t.Errorf("phone page misses %q", want)
		}
	}
	if strings.Contains(phone, "Schlüssel abtippen") {
		t.Error("the phone page asks to type the key")
	}
}

func TestTOTPWrongCodeNamesTheTwoCauses(t *testing.T) {
	b := newBox(t)
	b.post("/setup", validSetupForm())
	r := b.do(http.MethodPost, "/setup/totp", url.Values{"code": {"000000"}}, map[string]string{"Accept-Language": "de"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "Der Code stimmt nicht. Nimm den Code, der gerade in der App steht. Stimmt er wieder nicht, prüfe, ob die Uhrzeit deines Handys stimmt.") || !strings.Contains(r.body, `id="err-code"`) {
		t.Errorf("wrong code = %d\n%s", r.status, r.body)
	}
}

// After a reset of the one-time codes the same page shows no step progress.
func TestTOTPPageAfterAResetHasNoProgress(t *testing.T) {
	b, mamaSecret, _ := setupWithCodes(t)
	papa, papaSecret := secondParent(t, b)
	loginWith(b, "papa", "papa's long password", totpNow(b, papaSecret))
	b.logout()
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))
	own := url.Values{"password": {"correct horse battery"}, "code": {totpNow(b, mamaSecret)}}
	if r := b.post("/account/reset/"+papa.ID, own); r.status != http.StatusSeeOther {
		t.Fatalf("reset = %d", r.status)
	}
	b.logout()
	if r := loginWith(b, "papa", "papa's long password", ""); r.location != "/setup/totp" {
		t.Fatalf("sign-in after reset = %d %q", r.status, r.location)
	}
	r := deGet(b, "/setup/totp")
	if r.status != http.StatusOK || !strings.Contains(r.body, "Neue Einmalcodes einrichten") {
		t.Fatalf("setup/totp after a reset = %d\n%s", r.status, r.body)
	}
	if strings.Contains(r.body, `class="wsteps"`) || strings.Contains(r.body, "Schritt 3 von 4") || !strings.Contains(r.body, `class="wizard"`) {
		t.Errorf("after a reset the page shows the first-setup progress or no shell:\n%s", r.body)
	}
}

// Ticket 133 (card reworked in 176): the recovery codes are step 3 of the
// wizard in the first setup, numbered, printable, with a required confirmation
// that the box checks; outside the wizard the same page sits in the frame.
func TestRecoveryCodesPageInTheWizardAndOutside(t *testing.T) {
	b := newBox(t)
	b.post("/setup", validSetupForm())
	secret := secretOn(t, b.get("/setup/totp").body)
	code, _ := auth.TOTPCode(secret, b.now)
	r := b.do(http.MethodPost, "/setup/totp", url.Values{"code": {code}}, map[string]string{"Accept-Language": "de"})
	for _, want := range []string{
		"Schritt 3 von 4", "<h1>Wiederherstellungs\u00adcodes sichern</h1>", "Du siehst die Codes <b>nur jetzt</b>.", `<ol class="codes">`, `class="rc-save no-print"`, `data-print>`, "Drucken",
		`<input type="checkbox" name="saved" value="1">`, "Ich habe meine Wiederherstellungs\u00adcodes gesichert.", `<form class="rc" method="post" action="/account/recovery-codes/saved" data-rc>`,
		`type="submit" aria-disabled="true">Weiter zu den ersten Schritten`,
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("wizard recovery page misses %q", want)
		}
	}
	if strings.Contains(r.body, `class="frame-nav"`) {
		t.Error("the wizard recovery page has the navigation")
	}
	if len(recoveryCodesOn(t, r.body)) != 10 {
		t.Error("not ten codes")
	}
	// Outside the wizard: in the frame, generic label.
	secondParent(t, b)
	out := b.post("/account/recovery-codes", url.Values{})
	for _, want := range []string{`class="frame-nav"`, `type="submit" aria-disabled="true">Fertig, zurück zu Mein Konto`, `name="saved" value="1"`} {
		if !strings.Contains(out.body, want) {
			t.Errorf("the recovery page outside the wizard misses %q", want)
		}
	}
	if strings.Contains(out.body, "Weiter zu den ersten Schritten") || strings.Contains(out.body, "Schritt 3 von 4") {
		t.Error("the recovery page outside the wizard is the wizard's")
	}
	print := b.get("/static/hejmo.css").body
	if !strings.Contains(print, "@media print") || !strings.Contains(print, ".wizard > header, .wsteps, .no-print { display: none !important; }") {
		t.Error("the print styles do not leave only the codes")
	}
}
