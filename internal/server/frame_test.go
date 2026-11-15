package server_test

import (
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/web"
)

var navHrefRe = regexp.MustCompile(`<nav class="frame-nav"[^>]*>(?s:(.*?))</nav>`)
var hrefRe = regexp.MustCompile(`href="([^"]+)"`)

func navTargets(t *testing.T, page string) []string {
	t.Helper()
	m := navHrefRe.FindStringSubmatch(page)
	if m == nil {
		return nil
	}
	var out []string
	for _, h := range hrefRe.FindAllStringSubmatch(m[1], -1) {
		out = append(out, h[1])
	}
	return out
}

// 5.11 / 5.18: the navigation holds the everyday screens and Settings, and marks the current one.
func TestParentNavigationMarksTheCurrentScreen(t *testing.T) {
	b := signedInBox(t)
	want := []string{"/", "/calendar", "/tasks", "/settings"}
	for _, page := range want {
		r := b.get(page)
		if r.status != http.StatusOK {
			t.Fatalf("GET %s = %d", page, r.status)
		}
		got := navTargets(t, r.body)
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("nav on %s = %v, want %v", page, got, want)
		}
		if !strings.Contains(r.body, `<a href="`+page+`" aria-current="page"`) {
			t.Errorf("nav on %s does not mark the current screen", page)
		}
	}
}

// 5.11: a child's device session sees only the screens its role allows.
func TestChildNavigationShowsOnlyAllowedScreens(t *testing.T) {
	b := signedInBox(t)
	child := childSession(t, b)
	r := child.get("/")
	if r.status != http.StatusOK {
		t.Fatalf("child GET / = %d", r.status)
	}
	if got := navTargets(t, r.body); strings.Join(got, " ") != "/ /tasks" {
		t.Fatalf("child nav = %v, want / and /tasks", got)
	}
	for _, page := range []string{"/calendars", "/caldav", "/displays", "/phones", "/settings"} {
		if r := child.get(page); r.status != http.StatusForbidden {
			t.Errorf("child GET %s = %d, want 403", page, r.status)
		}
	}
}

// 5.11: the sign-in and setup screens sit in the frame, without navigation.
func TestSignedOutScreensHaveFrameWithoutNavigation(t *testing.T) {
	b, _ := configuredBox(t)
	r := b.get("/login")
	if !strings.Contains(r.body, `class="frame-bar"`) || strings.Contains(r.body, `class="frame-nav"`) {
		t.Fatalf("login frame wrong:\n%s", r.body)
	}
	fresh := newBox(t)
	r = fresh.get("/setup")
	if !strings.Contains(r.body, `class="frame-bar"`) || strings.Contains(r.body, `class="frame-nav"`) {
		t.Fatalf("setup frame wrong:\n%s", r.body)
	}
}

// 5.11: every web screen uses the shared stylesheet and no inline colours.
func TestEveryScreenUsesTheSharedStylesheetWithoutInlineStyles(t *testing.T) {
	b := signedInBox(t)
	b.post("/calendars", map[string][]string{"name": {"Feiertage"}, "url": {"https://example.org/a.ics"}, "color": {"#B4583A"}, "interval": {"60"}})
	pages := []string{"/", "/calendars", "/caldav", "/displays", "/phones", "/tasks", "/settings", "/ca"}
	for _, page := range pages {
		body := b.get(page).body
		if !strings.Contains(body, `href="/static/hejmo.css"`) {
			t.Errorf("%s does not use the shared stylesheet", page)
		}
		if strings.Contains(body, "style=") || strings.Contains(body, "<style") {
			t.Errorf("%s has inline styles", page)
		}
	}
	b.logout()
	for _, page := range []string{"/login"} {
		if body := b.get(page).body; strings.Contains(body, "style=") || strings.Contains(body, "<style") {
			t.Errorf("%s has inline styles", page)
		}
	}
}

