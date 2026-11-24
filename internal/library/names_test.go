package library_test

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/library"
)

// Ticket 277 (Y): a typed name matches an article name in German or English
// ignoring case, umlauts and singular or plural.

func TestSameNameIgnoresPluralAndUmlauts(t *testing.T) {
	for _, p := range []struct {
		a, b string
		l    library.Lang
	}{
		{"Zitrone", "Zitronen", library.German}, {"Apfel", "Äpfel", library.German}, {"Ei", "Eier", library.German},
		{"Nuss", "Nüsse", library.German}, {"Tomate", "Tomaten", library.German}, {"Kartoffel", "Kartoffeln", library.German},
		{"lemon", "lemons", library.English}, {"tomato", "tomatoes", library.English}, {"berry", "berries", library.English},
		{"MEHL", "mehl", library.German}, {"Fuß", "Füße", library.German},
	} {
		if !library.SameName(p.a, p.b, p.l) || !library.SameName(p.b, p.a, p.l) {
			t.Errorf("%q and %q are the same name", p.a, p.b)
		}
	}
	for _, p := range [][3]string{{"Mehl", "Milch", "de"}, {"Apfel", "Apfelsaft", "de"}, {"", "Ei", "de"}, {"a", "an", "de"},
		{"Beeren", "Beer", "en"}, {"Beer", "Beeren", "en"}, {"Zitronen", "Zitron", "en"}} {
		if library.SameName(p[0], p[1], library.Lang(p[2])) {
			t.Errorf("%q and %q are not the same %s name", p[0], p[1], p[2])
		}
	}
}

// A German plural never matches an English name (and the other way round).
func TestAGermanPluralDoesNotFindAnEnglishName(t *testing.T) {
	all := []library.Article{{ID: "beer", De: "Bier", En: "Beer"}, {ID: "berries", De: "Beeren", En: "Berries"}}
	if a, ok := library.Resolve("Beeren", all[:1]); ok {
		t.Errorf("Beeren is not Beer: %+v", a)
	}
	if a, ok := library.Resolve("Beeren", all); !ok || a.ID != "berries" {
		t.Errorf("%+v %v", a, ok)
	}
}

// No inflected shipped name finds another article: the plural endings of a
// language apply to the names of that language only.
func TestNoPluralOfAShippedNameFindsAnotherArticle(t *testing.T) {
	all := library.Shipped()
	for _, a := range all {
		for _, name := range []string{a.De, a.En} {
			for _, end := range []string{"n", "en", "e", "er", "s", "es"} {
				f := name + end
				r, ok := library.Resolve(f, all)
				if !ok || r.ID == a.ID || library.Fold(r.De) == library.Fold(f) || library.Fold(r.En) == library.Fold(f) {
					continue
				}
				if r.Named(f) && !a.Named(f) {
					t.Errorf("%q (%s plural of %q) finds %s", f, end, name, r.ID)
				}
			}
		}
	}
	if a, ok := library.Resolve("Beeren", all); ok && a.ID == "beer" {
		t.Errorf("Beeren is not Beer: %+v", a)
	}
}

func arts() []library.Article {
	return []library.Article{
		{ID: "flour", De: "Mehl", En: "Flour"},
		{ID: "apple", De: "Apfel", En: "Apple"},
		{ID: "oil", De: "Olivenöl", En: "Olive oil"},
		{ID: "oil2", De: "Öl", En: "Oil"},
		{ID: "lemon", De: "Zitrone", En: "Lemon"},
		{ID: "milk", De: "Milch", En: "Milk"},
		{ID: "milkchoc", De: "Milch Schokolade", En: "Milk chocolate"},
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
	for _, in := range []string{"Milk chocolate", "Milch Schokolade", "Olive oil", "Mehl", "Mehl ", "Mehltau", "Mehl ·", ""} {
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
