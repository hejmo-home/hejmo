package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/sun"
)

var htmlThemeRe = regexp.MustCompile(`<html[^>]*>`)

// themeOf is the data-theme of a page's <html> element, "" without one.
func themeOf(t *testing.T, page string) string {
	t.Helper()
	tag := htmlThemeRe.FindString(page)
	if tag == "" {
		t.Fatalf("no <html> in page:\n%s", page)
	}
	if m := regexp.MustCompile(`data-theme="([^"]*)"`).FindStringSubmatch(tag); m != nil {
		return m[1]
	}
	return ""
}

// atMunich puts the household's home location in Munich.
func (f *familyBox) atMunich(t *testing.T) {
	t.Helper()
	if _, err := f.db.SQL().Exec(`UPDATE households SET latitude = 48.1371, longitude = 11.5754`); err != nil {
		t.Fatal(err)
	}
}

// setTheme saves a display's theme in the web interface.
func (f *familyBox) setTheme(t *testing.T, theme string, sunrise, sunset string) {
	t.Helper()
	ds, _ := f.db.Displays(f.householdID)
	form := url.Values{"theme": {theme}, "sunrise_offset": {sunrise}, "sunset_offset": {sunset}}
	if r := f.post("/displays/"+ds[0].ID+"/settings", form); r.status != http.StatusSeeOther {
		t.Fatalf("save theme = %d: %s", r.status, r.body)
	}
}

func midsummer() sun.Times {
	return sun.On(time.Date(2026, 6, 21, 12, 0, 0, 0, berlinTZ), 48.1371, 11.5754)
}

// 5.15: automatic with offset -30 at sunset; 30 minutes before sunset the display is dark.
func TestAutomaticDisplayTurnsDarkThirtyMinutesBeforeSunsetWithOffsetMinus30(t *testing.T) {
	f := newFamilyBox(t, "screen")
	f.atMunich(t)
	f.setTheme(t, "auto", "0", "-30")
	s := midsummer()
	if got := themeOf(t, f.view(t, "en", s.Set.Add(-30*time.Minute))); got != "dark" {
		t.Errorf("at sunset-30 = %q, want dark", got)
	}
	if got := themeOf(t, f.view(t, "en", s.Set.Add(-31*time.Minute))); got != "light" {
		t.Errorf("at sunset-31 = %q, want light", got)
	}
}

// 5.15: offset +45 at sunrise: light 45 minutes after sunrise.
func TestAutomaticDisplayTurnsLight45MinutesAfterSunriseWithOffsetPlus45(t *testing.T) {
	f := newFamilyBox(t, "screen")
	f.atMunich(t)
	f.setTheme(t, "auto", "45", "0")
	s := midsummer()
	if got := themeOf(t, f.view(t, "en", s.Rise.Add(45*time.Minute))); got != "light" {
		t.Errorf("at sunrise+45 = %q, want light", got)
	}
	if got := themeOf(t, f.view(t, "en", s.Rise.Add(44*time.Minute))); got != "dark" {
		t.Errorf("at sunrise+44 = %q, want dark", got)
	}
}

// ADR 0015 defaults: E-Ink light, normal screen automatic.
func TestNewDisplaysStartWithTheDefaultThemeOfTheirType(t *testing.T) {
	night := time.Date(2026, 6, 21, 2, 0, 0, 0, berlinTZ)
	for typ, want := range map[string]string{"eink_bw": "bw", "eink_color": "light", "screen": "dark"} {
		f := newFamilyBox(t, typ)
		f.atMunich(t)
		if got := themeOf(t, f.view(t, "en", night)); got != want {
			t.Errorf("%s at 02:00 = %q, want %q", typ, got, want)
		}
	}
}

func TestDarkThemeOnABlackAndWhiteEInkDisplayUsesTheDarkPalette(t *testing.T) {
	f := newFamilyBox(t, "eink_bw")
	if got := themeOf(t, f.view(t, "en", f.at(14, 12, 0))); got != "bw" {
		t.Fatalf("light = %q, want bw", got)
	}
	f.setTheme(t, "dark", "0", "0")
	if got := themeOf(t, f.view(t, "en", f.at(14, 12, 0))); got != "dark" {
		t.Fatalf("dark = %q, want dark", got)
	}
}

func TestDisplaysPageOffersThemeAndOffsets(t *testing.T) {
	f := newFamilyBox(t, "screen")
	p := f.get("/displays/" + f.displayID(t)).body
	for _, want := range []string{`name="theme"`, `value="auto" checked`, `name="sunrise_offset"`, `name="sunset_offset"`, `min="-180"`, `max="180"`} {
		if !strings.Contains(p, want) {
			t.Errorf("displays page misses %s", want)
		}
	}
	f.setTheme(t, "dark", "-20", "35")
	p = f.get("/displays/" + f.displayID(t)).body
	if !strings.Contains(p, `value="dark" checked`) || !strings.Contains(p, `value="-20"`) || !strings.Contains(p, `value="35"`) {
		t.Errorf("saved theme not shown:\n%s", p)
	}
}

