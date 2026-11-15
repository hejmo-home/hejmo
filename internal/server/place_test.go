package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// Q59 (ticket 5.x home location): the parent enters a place name, the box
// geocodes it once, shows the resolved place for confirmation and stores
// latitude and longitude. Tests use a fake geocoding server, never the real one.

var en = map[string]string{"Accept-Language": "en"}

// unconfirmedSetup is the setup form as sent first: a place name, no confirmed place yet.
func unconfirmedSetup(name string) url.Values {
	f := validSetupForm()
	f.Set("home_name", name)
	f.Del("place")
	f.Del("place_for")
	return f
}

func radios(page, name string) []string {
	re := regexp.MustCompile(`<input [^>]*type="radio"[^>]*name="` + name + `"[^>]*>`)
	return re.FindAllString(page, -1)
}

func homeOf(t *testing.T, b *box) (lat, lon float64, name string) {
	t.Helper()
	h, err := b.db.HomeHousehold()
	if err != nil {
		t.Fatal(err)
	}
	return h.Latitude, h.Longitude, h.HomeName
}

func TestSetupShowsTheResolvedPlaceForConfirmationBeforeCreatingAnything(t *testing.T) {
	b := newBox(t)
	r := b.do(http.MethodPost, "/setup", unconfirmedSetup("Pasing"), en)
	if r.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the place is to be confirmed)\n%s", r.status, r.body)
	}
	if !strings.Contains(r.body, "Pasing, Bavaria, Germany") {
		t.Errorf("resolved place not shown:\n%s", r.body)
	}
	rs := radios(r.body, "place")
	if len(rs) != 1 || !strings.Contains(rs[0], `value="48.15,11.46"`) || !strings.Contains(rs[0], "checked") {
		t.Errorf("place choice = %q", rs)
	}
	if !strings.Contains(inputTag(r.body, "place_for"), `value="Pasing"`) {
		t.Errorf("searched name not carried: %q", inputTag(r.body, "place_for"))
	}
	if b.db.Configured() {
		t.Fatal("the household exists before the place was confirmed")
	}
	if got := b.geo.searches(); len(got) != 1 || got[0].Get("countryCode") != "DE" || got[0].Get("language") != "en" {
		t.Errorf("searches = %v, want one search in the region's country and the page's language", got)
	}
	// Confirming it creates the household with the coordinates.
	confirm := unconfirmedSetup("Pasing")
	confirm.Set("place", "48.15,11.46")
	confirm.Set("place_for", "Pasing")
	if r := b.post("/setup", confirm); r.status != http.StatusSeeOther || r.location != "/setup/totp" {
		t.Fatalf("confirm = %d %q\n%s", r.status, r.location, r.body)
	}
	if lat, lon, name := homeOf(t, b); lat != 48.15 || lon != 11.46 || name != "Pasing" {
		t.Errorf("home = %v %v %q", lat, lon, name)
	}
	if n := len(b.geo.searches()); n != 1 {
		t.Errorf("%d searches, want the place geocoded once", n)
	}
}

func TestSetupLetsTheParentChooseAmongSeveralPlaces(t *testing.T) {
	b := newBox(t)
	r := b.do(http.MethodPost, "/setup", unconfirmedSetup("Neustadt"), en)
	rs := radios(r.body, "place")
	if r.status != http.StatusOK || len(rs) != 2 {
		t.Fatalf("status %d, choices %q", r.status, rs)
	}
	for _, c := range rs {
		if strings.Contains(c, "checked") {
			t.Errorf("a place is preselected among several: %q", c)
		}
		if !strings.Contains(c, "required") {
			t.Errorf("choice not required: %q", c)
		}
	}
	for _, want := range []string{"Neustadt, Rhineland-Palatinate, Germany", "Neustadt, Coburg, Bavaria, Germany", `value="50.25,11.88"`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	// Submitting without a choice asks again and creates nothing.
	if r := b.post("/setup", unconfirmedSetup("Neustadt")); r.status != http.StatusOK || b.db.Configured() {
		t.Fatalf("without choice: status %d configured %v", r.status, b.db.Configured())
	}
	pick := unconfirmedSetup("Neustadt")
	pick.Set("place", "50.25,11.88")
	pick.Set("place_for", "Neustadt")
	if r := b.post("/setup", pick); r.status != http.StatusSeeOther {
		t.Fatalf("pick = %d", r.status)
	}
	if lat, lon, _ := homeOf(t, b); lat != 50.25 || lon != 11.88 {
		t.Errorf("home = %v %v, want the second place", lat, lon)
	}
}

func TestSetupSearchesAgainWhenTheNameChangedAfterConfirming(t *testing.T) {
	b := newBox(t)
	f := unconfirmedSetup("Neustadt")
	f.Set("place", "48.15,11.46") // confirmed for "Pasing", then the name was edited
	f.Set("place_for", "Pasing")
	if r := b.post("/setup", f); r.status != http.StatusOK || b.db.Configured() {
		t.Fatalf("status %d configured %v, want a new search", r.status, b.db.Configured())
	}
}

func TestSetupRefusesCoordinatesOutOfRange(t *testing.T) {
	b := newBox(t)
	for _, bad := range []string{"91,11", "48,181", "x,y", "48", "48,11,3", "NaN,1"} {
		f := validSetupForm()
		f.Set("place", bad)
		if r := b.post("/setup", f); r.status != http.StatusOK || b.db.Configured() {
			t.Errorf("place %q: status %d configured %v, want a new search", bad, r.status, b.db.Configured())
		}
	}
}

func TestSetupShowsAFieldErrorWhenNoPlaceMatchesAndKeepsWhatWasTyped(t *testing.T) {
	b := newBox(t)
	f := unconfirmedSetup("Nirgendwo")
	r := b.do(http.MethodPost, "/setup", f, en)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", r.status)
	}
	tag := inputTag(r.body, "home_name")
	if !strings.Contains(tag, `aria-invalid="true"`) || !strings.Contains(tag, `value="Nirgendwo"`) {
		t.Errorf("home_name = %q", tag)
	}
	if !strings.Contains(r.body, "No place found") {
		t.Errorf("no message:\n%s", r.body)
	}
	if !strings.Contains(inputTag(r.body, "name"), `value="Mama"`) || b.db.Configured() {
		t.Error("other entries lost or household created")
	}
}

