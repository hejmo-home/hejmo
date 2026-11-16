package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/kiosk"
	"github.com/hejmo-home/hejmo/internal/server"
)

// quietSettings is the settings form of a display with quiet mode on.
func quietForm(extra url.Values) url.Values {
	f := url.Values{"quiet": {"on"}, "quiet_from": {"22:00"}, "quiet_to": {"06:00"}}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

func (f *familyBox) displayID(t *testing.T) string {
	t.Helper()
	ds, err := f.db.Displays(f.householdID)
	if err != nil || len(ds) != 1 {
		t.Fatalf("displays = %v, %v", ds, err)
	}
	return ds[0].ID
}

func (f *familyBox) saveSettings(t *testing.T, form url.Values) resp {
	t.Helper()
	return f.post("/displays/"+f.displayID(t)+"/settings", form)
}

func (f *familyBox) quietOn(t *testing.T) {
	t.Helper()
	if r := f.saveSettings(t, quietForm(nil)); r.status != http.StatusSeeOther {
		t.Fatalf("settings = %d\n%s", r.status, r.body)
	}
}

// 5.9: Given quiet mode from 22:00 to 06:00, when it turns 22:00, then the display switches to quiet mode.
func TestDisplaySwitchesToQuietModeAt2200AndBackAt0600(t *testing.T) {
	f := newFamilyBox(t, "eink_bw")
	f.quietOn(t)
	f.event(t, "Dentist", f.at(15, 9, 0), f.at(15, 10, 0), false, "Mia")
	if p := f.view(t, "en", f.at(14, 21, 59)); strings.Contains(p, "data-quiet") || !strings.Contains(p, "data-family") {
		t.Fatalf("21:59 is not quiet:\n%s", p)
	}
	if p := f.view(t, "en", f.at(14, 22, 0)); !strings.Contains(p, `data-quiet="night"`) || strings.Contains(p, "data-family") {
		t.Fatalf("22:00 is quiet:\n%s", p)
	}
	if p := f.view(t, "en", f.at(15, 5, 59)); !strings.Contains(p, `data-quiet="night"`) {
		t.Fatal("05:59 is quiet")
	}
	if p := f.view(t, "en", f.at(15, 6, 0)); strings.Contains(p, "data-quiet") || !strings.Contains(p, "data-family") {
		t.Fatal("06:00 is not quiet")
	}
}

func TestQuietModeIsOffUnlessSwitchedOn(t *testing.T) {
	f := newFamilyBox(t, "eink_bw")
	if p := f.view(t, "en", f.at(14, 23, 0)); strings.Contains(p, "data-quiet") {
		t.Fatal("quiet by default")
	}
}

// ADR 0015: the night page is about the coming morning: its date, the
// household's first timed event and its waste collection. At 02:00 on
// Sunday it still shows Sunday.
func TestNightPageShowsTheComingMorning(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.quietOn(t)
	f.event(t, "Swimming", f.at(15, 10, 0), f.at(15, 11, 0), false, "Sebastian")
	f.event(t, "Dentist", f.at(15, 8, 30), f.at(15, 9, 15), false, "Mia")
	f.event(t, "Birthday", f.at(15, 0, 0), f.at(16, 0, 0), true)
	f.event(t, "Monday event", f.at(16, 7, 0), f.at(16, 8, 0), false)
	for _, now := range []time.Time{f.at(14, 22, 30), f.at(15, 2, 0)} {
		p := f.view(t, "en", now)
		for _, want := range []string{"Sunday", "15 November", "Dentist", "08:30–09:15"} {
			if !strings.Contains(p, want) {
				t.Errorf("night page at %s misses %q:\n%s", now.Format("02 15:04"), want, p)
			}
		}
		for _, not := range []string{"Swimming", "Birthday", "Monday event", `data-widget`} {
			if strings.Contains(p, not) {
				t.Errorf("night page shows %q", not)
			}
		}
	}
	if p := f.view(t, "de", f.at(15, 2, 0)); !strings.Contains(p, "Sonntag") || !strings.Contains(p, "15. November") {
		t.Fatalf("German night page:\n%s", p)
	}
}

func TestNightPageWithoutEventsSaysSo(t *testing.T) {
	f := newFamilyBox(t, "eink_bw")
	f.quietOn(t)
	if p := f.view(t, "en", f.at(14, 23, 0)); !strings.Contains(p, "data-no-event") {
		t.Fatalf("no hint without events:\n%s", p)
	}
}

// 5.9: a normal screen goes black; without a kiosk app that is the whole story.
func TestNormalScreenShowsABlackPageInQuietMode(t *testing.T) {
	f := newFamilyBox(t, "screen")
	f.quietOn(t)
	f.event(t, "Dentist", f.at(15, 9, 0), f.at(15, 10, 0), false, "Mia")
	p := f.view(t, "en", f.at(14, 23, 0))
	if !strings.Contains(p, `data-quiet="black"`) || strings.Contains(p, "Dentist") || strings.Contains(p, "Sunday") {
		t.Fatalf("not a black page:\n%s", p)
	}
}

// ADR 0015: against ghosting an E-Ink display reloads once an hour, only in
// quiet mode.
func TestEInkDisplaysReloadOncePerHourInQuietModeOnly(t *testing.T) {
	for typ, want := range map[string]bool{"eink_bw": true, "eink_color": true, "screen": false} {
		f := newFamilyBox(t, typ)
		f.quietOn(t)
		quietPage := f.view(t, "en", f.at(14, 23, 0))
		if got := strings.Contains(quietPage, `data-reload-seconds="3600"`); got != want {
			t.Errorf("%s: quiet page reloads hourly = %v, want %v", typ, got, want)
		}
		if p := f.view(t, "en", f.at(15, 12, 0)); strings.Contains(p, "data-reload-seconds") {
			t.Errorf("%s: family view reloads hourly", typ)
		}
	}
}

// The partial view and the live version follow quiet mode too.
func TestLiveQuietModeIsAnnouncedAndServed(t *testing.T) {
	l := newLiveBox(t, 10*time.Millisecond)
	l.quietOn(t)
	l.clock.set(l.at(14, 21, 59))
	s := l.open(t)
	seen := s.first(t)
	l.clock.set(l.at(14, 22, 0))
	v, ok := s.next(t, 2*time.Second)
	if !ok || v == seen {
		t.Fatal("quiet mode not announced at 22:00")
	}
	if _, h, body := l.get("/display/view"); !strings.Contains(body, `data-quiet="night"`) || h.Get("X-Hejmo-View") != v {
		t.Fatalf("view = %q (version %q, announced %q)", body, h.Get("X-Hejmo-View"), v)
	}
}

func TestDisplaysPageEditsQuietModeAndKiosk(t *testing.T) {
	f := newFamilyBox(t, "eink_bw")
	for _, want := range []string{`name="quiet"`, `name="quiet_from"`, `name="quiet_to"`, `name="kiosk_url"`, `name="kiosk_key"`} {
		if !strings.Contains(f.get("/displays/"+f.displayID(t)).body, want) {
			t.Fatalf("displays page misses %s", want)
		}
	}
	form := quietForm(url.Values{"quiet_from": {"21:30"}, "quiet_to": {"06:45"}, "kiosk_url": {"http://192.168.1.20:8080"}, "kiosk_key": {"tablet-key-1"}})
	if r := f.saveSettings(t, form); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d", r.status)
	}
	p := f.get("/displays/" + f.displayID(t)).body
	for _, want := range []string{`name="quiet" checked`, `value="21:30"`, `value="06:45"`, `value="http://192.168.1.20:8080"`} {
		if !strings.Contains(p, want) {
			t.Errorf("page misses %s", want)
		}
	}
	if strings.Contains(p, "tablet-key-1") {
		t.Fatal("the kiosk key is shown again")
	}
	// An empty key field keeps the key.
	f.saveSettings(t, quietForm(url.Values{"kiosk_url": {"http://192.168.1.20:8080"}}))
	if got, _ := f.db.DisplayKioskKey(f.householdID, f.displayID(t)); got != "tablet-key-1" {
		t.Fatalf("key = %q after saving with an empty key field", got)
	}
	for name, bad := range map[string]url.Values{
		"bad time":  quietForm(url.Values{"quiet_from": {"25:00"}}),
		"same time": quietForm(url.Values{"quiet_to": {"22:00"}}),
		"bad url":   quietForm(url.Values{"kiosk_url": {"tablet.local"}}),
	} {
		if r := f.saveSettings(t, bad); r.status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, r.status)
		}
	}
}

