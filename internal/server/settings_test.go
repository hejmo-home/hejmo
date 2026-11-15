package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// ADR 0016: the household settings screen offers time format and temperature unit.
func TestSettingsScreenSavesTimeFormatAndUnit(t *testing.T) {
	b := signedInBox(t)
	r := b.get("/settings/household")
	if r.status != http.StatusOK || !strings.Contains(r.body, `name="time_format"`) || !strings.Contains(r.body, `name="temperature_unit"`) {
		t.Fatalf("settings page = %d\n%s", r.status, r.body)
	}
	if !strings.Contains(r.body, `value="24h" checked`) || !strings.Contains(r.body, `value="c" checked`) {
		t.Fatalf("defaults not selected:\n%s", r.body)
	}
	if r := b.post("/settings/household", url.Values{"time_format": {"12h"}, "temperature_unit": {"f"}}); r.status != http.StatusSeeOther || r.location != "/settings/household" {
		t.Fatalf("save = %d %q", r.status, r.location)
	}
	r = b.get("/settings/household")
	if !strings.Contains(r.body, `value="12h" checked`) || !strings.Contains(r.body, `value="f" checked`) {
		t.Fatalf("saved values not selected:\n%s", r.body)
	}
	if r := b.post("/settings/household", url.Values{"time_format": {"25h"}, "temperature_unit": {"f"}}); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("bad format = %d, want 422", r.status)
	}
}

// ADR 0016: the time format applies to every time shown in the web interface.
func TestWebTimesFollowTheHouseholdTimeFormat(t *testing.T) {
	b := signedInBox(t)
	d := newScreen(b)
	b.post("/displays", pairForm(codeOn(t, d.get("/display", "en").body)))
	b.post("/calendars", url.Values{"name": {"Feiertage"}, "url": {"https://example.org/a.ics"}, "color": {"#B4583A"}, "interval": {"60"}})
	b.post("/caldav/tokens", url.Values{"name": {"Phone"}})
	// The token is stamped with the real clock, so the CalDAV page is matched by shape.
	h24 := regexp.MustCompile(`\d\d\.\d\d\.\d{4}\x{00a0}\d{1,2}:\d\d(?:\x{00a0}[AP]M)?`)
	if body := b.get("/calendars").body; !strings.Contains(body, "14.11.2026\u00a013:00:00") {
		t.Errorf("/calendars does not use the 24h format:\n%s", body)
	}
	if m := h24.FindString(b.get("/caldav").body); m == "" || strings.HasSuffix(m, "M") {
		t.Errorf("/caldav time %q is not 24h", m)
	}
	if page := b.get("/displays").body; !strings.Contains(page, "14.11.2026\u00a013:00") {
		t.Fatalf("24h time missing:\n%s", page)
	}
	b.post("/settings/household", url.Values{"time_format": {"12h"}, "temperature_unit": {"c"}})
	if page := b.get("/displays").body; !strings.Contains(page, "14.11.2026\u00a01:00\u00a0PM") {
		t.Fatalf("12h time missing:\n%s", page)
	}
	if body := b.get("/calendars").body; !strings.Contains(body, "14.11.2026\u00a01:00:00\u00a0PM") || strings.Contains(body, "14.11.2026\u00a013:00") {
		t.Errorf("/calendars does not use the 12h format:\n%s", body)
	}
	if m := h24.FindString(b.get("/caldav").body); !strings.HasSuffix(m, "M") {
		t.Errorf("/caldav time %q is not 12h", m)
	}
}

func TestChildCannotOpenSettings(t *testing.T) {
	b := signedInBox(t)
	child := childSession(t, b)
	if r := child.get("/settings/household"); r.status != http.StatusForbidden {
		t.Fatalf("child GET /settings = %d, want 403", r.status)
	}
	if r := child.post("/settings/household", url.Values{"time_format": {"12h"}, "temperature_unit": {"f"}}); r.status != http.StatusForbidden {
		t.Fatalf("child POST /settings = %d, want 403", r.status)
	}
}

