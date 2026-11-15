package wholine_test

import (
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/wholine"
)

// The household of eight (2 parents, 6 children, #76), in display order.
var names = []string{"Mama", "Papa", "Sebastian", "Mia", "Jonas", "Lotta", "Ben", "Emma"}

func household(n int) []wholine.Person {
	var out []wholine.Person
	for _, name := range names[:n] {
		out = append(out, wholine.Person{ID: strings.ToLower(name), Name: name})
	}
	return out
}

func ids(who ...string) []string {
	out := make([]string, len(who))
	for i, w := range who {
		out[i] = strings.ToLower(w)
	}
	return out
}

func TestLine(t *testing.T) {
	kids := wholine.Group{Name: "Kids", Members: ids("Sebastian", "Mia")}
	tr := i18n.MustLoad()
	cases := []struct {
		name             string
		in               wholine.Input
		rule             wholine.Rule
		en, de, brackets string // text on a card (en, de); what goes in the family strip's brackets (en)
	}{
		{"rule 1: everyone", wholine.Input{Household: household(4), Audience: ids("Mama", "Papa", "Sebastian", "Mia")},
			1, "Whole family", "ganze Familie", ""},
		{"rule 1 beats rule 2: a group that is everyone", wholine.Input{Household: household(2), Groups: []wholine.Group{{Name: "All", Members: ids("Mama", "Papa")}}, Audience: ids("Mama", "Papa")},
			1, "Whole family", "ganze Familie", ""},
		{"rule 2: exactly one subgroup", wholine.Input{Household: household(4), Groups: []wholine.Group{kids}, Audience: ids("Sebastian", "Mia")},
			2, "with Kids", "mit Gruppe Kids", "Kids"},
		{"rule 2 beats rule 3: a subgroup that is all but two", wholine.Input{Household: household(4), Groups: []wholine.Group{kids}, Audience: ids("Mia", "Sebastian")},
			2, "with Kids", "mit Gruppe Kids", "Kids"},
		{"rule 2 ignores group members who left the household", wholine.Input{Household: household(4), Groups: []wholine.Group{{Name: "Kids", Members: ids("Sebastian", "Mia", "ghost")}}, Audience: ids("Sebastian", "Mia")},
			2, "with Kids", "mit Gruppe Kids", "Kids"},
		{"rule 2 needs the exact set", wholine.Input{Household: household(5), Groups: []wholine.Group{kids}, Audience: ids("Sebastian", "Mia", "Jonas")},
			3, "everyone except Mama and Papa", "alle außer Mama und Papa", "everyone except Mama and Papa"},
		{"rule 3: all but one", wholine.Input{Household: household(4), Audience: ids("Mama", "Sebastian", "Mia")},
			3, "everyone except Papa", "alle außer Papa", "everyone except Papa"},
		{"rule 3: all but two", wholine.Input{Household: household(4), Audience: ids("Mama", "Papa")},
			3, "everyone except Sebastian and Mia", "alle außer Sebastian und Mia", "everyone except Sebastian and Mia"},
		{"rule 4: all but three lists the names", wholine.Input{Household: household(5), Audience: ids("Mama", "Papa")},
			4, "with Mama, Papa", "mit Mama, Papa", "Mama, Papa"},
		{"rule 4: one person of a big household", wholine.Input{Household: household(8), Audience: ids("Mia")},
			4, "with Mia", "mit Mia", "Mia"},
		{"rule 3 is not used for a single person", wholine.Input{Household: household(3), Audience: ids("Mama")},
			4, "with Mama", "mit Mama", "Mama"},
		{"five of eight are listed in full, no count, no ellipsis", wholine.Input{Household: household(8), Audience: ids("Mama", "Papa", "Sebastian", "Mia", "Jonas")},
			4, "with Mama, Papa, Sebastian, Mia, Jonas", "mit Mama, Papa, Sebastian, Mia, Jonas", "Mama, Papa, Sebastian, Mia, Jonas"},
		{"names follow the household order, not the audience order", wholine.Input{Household: household(8), Audience: ids("Ben", "Mia", "Jonas")},
			4, "with Mia, Jonas, Ben", "mit Mia, Jonas, Ben", "Mia, Jonas, Ben"},
		{"unknown people are ignored", wholine.Input{Household: household(8), Audience: ids("Mia", "ghost")},
			4, "with Mia", "mit Mia", "Mia"},
		{"nobody: no line", wholine.Input{Household: household(4)},
			0, "", "", ""},
		// Omit: the person's own column.
		{"column: own person left out of rule 4", wholine.Input{Household: household(8), Audience: ids("Mia", "Ben", "Jonas"), Omit: "mia"},
			4, "with Jonas, Ben", "mit Jonas, Ben", "Jonas, Ben"},
		{"column: a single-person event shows no line", wholine.Input{Household: household(8), Audience: ids("Mia"), Omit: "mia"},
			0, "", "", ""},
		{"column: rules 1 to 3 use the full set", wholine.Input{Household: household(4), Audience: ids("Mama", "Papa", "Mia"), Omit: "mia"},
			3, "everyone except Sebastian", "alle außer Sebastian", "everyone except Sebastian"},
		{"column: whole family", wholine.Input{Household: household(4), Audience: ids("Mama", "Papa", "Sebastian", "Mia"), Omit: "papa"},
			1, "Whole family", "ganze Familie", ""},
		{"column: subgroup", wholine.Input{Household: household(4), Groups: []wholine.Group{kids}, Audience: ids("Sebastian", "Mia"), Omit: "mia"},
			2, "with Kids", "mit Gruppe Kids", "Kids"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for lang, want := range map[string]string{"en": c.en, "de": c.de} {
				got := wholine.Of(c.in, tr, lang)
				if got.Rule != c.rule || got.Text != want {
					t.Errorf("%s: got rule %d %q, want rule %d %q", lang, got.Rule, got.Text, c.rule, want)
				}
				if lang == "en" && got.InBrackets != c.brackets {
					t.Errorf("brackets: got %q, want %q", got.InBrackets, c.brackets)
				}
				for _, bad := range []string{"+", "…", "..."} {
					if strings.Contains(got.Text, bad) {
						t.Errorf("%s: %q contains %q", lang, got.Text, bad)
					}
				}
			}
		})
	}
}