// 5.11: the frame is German or English, from the locales.
func TestNavigationIsTranslated(t *testing.T) {
	b := signedInBox(t)
	de := b.do(http.MethodGet, "/", nil, map[string]string{"Accept-Language": "de"}).body
	en := b.do(http.MethodGet, "/", nil, map[string]string{"Accept-Language": "en"}).body
	for _, want := range []string{"Einstellungen", "Hauptnavigation", "Abmelden"} {
		if !strings.Contains(de, want) {
			t.Errorf("German frame lacks %q", want)
		}
	}
	for _, want := range []string{"Settings", "Main navigation", "Sign out"} {
		if !strings.Contains(en, want) {
			t.Errorf("English frame lacks %q", want)
		}
	}
}

func TestSignOutIsInTheFrameOnEveryScreen(t *testing.T) {
	b := signedInBox(t)
	for _, page := range []string{"/", "/calendars", "/settings"} {
		if !strings.Contains(b.get(page).body, `action="/logout"`) {
			t.Errorf("%s has no sign-out in the frame", page)
		}
	}
}

// ADR 0015: displays choose their theme per display (pairing: light, E-Ink: bw), so they
// neither follow the device's colour scheme nor sit in the web frame.
func TestDisplaysDoNotFollowTheDeviceAndSitOutsideTheFrame(t *testing.T) {
	b := signedInBox(t)
	d := newScreen(b)
	pairing := d.get("/display", "en").body
	b.post("/displays", pairForm(codeOn(t, pairing)))
	paired := d.get("/display", "en").body
	for name, page := range map[string]string{"pairing": pairing, "family": paired} {
		// An explicit display theme, never the device's: pairing is always
		// light, the paired E-Ink display is bw.
		want := map[string]string{"pairing": "light", "family": "bw"}[name]
		if !strings.Contains(page, `<html lang="en" data-theme="`+want+`">`) {
			t.Errorf("%s display page lacks data-theme=%q:\n%s", name, want, page)
		}
		if strings.Contains(page, `class="frame`) || strings.Contains(page, `<body class="web"`) {
			t.Errorf("%s display page is wrapped in the web frame", name)
		}
	}
	if page := b.get("/").body; regexp.MustCompile(`<html lang="[a-z]+">`).FindString(page) == "" || !strings.Contains(page, `<body class="web">`) {
		t.Errorf("web page lacks the follow-the-device root or the web body class:\n%s", page)
	}
}

// 5.11: no template of any screen carries inline styles, rendered or not.
// The one exception is data handed to CSS as custom properties
// (style="--cols:{{...}}"); the stylesheet decides what they mean.
var customPropStyleRe = regexp.MustCompile(`style="--[a-z0-9-]+:\{\{[^}]*\}\}(;\s*--[a-z0-9-]+:\{\{[^}]*\}\})*;?"`)

func hasInlineStyle(src string) bool {
	src = customPropStyleRe.ReplaceAllString(src, "")
	return strings.Contains(src, "style=") || strings.Contains(src, "<style")
}

func TestInlineStyleCheckAllowsOnlyCustomProperties(t *testing.T) {
	for _, ok := range []string{`<span style="--pct:{{.Percent}}">`, `<main style="--cols:{{len .Columns}}">`, `<i style="--a:{{.A}}; --b:{{.B}};">`} {
		if hasInlineStyle(ok) {
			t.Errorf("%s should be allowed", ok)
		}
	}
	for _, bad := range []string{`<span style="width:{{.X}}%">`, `<p style="color:red">`, `<p style="--a:red">`, `<p style="--a:{{.A}};color:red">`, `<style>p{}</style>`} {
		if !hasInlineStyle(bad) {
			t.Errorf("%s should be rejected", bad)
		}
	}
}

func TestNoTemplateHasInlineStyles(t *testing.T) {
	files, err := fs.Glob(web.Templates, "templates/*.html")
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates: %v", err)
	}
	for _, f := range files {
		b, _ := fs.ReadFile(web.Templates, f)
		if hasInlineStyle(string(b)) {
			t.Errorf("%s has inline styles", f)
		}
	}
}
