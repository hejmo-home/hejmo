package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/pki"
	"github.com/hejmo-home/hejmo/internal/server"
)

var profilePasswordRe = regexp.MustCompile(`<key>CalDAVPassword</key>\s*<string>(hejmo_[^<]+)</string>`)

// Ticket 74, ADR 0017: a signed-in parent downloads an iOS profile that holds
// the box address, their user name and a new device token with scopes calendars and tasks (ticket 94: task lists
// appear on iPhones set up by profile).
func TestParentDownloadsAnIOSProfileWithANewDeviceToken(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	b := c.b
	h, _ := familyOf(t, c)

	res := b.do(http.MethodPost, "/caldav/profile", url.Values{"profile_name": {"iPhone Mama"}}, nil)
	if res.status != http.StatusOK {
		t.Fatalf("profile = %d\n%s", res.status, res.body)
	}
	m := profilePasswordRe.FindStringSubmatch(res.body)
	if m == nil {
		t.Fatalf("no device token in the profile:\n%s", res.body)
	}
	for _, want := range []string{"com.apple.caldav.account", "com.apple.carddav.account", "<key>CardDAVPrincipalURL</key>", "/carddav/", "<string>mama</string>", "<string>iPhone Mama</string>", "https://127.0.0.1:"} {
		if !strings.Contains(res.body, want) {
			t.Errorf("profile misses %q:\n%s", want, res.body)
		}
	}

	// A new token with scope calendars exists, named after the device.
	toks, _ := b.db.APITokens(h.ID)
	if len(toks) != 1 || toks[0].Name != "iPhone Mama" || toks[0].Scopes != "calendars tasks contacts" || toks[0].PersonName != "Mama" {
		t.Fatalf("tokens = %+v", toks)
	}
	// It works as the CalDAV password, and lists like any device token.
	cals, err := c.personalDeviceWith(t, "mama", m[1]).calendars()
	if err != nil {
		t.Fatalf("the token in the profile does not sync: %v", err)
	}
	if len(cals) < 2 {
		t.Fatalf("the profile's token sees no task list: %v", cals)
	}
	// The same token opens the address book (ticket 299).
	if _, err := c.addressDeviceWith(t, m[1]).book(); err != nil {
		t.Fatalf("the profile's token does not sync contacts: %v", err)
	}
	if page := b.get("/caldav"); strings.Contains(page.body, m[1]) || !strings.Contains(page.body, "iPhone Mama") {
		t.Fatalf("token page:\n%s", page.body)
	}

	// Downloading again makes a new profile with a new token; none is kept.
	again := b.post("/caldav/profile", url.Values{"profile_name": {"iPad Mama"}})
	m2 := profilePasswordRe.FindStringSubmatch(again.body)
	if again.status != http.StatusOK || m2 == nil || m2[1] == m[1] {
		t.Fatalf("second profile = %d, tokens %v %v", again.status, m, m2)
	}

	// Revoked: the iPhone can no longer sync.
	toks, _ = b.db.APITokens(h.ID)
	for _, tok := range toks {
		if tok.Name == "iPhone Mama" {
			if r := b.post("/caldav/tokens/"+tok.ID+"/revoke", url.Values{}); r.status != http.StatusSeeOther {
				t.Fatalf("revoke = %d", r.status)
			}
		}
	}
	if _, err := c.personalDeviceWith(t, "mama", m[1]).calendars(); err == nil {
		t.Fatal("a revoked profile token still syncs")
	}
	if _, err := c.personalDeviceWith(t, "mama", m2[1]).calendars(); err != nil {
		t.Fatalf("the other profile's token stopped working: %v", err)
	}
}

