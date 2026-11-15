package server_test

import (
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/web"
)

func mustStatic(t *testing.T, name string) []byte {
	t.Helper()
	b, err := fs.ReadFile(web.Static, name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var mainHrefRe = regexp.MustCompile(`<main id="content">(?s:(.*))`)

func hubTargets(body string) []string {
	m := mainHrefRe.FindStringSubmatch(body)
	if m == nil {
		return nil
	}
	var out []string
	for _, h := range hrefRe.FindAllStringSubmatch(m[1], -1) {
		out = append(out, h[1])
	}
	return out
}

// 5.18: the navigation holds everyday screens only; setup screens sit under Settings (ADR 0017).
func TestNavigationHoldsEverydayScreensAndSettingsOnly(t *testing.T) {
	b := signedInBox(t)
	want := []string{"/", "/tasks", "/meals", "/settings"}
	for _, page := range []string{"/", "/tasks", "/settings", "/calendars", "/displays"} {
		got := navTargets(t, b.get(page).body)
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("nav on %s = %v, want %v", page, got, want)
		}
	}
}

// hubPages are the setup screens the Settings hub holds. Add a new hub entry here; the tests
// below also check that the hub shows nothing this list lacks.
var hubPages = []string{"/calendars", "/caldav", "/displays", "/phones", "/settings/household", "/otp"}

// 5.18: every setup page is reachable from the Settings hub.
func TestSettingsHubLinksEverySetupPage(t *testing.T) {
	b := signedInBox(t)
	r := getEN(b, "/settings")
	if r.status != http.StatusOK {
		t.Fatalf("GET /settings = %d", r.status)
	}
	got := hubTargets(r.body)
	for _, want := range hubPages {
		found := false
		for _, g := range got {
			found = found || g == want
		}
		if !found {
			t.Errorf("hub lacks a link to %s: %v", want, got)
		}
	}
	for _, want := range []string{"Calendar connections", "Calendar apps", "Displays", "Phones", "Household", "One-time codes"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("hub lacks %q", want)
		}
	}
	if strings.Join(got, " ") != strings.Join(hubPages, " ") {
		t.Errorf("hub links %v, want %v", got, hubPages)
	}
	for _, link := range got {
		if r := b.get(link); r.status != http.StatusOK {
			t.Errorf("hub link %s = %d", link, r.status)
		}
	}
}

// 5.18: every setup page links back to the hub, and Settings stays marked in the navigation.
func TestSetupPagesLinkBackToSettings(t *testing.T) {
	b := signedInBox(t)
	b.post("/calendars", url.Values{"name": {"Feiertage"}, "url": {"https://example.org/a.ics"}, "color": {"#B4583A"}, "interval": {"60"}})
	for _, page := range hubPages {
		body := getEN(b, page).body
		if !strings.Contains(body, `<a class="back" href="/settings">`) {
			t.Errorf("%s has no link back to Settings", page)
		}
		if !strings.Contains(body, `<a href="/settings" aria-current="page"`) {
			t.Errorf("%s does not mark Settings as the current section", page)
		}
	}
	if strings.Contains(getEN(b, "/settings").body, `class="back"`) {
		t.Error("the hub itself has a back link")
	}
	if strings.Contains(getEN(b, "/tasks").body, `class="back"`) {
		t.Error("an everyday screen has a back link to Settings")
	}
}

// 5.18: the household page holds the time format and temperature unit.
func TestHouseholdPageSavesFormats(t *testing.T) {
	b := signedInBox(t)
	r := b.get("/settings/household")
	if r.status != http.StatusOK || !strings.Contains(r.body, `name="time_format"`) {
		t.Fatalf("household page = %d\n%s", r.status, r.body)
	}
	if strings.Contains(b.get("/settings").body, `name="time_format"`) {
		t.Error("the hub holds a form")
	}
	if r := b.post("/settings/household", url.Values{"time_format": {"12h"}, "temperature_unit": {"c"}}); r.status != http.StatusSeeOther || r.location != "/settings/household" {
		t.Fatalf("save = %d %q", r.status, r.location)
	}
}

// 5.18: a child's device session sees neither Settings nor its pages.
func TestChildSeesNoSettingsPages(t *testing.T) {
	b := signedInBox(t)
	child := childSession(t, b)
	body := child.get("/").body
	if strings.Contains(body, `href="/settings"`) {
		t.Error("child sees the Settings link")
	}
	for _, page := range append([]string{"/settings"}, hubPages...) {
		if r := child.get(page); r.status != http.StatusForbidden {
			t.Errorf("child GET %s = %d, want 403", page, r.status)
		}
	}
	if r := child.post("/settings/household", url.Values{"time_format": {"12h"}, "temperature_unit": {"f"}}); r.status != http.StatusForbidden {
		t.Errorf("child POST household = %d, want 403", r.status)
	}
}

// 5.18: at 375 px the navigation is one row with targets of at least 44 px.
func TestNavigationFitsOneRowWithTouchTargets(t *testing.T) {
	css := string(mustStatic(t, "static/hejmo.css"))
	m := regexp.MustCompile(`\.frame-nav ul \{([^}]*)\}`).FindStringSubmatch(css)
	if m == nil || !strings.Contains(m[1], "flex-wrap: nowrap") || !strings.Contains(m[1], "overflow-x: auto") {
		t.Errorf("the navigation is not a single scrollable row: %v", m)
	}
	a := regexp.MustCompile(`\.frame-nav a \{([^}]*)\}`).FindStringSubmatch(css)
	if a == nil || !strings.Contains(a[1], "min-height: var(--touch-target)") {
		t.Error("navigation links lack the 44 px touch target")
	}
	if !strings.Contains(css, "--touch-target: 44px") {
		t.Error("touch target is not 44 px")
	}
}

// 5.18 / ADR 0017: a display is described by one question, where it hangs; there is no room field.
func TestDisplayFormAsksWhereItHangs(t *testing.T) {
	b := signedInBox(t)
	body := getEN(b, "/displays").body
	if !strings.Contains(body, "Where does it hang?") {
		t.Errorf("displays page lacks the question:\n%s", body)
	}
	if strings.Contains(body, `name="room"`) {
		t.Error("displays page still has a room field")
	}
	de := b.do(http.MethodGet, "/displays", nil, map[string]string{"Accept-Language": "de"}).body
	if !strings.Contains(de, "Wo hängt es?") {
		t.Errorf("German displays page lacks the question")
	}
}
