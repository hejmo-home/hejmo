package library_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/food"
	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// The shipped library (ADR 0019, ticket 164): about 250 articles with symbol,
// department, what is in it and the allergens it carries.

func TestHejmoShipsAboutTwoHundredAndFiftyArticlesInThirteenDepartments(t *testing.T) {
	all := library.Shipped()
	if n := len(all); n < 240 || n > 260 {
		t.Errorf("%d shipped articles, want about 250", n)
	}
	if n := len(library.Depts); n != 13 {
		t.Errorf("%d departments, want 13", n)
	}
	seen := map[string]bool{}
	perDept := map[string]int{}
	for _, a := range all {
		if seen[a.ID] {
			t.Errorf("article key %q twice", a.ID)
		}
		seen[a.ID] = true
		perDept[a.Dept]++
		if a.De == "" || a.En == "" || a.Symbol == "" {
			t.Errorf("%s lacks a name in one language or its symbol: %+v", a.ID, a)
		}
		if a.Own {
			t.Errorf("%s is shipped but marked own", a.ID)
		}
	}
	for _, d := range library.Depts {
		if perDept[d] == 0 {
			t.Errorf("department %s has no shipped article", d)
		}
	}
}

func TestEveryShippedArticleHasValidDepartmentKindAllergensUnitsAndSymbol(t *testing.T) {
	for _, a := range library.Shipped() {
		if !library.ValidDept(a.Dept) {
			t.Errorf("%s: department %q", a.ID, a.Dept)
		}
		if !library.ValidKind(string(a.Kind)) {
			t.Errorf("%s: kind %q", a.ID, a.Kind)
		}
		if _, ok := food.NormalizeAllergens(a.Allergens); !ok {
			t.Errorf("%s: allergens %v", a.ID, a.Allergens)
		}
		if n := len(a.Units); n < 1 || n > 2 {
			t.Errorf("%s: %d default units, want one or two", a.ID, n)
		}
		for _, u := range a.Units {
			if !unit.Valid(u) {
				t.Errorf("%s: unit %q", a.ID, u)
			}
		}
		if _, ok := library.SymbolByEmoji(a.Symbol); !ok {
			t.Errorf("%s: symbol %q is not a Noto Emoji symbol with a name", a.ID, a.Symbol)
		}
	}
}

// The allergen tags are reviewed by the product owner. These rules catch the
// mistakes a tired eye misses: a vegan article cannot hold milk or egg, a
// vegetarian one no fish, and something that is not food holds no allergen.
func TestAllergenTagsAgreeWithWhatIsInTheArticle(t *testing.T) {
	animal := map[library.Kind][]string{
		library.Vegan: {"milk", "eggs", "fish", "crustaceans", "molluscs"},
		library.Veg:   {"fish", "crustaceans", "molluscs"},
	}
	for _, a := range library.Shipped() {
		for _, bad := range animal[a.Kind] {
			if slices.Contains(a.Allergens, bad) {
				t.Errorf("%s is %s but carries %s", a.ID, a.Kind, bad)
			}
		}
		if a.Kind == library.NonFood && len(a.Allergens) > 0 {
			t.Errorf("%s is not food but carries %v", a.ID, a.Allergens)
		}
		if a.Kind == library.Fish && !slices.ContainsFunc(a.Allergens, func(x string) bool { return x == "fish" || x == "crustaceans" || x == "molluscs" }) {
			t.Errorf("%s is fish but carries no fish, crustacean or mollusc allergen", a.ID)
		}
	}
}

func TestKnownArticlesCarryTheirAllergens(t *testing.T) {
	want := map[string][]string{
		"peanut-butter":     {"peanuts"},
		"nut-nougat-spread": {"milk", "nuts", "soy"},
		"celeriac":          {"celery"},
		"salmon-fillet":     {"fish"},
		"bread":             {"gluten"},
		"eggs":              {"eggs"},
		"butter":            {"milk"},
		"prawns":            {"crustaceans"},
		"mussels":           {"molluscs"},
		"tahini":            {"sesame"},
		"mustard":           {"mustard"},
		"wine":              {"sulphites"},
		"soy-sauce":         {"soy", "gluten"},
		"apples":            {},
		"toothpaste":        {},
	}
	byID := map[string]library.Article{}
	for _, a := range library.Shipped() {
		byID[a.ID] = a
	}
	for id, allergens := range want {
		a, ok := byID[id]
		if !ok {
			t.Errorf("no shipped article %s", id)
			continue
		}
		got, _ := food.NormalizeAllergens(a.Allergens)
		w, _ := food.NormalizeAllergens(allergens)
		if !reflect.DeepEqual(got, w) {
			t.Errorf("%s carries %v, want %v", id, got, w)
		}
	}
	if a := byID["peanut-butter"]; a.De != "Erdnussbutter" || a.En != "Peanut butter" || a.Dept != "pantry" || a.Kind != library.Vegan || !reflect.DeepEqual(a.Units, []string{"piece", "g"}) {
		t.Errorf("peanut butter = %+v", a)
	}
}