func TestSetupShowsAFieldErrorWhenTheServiceIsUnreachable(t *testing.T) {
	b := newBox(t)
	b.geo.setDown(true)
	r := b.do(http.MethodPost, "/setup", unconfirmedSetup("Pasing"), en)
	tag := inputTag(r.body, "home_name")
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(tag, `aria-invalid="true"`) || !strings.Contains(tag, `value="Pasing"`) {
		t.Fatalf("status %d, home_name %q", r.status, tag)
	}
	if !strings.Contains(r.body, "could not be reached") || strings.Contains(r.body, "No place found") {
		t.Errorf("wrong message:\n%s", r.body)
	}
	if b.db.Configured() {
		t.Error("household created without a place")
	}
	de := b.do(http.MethodPost, "/setup", unconfirmedSetup("Pasing"), map[string]string{"Accept-Language": "de"})
	if !strings.Contains(de.body, "nicht erreichbar") {
		t.Errorf("German message missing:\n%s", de.body)
	}
}

// --- Settings → Household ---

func settingsForm(home string) url.Values {
	return url.Values{"time_format": {"12h"}, "temperature_unit": {"f"}, "home_name": {home}}
}

func TestHouseholdSettingsShowTheHomeAndItsLocation(t *testing.T) {
	b := signedInBox(t)
	r := b.do(http.MethodGet, "/settings/household", nil, en)
	if tag := inputTag(r.body, "home_name"); !strings.Contains(tag, `value="Pasing"`) {
		t.Errorf("home_name = %q", tag)
	}
	if !strings.Contains(r.body, "Location in use: 48.15° N, 11.46° E") {
		t.Errorf("location not shown:\n%s", r.body)
	}
}

func TestChangingTheHomeAsksToConfirmAndSavesNothingYet(t *testing.T) {
	b := signedInBox(t)
	r := b.do(http.MethodPost, "/settings/household", settingsForm("Neustadt"), en)
	if r.status != http.StatusOK || len(radios(r.body, "place")) != 2 {
		t.Fatalf("status %d, choices %q", r.status, radios(r.body, "place"))
	}
	if lat, lon, name := homeOf(t, b); lat != 48.15 || lon != 11.46 || name != "Pasing" {
		t.Errorf("home changed before confirming: %v %v %q", lat, lon, name)
	}
	h, _ := b.db.HomeHousehold()
	if h.TimeFormat != "24h" {
		t.Errorf("time format saved before confirming: %q", h.TimeFormat)
	}
	// What was typed is kept.
	if !strings.Contains(inputTag(r.body, "home_name"), `value="Neustadt"`) || !strings.Contains(r.body, `value="12h" checked`) || !strings.Contains(r.body, `value="f" checked`) {
		t.Errorf("typed values not kept:\n%s", r.body)
	}
	// Choosing the second place saves the name, the coordinates and the other settings together.
	f := settingsForm("Neustadt")
	f.Set("place", "50.25,11.88")
	f.Set("place_for", "Neustadt")
	if r := b.post("/settings/household", f); r.status != http.StatusSeeOther || r.location != "/settings/household" {
		t.Fatalf("confirm = %d %q", r.status, r.location)
	}
	h, _ = b.db.HomeHousehold()
	if h.Latitude != 50.25 || h.Longitude != 11.88 || h.HomeName != "Neustadt" || h.TimeFormat != "12h" {
		t.Errorf("household = %+v", h)
	}
	if !strings.Contains(b.do(http.MethodGet, "/settings/household", nil, en).body, "50.25° N, 11.88° E") {
		t.Error("new location not shown")
	}
}

