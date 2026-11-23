package unit_test

import (
	"math"
	"reflect"
	"testing"

	"github.com/hejmo-home/hejmo/internal/unit"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9*math.Max(1, math.Abs(b)) }

func TestEveryUnitHasAKindAndASize(t *testing.T) {
	kinds := map[string]unit.Kind{
		"piece": unit.Count, "dozen": unit.Count, "pack": unit.Pack, "bunch": unit.Bunch,
		"g": unit.Weight, "kg": unit.Weight, "oz": unit.Weight, "lb": unit.Weight,
		"ml": unit.Volume, "l": unit.Volume, "floz_us": unit.Volume, "floz_uk": unit.Volume, "cup_us": unit.Volume,
		"pint_us": unit.Volume, "pint_uk": unit.Volume, "quart_us": unit.Volume, "gallon_us": unit.Volume, "gallon_uk": unit.Volume,
	}
	for _, k := range unit.All {
		if unit.KindOf(k) != kinds[k] {
			t.Errorf("%s has kind %q, want %q", k, unit.KindOf(k), kinds[k])
		}
		if unit.ToBase(1, k) <= 0 {
			t.Errorf("%s has no size", k)
		}
	}
	if unit.KindOf("stone") != "" {
		t.Error("an unknown unit has no kind")
	}
}

func TestTheSizesOfTheUnits(t *testing.T) {
	for _, c := range []struct {
		key  string
		base float64
	}{
		{"dozen", 12}, {"kg", 1000}, {"l", 1000}, {"oz", 28.349523125}, {"lb", 453.59237},
		{"floz_us", 29.5735295625}, {"floz_uk", 28.4130625}, {"cup_us", 236.5882365},
		{"pint_us", 473.176473}, {"pint_uk", 568.26125}, {"quart_us", 946.352946}, {"gallon_us", 3785.411784}, {"gallon_uk", 4546.09},
	} {
		if got := unit.ToBase(1, c.key); !near(got, c.base) {
			t.Errorf("1 %s = %v, want %v", c.key, got, c.base)
		}
	}
	if !near(unit.FromBase(1500, "kg"), 1.5) {
		t.Error("1500 g are 1.5 kg")
	}
}

func TestUnitsFollowTheCountryOfTheHome(t *testing.T) {
	metric := []string{"piece", "dozen", "pack", "bunch", "g", "kg", "ml", "l"}
	for _, c := range []string{"DE", "AT", "CH", "FR", "", "XX"} {
		if got := unit.Preset(c); !reflect.DeepEqual(got, metric) {
			t.Errorf("%q: %v", c, got)
		}
	}
	if got, want := unit.Preset("GB"), []string{"piece", "dozen", "pack", "bunch", "g", "kg", "ml", "l", "oz", "lb", "floz_uk", "pint_uk", "gallon_uk"}; !reflect.DeepEqual(got, want) {
		t.Errorf("GB: %v", got)
	}
	if got, want := unit.Preset("US"), []string{"piece", "dozen", "pack", "bunch", "oz", "lb", "floz_us", "cup_us", "pint_us", "quart_us", "gallon_us"}; !reflect.DeepEqual(got, want) {
		t.Errorf("US: %v", got)
	}
}

func TestAtLeastOneUnitPerKindStaysOnAndPieceAlways(t *testing.T) {
	ok := []string{"piece", "g", "l"}
	if g, err := unit.CheckActive(ok); err != nil || g != "" {
		t.Errorf("one per kind: %q, %v", g, err)
	}
	if g, err := unit.CheckActive([]string{"piece", "l"}); err == nil || g != unit.GroupWeight {
		t.Errorf("no weight unit: %q, %v", g, err)
	}
	if g, err := unit.CheckActive([]string{"piece", "g"}); err == nil || g != unit.GroupVolume {
		t.Errorf("no volume unit: %q, %v", g, err)
	}
	if got := unit.Normalize([]string{"l", "g", "bogus", "g"}); !reflect.DeepEqual(got, []string{"piece", "g", "l"}) {
		t.Errorf("normalised = %v (piece is always on, unknown units go, the catalogue order stays)", got)
	}
	if unit.GroupOf("dozen") != unit.GroupCount || unit.GroupOf("oz") != unit.GroupWeight || unit.GroupOf("cup_us") != unit.GroupVolume {
		t.Error("groups")
	}
}

