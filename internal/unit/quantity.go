package unit

import (
	"errors"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Kind is what a unit measures. Only units of the same kind convert into each
// other: Hejmo does not know how many grams a pack holds.
type Kind string

// The kinds. Pack and bunch convert into nothing but themselves.
const (
	Count  Kind = "count"  // piece, dozen
	Pack   Kind = "pack"   // pack
	Bunch  Kind = "bunch"  // bunch
	Weight Kind = "weight" // g, kg, oz, lb
	Volume Kind = "volume" // ml, l, fl oz, cup, pint, quart, gallon
)

// Groups are how the settings page sorts the units.
const (
	GroupCount  = "count"
	GroupWeight = "weight"
	GroupVolume = "volume"
)

// MaxAmount is the largest amount that can be typed.
const MaxAmount = 9999

// size is the size of one unit in the base of its kind: pieces, grams or
// millilitres. Pack and bunch count themselves.
var size = map[string]float64{
	"piece": 1, "dozen": 12, "pack": 1, "bunch": 1,
	"g": 1, "kg": 1000, "oz": 28.349523125, "lb": 453.59237,
	"ml": 1, "l": 1000, "floz_us": 29.5735295625, "floz_uk": 28.4130625, "cup_us": 236.5882365,
	"pint_us": 473.176473, "pint_uk": 568.26125, "quart_us": 946.352946, "gallon_us": 3785.411784, "gallon_uk": 4546.09,
}

// KindOf is the kind of a unit, "" for a key that is no unit.
func KindOf(key string) Kind {
	switch key {
	case "piece", "dozen":
		return Count
	case "pack":
		return Pack
	case "bunch":
		return Bunch
	case "g", "kg", "oz", "lb":
		return Weight
	case "ml", "l", "floz_us", "floz_uk", "cup_us", "pint_us", "pint_uk", "quart_us", "gallon_us", "gallon_uk":
		return Volume
	}
	if IsKitchen(key) || IsContainer(key) { // recipes (ticket 279): each of them counts only itself
		return Kind(key)
	}
	return ""
}

// GroupOf is the group of the settings page a unit is listed in.
func GroupOf(key string) string {
	switch KindOf(key) {
	case Weight:
		return GroupWeight
	case Volume:
		return GroupVolume
	}
	return GroupCount
}

// ToBase turns an amount of a unit into pieces, grams or millilitres.
func ToBase(amount float64, key string) float64 { return amount * size[key] }

// FromBase turns pieces, grams or millilitres into an amount of a unit.
func FromBase(base float64, key string) float64 { return base / size[key] }

// Size is the size of one unit in the base of its kind (0 for an unknown key).
func Size(key string) float64 { return size[key] }

// Preset is the units the home's country switches on until a parent changes
// them: metric plus the counting units in most countries, the United Kingdom
// adds its imperial units, the United States use US customary.
func Preset(country string) []string {
	var on []string
	switch strings.ToUpper(country) {
	case "GB":
		on = []string{"piece", "dozen", "pack", "bunch", "g", "kg", "ml", "l", "oz", "lb", "floz_uk", "pint_uk", "gallon_uk"}
	case "US":
		on = []string{"piece", "dozen", "pack", "bunch", "oz", "lb", "floz_us", "cup_us", "pint_us", "quart_us", "gallon_us"}
	default:
		on = DefaultActive
	}
	return slices.Clone(on)
}

// Normalize returns the known units of a choice, once each, in the order of
// the catalogue, with piece always on: free text needs it.
func Normalize(keys []string) []string {
	out := []string{}
	for _, k := range All {
		if k == Piece || slices.Contains(keys, k) {
			out = append(out, k)
		}
	}
	return out
}

// CheckActive says which group has no unit left on, or "" when each of the
// weights and the volumes has one (piece makes sure of the counting units).
func CheckActive(active []string) (group string, err error) {
	for _, g := range []string{GroupWeight, GroupVolume} {
		if !slices.ContainsFunc(active, func(k string) bool { return GroupOf(k) == g && Valid(k) }) {
			return g, errors.New("unit: no " + g + " unit is on")
		}
	}
	return "", nil
}

func sameKind(a, b string) bool { return KindOf(a) != "" && KindOf(a) == KindOf(b) }

// Effective returns the units to offer for an article's default units: a
// default unit that is switched off gives way to the active unit of its kind
// with the nearest size (kg to lb); one without a stand-in (pack, bunch) is
// dropped; when nothing is left it is piece.
func Effective(defaults, active []string) []string {
	out := []string{}
	for _, d := range defaults {
		u := d
		if !slices.Contains(active, d) {
			u = ""
			best := math.Inf(1)
			for _, a := range active {
				if !sameKind(a, d) {
					continue
				}
				if dist := math.Abs(math.Log(size[a] / size[d])); dist < best {
					best, u = dist, a
				}
			}
		}
		if u != "" && !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	if len(out) == 0 {
		return []string{Piece}
	}
	return out
}

const eps = 1e-9

// fits says whether an amount reads well in a unit: whole for dozen, pack and
// bunch, 1 to 999 for every other.
func fits(base float64, key string) bool {
	v := FromBase(base, key)
	switch key {
	case "dozen", "pack", "bunch":
		return v >= 1-eps && math.Abs(v-math.Round(v)) < eps
	}
	return v >= 1-eps && v <= 999+eps
}

// ShowUnit is the unit a total of base (pieces, grams or millilitres of the
// given kind) is shown in: the first of the article's default units that fits,
// else the largest active unit of the kind that gives at least 1.
func ShowUnit(base float64, kind Kind, defaults, active []string) string {
	for _, u := range Effective(defaults, active) {
		if KindOf(u) == kind && fits(base, u) {
			return u
		}
	}
	var of []string
	for _, k := range All {
		if KindOf(k) == kind && slices.Contains(active, k) {
			of = append(of, k)
		}
	}
	if len(of) == 0 { // the household switched them off; entries keep their kind
		for _, k := range All {
			if KindOf(k) == kind {
				of = append(of, k)
			}
		}
	}
	slices.SortStableFunc(of, func(a, b string) int {
		switch {
		case size[a] > size[b]:
			return -1
		case size[a] < size[b]:
			return 1
		}
		return 0
	})
	for _, k := range of {
		if FromBase(base, k) >= 1-eps {
			return k
		}
	}
	return of[len(of)-1]
}

// Round rounds an amount for display: g and ml to whole numbers, everything
// else to at most two decimals, half up.
func Round(amount float64, key string) float64 {
	if key == "g" || key == "ml" {
		return math.Floor(amount + 0.5 + eps)
	}
	return math.Floor(amount*100+0.5+eps) / 100
}

// Format writes an amount as a person reads it: rounded, no trailing zeros, no
// thousands separator, a comma in German and a point in English.
func Format(amount float64, key, lang string) string {
	s := strconv.FormatFloat(Round(amount, key), 'f', -1, 64)
	if lang != "en" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

// ErrAmount means the text is no amount between 0 and MaxAmount.
var ErrAmount = errors.New("unit: not an amount above 0 up to 9999")

var amountRe = regexp.MustCompile(`^\d+(?:[.,]\d+)?$`)

// Parse reads an amount typed with a comma or a point: above 0 and at most 9999.
func Parse(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if !amountRe.MatchString(s) {
		return 0, ErrAmount
	}
	v, err := strconv.ParseFloat(strings.Replace(s, ",", ".", 1), 64)
	if err != nil || v <= 0 || v > MaxAmount {
		return 0, ErrAmount
	}
	return v, nil
}

// Step is the size of one tap on - or +: 1, and 50 for g and ml.
func Step(key string) float64 {
	if key == "g" || key == "ml" {
		return 50
	}
	return 1
}

// StepUp goes to the next whole step above the amount, up to MaxAmount.
func StepUp(amount float64, key string) float64 {
	st := Step(key)
	return math.Min(MaxAmount, (math.Floor(amount/st+eps)+1)*st)
}

// StepDown goes to the next whole step below the amount; false when that
// would be below one step (nothing is zero, "without quantity" says so).
func StepDown(amount float64, key string) (float64, bool) {
	st := Step(key)
	v := (math.Ceil(amount/st-eps) - 1) * st
	if v < st-eps {
		return 0, false
	}
	return v, true
}