func TestSettingsWithoutChangingTheHomeDoNotSearchAgain(t *testing.T) {
	b := signedInBox(t)
	before := len(b.geo.searches())
	if r := b.post("/settings/household", settingsForm("Pasing")); r.status != http.StatusSeeOther {
		t.Fatalf("status = %d", r.status)
	}
	// A form without the field (older clients) leaves the home alone too.
	if r := b.post("/settings/household", url.Values{"time_format": {"24h"}, "temperature_unit": {"c"}}); r.status != http.StatusSeeOther {
		t.Fatalf("status = %d", r.status)
	}
	if n := len(b.geo.searches()) - before; n != 0 {
		t.Errorf("%d searches, want none", n)
	}
	if lat, lon, _ := homeOf(t, b); lat != 48.15 || lon != 11.46 {
		t.Errorf("home = %v %v", lat, lon)
	}
}

func TestSettingsSearchWhenTheHomeHasNoLocationYet(t *testing.T) {
	b := signedInBox(t)
	if _, err := b.db.SQL().Exec(`UPDATE households SET latitude = NULL, longitude = NULL`); err != nil {
		t.Fatal(err)
	}
	r := b.do(http.MethodGet, "/settings/household", nil, en)
	if !strings.Contains(r.body, "No location yet") {
		t.Errorf("missing location not pointed out:\n%s", r.body)
	}
	if r := b.post("/settings/household", settingsForm("Pasing")); r.status != http.StatusOK || len(radios(r.body, "place")) != 1 {
		t.Fatalf("status %d: the unchanged name must be searched while there is no location", r.status)
	}
}

func TestSettingsFieldErrorsForNoPlaceAndUnreachableKeepWhatWasTyped(t *testing.T) {
	b := signedInBox(t)
	r := b.do(http.MethodPost, "/settings/household", settingsForm("Nirgendwo"), en)
	tag := inputTag(r.body, "home_name")
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(tag, `aria-invalid="true"`) || !strings.Contains(tag, `value="Nirgendwo"`) || !strings.Contains(r.body, "No place found") {
		t.Fatalf("no match: status %d, %q", r.status, tag)
	}
	if !strings.Contains(r.body, `value="12h" checked`) {
		t.Error("other typed values lost")
	}
	b.geo.setDown(true)
	r = b.do(http.MethodPost, "/settings/household", settingsForm("Neustadt"), en)
	tag = inputTag(r.body, "home_name")
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(tag, `value="Neustadt"`) || !strings.Contains(r.body, "could not be reached") {
		t.Fatalf("unreachable: status %d, %q", r.status, tag)
	}
	if _, _, name := homeOf(t, b); name != "Pasing" {
		t.Errorf("home changed to %q", name)
	}
	if r := b.post("/settings/household", settingsForm("  ")); r.status != http.StatusUnprocessableEntity || !strings.Contains(inputTag(r.body, "home_name"), `aria-invalid="true"`) {
		t.Errorf("empty name: status %d", r.status)
	}
}

func TestChangingTheHomeShowsTheWeatherThereAtOnce(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.homeWeather(t) // 18 degrees at the old location
	f.meteo.set(`{"current":{"temperature_2m":3.0,"weather_code":3,"is_day":1}}`, 200)
	g := settingsForm("Neustadt")
	g.Set("temperature_unit", "c")
	g.Set("place", "50.25,11.88")
	g.Set("place_for", "Neustadt")
	if r := f.post("/settings/household", g); r.status != http.StatusSeeOther {
		t.Fatalf("status = %d", r.status)
	}
	if chip := chipRe.FindString(f.view(t, "en", f.at(14, 8, 0))); !strings.Contains(chip, "3°C") {
		t.Errorf("chip = %q, want the weather of the new home", chip)
	}
}

// --- /settings and /places API (parity with the web interface, ADR 0015) ---