func TestADefaultUnitThatIsOffGivesWayToTheNearestSizeOfItsKind(t *testing.T) {
	for _, c := range []struct {
		name             string
		defaults, active []string
		want             []string
	}{
		{"both on", []string{"dozen", "piece"}, unit.DefaultActive, []string{"dozen", "piece"}},
		{"kg off, lb on (US)", []string{"g", "kg"}, unit.Preset("US"), []string{"oz", "lb"}},
		{"l and ml in the US", []string{"l", "ml"}, unit.Preset("US"), []string{"quart_us", "floz_us"}},
		{"pack off has no stand-in", []string{"pack", "g"}, []string{"piece", "g", "kg", "ml", "l"}, []string{"g"}},
		{"nothing left offers piece", []string{"pack"}, []string{"piece", "g", "l"}, []string{"piece"}},
	} {
		if got := unit.Effective(c.defaults, c.active); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTheTotalIsShownInTheFirstDefaultUnitThatFits(t *testing.T) {
	act := unit.DefaultActive
	for _, c := range []struct {
		name     string
		base     float64
		kind     unit.Kind
		defaults []string
		want     string
	}{
		{"1500 g of flour", 1500, unit.Weight, []string{"g", "kg"}, "kg"},
		{"500 g of flour", 500, unit.Weight, []string{"g", "kg"}, "g"},
		{"999 g is 999 g", 999, unit.Weight, []string{"g", "kg"}, "g"},
		{"1000 g of flour", 1000, unit.Weight, []string{"g", "kg"}, "kg"},
		{"0.75 l of milk", 750, unit.Volume, []string{"l", "ml"}, "ml"},
		{"3 l of milk", 3000, unit.Volume, []string{"l", "ml"}, "l"},
		{"18 eggs are 18 eggs", 18, unit.Count, []string{"dozen", "piece"}, "piece"},
		{"24 eggs are 2 dozen", 24, unit.Count, []string{"dozen", "piece"}, "dozen"},
		{"6 eggs", 6, unit.Count, []string{"dozen", "piece"}, "piece"},
		{"text counts in pieces", 18, unit.Count, []string{"piece", "pack"}, "piece"},
		{"500 g where no default is a weight: the largest unit of at least 1", 500, unit.Weight, []string{"l", "ml"}, "g"},
		{"1500 g where no default is a weight", 1500, unit.Weight, []string{"l"}, "kg"},
		{"2 packs", 2, unit.Pack, []string{"pack", "g"}, "pack"},
		{"1.5 packs are no whole pack: still packs", 1.5, unit.Pack, []string{"pack", "g"}, "pack"},
		{"bunch", 3, unit.Bunch, nil, "bunch"},
	} {
		if got := unit.ShowUnit(c.base, c.kind, c.defaults, act); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	if got := unit.ShowUnit(1666, unit.Weight, []string{"g", "kg"}, act); got != "kg" {
		t.Errorf("1666 g: %s", got)
	}
	if got := unit.ShowUnit(5, unit.Pack, nil, []string{"piece", "g", "l"}); got != "pack" {
		t.Errorf("a pack entry stays in packs even when packs are switched off: %s", got)
	}
}

func TestFiguresAreRoundedForDisplayOnly(t *testing.T) {
	for _, c := range []struct {
		amount float64
		unit   string
		lang   string
		want   string
	}{
		{1.5, "kg", "de", "1,5"}, {1.5, "kg", "en", "1.5"}, {1.666, "kg", "de", "1,67"}, {1.005, "kg", "en", "1.01"},
		{1.0, "kg", "de", "1"}, {2.50, "l", "de", "2,5"}, {0.125, "l", "en", "0.13"}, {750.4, "ml", "de", "750"}, {499.5, "g", "de", "500"},
		{1000, "g", "en", "1000"}, {12, "piece", "de", "12"}, {0.33333, "pack", "en", "0.33"},
	} {
		if got := unit.Format(c.amount, c.unit, c.lang); got != c.want {
			t.Errorf("Format(%v %s, %s) = %q, want %q", c.amount, c.unit, c.lang, got, c.want)
		}
	}
}

func TestAnAmountTakesCommaOrPointAndMustBeBetween0And9999(t *testing.T) {
	for in, want := range map[string]float64{"2": 2, "0,5": 0.5, "0.5": 0.5, " 12 ": 12, "9999": 9999, "1,25": 1.25, "10": 10} {
		got, err := unit.Parse(in)
		if err != nil || !near(got, want) {
			t.Errorf("Parse(%q) = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "0", "0,0", "-1", "abc", "1e3", "10000", "9999,5", "1,2,3", "1.000,5", "NaN", "Inf", ",5", "2 kg"} {
		if _, err := unit.Parse(in); err == nil {
			t.Errorf("Parse(%q) should fail", in)
		}
	}
}

func TestStepsGoToTheNextWholeStep(t *testing.T) {
	for _, c := range []struct {
		amount float64
		unit   string
		up     float64
		down   float64
		canDn  bool
	}{
		{1, "piece", 2, 0, false}, {0.5, "kg", 1, 0, false}, {1.5, "kg", 2, 1, true}, {2, "kg", 3, 1, true},
		{500, "g", 550, 450, true}, {50, "g", 100, 0, false}, {30, "ml", 50, 0, false}, {525, "ml", 550, 500, true},
		{9999, "piece", 9999, 9998, true},
	} {
		if got := unit.StepUp(c.amount, c.unit); !near(got, c.up) {
			t.Errorf("StepUp(%v %s) = %v, want %v", c.amount, c.unit, got, c.up)
		}
		got, ok := unit.StepDown(c.amount, c.unit)
		if ok != c.canDn || (ok && !near(got, c.down)) {
			t.Errorf("StepDown(%v %s) = %v, %v; want %v, %v", c.amount, c.unit, got, ok, c.down, c.canDn)
		}
	}
}

func TestSumsAreExact(t *testing.T) {
	sum := 3*unit.ToBase(333, "g") + 0
	if unit.Format(unit.FromBase(sum, "g"), "g", "de") != "999" {
		t.Error("3 x 333 g = 999 g")
	}
	if got := unit.FromBase(2*unit.ToBase(333, "g")+unit.ToBase(1, "kg"), "kg"); unit.Format(got, "kg", "de") != "1,67" {
		t.Errorf("2 x 333 g + 1 kg = %v kg", got)
	}
}
