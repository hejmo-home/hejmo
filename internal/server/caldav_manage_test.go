package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func (c *apiClient) calendarJSON(t *testing.T, id string) map[string]any {
	t.Helper()
	_, _, list := c.call(http.MethodGet, "/calendars", nil)
	for _, x := range list {
		if cal := x.(map[string]any); cal["id"] == id {
			return cal
		}
	}
	t.Fatalf("calendar %s not listed", id)
	return nil
}

// ADR 0012: the family calendar is always served; ICS calendars are shared by
// default; a parent can switch sharing off for synced calendars.
func TestShareThroughTheBoxIsVisibleAndChangeableThroughTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, family := familyOf(t, c)
	c.b.feeds.set(schoolFeed, feedOf(), nil)
	school := c.addICS(t, map[string]any{"kind": "ics", "name": "School", "url": schoolFeed})
	id := school["id"].(string)

	if school["share_through_box"] != true || c.calendarJSON(t, family.ID)["share_through_box"] != true {
		t.Fatalf("defaults: school %v, family %v", school["share_through_box"], c.calendarJSON(t, family.ID)["share_through_box"])
	}
	status, got, _ := c.call(http.MethodPatch, "/calendars/"+id, map[string]any{"share_through_box": false})
	if status != http.StatusOK || got["share_through_box"] != false || c.calendarJSON(t, id)["share_through_box"] != false {
		t.Fatalf("switch off = %d %v", status, got)
	}
	if status, got, _ := c.call(http.MethodPatch, "/calendars/"+id, map[string]any{"share_through_box": true}); status != http.StatusOK || got["share_through_box"] != true {
		t.Fatalf("switch on = %d %v", status, got)
	}
	if status, _, _ := c.call(http.MethodPatch, "/calendars/"+family.ID, map[string]any{"share_through_box": false}); status != http.StatusUnprocessableEntity {
		t.Fatalf("family calendar = %d, want 422", status)
	}
	if status, _, _ := c.call(http.MethodPatch, "/calendars/00000000-0000-4000-8000-000000000000", map[string]any{"share_through_box": false}); status != http.StatusNotFound {
		t.Fatalf("unknown calendar = %d, want 404", status)
	}
	ro := c.withToken(c.newToken("read"))
	if status, _, _ := ro.call(http.MethodPatch, "/calendars/"+id, map[string]any{"share_through_box": false}); status != http.StatusForbidden {
		t.Fatalf("read-only token = %d, want 403", status)
	}
}

func TestDeviceTokensAreListedWithoutTheirSecret(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	status, made, _ := c.call(http.MethodPost, "/tokens", map[string]any{"name": "iPhone Mama", "scopes": "calendars"})
	if status != http.StatusCreated {
		t.Fatalf("create = %d", status)
	}
	// Using the token marks it as used.
	ph := c.phoneWith(t, "mama", made["token"].(string))
	if _, err := ph.calendars(); err != nil {
		t.Fatal(err)
	}

	status, _, list := c.call(http.MethodGet, "/tokens", nil)
	if status != http.StatusOK || len(list) != 1 {
		t.Fatalf("list = %d %v", status, list)
	}
	tok := list[0].(map[string]any)
	if tok["id"] != made["id"] || tok["name"] != "iPhone Mama" || tok["scopes"] != "calendars" || tok["person"] != "Mama" || tok["last_used_at"] == nil {
		t.Fatalf("token = %v", tok)
	}
	if _, leaks := tok["token"]; leaks {
		t.Fatal("the list shows the token value")
	}
	c.call(http.MethodDelete, "/tokens/"+made["id"].(string), nil)
	if _, _, list := c.call(http.MethodGet, "/tokens", nil); len(list) != 0 {
		t.Fatalf("revoked token still listed: %v", list)
	}
	narrow := c.withToken(c.newToken("people"))
	if status, _, _ := narrow.call(http.MethodGet, "/tokens", nil); status != http.StatusForbidden {
		t.Fatalf("token without tokens:read lists tokens = %d, want 403", status)
	}
}

var deviceTokenRe = regexp.MustCompile(`data-device-token="(hejmo_[^"]+)"`)

