package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The home location (ADR 0020, ticket 86): the parent picks a place from the
// list of places on the box. The page posts place_id and never coordinates;
// the box looks coordinates and region up. Nothing goes to the internet.

// typedSetup is the setup form as sent while the parent still types the home:
// the typed text, no place picked (what a browser without JavaScript sends when
// Enter is pressed).
func typedSetup(text string) url.Values {
	f := validSetupForm()
	f.Set("home_name", text)
	f.Del("place_id")
	f.Del("region")
	return f
}

// pickedSetup is the setup form after a suggestion button was pressed.
func pickedSetup(text, id string) url.Values {
	f := typedSetup(text)
	f.Set("place_id", id)
	return f
}

func homeOf(t *testing.T, b *box) (lat, lon float64, name string) {
	t.Helper()
	h, err := b.db.HomeHousehold()
	if err != nil {
		t.Fatal(err)
	}
	return h.Latitude, h.Longitude, h.HomeName
}

func options(page string) []string {
	return regexp.MustCompile(`<li role="option"[^>]*>`).FindAllString(page, -1)
}

// selectedRegion is the value of the selected option of the region select.
func selectedRegion(page string) string {
	m := regexp.MustCompile(`<option value="([^"]*)" selected>`).FindStringSubmatch(page)
	if m == nil {
		return "(none)"
	}
	return m[1]
}

func TestSetupSuggestsAtMostFivePlacesAndNeverPlusN(t *testing.T) {
	b := newBox(t)
	r := b.do(http.MethodPost, "/setup", typedSetup("Neustadt"), en)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (no place picked)", r.status)
	}
	if n := len(options(r.body)); n != 5 {
		t.Errorf("%d suggestions, want 5", n)
	}
	for _, want := range []string{
		`<p class="place-q" id="pl-q">More than 5 places match; here are the 5 largest. Which one do you mean?</p>`,
		`<ul class="place-list" id="pl" role="listbox" aria-labelledby="pl-q">`,
		`<p class="hint">Not in the list? Keep typing, or pick a larger town nearby.</p>`,
		`<p class="hint place-privacy">The list of places in Germany, Austria and Switzerland is on the box. Nothing goes to the internet while you type.</p>`,
		"Pick your town in the list.",
		`aria-expanded="true" aria-controls="pl" aria-autocomplete="list"`,
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	if strings.Contains(r.body, "aria-selected=\"true\"") {
		t.Error("a suggestion is highlighted before the arrow keys were used")
	}
	if regexp.MustCompile(`\+\s*\d`).MatchString(regexp.MustCompile(`(?s)<ul class="place-list".*?</ul>`).FindString(r.body)) {
		t.Error("the list shows a +N")
	}
	one := b.do(http.MethodPost, "/setup", typedSetup("Pasing"), en)
	if !strings.Contains(one.body, "1 place matches. Is this the one you mean?") || len(options(one.body)) != 1 {
		t.Errorf("one match:\n%s", one.body)
	}
	two := b.do(http.MethodPost, "/setup", typedSetup("Obermenz"), en)
	if len(options(two.body)) < 1 {
		t.Errorf("Obermenz finds nothing:\n%s", two.body)
	}
	if m := regexp.MustCompile(`(\d) places match\. Which one do you mean\?`).FindStringSubmatch(b.do(http.MethodPost, "/setup", typedSetup("Pasin"), en).body); m != nil && m[1] != strconv.Itoa(len(options(b.do(http.MethodPost, "/setup", typedSetup("Pasin"), en).body))) {
		t.Errorf("the question counts %s places but shows another number", m[1])
	}
	if g := b.do(http.MethodPost, "/setup", typedSetup("Neustadt"), de).body; !strings.Contains(g, "Mehr als 5 Orte passen, hier die 5 größten. Welchen meinst du?") || !strings.Contains(g, "Tippe weiter oder nimm einen größeren Ort in der Nähe.") {
		t.Errorf("German question missing:\n%s", g)
	}
}

func TestSetupSuggestionsNameTheRegionInThePageLanguage(t *testing.T) {
	b := newBox(t)
	if r := b.do(http.MethodPost, "/setup", typedSetup("Pasing"), en); !strings.Contains(r.body, "<b>Pasing</b><small>district of München, Bayern, Germany</small>") {
		t.Errorf("English label:\n%s", r.body)
	}
	if r := b.do(http.MethodPost, "/setup", typedSetup("Pasing"), de); !strings.Contains(r.body, "<b>Pasing</b><small>Stadtteil von München, Bayern, Deutschland</small>") {
		t.Errorf("German label:\n%s", r.body)
	}
}

