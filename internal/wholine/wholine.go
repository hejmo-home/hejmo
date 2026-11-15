// Package wholine words who an event is for: "Whole family", "with Kids",
// "everyone except Papa", or the names (Q62, ticket 5.22). It is the one place
// that decides; templates only print the result. Names are never cut and never
// replaced by a count.
package wholine

import (
	"slices"
	"strings"

	"github.com/hejmo-home/hejmo/internal/i18n"
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

// Of words who the event is for, in lang, from the catalogue b (wholine.family,
// wholine.with and wholine.except with {names}, wholine.withGroup with {group},
// wholine.and).
func Of(in Input, b *i18n.Bundle, lang string) Line {
	tr := func(key string) string { return b.T(lang, key) }
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
		return line(Everyone, []Part{{Text: tr("wholine.family")}}, nil)
	}
	for _, g := range in.Groups {
		if sameSet(people, g.Members, in.Household) {
			name := []Part{{Text: g.Name, Name: true}}
			return line(Subgroup, fill(tr("wholine.withGroup"), "{group}", name), name)
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
		parts := fill(tr("wholine.except"), "{names}", namesAnd(left, tr("wholine.and")))
		return line(Except, parts, parts)
	}
	var others []string
	for _, p := range people {
		if p.ID != in.Omit {
			others = append(others, p.Name)
		}
	}
	list := namesComma(others)
	return line(NamesOnly, fill(tr("wholine.with"), "{names}", list), list)
}

// line builds a Line; the texts are the parts joined, so there is one source.
func line(r Rule, parts, bracket []Part) Line {
	return Line{Rule: r, Text: Plain(parts), InBrackets: Plain(bracket), Parts: parts, BracketParts: bracket}
}

// Plain is the text the parts add up to.
func Plain(parts []Part) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

// fill puts parts where the template has the placeholder.
func fill(tmpl, placeholder string, inner []Part) []Part {
	before, after, _ := strings.Cut(tmpl, placeholder)
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

// namesAnd lists one or two names: "A" or "A and B", with the catalogue's word
// for "and" (spaces included).
func namesAnd(names []string, and string) []Part {
	if len(names) == 2 {
		return []Part{{Text: names[0], Name: true}, {Text: and}, {Text: names[1], Name: true}}
	}
	return namesComma(names)
}

// namesComma lists names as "A, B, C". A comma stays attached to its name, so
// a name never splits from it across lines.
func namesComma(names []string) []Part {
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
