package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// Ticket 94: a device token created on the CalDAV clients page opens task
// lists as well, and the page says so in one line.
func TestDeviceTokenFromTheWebPageOpensCalendarsAndTaskLists(t *testing.T) {
	c := signedInAPI(t)
	h, _ := familyOf(t, c)
	res := c.b.post("/caldav/tokens", url.Values{"name": {"iPhone Mama"}})
	if res.status != http.StatusOK {
		t.Fatalf("create = %d", res.status)
	}
	m := regexp.MustCompile(`hejmo_[A-Za-z0-9_-]+`).FindString(res.body)
	toks, _ := c.b.db.APITokens(h.ID)
	if m == "" || len(toks) != 1 || toks[0].Scopes != "calendars tasks" {
		t.Fatalf("token %q, tokens %+v", m, toks)
	}
	cals, err := c.phoneWith(t, "mama", m).calendars()
	if err != nil || len(cals) != 2 {
		t.Fatalf("calendars and task lists = %v, %v", cals, err)
	}
	// Its detail page names what it may do.
	page := c.b.do(http.MethodGet, "/caldav/tokens/"+toks[0].ID, nil, map[string]string{"Accept-Language": "en"})
	if page.status != http.StatusOK || !strings.Contains(page.body, "Read calendars and tasks") {
		t.Fatalf("detail page = %d\n%s", page.status, page.body)
	}
}

func TestDeviceTokensOfBothKindsAreListed(t *testing.T) {
	c := signedInAPI(t)
	h, _ := familyOf(t, c)
	c.newToken("calendars")       // an older device token
	c.newToken("calendars tasks") // a new one
	c.newToken("lists")           // other software, no device
	got, err := c.b.db.APITokens(h.ID)
	if err != nil || len(got) != 3 {
		t.Fatal(got, err)
	}
	page := c.b.get("/caldav")
	if n := strings.Count(page.body, `href="/caldav/tokens/`); n != 2 {
		t.Fatalf("device tokens on the page = %d, want 2", n)
	}
}

func TestCalDAVPageMentionsTaskLists(t *testing.T) {
	c := signedInAPI(t)
	en := c.b.do(http.MethodGet, "/caldav", nil, map[string]string{"Accept-Language": "en"})
	de := c.b.do(http.MethodGet, "/caldav", nil, map[string]string{"Accept-Language": "de"})
	if !strings.Contains(en.body, "one task list per person") || !strings.Contains(de.body, "eine Aufgabenliste pro Person") {
		t.Fatalf("no line on task lists\n%s", en.body)
	}
}
