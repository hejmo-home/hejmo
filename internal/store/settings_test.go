package store_test

import (
	"errors"
	"math"
	"strings"
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
	if err != nil || got.DateFormat != store.DateDMY || got.TimeFormat != store.Time24h || got.TemperatureUnit != store.Celsius {
		t.Fatalf("defaults = %+v, %v", got, err)
	}
}

func TestHouseholdSettingsPersistAndRejectUnknownValues(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	if err := db.SaveHouseholdSettings(h.ID, settings(store.Time12h, store.Fahrenheit, store.UpdateNotify)); err != nil {
		t.Fatal(err)
	}
	got, _ := db.HomeHousehold()
	if got.DateFormat != store.DateMDY || got.TimeFormat != store.Time12h || got.TemperatureUnit != store.Fahrenheit || got.AutoUpdate != store.UpdateNotify {
		t.Fatalf("saved = %+v", got)
	}
	for name, in := range map[string]store.HouseholdSettings{
		"time format": settings("25h", store.Celsius, store.UpdateOn),
		"unit":        settings(store.Time24h, "kelvin", store.UpdateOn),
		"mode":        settings(store.Time24h, store.Celsius, "sometimes"),
		"date format": func() store.HouseholdSettings {
			in := settings(store.Time24h, store.Celsius, store.UpdateOn)
			in.DateFormat = "ymd"
			return in
		}(),
	} {
		if err := db.SaveHouseholdSettings(h.ID, in); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("unknown %s: %v, want ErrInvalid", name, err)
		}
	}
	if err := db.SaveHouseholdSettings("missing", settings(store.Time24h, store.Celsius, store.UpdateOn)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown household: %v", err)
	}
}

func settings(f store.TimeFormat, u store.TemperatureUnit, a store.UpdateMode) store.HouseholdSettings {
	return store.HouseholdSettings{DateFormat: store.DateMDY, TimeFormat: f, TemperatureUnit: u, AutoUpdate: a, MealCutoffs: meal.DefaultCutoffs()}
}

// A settings save is all or nothing: a bad update mode must not leave the
// time format and unit half saved, and the other way round.
func TestHouseholdSettingsSaveIsAllOrNothing(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	if err := db.SaveHouseholdSettings(h.ID, settings(store.Time12h, store.Fahrenheit, "sometimes")); err == nil {
		t.Fatal("unknown update mode accepted")
	}
	got, _ := db.Household(h.ID)
	if got.DateFormat != store.DateDMY || got.TimeFormat != store.Time24h || got.TemperatureUnit != store.Celsius || got.AutoUpdate != store.UpdateOn {
		t.Fatalf("a rejected save changed the settings: %+v", got)
	}
}

