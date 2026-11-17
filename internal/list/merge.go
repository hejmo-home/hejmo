package list

import (
	"errors"

	"github.com/hejmo-home/hejmo/internal/unit"
)

// Quantity is how much of a thing an entry asks for. Base is exact, in pieces,
// grams or millilitres (what adding up needs); Unit is the unit it is shown in.
// The zero Quantity is no quantity.
type Quantity struct {
	Base float64
	Unit string
}

// NewQuantity makes a quantity from what was typed: an amount in a unit.
func NewQuantity(amount float64, key string) Quantity {
	return Quantity{Base: unit.ToBase(amount, key), Unit: key}
}

// None reports whether there is no quantity.
func (q Quantity) None() bool { return q.Unit == "" }

// Kind is what the quantity measures; a pack and a bunch measure only themselves.
func (q Quantity) Kind() unit.Kind { return unit.KindOf(q.Unit) }

// Amount is the number in the shown unit, not rounded.
func (q Quantity) Amount() float64 { return unit.FromBase(q.Base, q.Unit) }

// Show says in which unit a total of the given kind is shown. The box knows
// the article's default units and the units the household switched on.
type Show func(base float64, kind unit.Kind) string

func (q Quantity) shown(show Show) Quantity {
	if q.None() {
		return q
	}
	return Quantity{Base: q.Base, Unit: show(q.Base, q.Kind())}
}

// Same reports whether an open entry is the same thing as the article (or, for
// free text, the text, ignoring case and umlauts).
func (e Entry) Same(articleID, text string) bool {
	return !e.Done && ((articleID != "" && e.ArticleID == articleID) || key(e.Text) == key(text))
}

// AddAction says what adding did.
type AddAction string

// What adding can do.
const (
	AddedNew AddAction = "new"    // a new entry
	AddedTo  AddAction = "added"  // the quantity was added to an entry of the same kind
	Filled   AddAction = "filled" // an entry without quantity took the quantity
	Exists   AddAction = "exists" // already on the list; added nothing
)

// AddPlan is what adding a thing with a quantity comes to.
type AddPlan struct {
	Action AddAction
	// Target is the entry that changes or already exists (not for AddedNew).
	Target Entry
	// After is the entry a new one goes directly under; "" puts it at the end.
	After string
	// Before is the quantity of Target before; Result the quantity Target (or
	// the new entry) has afterwards.
	Before, Result Quantity
}

// PlanAdd decides what adding article (or free text) with a quantity does to
// the entries of a list. Only open entries merge. The same kind of unit adds
// up, an entry without quantity takes the quantity, adding without quantity
// never makes a second entry, and another kind stands under the old entry.
func PlanAdd(entries []Entry, articleID, text string, add Quantity, show Show) AddPlan {
	var same []Entry
	for _, e := range entries {
		if e.Same(articleID, text) {
			same = append(same, e)
		}
	}
	if add.None() {
		if len(same) > 0 {
			return AddPlan{Action: Exists, Target: same[0], Before: same[0].Qty, Result: same[0].Qty}
		}
		return AddPlan{Action: AddedNew}
	}
	for _, e := range same {
		if !e.Qty.None() && e.Qty.Kind() == add.Kind() {
			sum := Quantity{Base: e.Qty.Base + add.Base, Unit: e.Qty.Unit}
			return AddPlan{Action: AddedTo, Target: e, Before: e.Qty, Result: sum.shown(show)}
		}
	}
	for _, e := range same {
		if e.Qty.None() {
			return AddPlan{Action: Filled, Target: e, Result: add.shown(show)}
		}
	}
	p := AddPlan{Action: AddedNew, Result: add.shown(show)}
	if n := len(same); n > 0 {
		p.After = same[n-1].ID
	}
	return p
}

// SetAction says what changing a quantity did.
type SetAction string

// What changing can do.
const (
	SetOnly   SetAction = "set"    // the entry has its new quantity
	SetMerged SetAction = "merged" // the entry went into another one of the article
)

// SetPlan is what changing the quantity of one entry comes to.
type SetPlan struct {
	Action SetAction
	// Entry is the entry that was changed.
	Entry Entry
	// Into is the other entry that took this one in (SetMerged).
	Into Entry
	// Before is the quantity of Entry before; Result the quantity that stands
	// afterwards on Entry (SetOnly) or on Into (SetMerged).
	Before, Result Quantity
}

// ErrNotOpen means the entry is gone or ticked off.
var ErrNotOpen = errors.New("list: the entry is not open")

// PlanSet decides what giving the entry with this id a new quantity (the zero
// Quantity for none) comes to. It replaces and never adds. The amount typed is
// the amount: a change of unit within one kind converts the number before it
// gets here. When another open entry of the article has the same kind (or, for
// "none", any quantity), this entry goes into it: one article, one kind, one
// entry.
func PlanSet(entries []Entry, id string, to Quantity, show Show) (SetPlan, error) {
	var cur *Entry
	for i := range entries {
		if entries[i].ID == id && !entries[i].Done {
			cur = &entries[i]
		}
	}
	if cur == nil {
		return SetPlan{}, ErrNotOpen
	}
	p := SetPlan{Entry: *cur, Before: cur.Qty}
	for _, e := range entries {
		if e.ID == id || !e.Same(cur.ArticleID, cur.Text) || e.Qty.None() {
			continue
		}
		if to.None() {
			p.Action, p.Into, p.Result = SetMerged, e, e.Qty
			return p, nil
		}
		if e.Qty.Kind() == to.Kind() {
			sum := Quantity{Base: e.Qty.Base + to.Base, Unit: e.Qty.Unit}
			p.Action, p.Into, p.Result = SetMerged, e, sum.shown(show)
			return p, nil
		}
	}
	p.Action, p.Result = SetOnly, to.shown(show)
	return p, nil
}
