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
}

// Entry is one line of text on a list that can be ticked off.
type Entry struct {
	ID, Text string
	Done     bool
	Position int
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

var fold = strings.NewReplacer("ä", "a", "ö", "o", "ü", "u", "ß", "ss")

func key(s string) string { return fold.Replace(strings.ToLower(strings.TrimSpace(s))) }

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
		}
		return a.Position - b.Position
	})
	return out
}