func TestSetupIgnoresPostedCoordinatesAndResolvesThePlaceID(t *testing.T) {
	b := newBox(t)
	f := validSetupForm()
	for k, v := range map[string]string{"latitude": "1.5", "longitude": "2.5", "lat": "1.5", "lon": "2.5", "place": "1.5,2.5", "home_name_lat": "9"} {
		f.Set(k, v)
	}
	if r := b.post("/setup", f); r.status != http.StatusSeeOther || r.location != "/setup/totp" {
		t.Fatalf("setup = %d %q\n%s", r.status, r.location, r.body)
	}
	h, err := b.db.HomeHousehold()
	if err != nil {
		t.Fatal(err)
	}
	if h.Latitude != 48.1415 || h.Longitude != 11.456 || h.HomeName != "Pasing" || h.HomePlaceID != 2855334 || h.Region != "DE-BY" {
		t.Errorf("household = %+v, want Pasing from the list", h)
	}
}

func TestSetupRefusesAnUnknownPlaceIDWithAFieldError(t *testing.T) {
	b := newBox(t)
	for _, bad := range []string{"999999999", "abc", "-5", "0", "48.15"} {
		f := validSetupForm()
		f.Set("place_id", bad)
		r := b.do(http.MethodPost, "/setup", f, en)
		tag := inputTag(r.body, "home_name")
		if r.status != http.StatusUnprocessableEntity || !strings.Contains(tag, `aria-invalid="true"`) || !strings.Contains(r.body, "Pick your town in the list.") {
			t.Errorf("place_id %q: status %d, home_name %q", bad, r.status, tag)
		}
		if b.db.Configured() {
			t.Errorf("place_id %q created the household", bad)
		}
	}
}

// Without JavaScript: Enter shows the suggestions, a suggestion button picks,
// the next submit creates the household.
func TestSetupWithoutJavaScriptShowsSuggestionsThenTheConfirmedPlace(t *testing.T) {
	b := newBox(t)
	r := b.do(http.MethodPost, "/setup", typedSetup("München"), en)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `<button type="submit" name="place_id" value="`+idMuenchen+`"`) {
		t.Fatalf("suggestions: status %d\n%s", r.status, r.body)
	}
	if !strings.Contains(inputTag(r.body, "home_name"), `value="München"`) || !strings.Contains(inputTag(r.body, "name"), `value="Mama"`) {
		t.Error("what was typed is lost")
	}
	if selectedRegion(r.body) != "" {
		t.Errorf("region = %q before a place was picked, want the empty option", selectedRegion(r.body))
	}
	for _, want := range []string{`<option value="" selected>Set by the place you pick</option>`, `<p class="hint">For public holidays.</p>`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("open wizard misses %q", want)
		}
	}
	// The suggestion button: nothing is created, the place is shown confirmed.
	p := b.do(http.MethodPost, "/setup", pickedSetup("München", idMuenchen), en)
	if p.status != http.StatusOK || b.db.Configured() {
		t.Fatalf("pick: status %d configured %v", p.status, b.db.Configured())
	}
	for _, want := range []string{
		`<p class="lab">Town or district</p>`,
		`<div class="place-status ok" role="status" data-location>`,
		`<b>München</b><small>Bayern, Germany</small><small class="st">Chosen. Used for the weather and the sun times.</small>`,
		`<button class="button secondary" type="submit" name="place_change" value="1">Change</button>`,
		`<input type="hidden" name="place_id" value="` + idMuenchen + `">`,
		`<p class="hint">Taken from the place. Sets the public holidays.</p>`,
	} {
		if !strings.Contains(p.body, want) {
			t.Errorf("confirmed state misses %q", want)
		}
	}
	if inputTag(p.body, "home_name") != "" {
		t.Error("the confirmed state still has the text field")
	}
	if selectedRegion(p.body) != "DE-BY" || strings.Contains(p.body, `<option value="" selected>`) {
		t.Errorf("region = %q, want DE-BY from the place and no empty option", selectedRegion(p.body))
	}
	// Create: the confirmed form carries the id and the region.
	f := validSetupForm()
	f.Set("place_id", idMuenchen)
	f.Set("region", "DE-BY")
	if r := b.post("/setup", f); r.status != http.StatusSeeOther || r.location != "/setup/totp" {
		t.Fatalf("create = %d\n%s", r.status, r.body)
	}
	if _, _, name := homeOf(t, b); name != "München" {
		t.Errorf("home name = %q, want the place's name", name)
	}
}

