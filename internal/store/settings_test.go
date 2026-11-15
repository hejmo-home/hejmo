package store_test

import (
	"errors"
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
	if err := db.SaveHouseholdSettings(h.ID, store.Time12h, store.Fahrenheit, store.UpdateNotify, good, true, []string{"tasks", "family"}); err != nil {
		t.Fatal(err)
	}
	got, _ := db.Household(h.ID)
	if got.TimeFormat != store.Time12h || got.TemperatureUnit != store.Fahrenheit || got.AutoUpdate != store.UpdateNotify || got.MealCutoffs.Breakfast != "10:00" || !got.FirstStepsHidden || strings.Join(got.FirstStepsSkipped, ",") != "family,tasks" {
		t.Fatalf("not all stored: %+v", got)
	}
	bad := meal.Cutoffs{Breakfast: "25:99"}
	if err := db.SaveHouseholdSettings(h.ID, store.Time24h, store.Celsius, store.UpdateOn, bad, false, nil); err == nil {
		t.Fatal("invalid cut-off accepted")
	}
	if again, _ := db.Household(h.ID); again.TimeFormat != store.Time12h || again.MealCutoffs.Breakfast != "10:00" || !again.FirstStepsHidden || len(again.FirstStepsSkipped) != 2 {
		t.Fatalf("a refused save changed something: %+v", again)
	}
	if err := db.SaveHouseholdSettings("missing", store.Time24h, store.Celsius, store.UpdateOn, good, false, nil); !errors.Is(err, store.ErrNotFound) {
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
	if err := db.SaveHouseholdSettings(h.ID, store.Time24h, store.Celsius, store.UpdateOn, meal.Cutoffs{}, false, []string{"bogus"}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("a save with an unknown step: %v", err)
	}
	if want := []string{"family", "parents", "calendars", "tasks", "meals", "display"}; strings.Join(store.FirstStepIDs, ",") != strings.Join(want, ",") {
		t.Fatalf("steps = %v", store.FirstStepIDs)
	}
}
