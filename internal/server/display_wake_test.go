package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Q79: a touch while quiet mode switched the display off wakes it for the
// adjustable minutes at evening brightness, showing the night page.
func TestATouchWakesAQuietDisplayWithTheNightPage(t *testing.T) {
	f, fake := kioskBox(t, "screen", quietForm(url.Values{"wake_minutes": {"2"}}))
	f.tick(f.at(14, 23, 0))
	fake.take()
	if p := f.view(t, "en", f.at(14, 23, 1)); !strings.Contains(p, `data-quiet="black"`) || !strings.Contains(p, `data-wake-url="/display/wake"`) {
		t.Fatalf("a switched-off screen shows a black page that listens for a touch:\n%s", p)
	}
	if r := f.display.post("/display/wake", nil); r.status != http.StatusNoContent {
		t.Fatalf("wake = %d, want 204", r.status)
	}
	if p := f.view(t, "en", f.at(14, 23, 2)); !strings.Contains(p, `data-quiet="night"`) {
		t.Fatalf("a woken screen shows the night page:\n%s", p)
	}
	f.tick(f.at(14, 23, 2))
	expect(t, "woken", fake.take(), "screen_on", "brightness 40")
	if p := f.view(t, "en", f.at(14, 23, 4)); !strings.Contains(p, `data-quiet="black"`) {
		t.Fatalf("after the wake minutes the page is black again:\n%s", p)
	}
	f.tick(f.at(14, 23, 4))
	expect(t, "off again", fake.take(), "screen_off")
}

func TestOnlyAQuietDisplayWithAKioskAppAndQuietModeOffListensForATouch(t *testing.T) {
	// A kiosk app and Off: the black page listens.
	f, _ := kioskBox(t, "screen", quietForm(nil))
	if p := f.view(t, "en", f.at(14, 23, 0)); !strings.Contains(p, "data-wake-url") {
		t.Error("a quiet screen with a kiosk app does not listen")
	}
	// Dimmed: the night page stays visible, nothing to wake.
	f.saveSettings(t, quietForm(url.Values{"quiet_light": {"dim"}}))
	p := f.view(t, "en", f.at(14, 23, 0))
	if !strings.Contains(p, `data-quiet="night"`) || strings.Contains(p, "data-wake-url") {
		t.Errorf("a dimmed screen shows the night page and does not listen:\n%s", p)
	}
	// No kiosk app: a plain browser shows a black page and cannot be woken.
	b := newFamilyBox(t, "screen", withKiosk(&fakeKiosk{}))
	b.quietOn(t)
	if p := b.view(t, "en", b.at(14, 23, 0)); !strings.Contains(p, `data-quiet="black"`) || strings.Contains(p, "data-wake-url") {
		t.Errorf("a plain browser at night:\n%s", p)
	}
	if r := b.display.post("/display/wake", nil); r.status != http.StatusNoContent || b.kioskRun.Woken(b.displayID(t)) {
		t.Errorf("a display without a kiosk app was woken (%d)", r.status)
	}
	// Daytime touches change nothing.
	d, _ := kioskBox(t, "screen", quietForm(nil))
	d.now = d.at(14, 12, 0)
	if r := d.display.post("/display/wake", nil); r.status != http.StatusNoContent || d.kioskRun.Woken(d.displayID(t)) {
		t.Errorf("a daytime touch woke the display (%d)", r.status)
	}
}

func TestWakeNeedsAPairedDisplay(t *testing.T) {
	f, _ := kioskBox(t, "screen", nil)
	if r := newScreen(f.box).post("/display/wake", nil); r.status != http.StatusForbidden {
		t.Errorf("an unknown browser = %d, want 403", r.status)
	}
}

// ADR 0015 parity: the wake is on the API too, with the display's token.
func TestAWakeWorksThroughTheAPIWithTheDisplaysToken(t *testing.T) {
	c := signedInAPI(t, withKiosk(&fakeKiosk{}))
	anon := newAPIClient(t, c.b)
	_, opened := openAPIPairing(anon)
	d := anon.withToken(opened["token"].(string))
	if status, _, _ := d.call(http.MethodPost, "/display/wake", nil); status != http.StatusForbidden {
		t.Fatalf("a display that is not paired = %d, want 403", status)
	}
	_, shown, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": opened["code"], "name": "Kitchen", "type": "screen"})
	id := shown["id"].(string)
	c.call(http.MethodPatch, "/displays/"+id, map[string]any{"kiosk": map[string]any{"url": tablet}, "quiet_mode": map[string]any{"enabled": true, "from": "22:00", "to": "06:00"}})
	c.b.now = time.Date(2026, 11, 14, 22, 30, 0, 0, time.UTC) // 23:30 in Munich: quiet
	if status, _, _ := d.call(http.MethodPost, "/display/wake", nil); status != http.StatusNoContent {
		t.Fatalf("wake = %d, want 204", status)
	}
	if !c.b.kioskRun.Woken(id) {
		t.Fatal("the display is not awake")
	}
}
