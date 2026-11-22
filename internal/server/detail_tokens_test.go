package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var tokenHrefRe = regexp.MustCompile(`href="/caldav/tokens/([0-9a-f-]{36})"`)

// newDeviceToken creates a device token in the web interface and returns its
// id and its secret, which only the creating page shows.
func newDeviceToken(t *testing.T, b *box, name string) (id, secret string) {
	t.Helper()
	r := b.do(http.MethodPost, "/caldav/tokens", url.Values{"name": {name}}, map[string]string{"Accept-Language": "en"})
	m := deviceTokenRe.FindStringSubmatch(r.body)
	h := tokenHrefRe.FindStringSubmatch(r.body)
	if r.status != http.StatusOK || m == nil || h == nil {
		t.Fatalf("create token = %d\n%s", r.status, r.body)
	}
	return h[1], m[1]
}

func TestDeviceTokenOverviewListsRowsThatOpenTheDetailPage(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	id, secret := newDeviceToken(t, b, "iPhone Mama")
	body := getEN(b, "/caldav").body
	for _, want := range []string{`<ul class="list-rows">`, `<a href="/caldav/tokens/` + id + `">`, "<b>iPhone Mama</b>", "For ", `class="marker"`, `class="flag warn"`, "Never used", `<dl class="facts">`} {
		if !strings.Contains(body, want) {
			t.Errorf("/caldav lacks %q\n%s", want, body)
		}
	}
	if strings.Contains(body, secret) {
		t.Error("the overview shows the token secret")
	}
	noDestructiveForm(t, "/caldav", body)
}

func TestDeviceTokenRowSaysWhenItWasLastUsedWithSeconds(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, secret := newDeviceToken(t, c.b, "iPhone Mama")
	if _, err := c.phoneWith(t, "mama", secret).calendars(); err != nil {
		t.Fatal(err)
	}
	body := getEN(c.b, "/caldav").body
	if !strings.Contains(body, "Last used ") || strings.Contains(body, "Never used") {
		t.Errorf("a used token still says never used\n%s", body)
	}
	if !regexp.MustCompile(`Last used \d\d\.\d\d\.\d{4},\x{00a0}\d{1,2}:\d\d:\d\d`).MatchString(body) {
		t.Errorf("last use has no seconds\n%s", body)
	}
}

func TestDeviceTokenDetailShowsFactsAndConfirmsRevokingByName(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	id, secret := newDeviceToken(t, b, "iPhone Mama")
	r := getEN(b, "/caldav/tokens/"+id)
	if r.status != http.StatusOK {
		t.Fatalf("GET = %d", r.status)
	}
	for _, want := range []string{
		`<nav class="crumbs" aria-label="Breadcrumb">`, `<a href="/caldav">Calendar apps</a>`, "<h1>iPhone Mama</h1>", "<dt>For</dt>", "<dt>Allowed</dt>", "Read calendars and tasks, save events, change tasks",
		"<dt>Created on</dt>", "<dt>Last used</dt>", "never shows the token itself again",
		`<details class="danger">`, "Revoke access", `action="/caldav/tokens/` + id + `/revoke"`, "Yes, revoke access", "No, change nothing",
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("detail lacks %q\n%s", want, r.body)
		}
	}
	if strings.Contains(r.body, secret) {
		t.Error("the detail page shows the token secret again")
	}
	if strings.Contains(r.body, `<a class="back" href="/settings">`) {
		t.Error("the detail page also shows the hub link")
	}
	if c := b.get("/caldav/tokens/nope"); c.status != http.StatusNotFound {
		t.Errorf("unknown token = %d", c.status)
	}
}

func TestOnlyDeviceTokensHaveADetailPage(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, made, _ := c.call(http.MethodPost, "/tokens", map[string]any{"name": "Script", "scopes": "read"})
	if r := c.b.get("/caldav/tokens/" + made["id"].(string)); r.status != http.StatusNotFound {
		t.Errorf("an API token's page on the device page = %d, want 404", r.status)
	}
}

func TestRevokingATokenNamesItInTheSuccessLine(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	id, _ := newDeviceToken(t, b, "iPhone Mama")
	r := b.post("/caldav/tokens/"+id+"/revoke", url.Values{})
	if r.status != http.StatusSeeOther || r.location != "/caldav" {
		t.Fatalf("revoke = %d to %q", r.status, r.location)
	}
	if body := getEN(b, "/caldav").body; !strings.Contains(body, "Access for “iPhone Mama” revoked.") {
		t.Errorf("success line misses the name\n%s", body)
	}
	if c := b.get("/caldav/tokens/" + id); c.status != http.StatusNotFound {
		t.Errorf("a revoked token's page = %d, want 404", c.status)
	}
}

func TestANewTokenIsShownOnceOnItsOwnPage(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	// A success line is waiting from an earlier save; it must not replace this page's own.
	id := pairedDisplay(t, b, "Hallway", "screen")
	b.post("/displays/"+id+"/settings", url.Values{"show_tomorrow": {"on"}})
	r := b.do(http.MethodPost, "/caldav/tokens", url.Values{"name": {"Phone"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusOK {
		t.Fatalf("create = %d", r.status)
	}
	for _, want := range []string{
		"Device token for “Phone” created.", `<section class="once"`, `data-device-token="hejmo_`, `data-copy`, "Copy token",
		"Server address", "User name", "Password", "the token above", "Done, the token is entered", `/caldav/tokens/`,
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("once page lacks %q\n%s", want, r.body)
		}
	}
	if strings.Contains(r.body, "Saved.") {
		t.Error("a waiting success line replaced the page's own")
	}
	// Nothing was stored: reloading the address does not show it again, and the owner never sees it again.
	m := deviceTokenRe.FindStringSubmatch(r.body)
	h := tokenHrefRe.FindStringSubmatch(r.body)
	if m == nil || h == nil {
		t.Fatal("no secret or detail link on the page")
	}
	for _, page := range []string{"/caldav", "/caldav/tokens/" + h[1]} {
		if strings.Contains(getEN(b, page).body, m[1]) {
			t.Errorf("%s shows the secret again", page)
		}
	}
}

func TestATokenNeedsAValidNameOnTheOverview(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	r := b.do(http.MethodPost, "/caldav/tokens", url.Values{"name": {" "}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-name"`) || strings.Contains(r.body, `class="once"`) {
		t.Errorf("blank name = %d\n%s", r.status, r.body)
	}
}
