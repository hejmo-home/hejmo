package server_test

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// themePick is the theme chooser of the brand bar (ticket 125): from the
// opening of its <details> to the closing of the menu.
func themePick(t *testing.T, page string) string {
	t.Helper()
	i := strings.Index(page, `<details class="theme-pick"`)
	if i < 0 {
		t.Fatalf("page has no theme chooser:\n%s", page)
	}
	j := strings.Index(page[i:], "</details>")
	if j < 0 {
		t.Fatalf("theme chooser is not closed:\n%s", page[i:])
	}
	return page[i : i+j]
}

func atHome(t *testing.T, b *box) {
	t.Helper()
	if _, err := b.db.SQL().Exec(`UPDATE households SET latitude = 48.1371, longitude = 11.5754`); err != nil {
		t.Fatal(err)
	}
}

// #125: a parent finds the chooser in the brand bar, between the language
// switcher and Sign out, with the four choices in the order light, dark,
// automatic, follow the device.
func TestParentFindsTheThemeChooserInTheBrandBar(t *testing.T) {
	b := signedInBox(t)
	page := getEN(b, "/").body
	bar := page[strings.Index(page, `<header class="frame-bar">`):strings.Index(page, "</header>")]
	lang, pick, out := strings.Index(bar, `<details class="lang"`), strings.Index(bar, `<details class="theme-pick"`), strings.Index(bar, `<form class="signout"`)
	if lang < 0 || pick < lang || out < pick {
		t.Fatalf("brand bar order is language %d, theme %d, sign out %d:\n%s", lang, pick, out, bar)
	}
	menu := themePick(t, page)
	want := []string{`value="light"`, `value="dark"`, `value="auto"`, `value="device"`}
	last := -1
	for _, w := range want {
		i := strings.Index(menu, w)
		if i <= last {
			t.Errorf("choice %s is missing or out of order in:\n%s", w, menu)
		}
		last = i
	}
	for _, w := range []string{`<fieldset class="theme-menu" data-theme-choices `, "<legend>Theme in this browser</legend>", `<span class="vh">Theme: </span>`,
		"Always light", "Always dark, easy on the eyes in the evening", "Light or dark, the way your phone or computer is set",
		`<span class="opt-state">Selected</span>`, "Applies to this browser only. Everyone else keeps their theme.", "Like your device"} {
		if !strings.Contains(menu, w) {
			t.Errorf("chooser lacks %q:\n%s", w, menu)
		}
	}
	if n := strings.Count(page, `name="browser_theme"`); n != 4 {
		t.Errorf("page has %d theme radios, want the 4 of the chooser", n)
	}
}

// #125: language and theme menus are one exclusive group: opening one closes the other.
func TestLanguageAndThemeMenusAreOneExclusiveGroup(t *testing.T) {
	page := getEN(signedInBox(t), "/").body
	if !strings.Contains(page, `<details class="lang" name="frame-tool"`) || !strings.Contains(page, `<details class="theme-pick" name="frame-tool"`) {
		t.Errorf("language and theme menus do not share details name=frame-tool:\n%s", page)
	}
}

// #125: the chooser reaches children: a child's phone has no Settings, but the brand bar.
func TestChildsPhoneHasTheThemeChooserAndSaysPhone(t *testing.T) {
	cp := newChildPhone(t)
	for _, path := range []string{"/", "/tasks"} {
		menu := themePick(t, cp.screen.get(path, "de").body)
		for _, w := range []string{"<legend>Design auf diesem Handy</legend>", "Wie dein Handy", "Gilt nur auf diesem Handy. Alle anderen behalten ihr Design.", `<span class="vh">Design: </span>`} {
			if !strings.Contains(menu, w) {
				t.Errorf("%s: child's chooser lacks %q:\n%s", path, w, menu)
			}
		}
	}
	if strings.Contains(cp.screen.get("/", "en").body, "this browser") {
		t.Error("a phone's chooser speaks of a browser")
	}
}

// #125: a browser session (not a paired phone) says browser and device.
func TestBrowserChooserSaysBrowserAndDevice(t *testing.T) {
	menu := themePick(t, signedInBox(t).get("/").body)
	for _, w := range []string{"<legend>Design in diesem Browser</legend>", "Wie dein Gerät", "Gilt nur in diesem Browser. Alle anderen behalten ihr Design."} {
		if !strings.Contains(menu, w) {
			t.Errorf("chooser lacks %q:\n%s", w, menu)
		}
	}
}

