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