func TestDietsThatAKindFits(t *testing.T) {
	cases := map[library.Kind][]food.Diet{
		library.Meat:    {food.Omnivore},
		library.Fish:    {food.Omnivore, food.Pescatarian},
		library.Veg:     {food.Omnivore, food.Pescatarian, food.Vegetarian},
		library.Vegan:   {food.Omnivore, food.Pescatarian, food.Vegetarian, food.Vegan},
		library.NonFood: {food.Omnivore, food.Pescatarian, food.Vegetarian, food.Vegan},
	}
	for kind, fits := range cases {
		for _, d := range food.Diets {
			if got, want := kind.Fits(d), slices.Contains(fits, d); got != want {
				t.Errorf("%s fits %s = %v, want %v", kind, d, got, want)
			}
		}
	}
}

func TestSearchIgnoresCaseAndUmlautsAndFindsGermanAndEnglishNames(t *testing.T) {
	var hits []string
	for _, a := range library.Shipped() {
		if library.Matches(a, "NUSS") {
			hits = append(hits, a.De)
		}
	}
	for _, want := range []string{"Erdnussbutter", "Nuss-Nougat-Creme", "Muskatnuss", "Haselnüsse, gemahlen", "Walnüsse", "Kokosnussmilch"} {
		if !slices.Contains(hits, want) {
			t.Errorf("\"nuss\" lacks %s in %v", want, hits)
		}
	}
	if library.Fold("Äpfel Müsli Süß") != "apfel musli suss" {
		t.Errorf("fold = %q", library.Fold("Äpfel Müsli Süß"))
	}
	apples, _ := library.ShippedByID("apples")
	for _, q := range []string{"apfel", "Äpfel", "APPLES", "appl"} {
		if !library.Matches(apples, q) {
			t.Errorf("%q does not find apples", q)
		}
	}
	if library.Matches(apples, "birne") {
		t.Error("\"birne\" finds apples")
	}
}

func TestNameSuggestionsAreUpToFiveSimilarArticles(t *testing.T) {
	own := library.Article{ID: "o1", Own: true, De: "Hafer-Drink Barista", En: "Hafer-Drink Barista", Dept: "drinks", Kind: library.Vegan}
	all := append([]library.Article{own}, library.Shipped()...)
	got := library.Similar("Hafer", all, "de", 5)
	if len(got) == 0 || len(got) > 5 {
		t.Fatalf("suggestions = %d", len(got))
	}
	var names []string
	for _, a := range got {
		names = append(names, a.Name("de"))
	}
	for _, want := range []string{"Haferflocken", "Hafer-Drink Barista", "Haferkekse"} {
		if !slices.Contains(names, want) {
			t.Errorf("suggestions %v lack %s", names, want)
		}
	}
	if got := library.Similar("h", all, "de", 5); len(got) != 0 {
		t.Errorf("one letter suggests %d articles", len(got))
	}
	if got := library.Similar("Quinoa-Pops", all, "de", 5); len(got) != 0 {
		t.Errorf("an unknown name suggests %v", got)
	}
}

func TestEveryDepartmentHasASymbolAndTheSymbolsToSuggestStartWithIt(t *testing.T) {
	for _, d := range library.Depts {
		if _, ok := library.SymbolByEmoji(library.DeptSymbol(d)); !ok {
			t.Errorf("department %s: symbol %q is not in the symbol catalogue", d, library.DeptSymbol(d))
		}
	}
	got := library.DeptSymbols("pantry")
	if len(got) < 5 || got[0] != library.DeptSymbol("pantry") {
		t.Fatalf("suggested = %v", got)
	}
	seen := map[string]bool{}
	for _, s := range got {
		if seen[library.CanonEmoji(s)] {
			t.Errorf("%s suggested twice", s)
		}
		seen[library.CanonEmoji(s)] = true
	}
	for _, want := range []string{"🥣", "🍯", "🥜"} {
		if !seen[want] {
			t.Errorf("the symbols of pantry articles lack %s", want)
		}
	}
}

