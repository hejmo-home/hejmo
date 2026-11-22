package server_test

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const iPhone = "Mozilla/5.0 (iPhone; CPU iPhone OS 19_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148 Safari/604.1"

// Acceptance: Given a fresh box, when the wizard runs, then no parent password
// can be set before HTTPS works.
func TestFreshBoxAsksForHTTPSBeforeAnyPassword(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)

	// The first page says what is coming and leads to the HTTPS step; it
	// does not ask for any password.
	welcome := b.get("/setup")
	if welcome.status != http.StatusOK || !strings.Contains(welcome.body, `href="/setup/https"`) || strings.Contains(welcome.body, `name="password"`) {
		t.Fatalf("GET /setup before HTTPS = %d, want the welcome page with a link to /setup/https\n%s", welcome.status, welcome.body)
	}
	r := b.post("/setup", validSetupForm())
	if r.status != http.StatusSeeOther || r.location != "/setup/https" {
		t.Fatalf("POST /setup before HTTPS = %d %q, want 303 to /setup/https", r.status, r.location)
	}
	if _, err := b.db.HomeHousehold(); err == nil {
		t.Fatal("a household was stored before HTTPS worked")
	}
}

func TestHTTPSStepIsSpokenInBothLanguages(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	de := b.do(http.MethodGet, "/setup/https", nil, map[string]string{"Accept-Language": "de"})
	en := b.do(http.MethodGet, "/setup/https", nil, map[string]string{"Accept-Language": "en"})
	if de.status != http.StatusOK || !strings.Contains(de.body, "Wie soll die Box sicher erreichbar sein?") || !strings.Contains(de.body, `name="way"`) {
		t.Errorf("German HTTPS step:\n%s", de.body)
	}
	if en.status != http.StatusOK || !strings.Contains(en.body, "reach the box securely") {
		t.Errorf("English HTTPS step:\n%s", en.body)
	}
}

func TestCreatingTheCertificateAuthorityUnlocksThePasswordStepOverHTTPS(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	r := b.post("/setup/https", url.Values{})
	if r.status != http.StatusSeeOther || r.location != "/ca" {
		t.Fatalf("POST /setup/https = %d %q, want 303 to /ca", r.status, r.location)
	}
	if !b.pki.Ready() {
		t.Fatal("no certificate after the HTTPS step")
	}

	// The wizard link leads to the same host over HTTPS.
	page := b.get("/ca")
	host := strings.TrimPrefix(b.srv.URL, "http://")
	host = host[:strings.LastIndex(host, ":")]
	if want := "https://" + host + "/setup"; !strings.Contains(page.body, want) {
		t.Errorf("trust page misses the continue link %q:\n%s", want, page.body)
	}

	// Passwords are never accepted over plain HTTP, even with HTTPS set up.
	if r := b.post("/setup", validSetupForm()); r.status != http.StatusPermanentRedirect {
		t.Fatalf("POST /setup over HTTP = %d, want 308", r.status)
	}
	if _, err := b.db.HomeHousehold(); err == nil {
		t.Fatal("a household was stored over plain HTTP")
	}

	tlsBox := b.serveTLS()
	b.client = b.trustingClient()
	b.srv = tlsBox
	if r := b.post("/setup", validSetupForm()); r.status != http.StatusSeeOther || r.location != "/setup/totp" {
		t.Fatalf("POST /setup over HTTPS = %d %q", r.status, r.location)
	}
}

func TestHTTPSStepRunsOnlyOnce(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	before := string(b.pki.CAPEM())
	if r := b.post("/setup/https", url.Values{}); r.status != http.StatusSeeOther || r.location != "/setup" {
		t.Fatalf("second HTTPS step = %d %q, want 303 to /setup", r.status, r.location)
	}
	if string(b.pki.CAPEM()) != before {
		t.Fatal("the CA was replaced")
	}
}