func TestIOSProfileDownloadHeadersAndFieldError(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	b := c.b
	req, _ := http.NewRequest(http.MethodPost, b.srv.URL+"/caldav/profile", strings.NewReader(url.Values{"profile_name": {"iPhone Mama"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", b.srv.URL)
	res, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "application/x-apple-aspen-config" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".mobileconfig") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, the profile holds a token", cc)
	}

	// An empty name is a field error and creates no token.
	h, _ := familyOf(t, c)
	before, _ := b.db.APITokens(h.ID)
	r := b.post("/caldav/profile", url.Values{"profile_name": {"  "}})
	after, _ := b.db.APITokens(h.ID)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `class="ferr" id="err-profile_name"`) || !strings.Contains(r.body, `aria-invalid="true" aria-describedby="err-profile_name"`) || strings.Contains(r.body, `<p class="error"`) || len(after) != len(before) {
		t.Fatalf("empty name = %d, tokens %d -> %d", r.status, len(before), len(after))
	}
}

func TestIOSProfileIsForSignedInParentsOverHTTPSOnly(t *testing.T) {
	t.Parallel()
	b, _ := configuredBox(t)
	if r := b.post("/caldav/profile", url.Values{"profile_name": {"x"}}); r.status != http.StatusSeeOther || r.location != "/login" {
		t.Fatalf("signed out = %d %q", r.status, r.location)
	}
	child := childSession(t, b)
	if r := child.post("/caldav/profile", url.Values{"profile_name": {"x"}}); r.status == http.StatusOK {
		t.Fatalf("a child got a profile")
	}

	c := signedInAPI(t)
	// With HTTPS set up, plain HTTP is sent there (a token must never travel
	// unencrypted); the 403 without a certificate is tested below.
	plain := c.b.plainServer()
	req, _ := http.NewRequest(http.MethodPost, plain.URL+"/caldav/profile", strings.NewReader(url.Values{"profile_name": {"x"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode == http.StatusOK || strings.Contains(string(body), "hejmo_") {
		t.Fatalf("plain HTTP = %d, want a redirect to HTTPS", res.StatusCode)
	}
}

func TestIOSProfileFlowPointsToTheCAProfileOnlyForTheOwnCA(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	page := c.b.do(http.MethodGet, "/caldav", nil, map[string]string{"Accept-Language": "en"})
	if !strings.Contains(page.body, `action="/caldav/profile"`) || !strings.Contains(page.body, `data-ca-profile-first`) || !strings.Contains(page.body, "/ca") {
		t.Fatalf("own-CA page:\n%s", page.body)
	}
	c.b.public.name = "mia.dedyn.io"
	page = c.b.get("/caldav")
	if !strings.Contains(page.body, `action="/caldav/profile"`) || strings.Contains(page.body, `data-ca-profile-first`) {
		t.Fatalf("public-certificate page:\n%s", page.body)
	}
}

func TestIOSProfileTextsAreInBothLanguages(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	de := c.b.do(http.MethodGet, "/caldav", nil, map[string]string{"Accept-Language": "de"})
	if !strings.Contains(de.body, "Profil für iPhone") {
		t.Fatalf("German page:\n%s", de.body)
	}
	en := c.b.do(http.MethodGet, "/caldav", nil, map[string]string{"Accept-Language": "en"})
	if !strings.Contains(en.body, "iPhone profile") {
		t.Fatalf("English page:\n%s", en.body)
	}
}

// API parity (ADR 0015): the same profile through the REST API, own account only.
func TestIOSProfileThroughTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	h, _ := familyOf(t, c)
	raw, status, ct := c.callRaw(http.MethodPost, "/caldav-profile", map[string]any{"name": "iPad Mama"})
	m := profilePasswordRe.FindStringSubmatch(raw)
	if status != http.StatusOK || ct != "application/x-apple-aspen-config" || m == nil {
		t.Fatalf("profile = %d %q\n%s", status, ct, raw)
	}
	toks, _ := c.b.db.APITokens(h.ID)
	if len(toks) != 1 || toks[0].Scopes != "calendars tasks contacts" || toks[0].Name != "iPad Mama" {
		t.Fatalf("tokens = %+v", toks)
	}
	if cals, err := c.personalDeviceWith(t, "mama", m[1]).calendars(); err != nil || len(cals) < 2 {
		t.Fatalf("API profile token does not sync calendars and task lists: %v %v", cals, err)
	}
	if _, st, _ := c.callRaw(http.MethodPost, "/caldav-profile", map[string]any{"name": " "}); st != http.StatusUnprocessableEntity {
		t.Fatalf("empty name = %d, want 422", st)
	}
	// A read-only token and a device token must not mint further tokens.
	for _, scopes := range []string{"read", "calendars", "calendars tasks"} {
		if _, st, _ := c.withToken(c.newToken(scopes)).callRaw(http.MethodPost, "/caldav-profile", map[string]any{"name": "x"}); st != http.StatusForbidden {
			t.Errorf("token %q = %d, want 403", scopes, st)
		}
	}
}

// plainBoxSignedIn returns a parent's client on a box that has no certificate
// yet, served over plain HTTP: requireHTTPS has nowhere to redirect to.
func plainBoxSignedIn(t *testing.T) *apiClient {
	t.Helper()
	b, secret := configuredBox(t)
	fresh, err := pki.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := b.now
	b.pki = fresh
	b.handler = server.New(server.Options{DB: b.db, PKI: fresh, Public: &fakePublic{}, Now: func() time.Time { return now }})
	b.srv = httptest.NewServer(b.handler)
	t.Cleanup(b.srv.Close)
	b.client = b.trustingClient()
	if r := b.post("/login", loginForm(b, secret, "correct horse battery")); r.status != http.StatusSeeOther {
		t.Fatalf("login on the plain box = %d", r.status)
	}
	c := newAPIClient(t, b)
	c.cookie = true
	return c
}

func TestIOSProfileIsRefusedOnPlainHTTPBeforeHTTPSIsSetUp(t *testing.T) {
	t.Parallel()
	c := plainBoxSignedIn(t)
	b := c.b
	h, _ := familyOf(t, c)

	for lang, want := range map[string]string{"de": "nur über HTTPS", "en": "only offered over HTTPS"} {
		r := b.do(http.MethodPost, "/caldav/profile", url.Values{"profile_name": {"iPhone Mama"}}, map[string]string{"Accept-Language": lang})
		if r.status != http.StatusForbidden || !strings.Contains(r.body, `role="alert"`) || !strings.Contains(r.body, want) || strings.Contains(r.body, "hejmo_") {
			t.Errorf("web %s = %d, want 403 with the HTTPS message %q:\n%s", lang, r.status, want, r.body)
		}
	}
	raw, status, _ := c.callRaw(http.MethodPost, "/caldav-profile", map[string]any{"name": "iPhone Mama"})
	if status != http.StatusForbidden || strings.Contains(raw, "hejmo_") {
		t.Errorf("API = %d, want 403:\n%s", status, raw)
	}
	if toks, _ := b.db.APITokens(h.ID); len(toks) != 0 {
		t.Errorf("tokens = %+v, want none", toks)
	}
}

func TestIOSProfileTakesNamesByCharactersAndAPIRejectsBrokenJSONWith400(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	name := strings.Repeat("ä", 80) // 160 bytes, 80 characters: within the limit
	if r := c.b.post("/caldav/profile", url.Values{"profile_name": {name}}); r.status != http.StatusOK {
		t.Errorf("web, 80 umlauts = %d, want 200", r.status)
	}
	if r := c.b.post("/caldav/tokens", url.Values{"name": {name}}); r.status != http.StatusOK {
		t.Errorf("token form, 80 umlauts = %d, want 200", r.status)
	}
	if _, st, _ := c.callRaw(http.MethodPost, "/caldav-profile", map[string]any{"name": name}); st != http.StatusOK {
		t.Errorf("API, 80 umlauts = %d, want 200", st)
	}
	req, _ := http.NewRequest(http.MethodPost, c.b.srv.URL+"/api/v1/caldav-profile", strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.b.srv.URL)
	res, err := c.b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("broken JSON = %d, want 400", res.StatusCode)
	}
}