// ADR 0015: everything the web interface can do is also possible through the API.
func TestSettingsWorkThroughTheAPI(t *testing.T) {
	c := signedInAPI(t)
	status, got, _ := c.call(http.MethodGet, "/settings", nil)
	if status != http.StatusOK || got["time_format"] != "24h" || got["temperature_unit"] != "c" {
		t.Fatalf("get = %d %v", status, got)
	}
	status, got, _ = c.call(http.MethodPatch, "/settings", map[string]any{"time_format": "12h"})
	if status != http.StatusOK || got["time_format"] != "12h" || got["temperature_unit"] != "c" {
		t.Fatalf("patch = %d %v", status, got)
	}
	if status, _, _ := c.call(http.MethodPatch, "/settings", map[string]any{"temperature_unit": "kelvin"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("bad unit = %d, want 422", status)
	}
	ro := c.withToken(c.newToken("read"))
	if status, _, _ := ro.call(http.MethodPatch, "/settings", map[string]any{"time_format": "24h"}); status != http.StatusForbidden {
		t.Fatalf("read token patches = %d, want 403", status)
	}
}

// ADR 0016: household settings gain a cut-off time per mealtime, from which it
// is no longer shown on displays. Ticket 6.3 design pass: a switch row per
// mealtime with its state in words; off means no cut-off.
func TestSettingsScreenShowsAndSavesTheMealCutoffTimes(t *testing.T) {
	b := signedInBox(t)
	page := b.do(http.MethodGet, "/settings/household", nil, map[string]string{"Accept-Language": "en"})
	for _, want := range []string{`name="cutoff_breakfast"`, `value="10:00"`, `name="cutoff_lunch"`, `value="13:30"`, `name="cutoff_snack"`, `value="16:30"`, `name="cutoff_dinner"`, "Breakfast", "Dinner",
		`name="cutoff_breakfast_on"`, `name="cutoff_dinner_on"`, "Disappears from displays at a set time", "Shown all day", "Disappears at",
		`class="tcap" type="time"`, `step="300"`, `<div class="cut">`, `class="at"`,
		"Displays show today&#39;s meals. Choose for each mealtime whether it disappears at a set time or stays all day."} {
		if !strings.Contains(page.body, want) {
			t.Fatalf("settings page lacks %q:\n%s", want, page.body)
		}
	}
	// Breakfast, lunch and snack are on, dinner is off with 20:00 waiting in its field.
	for _, m := range []string{"breakfast", "lunch", "snack"} {
		if !regexp.MustCompile(`name="cutoff_` + m + `_on" checked`).MatchString(page.body) {
			t.Errorf("%s switch is not on", m)
		}
	}
	if regexp.MustCompile(`name="cutoff_dinner_on" checked`).MatchString(page.body) || !regexp.MustCompile(`name="cutoff_dinner"[^>]*value="20:00"`).MatchString(page.body) {
		t.Errorf("dinner is not off with 20:00 prefilled:\n%s", page.body)
	}
	if n := strings.Count(page.body, `class="state is-on"`); n != 3 {
		t.Errorf("%d switches show the on state, want 3", n)
	}
	form := url.Values{"time_format": {"24h"}, "temperature_unit": {"c"}, "cutoff_breakfast": {"09:30"}, "cutoff_breakfast_on": {"on"}, "cutoff_lunch": {"13:30"}, "cutoff_snack": {"17:00"}, "cutoff_snack_on": {"on"}, "cutoff_dinner": {"21:00"}, "cutoff_dinner_on": {"on"}}
	if r := b.post("/settings/household", form); r.status != http.StatusSeeOther || r.location != "/settings/household" {
		t.Fatalf("save = %d %q\n%s", r.status, r.location, r.body)
	}
	h, _ := b.db.HomeHousehold()
	// Lunch has a time in its field but its switch is off (unchecked boxes are not sent): no cut-off.
	if got := h.MealCutoffs; got.Breakfast != "09:30" || got.Lunch != "" || got.Snack != "17:00" || got.Dinner != "21:00" {
		t.Fatalf("stored = %+v", got)
	}
	page = b.do(http.MethodGet, "/settings/household", nil, map[string]string{"Accept-Language": "en"})
	if !strings.Contains(page.body, `value="09:30"`) || !strings.Contains(page.body, `value="21:00"`) || regexp.MustCompile(`name="cutoff_lunch_on" checked`).MatchString(page.body) {
		t.Fatalf("saved values not shown:\n%s", page.body)
	}
	if !regexp.MustCompile(`name="cutoff_lunch"[^>]*value="13:30"`).MatchString(page.body) {
		t.Error("an off mealtime does not wait with its suggested time")
	}
}

func TestSwitchedOffMealtimeStoresNoCutoffWhateverTheTimeFieldHolds(t *testing.T) {
	b := signedInBox(t)
	for _, off := range []url.Values{{"cutoff_snack_on": {"off"}}, {}} { // an explicit "off" and an unchecked box (not sent)
		form := url.Values{"time_format": {"24h"}, "temperature_unit": {"c"}, "cutoff_snack": {"17:45"}}
		for k, v := range off {
			form[k] = v
		}
		if r := b.post("/settings/household", form); r.status != http.StatusSeeOther {
			t.Fatalf("save = %d\n%s", r.status, r.body)
		}
		if h, _ := b.db.HomeHousehold(); h.MealCutoffs.Snack != "" {
			t.Errorf("snack cut-off = %q with its switch off (%v), want none", h.MealCutoffs.Snack, off)
		}
		b.post("/settings/household", url.Values{"time_format": {"24h"}, "temperature_unit": {"c"}, "cutoff_snack": {"16:30"}, "cutoff_snack_on": {"on"}})
	}
}

func TestSwitchedOnMealtimeWithAnEmptyTimeStartsAtItsSuggestedTime(t *testing.T) {
	b := signedInBox(t)
	b.post("/settings/household", url.Values{"time_format": {"24h"}, "temperature_unit": {"c"}, "cutoff_dinner": {""}, "cutoff_dinner_on": {"on"}})
	if h, _ := b.db.HomeHousehold(); h.MealCutoffs.Dinner != "20:00" {
		t.Errorf("dinner = %q, want 20:00", h.MealCutoffs.Dinner)
	}
}

func TestSettingsScreenShowsTheSwitchStatesInGerman(t *testing.T) {
	b := signedInBox(t)
	page := b.do(http.MethodGet, "/settings/household", nil, map[string]string{"Accept-Language": "de"}).body
	for _, want := range []string{"Verschwindet zu einer festen Uhrzeit von Displays", "Den ganzen Tag sichtbar", "Verschwindet um", "Frühstück", "Abendessen", "Displays zeigen die Mahlzeiten von heute."} {
		if !strings.Contains(page, want) {
			t.Errorf("German page lacks %q", want)
		}
	}
}

func TestSettingsFormWithoutCutoffFieldsLeavesThemAlone(t *testing.T) {
	b := signedInBox(t)
	if r := b.post("/settings/household", url.Values{"time_format": {"12h"}, "temperature_unit": {"c"}}); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d", r.status)
	}
	h, _ := b.db.HomeHousehold()
	if got := h.MealCutoffs; got.Breakfast != "10:00" || got.Lunch != "13:30" || got.Snack != "16:30" || got.Dinner != "" {
		t.Fatalf("cut-offs = %+v, want the defaults", got)
	}
}