// A parent creates a device token ("iPhone Mama") in the web interface and
// uses it as the password; tokens are revoked one by one.
func TestParentManagesDeviceTokensAndSharingInTheWebInterface(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	b := c.b
	h, _ := familyOf(t, c)
	b.feeds.set(schoolFeed, feedOf(), nil)
	school := c.addICS(t, map[string]any{"kind": "ics", "name": "School", "url": schoolFeed})

	page := b.do(http.MethodGet, "/caldav", nil, map[string]string{"Accept-Language": "en"})
	for _, want := range []string{"/dav/", "mama", "School", "Family"} {
		if page.status != http.StatusOK || !strings.Contains(page.body, want) {
			t.Fatalf("page misses %q (%d):\n%s", want, page.status, page.body)
		}
	}

	r := b.do(http.MethodPost, "/caldav/tokens", url.Values{"name": {"iPhone Mama"}}, map[string]string{"Accept-Language": "en"})
	m := deviceTokenRe.FindStringSubmatch(r.body)
	if r.status != http.StatusOK || m == nil || !strings.Contains(r.body, "iPhone Mama") {
		t.Fatalf("create = %d\n%s", r.status, r.body)
	}
	secret := m[1]
	if _, err := c.phoneWith(t, "mama", secret).calendars(); err != nil {
		t.Fatalf("the shown token does not work: %v", err)
	}
	// The value is shown once only; the API lists the token (parity).
	if again := b.get("/caldav"); strings.Contains(again.body, secret) || !strings.Contains(again.body, "iPhone Mama") {
		t.Fatalf("token page after creation:\n%s", again.body)
	}
	_, _, list := c.call(http.MethodGet, "/tokens", nil)
	if len(list) != 1 || list[0].(map[string]any)["name"] != "iPhone Mama" || list[0].(map[string]any)["scopes"] != "calendars tasks lists" {
		t.Fatalf("API list = %v", list)
	}

	if r := b.post("/caldav/tokens", url.Values{"name": {"  "}}); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `role="alert"`) {
		t.Fatalf("empty name = %d", r.status)
	}

	// Sharing, with parity in the API.
	if r := b.post("/caldav/calendars/"+school["id"].(string)+"/share", url.Values{"shared": {"off"}}); r.status != http.StatusSeeOther || r.location != "/caldav" {
		t.Fatalf("switch off = %d %q", r.status, r.location)
	}
	if c.calendarJSON(t, school["id"].(string))["share_through_box"] != false {
		t.Fatal("web switch is not visible in the API")
	}
	if r := b.post("/caldav/calendars/"+school["id"].(string)+"/share", url.Values{"shared": {"on"}}); r.status != http.StatusSeeOther {
		t.Fatalf("switch on = %d", r.status)
	}
	if c.calendarJSON(t, school["id"].(string))["share_through_box"] != true {
		t.Fatal("web switch on is not visible in the API")
	}
	if r := b.post("/caldav/calendars/nope/share", url.Values{"shared": {"off"}}); r.status != http.StatusNotFound {
		t.Fatalf("unknown calendar = %d, want 404", r.status)
	}

	toks, _ := b.db.APITokens(h.ID)
	if r := b.post("/caldav/tokens/"+toks[0].ID+"/revoke", url.Values{}); r.status != http.StatusSeeOther || r.location != "/caldav" {
		t.Fatalf("revoke = %d %q", r.status, r.location)
	}
	if _, err := c.phoneWith(t, "mama", secret).calendars(); err == nil {
		t.Fatal("a token revoked in the web interface still works")
	}
	if r := b.post("/caldav/tokens/"+toks[0].ID+"/revoke", url.Values{}); r.status != http.StatusNotFound {
		t.Fatalf("revoking twice = %d, want 404", r.status)
	}
}

func TestCalDAVPageIsGerman(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	page := c.b.do(http.MethodGet, "/caldav", nil, map[string]string{"Accept-Language": "de"})
	if !strings.Contains(page.body, "Kalender-Apps") {
		t.Fatalf("German page:\n%s", page.body)
	}
}

func TestCalDAVPageNeedsASignedInParent(t *testing.T) {
	t.Parallel()
	b, _ := configuredBox(t)
	if r := b.get("/caldav"); r.status != http.StatusSeeOther || r.location != "/login?next=%2Fcaldav" {
		t.Fatalf("signed out = %d %q", r.status, r.location)
	}
	if r := b.post("/caldav/tokens", url.Values{"name": {"x"}}); r.status != http.StatusSeeOther || r.location != "/login" {
		t.Fatalf("signed out post = %d %q", r.status, r.location)
	}
}

func TestUsingATokenOverTheRESTAPIMarksItAsUsed(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, made, _ := c.call(http.MethodPost, "/tokens", map[string]any{"name": "script", "scopes": "read"})
	if status, _, _ := c.withToken(made["token"].(string)).call(http.MethodGet, "/me", nil); status != http.StatusOK {
		t.Fatalf("me = %d", status)
	}
	_, _, list := c.call(http.MethodGet, "/tokens", nil)
	if len(list) != 1 || list[0].(map[string]any)["last_used_at"] == nil {
		t.Fatalf("tokens = %v, want last_used_at set", list)
	}
}

func TestMalformedCalendarPatchIsABadRequest(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, family := familyOf(t, c)
	req, _ := http.NewRequest(http.MethodPatch, c.b.srv.URL+"/api/v1/calendars/"+family.ID, strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.newToken("write"))
	res, err := c.b.bareClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid JSON = %d, want 400", res.StatusCode)
	}
}