// A save that does not carry the first steps state leaves it alone, so a skip
// or hide made by another parent in the meantime is not undone.
func TestSaveHouseholdSettingsLeavesTheFirstStepsAloneWhenNotGiven(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	if err := db.SetFirstStepSkipped(h.ID, "tasks", true); err != nil {
		t.Fatal(err)
	}
	if err := db.SetFirstStepsHidden(h.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveHouseholdSettings(h.ID, settings(store.Time12h, store.Celsius, store.UpdateOn)); err != nil {
		t.Fatal(err)
	}
	got, _ := db.Household(h.ID)
	if !got.FirstStepsHidden || strings.Join(got.FirstStepsSkipped, ",") != "tasks" || got.TimeFormat != store.Time12h {
		t.Fatalf("first steps changed by a save without them: %+v", got)
	}
	no, none := false, []string{}
	in := settings(store.Time12h, store.Celsius, store.UpdateOn)
	in.FirstStepsHidden, in.FirstStepsSkipped = &no, &none
	if err := db.SaveHouseholdSettings(h.ID, in); err != nil {
		t.Fatal(err)
	}
	if got, _ = db.Household(h.ID); got.FirstStepsHidden || len(got.FirstStepsSkipped) != 0 {
		t.Fatalf("given values not stored: %+v", got)
	}
}

// Ticket 133: the settings, the cut-offs and the first steps flag are saved in
// one statement, so a refused save changes none of them.
func TestSaveHouseholdSettingsIsAllOrNothing(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	good := meal.Cutoffs{Breakfast: "10:00"}
	yes, steps := true, []string{"tasks", "family"}
	full := store.HouseholdSettings{DateFormat: store.DateDMY, TimeFormat: store.Time12h, TemperatureUnit: store.Fahrenheit, AutoUpdate: store.UpdateNotify, MealCutoffs: good, FirstStepsHidden: &yes, FirstStepsSkipped: &steps}
	if err := db.SaveHouseholdSettings(h.ID, full); err != nil {
		t.Fatal(err)
	}
	got, _ := db.Household(h.ID)
	if got.TimeFormat != store.Time12h || got.TemperatureUnit != store.Fahrenheit || got.AutoUpdate != store.UpdateNotify || got.MealCutoffs.Breakfast != "10:00" || !got.FirstStepsHidden || strings.Join(got.FirstStepsSkipped, ",") != "family,tasks" {
		t.Fatalf("not all stored: %+v", got)
	}
	bad := meal.Cutoffs{Breakfast: "25:99"}
	badIn := settings(store.Time24h, store.Celsius, store.UpdateOn)
	badIn.MealCutoffs = bad
	if err := db.SaveHouseholdSettings(h.ID, badIn); err == nil {
		t.Fatal("invalid cut-off accepted")
	}
	if again, _ := db.Household(h.ID); again.TimeFormat != store.Time12h || again.MealCutoffs.Breakfast != "10:00" || !again.FirstStepsHidden || len(again.FirstStepsSkipped) != 2 {
		t.Fatalf("a refused save changed something: %+v", again)
	}
	if err := db.SaveHouseholdSettings("missing", full); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown household: %v", err)
	}
}

// Ticket 133, design pass 2: a step of the first steps card can be skipped and
// taken back; the household keeps the skipped steps in the order of the card,
// and an unknown step is refused.
func TestFirstStepsCanBeSkippedAndTakenBack(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	if got, _ := db.Household(h.ID); len(got.FirstStepsSkipped) != 0 {
		t.Fatalf("steps skipped by default: %v", got.FirstStepsSkipped)
	}
	for _, step := range []string{"display", "family", "display"} { // skipping twice changes nothing
		if err := db.SetFirstStepSkipped(h.ID, step, true); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := db.HomeHousehold(); strings.Join(got.FirstStepsSkipped, ",") != "family,display" {
		t.Fatalf("skipped = %v, want family,display", got.FirstStepsSkipped)
	}
	if err := db.SetFirstStepSkipped(h.ID, "family", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.Household(h.ID); strings.Join(got.FirstStepsSkipped, ",") != "display" {
		t.Fatalf("after taking one back: %v", got.FirstStepsSkipped)
	}
	if err := db.SetFirstStepSkipped(h.ID, "nonsense", true); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("unknown step: %v", err)
	}
	if err := db.SetFirstStepSkipped("missing", "family", true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown household: %v", err)
	}
	if err := db.SaveHouseholdSettings(h.ID, withSteps(settings(store.Time24h, store.Celsius, store.UpdateOn), "bogus")); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("a save with an unknown step: %v", err)
	}
	if want := []string{"family", "parents", "calendars", "tasks", "meals", "display"}; strings.Join(store.FirstStepIDs, ",") != strings.Join(want, ",") {
		t.Fatalf("steps = %v", store.FirstStepIDs)
	}
}

func withSteps(in store.HouseholdSettings, steps ...string) store.HouseholdSettings {
	in.FirstStepsSkipped = &steps
	return in
}

