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
	b := newFreshBox(t)

	if r := b.get("/setup"); r.status != http.StatusSeeOther || r.location != "/setup/https" {
		t.Fatalf("GET /setup = %d %q, want 303 to /setup/https", r.status, r.location)
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
	b := newFreshBox(t)
	de := b.do(http.MethodGet, "/setup/https", nil, map[string]string{"Accept-Language": "de"})
	en := b.do(http.MethodGet, "/setup/https", nil, map[string]string{"Accept-Language": "en"})
	if de.status != http.StatusOK || !strings.Contains(de.body, "Verschlüsselung") || !strings.Contains(de.body, `action="/setup/https"`) {
		t.Errorf("German HTTPS step:\n%s", de.body)
	}
	if en.status != http.StatusOK || !strings.Contains(en.body, "encryption") {
		t.Errorf("English HTTPS step:\n%s", en.body)
	}
}

func TestCreatingTheCertificateAuthorityUnlocksThePasswordStepOverHTTPS(t *testing.T) {
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
	if r := b.post("/setup", validSetupForm()); r.status != http.StatusForbidden {
		t.Fatalf("POST /setup over HTTP = %d, want 403", r.status)
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

	// Sending a body over HTTP is refused instead of followed.
	res, err = client.Post(plain.URL+"/api/v1/tokens", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("POST http:// = %d, want 403", res.StatusCode)
	}
}

func TestRedirectKeepsAnConfiguredPublicHTTPSPort(t *testing.T) {
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

func TestPlainHTTPStillServesWhatPhonesNeedBeforeTheyTrustTheBox(t *testing.T) {
	b := newBox(t)
	plain := b.plainServer()
	for _, path := range []string{"/healthz", "/ca", "/ca/hejmo-ca.crt", "/ca/hejmo.mobileconfig", "/static/hejmo.css"} {
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
	b := newBox(t)
	r := b.get("/ca/hejmo-ca.crt")
	if r.status != http.StatusOK || r.body != string(b.pki.CAPEM()) {
		t.Fatalf("CA download = %d", r.status)
	}
}

func TestTrustPageExplainsIPhoneAndroidAndBrowsers(t *testing.T) {
	b := newBox(t)
	de := b.do(http.MethodGet, "/ca", nil, map[string]string{"Accept-Language": "de"})
	for _, want := range []string{"iPhone", "DAVx5", "Firefox", "/ca/hejmo.mobileconfig", "/ca/hejmo-ca.crt", "Zertifikat"} {
		if !strings.Contains(de.body, want) {
			t.Errorf("German trust page misses %q", want)
		}
	}
	en := b.do(http.MethodGet, "/ca", nil, map[string]string{"Accept-Language": "en"})
	if !strings.Contains(en.body, "certificate") || !strings.Contains(en.body, "DAVx5") {
		t.Errorf("English trust page:\n%s", en.body)
	}
}

func TestTrustPageNeedsHTTPSToBeSetUp(t *testing.T) {
	b := newFreshBox(t)
	if r := b.get("/ca"); r.status != http.StatusSeeOther || r.location != "/setup/https" {
		t.Fatalf("GET /ca before HTTPS = %d %q", r.status, r.location)
	}
	if r := b.get("/ca/hejmo.mobileconfig"); r.status != http.StatusNotFound {
		t.Fatalf("profile before HTTPS = %d, want 404", r.status)
	}
}
