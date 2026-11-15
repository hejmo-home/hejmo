package store_test

import (
	"errors"
	"testing"

	"github.com/hejmo-home/hejmo/internal/meal"
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

// Ticket 133: the settings, the cut-offs and the first steps flag are saved in
// one statement, so a refused save changes none of them.
func TestSaveHouseholdSettingsIsAllOrNothing(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	good := meal.Cutoffs{Breakfast: "10:00"}
	if err := db.SaveHouseholdSettings(h.ID, store.Time12h, store.Fahrenheit, store.UpdateNotify, good, true); err != nil {
		t.Fatal(err)
	}
	got, _ := db.Household(h.ID)
	if got.TimeFormat != store.Time12h || got.TemperatureUnit != store.Fahrenheit || got.AutoUpdate != store.UpdateNotify || got.MealCutoffs.Breakfast != "10:00" || !got.FirstStepsHidden {
		t.Fatalf("not all stored: %+v", got)
	}
	bad := meal.Cutoffs{Breakfast: "25:99"}
	if err := db.SaveHouseholdSettings(h.ID, store.Time24h, store.Celsius, store.UpdateOn, bad, false); err == nil {
		t.Fatal("invalid cut-off accepted")
	}
	if again, _ := db.Household(h.ID); again.TimeFormat != store.Time12h || again.MealCutoffs.Breakfast != "10:00" || !again.FirstStepsHidden {
		t.Fatalf("a refused save changed something: %+v", again)
	}
	if err := db.SaveHouseholdSettings("missing", store.Time24h, store.Celsius, store.UpdateOn, good, false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown household: %v", err)
	}
}
