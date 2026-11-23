// Package list holds the rules of lists and their entries (ADR 0015): what a
// list is, which colours and symbols it may have and in which order its
// entries are shown. It has no storage and no HTTP.
package list

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// Sort is how a list orders its entries.
type Sort string

// Sorts of a list.
const (
	Manual       Sort = "manual"
	Alphabetical Sort = "alphabetical"
)

// Limits.
const (
	MaxName = 60
	MaxText = 200
	// MaxVariety is the longest variety, in characters.
	MaxVariety = 60
)

// Colors are the list colours, in the order of the person colour slots of
// DESIGN.md (slot 1 to 6): the page colours a list like a person.
var Colors = []string{"terracotta", "blue", "green", "ochre", "plum", "teal"}

// Symbols are the black-and-white symbols, in slot order (DESIGN.md).
var Symbols = []string{"circle-filled", "circle-empty", "square-filled", "square-empty", "triangle-filled", "diamond-filled"}

// Slot is the person colour slot (1 to 6) of a list colour, or 0.
func Slot(color string) int {
	if i := slices.Index(Colors, color); i >= 0 {
		return i + 1
	}
	return 0
}

// List is a named collection of entries of the household.
type List struct {
	ID, Name, Color, Symbol string
	Sort                    Sort
	// Suggestions says the list offers articles from the library while typing
	// and takes quantities (ticket 165). A to-do list switches it off.
	Suggestions bool
}

// Entry is one line of text on a list that can be ticked off.
type Entry struct {
	ID, Text string
	Done     bool
	Position int
	// ArticleID is the article of the library the entry was made from, "" for free text.
	ArticleID string
	// Variety is the more exact kind of the article ("Type 405" for flour),
	// "" for none; only an entry with an article has one (ticket 277).
	Variety string
	// Qty is how much of it; none for an entry without quantity.
	Qty Quantity
	// Origin says a rule of "always on the list" put the entry on the list.
	Origin Origin
}

// Origin is where an entry came from when a rule put it on the list (ticket
// 168): Part is the quantity the rule brought and Prior the quantity that was
// already on the list, both exact. Both are zero after a quantity was changed by hand.
type Origin struct {
	Auto        bool
	Part, Prior float64
}

// InvalidError says which field of a list is wrong.
type InvalidError struct{ Field, Msg string }

func (e *InvalidError) Error() string { return "list " + e.Field + ": " + e.Msg }

// Validate checks a list. Name is expected trimmed.
func (l List) Validate() error {
	switch {
	case strings.TrimSpace(l.Name) == "":
		return &InvalidError{"name", "required"}
	case utf8.RuneCountInString(l.Name) > MaxName:
		return &InvalidError{"name", "too long"}
	case !slices.Contains(Colors, l.Color):
		return &InvalidError{"color", "unknown colour"}
	case !slices.Contains(Symbols, l.Symbol):
		return &InvalidError{"symbol", "unknown symbol"}
	case l.Sort != Manual && l.Sort != Alphabetical:
		return &InvalidError{"sort", "unknown sorting"}
	}
	return nil
}

// ValidateText checks the text of an entry.
func ValidateText(text string) error {
	switch {
	case strings.TrimSpace(text) == "":
		return &InvalidError{"text", "required"}
	case utf8.RuneCountInString(text) > MaxText:
		return &InvalidError{"text", "too long"}
	}
	return nil
}

// NormalizeVariety trims a variety and collapses the spaces inside it.
func NormalizeVariety(s string) string { return strings.Join(strings.Fields(s), " ") }

// ValidateVariety checks a variety that was asked for: it must not be empty
// ("required": "Neue Sorte" was chosen and left empty) nor longer than 60
// characters ("too long").
func ValidateVariety(v string) error {
	v = NormalizeVariety(v)
	switch {
	case v == "":
		return &InvalidError{"variety", "required"}
	case utf8.RuneCountInString(v) > MaxVariety:
		return &InvalidError{"variety", "too long"}
	}
	return nil
}

var fold = strings.NewReplacer("ä", "a", "ö", "o", "ü", "u", "ß", "ss")

func key(s string) string { return fold.Replace(strings.ToLower(strings.TrimSpace(s))) }

// VarietyKey is what two varieties are compared by: lower case, ä ö ü ß
// folded, trimmed, inner spaces collapsed.
func VarietyKey(s string) string { return key(NormalizeVariety(s)) }

// Order returns the entries in the order a list with this sorting shows them.
// Alphabetical ignores case and treats ä, ö, ü like a, o, u; manual follows
// the positions. The input is not changed.
func Order(entries []Entry, s Sort) []Entry {
	out := slices.Clone(entries)
	slices.SortStableFunc(out, func(a, b Entry) int {
		if s == Alphabetical {
			if c := strings.Compare(key(a.Text), key(b.Text)); c != 0 {
				return c
			}
			if c := strings.Compare(VarietyKey(a.Variety), VarietyKey(b.Variety)); c != 0 {
				return c // no variety first
			}
		}
		return a.Position - b.Position
	})
	return out
}

// View is how a list is shown (ADR 0019): as tiles (picture or symbol, name,
// tick) or as rows.
type View string

// The views.
const (
	Tiles View = "tiles"
	Rows  View = "list"
)

// DefaultView is the view of someone who has not chosen: children see tiles,
// adults the list.
func DefaultView(child bool) View {
	if child {
		return Tiles
	}
	return Rows
}

// ParseView reads a view as the forms and the API name it.
func ParseView(s string) (View, bool) {
	switch v := View(s); v {
	case Tiles, Rows:
		return v, true
	}
	return "", false
}
