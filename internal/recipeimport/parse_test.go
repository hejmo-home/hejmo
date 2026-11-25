package recipeimport_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/hejmo-home/hejmo/internal/recipeimport"
)

func page(ld string) []byte {
	return []byte(`<html><head><title>x</title><script type="application/ld+json">` + ld + `</script></head><body>hi</body></html>`)
}

func one(t *testing.T, ld string) recipeimport.Found {
	t.Helper()
	got, err := recipeimport.Extract(page(ld))
	if err != nil || len(got) != 1 {
		t.Fatalf("Extract: %v, %d recipes", err, len(got))
	}
	return got[0]
}

func TestExtractNoRecipe(t *testing.T) {
	for name, body := range map[string]string{
		"no script":   `<html><body>nothing</body></html>`,
		"other type":  `<html><script type="application/ld+json">{"@type":"Article","name":"x"}</script></html>`,
		"broken json": `<html><script type="application/ld+json">{"@type":</script></html>`,
	} {
		if _, err := recipeimport.Extract([]byte(body)); !errors.Is(err, recipeimport.ErrNoRecipe) {
			t.Errorf("%s: err = %v, want ErrNoRecipe", name, err)
		}
	}
}

func TestExtractGraphAndLists(t *testing.T) {
	got, err := recipeimport.Extract(page(`{"@context":"https://schema.org","@graph":[{"@type":"WebSite","name":"s"},{"@type":["Recipe","Thing"],"name":"A","recipeIngredient":["1 Ei"]},{"@type":"Recipe","name":"B"}]}`))
	if err != nil || len(got) != 2 || got[0].Name != "A" || got[1].Name != "B" {
		t.Fatalf("graph: %v %+v", err, got)
	}
	got, err = recipeimport.Extract(page(`[{"@type":"Recipe","name":"C"}]`))
	if err != nil || len(got) != 1 || got[0].Name != "C" {
		t.Fatalf("list: %v %+v", err, got)
	}
}

func TestExtractBasics(t *testing.T) {
	f := one(t, `{"@type":"Recipe","name":"Rote &amp; Linsen","recipeYield":"4 Portionen","totalTime":"PT1H30M","recipeCategory":"Suppe","keywords":"Suppe, vegetarisch ,Linsen","inLanguage":"de-DE","image":{"@type":"ImageObject","url":"/a.jpg"}}`)
	if f.Name != "Rote & Linsen" || f.Portions != 4 || !f.PortionsGiven || f.Minutes != 90 {
		t.Errorf("basics: %+v", f)
	}
	if f.Category != "main" || f.CategoryWord != "Suppe" {
		t.Errorf("category %q word %q", f.Category, f.CategoryWord)
	}
	if !reflect.DeepEqual(f.Keywords, []string{"Suppe", "vegetarisch", "Linsen"}) || f.Lang != "de" || f.ImageURL != "/a.jpg" {
		t.Errorf("rest: %+v", f)
	}
	if f = one(t, `{"@type":"Recipe","name":"x","recipeCategory":["Nachtisch"],"prepTime":"PT20M","cookTime":"PT10M","image":["/b.jpg","/c.jpg"],"keywords":["a","b"]}`); f.Category != "dessert" || f.Minutes != 30 || f.ImageURL != "/b.jpg" || len(f.Keywords) != 2 {
		t.Errorf("second: %+v", f)
	}
}

func TestYield(t *testing.T) {
	cases := []struct {
		in       string
		n        int
		given    bool
		fallback bool
	}{
		{`"4"`, 4, true, false}, {`4`, 4, true, false}, {`["6","6 Portionen"]`, 6, true, false}, {`"2 Personen"`, 2, true, false},
		{`"1 Blech (12 Stücke)"`, 12, false, false}, {`"ergibt viele"`, 0, false, true}, {`"12 Stück"`, 12, false, false},
	}
	for _, c := range cases {
		f := one(t, `{"@type":"Recipe","name":"x","recipeYield":`+c.in+`}`)
		if f.Portions != c.n || f.PortionsGiven != c.given {
			t.Errorf("%s: portions %d given %v, want %d %v", c.in, f.Portions, f.PortionsGiven, c.n, c.given)
		}
		if c.fallback && f.YieldText == "" {
			t.Errorf("%s: no yield text", c.in)
		}
	}
	if f := one(t, `{"@type":"Recipe","name":"x"}`); f.Portions != 0 || f.PortionsGiven || f.YieldText != "" {
		t.Errorf("none: %+v", f)
	}
}

