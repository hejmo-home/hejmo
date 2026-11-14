package server_test

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/weather"
)

// homeWeather gives the home its coordinates and fetches Open-Meteo's answer.
func (f *familyBox) homeWeather(t *testing.T) {
	t.Helper()
	if err := f.db.SetHouseholdCoordinates(f.householdID, 48.15, 11.46); err != nil {
		t.Fatal(err)
	}
	if err := f.weather.Refresh(context.Background(), 48.15, 11.46); err != nil {
		t.Fatal(err)
	}
}

var chipRe = regexp.MustCompile(`(?s)<span class="chip" data-weather[^>]*>.*?</span>\s*</span>|<span class="chip" data-weather[^>]*>.*?</span>`)

// 5.8: Given an Open-Meteo answer, the chip shows temperature and icon.
func TestWeatherChipShowsTemperatureAndIcon(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	if p := f.view(t, "en", f.at(14, 8, 0)); strings.Contains(p, "data-weather") {
		t.Fatal("weather chip before there is a reading")
	}
	f.homeWeather(t)
	p := f.view(t, "en", f.at(14, 8, 0))
	chip := chipRe.FindString(p)
	if !strings.Contains(chip, "18°C") || !strings.Contains(chip, `data-icon="clear"`) || !strings.Contains(chip, "<svg") {
		t.Fatalf("chip = %q\n%s", chip, p)
	}
	if !strings.Contains(chip, `aria-label="Clear"`) && !strings.Contains(chip, "Clear") {
		t.Errorf("icon has no text alternative: %q", chip)
	}
	if de := chipRe.FindString(f.view(t, "de", f.at(14, 8, 0))); !strings.Contains(de, "Klar") {
		t.Errorf("German chip = %q", de)
	}
}

// ADR 0016: temperature unit and time format of the household apply on the display.
func TestHeaderFollowsTheHouseholdSettings(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.homeWeather(t)
	f.event(t, "Dentist", f.at(14, 15, 0), f.at(14, 15, 45), false, "Mama")
	if r := f.post("/settings", url.Values{"time_format": {"12h"}, "temperature_unit": {"f"}}); r.status != 303 {
		t.Fatalf("settings = %d", r.status)
	}
	p := f.view(t, "en", f.at(14, 13, 5))
	for _, want := range []string{"65°F", "data-clock>1:05 PM<", "3:00 PM–3:45 PM"} {
		if !strings.Contains(p, want) {
			t.Errorf("page misses %q", want)
		}
	}
}

// 5.8: Given Open-Meteo unreachable, the chip shows the last value or hides, without errors.
func TestWeatherChipKeepsTheLastValueThenHidesWhenOpenMeteoIsUnreachable(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.homeWeather(t)
	f.meteo.set("oops", 503)
	if err := f.weather.Refresh(context.Background(), 48.15, 11.46); err == nil {
		t.Fatal("failure not reported")
	}
	r := f.display.get("/display", "en")
	if r.status != 200 || !strings.Contains(r.body, "18°C") || strings.Contains(strings.ToLower(r.body), "error") {
		t.Fatalf("display with the last value = %d\n%s", r.status, r.body)
	}
	f.now = f.now.Add(weather.MaxAge + time.Minute)
	r = f.display.get("/display", "en")
	if r.status != 200 || strings.Contains(r.body, "data-weather") || strings.Contains(strings.ToLower(r.body), "error") {
		t.Fatalf("display after the value went stale = %d\n%s", r.status, r.body)
	}
	if !strings.Contains(r.body, "data-clock") {
		t.Error("clock chip missing")
	}
}

// 5.8: the display never calls outside: the page carries no external address.
func TestFamilyViewNamesNoExternalAddress(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.homeWeather(t)
	p := f.view(t, "en", f.at(14, 8, 0))
	if m := regexp.MustCompile(`(?i)(https?:)?//[a-z0-9.-]+\.[a-z]{2,}|open-meteo`).FindString(strings.ReplaceAll(p, "http://www.w3.org/2000/svg", "")); m != "" {
		t.Fatalf("page names an external address %q", m)
	}
}

// The display asks the box once a minute for the clock and weather chips; no page reload.
func TestDisplayStateCarriesTheChipsForTheMinuteUpdate(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.now = f.at(14, 8, 7)
	f.homeWeather(t)
	body := f.display.get("/display/state", "en").body
	for _, want := range []string{`"clock":"08:07"`, `"weather":"18°C"`, `"state":"paired"`} {
		if !strings.Contains(body, want) {
			t.Errorf("state misses %s: %s", want, body)
		}
	}
	f.now = f.now.Add(weather.MaxAge + time.Minute)
	if body := f.display.get("/display/state", "en").body; strings.Contains(body, `"weather"`) {
		t.Errorf("stale weather in state: %s", body)
	}
}