// An unknown first step comes back as a typed error that names the step, so
// the API builds its message from it in one place.
func TestSaveHouseholdSettingsNamesTheUnknownFirstStep(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	in := settings(store.Time24h, store.Celsius, store.UpdateOn)
	in.FirstStepsSkipped = &[]string{"family", "nonsense"}
	err := db.SaveHouseholdSettings(h.ID, in)
	var unknown *store.UnknownStepError
	if !errors.As(err, &unknown) || unknown.Step != "nonsense" || !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("err = %v, want *UnknownStepError for nonsense that is ErrInvalid", err)
	}
}

// Q59: the home place and its coordinates are saved with the settings, all or nothing.
func TestHouseholdHomeIsSavedWithTheSettingsAndRefusesBadCoordinates(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family", HomeName: "Pasing"})
	in := settings(store.Time12h, store.Fahrenheit, store.UpdateNotify)
	in.Home = &store.Home{Name: "München-Pasing", Latitude: 48.1486, Longitude: 11.4558}
	if err := db.SaveHouseholdSettings(h.ID, in); err != nil {
		t.Fatal(err)
	}
	got, _ := db.HomeHousehold()
	if got.HomeName != "München-Pasing" || got.Latitude != 48.1486 || got.Longitude != 11.4558 || got.TimeFormat != store.Time12h {
		t.Fatalf("saved = %+v", got)
	}
	// A save without a home leaves the stored home alone.
	if err := db.SaveHouseholdSettings(h.ID, settings(store.Time24h, store.Celsius, store.UpdateOn)); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.HomeHousehold(); got.HomeName != "München-Pasing" || got.Latitude != 48.1486 || got.TimeFormat != store.Time24h {
		t.Fatalf("a save without a home changed it: %+v", got)
	}
	nan := math.NaN()
	for _, bad := range []store.Home{
		{Name: "x", Latitude: 90.1, Longitude: 0}, {Name: "x", Latitude: -91, Longitude: 0},
		{Name: "x", Latitude: 0, Longitude: 180.1}, {Name: "x", Latitude: nan, Longitude: 0}, {Name: " ", Latitude: 1, Longitude: 1},
		{Name: "Null Island", Latitude: 0, Longitude: 0}, // (0, 0) means "no location yet"
	} {
		in := settings(store.Time12h, store.Fahrenheit, store.UpdateNotify)
		in.Home = &bad
		if err := db.SaveHouseholdSettings(h.ID, in); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if got, _ := db.HomeHousehold(); got.TimeFormat != store.Time24h || got.Latitude != 48.1486 {
		t.Errorf("a refused save changed the household: %+v", got)
	}
}

// ADR 0022: the home keeps the id of the place it was picked from, so the
// region line can be rebuilt in the page language. Coordinates set without a
// place leave it empty, and a save without a home leaves it alone.
func TestHouseholdHomeKeepsTheIdOfThePickedPlace(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family", HomeName: "München", HomePlaceID: 2867714})
	if got, _ := db.HomeHousehold(); got.HomePlaceID != 2867714 {
		t.Fatalf("created with place id %d", got.HomePlaceID)
	}
	in := settings(store.Time24h, store.Celsius, store.UpdateOn)
	if err := db.SaveHouseholdSettings(h.ID, in); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.HomeHousehold(); got.HomePlaceID != 2867714 {
		t.Errorf("a save without a home changed the place id to %d", got.HomePlaceID)
	}
	in.Home = &store.Home{Name: "Pasing", Latitude: 48.1415, Longitude: 11.456, PlaceID: 2855334}
	if err := db.SaveHouseholdSettings(h.ID, in); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.HomeHousehold(); got.HomePlaceID != 2855334 || got.HomeName != "Pasing" {
		t.Errorf("saved place id %d, name %q", got.HomePlaceID, got.HomeName)
	}
	in.Home = &store.Home{Name: "By API", Latitude: 48.2, Longitude: 11.4}
	if err := db.SaveHouseholdSettings(h.ID, in); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.HomeHousehold(); got.HomePlaceID != 0 {
		t.Errorf("coordinates without a place must clear the place id, got %d", got.HomePlaceID)
	}
}
