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
	h24 := regexp.MustCompile(`\d\d/\d\d/\d{4} \d{1,2}:\d\d(?: [AP]M)?\b`)
	if body := b.get("/calendars").body; !strings.Contains(body, "14/11/2026 13:00:00") {
		t.Errorf("/calendars does not use the 24h format:\n%s", body)
	}
	if m := h24.FindString(b.get("/caldav").body); m == "" || strings.HasSuffix(m, "M") {
		t.Errorf("/caldav time %q is not 24h", m)
	}
	if page := b.get("/displays").body; !strings.Contains(page, "14/11/2026 13:00") {
		t.Fatalf("24h time missing:\n%s", page)
	}
	b.post("/settings/household", url.Values{"time_format": {"12h"}, "temperature_unit": {"c"}})
	if page := b.get("/displays").body; !strings.Contains(page, "14/11/2026 1:00 PM") {
		t.Fatalf("12h time missing:\n%s", page)
	}
	if body := b.get("/calendars").body; !strings.Contains(body, "14/11/2026 1:00:00 PM") || strings.Contains(body, "14/11/2026 13:00") {
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
// is no longer shown on displays.
func TestSettingsScreenShowsAndSavesTheMealCutoffTimes(t *testing.T) {
	b := signedInBox(t)
	page := b.do(http.MethodGet, "/settings/household", nil, map[string]string{"Accept-Language": "en"})
	for _, want := range []string{`name="cutoff_breakfast"`, `value="10:00"`, `name="cutoff_lunch"`, `value="13:30"`, `name="cutoff_snack"`, `value="16:30"`, `name="cutoff_dinner"`, "Breakfast", "Dinner"} {
		if !strings.Contains(page.body, want) {
			t.Fatalf("settings page lacks %q:\n%s", want, page.body)
		}
	}
	form := url.Values{"time_format": {"24h"}, "temperature_unit": {"c"}, "cutoff_breakfast": {"09:30"}, "cutoff_lunch": {""}, "cutoff_snack": {"17:00"}, "cutoff_dinner": {"21:00"}}
	if r := b.post("/settings/household", form); r.status != http.StatusSeeOther || r.location != "/settings/household" {
		t.Fatalf("save = %d %q\n%s", r.status, r.location, r.body)
	}
	h, _ := b.db.HomeHousehold()
	if got := h.MealCutoffs; got.Breakfast != "09:30" || got.Lunch != "" || got.Snack != "17:00" || got.Dinner != "21:00" {
		t.Fatalf("stored = %+v", got)
	}
	page = b.get("/settings/household")
	if !strings.Contains(page.body, `value="09:30"`) || !strings.Contains(page.body, `value="21:00"`) || strings.Contains(page.body, `value="13:30"`) {
		t.Fatalf("saved values not shown:\n%s", page.body)
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
	r := b.do(http.MethodPost, "/settings/household", url.Values{"time_format": {"12h"}, "temperature_unit": {"f"}, "cutoff_breakfast": {"09:30"}, "cutoff_snack": {"25:61"}},
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
