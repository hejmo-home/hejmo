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

// Rule is the rule that worded a line.
type Rule int

// Rules, in the order they are tried.
const (
	None      Rule = 0 // nothing to say
	Everyone  Rule = 1
	Subgroup  Rule = 2
	Except    Rule = 3
	NamesOnly Rule = 4
)

// Part is a piece of a line. Name parts are names (a person's, or a group's)
// with their trailing comma: a page keeps each on one line.
type Part struct {
	Text string
	Name bool
}

// Line is the who-line of an event.
type Line struct {
	Rule Rule
	// Text is the line on a card: "Whole family", "with Kids", "everyone except
	// Papa", "with Mia, Ben".
	Text string
	// InBrackets is the line in the family strip, where it follows the title in
	// square brackets: "Kids" for a subgroup, only the names for rule 4, the
	// "everyone except" line for rule 3, and nothing for the whole family. It
	// never says "with".
	InBrackets string
	// Parts and BracketParts are Text and InBrackets in pieces; they add up to them.
	Parts, BracketParts []Part
}

// Of words who the event is for. tr looks up a catalogue key in the language
// of the page (wholine.family, wholine.with, wholine.withGroup, wholine.except,
// wholine.and).
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
		t := tr("wholine.family")
		return Line{Rule: Everyone, Text: t, Parts: []Part{{Text: t}}}
	}
	for _, g := range in.Groups {
		if sameSet(people, g.Members, in.Household) {
			name := []Part{{Text: g.Name, Name: true}}
			return Line{Rule: Subgroup, Text: text(tr("wholine.withGroup"), g.Name), InBrackets: g.Name,
				Parts: fill(tr("wholine.withGroup"), name), BracketParts: name}
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
		parts := fill(tr("wholine.except"), joinParts(left, tr("wholine.and")))
		return Line{Rule: Except, Text: plain(parts), InBrackets: plain(parts), Parts: parts, BracketParts: parts}
	}
	var others []string
	for _, p := range people {
		if p.ID != in.Omit {
			others = append(others, p.Name)
		}
	}
	list := joinParts(others, ", ")
	return Line{Rule: NamesOnly, Text: text(tr("wholine.with"), plain(list)), InBrackets: plain(list),
		Parts: fill(tr("wholine.with"), list), BracketParts: list}
}

func text(tmpl, s string) string { return strings.Replace(tmpl, "%s", s, 1) }

func plain(parts []Part) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

// fill puts parts where the template has %s.
func fill(tmpl string, inner []Part) []Part {
	before, after, _ := strings.Cut(tmpl, "%s")
	var out []Part
	if before != "" {
		out = append(out, Part{Text: before})
	}
	out = append(out, inner...)
	if after != "" {
		out = append(out, Part{Text: after})
	}
	return out
}

// joinParts lists names. With "and" as the joiner (the catalogue's word, spaces
// included) two names read "A and B"; with ", " every list is "A, B, C". A
// comma stays attached to its name, so the pair never splits across lines.
func joinParts(names []string, and string) []Part {
	if len(names) == 2 && and != ", " {
		return []Part{{Text: names[0], Name: true}, {Text: and}, {Text: names[1], Name: true}}
	}
	var out []Part
	for i, n := range names {
		if i == len(names)-1 {
			out = append(out, Part{Text: n, Name: true})
			break
		}
		out = append(out, Part{Text: n + ",", Name: true}, Part{Text: " "})
	}
	return out
}

// sameSet reports whether the people are exactly the members who are still in
// the household.
func sameSet(people []Person, members []string, household []Person) bool {
	n := 0
	for _, h := range household {
		if slices.Contains(members, h.ID) {
			n++
		}
	}
	if len(people) != n {
		return false
	}
	for _, p := range people {
		if !slices.Contains(members, p.ID) {
			return false
		}
	}
	return true
}
