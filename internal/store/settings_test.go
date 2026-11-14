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
	if err != nil || got.TimeFormat != store.Time24h || got.TemperatureUnit != store.Celsius {
		t.Fatalf("defaults = %+v, %v", got, err)
	}
}

func TestHouseholdSettingsPersistAndRejectUnknownValues(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	if err := db.UpdateHouseholdSettings(h.ID, store.Time12h, store.Fahrenheit); err != nil {
		t.Fatal(err)
	}
	got, _ := db.HomeHousehold()
	if got.TimeFormat != store.Time12h || got.TemperatureUnit != store.Fahrenheit {
		t.Fatalf("saved = %+v", got)
	}
	if err := db.UpdateHouseholdSettings(h.ID, "25h", store.Celsius); err == nil {
		t.Fatal("unknown time format accepted")
	}
	if err := db.UpdateHouseholdSettings(h.ID, store.Time24h, "kelvin"); err == nil {
		t.Fatal("unknown unit accepted")
	}
	if err := db.UpdateHouseholdSettings("missing", store.Time24h, store.Celsius); err == nil {
		t.Fatal("unknown household accepted")
	}
}