func TestSetupChangeOpensThePickerAgainAndForgetsTheRegionOfThePlace(t *testing.T) {
	b := newBox(t)
	f := validSetupForm()
	f.Set("place_id", idMuenchen)
	f.Set("region", "DE-BY") // taken from the place
	f.Set("place_change", "1")
	r := b.do(http.MethodPost, "/setup", f, en)
	if r.status != http.StatusOK || b.db.Configured() {
		t.Fatalf("status %d configured %v", r.status, b.db.Configured())
	}
	if !strings.Contains(inputTag(r.body, "home_name"), `value="München"`) || len(options(r.body)) == 0 {
		t.Errorf("the field is not open with suggestions for the place:\n%s", r.body)
	}
	if selectedRegion(r.body) != "" {
		t.Errorf("region = %q, want it empty again (set by the next pick)", selectedRegion(r.body))
	}
	// An explicit change of the region stays.
	f.Set("region", "AT-9")
	if r := b.do(http.MethodPost, "/setup", f, en); selectedRegion(r.body) != "AT-9" {
		t.Errorf("an explicit region = %q, want AT-9 kept", selectedRegion(r.body))
	}
}

func TestSetupTakesTheRegionFromThePlaceAndKeepsAnExplicitChoice(t *testing.T) {
	b := newBox(t)
	f := validSetupForm()
	f.Set("place_id", idMuenchen)
	f.Del("region") // the select still says "Set by the place you pick"
	if r := b.post("/setup", f); r.status != http.StatusSeeOther {
		t.Fatalf("status %d\n%s", r.status, r.body)
	}
	if h, _ := b.db.HomeHousehold(); h.Region != "DE-BY" {
		t.Errorf("region = %q, want DE-BY from München", h.Region)
	}
	b = newBox(t)
	f.Set("region", "AT-9") // the parent chose another one
	if r := b.post("/setup", f); r.status != http.StatusSeeOther {
		t.Fatalf("status %d", r.status)
	}
	if h, _ := b.db.HomeHousehold(); h.Region != "AT-9" || h.HomeName != "München" {
		t.Errorf("household = %+v, want the explicit region kept", h)
	}
	// A place of another country selects that country's region.
	b = newBox(t)
	g := validSetupForm()
	g.Set("place_id", idWien)
	g.Del("region")
	b.post("/setup", g)
	if h, _ := b.db.HomeHousehold(); h.Region != "AT-9" {
		t.Errorf("Wien region = %q", h.Region)
	}
}

func TestSetupNotFoundShowsTheErrorAtTheFieldAndKeepsTheText(t *testing.T) {
	b := newBox(t)
	r := b.do(http.MethodPost, "/setup", typedSetup("Pasingg"), en)
	tag := inputTag(r.body, "home_name")
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", r.status)
	}
	for _, want := range []string{`aria-invalid="true"`, `aria-describedby="err-home"`, `value="Pasingg"`, `aria-expanded="false"`} {
		if !strings.Contains(tag, want) {
			t.Errorf("input misses %q: %s", want, tag)
		}
	}
	for _, want := range []string{
		`<div class="f place-pick invalid">`,
		`<p class="ferr" id="err-home" role="alert">`,
		`<span>No place called “Pasingg” found. Check the spelling, or pick a larger town nearby.</span>`,
		`<p class="hint place-privacy">`,
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	if len(options(r.body)) != 0 || b.db.Configured() {
		t.Error("suggestions for a nothing, or household created")
	}
	if !strings.Contains(inputTag(r.body, "name"), `value="Mama"`) {
		t.Error("other entries lost")
	}
	g := b.do(http.MethodPost, "/setup", typedSetup("Garmisch-Partenkirchn"), de)
	if !strings.Contains(g.body, "Kein Ort „Garmisch-Partenkirchn“ gefunden. Prüf die Schreibweise oder nimm einen größeren Ort in der Nähe.") {
		t.Errorf("German message:\n%s", g.body)
	}
	if e := b.do(http.MethodPost, "/setup", typedSetup(""), en); e.status != http.StatusUnprocessableEntity || !strings.Contains(e.body, "Please enter your town.") {
		t.Errorf("empty text: status %d", e.status)
	}
}

func TestSetupKeepsThePickedPlaceWhileOtherFieldsAreCorrected(t *testing.T) {
	b := newBox(t)
	f := validSetupForm()
	f.Set("password", "x") // another field is wrong
	r := b.do(http.MethodPost, "/setup", f, en)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `<b>Pasing</b><small>district of München, Bayern, Germany</small>`) || !strings.Contains(inputTag(r.body, "place_id"), `value="`+idPasing+`"`) {
		t.Errorf("status %d, the picked place is not carried along:\n%s", r.status, r.body)
	}
}

func TestSetupPressingEnterDoesNotPressChange(t *testing.T) {
	b := newBox(t)
	r := b.do(http.MethodPost, "/setup", pickedSetup("München", idMuenchen), en)
	form := formOf(t, r.body, "/setup")
	first := regexp.MustCompile(`<button[^>]*type="submit"[^>]*>`).FindString(form)
	if !strings.Contains(first, `tabindex="-1"`) || strings.Contains(first, "name=") {
		t.Errorf("the first submit button of the form is %q: Enter would press it", first)
	}
}

// --- Settings → Household ---

