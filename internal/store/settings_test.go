package store_test

import (
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
	if err != nil || got.DateFormat != store.DateDMY || got.TimeFormat != store.Time24h || got.TemperatureUnit != store.Celsius {
		t.Fatalf("defaults = %+v, %v", got, err)
	}
}

func TestHouseholdSettingsPersistAndRejectUnknownValues(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	if err := db.UpdateHouseholdSettings(h.ID, store.DateMDY, store.Time12h, store.Fahrenheit, store.UpdateNotify); err != nil {
		t.Fatal(err)
	}
	got, _ := db.HomeHousehold()
	if got.DateFormat != store.DateMDY || got.TimeFormat != store.Time12h || got.TemperatureUnit != store.Fahrenheit || got.AutoUpdate != store.UpdateNotify {
		t.Fatalf("saved = %+v", got)
	}
	if err := db.UpdateHouseholdSettings(h.ID, store.DateMDY, "25h", store.Celsius, store.UpdateOn); err == nil {
		t.Fatal("unknown time format accepted")
	}
	if err := db.UpdateHouseholdSettings(h.ID, store.DateMDY, store.Time24h, "kelvin", store.UpdateOn); err == nil {
		t.Fatal("unknown unit accepted")
	}
	if err := db.UpdateHouseholdSettings(h.ID, "ymd", store.Time24h, store.Celsius, store.UpdateOn); err == nil {
		t.Fatal("unknown date format accepted")
	}
	if err := db.UpdateHouseholdSettings("missing", store.DateDMY, store.Time24h, store.Celsius, store.UpdateOn); err == nil {
		t.Fatal("unknown household accepted")
	}
}

// A settings save is all or nothing: a bad update mode must not leave the
// time format and unit half saved, and the other way round.
func TestHouseholdSettingsSaveIsAllOrNothing(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	if err := db.UpdateHouseholdSettings(h.ID, store.DateMDY, store.Time12h, store.Fahrenheit, "sometimes"); err == nil {
		t.Fatal("unknown update mode accepted")
	}
	got, _ := db.Household(h.ID)
	if got.DateFormat != store.DateDMY || got.TimeFormat != store.Time24h || got.TemperatureUnit != store.Celsius || got.AutoUpdate != store.UpdateOn {
		t.Fatalf("a rejected save changed the settings: %+v", got)
	}
}