func TestSymbolSearchFindsGermanAndEnglishNamesWithoutUmlautDifference(t *testing.T) {
	cat := library.SearchSymbols("katze", "de")
	if len(cat) < 4 {
		t.Fatalf("katze = %d symbols", len(cat))
	}
	if n := library.SymbolName(cat[0], "de"); n == "" {
		t.Error("a symbol without its name")
	}
	en := library.SearchSymbols("cat", "de")
	for _, c := range cat {
		if !slices.Contains(en, c) {
			t.Errorf("\"cat\" does not find %s that \"katze\" finds", c.Emoji)
		}
	}
	if got := library.SearchSymbols("schussel", "de"); len(got) == 0 {
		t.Error("ü = u is not folded in the symbol search")
	}
	if got := library.SearchSymbols("quinoa", "de"); len(got) != 0 {
		t.Errorf("quinoa = %v", got)
	}
	if n := library.SymbolCount(); n < 1500 {
		t.Errorf("only %d symbols", n)
	}
	s, ok := library.SymbolByEmoji("🌶️") // with and without the variation selector
	if !ok || library.SymbolName(s, "en") != "hot pepper" {
		t.Errorf("hot pepper = %v %v", s, ok)
	}
}

func TestValidateAnOwnArticle(t *testing.T) {
	active := unit.DefaultActive
	ok := library.Input{Name: "Katzenfutter Huhn", Dept: "other", Kind: "nonfood", Unit1: "piece", Symbol: "🐈", PicSource: "symbol"}
	if errs := ok.Validate(active); len(errs) != 0 {
		t.Errorf("a fine article: %v", errs)
	}
	bad := library.Input{Name: "  ", Dept: "garden", Kind: "", Unit1: "", Allergens: []string{"kiwi"}}
	got := map[string]string{}
	for _, e := range bad.Validate(active) {
		got[e.Field] = e.Code
	}
	want := map[string]string{"name": "required", "dept": "unknown", "kind": "required", "unit1": "required", "allergens": "unknown"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("errors = %v, want %v", got, want)
	}
	long := library.Input{Name: strings.Repeat("a", 61), Dept: "other", Kind: "veg", Unit1: "piece"}
	if e := long.Validate(active); len(e) != 1 || e[0].Field != "name" || e[0].Code != "long" {
		t.Errorf("long name: %v", e)
	}
	for name, in := range map[string]library.Input{
		"same unit twice":       {Name: "x", Dept: "other", Kind: "veg", Unit1: "g", Unit2: "g"},
		"unit switched off":     {Name: "x", Dept: "other", Kind: "veg", Unit1: "lb"},
		"unknown unit":          {Name: "x", Dept: "other", Kind: "veg", Unit1: "stone"},
		"unknown second unit":   {Name: "x", Dept: "other", Kind: "veg", Unit1: "g", Unit2: "stone"},
		"symbol not a symbol":   {Name: "x", Dept: "other", Kind: "veg", Unit1: "g", PicSource: "symbol", Symbol: "abc"},
		"symbol chosen, absent": {Name: "x", Dept: "other", Kind: "veg", Unit1: "g", PicSource: "symbol"},
	} {
		if len(in.Validate(active)) == 0 {
			t.Errorf("%s was accepted", name)
		}
	}
	nonfood := library.Input{Name: "Windeln", Dept: "baby", Kind: "nonfood", Unit1: "pack", Allergens: []string{"milk"}}
	if got := nonfood.Normalized(); len(got.Allergens) != 0 {
		t.Errorf("something that is not food keeps allergens: %v", got.Allergens)
	}
	messy := library.Input{Name: "  Hafer-\n Drink   Barista ", Dept: "drinks", Kind: "vegan", Unit1: "l", Allergens: []string{"soy", "gluten", "soy"}}
	n := messy.Normalized()
	if n.Name != "Hafer- Drink Barista" || !reflect.DeepEqual(n.Allergens, []string{"gluten", "soy"}) {
		t.Errorf("normalized = %+v", n)
	}
}