func settingsForm() url.Values {
	return url.Values{"time_format": {"12h"}, "temperature_unit": {"f"}}
}

func TestHouseholdSettingsShowTheHomeInUseWithSunTimes(t *testing.T) {
	b := signedInBox(t)
	r := b.do(http.MethodGet, "/settings/household", nil, en)
	for _, want := range []string{
		"<legend>Home location</legend>",
		`<span class="hint">Sunrise and sunset (front light, themes) and the weather are taken for this place.</span>`,
		`<p class="lab">Town or district</p>`,
		`<b>Pasing</b><small>district of München, Bayern, Germany</small><small class="st">In use · today sunrise `,
		`<input type="hidden" name="place_id" value="` + idPasing + `">`,
		`name="place_change" value="1">Change</button>`,
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	if !regexp.MustCompile(`In use · today sunrise \d\d:\d\d, sunset \d\d:\d\d</small>`).MatchString(r.body) {
		t.Errorf("sun times of today missing:\n%s", r.body)
	}
	if strings.Contains(r.body, "place-keep") || strings.Contains(r.body, "° N") {
		t.Error("keep row or coordinates shown for the place in use")
	}
	g := b.do(http.MethodGet, "/settings/household", nil, de).body
	for _, want := range []string{"<legend>Heimatort</legend>", "Ort oder Stadtteil", "Stadtteil von München, Bayern, Deutschland", "In Verwendung · heute Sonnenaufgang ", ", Sonnenuntergang "} {
		if !strings.Contains(g, want) {
			t.Errorf("German page misses %q", want)
		}
	}
}

func TestSettingsChangeOpensThePickerAndKeepRestoresTheSavedPlace(t *testing.T) {
	b := signedInBox(t)
	f := settingsForm()
	f.Set("place_id", idPasing)
	f.Set("place_change", "1")
	r := b.do(http.MethodPost, "/settings/household", f, en)
	if r.status != http.StatusOK {
		t.Fatalf("status = %d", r.status)
	}
	if !strings.Contains(inputTag(r.body, "home_name"), `value="Pasing"`) || len(options(r.body)) == 0 {
		t.Errorf("picker not open with suggestions:\n%s", r.body)
	}
	for _, want := range []string{
		`<div class="place-keep" data-location><p class="hint">Until you save, Pasing stays in use.</p><button class="button secondary" type="submit" name="place_keep" value="1">Keep Pasing</button></div>`,
		`value="12h" checked`, // what was typed stays
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	if h, _ := b.db.HomeHousehold(); h.TimeFormat == "12h" {
		t.Error("Change saved the other settings")
	}
	// Keep: the saved place is back, nothing was saved.
	k := settingsForm()
	k.Set("home_name", "Obermen")
	k.Set("place_keep", "1")
	r = b.do(http.MethodPost, "/settings/household", k, en)
	if r.status != http.StatusOK || !strings.Contains(r.body, `<b>Pasing</b>`) || !strings.Contains(r.body, "In use · today sunrise") || inputTag(r.body, "home_name") != "" || strings.Contains(r.body, "place-keep") {
		t.Errorf("Keep did not restore the saved place: status %d\n%s", r.status, r.body)
	}
	if _, _, name := homeOf(t, b); name != "Pasing" {
		t.Errorf("home = %q", name)
	}
	if g := b.do(http.MethodPost, "/settings/household", f, de).body; !strings.Contains(g, "Pasing behalten") || !strings.Contains(g, "Bis du speicherst, bleibt Pasing in Verwendung.") {
		t.Errorf("German keep row:\n%s", g)
	}
}

func TestSettingsPickShowsPendingThenSaveStoresThePlace(t *testing.T) {
	b := signedInBox(t)
	pick := settingsForm()
	pick.Set("home_name", "Obermen")
	pick.Set("place_id", idObermenzing)
	r := b.do(http.MethodPost, "/settings/household", pick, en)
	if r.status != http.StatusOK {
		t.Fatalf("status = %d", r.status)
	}
	for _, want := range []string{`<b>Obermenzing</b>`, `Chosen, not saved yet. Until you save, Pasing stays in use.`, `<input type="hidden" name="place_id" value="` + idObermenzing + `">`, `value="12h" checked`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("pending state misses %q", want)
		}
	}
	if _, _, name := homeOf(t, b); name != "Pasing" {
		t.Errorf("saved before Save: %q", name)
	}
	if h, _ := b.db.HomeHousehold(); h.TimeFormat == "12h" {
		t.Error("other settings saved before Save")
	}
	// Save posts the id only.
	save := settingsForm()
	save.Set("place_id", idObermenzing)
	if r := b.post("/settings/household", save); r.status != http.StatusSeeOther || r.location != "/settings/household" {
		t.Fatalf("save = %d %q\n%s", r.status, r.location, r.body)
	}
	if page := b.do(http.MethodGet, "/settings/household", nil, en).body; !strings.Contains(page, "Saved. Sunrise, sunset and the weather now use Obermenzing.") || !strings.Contains(page, "In use · today sunrise") {
		t.Errorf("after saving:\n%s", page)
	}
	h, _ := b.db.HomeHousehold()
	if h.HomeName != "Obermenzing" || h.HomePlaceID != 2859782 || h.Latitude == 48.1415 || h.TimeFormat != "12h" {
		t.Errorf("household = %+v", h)
	}
	// Saving the same place again is a plain save.
	if r := b.post("/settings/household", save); r.status != http.StatusSeeOther {
		t.Fatal(r.status)
	}
	if page := b.do(http.MethodGet, "/settings/household", nil, en).body; strings.Contains(page, "now use") {
		t.Errorf("an unchanged place gets the plain success line:\n%s", page)
	}
}

func TestSettingsTextWithoutAPickedPlaceIsAFieldErrorWithTheListOpen(t *testing.T) {
	b := signedInBox(t)
	f := settingsForm()
	f.Set("home_name", "Obermen")
	r := b.do(http.MethodPost, "/settings/household", f, en)
	tag := inputTag(r.body, "home_name")
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(tag, `aria-invalid="true"`) || !strings.Contains(tag, `value="Obermen"`) || !strings.Contains(r.body, "Pick your town in the list.") || len(options(r.body)) == 0 {
		t.Fatalf("status %d, tag %q", r.status, tag)
	}
	if !strings.Contains(r.body, "Keep Pasing") || !strings.Contains(r.body, `value="12h" checked`) {
		t.Errorf("keep row or typed values missing:\n%s", r.body)
	}
	f.Set("home_name", "Pasingg")
	r = b.do(http.MethodPost, "/settings/household", f, en)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "No place called “Pasingg” found.") || !strings.Contains(r.body, "Keep Pasing") || len(options(r.body)) != 0 {
		t.Errorf("not found: status %d\n%s", r.status, r.body)
	}
	f.Set("home_name", "  ")
	if r := b.do(http.MethodPost, "/settings/household", f, en); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "Please enter your town.") {
		t.Errorf("empty text: status %d", r.status)
	}
	g := settingsForm()
	g.Set("place_id", "999999999")
	if r := b.do(http.MethodPost, "/settings/household", g, en); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "Pick your town in the list.") {
		t.Errorf("unknown place_id: status %d", r.status)
	}
	if _, _, name := homeOf(t, b); name != "Pasing" {
		t.Errorf("home changed to %q", name)
	}
}