func TestRefusedThemeSettingsLeaveTheDisplayAsItWas(t *testing.T) {
	f := newFamilyBox(t, "screen")
	ds, _ := f.db.Displays(f.householdID)
	for _, form := range []url.Values{
		{"theme": {"sepia"}, "sunrise_offset": {"0"}, "sunset_offset": {"0"}},
		{"theme": {"auto"}, "sunrise_offset": {"181"}, "sunset_offset": {"0"}},
		{"theme": {"auto"}, "sunrise_offset": {"0"}, "sunset_offset": {"x"}},
	} {
		if r := f.post("/displays/"+ds[0].ID+"/settings", form); r.status != http.StatusUnprocessableEntity {
			t.Errorf("%v = %d, want 422", form, r.status)
		}
	}
	if d, _ := f.db.Displays(f.householdID); d[0].Theme != "auto" || d[0].SunsetOffset != 0 {
		t.Errorf("display changed: %+v", d[0])
	}
}

// A display that stays open switches theme without a reload: its state call carries the theme.
func TestDisplayStateCarriesTheCurrentTheme(t *testing.T) {
	f := newFamilyBox(t, "screen")
	f.atMunich(t)
	f.now = midsummer().Set.Add(time.Hour)
	if body := f.display.get("/display/state", "").body; !strings.Contains(body, `"theme":"dark"`) {
		t.Fatalf("state = %s", body)
	}
	f.now = midsummer().Rise.Add(5 * time.Hour)
	if body := f.display.get("/display/state", "").body; !strings.Contains(body, `"theme":"light"`) {
		t.Fatalf("state = %s", body)
	}
}

// 5.15: the web interface follows the device by default, so a device that prefers dark gets dark.
func TestWebInterfaceFollowsTheDeviceByDefault(t *testing.T) {
	b := signedInBox(t)
	p := b.get("/settings").body
	if got := themeOf(t, p); got != "" {
		t.Errorf("web page has data-theme=%q, want none (prefers-color-scheme decides)", got)
	}
	if !strings.Contains(p, `src="/static/theme.js"`) {
		t.Error("web page does not load the theme script")
	}
}

func TestWebInterfaceOffersLightDarkAutomaticAndFollowTheDevice(t *testing.T) {
	b := signedInBox(t)
	p := b.get("/").body
	for _, v := range []string{"device", "light", "dark", "auto"} {
		if !strings.Contains(p, `data-theme-choice value="`+v+`"`) && !strings.Contains(p, `value="`+v+`" data-theme-choice`) {
			t.Errorf("page misses the choice %q", v)
		}
	}
}

func TestWebPagesCarryTodaysSunTimesForAutomatic(t *testing.T) {
	f := newFamilyBox(t, "screen")
	f.atMunich(t)
	f.now = time.Date(2026, 6, 21, 12, 0, 0, 0, berlinTZ)
	tag := htmlThemeRe.FindString(f.get("/settings").body)
	rise, set := midsummer().Rise.Unix(), midsummer().Set.Unix()
	if !strings.Contains(tag, `data-sun-rise="`+itoa64(rise)+`"`) || !strings.Contains(tag, `data-sun-set="`+itoa64(set)+`"`) {
		t.Fatalf("html tag = %s, want sun times %d / %d", tag, rise, set)
	}
}

func TestSignedOutPagesCarryNoSunTimes(t *testing.T) {
	f := newFamilyBox(t, "screen")
	f.atMunich(t)
	f.now = time.Date(2026, 6, 21, 12, 0, 0, 0, berlinTZ)
	f.logout()
	if body := f.get("/login").body; strings.Contains(body, "data-sun-") {
		t.Fatalf("signed-out /login leaks sun times (they reveal the home location): %s", htmlThemeRe.FindString(body))
	}
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

// ADR 0015: quiet mode takes precedence over the theme. A normal screen shows
// a black page, so it is dark whatever its theme says; the night page of an
// E-Ink display keeps the display's own palette, even when its theme is dark.
// Outside the quiet window the theme applies again.
func TestQuietModeTakesPrecedenceOverTheTheme(t *testing.T) {
	for _, c := range []struct {
		typ, theme, quiet, outside string
	}{
		{"screen", "light", "dark", "light"},
		{"eink_bw", "dark", "bw", "dark"},
		{"eink_color", "dark", "light", "dark"},
		{"screen", "auto", "dark", "light"},
		{"eink_color", "auto", "light", "light"},
		{"eink_bw", "auto", "bw", "bw"},
	} {
		f := newFamilyBox(t, c.typ)
		f.atMunich(t)
		f.setTheme(t, c.theme, "0", "0")
		f.quietOn(t)
		night := f.view(t, "en", f.at(14, 2, 0))
		if !strings.Contains(night, "data-quiet") {
			t.Fatalf("%s: 02:00 is not quiet:\n%s", c.typ, night)
		}
		if got := themeOf(t, night); got != c.quiet {
			t.Errorf("%s with theme %s in quiet mode = %q, want %q", c.typ, c.theme, got, c.quiet)
		}
		f.now = f.at(14, 2, 0)
		if body := f.display.get("/display/state", "").body; !strings.Contains(body, `"theme":"`+c.quiet+`"`) {
			t.Errorf("%s: state in quiet mode = %s, want theme %s", c.typ, body, c.quiet)
		}
		day := f.view(t, "en", f.at(14, 12, 0))
		if got := themeOf(t, day); got != c.outside {
			t.Errorf("%s with theme %s at noon = %q, want %q", c.typ, c.theme, got, c.outside)
		}
	}
}
