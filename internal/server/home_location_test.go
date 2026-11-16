package server_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/sun"
	"github.com/hejmo-home/hejmo/internal/weather"
)

// The home location a parent confirmed in Settings → Household is what the sun
// rule of displays (#29), the themes by sunrise and sunset (#52) and the
// weather (#28) read. These tests start from a box whose home has no location
// (as before this ticket) and give it one only through the web interface.

// withoutLocation takes the coordinates from the household, as on a box set up
// before the home location was confirmed.
func (f *familyBox) withoutLocation(t *testing.T) {
	t.Helper()
	if _, err := f.db.SQL().Exec(`UPDATE households SET latitude = NULL, longitude = NULL, home_place_id = NULL`); err != nil {
		t.Fatal(err)
	}
}

// setHomeInSettings picks Pasing from the list of places under Settings →
// Household and saves it.
func (f *familyBox) setHomeInSettings(t *testing.T) {
	t.Helper()
	form := url.Values{"time_format": {"24h"}, "temperature_unit": {"c"}, "home_name": {"Pasing"}}
	if r := f.do(http.MethodPost, "/settings/household", form, en); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `name="place_id" value="`+idPasing+`"`) {
		t.Fatalf("suggestion step = %d", r.status)
	}
	form.Set("place_id", idPasing)
	if r := f.do(http.MethodPost, "/settings/household", form, en); r.status != http.StatusOK || !strings.Contains(r.body, "Chosen. Used for the weather and the sun times.") {
		t.Fatalf("pick step = %d", r.status)
	}
	save := url.Values{"time_format": {"24h"}, "temperature_unit": {"c"}, "place_id": {idPasing}}
	if r := f.post("/settings/household", save); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
}

// #29 acceptance criterion 2: given no light sensor, when the sun sets at the
// home location, then the front light is switched off (fake FreeKiosk API).
func TestFrontLightFollowsSunsetAtTheHomeLocationSetInSettings(t *testing.T) {
	f := newFamilyBox(t, "eink_bw")
	f.withoutLocation(t)
	if r := f.saveSettings(t, url.Values{"kiosk_url": {"http://192.168.1.20:8080"}, "kiosk_key": {"k-1"}}); r.status != http.StatusSeeOther {
		t.Fatalf("kiosk settings = %d", r.status)
	}
	fake := &fakeKiosk{}
	run := server.NewKioskRunner(server.KioskOptions{DB: f.db, Factory: fake.factory(), Now: func() time.Time { return f.now }})
	ctx := context.Background()

	f.now = f.at(14, 17, 30) // after sunset in Munich, but the box does not know where home is
	run.Tick(ctx)
	if fake.count() != 0 {
		t.Fatal("front light switched off without a home location")
	}

	f.setHomeInSettings(t)
	f.now = f.at(14, 16, 0) // sunset in Pasing is about 16:36
	run.Tick(ctx)
	if fake.count() != 0 {
		t.Fatal("front light switched off before sunset")
	}
	f.now = f.at(14, 16, 50)
	run.Tick(ctx)
	if fake.count() != 1 || fake.calls[0] != "http://192.168.1.20:8080 k-1" {
		t.Fatalf("calls = %q, want the front light off once after sunset", fake.calls)
	}
}

// #52: the theme switch by sunrise and sunset works on a box whose home
// location was set in the web interface.
func TestAutomaticThemeFollowsSunsetAtTheHomeLocationSetInSettings(t *testing.T) {
	f := newFamilyBox(t, "screen")
	f.withoutLocation(t)
	f.setTheme(t, "auto", "0", "-30")
	set := sun.On(time.Date(2026, 6, 21, 12, 0, 0, 0, berlinTZ), 48.1415, 11.456).Set
	if got := themeOf(t, f.view(t, "en", set.Add(-30*time.Minute))); got != "light" {
		t.Fatalf("without a home location the theme is %q, want the light default", got)
	}
	f.setHomeInSettings(t)
	if got := themeOf(t, f.view(t, "en", set.Add(-30*time.Minute))); got != "dark" {
		t.Errorf("30 minutes before sunset at home = %q, want dark", got)
	}
	if got := themeOf(t, f.view(t, "en", set.Add(-31*time.Minute))); got != "light" {
		t.Errorf("31 minutes before sunset at home = %q, want light", got)
	}
	// The web interface's automatic theme gets the same sun times.
	page := f.get("/").body
	if !strings.Contains(page, `data-sun-set="`) {
		t.Errorf("web page carries no sun times for the stored location:\n%s", page)
	}
}

// storedHome gives the weather service the household's stored home location,
// as the box's main does.
type storedHome struct{ db *store.DB }

func (h storedHome) Home() (lat, lon float64, err error) {
	hh, err := h.db.HomeHousehold()
	return hh.Latitude, hh.Longitude, err
}

// #28: the weather is fetched for the stored coordinates of the place picked
// from the list.
func TestWeatherUsesTheStoredHomeLocation(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.withoutLocation(t)
	f.setHomeInSettings(t)
	if err := f.weather.Keep(context.Background(), storedHome{f.db}); err != nil {
		t.Fatalf("keep: %v", err)
	}
	if r, ok := f.weather.Latest(48.1415, 11.456); !ok || r.TempC != 18.4 || r.Icon != weather.Clear {
		t.Errorf("reading for the stored location = %v %v", r, ok)
	}
	if chip := chipRe.FindString(f.view(t, "en", f.at(14, 8, 0))); !strings.Contains(chip, "18°C") {
		t.Errorf("chip = %q", chip)
	}
}