func TestSettingsWithoutPlaceFieldsLeaveTheHomeAlone(t *testing.T) {
	b := signedInBox(t)
	if r := b.post("/settings/household", url.Values{"time_format": {"24h"}, "temperature_unit": {"c"}}); r.status != http.StatusSeeOther {
		t.Fatalf("status = %d", r.status)
	}
	if page := b.do(http.MethodGet, "/settings/household", nil, en).body; !strings.Contains(page, ">Household settings saved.</p>") {
		t.Errorf("plain success line missing:\n%s", page)
	}
	// Saving the place in use is a plain save too.
	f := settingsForm()
	f.Set("place_id", idPasing)
	if r := b.post("/settings/household", f); r.status != http.StatusSeeOther {
		t.Fatalf("status = %d", r.status)
	}
	if lat, lon, name := homeOf(t, b); lat != 48.1415 || lon != 11.456 || name != "Pasing" {
		t.Errorf("home = %v %v %q", lat, lon, name)
	}
}

// A household set up before the list of places has a name and no location: the
// dashed box says so and the list is already open for the saved name.
func TestOlderHouseholdWithoutLocationGetsSuggestionsForItsStoredName(t *testing.T) {
	b := signedInBox(t)
	if _, err := b.db.SQL().Exec(`UPDATE households SET latitude = NULL, longitude = NULL, home_place_id = NULL`); err != nil {
		t.Fatal(err)
	}
	r := b.do(http.MethodGet, "/settings/household", nil, en)
	for _, want := range []string{
		`<div class="place-status missing" data-location>`,
		`<b>No place chosen yet</b><small>Sunrise, sunset and the weather need one. Pick your town in the list below.</small>`,
		`<p class="place-q" id="pl-q">1 place matches. Is this the one you mean?</p>`,
		`value="Pasing"`,
		`<button type="submit" name="place_id" value="` + idPasing + `"`,
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	if strings.Contains(r.body, "place-keep") || strings.Contains(r.body, "place-status ok") {
		t.Error("keep row or place in use shown for a household without a place")
	}
	if g := b.do(http.MethodGet, "/settings/household", nil, de).body; !strings.Contains(g, "<b>Noch kein Ort gewählt</b><small>Sonnenaufgang, Sonnenuntergang und Wetter brauchen ihn. Wähle unten deinen Ort aus der Liste.</small>") {
		t.Errorf("German dashed box:\n%s", g)
	}
	// Saving without picking asks to pick; picking from the list gives the household its place.
	if r := b.post("/settings/household", settingsForm()); r.status != http.StatusOK && r.status != http.StatusSeeOther {
		t.Errorf("a save without place fields = %d", r.status)
	}
	f := settingsForm()
	f.Set("home_name", "Pasing")
	if r := b.post("/settings/household", f); r.status != http.StatusUnprocessableEntity {
		t.Errorf("text without a pick = %d, want 422", r.status)
	}
	f.Set("place_id", idPasing)
	if r := b.do(http.MethodPost, "/settings/household", f, en); r.status != http.StatusOK || !strings.Contains(r.body, "Chosen. Used for the weather and the sun times.") {
		t.Fatalf("pick = %d\n%s", r.status, r.body)
	}
	save := settingsForm()
	save.Set("place_id", idPasing)
	if r := b.post("/settings/household", save); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d", r.status)
	}
	if lat, _, _ := homeOf(t, b); lat != 48.1415 {
		t.Errorf("latitude = %v", lat)
	}
}