func TestQuietModeAndKioskWorkThroughTheAPI(t *testing.T) {
	c := signedInAPI(t)
	code := codeOn(t, newScreen(c.b).get("/display", "en").body)
	_, shown, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": code, "name": "Hall", "type": "eink_bw"})
	id := shown["id"].(string)
	q, _ := shown["quiet_mode"].(map[string]any)
	if q["enabled"] != false || q["from"] != "22:00" || q["to"] != "06:00" {
		t.Fatalf("default quiet_mode = %v", shown["quiet_mode"])
	}
	status, got, _ := c.call(http.MethodPatch, "/displays/"+id, map[string]any{
		"quiet_mode": map[string]any{"enabled": true, "from": "21:00", "to": "07:00"},
		"kiosk":      map[string]any{"url": "http://10.0.0.5:8080", "api_key": "abc"},
	})
	if status != http.StatusOK {
		t.Fatalf("patch = %d %v", status, got)
	}
	q, _ = got["quiet_mode"].(map[string]any)
	k, _ := got["kiosk"].(map[string]any)
	if q["enabled"] != true || q["from"] != "21:00" || q["to"] != "07:00" || k["url"] != "http://10.0.0.5:8080" || k["api_key_set"] != true {
		t.Fatalf("patched = %v", got)
	}
	if strings.Contains(strings.Join(mapKeys(k), ","), "api_key,") || k["api_key"] != nil {
		t.Fatal("the API returns the kiosk key")
	}
	// Only the fields sent change.
	_, got, _ = c.call(http.MethodPatch, "/displays/"+id, map[string]any{"show_tomorrow": true})
	if got["quiet_mode"].(map[string]any)["from"] != "21:00" || got["kiosk"].(map[string]any)["api_key_set"] != true {
		t.Fatalf("untouched settings changed: %v", got)
	}
	// The contract refuses a malformed time itself; the box must too.
	if status := rawCall(t, c, http.MethodPatch, "/displays/"+id, `{"quiet_mode":{"enabled":true,"from":"99:00","to":"07:00"}}`); status != http.StatusUnprocessableEntity {
		t.Errorf("bad time = %d, want 422", status)
	}
	if status, _, _ := c.call(http.MethodPatch, "/displays/"+id, map[string]any{"kiosk": map[string]any{"url": "ftp://x"}}); status != http.StatusUnprocessableEntity {
		t.Errorf("bad url = %d, want 422", status)
	}
	// Removing the kiosk.
	_, got, _ = c.call(http.MethodPatch, "/displays/"+id, map[string]any{"kiosk": map[string]any{"url": ""}})
	if k := got["kiosk"].(map[string]any); k["url"] != "" || k["api_key_set"] != false {
		t.Fatalf("kiosk not removed: %v", got)
	}
}

