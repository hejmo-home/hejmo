package unit_test

import (
	"reflect"
	"testing"

	"github.com/hejmo-home/hejmo/internal/unit"
)

func TestTheUnitsOfADR0019(t *testing.T) {
	want := []string{"piece", "dozen", "pack", "bunch", "g", "kg", "ml", "l", "oz", "lb", "floz_us", "floz_uk", "cup_us", "pint_us", "pint_uk", "quart_us", "gallon_us", "gallon_uk"}
	if !reflect.DeepEqual(unit.All, want) {
		t.Errorf("units = %v", unit.All)
	}
	if !unit.Valid("kg") || unit.Valid("stone") || unit.Valid("") {
		t.Error("Valid knows the units of the catalogue only")
	}
}

func TestMostCountriesStartWithMetricAndTheCountingUnits(t *testing.T) {
	want := []string{"piece", "dozen", "pack", "bunch", "g", "kg", "ml", "l"}
	if got := unit.DefaultActive; !reflect.DeepEqual(got, want) {
		t.Errorf("default active = %v", got)
	}
}
