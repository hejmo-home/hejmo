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
