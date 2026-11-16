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

func TestADefaultUnitThatIsSwitchedOffIsLeftOutAndPieceIsOfferedWhenNoneIsActive(t *testing.T) {
	active := []string{"piece", "kg", "l"}
	for _, c := range []struct {
		name string
		in   []string
		want []string
	}{
		{"both active", []string{"piece", "kg"}, []string{"piece", "kg"}},
		{"second switched off", []string{"kg", "g"}, []string{"kg"}},
		{"first switched off keeps the second first", []string{"g", "kg"}, []string{"kg"}},
		{"neither active offers piece", []string{"g", "ml"}, []string{"piece"}},
		{"nothing given offers piece", nil, []string{"piece"}},
	} {
		if got := unit.Offered(c.in, active); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPieceIsOfferedEvenWhenPieceItselfIsSwitchedOff(t *testing.T) {
	if got := unit.Offered([]string{"g"}, []string{"kg"}); !reflect.DeepEqual(got, []string{"piece"}) {
		t.Errorf("got %v", got)
	}
}