// #125: the chooser is one place for everyone, so the Settings hub is a plain list again.
func TestSettingsHubHasNoThemeBlock(t *testing.T) {
	page := signedInBox(t).get("/settings").body
	main := page[strings.Index(page, `<main`):]
	if strings.Contains(main, "browser_theme") || strings.Contains(main, "data-theme-choices") {
		t.Errorf("the hub still has a theme block:\n%s", main)
	}
}

// #125: the chooser is for signed-in pages; sign-in shows none.
func TestSignedOutPagesHaveNoThemeChooser(t *testing.T) {
	b := signedInBox(t)
	b.logout()
	if body := b.get("/login").body; strings.Contains(body, "theme-pick") {
		t.Errorf("signed-out sign-in page has a theme chooser:\n%s", body)
	}
}

// #125: without a home location there are no sun times: "automatic" cannot be
// chosen and says why; a child reads that an adult must enter the place.
func TestWithoutHomeLocationAutomaticIsDisabledAndSaysWhy(t *testing.T) {
	cp := newChildPhone(t)
	noHome(t, cp.parent.b)
	menu := themePick(t, cp.screen.get("/", "de").body)
	if !regexp.MustCompile(`value="auto" data-theme-choice disabled`).MatchString(menu) || !strings.Contains(menu, "Geht erst, wenn ein Erwachsener den Ort eures Zuhauses eingetragen hat.") {
		t.Errorf("child: automatic is not disabled with its reason:\n%s", menu)
	}
	if strings.Contains(menu, "<a ") {
		t.Errorf("a child gets a link:\n%s", menu)
	}
	b := signedInBox(t)
	noHome(t, b)
	menu = themePick(t, b.get("/").body)
	if !regexp.MustCompile(`value="auto" data-theme-choice disabled`).MatchString(menu) || !strings.Contains(menu, `Geht erst mit dem Ort eures Zuhauses. <a href="/settings/household">Ort eintragen</a>`) {
		t.Errorf("parent: automatic is not disabled with a link to Household:\n%s", menu)
	}
}

// #125: at a place with polar day or night there are no sun times either, but
// the place is set: the reason must not say the place is missing.
func TestPolarDayDisablesAutomaticWithoutSayingThePlaceIsMissing(t *testing.T) {
	b := signedInBox(t)
	if _, err := b.db.SQL().Exec(`UPDATE households SET latitude = 78.2, longitude = 15.6`); err != nil {
		t.Fatal(err)
	}
	b.now = time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	menu := themePick(t, b.get("/").body)
	if !regexp.MustCompile(`value="auto" data-theme-choice disabled`).MatchString(menu) {
		t.Errorf("automatic is not disabled in polar day:\n%s", menu)
	}
	if strings.Contains(menu, "Ort eintragen") || strings.Contains(menu, "Geht erst mit dem Ort") {
		t.Errorf("polar day is explained as a missing place:\n%s", menu)
	}
	if !strings.Contains(menu, "Polartag") {
		t.Errorf("polar day is not named:\n%s", menu)
	}
}

// #125: with a home location automatic works.
func TestWithHomeLocationAutomaticCanBeChosen(t *testing.T) {
	b := signedInBox(t)
	atHome(t, b)
	menu := themePick(t, b.get("/").body)
	if !strings.Contains(menu, "Tagsüber hell, ab Sonnenuntergang dunkel") || strings.Contains(menu, "disabled") || strings.Contains(menu, "Ort eintragen") {
		t.Errorf("automatic is disabled although the home has a location:\n%s", menu)
	}
}

// #125: the success and error lines have their words from the locales, which the script reads.
func TestChooserCarriesTheLinesTheScriptShows(t *testing.T) {
	menu := themePick(t, getEN(signedInBox(t), "/").body)
	for _, w := range []string{`data-saved="Saved: {name}."`, `data-error="Could not be saved in this browser. The theme lasts until the page reloads."`} {
		if !strings.Contains(menu, w) {
			t.Errorf("chooser lacks %q:\n%s", w, menu)
		}
	}
}

// noHome clears the home location a fresh box now starts with (#86).
func noHome(t *testing.T, b *box) {
	t.Helper()
	if _, err := b.db.SQL().Exec(`UPDATE households SET latitude = NULL, longitude = NULL, home_place_id = NULL`); err != nil {
		t.Fatal(err)
	}
}
