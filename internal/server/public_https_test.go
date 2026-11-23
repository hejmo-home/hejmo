package server_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/publiccert"
)

func publicForm() url.Values {
	return url.Values{
		"name": {"mia.dedyn.io"}, "provider": {"desec"}, "token": {"desec-token-4711"},
		"issuer": {"letsencrypt"}, "lan_ip": {"192.168.178.20"}, "email": {"papa@example.org"},
	}
}

func TestWizardOffersPublicNameBesideTheOwnCA(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	for lang, wants := range map[string][]string{
		"de": {"deSEC", "Zugangstoken", "Actalis", `action="/setup/https/public"`, `name="lan_ip"`, `value="127.0.0.1"`},
		"en": {"deSEC", "access token", "Actalis", `action="/setup/https/public"`},
	} {
		r := b.do(http.MethodGet, "/setup/https?way=public", nil, map[string]string{"Accept-Language": lang})
		for _, want := range wants {
			if !strings.Contains(r.body, want) {
				t.Errorf("%s: HTTPS step misses %q:\n%s", lang, want, r.body)
			}
		}
	}
}

// Acceptance: given a deSEC token, when the wizard finishes, then the box
// serves a certificate for the chosen name.
func TestPublicSetupHandsTheWizardInputToTheCertificateManager(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	r := b.post("/setup/https/public", publicForm())
	if r.status != http.StatusSeeOther || r.location != "/setup/https/check" {
		t.Fatalf("POST = %d %q, want 303 to the name check", r.status, r.location)
	}
	got := b.public.requests()
	want := publiccert.Request{Name: "mia.dedyn.io", Provider: "desec", Token: "desec-token-4711", Issuer: "letsencrypt", LANIP: "192.168.178.20", Email: "papa@example.org"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("manager got %+v, want %+v", got, want)
	}
	// HTTPS counts as set up now: the password step opens (over HTTPS only).
	if r := b.get("/setup"); r.location == "/setup/https" {
		t.Error("the wizard still asks for HTTPS")
	}
	if r := b.post("/setup", validSetupForm()); r.status == http.StatusSeeOther && r.location == "/setup/https" {
		t.Error("password refused although HTTPS works")
	}
}

// Acceptance: given Actalis selected, then certificates are requested from Actalis.
func TestActalisChoiceAndItsAccountKeysReachTheManager(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	f := publicForm()
	f.Set("issuer", "actalis")
	f.Set("eab_key_id", "kid-1")
	f.Set("eab_hmac", "aG1hYw")
	if r := b.post("/setup/https/public", f); r.status != http.StatusSeeOther {
		t.Fatalf("POST = %d", r.status)
	}
	got := b.public.requests()[0]
	if got.Issuer != "actalis" || got.EABKeyID != "kid-1" || got.EABHMAC != "aG1hYw" {
		t.Errorf("manager got %+v", got)
	}
}

func TestPublicSetupFailuresAreExplainedAndTheFormComesBackWithoutTheToken(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		err    error
		status int
		key    string // English text
	}{
		"invalid":  {publiccert.ErrInvalid, http.StatusUnprocessableEntity, "check your entries"},
		"provider": {publiccert.ErrUnknownProvider, http.StatusUnprocessableEntity, "check your entries"},
		"failed":   {errors.New("obtain certificate: dns says no"), http.StatusBadGateway, "could not get the certificate"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			b := newFreshBox(t)
			b.public.failure = c.err
			r := b.do(http.MethodPost, "/setup/https/public", publicForm(), map[string]string{"Accept-Language": "en", "Origin": b.srv.URL})
			if r.status != c.status || !strings.Contains(r.body, c.key) {
				t.Fatalf("status %d, want %d with %q:\n%s", r.status, c.status, c.key, r.body)
			}
			if strings.Contains(r.body, "desec-token-4711") {
				t.Error("the token is echoed back into the page")
			}
			if strings.Contains(r.body, "dns says no") {
				t.Error("the CA's untranslated error text is shown to the family")
			}
			if !strings.Contains(r.body, `value="mia.dedyn.io"`) {
				t.Error("the name is not kept in the form")
			}
			if b.public.Ready() {
				t.Error("ready after a failure")
			}
		})
	}
}

