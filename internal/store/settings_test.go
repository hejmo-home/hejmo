package store_test

import (
	"math"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

// ADR 0016: household settings offer time format and temperature unit.
func TestHouseholdDefaultsTo24HourAndCelsius(t *testing.T) {
	db, _ := openTemp(t)
	h, err := db.CreateHousehold(store.Household{Name: "Family"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.Household(h.ID)
	if err != nil || got.TimeFormat != store.Time24h || got.TemperatureUnit != store.Celsius {
		t.Fatalf("defaults = %+v, %v", got, err)
	}
}

func TestHouseholdSettingsPersistAndRejectUnknownValues(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	if err := db.UpdateHouseholdSettings(h.ID, store.Time12h, store.Fahrenheit, store.UpdateNotify); err != nil {
		t.Fatal(err)
	}
	got, _ := db.HomeHousehold()
	if got.TimeFormat != store.Time12h || got.TemperatureUnit != store.Fahrenheit || got.AutoUpdate != store.UpdateNotify {
		t.Fatalf("saved = %+v", got)
	}
	if err := db.UpdateHouseholdSettings(h.ID, "25h", store.Celsius, store.UpdateOn); err == nil {
		t.Fatal("unknown time format accepted")
	}
	if err := db.UpdateHouseholdSettings(h.ID, store.Time24h, "kelvin", store.UpdateOn); err == nil {
		t.Fatal("unknown unit accepted")
	}
	if err := db.UpdateHouseholdSettings("missing", store.Time24h, store.Celsius, store.UpdateOn); err == nil {
		t.Fatal("unknown household accepted")
	}
}

// A settings save is all or nothing: a bad update mode must not leave the
// time format and unit half saved, and the other way round.
func TestHouseholdSettingsSaveIsAllOrNothing(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	if err := db.UpdateHouseholdSettings(h.ID, store.Time12h, store.Fahrenheit, "sometimes"); err == nil {
		t.Fatal("unknown update mode accepted")
	}
	got, _ := db.Household(h.ID)
	if got.TimeFormat != store.Time24h || got.TemperatureUnit != store.Celsius || got.AutoUpdate != store.UpdateOn {
		t.Fatalf("a rejected save changed the settings: %+v", got)
	}
}

// Q59: the home place and its coordinates are saved with the settings, all or nothing.
func TestHouseholdHomeIsSavedWithTheSettingsAndRefusesBadCoordinates(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family", HomeName: "Pasing"})
	home := store.Home{Name: "München-Pasing", Latitude: 48.1486, Longitude: 11.4558}
	if err := db.UpdateHouseholdSettingsAndHome(h.ID, store.Time12h, store.Fahrenheit, store.UpdateNotify, home); err != nil {
		t.Fatal(err)
	}
	got, _ := db.HomeHousehold()
	if got.HomeName != "München-Pasing" || got.Latitude != 48.1486 || got.Longitude != 11.4558 || got.TimeFormat != store.Time12h {
		t.Fatalf("saved = %+v", got)
	}
	nan := math.NaN()
	for _, bad := range []store.Home{
		{Name: "x", Latitude: 90.1, Longitude: 0}, {Name: "x", Latitude: -91, Longitude: 0},
		{Name: "x", Latitude: 0, Longitude: 180.1}, {Name: "x", Latitude: nan, Longitude: 0}, {Name: " ", Latitude: 1, Longitude: 1},
	} {
		if err := db.UpdateHouseholdSettingsAndHome(h.ID, store.Time24h, store.Celsius, store.UpdateOn, bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if got, _ := db.HomeHousehold(); got.TimeFormat != store.Time12h || got.Latitude != 48.1486 {
		t.Errorf("a refused save changed the household: %+v", got)
	}
}
