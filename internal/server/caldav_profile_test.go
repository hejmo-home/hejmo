package server_test

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var profilePasswordRe = regexp.MustCompile(`<key>CalDAVPassword</key>\s*<string>(hejmo_[^<]+)</string>`)

// Ticket 74, ADR 0017: a signed-in parent downloads an iOS profile that holds
// the box address, their user name and a new device token with scope calendars.
func TestParentDownloadsAnIOSProfileWithANewDeviceToken(t *testing.T) {
	c := signedInAPI(t)
	b := c.b
	h, _ := familyOf(t, c)

	res := b.do(http.MethodPost, "/caldav/profile", url.Values{"name": {"iPhone Mama"}}, nil)
	if res.status != http.StatusOK {
		t.Fatalf("profile = %d\n%s", res.status, res.body)
	}
	m := profilePasswordRe.FindStringSubmatch(res.body)
	if m == nil {
		t.Fatalf("no device token in the profile:\n%s", res.body)
	}
	for _, want := range []string{"com.apple.caldav.account", "<string>mama</string>", "<string>iPhone Mama</string>", "https://127.0.0.1:"} {
		if !strings.Contains(res.body, want) {
			t.Errorf("profile misses %q:\n%s", want, res.body)
		}
	}

	// A new token with scope calendars exists, named after the device.
	toks, _ := b.db.APITokens(h.ID)
	if len(toks) != 1 || toks[0].Name != "iPhone Mama" || toks[0].Scopes != "calendars" || toks[0].PersonName != "Mama" {
		t.Fatalf("tokens = %+v", toks)
	}
	// It works as the CalDAV password, and lists like any device token.
	if _, err := c.phoneWith(t, "mama", m[1]).calendars(); err != nil {
		t.Fatalf("the token in the profile does not sync: %v", err)
	}
	if page := b.get("/caldav"); strings.Contains(page.body, m[1]) || !strings.Contains(page.body, "iPhone Mama") {
		t.Fatalf("token page:\n%s", page.body)
	}

	// Downloading again makes a new profile with a new token; none is kept.
	again := b.post("/caldav/profile", url.Values{"name": {"iPad Mama"}})
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
	if _, err := c.phoneWith(t, "mama", m[1]).calendars(); err == nil {
		t.Fatal("a revoked profile token still syncs")
	}
	if _, err := c.phoneWith(t, "mama", m2[1]).calendars(); err != nil {
		t.Fatalf("the other profile's token stopped working: %v", err)
	}
}

func TestIOSProfileDownloadHeadersAndFieldError(t *testing.T) {
	c := signedInAPI(t)
	b := c.b
	req, _ := http.NewRequest(http.MethodPost, b.srv.URL+"/caldav/profile", strings.NewReader(url.Values{"name": {"iPhone Mama"}}.Encode()))
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
	r := b.post("/caldav/profile", url.Values{"name": {"  "}})
	after, _ := b.db.APITokens(h.ID)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `role="alert"`) || len(after) != len(before) {
		t.Fatalf("empty name = %d, tokens %d -> %d", r.status, len(before), len(after))
	}
}

func TestIOSProfileIsForSignedInParentsOverHTTPSOnly(t *testing.T) {
	b, _ := configuredBox(t)
	if r := b.post("/caldav/profile", url.Values{"name": {"x"}}); r.status != http.StatusSeeOther || r.location != "/login" {
		t.Fatalf("signed out = %d %q", r.status, r.location)
	}
	child := childSession(t, b)
	if r := child.post("/caldav/profile", url.Values{"name": {"x"}}); r.status == http.StatusOK {
		t.Fatalf("a child got a profile")
	}

	c := signedInAPI(t)
	// Plain HTTP is sent to HTTPS (a token must never travel unencrypted).
	plain := c.b.plainServer()
	req, _ := http.NewRequest(http.MethodPost, plain.URL+"/caldav/profile", strings.NewReader(url.Values{"name": {"x"}}.Encode()))
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
	c := signedInAPI(t)
	h, _ := familyOf(t, c)
	raw, status, ct := c.callRaw(http.MethodPost, "/caldav-profile", map[string]any{"name": "iPad Mama"})
	m := profilePasswordRe.FindStringSubmatch(raw)
	if status != http.StatusOK || ct != "application/x-apple-aspen-config" || m == nil {
		t.Fatalf("profile = %d %q\n%s", status, ct, raw)
	}
	toks, _ := c.b.db.APITokens(h.ID)
	if len(toks) != 1 || toks[0].Scopes != "calendars" || toks[0].Name != "iPad Mama" {
		t.Fatalf("tokens = %+v", toks)
	}
	if _, err := c.phoneWith(t, "mama", m[1]).calendars(); err != nil {
		t.Fatalf("API profile token does not sync: %v", err)
	}
	if _, st, _ := c.callRaw(http.MethodPost, "/caldav-profile", map[string]any{"name": " "}); st != http.StatusUnprocessableEntity {
		t.Fatalf("empty name = %d, want 422", st)
	}
	// A read-only token and a device token must not mint further tokens.
	for _, scopes := range []string{"read", "calendars"} {
		if _, st, _ := c.withToken(c.newToken(scopes)).callRaw(http.MethodPost, "/caldav-profile", map[string]any{"name": "x"}); st != http.StatusForbidden {
			t.Errorf("token %q = %d, want 403", scopes, st)
		}
	}
}
