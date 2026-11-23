package library_test

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/library"
)

// Ticket 277 (Y): a typed name matches an article name in German or English
// ignoring case, umlauts and singular or plural.

func TestSameNameIgnoresPluralAndUmlauts(t *testing.T) {
	for _, p := range [][2]string{
		{"Zitrone", "Zitronen"}, {"Apfel", "Äpfel"}, {"Ei", "Eier"}, {"Nuss", "Nüsse"}, {"Tomate", "Tomaten"},
		{"Kartoffel", "Kartoffeln"}, {"lemon", "lemons"}, {"tomato", "tomatoes"}, {"berry", "berries"},
		{"MEHL", "mehl"}, {"Fuß", "Füße"},
	} {
		if !library.SameName(p[0], p[1]) || !library.SameName(p[1], p[0]) {
			t.Errorf("%q and %q are the same name", p[0], p[1])
		}
	}
	for _, p := range [][2]string{{"Mehl", "Milch"}, {"Apfel", "Apfelsaft"}, {"", "Ei"}, {"a", "an"}} {
		if library.SameName(p[0], p[1]) {
			t.Errorf("%q and %q are not the same name", p[0], p[1])
		}
	}
}

func arts() []library.Article {
	return []library.Article{
		{ID: "flour", De: "Mehl", En: "Flour"},
		{ID: "apple", De: "Apfel", En: "Apple"},
		{ID: "oil", De: "Olivenöl", En: "Olive oil"},
		{ID: "oil2", De: "Öl", En: "Oil"},
		{ID: "lemon", De: "Zitrone", En: "Lemon"},
	}
}

func TestResolveFindsAPluralByItsSingularAndPrefersTheExactName(t *testing.T) {
	a, ok := library.Resolve("Zitronen", arts())
	if !ok || a.ID != "lemon" {
		t.Errorf("%+v %v", a, ok)
	}
	if a, ok := library.Resolve("Äpfel", arts()); !ok || a.ID != "apple" {
		t.Errorf("%+v %v", a, ok)
	}
	all := append([]library.Article{{ID: "ei", De: "Ei"}}, library.Article{ID: "eis", De: "Eis"})
	if a, _ := library.Resolve("Eis", all); a.ID != "eis" {
		t.Errorf("the exact name wins over a plural fold: %+v", a)
	}
}

func TestLeadingNameSplitsTheVarietyOffTheEnd(t *testing.T) {
	for _, c := range []struct{ in, id, rest string }{
		{"Mehl Type 405", "flour", "Type 405"},
		{"mehl, Type 405", "flour", "Type 405"},
		{"Mehl · Type 405", "flour", "Type 405"},
		{"Mehl-Type 405", "flour", "Type 405"},
		{"Mehl: Type 405", "flour", "Type 405"},
		{"Äpfel Elstar", "apple", "Elstar"},
		{"Flour type 00", "flour", "type 00"},
		{"Olivenöl nativ extra", "oil", "nativ extra"},
		{"Olive oil cold pressed", "oil", "cold pressed"},
		{"Zitronen  Bio", "lemon", "Bio"},
	} {
		a, rest, ok := library.LeadingName(c.in, arts())
		if !ok || a.ID != c.id || rest != c.rest {
			t.Errorf("%q: %+v %q %v", c.in, a, rest, ok)
		}
	}
	for _, in := range []string{"Mehl", "Mehl ", "Mehltau", "Milch Vollmilch", "Mehl ·", ""} {
		if a, rest, ok := library.LeadingName(in, arts()); ok {
			t.Errorf("%q has no variety: %+v %q", in, a, rest)
		}
	}
}

func TestCompleteFindsAPluralTypedForASingularName(t *testing.T) {
	got := library.Complete("Zitronen", arts(), 4)
	if len(got) == 0 || got[0].ID != "lemon" {
		t.Errorf("%+v", got)
	}
	if got := library.Complete("Nüsse", []library.Article{{ID: "nut", De: "Nuss"}}, 4); len(got) != 1 {
		t.Errorf("%+v", got)
	}
}