// Acceptance: Given a box with HTTPS set up, when a client calls http://, then
// it is redirected to https://.
func TestHTTPRedirectsToHTTPS(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	plain := b.plainServer()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	res, err := client.Get(plain.URL + "/login?next=%2Fcalendar")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	host := strings.TrimPrefix(plain.URL, "http://")
	host = host[:strings.LastIndex(host, ":")]
	if res.StatusCode != http.StatusPermanentRedirect || res.Header.Get("Location") != "https://"+host+"/login?next=%2Fcalendar" {
		t.Fatalf("GET http:// = %d %q", res.StatusCode, res.Header.Get("Location"))
	}

	// Every method is redirected, the API included (port 80 only redirects).
	res, err = client.Post(plain.URL+"/api/v1/tokens", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusPermanentRedirect || res.Header.Get("Location") != "https://"+host+"/api/v1/tokens" {
		t.Errorf("POST http:// = %d %q, want 308 to https", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestRedirectKeepsAConfiguredPublicHTTPSPort(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	b.handler = serverWithHTTPSPort(b, "8443")
	plain := b.plainServer()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(plain.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if loc := res.Header.Get("Location"); !strings.HasSuffix(loc, ":8443/") {
		t.Errorf("Location = %q, want the public HTTPS port", loc)
	}
}

// Everything else is redirected (ADR 0014).
func TestPlainHTTPRedirectsEverythingButTheTrustSteps(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	plain := b.plainServer()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, path := range []string{"/static/sw.js", "/static/form.js", "/login", "/api/v1/members", "/ca/other", "/setup",
		// encoded traversal out of the allowed static prefixes, and directory listings
		"/static/icons/..%2fform.js", "/static/fonts/%2e%2e/sw.js", "/static/fonts/../sw.js", "/static/icons/", "/static/fonts/"} {
		res, err := client.Get(plain.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusPermanentRedirect {
			t.Errorf("GET http://%s = %d, want 308", path, res.StatusCode)
		}
	}
}

// The trust page is read before the device trusts the box, so what it needs
// to look right (stylesheet, font, icons) must not redirect to HTTPS. These
// files are public and identical on every box: serving them in the clear
// leaks nothing.
func TestTrustPageRendersStyledOverPlainHTTP(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	plain := b.plainServer()
	page, err := http.Get(plain.URL + "/ca")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if !strings.Contains(string(raw), `href="/static/hejmo.css"`) {
		t.Fatalf("trust page links no stylesheet:\n%s", raw)
	}
	for _, path := range []string{"/static/hejmo.css", "/static/fonts/Figtree-wght.ttf", "/static/icons/apple-touch-icon.png"} {
		res, err := http.Get(plain.URL + path) // follows redirects: a redirect to HTTPS fails on the untrusted certificate
		if err != nil {
			t.Fatalf("GET http://%s: %v", path, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK || res.Request.URL.Scheme != "http" {
			t.Errorf("GET http://%s = %d via %s, want 200 over http", path, res.StatusCode, res.Request.URL.Scheme)
		}
	}
}

func TestPlainHTTPStillServesWhatPhonesNeedBeforeTheyTrustTheBox(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	plain := b.plainServer()
	for _, path := range []string{"/healthz", "/ca", "/ca/hejmo-ca.crt", "/ca/hejmo.mobileconfig"} {
		res, err := http.Get(plain.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Errorf("GET http://%s = %d, want 200", path, res.StatusCode)
		}
	}
}

// Acceptance: Given an iPhone, when the parent opens the profile download, then
// a valid configuration profile with the CA is served.
func TestIPhoneGetsAConfigurationProfileWithTheCA(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	req, _ := http.NewRequest(http.MethodGet, b.srv.URL+"/ca/hejmo.mobileconfig", nil)
	req.Header.Set("User-Agent", iPhone)
	res, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "application/x-apple-aspen-config" {
		t.Fatalf("profile = %d %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.Contains(cd, "hejmo.mobileconfig") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	raw, _ := io.ReadAll(res.Body)
	body := string(raw)
	for _, want := range []string{"com.apple.security.root", "<string>Configuration</string>"} {
		if !strings.Contains(body, want) {
			t.Errorf("profile misses %q", want)
		}
	}
	der := base64.StdEncoding.EncodeToString(pemToDER(t, b.pki.CAPEM()))
	if !strings.Contains(body, der) {
		t.Error("profile does not carry the box CA")
	}
}

func TestCACertificateDownload(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	r := b.get("/ca/hejmo-ca.crt")
	if r.status != http.StatusOK || r.body != string(b.pki.CAPEM()) {
		t.Fatalf("CA download = %d", r.status)
	}
}

func (b *box) trustPage(path, lang, userAgent string) resp {
	return b.do(http.MethodGet, path, nil, map[string]string{"Accept-Language": lang, "User-Agent": userAgent})
}

const (
	androidUA = "Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Mobile Safari/537.36"
	macUA     = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/19.0 Safari/605.1.15"
	windowsUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"
	linuxUA   = "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0"
)

// Ticket 133: the trust page detects the visitor's device from the User-Agent
// and shows its four steps first; the other devices are links. The only main
// button of the page is the download in step 1.
func TestTrustPageDetectsTheDeviceAndShowsItsFourSteps(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	cases := []struct {
		name, ua, current string
		wants             []string
	}{
		{"iPhone", iPhone, "ios", []string{"iPhone oder iPad: 4 Schritte", `href="/ca/hejmo.mobileconfig"`, "Profil laden", "Profil installieren", "Vertrauen einschalten", "Hierher zurückkommen", "Erkannt: iPhone oder iPad."}},
		{"Android", androidUA, "android", []string{"Android: 4 Schritte", `href="/ca/hejmo-ca.crt"`, "Zertifikat laden", "CA-Zertifikat", "Warnung bestätigen", "Hersteller", "DAVx5"}},
		{"Mac", macUA, "mac", []string{"Mac: 4 Schritte", "Schlüsselbund", "Anmeldung", "Immer vertrauen", "Firefox", "Erkannt: Mac."}},
		{"Windows", windowsUA, "windows", []string{"Windows: 4 Schritte", "Vertrauenswürdige Stammzertifizierungsstellen", "Firefox"}},
		{"Linux", linuxUA, "linux", []string{"Linux: 4 Schritte", "update-ca-certificates"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := b.trustPage("/ca", "de", c.ua)
			for _, want := range c.wants {
				if !strings.Contains(r.body, want) {
					t.Errorf("trust page for %s misses %q", c.name, want)
				}
			}
			if n := strings.Count(r.body, `aria-current="true"`); n != 1 || !strings.Contains(r.body, `href="?device=`+c.current+`" aria-current="true"`) {
				t.Errorf("the device chooser marks %d devices, want only %s", n, c.current)
			}
			if n := strings.Count(r.body, `class="button" href=`) - strings.Count(r.body, `data-continue-link`); n != 1 {
				t.Errorf("trust page has %d main buttons, want the download only", n)
			}
			for _, other := range []string{"ios", "android", "mac", "windows", "linux"} {
				if !strings.Contains(r.body, `href="?device=`+other) {
					t.Errorf("no link to %s", other)
				}
			}
		})
	}
	en := b.trustPage("/ca", "en", macUA)
	for _, want := range []string{"Keychain Access", "login", "Always Trust", "Detected: Mac."} {
		if !strings.Contains(en.body, want) {
			t.Errorf("English trust page misses %q", want)
		}
	}
}

// ?device= overrides the detection; unknown values and unknown agents fall back.
func TestTrustPageDeviceOverrideAndFallback(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	r := b.trustPage("/ca?device=windows", "de", iPhone)
	if !strings.Contains(r.body, "Windows: 4 Schritte") || strings.Contains(r.body, "Erkannt:") || !strings.Contains(r.body, "Gewählt: Windows.") {
		t.Errorf("?device=windows on an iPhone:\n%s", r.body)
	}
	if r := b.trustPage("/ca?device=toaster", "de", androidUA); !strings.Contains(r.body, "Android: 4 Schritte") {
		t.Errorf("an unknown device is not ignored:\n%s", r.body)
	}
	r = b.trustPage("/ca", "de", "curl/8.0")
	if !strings.Contains(r.body, "nicht erkannt") || !strings.Contains(r.body, "iPhone oder iPad: 4 Schritte") {
		t.Errorf("an unknown agent shows no hint or no first device:\n%s", r.body)
	}
	// An iPad asking for the desktop site looks like a Mac; the page script
	// corrects that where there is a touch screen.
	mac := b.trustPage("/ca", "de", macUA)
	if !strings.Contains(mac.body, "data-detected") || !strings.Contains(mac.body, `data-device="mac"`) {
		t.Errorf("the page does not tell its script that the Mac was a guess:\n%s", mac.body)
	}
	js := b.get("/static/https-check.js")
	if !strings.Contains(js.body, "maxTouchPoints") || !strings.Contains(js.body, `"ios"`) {
		t.Error("the script does not turn a touch Mac into an iPad")
	}
}

// ChromeOS has no update-ca-certificates: it is not guessed as Linux.
func TestChromeOSIsNotTakenForLinux(t *testing.T) {
	t.Parallel()
	const crOS = "Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"
	r := newBox(t).trustPage("/ca", "de", crOS)
	if !strings.Contains(r.body, "nicht erkannt") {
		t.Errorf("ChromeOS must count as unknown:\n%s", r.body)
	}
	if strings.Contains(r.body, `href="?device=linux" aria-current="true"`) {
		t.Errorf("ChromeOS must not mark Linux as the current device:\n%s", r.body)
	}
}

// An iPad is not called an iPhone in the check lines, Firefox on Android gets its note, and the Mac button is clicked.
func TestTrustTextsForIPadFirefoxAndMac(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	ios := b.trustPage("/ca?device=ios", "de", iPhone)
	if strings.Contains(ios.body, "Dein iPhone vertraut") || strings.Contains(ios.body, "iPhone ist nicht") {
		t.Errorf("check lines name the iPhone only:\n%s", ios.body)
	}
	if a := b.trustPage("/ca", "de", androidUA); !strings.Contains(a.body, "Firefox") {
		t.Error("Android misses the Firefox note")
	}
	if m := b.trustPage("/ca", "de", macUA); strings.Contains(m.body, "Tippe auf den Knopf") || !strings.Contains(m.body, "Klicke auf den Knopf") {
		t.Error("Mac text must say Klicke")
	}
}

// The iOS steps work on an iPad too, so they never say "das iPhone"; Windows
// shows only SHA-1 at install time, so it gets no "compare, else do not install" hint.
func TestTrustTextsFitIPadAndWindows(t *testing.T) {
	t.Parallel()
	const iPad = "Mozilla/5.0 (iPad; CPU OS 19_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148 Safari/604.1"
	b := newBox(t)
	ipad := b.trustPage("/ca", "de", iPad)
	if !strings.Contains(ipad.body, "Profil laden") {
		t.Fatalf("an iPad is not served the iOS steps:\n%s", ipad.body)
	}
	for _, bad := range []string{"das iPhone", "deines iPhones", "dein iPhone"} {
		if strings.Contains(ipad.body, bad) {
			t.Errorf("iOS steps on an iPad say %q", bad)
		}
	}
	win := b.trustPage("/ca", "de", windowsUA)
	if strings.Contains(win.body, "installiere das Zertifikat nicht") {
		t.Error("Windows shows no SHA-256 at install time, so it must not be told to compare")
	}
	if !strings.Contains(win.body, "SHA-1") {
		t.Error("Windows misses its SHA-1 note")
	}
}

func TestDetectsDevicesFromTheUserAgent(t *testing.T) {
	t.Parallel()
	for ua, want := range map[string]string{iPhone: "ios", androidUA: "android", macUA: "mac", windowsUA: "windows", linuxUA: "linux"} {
		b := newBox(t)
		if r := b.trustPage("/ca", "en", ua); !strings.Contains(r.body, `href="?device=`+want+`" aria-current="true"`) {
			t.Errorf("%q is not detected as %s", ua, want)
		}
	}
}

// The trust page checks by itself whether the device trusts the box (see
// static/https-check.js): the HTTPS address it asks, the lines that tell the
// state in words, and the way on, which stays hidden until the check passes.
func TestTrustPageChecksByItselfAndContinuesOnlyWhenTrusted(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	host := strings.TrimPrefix(b.srv.URL, "https://")
	host = host[:strings.LastIndex(host, ":")]
	r := b.trustPage("/ca", "de", iPhone)
	for _, want := range []string{
		`data-check="trust"`, `data-check-url="https://` + host, `/healthz"`, `/static/https-check.js`,
		`data-hook="waiting"`, `data-hook="ok"`, `data-hook="fail"`, `data-download`,
		`Dein Gerät vertraut der Box noch nicht.`, `Dein Gerät vertraut der Box.`,
		`href="https://` + host, `/setup" data-continue-link hidden>Weiter zu Schritt 2: Dein Konto`,
		"Ohne Prüfung weiter", "Die Prüfung klappt nicht?",
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("trust page misses %q", want)
		}
	}
	// The failure after a download sits in the step that usually goes wrong
	// (step 3 on iOS, Android and Mac), not at the end.
	for _, ua := range []string{iPhone, androidUA, macUA} {
		page := b.trustPage("/ca", "de", ua).body
		step3 := page[strings.Index(page, "data-fail-step"):]
		if i := strings.Index(step3, `data-hook="fail"`); i < 0 || i > strings.Index(step3, "</li>") {
			t.Errorf("the failure line is not in the failing step for %q", ua)
		}
	}
	js := b.get("/static/https-check.js")
	for _, want := range []string{"visibilitychange", "no-cors", "data-download", "setTimeout"} {
		if !strings.Contains(js.body, want) {
			t.Errorf("the check script lacks %q", want)
		}
	}
	// A plain-HTTP device must be able to load the script that checks.
	if r := b.get("/static/https-check.js"); r.status != http.StatusOK {
		t.Errorf("script = %d", r.status)
	}
}

// A phone on its way to pair keeps its target and its label; the box does not
// pretend it is the wizard.
func TestTrustPageKeepsThePairingTargetAndHasNoWizardAfterSetup(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	host := strings.TrimPrefix(c.b.srv.URL, "https://")
	host = host[:strings.LastIndex(host, ":")] // the box answers on the default HTTPS port in its links
	target := "https://" + host + "/phones/join/ABC123"
	r := c.b.trustPage("/ca?continue="+url.QueryEscape(target), "de", iPhone)
	if !strings.Contains(r.body, `href="`+target+`" data-continue-link hidden>Weiter mit der Einrichtung`) {
		t.Errorf("the pairing target or its label is gone:\n%s", r.body)
	}
	if strings.Contains(r.body, "Schritt 2: Dein Konto") || strings.Contains(r.body, `class="wsteps"`) {
		t.Error("a configured box shows the wizard on the trust page")
	}
	if !strings.Contains(r.body, `continue=`) {
		t.Error("choosing another device would lose the pairing target")
	}
	// Without a pairing target there is nothing to continue to.
	if r := c.b.trustPage("/ca", "de", iPhone); strings.Contains(r.body, "data-continue-link") || strings.Contains(r.body, "Ohne Prüfung weiter") {
		t.Error("a configured box offers a way on without a target")
	}
}

// During the first setup the trust page is step 1 of the wizard.
func TestTrustPageIsStepOneOfTheWizard(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	b.post("/setup/https", url.Values{})
	r := b.trustPage("/ca", "de", macUA)
	for _, want := range []string{`class="wizard"`, "Einrichtung: Schritt 1 von 4", `aria-current="step"`, "Sichere Verbin\u00addung"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("the wizard shell misses %q", want)
		}
	}
	if strings.Contains(r.body, `class="frame-nav"`) {
		t.Error("the wizard has a navigation")
	}
}

// The fingerprint is shown as 32 pairs, explained in plain words, with where
// to find it on this device; Windows only shows SHA-1, so it says comparing
// is not possible there.
func TestTrustPageFingerprintInPlainWords(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	fp := b.pki.CAFingerprint()
	pairs := strings.Split(fp, ":")
	r := b.trustPage("/ca", "de", macUA)
	if got := strings.Count(r.body, "<span>"); got < 32 {
		t.Errorf("fingerprint grid has %d spans, want 32 pairs", got)
	}
	for _, want := range []string{`class="fp"`, "<span>" + pairs[0] + "</span>", "<span>" + pairs[31] + "</span>", "wie eine Seriennummer", "Fingerabdruck vergleichen (freiwillig)", "Schlüsselbundverwaltung", "Protokoll der Box"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("fingerprint section misses %q", want)
		}
	}
	win := b.trustPage("/ca", "de", windowsUA)
	// Windows shows only SHA-1, but the grid and the mismatch hint stay for the comparison with the box log.
	for _, want := range []string{`class="fp"`, "<span>" + pairs[0] + "</span>", "SHA-1", "Protokoll der Box"} {
		if !strings.Contains(win.body, want) {
			t.Errorf("Windows fingerprint section misses %q", want)
		}
	}
}

func TestTrustPageShowsTheCAFingerprint(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	if r := b.get("/ca"); !strings.Contains(r.body, b.pki.CAFingerprint()) {
		t.Errorf("trust page misses the CA fingerprint:\n%s", r.body)
	}
}

func TestHTTPSStepRefusesNamesThatCannotGoIntoACertificate(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	r := b.do(http.MethodPost, "/setup/https", url.Values{}, map[string]string{"Host": "hejmo", "Origin": "http://hejmo"})
	if r.status != http.StatusBadRequest {
		t.Fatalf("POST /setup/https for a single-label host = %d, want 400", r.status)
	}
	if !strings.Contains(r.body, `action="/setup/https"`) {
		t.Errorf("the HTTPS step is not shown again:\n%s", r.body)
	}
	if b.pki.Ready() {
		t.Fatal("a certificate was issued")
	}
}

func TestTrustPageNeedsHTTPSToBeSetUp(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	if r := b.get("/ca"); r.status != http.StatusSeeOther || r.location != "/setup/https" {
		t.Fatalf("GET /ca before HTTPS = %d %q", r.status, r.location)
	}
	if r := b.get("/ca/hejmo.mobileconfig"); r.status != http.StatusNotFound {
		t.Fatalf("profile before HTTPS = %d, want 404", r.status)
	}
}

func TestRefusedNameIsExplainedInBothLanguages(t *testing.T) {
	t.Parallel()
	for lang, want := range map[string]string{"de": "kann nicht für HTTPS verwendet werden", "en": "cannot be used for HTTPS"} {
		b := newFreshBox(t)
		r := b.do(http.MethodPost, "/setup/https", url.Values{}, map[string]string{"Host": "hejmo", "Origin": "http://hejmo", "Accept-Language": lang})
		if r.status != http.StatusBadRequest || !strings.Contains(r.body, want) {
			t.Errorf("%s: status %d, want 400 with %q:\n%s", lang, r.status, want, r.body)
		}
	}
}

// The trust page carries the language switcher (ticket 134), and a phone reads
// it over plain HTTP before it trusts the box: the flags and the switch itself
// must work there instead of redirecting to the untrusted HTTPS address.
func TestLanguageSwitcherWorksOnTheTrustPageOverPlainHTTP(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	plain := b.plainServer()
	page, err := http.Get(plain.URL + "/ca")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if !strings.Contains(string(raw), `<details class="lang">`) {
		t.Fatalf("trust page over HTTP has no switcher:\n%s", raw)
	}
	for _, f := range []string{"de", "gb"} {
		res, err := http.Get(plain.URL + "/static/flags/" + f + ".svg") // follows redirects: HTTPS would fail on the untrusted certificate
		if err != nil {
			t.Fatalf("flag %s: %v", f, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK || res.Request.URL.Scheme != "http" {
			t.Errorf("flag %s = %d via %s, want 200 over http", f, res.StatusCode, res.Request.URL.Scheme)
		}
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest(http.MethodPost, plain.URL+"/language", strings.NewReader(url.Values{"lang": {"en"}, "next": {"/ca"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", plain.URL)
	res, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/ca" {
		t.Fatalf("POST http://language = %d %q, want 303 to /ca", res.StatusCode, res.Header.Get("Location"))
	}
	var set *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == "hejmo_lang" {
			set = c
		}
	}
	if set == nil || set.Value != "en" || set.Secure {
		t.Errorf("language cookie over HTTP = %+v, want en and not Secure", set)
	}
	// Following the 303 back to the trust page over HTTP shows it in English.
	back, _ := http.NewRequest(http.MethodGet, plain.URL+res.Header.Get("Location"), nil)
	back.AddCookie(set)
	back.Header.Set("Accept-Language", "de")
	br, err := noRedirect.Do(back)
	if err != nil {
		t.Fatal(err)
	}
	braw, _ := io.ReadAll(br.Body)
	br.Body.Close()
	if br.StatusCode != http.StatusOK || !strings.Contains(string(braw), `<html lang="en"`) {
		t.Fatalf("GET http://ca after the switch = %d, want 200 with lang=\"en\":\n%s", br.StatusCode, braw)
	}

	// Only the switch and single flag files: no other POST, no listing, no traversal.
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/static/flags/"}, {http.MethodGet, "/static/flags/..%2fform.js"},
		{http.MethodGet, "/static/flags/../sw.js"}, {http.MethodPost, "/login"},
		{http.MethodPost, "/ca"}, {http.MethodPost, "/setup"},
	} {
		req, _ := http.NewRequest(c.method, plain.URL+c.path, nil)
		res, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusPermanentRedirect {
			t.Errorf("%s http://%s = %d, want 308", c.method, c.path, res.StatusCode)
		}
	}
}
