package nutrition

import (
	"slices"

	"github.com/hejmo-home/hejmo/internal/unit"
)

// Millilitres of the kitchen measures that may count in grams (ADR 0021,
// product owner decision of 25/11/2026): tablespoon, teaspoon, pinch. A pinch
// is a sixteenth of a teaspoon.
const (
	tbspML  = 15.0
	tspML   = 5.0
	pinchML = 0.3
)

// Grams turns an amount of a unit into grams, for nutrition only (shopping
// lists never convert kitchen measures). ok is false when Hejmo does not know
// a weight: it says so instead of guessing.
//
//   - weights (g, kg, oz, lb) always convert;
//   - volumes (ml, l, fl oz, cup, pint, quart, gallon), tablespoon, teaspoon and
//     pinch need the article's grams per millilitre (gPerML > 0);
//   - piece, dozen, jar and tub (and a quantity without a unit) need the weight of
//     one piece of the article (pieceG > 0);
//   - everything else (knife tip, dash, splash, handful, cup, can, packet, clove,
//     slice, pack, bunch ...) has no weight.
func Grams(amount float64, key string, pieceG, gPerML float64) (float64, bool) {
	if amount <= 0 {
		return 0, false
	}
	switch key {
	case "":
		key = unit.Piece
	case "jar", "tub":
		if pieceG <= 0 {
			return 0, false
		}
		return amount * pieceG, true
	case "tbsp":
		return millilitres(amount*tbspML, gPerML)
	case "tsp":
		return millilitres(amount*tspML, gPerML)
	case "pinch":
		return millilitres(amount*pinchML, gPerML)
	}
	switch unit.KindOf(key) {
	case unit.Weight:
		return unit.ToBase(amount, key), true
	case unit.Volume:
		return millilitres(unit.ToBase(amount, key), gPerML)
	case unit.Count:
		if pieceG <= 0 {
			return 0, false
		}
		return unit.ToBase(amount, key) * pieceG, true
	}
	return 0, false
}

func millilitres(ml, gPerML float64) (float64, bool) {
	if gPerML <= 0 {
		return 0, false
	}
	return ml * gPerML, true
}

// Line is one ingredient of a recipe as the calculation needs it.
type Line struct {
	// Name is how the page names the line (the article's name, or the text).
	Name string
	// Amount is the quantity (0: none); Unit is the unit key, "" for none.
	Amount float64
	Unit   string
	// Free marks free text: Hejmo knows no food for it.
	Free bool
	// ArticleID and Own say which article it is, so a page can link to an own
	// article that has no food yet.
	ArticleID string
	Own       bool
	// Food is the food of the article, nil when there is none (an own
	// article that has none).
	Food *Food
	// PieceG is the weight of one piece, GPerML the grams of a millilitre; 0:
	// not known.
	PieceG, GPerML float64
}

// Reason says why a line is not counted.
type Reason int

// The reasons a line is not counted.
const (
	// ReasonFree: free text with a quantity.
	ReasonFree Reason = iota + 1
	// ReasonFreeNoAmount: free text without a quantity.
	ReasonFreeNoAmount
	// ReasonNoFood: an own article without a food.
	ReasonNoFood
	// ReasonNoAmount: an article without a quantity ("Salz").
	ReasonNoAmount
	// ReasonNoWeight: a unit Hejmo cannot turn into grams for this article.
	ReasonNoWeight
)

// Skipped is a line that was not counted, and why.
type Skipped struct {
	Name      string
	Unit      string
	ArticleID string
	Own       bool
	Reason    Reason
}

// Gap says a value is unknown because the food of a counted line lacks it.
type Gap struct {
	// Key is the first of the numbers (KCal stands for the energy, both numbers).
	Key   Key
	Foods []Food
}

// Result is what a recipe's lines make per portion.
type Result struct {
	// Counted is the number of lines in the sum.
	Counted int
	// Values are per portion; a value that is missing is unknown (a gap), and
	// every value is missing when nothing was counted.
	Values  Values
	Skipped []Skipped
	Gaps    []Gap
	// Sources are the databases the counted foods come from, BLS first.
	Sources []Source
}

// Calculate sums the lines and divides by the portions.
func Calculate(portions int, lines []Line) Result {
	var r Result
	if portions < 1 {
		portions = 1
	}
	var sum Values
	var lacking [NumKeys][]Food
	for _, l := range lines {
		grams, reason := classify(l)
		if reason != 0 {
			r.Skipped = append(r.Skipped, Skipped{Name: l.Name, Unit: l.Unit, ArticleID: l.ArticleID, Own: l.Own, Reason: reason})
			continue
		}
		r.Counted++
		if !slices.Contains(r.Sources, l.Food.Source) {
			r.Sources = append(r.Sources, l.Food.Source)
		}
		for k := Key(0); k < NumKeys; k++ {
			if per100, ok := l.Food.Per100.Get(k); ok {
				cur, _ := sum.Get(k)
				sum.Set(k, cur+per100*grams/100)
			} else if !slices.ContainsFunc(lacking[k], func(f Food) bool { return f.ID() == l.Food.ID() }) {
				lacking[k] = append(lacking[k], *l.Food)
			}
		}
	}
	slices.Sort(r.Sources) // "bls" before "usda"
	if r.Counted == 0 {
		return r
	}
	for k := Key(0); k < NumKeys; k++ {
		if len(lacking[k]) > 0 {
			continue
		}
		x, _ := sum.Get(k)
		r.Values.Set(k, x/float64(portions))
	}
	for k := Key(0); k < NumKeys; k++ {
		if len(lacking[k]) == 0 || k == KJ && len(lacking[KCal]) > 0 {
			continue // energy is one gap: it is named once, by KCal (or by KJ when only that is missing)
		}
		r.Gaps = append(r.Gaps, Gap{Key: k, Foods: lacking[k]})
	}
	return r
}

// classify returns the grams a line counts, or why it does not count.
func classify(l Line) (float64, Reason) {
	switch {
	case l.Free:
		if l.Amount <= 0 {
			return 0, ReasonFreeNoAmount
		}
		return 0, ReasonFree
	case l.Food == nil:
		return 0, ReasonNoFood
	case l.Amount <= 0:
		return 0, ReasonNoAmount
	}
	g, ok := Grams(l.Amount, l.Unit, l.PieceG, l.GPerML)
	if !ok {
		return 0, ReasonNoWeight
	}
	return g, 0
}