// A home set through the API has coordinates and no place id: the box shows
// the name and today's sun times without a region.
func TestHomeSetByCoordinatesShowsNameAndSunTimesWithoutARegion(t *testing.T) {
	c := signedInAPI(t)
	if status, _, _ := c.call(http.MethodPatch, "/settings", map[string]any{"home_name": "Zuhause", "latitude": 48.2, "longitude": 11.4}); status != http.StatusOK {
		t.Fatalf("patch = %d", status)
	}
	r := c.b.do(http.MethodGet, "/settings/household", nil, en)
	if !strings.Contains(r.body, `<b>Zuhause</b><small class="st">In use · today sunrise `) || inputTag(r.body, "place_id") != "" {
		t.Errorf("home without a place id:\n%s", r.body)
	}
	// A save leaves it as it is.
	if r := c.b.post("/settings/household", settingsForm()); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d", r.status)
	}
	if lat, _, name := homeOf(t, c.b); lat != 48.2 || name != "Zuhause" {
		t.Errorf("home = %v %q", lat, name)
	}
}

func TestSettingsWithInvalidFieldsKeepTheTypedPickerState(t *testing.T) {
	b := signedInBox(t)
	g := settingsForm()
	g.Set("time_format", "25h")
	g.Set("place_id", idPasing)
	r := b.do(http.MethodPost, "/settings/household", g, en)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `<b>Pasing</b>`) {
		t.Errorf("status %d, picker lost:\n%s", r.status, r.body)
	}
}

func TestChangingTheHomeShowsTheWeatherThereAtOnce(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.homeWeather(t) // 18 degrees at the old location
	f.meteo.set(`{"current":{"temperature_2m":3.0,"weather_code":3,"is_day":1}}`, 200)
	g := settingsForm()
	g.Set("temperature_unit", "c")
	g.Set("place_id", idObermenzing)
	if r := f.post("/settings/household", g); r.status != http.StatusSeeOther {
		t.Fatalf("status = %d", r.status)
	}
	if chip := chipRe.FindString(f.view(t, "en", f.at(14, 8, 0))); !strings.Contains(chip, "3°C") {
		t.Errorf("chip = %q, want the weather of the new home", chip)
	}
}

// --- The list stays on the box ---

// outgoing records the requests made through http.DefaultTransport, which the
// weather client uses.
type outgoing struct {
	mu   sync.Mutex
	urls []string
}

func (o *outgoing) RoundTrip(r *http.Request) (*http.Response, error) {
	o.mu.Lock()
	o.urls = append(o.urls, r.URL.String())
	o.mu.Unlock()
	return nil, http.ErrNotSupported
}

func TestPlaceSearchNeverLeavesTheBox(t *testing.T) {
	rec := &outgoing{}
	old := http.DefaultTransport
	http.DefaultTransport = rec
	t.Cleanup(func() { http.DefaultTransport = old })
	b := newBox(t)
	b.do(http.MethodPost, "/setup", typedSetup("Neustadt"), en)
	b.do(http.MethodPost, "/setup", typedSetup("Nirgendwo"), en)
	b.do(http.MethodGet, "/places?name=Pasing", nil, en)
	b.do(http.MethodPost, "/setup", pickedSetup("Pasing", idPasing), en)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.urls) != 0 {
		t.Errorf("the box made %d outgoing requests while the parent typed: %v", len(rec.urls), rec.urls)
	}
}