func TestPublicSetupIsClosedOnceHTTPSOrTheBoxIsSetUp(t *testing.T) {
	t.Parallel()
	b := newBox(t) // own CA already in place
	if r := b.post("/setup/https/public", publicForm()); r.status != http.StatusSeeOther || r.location != "/setup" {
		t.Fatalf("POST after HTTPS = %d %q", r.status, r.location)
	}
	if len(b.public.requests()) != 0 {
		t.Error("the manager was called")
	}
}

// Acceptance: given a name that does not resolve from the browser, then the
// wizard shows the rebind help and checks again.
func TestNameCheckPageShowsRebindHelpAndRetries(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	b.post("/setup/https/public", publicForm())

	for lang, wants := range map[string][]string{
		"de": {"DNS-Rebind-Schutz", "FRITZ!Box", "mia.dedyn.io", `href="http://fritz.box/"`, `class="addr">mia.dedyn.io`},
		"en": {"DNS rebind protection", "FRITZ!Box", "mia.dedyn.io"},
	} {
		r := b.do(http.MethodGet, "/setup/https/check", nil, map[string]string{"Accept-Language": lang})
		if r.status != http.StatusOK {
			t.Fatalf("check page = %d", r.status)
		}
		for _, want := range append(wants, `data-check-url="https://mia.dedyn.io/healthz"`, `data-rebind-help`, `/static/https-check.js`, `href="https://mia.dedyn.io/setup"`) {
			if !strings.Contains(r.body, want) {
				t.Errorf("%s: check page misses %q:\n%s", lang, want, r.body)
			}
		}
	}
	js := b.get("/static/https-check.js")
	if js.status != http.StatusOK || !strings.Contains(js.body, "setTimeout") || !strings.Contains(js.body, "no-cors") {
		t.Errorf("the check script does not retry:\n%s", js.body)
	}
}

func TestNameCheckKeepsAConfiguredHTTPSPortInItsLinks(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	b.handler = serverWithHTTPSPort(b, "8443")
	b.srv.Close()
	b.srv = b.plainServer()
	b.post("/setup/https/public", publicForm())
	r := b.get("/setup/https/check")
	if !strings.Contains(r.body, `data-check-url="https://mia.dedyn.io:8443/healthz"`) {
		t.Errorf("port missing:\n%s", r.body)
	}
}

func TestNameCheckNeedsAPublicName(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	if r := b.get("/setup/https/check"); r.status != http.StatusSeeOther || r.location != "/setup/https" {
		t.Fatalf("check page without public name = %d %q", r.status, r.location)
	}
}