func mapKeys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// fakeKiosk stands in for FreeKiosk on a tablet; the adapter itself is tested
// against a fake REST API in internal/kiosk.
type fakeKiosk struct {
	mu    sync.Mutex
	calls []string // "url key"
	fail  bool
}

func (f *fakeKiosk) factory() kiosk.Factory {
	return func(base, key string) kiosk.Adapter { return fakeAdapter{f, base + " " + key} }
}

type fakeAdapter struct {
	f   *fakeKiosk
	who string
}

func (a fakeAdapter) FrontLightOff(context.Context) error {
	a.f.mu.Lock()
	defer a.f.mu.Unlock()
	if a.f.fail {
		return errors.New("tablet unreachable")
	}
	a.f.calls = append(a.f.calls, a.who)
	return nil
}

func (f *fakeKiosk) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

// kioskBox is a family box in Munich whose display has a kiosk app.
func kioskBox(t *testing.T, quietOn bool) (*familyBox, *fakeKiosk, *server.KioskRunner) {
	t.Helper()
	f := newFamilyBox(t, "eink_bw")
	if _, err := f.db.SQL().Exec(`UPDATE households SET latitude = 48.15, longitude = 11.46`); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"kiosk_url": {"http://192.168.1.20:8080"}, "kiosk_key": {"k-1"}}
	if quietOn {
		form = quietForm(form)
	}
	if r := f.saveSettings(t, form); r.status != http.StatusSeeOther {
		t.Fatalf("settings = %d", r.status)
	}
	fake := &fakeKiosk{}
	run := server.NewKioskRunner(server.KioskOptions{DB: f.db, Factory: fake.factory(), Now: func() time.Time { return f.now }})
	return f, fake, run
}