func TestPlacesEndpointOfThePagesAnswersFromTheListWithoutASignIn(t *testing.T) {
	b := newBox(t)
	r := b.do(http.MethodGet, "/places?name=Pasing&lang=de", nil, en)
	var got struct {
		Places []map[string]any `json:"places"`
		More   bool             `json:"more"`
	}
	decodeJSON(t, r.body, &got)
	if r.status != http.StatusOK || len(got.Places) != 1 || got.More || got.Places[0]["name"] != "Pasing" || got.Places[0]["region"] != "Stadtteil von München, Bayern, Deutschland" {
		t.Fatalf("status %d: %s", r.status, r.body)
	}
	if r := b.do(http.MethodGet, "/places?name=Neustadt", nil, en); !strings.Contains(r.body, `"more":true`) {
		t.Errorf("more missing: %s", r.body)
	}
}

// --- /settings and /places API (parity with the web interface, ADR 0015) ---

func TestSettingsAPIExposesAndChangesTheHomeLocation(t *testing.T) {
	c := signedInAPI(t)
	status, got, _ := c.call(http.MethodGet, "/settings", nil)
	if status != http.StatusOK || got["home_name"] != "Pasing" || got["latitude"] != 48.1415 || got["longitude"] != 11.456 || got["home_place_id"] != float64(2855334) {
		t.Fatalf("get = %d %v", status, got)
	}
	status, got, _ = c.call(http.MethodPatch, "/settings", map[string]any{"home_name": "Neustadt", "latitude": 50.25, "longitude": 11.88})
	if status != http.StatusOK || got["home_name"] != "Neustadt" || got["latitude"] != 50.25 || got["longitude"] != 11.88 || got["home_place_id"] != nil {
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

func TestSettingsAPITakesAPlaceByItsID(t *testing.T) {
	c := signedInAPI(t)
	status, got, _ := c.call(http.MethodPatch, "/settings", map[string]any{"home_place_id": 2867714})
	if status != http.StatusOK || got["home_name"] != "München" || got["home_place_id"] != float64(2867714) || got["latitude"] != 48.1374 || got["longitude"] != 11.5755 {
		t.Fatalf("patch = %d %v", status, got)
	}
	if h, _ := c.b.db.HomeHousehold(); h.HomePlaceID != 2867714 || h.HomeName != "München" {
		t.Errorf("household = %+v", h)
	}
	for name, body := range map[string]map[string]any{
		"unknown id":         {"home_place_id": 1},
		"id and coordinates": {"home_place_id": 2867714, "latitude": 48.0, "longitude": 11.0},
		"id and name":        {"home_place_id": 2867714, "home_name": "Anders"},
	} {
		if status, _, _ := c.call(http.MethodPatch, "/settings", body); status != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want 422", name, status)
		}
	}
	if h, _ := c.b.db.HomeHousehold(); h.HomePlaceID != 2867714 {
		t.Errorf("a refused patch changed the place id: %d", h.HomePlaceID)
	}
}

func TestSettingsAPIRefusesBadLocations(t *testing.T) {
	c := signedInAPI(t)
	for name, body := range map[string]map[string]any{
		"latitude too big":     {"latitude": 91.0, "longitude": 0.0},
		"latitude too small":   {"latitude": -90.5, "longitude": 0.0},
		"longitude too big":    {"latitude": 0.0, "longitude": 180.5},
		"null island":          {"latitude": 0.0, "longitude": 0.0},
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
	if lat, lon, name := homeOf(t, c.b); lat != 48.1415 || lon != 11.456 || name != "Pasing" {
		t.Errorf("a refused patch changed the home: %v %v %q", lat, lon, name)
	}
	ro := c.withToken(c.newToken("read"))
	if status, _, _ := ro.call(http.MethodPatch, "/settings", map[string]any{"latitude": 1.0, "longitude": 1.0}); status != http.StatusForbidden {
		t.Errorf("read token moves the home = %d, want 403", status)
	}
}

func TestSettingsAPIShowsAnUnknownLocationAsNull(t *testing.T) {
	c := signedInAPI(t)
	if _, err := c.b.db.SQL().Exec(`UPDATE households SET latitude = NULL, longitude = NULL, home_place_id = NULL`); err != nil {
		t.Fatal(err)
	}
	status, got, _ := c.call(http.MethodGet, "/settings", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	for _, k := range []string{"latitude", "longitude", "home_place_id"} {
		if v, ok := got[k]; !ok || v != nil {
			t.Errorf("%s = %v (present %v), want null", k, v, ok)
		}
	}
}

func TestPlacesAPIAnswersFromTheListOnTheBox(t *testing.T) {
	c := signedInAPI(t)
	status, got, _ := c.call(http.MethodGet, "/places?name=Pasing&lang=en", nil)
	places, _ := got["places"].([]any)
	if status != http.StatusOK || len(places) != 1 || got["more"] != false {
		t.Fatalf("status %d: %v", status, got)
	}
	p := places[0].(map[string]any)
	if p["id"] != float64(2855334) || p["name"] != "Pasing" || p["region"] != "district of München, Bayern, Germany" || p["latitude"] != 48.1415 || p["longitude"] != 11.456 || p["region_code"] != "DE-BY" {
		t.Errorf("place = %v", p)
	}
	// More than five: five items and more: true.
	status, got, _ = c.call(http.MethodGet, "/places?name=Neustadt&lang=de", nil)
	places, _ = got["places"].([]any)
	if status != http.StatusOK || len(places) != 5 || got["more"] != true {
		t.Errorf("Neustadt = %d, %d places, more %v", status, len(places), got["more"])
	}
	if r := places[0].(map[string]any)["region"].(string); !strings.Contains(r, "Deutschland") {
		t.Errorf("lang=de region = %q", r)
	}
	// No match is an empty list, not an error; there is no 502 any more.
	if status, got, _ := c.call(http.MethodGet, "/places?name=Nirgendwo", nil); status != http.StatusOK || len(got["places"].([]any)) != 0 || got["more"] != false {
		t.Errorf("no match = %d %v", status, got)
	}
	// One letter finds nothing: the search starts at two.
	if status, got, _ := c.call(http.MethodGet, "/places?name=P", nil); status != http.StatusOK || len(got["places"].([]any)) != 0 {
		t.Errorf("one letter = %d %v", status, got)
	}
	for _, bad := range []string{"/places?name=%20"} {
		if status, _, _ := c.call(http.MethodGet, bad, nil); status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", bad, status)
		}
	}
	if status, _, _ := c.withToken("nope").call(http.MethodGet, "/places?name=Pasing", nil); status != http.StatusUnauthorized {
		t.Errorf("bad token = %d, want 401", status)
	}
}

// --- Licences ---

func TestLicencesPageCreditsGeoNames(t *testing.T) {
	b := signedInBox(t)
	r := b.do(http.MethodGet, "/settings/licences", nil, en)
	if r.status != http.StatusOK {
		t.Fatalf("status = %d", r.status)
	}
	if !regexp.MustCompile(`Contains data from GeoNames \(https://www.geonames.org\), licensed CC BY 4.0, extract of \d{4}-\d\d-\d\d, filtered to DE/AT/CH and reformatted\.`).MatchString(r.body) {
		t.Errorf("attribution missing:\n%s", r.body)
	}
	if hub := b.do(http.MethodGet, "/settings", nil, en).body; !strings.Contains(hub, `href="/settings/licences"`) {
		t.Error("the Settings hub does not link to the licences")
	}
	signedOut := b.bareClient()
	signedOut.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if res, err := signedOut.Get(b.srv.URL + "/settings/licences"); err != nil || res.StatusCode != http.StatusSeeOther {
		t.Errorf("signed out: %v %v", res, err)
	}
}

// The script that updates the list while typing belongs to the two pages with
// the picker, and to no other.
func TestPlaceScriptIsLoadedOnlyWhereThePickerIs(t *testing.T) {
	b := signedInBox(t)
	if r := b.do(http.MethodGet, "/settings/household", nil, en); !strings.Contains(r.body, `src="/static/place.js"`) {
		t.Error("Settings → Household does not load place.js")
	}
	for _, page := range []string{"/settings", "/", "/people"} {
		if r := b.do(http.MethodGet, page, nil, en); strings.Contains(r.body, "place.js") {
			t.Errorf("%s loads place.js", page)
		}
	}
	w := newBox(t)
	if r := w.do(http.MethodGet, "/setup", nil, en); !strings.Contains(r.body, `src="/static/place.js"`) || !strings.Contains(r.body, `data-url="/places"`) {
		t.Errorf("the wizard does not load place.js:\n%s", r.body)
	}
	js := w.get("/static/place.js")
	if js.status != http.StatusOK || !strings.Contains(js.body, "/places") && !strings.Contains(js.body, "cfg.url") {
		t.Errorf("place.js = %d", js.status)
	}
}

func TestSettingsPlaceStepsKeepATypedDateFormat(t *testing.T) {
	b := signedInBox(t)
	pick := settingsForm()
	pick.Set("date_format", "mdy")
	pick.Set("home_name", "Obermen")
	pick.Set("place_id", idObermenzing)
	r := b.do(http.MethodPost, "/settings/household", pick, en)
	if r.status != http.StatusOK || !strings.Contains(r.body, `value="mdy" checked`) || strings.Contains(r.body, `value="dmy" checked`) {
		t.Fatalf("typed date format lost after a pick: %d\n%s", r.status, r.body)
	}
}

func TestPlacesSearchRefusesALongName(t *testing.T) {
	b := signedInBox(t)
	if r := b.get("/places?name=" + strings.Repeat("a", 101)); r.status != http.StatusUnprocessableEntity {
		t.Errorf("status = %d", r.status)
	}
}