func TestWithAPublicNameHTTPRedirectsToThatNameNotTheAddressUsed(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	b.post("/setup/https/public", publicForm())
	plain := b.plainServer()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	res, err := client.Get(plain.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusPermanentRedirect || res.Header.Get("Location") != "https://mia.dedyn.io/login" {
		t.Errorf("GET http:// = %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	// The check page itself must stay reachable over HTTP: the browser is
	// still on the LAN address while it tests the name.
	res, err = http.Get(plain.URL + "/setup/https/check")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("check page over HTTP = %d", res.StatusCode)
	}
}

func TestTrustPageOfTheOwnCAIsNotForPublicNames(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	b.post("/setup/https/public", publicForm())
	if r := b.get("/ca"); r.status != http.StatusSeeOther || r.location != "/setup" {
		t.Fatalf("GET /ca with a public name = %d %q", r.status, r.location)
	}
}

// Acceptance: given a certificate that cannot be renewed, then parents see a
// warning in the web interface (and the API).
func TestParentsSeeAWarningWhenTheCertificateCannotBeRenewed(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	b := c.b
	b.public.name = "mia.dedyn.io"
	const notice = `data-https-warning`

	for _, page := range []string{"/", "/calendars"} {
		if r := b.get(page); strings.Contains(r.body, notice) {
			t.Fatalf("warning on %s while all is well", page)
		}
	}
	if status, body, _ := c.call(http.MethodGet, "/https", nil); status != http.StatusOK || body["mode"] != "public" || body["warning"] != false || body["name"] != "mia.dedyn.io" {
		t.Fatalf("GET /https = %d %v", status, body)
	}

	b.public.mu.Lock()
	b.public.status.Warning, b.public.status.RenewalError = true, "acme: rate limited"
	b.public.mu.Unlock()
	for _, page := range []string{"/", "/calendars"} {
		r := b.do(http.MethodGet, page, nil, map[string]string{"Accept-Language": "en"})
		if !strings.Contains(r.body, notice) || !strings.Contains(r.body, "certificate") {
			t.Errorf("no warning on %s:\n%s", page, r.body)
		}
	}
	if _, body, _ := c.call(http.MethodGet, "/https", nil); body["warning"] != true || body["renewal_error"] != "acme: rate limited" {
		t.Errorf("GET /https = %v", body)
	}
}

func TestOwnCAModeReportsNoWarning(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	status, body, _ := c.call(http.MethodGet, "/https", nil)
	if status != http.StatusOK || body["mode"] != "own-ca" || body["warning"] != false {
		t.Fatalf("GET /https = %d %v", status, body)
	}
	if r := c.b.get("/"); strings.Contains(r.body, "data-https-warning") {
		t.Error("warning without a public certificate")
	}
}

// A name check that cannot pass (the router offers no rebind exception) must
// not trap the family: the check page offers the box's own CA instead, which
// forgets the public setup (ADR 0014).
func TestNameCheckPageOffersTheOwnCAAndSwitchingForgetsThePublicSetup(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	b.post("/setup/https/public", publicForm())
	for lang, want := range map[string]string{"de": "Router lässt sich nicht ändern?", "en": "Cannot change the router?"} {
		r := b.do(http.MethodGet, "/setup/https/check", nil, map[string]string{"Accept-Language": lang})
		if !strings.Contains(r.body, `action="/setup/https/own-ca"`) || !strings.Contains(r.body, want) {
			t.Errorf("%s: the check page offers no way to the own CA:\n%s", lang, r.body)
		}
	}

	// The browser is still on plain HTTP: the public name cannot be reached.
	r := b.post("/setup/https/own-ca", url.Values{"offline": {"off"}}) // with the answer to "Should the box stay fully offline?"
	if r.status != http.StatusSeeOther || r.location != "/ca" {
		t.Fatalf("POST own-ca = %d %q, want 303 /ca", r.status, r.location)
	}
	if b.public.Ready() || b.public.cleared != 1 {
		t.Errorf("the public setup stays: ready=%v cleared=%d", b.public.Ready(), b.public.cleared)
	}
	if !b.pki.Ready() {
		t.Error("the box's own CA is not set up")
	}
}

func TestSwitchingToTheOwnCAIsClosedOnceTheBoxIsSetUpOrHasNoPublicName(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	c.b.public.name = "mia.dedyn.io"
	if r := c.b.post("/setup/https/own-ca", url.Values{}); r.status != http.StatusSeeOther || r.location != "/setup" {
		t.Errorf("POST on a set-up box = %d %q", r.status, r.location)
	}
	if !c.b.public.Ready() || c.b.public.cleared != 0 {
		t.Error("a set-up box lost its public name")
	}

	fresh := newFreshBox(t)
	if r := fresh.post("/setup/https/own-ca", url.Values{}); r.status != http.StatusSeeOther || r.location != "/setup/https" {
		t.Errorf("POST without a public name = %d %q", r.status, r.location)
	}
	if fresh.pki.Ready() {
		t.Error("own CA created without a failed public setup")
	}
}

// A stored setup that cannot be resumed (lost secret.key) leaves the box up;
// parents and the wizard are told.
func TestParentsAndTheWizardSeeAWarningWhenThePublicSetupCannotBeResumed(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	c.b.public.status = publiccert.Status{Warning: true, RenewalError: "publiccert: DNS token: cipher: message authentication failed"}
	if r := c.b.do(http.MethodGet, "/", nil, map[string]string{"Accept-Language": "en"}); !strings.Contains(r.body, "data-https-warning") {
		t.Errorf("no warning on the start page:\n%s", r.body)
	}

	if _, body, _ := c.call(http.MethodGet, "/https", nil); body["warning"] != true || body["renewal_error"] == "" {
		t.Errorf("the API shows no warning: %v", body)
	}

	fresh := newFreshBox(t)
	fresh.public.status = publiccert.Status{Warning: true, RenewalError: "x"}
	r := fresh.do(http.MethodGet, "/setup/https?way=public", nil, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusOK || !strings.Contains(r.body, "data-https-warning") || !strings.Contains(r.body, `action="/setup/https/public"`) {
		t.Errorf("the wizard shows no warning or no form to redo the step:\n%s", r.body)
	}
}

// The family's browser may give up while the CA is slow; the order must not
// be cancelled partway through.
func TestPublicSetupSurvivesTheBrowserGivingUp(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	b.public.gate = make(chan struct{})
	req, _ := http.NewRequest(http.MethodPost, b.srv.URL+"/setup/https/public", strings.NewReader(publicForm().Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", b.srv.URL)
	if res, err := (&http.Client{Timeout: 200 * time.Millisecond}).Do(req); err == nil {
		res.Body.Close()
		t.Fatal("the request finished although Setup was held back")
	}
	time.Sleep(100 * time.Millisecond) // let the server notice the closed connection
	close(b.public.gate)
	deadline := time.Now().Add(3 * time.Second)
	for len(b.public.requests()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	b.public.mu.Lock()
	defer b.public.mu.Unlock()
	if b.public.ctxErr != nil {
		t.Errorf("Setup was cancelled with the request: %v", b.public.ctxErr)
	}
	if !b.public.ctxDeadline {
		t.Error("Setup has no time limit of its own")
	}
}

// A configured box whose token cannot be opened still holds its certificate
// (name set, warning raised): plain HTTP must keep redirecting, never serve
// login, API or CalDAV (ADR 0014).
func TestConfiguredBoxWithAWarnedPublicSetupStillRedirectsHTTP(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	b := c.b
	b.public.name = "mia.dedyn.io"
	b.public.status = publiccert.Status{Warning: true, RenewalError: "publiccert: DNS token: x"}
	plain := b.plainServer()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, path := range []string{"/login", "/api/v1/https", "/dav/"} {
		res, err := client.Get(plain.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusPermanentRedirect || res.Header.Get("Location") != "https://mia.dedyn.io"+path {
			t.Errorf("GET http://%s = %d %q", path, res.StatusCode, res.Header.Get("Location"))
		}
	}
}

// A failed public attempt may leave a warning in the manager; choosing the
// box's own CA afterwards must leave none behind.
func TestOwnCAAfterAFailedPublicAttemptLeavesNoWarning(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	b.public.failure = errors.New("acme: the CA is down")
	b.public.warnOnFailure = true
	if r := b.post("/setup/https/public", publicForm()); r.status != http.StatusBadGateway {
		t.Fatalf("failed attempt = %d", r.status)
	}
	if r := b.post("/setup/https", url.Values{}); r.status != http.StatusSeeOther || r.location != "/ca" {
		t.Fatalf("own CA = %d %q", r.status, r.location)
	}
	if st := b.public.Status(); st.Warning || st.RenewalError != "" {
		t.Errorf("warning stays after the own CA was provisioned: %+v", st)
	}
}

// A stored public setup whose certificate could not be obtained: the manager
// is ready but has nothing to serve, so the box's own CA is what personal devices get.
func TestOwnCAFallbackWithoutAPublicCertificateIsReportedAndOffersItsTrustPage(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	b := c.b
	b.public.name, b.public.noCert = "mia.dedyn.io", true
	b.public.status = publiccert.Status{Warning: true, RenewalError: "acme: unreachable"}

	r := b.get("/ca")
	if r.status != http.StatusOK || !strings.Contains(r.body, b.pki.CAFingerprint()) {
		t.Errorf("GET /ca = %d, want the fingerprint of the own CA:\n%s", r.status, r.body)
	}
	if status, body, _ := c.call(http.MethodGet, "/https", nil); status != http.StatusOK || body["mode"] != "own-ca" || body["warning"] != true || body["renewal_error"] != "acme: unreachable" {
		t.Errorf("GET /https = %d %v, want own-ca with a warning", status, body)
	}
}

// The same fallback before setup is complete (the box restarted during the
// name check and could not obtain the certificate): the wizard must not
// redirect away from the HTTPS step, and the own CA must be choosable.
func TestWizardOffersTheHTTPSStepAgainWhenThePublicCertificateIsMissing(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	b.public.name, b.public.noCert = "mia.dedyn.io", true
	if r := b.get("/setup/https"); r.status != http.StatusOK {
		t.Fatalf("GET /setup/https = %d %q, want the step again", r.status, r.location)
	}
	r := b.post("/setup/https/own-ca", url.Values{"offline": {"off"}}) // with the answer to "Should the box stay fully offline?"
	if r.status != http.StatusSeeOther || r.location != "/ca" || !b.pki.Ready() {
		t.Fatalf("POST own-ca = %d %q ready=%v, want 303 /ca", r.status, r.location, b.pki.Ready())
	}
}

// The box restarted during the name check: its public certificate is there,
// no own CA was made for the public name, and the own CA can still be chosen.
func TestOwnCAChoiceAfterARestartDuringTheNameCheck(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	b.public.name = "mia.dedyn.io"                                     // as after Start with the stored setup
	r := b.post("/setup/https/own-ca", url.Values{"offline": {"off"}}) // with the answer to "Should the box stay fully offline?"
	if r.status != http.StatusSeeOther || r.location != "/ca" {
		t.Fatalf("POST own-ca = %d %q, want 303 /ca", r.status, r.location)
	}
	if r := b.get("/ca"); r.status != http.StatusOK || !strings.Contains(r.body, b.pki.CAFingerprint()) {
		t.Errorf("GET /ca = %d", r.status)
	}
}

// Ticket 201, Q142: the way out of a stuck public name is another way into the
// home network, so the wizard asks "Should the box stay fully offline?" here
// too: the answer was taken back when the public form was submitted. The
// browser is on plain HTTP (the public name does not work), so the question
// is answered at the same address.
func TestSwitchingToTheOwnCAAsksWhetherTheBoxStaysOffline(t *testing.T) {
	b := newFreshBox(t)
	b.post("/setup/https/public", publicForm())
	if box, _ := b.db.OutboundBox(); box.Asked {
		t.Fatal("the public form left the offline question answered")
	}
	r := b.do(http.MethodPost, "/setup/https/own-ca", url.Values{}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusOK {
		t.Fatalf("POST own-ca = %d %q, want the offline question", r.status, r.location)
	}
	mustContain(t, r.body, "Should the box stay fully offline?", `action="/setup/https/own-ca"`, `name="offline" value="on"`)
	mustNotContain(t, r.body, `href="/setup/https"`) // the way back needs the public name
	if b.pki.Ready() || b.public.cleared != 0 {
		t.Errorf("the own CA was set up (ready=%v) or the public setup forgotten (%d) before the question was answered", b.pki.Ready(), b.public.cleared)
	}
	if r := b.post("/setup/https/own-ca", url.Values{"offline": {"maybe"}}); r.status != http.StatusUnprocessableEntity || b.pki.Ready() {
		t.Errorf("a made-up answer = %d (ready=%v), want 422 and nothing set up", r.status, b.pki.Ready())
	}
	if r := b.post("/setup/https/own-ca", url.Values{"offline": {"on"}}); r.status != http.StatusSeeOther || r.location != "/ca" || !b.pki.Ready() || b.public.cleared != 1 {
		t.Fatalf("answered: POST own-ca = %d %q ready=%v cleared=%d, want 303 /ca", r.status, r.location, b.pki.Ready(), b.public.cleared)
	}
	if box, _ := b.db.OutboundBox(); !box.Asked || !box.Offline {
		t.Errorf("box = %+v, want asked and offline", box)
	}
}

// Review of ticket 201: the answer is stored only once the public setup is
// forgotten and the own CA is made. A failure in between leaves the question
// open, so a retry asks again instead of finding offline mode on over a
// public setup that is still stored.
func TestSwitchingToTheOwnCAStoresTheOfflineAnswerOnlyOnceItWorked(t *testing.T) {
	b := newFreshBox(t)
	b.post("/setup/https/public", publicForm())
	b.public.clearErr = errors.New("disk full")
	if r := b.post("/setup/https/own-ca", url.Values{"offline": {"on"}}); r.status != http.StatusInternalServerError {
		t.Fatalf("POST own-ca with a failing Clear = %d, want 500", r.status)
	}
	if box, _ := b.db.OutboundBox(); box.Asked || box.Offline {
		t.Fatalf("box = %+v, want the question still open and offline mode off", box)
	}
	b.public.clearErr = nil
	r := b.do(http.MethodPost, "/setup/https/own-ca", url.Values{}, map[string]string{"Accept-Language": "en"})
	mustContain(t, r.body, "Should the box stay fully offline?")
	if r := b.post("/setup/https/own-ca", url.Values{"offline": {"on"}}); r.status != http.StatusSeeOther || r.location != "/ca" {
		t.Fatalf("retry = %d %q, want 303 /ca", r.status, r.location)
	}
	if box, _ := b.db.OutboundBox(); !box.Asked || !box.Offline {
		t.Errorf("box = %+v, want asked and offline", box)
	}
}