func TestSettingsAPIExposesAndChangesTheHomeLocation(t *testing.T) {
	c := signedInAPI(t)
	status, got, _ := c.call(http.MethodGet, "/settings", nil)
	if status != http.StatusOK || got["home_name"] != "Pasing" || got["latitude"] != 48.15 || got["longitude"] != 11.46 {
		t.Fatalf("get = %d %v", status, got)
	}
	status, got, _ = c.call(http.MethodPatch, "/settings", map[string]any{"home_name": "Neustadt", "latitude": 50.25, "longitude": 11.88})
	if status != http.StatusOK || got["home_name"] != "Neustadt" || got["latitude"] != 50.25 || got["longitude"] != 11.88 {
		t.Fatalf("patch = %d %v", status, got)
	}
	if lat, lon, name := homeOf(t, c.b); lat != 50.25 || lon != 11.88 || name != "Neustadt" {
		t.Errorf("stored = %v %v %q", lat, lon, name)
	}
	// Coordinates alone move the location, the name stays.
	status, got, _ = c.call(http.MethodPatch, "/settings", map[string]any{"latitude": -33.9, "longitude": 18.4})
	if status != http.StatusOK || got["home_name"] != "Neustadt" || got["latitude"] != -33.9 {
		t.Fatalf("coordinates only = %d %v", status, got)
	}
	// Other settings leave the location alone.
	status, got, _ = c.call(http.MethodPatch, "/settings", map[string]any{"time_format": "12h"})
	if status != http.StatusOK || got["latitude"] != -33.9 || got["home_name"] != "Neustadt" {
		t.Fatalf("other setting = %d %v", status, got)
	}
}

func TestSettingsAPIRefusesBadLocations(t *testing.T) {
	c := signedInAPI(t)
	for name, body := range map[string]map[string]any{
		"latitude too big":     {"latitude": 91.0, "longitude": 0.0},
		"latitude too small":   {"latitude": -90.5, "longitude": 0.0},
		"longitude too big":    {"latitude": 0.0, "longitude": 180.5},
		"latitude alone":       {"latitude": 48.0},
		"longitude alone":      {"longitude": 11.0},
		"empty name":           {"home_name": " ", "latitude": 48.0, "longitude": 11.0},
		"new name, no coords":  {"home_name": "Neustadt"},
		"time format and move": {"time_format": "25h", "latitude": 48.0, "longitude": 11.0},
	} {
		if status, _, _ := c.call(http.MethodPatch, "/settings", body); status != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want 422", name, status)
		}
	}
	if lat, lon, name := homeOf(t, c.b); lat != 48.15 || lon != 11.46 || name != "Pasing" {
		t.Errorf("a refused patch changed the home: %v %v %q", lat, lon, name)
	}
	ro := c.withToken(c.newToken("read"))
	if status, _, _ := ro.call(http.MethodPatch, "/settings", map[string]any{"latitude": 1.0, "longitude": 1.0}); status != http.StatusForbidden {
		t.Errorf("read token moves the home = %d, want 403", status)
	}
}

func TestSettingsAPIShowsAnUnknownLocationAsNull(t *testing.T) {
	c := signedInAPI(t)
	if _, err := c.b.db.SQL().Exec(`UPDATE households SET latitude = NULL, longitude = NULL`); err != nil {
		t.Fatal(err)
	}
	status, got, _ := c.call(http.MethodGet, "/settings", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if v, ok := got["latitude"]; !ok || v != nil {
		t.Errorf("latitude = %v (present %v), want null", v, ok)
	}
}

func TestPlacesAPISearchesOnceAndListsWhatMatches(t *testing.T) {
	c := signedInAPI(t)
	before := len(c.b.geo.searches())
	status, got, _ := c.call(http.MethodGet, "/places?name=Neustadt", nil)
	places, _ := got["places"].([]any)
	if status != http.StatusOK || len(places) != 2 {
		t.Fatalf("status %d: %v", status, got)
	}
	first := places[1].(map[string]any)
	if first["label"] != "Neustadt, Coburg, Bavaria, Germany" || first["latitude"] != 50.25 || first["longitude"] != 11.88 {
		t.Errorf("place = %v", first)
	}
	qs := c.b.geo.searches()
	if len(qs)-before != 1 || qs[len(qs)-1].Get("countryCode") != "DE" {
		t.Errorf("searches = %v, want one in the household's country", qs)
	}
	// No match is an empty list, not an error.
	if status, got, _ := c.call(http.MethodGet, "/places?name=Nirgendwo", nil); status != http.StatusOK || len(got["places"].([]any)) != 0 {
		t.Errorf("no match = %d %v", status, got)
	}
	if status, _, _ := c.call(http.MethodGet, "/places?name=%20", nil); status != http.StatusUnprocessableEntity {
		t.Errorf("no name = %d, want 422", status)
	}
	c.b.geo.setDown(true)
	if status, _, _ := c.call(http.MethodGet, "/places?name=Pasing", nil); status != http.StatusBadGateway {
		t.Errorf("unreachable = %d, want 502", status)
	}
	if status, _, _ := c.withToken("nope").call(http.MethodGet, "/places?name=Pasing", nil); status != http.StatusUnauthorized {
		t.Errorf("bad token = %d, want 401", status)
	}
}