// 5.9: Given no light sensor, when the sun sets at the home location, then the
// front light is switched off (tested against a fake kiosk API).
func TestFrontLightGoesOffWhenTheSunSets(t *testing.T) {
	f, fake, run := kioskBox(t, false)
	ctx := context.Background()
	f.now = f.at(14, 16, 0) // sunset in Munich is about 16:36
	run.Tick(ctx)
	if fake.count() != 0 {
		t.Fatal("front light switched off before sunset")
	}
	f.now = f.at(14, 16, 50)
	run.Tick(ctx)
	if fake.count() != 1 || fake.calls[0] != "http://192.168.1.20:8080 k-1" {
		t.Fatalf("calls = %q", fake.calls)
	}
	f.now = f.at(14, 17, 30)
	run.Tick(ctx)
	if fake.count() != 1 {
		t.Fatal("front light command repeated while it stays night")
	}
	// The next evening it is switched off again.
	f.now = f.at(15, 12, 0)
	run.Tick(ctx)
	f.now = f.at(15, 16, 50)
	run.Tick(ctx)
	if fake.count() != 2 {
		t.Fatalf("calls = %d, want 2 after the second sunset", fake.count())
	}
}

func TestFrontLightGoesOffWhenQuietModeStartsEvenInDaylight(t *testing.T) {
	f, fake, run := kioskBox(t, true)
	if _, err := f.db.SQL().Exec(`UPDATE displays SET quiet_from = '12:00', quiet_to = '14:00'`); err != nil {
		t.Fatal(err)
	}
	f.now = f.at(14, 11, 59)
	run.Tick(context.Background())
	f.now = f.at(14, 12, 0)
	run.Tick(context.Background())
	if fake.count() != 1 {
		t.Fatalf("calls = %d, want 1 at the start of quiet mode", fake.count())
	}
}

func TestFrontLightIsRetriedAfterAFailure(t *testing.T) {
	f, fake, run := kioskBox(t, false)
	fake.fail = true
	f.now = f.at(14, 17, 0)
	run.Tick(context.Background())
	fake.mu.Lock()
	fake.fail = false
	fake.mu.Unlock()
	run.Tick(context.Background())
	if fake.count() != 1 {
		t.Fatalf("calls = %d, want the retry to succeed once", fake.count())
	}
}

func TestDisplaysWithoutKioskOrWithoutLocationAreLeftAlone(t *testing.T) {
	f := newFamilyBox(t, "eink_bw")
	fake := &fakeKiosk{}
	run := server.NewKioskRunner(server.KioskOptions{DB: f.db, Factory: fake.factory(), Now: func() time.Time { return f.now }})
	f.quietOn(t)
	f.now = f.at(14, 23, 0)
	run.Tick(context.Background()) // quiet, but no kiosk app: only the page goes quiet
	if fake.count() != 0 {
		t.Fatal("kiosk called without a kiosk app")
	}
	// A kiosk app but no home coordinates: the sun rule cannot apply, the schedule still does.
	f.saveSettings(t, url.Values{"kiosk_url": {"http://tablet:8080"}})
	f.now = f.at(14, 17, 0)
	run.Tick(context.Background())
	if fake.count() != 0 {
		t.Fatal("front light switched off by sun without a home location")
	}
	f.saveSettings(t, quietForm(url.Values{"kiosk_url": {"http://tablet:8080"}}))
	f.now = f.at(14, 22, 5)
	run.Tick(context.Background())
	if fake.count() != 1 {
		t.Fatalf("calls = %d, want 1 from the quiet schedule", fake.count())
	}
}
