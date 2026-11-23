package list

import (
	"regexp"
	"strings"

	"github.com/hejmo-home/hejmo/internal/unit"
)

// A list entry goes out to other apps as a reminder whose title holds the
// quantity after a middle dot ("Mehl · 1,5 kg"; ticket 167, Q26). These two
// functions are the whole of that convention.

// TitleSplit is a reminder title read as name and quantity.
type TitleSplit struct {
	Name string
	// Amount and Unit are the quantity; both zero without one.
	Amount float64
	Unit   string
	// Bare says the title ended in a number without a unit word: the caller
	// picks the unit (the article's first default unit).
	Bare bool
}

var titleRe = regexp.MustCompile(`^(.*?\S)(?:\s*[·\-,]\s*|\s+)(\d+(?:[.,]\d+)?)(?:\s*(\p{L}[\p{L}.]*))?$`)

// SplitTitle reads a trailing quantity: a number and a unit that unitOf knows
// (the active units, by their folded words), optionally after ·, - or ",". A
// number alone is Bare. Anything else, or an amount that is no amount, leaves
// the whole title as the name.
func SplitTitle(title string, unitOf func(word string) (string, bool)) TitleSplit {
	title = strings.TrimSpace(title)
	whole := TitleSplit{Name: title}
	m := titleRe.FindStringSubmatch(title)
	if m == nil {
		return whole
	}
	amount, err := unit.Parse(m[2])
	if err != nil {
		return whole
	}
	name := strings.TrimSpace(strings.TrimRight(m[1], "·-, "))
	if name == "" {
		return whole
	}
	if m[3] == "" {
		return TitleSplit{Name: name, Amount: amount, Bare: true}
	}
	key, ok := unitOf(strings.ToLower(m[3]))
	if !ok {
		return whole
	}
	return TitleSplit{Name: name, Amount: amount, Unit: key}
}

// JoinTitle writes name and a quantity in words as a reminder title.
func JoinTitle(name, qty string) string {
	if qty == "" {
		return name
	}
	return name + " · " + qty
}