// The bare group name goes in the brackets in both languages (no "with", no "mit").
func TestSubgroupInBracketsIsTheBareNameInBothLanguages(t *testing.T) {
	tr := i18n.MustLoad()
	in := wholine.Input{Household: household(4), Groups: []wholine.Group{{Name: "Kinder", Members: ids("Sebastian", "Mia")}}, Audience: ids("Sebastian", "Mia")}
	for _, lang := range []string{"en", "de"} {
		got := wholine.Of(in, tr, lang)
		if got.InBrackets != "Kinder" {
			t.Errorf("%s: brackets %q, want Kinder", lang, got.InBrackets)
		}
	}
}

// Every name, with its trailing comma, is its own part, so a page can keep it
// whole on one line; the parts add up to the text.
func TestPartsKeepEveryNameWhole(t *testing.T) {
	tr := i18n.MustLoad()
	cases := []struct {
		name  string
		in    wholine.Input
		names []string // the parts marked as names, card
		brk   []string // the same for the brackets
	}{
		{"rule 4", wholine.Input{Household: household(8), Audience: ids("Mama", "Papa", "Jonas")}, []string{"Mama,", "Papa,", "Jonas"}, []string{"Mama,", "Papa,", "Jonas"}},
		{"rule 3", wholine.Input{Household: household(4), Audience: ids("Mama", "Papa")}, []string{"Sebastian", "Mia"}, []string{"Sebastian", "Mia"}},
		{"rule 2", wholine.Input{Household: household(4), Groups: []wholine.Group{{Name: "Kids", Members: ids("Sebastian", "Mia")}}, Audience: ids("Sebastian", "Mia")}, []string{"Kids"}, []string{"Kids"}},
		{"rule 1", wholine.Input{Household: household(2), Audience: ids("Mama", "Papa")}, nil, nil},
	}
	join := func(parts []wholine.Part) (text string, names []string) {
		for _, p := range parts {
			text += p.Text
			if p.Name {
				names = append(names, p.Text)
			}
		}
		return
	}
	for _, c := range cases {
		line := wholine.Of(c.in, tr, "en")
		text, names := join(line.Parts)
		if text != line.Text || strings.Join(names, "|") != strings.Join(c.names, "|") {
			t.Errorf("%s: parts %q names %q, want text %q names %q", c.name, text, names, line.Text, c.names)
		}
		text, names = join(line.BracketParts)
		if text != line.InBrackets || strings.Join(names, "|") != strings.Join(c.brk, "|") {
			t.Errorf("%s: bracket parts %q names %q, want text %q names %q", c.name, text, names, line.InBrackets, c.brk)
		}
	}
}