func TestInvalidCutoffShowsAnErrorAtItsFieldAndSavesNothing(t *testing.T) {
	b := signedInBox(t)
	r := b.do(http.MethodPost, "/settings/household", url.Values{"time_format": {"12h"}, "temperature_unit": {"f"}, "cutoff_breakfast": {"09:30"}, "cutoff_breakfast_on": {"on"}, "cutoff_snack": {"25:61"}, "cutoff_snack_on": {"on"}},
		map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `role="alert"`) || !strings.Contains(r.body, `id="err-cutoff_snack"`) {
		t.Fatalf("status = %d\n%s", r.status, r.body)
	}
	if !strings.Contains(r.body, `value="09:30"`) || !strings.Contains(r.body, `value="25:61"`) {
		t.Errorf("the typed values were not kept:\n%s", r.body)
	}
	h, _ := b.db.HomeHousehold()
	if h.TimeFormat != "24h" || h.MealCutoffs.Breakfast != "10:00" {
		t.Fatalf("a refused save changed %+v", h)
	}
}

// ADR 0015: everything the web interface can do is also possible through the API.
func TestMealCutoffsWorkThroughTheAPI(t *testing.T) {
	c := signedInAPI(t)
	status, got, _ := c.call(http.MethodGet, "/settings", nil)
	want := map[string]any{"breakfast": "10:00", "lunch": "13:30", "snack": "16:30", "dinner": ""}
	if status != http.StatusOK || !sameMap(got["meal_cutoffs"], want) {
		t.Fatalf("get = %d %v", status, got)
	}
	status, got, _ = c.call(http.MethodPatch, "/settings", map[string]any{"meal_cutoffs": map[string]any{"dinner": "21:00", "lunch": ""}})
	if status != http.StatusOK || !sameMap(got["meal_cutoffs"], map[string]any{"breakfast": "10:00", "lunch": "", "snack": "16:30", "dinner": "21:00"}) {
		t.Fatalf("patch = %d %v", status, got)
	}
	for name, body := range map[string]string{
		"hour":         `{"meal_cutoffs":{"snack":"24:00"}}`,
		"no leading 0": `{"meal_cutoffs":{"snack":"9:00"}}`,
		"words":        `{"meal_cutoffs":{"snack":"noon"}}`,
		"mixed":        `{"time_format":"12h","meal_cutoffs":{"snack":"noon"}}`,
	} {
		if got := rawCall(t, c, http.MethodPatch, "/settings", body); got != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, got)
		}
	}
	_, got, _ = c.call(http.MethodGet, "/settings", nil)
	if got["time_format"] != "24h" || !sameMap(got["meal_cutoffs"], map[string]any{"breakfast": "10:00", "lunch": "", "snack": "16:30", "dinner": "21:00"}) {
		t.Fatalf("a refused patch changed %v", got)
	}
}

func sameMap(got any, want map[string]any) bool {
	m, ok := got.(map[string]any)
	if !ok || len(m) != len(want) {
		return false
	}
	for k, v := range want {
		if m[k] != v {
			return false
		}
	}
	return true
}
