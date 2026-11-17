package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/auth"
)

var successRe = regexp.MustCompile(`(?s)<p class="msg ok" role="status" data-success>(.*?)</p>`)

// successLine returns the text of the success line on a page, or "".
func successLine(page string) string {
	m := successRe.FindStringSubmatch(page)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(regexp.MustCompile(`(?s)<svg.*?</svg>`).ReplaceAllString(m[1], ""))
}

var linkTags = regexp.MustCompile(`</?a[^>]*>`)

func getEN(b *box, path string) resp {
	b.t.Helper()
	return b.do(http.MethodGet, path, nil, map[string]string{"Accept-Language": "en"})
}

// ADR 0017, ticket 5.16: every web page that saves something shows a success
// line afterwards, once.
func TestEverySavingPageShowsASuccessLineAfterwards(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	b := tb.parent.b
	b.feeds.set("https://example.org/a.ics", feedOf(), nil)
	taskID := tb.newDaily(t, tb.miaID)
	b.post("/calendars", url.Values{"name": {"Holidays"}, "url": {"https://example.org/a.ics"}, "color": {"#B4583A"}, "interval": {"60"}})
	h, _ := b.db.HomeHousehold()
	cals, _ := b.db.SyncedCalendars(h.ID)
	calID := cals[0].ID
	d := newScreen(b)
	b.post("/displays", pairForm(codeOn(t, d.get("/display", "en").body)))
	displays, _ := b.db.Displays(h.ID)
	displayID := displays[0].ID
	d2 := newScreen(b)
	code2 := codeOn(t, d2.get("/display", "en").body)
	b.post("/caldav/tokens", url.Values{"name": {"Phone"}})
	tokens, _ := b.db.APITokens(h.ID)
	var tokenID string
	for _, tok := range tokens {
		if tok.Name == "Phone" {
			tokenID = tok.ID
		}
	}

	connID := connectedBox(t, b)

	steps := []struct {
		name, path, page string
		form             url.Values
		want             string
	}{
		{"settings", "/settings/household", "/settings/household", url.Values{"time_format": {"12h"}, "temperature_unit": {"c"}}, "Household settings saved."},
		{"task add", "/tasks", "/tasks", url.Values{"person": {tb.miaID}, "title": {"Read"}, "repeat": {"daily"}}, "“Read” created for Mia. Add subtasks if Mia should tick it off in parts."},
		{"task done", "/tasks/" + taskID + "/done", "/tasks", url.Values{"done": {"1"}}, "Marked as done."},
		{"task reopen", "/tasks/" + taskID + "/done", "/tasks", url.Values{"done": {"0"}}, "Marked as open."},
		{"calendar add", "/calendars", "/calendars", url.Values{"name": {"Choir"}, "url": {"https://example.org/a.ics"}, "color": {"#2F5E7A"}, "interval": {"60"}}, "Calendar added."},
		{"calendar sync", "/calendars/" + calID + "/sync", "/calendars/" + calID, url.Values{}, "Updated at 1:00:00\u00a0PM. Events: 0 (new 0, changed 0, removed 0)."},
		{"display pair", "/displays", "/displays", pairForm(code2), "Display paired."},
		{"display settings", "/displays/" + displayID + "/settings", "/displays/" + displayID, url.Values{"show_tomorrow": {"on"}}, "Saved."},
		{"share", "/caldav/calendars/" + calID + "/share", "/caldav", url.Values{"shared": {"on"}}, "Sharing through the box is on."},
		{"token revoke", "/caldav/tokens/" + tokenID + "/revoke", "/caldav", url.Values{}, "Access for “Phone” revoked."},
		{"connection calendars", "/connections/" + connID + "/calendars", "/calendars", url.Values{"calendar": {flowCalendar}, "name_" + flowCalendar: {"Home"}, "interval": {"60"}}, "Calendar added."},
		{"task delete", "/tasks/" + taskID + "/delete", "/tasks", url.Values{}, "Task deleted."},
		{"calendar remove", "/calendars/" + calID + "/delete", "/calendars", url.Values{}, "“Holidays” removed."},
		{"connection remove", "/connections/" + connID + "/delete", "/calendars", url.Values{}, "“u” disconnected."},
		{"display unpair", "/displays/" + displayID + "/unpair", "/displays", url.Values{}, "“Hallway” unpaired."},
	}
	for _, s := range steps {
		r := b.post(s.path, s.form)
		if r.status != http.StatusSeeOther || r.location != s.page {
			t.Fatalf("%s: POST = %d %q", s.name, r.status, r.location)
		}
		if got := linkTags.ReplaceAllString(successLine(getEN(b, s.page).body), ""); got != s.want { // a link inside the line is part of its words
			t.Errorf("%s: success line = %q, want %q", s.name, got, s.want)
		}
		if got := successLine(getEN(b, s.page).body); got != "" {
			t.Errorf("%s: success line shown again: %q", s.name, got)
		}
	}
}

func TestSuccessLineIsGermanWithGermanLocale(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	b.post("/settings/household", url.Values{"time_format": {"12h"}, "temperature_unit": {"c"}})
	if got := successLine(b.do(http.MethodGet, "/settings/household", nil, map[string]string{"Accept-Language": "de"}).body); got != "Haushaltseinstellungen gespeichert." {
		t.Fatalf("success line = %q", got)
	}
}

// The line is not stored in the address, so it cannot be forged by a link.
func TestSuccessLineCannotBeForgedByALink(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	for _, path := range []string{"/settings/household?saved=1", "/settings/household?flash=flash.saved", "/settings/household?success=Saved."} {
		if got := successLine(getEN(b, path).body); got != "" {
			t.Errorf("%s shows %q", path, got)
		}
	}
}

func TestFinishingSetupShowsASuccessLine(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	b.post("/setup", validSetupForm())
	secret := secretOn(t, b.get("/setup/totp").body)
	code, _ := auth.TOTPCode(secret, b.now)
	b.post("/setup/totp", url.Values{"code": {code}})
	if got := successLine(getEN(b, "/").body); got == "" {
		t.Fatal("no success line after setup")
	}
}

// Creating a device token shows its value once, together with a success line.
func TestCreatingADeviceTokenShowsASuccessLine(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	r := b.do(http.MethodPost, "/caldav/tokens", url.Values{"name": {"Phone"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusOK {
		t.Fatalf("POST = %d", r.status)
	}
	if got := successLine(r.body); got != "Device token for “Phone” created." {
		t.Errorf("success line = %q", got)
	}
	if !strings.Contains(r.body, "data-device-token=") {
		t.Error("the new token is not shown")
	}
	if got := successLine(getEN(b, "/caldav").body); got != "" {
		t.Errorf("success line shown again: %q", got)
	}
	// A refused name is an error, not a success.
	r = b.do(http.MethodPost, "/caldav/tokens", url.Values{"name": {" "}}, map[string]string{"Accept-Language": "en"})
	if got := successLine(r.body); got != "" {
		t.Errorf("success line on an error: %q", got)
	}
}
