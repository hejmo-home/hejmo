package server_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/web"
)

// extLink is a link to an outside page as the guides draw it: underlined,
// opens elsewhere, with the "opens elsewhere" icon (design pass 2 of ticket 133).
func extLink(href, text string) string {
	return `<a class="ext" href="` + href + `" target="_blank" rel="noopener noreferrer">` + text +
		`<svg class="icon" width="16" height="16" viewBox="0 0 24 24" aria-hidden="true"><path d="M14 5h5v5M19 5l-8 8M17 14v5H5V7h5"/></svg></a>`
}

// Every address in a guide is a link; the welcome page names the apps and deSEC
// as links and ends step 4 with the guided start page.
func TestWelcomePageLinksTheAppsAndDeSEC(t *testing.T) {
	b := newFreshBox(t)
	body := deGet(b, "/setup").body
	for _, want := range []string{
		extLink("https://2fas.com/", "2FAS"), extLink("https://getaegis.app/", "Aegis") + " (nur Android)",
		extLink("https://support.google.com/accounts/answer/1066447", "Google Authenticator"), extLink("https://desec.io/signup", "deSEC"),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("welcome page misses %q", want)
		}
	}
	if !strings.Contains(inText(body), "Danach führt dich die Startseite: Familie eintragen, Kalender verbinden, das erste Display aufhängen. Jeder Schritt lässt sich überspringen.") {
		t.Error("welcome step 4 does not describe the guided start page")
	}
	en := b.do(http.MethodGet, "/setup", nil, map[string]string{"Accept-Language": "en"}).body
	for _, want := range []string{extLink("https://2fas.com/", "2FAS"), extLink("https://desec.io/signup", "deSEC")} {
		if !strings.Contains(en, want) {
			t.Errorf("English welcome page misses %q", want)
		}
	}
}

// The deSEC form links each step to the page where it happens.
func TestDeSECFormLinksSignupDomainsAndTokens(t *testing.T) {
	b := newFreshBox(t)
	body := deGet(b, "/setup/https?way=public").body
	signup := `<a class="link" href="https://desec.io/signup" target="_blank" rel="noopener noreferrer">Konto bei deSEC anlegen <svg`
	for _, want := range []string{signup, extLink("https://desec.io/domains", "Domains"), extLink("https://desec.io/tokens", "Token Management")} {
		if !strings.Contains(body, want) {
			t.Errorf("deSEC form misses %q", want)
		}
	}
	if strings.Contains(body, `href="https://desec.io/"`) || strings.Contains(body, `<span class="path">Token Management</span>`) {
		t.Error("the form still links the deSEC start page or shows Token Management as plain text")
	}
	if !strings.Contains(inText(body), "Lege bei deSEC unter Domains einen Namen an, der auf .dedyn.io endet") {
		t.Errorf("step 2 text is wrong:\n%s", inText(body))
	}
	en := b.do(http.MethodGet, "/setup/https?way=public", nil, map[string]string{"Accept-Language": "en"}).body
	if !strings.Contains(en, extLink("https://desec.io/tokens", "Token Management")) || !strings.Contains(en, "https://desec.io/signup") {
		t.Error("English deSEC form lacks the links")
	}
}

// One-time code step 1: store links depend on the device. A computer shows both
// stores, an Android phone only Google Play, an iPhone only the App Store.
func TestTOTPStoreLinksFollowTheDevice(t *testing.T) {
	b := newBox(t)
	b.post("/setup", validSetupForm())
	appStore2fas := extLink("https://apps.apple.com/app/2fas-auth/id1217793794", "für iPhone im App Store")
	play2fas := extLink("https://play.google.com/store/apps/details?id=com.twofasapp", "für Android bei Google Play")
	aegisPlay := "https://play.google.com/store/apps/details?id=com.beemdevelopment.aegis"
	googleApple := "https://apps.apple.com/app/google-authenticator/id388497605"

	desk := deGet(b, "/setup/totp").body
	for _, want := range []string{"2FAS ist kostenlos: " + appStore2fas + " oder " + play2fas + ".", extLink("https://getaegis.app/", "Aegis"), extLink("https://support.google.com/accounts/answer/1066447", "Google Authenticator") + " gehen."} {
		if !strings.Contains(desk, want) {
			t.Errorf("computer page misses %q", want)
		}
	}
	if strings.Contains(desk, "App Store oder Play Store") {
		t.Error("the plain text about the stores is still there")
	}

	as := func(ua, lang string) string {
		return b.do(http.MethodGet, "/setup/totp", nil, map[string]string{"Accept-Language": lang, "User-Agent": ua}).body
	}
	android := as(androidUA, "de")
	if !strings.Contains(android, "https://play.google.com/store/apps/details?id=com.twofasapp") || !strings.Contains(android, aegisPlay) {
		t.Errorf("Android lacks the Play links:\n%s", android)
	}
	if strings.Contains(android, "apps.apple.com") {
		t.Error("an Android phone is sent to the App Store")
	}
	ios := as(iPhone, "de")
	if !strings.Contains(ios, "https://apps.apple.com/app/2fas-auth/id1217793794") || !strings.Contains(ios, googleApple) {
		t.Errorf("iPhone lacks the App Store links:\n%s", ios)
	}
	if strings.Contains(ios, "play.google.com") || strings.Contains(ios, "getaegis.app") {
		t.Error("an iPhone is sent to Google Play or to Aegis, which has no iOS app")
	}
	if en := as(androidUA, "en"); !strings.Contains(en, "https://play.google.com/store/apps/details?id=com.twofasapp") {
		t.Error("English Android page lacks the Play link")
	}
}

// The name the parent types into the router is the one address that is not a
// link: it gets a "Copy" button instead.
func TestRouterNameHasACopyButton(t *testing.T) {
	b := newFreshBox(t)
	b.post("/setup/https/public", publicForm())
	body := deGet(b, "/setup/https/check").body
	want := `<div class="copyrow"><p class="addr">mia.dedyn.io</p><button class="button secondary" type="button" data-copy="mia.dedyn.io" data-copied-text="Kopiert">Kopieren</button></div>`
	if !strings.Contains(body, want) {
		t.Errorf("no copy row:\n%s", body)
	}
	raw, err := web.Static.ReadFile("static/form.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)
	if !strings.Contains(js, "data-copy") || !strings.Contains(js, "clipboard") {
		t.Error("form.js does not copy")
	}
	if !regexp.MustCompile(`data-copied-text`).MatchString(js) {
		t.Error("form.js does not say \"Kopiert\" from the attribute")
	}
}
