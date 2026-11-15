// Package wholine words who an event is for: "Whole family", "with Kids",
// "everyone except Papa", or the names (Q62, ticket 5.22). It is the one place
// that decides; templates only print the result. Names are never cut and never
// replaced by a count.
package wholine

import (
	"slices"
	"strings"
)

// Person is a member of the household.
type Person struct{ ID, Name string }

// Group is a subgroup with the IDs of its members.
type Group struct {
	Name    string
	Members []string
}

// Input is one event seen from one place.
type Input struct {
	Household []Person // everyone, in display order
	Groups    []Group
	Audience  []string // IDs of the people the event is for (the effective audience)
	// Omit is the person whose column shows the card: left out of the names,
	// because the column already says it. "" leaves nobody out.
	Omit string
}

// Rules, in the order they are tried.
const (
	None      = 0 // nothing to say
	Everyone  = 1
	Subgroup  = 2
	Except    = 3
	NamesOnly = 4
)

// Line is the who-line of an event.
type Line struct {
	Rule int
	// Text is the line on a card: "Whole family", "with Kids", "everyone except
	// Papa", "with Mia, Ben".
	Text string
	// InBrackets is the line in the all-day row, where it follows the title in
	// square brackets: Text, but only the names for rule 4, and nothing for the
	// whole family.
	InBrackets string
}

// Of words who the event is for. tr looks up a catalogue key in the language
// of the page (wholine.family, wholine.with, wholine.except, wholine.and).
func Of(in Input, tr func(key string) string) Line {
	var people []Person // the audience, in household order
	for _, p := range in.Household {
		if slices.Contains(in.Audience, p.ID) {
			people = append(people, p)
		}
	}
	if len(people) == 0 {
		return Line{}
	}
	if len(people) == 1 && people[0].ID == in.Omit {
		return Line{}
	}
	if len(people) == len(in.Household) {
		return Line{Rule: Everyone, Text: tr("wholine.family")}
	}
	for _, g := range in.Groups {
		if sameSet(people, g.Members) {
			t := strings.Replace(tr("wholine.with"), "%s", g.Name, 1)
			return Line{Rule: Subgroup, Text: t, InBrackets: t}
		}
	}
	// "Everyone except" needs a group to be everyone but one or two; a lone
	// person is not "everyone except" the others.
	if out := len(in.Household) - len(people); out <= 2 && len(people) >= 2 {
		var left []string
		for _, p := range in.Household {
			if !slices.Contains(in.Audience, p.ID) {
				left = append(left, p.Name)
			}
		}
		t := strings.Replace(tr("wholine.except"), "%s", join(left, tr("wholine.and")), 1)
		return Line{Rule: Except, Text: t, InBrackets: t}
	}
	var others []string
	for _, p := range people {
		if p.ID != in.Omit {
			others = append(others, p.Name)
		}
	}
	list := strings.Join(others, ", ")
	return Line{Rule: NamesOnly, Text: strings.Replace(tr("wholine.with"), "%s", list, 1), InBrackets: list}
}

func join(names []string, and string) string {
	if len(names) == 2 {
		return names[0] + and + names[1]
	}
	return strings.Join(names, ", ")
}

// sameSet reports whether the people are exactly the members.
func sameSet(people []Person, members []string) bool {
	if len(people) != len(members) {
		return false
	}
	for _, p := range people {
		if !slices.Contains(members, p.ID) {
			return false
		}
	}
	return true
}