func TestStepsPlainTextAndSections(t *testing.T) {
	f := one(t, `{"@type":"Recipe","name":"x","recipeInstructions":[
		{"@type":"HowToSection","name":"Für den Teig","itemListElement":[{"@type":"HowToStep","text":"Mehl <b>sieben</b>."},{"@type":"HowToStep","name":"Kneten"}]},
		{"@type":"HowToStep","text":"Backen &amp;amp; servieren<style>x</style>"},
		"Fertig"]}`)
	want := []string{"Für den Teig: Mehl sieben.", "Für den Teig: Kneten", "Backen &amp; servieren", "Fertig"}
	if !reflect.DeepEqual(f.Steps, want) {
		t.Errorf("steps = %q, want %q", f.Steps, want)
	}
	f = one(t, `{"@type":"Recipe","name":"x","recipeInstructions":"<ol><li>Eins</li><li>Zwei &lt;3</li></ol>"}`)
	if !reflect.DeepEqual(f.Steps, []string{"Eins", "Zwei <3"}) {
		t.Errorf("html string steps = %q", f.Steps)
	}
	f = one(t, `{"@type":"Recipe","name":"x","recipeInstructions":"Eins.\nZwei."}`)
	if len(f.Steps) != 2 {
		t.Errorf("lines = %q", f.Steps)
	}
}

func TestNutrition(t *testing.T) {
	f := one(t, `{"@type":"Recipe","name":"x","nutrition":{"@type":"NutritionInformation","calories":"380 kcal","fatContent":"17 g","saturatedFatContent":"9,6 g","carbohydrateContent":"38 g","sugarContent":"8.2 g","proteinContent":"17 g","sodiumContent":"800 mg"}}`)
	n := f.Nutrition
	if n == nil || n.KCal == nil || *n.KCal != 380 || *n.Saturates != 9.6 || *n.Sugars != 8.2 || *n.Salt != 2 {
		t.Fatalf("nutrition = %+v", n)
	}
	if n.KJ == nil || int(*n.KJ+0.5) != 1590 {
		t.Errorf("kJ derived from kcal: %v", n.KJ)
	}
	f = one(t, `{"@type":"Recipe","name":"x","nutrition":{"calories":"380 kcal","fatContent":"17 g"}}`)
	if f.Nutrition.Salt != nil || f.Nutrition.Protein != nil {
		t.Errorf("missing values must stay nil: %+v", f.Nutrition)
	}
	if f = one(t, `{"@type":"Recipe","name":"x","nutrition":{"@type":"NutritionInformation","servingSize":"1 Portion"}}`); f.Nutrition != nil {
		t.Errorf("no values at all: %+v", f.Nutrition)
	}
}

func TestParseLine(t *testing.T) {
	cases := []struct {
		in           string
		amount       float64
		unit, name   string
		rest, change string
	}{
		{"250 g Mehl (Type 405)", 250, "g", "Mehl", "Type 405", ""},
		{"0 g Backpulver", 0, "", "Backpulver", "", "zero"},
		{"1 1/2 TL Zimt", 1.5, "tsp", "Zimt", "", ""},
		{"½ TL Kreuzkümmel, gemahlen", 0.5, "tsp", "Kreuzkümmel", "gemahlen", ""},
		{"1.000 g Äpfel, säuerlich (z. B. Boskop)", 1000, "g", "Äpfel", "säuerlich (z. B. Boskop)", ""},
		{"0,5 l Milch", 0.5, "l", "Milch", "", ""},
		{"1–2 TL Harissa (scharfe Paste)", 1, "tsp", "Harissa", "scharfe Paste", "range"},
		{"1-2 EL Öl", 1, "tbsp", "Öl", "", "range"},
		{"2 große Eier", 2, "", "große Eier", "", ""},
		{"etwas Salz", 0, "", "Salz", "", "some"},
		{"1 Dose Tomaten (400 ml)", 1, "can", "Tomaten", "", ""},
		{"1.5 cups flour", 1.5, "cup", "flour", "", ""},
		{"2 Zehen Knoblauch", 2, "clove", "Knoblauch", "", ""},
		{"Salz", 0, "", "Salz", "", ""},
		{"500g Kartoffeln", 500, "g", "Kartoffeln", "", ""},
		{"¾ l Wasser", 0.75, "l", "Wasser", "", ""},
		{"1 ½ EL Honig", 1.5, "tbsp", "Honig", "", ""},
	}
	for _, c := range cases {
		l := recipeimport.ParseLine(c.in)
		if l.Original != c.in || l.Amount != c.amount || l.Unit != c.unit || l.Head != c.name || l.Rest != c.rest || l.Change != c.change {
			t.Errorf("%q = %+v, want amount %v unit %q head %q rest %q change %q", c.in, l, c.amount, c.unit, c.name, c.rest, c.change)
		}
	}
	if l := recipeimport.ParseLine("250 g Mehl (Type 405)"); l.Name() != "Mehl, Type 405" {
		t.Errorf("Name() = %q", l.Name())
	}
}

func TestYieldFallbackAndTags(t *testing.T) {
	if got := recipeimport.CategoryOf("Beilage"); got != "side" {
		t.Errorf("Beilage = %q", got)
	}
	if got := recipeimport.CategoryOf("Suppe"); got != "" {
		t.Errorf("unknown = %q", got)
	}
}
