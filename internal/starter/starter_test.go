package starter_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/starter"
	"github.com/hejmo-home/hejmo/internal/unit"
)

var langs = []string{"de", "en"}

// Acceptance: all 200 recipes load, in both languages.
func TestAllTwoHundredRecipesLoadInBothLanguages(t *testing.T) {
	all := starter.All()
	if len(all) != 200 {
		t.Fatalf("%d starter recipes, want 200", len(all))
	}
	ids := map[string]bool{}
	for i, e := range all {
		if e.Number != i+1 {
			t.Errorf("entry %d has number %d", i, e.Number)
		}
		for _, lang := range langs {
			r := e.Recipe(lang)
			if ids[r.ID+lang] || !starter.IsID(r.ID) {
				t.Errorf("%s: id %q is not a unique starter id", e.Slug, r.ID)
			}
			ids[r.ID+lang] = true
			checked, err := recipe.Check(r)
			if err != nil {
				t.Errorf("%s (%s): %v", e.Slug, lang, err)
			}
			if len(checked.Steps) != len(r.Steps) || len(r.Steps) == 0 || r.Name == "" || r.Minutes <= 0 || r.Portions < 1 {
				t.Errorf("%s (%s): name %q, %d steps, %d min, %d portions", e.Slug, lang, r.Name, len(r.Steps), r.Minutes, r.Portions)
			}
		}
		if len(e.Recipe("de").Steps) != len(e.Recipe("en").Steps) || len(e.Recipe("de").Ingredients) != len(e.Recipe("en").Ingredients) {
			t.Errorf("%s: German and English differ in steps or ingredients", e.Slug)
		}
	}
}

func TestTheLanguageOfTheViewerDecidesTheTexts(t *testing.T) {
	e, ok := starter.ByID("starter-rahmspinat")
	if !ok {
		t.Fatal("recipe 14 not found by its id")
	}
	de, en := e.Recipe("de"), e.Recipe("en")
	if de.Name != "Rahmspinat" || en.Name != "Creamed spinach" {
		t.Errorf("titles = %q, %q", de.Name, en.Name)
	}
	if !strings.HasPrefix(de.Steps[0], "Den Spinat waschen") || !strings.HasPrefix(en.Steps[0], "Wash the spinach") {
		t.Errorf("steps = %q, %q", de.Steps[0], en.Steps[0])
	}
	if e.Recipe("fr").Name != "Rahmspinat" {
		t.Error("an unknown language shows German")
	}
}

// Product owner, 24/11/2026: recipe 14 carries a tip in German and English.
func TestRecipe14CarriesATipInBothLanguages(t *testing.T) {
	e, _ := starter.ByID("starter-rahmspinat")
	if got := e.Recipe("de").Tips; got != "Dazu passen Salzkartoffeln und Spiegelei." {
		t.Errorf("German tip = %q", got)
	}
	if got := e.Recipe("en").Tips; got != "Serve with boiled potatoes and fried eggs." {
		t.Errorf("English tip = %q", got)
	}
	n := 0
	for _, x := range starter.All() {
		if x.Recipe("de").Tips != "" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d recipes with a tip, want only recipe 14", n)
	}
}

// Product owner, 24/11/2026 (#279): the unit spellings of the files map to the recipe units.
func TestUnitSpellingsMapToRecipeUnits(t *testing.T) {
	for spelling, want := range map[string]string{
		"Pck.": "packet", "Pck": "packet", "Packung": "packet", "Päckchen": "packet",
		"l": "l", "Liter": "l", "Stk": "piece", "Stück": "piece", "Msp.": "knifetip",
		"Topf": "pot", "Töpfchen": "pot", "EL": "tbsp", "TL": "tsp", "Prise": "pinch", "Schuss": "dash",
		"Spritzer": "splash", "Handvoll": "handful", "Tasse": "cup", "Dose": "can", "Becher": "tub", "Bund": "bunch",
		"Glas": "jar", "Flasche": "bottle", "Stange": "stick", "Zehe": "clove", "Scheibe": "slice", "Würfel": "cube",
		"Zweig": "sprig", "Stiel": "stalk", "Kugel": "ball", "Blatt": "leaf", "Beet": "punnet", "g": "g", "kg": "kg", "ml": "ml",
	} {
		got, amount, ok := starter.MapUnit(spelling, 3)
		if !ok || got != want || amount != 3 {
			t.Errorf("%q = %q, %v, %v; want %q", spelling, got, amount, ok, want)
		}
	}
	// A centilitre is ten millilitres; a length of ginger is one piece. Neither is free text.
	if u, a, ok := starter.MapUnit("cl", 2); !ok || u != "ml" || a != 20 {
		t.Errorf("2 cl = %v %q %v", a, u, ok)
	}
	if u, a, ok := starter.MapUnit("cm", 5); !ok || u != "piece" || a != 1 {
		t.Errorf("5 cm = %v %q %v", a, u, ok)
	}
	if _, _, ok := starter.MapUnit("Fass", 1); ok {
		t.Error("an unknown spelling must not map")
	}
}

// Acceptance: no ingredient ends up as free text because of its unit.
func TestNoIngredientIsFreeTextBecauseOfItsUnit(t *testing.T) {
	for _, e := range starter.All() {
		for _, lang := range langs {
			for i, in := range e.Recipe(lang).Ingredients {
				if in.Unit != "" && !unit.ValidForRecipe(in.Unit) {
					t.Errorf("%s (%s) line %d: unit %q is no recipe unit", e.Slug, lang, i, in.Unit)
				}
				if in.Text == "" {
					t.Errorf("%s (%s) line %d has no text", e.Slug, lang, i)
				}
			}
		}
	}
	if raw := starter.UnmappedUnits(); len(raw) != 0 {
		t.Errorf("unit spellings without a mapping: %v", raw)
	}
}

// Acceptance: ingredients map to library articles where possible, else free text.
func TestIngredientsAreLibraryArticlesOrFreeText(t *testing.T) {
	articles, free := 0, 0
	for _, e := range starter.All() {
		de, en := e.Recipe("de"), e.Recipe("en")
		for i, in := range de.Ingredients {
			if in.ArticleID == "" {
				free++
				continue
			}
			articles++
			_, ok := library.ShippedByID(in.ArticleID)
			if !ok {
				t.Errorf("%s line %d: article %q is not shipped", e.Slug, i, in.ArticleID)
				continue
			}
			if en.Ingredients[i].ArticleID != in.ArticleID {
				t.Errorf("%s line %d: German says %s, English %s", e.Slug, i, in.ArticleID, en.Ingredients[i].ArticleID)
			}
			if in.Variety != "" || en.Ingredients[i].Variety != "" || !strings.ContainsAny(library.Fold(in.Text), "abcdefghijklmnopqrstuvwxyz") {
				t.Errorf("%s line %d: %+v for %s", e.Slug, i, in, in.ArticleID)
			}
		}
	}
	t.Logf("%d article lines, %d free text", articles, free)
	if articles < 800 {
		t.Errorf("only %d article lines", articles)
	}
	e, _ := starter.ByID("starter-rahmspinat")
	if got := e.Recipe("de").Ingredients[0]; got.ArticleID != "" || got.Text != "Blattspinat" {
		t.Errorf("spinach line = %+v", got)
	}
	got := e.Recipe("en").Ingredients
	if got[1].ArticleID != "butter" || got[1].Amount != 25 || got[1].Unit != "g" {
		t.Errorf("butter line = %+v", got[1])
	}
	if got[2].Unit != "tbsp" || got[2].ArticleID != "" {
		t.Errorf("flour line = %+v", got[2])
	}
	if got[5].Amount != 0 || got[5].Unit != "" || got[5].ArticleID != "salt" {
		t.Errorf("salt line = %+v", got[5])
	}
}

// The notes of the files ("chopped", "optional") are kept behind the name.
func TestIngredientNotesStayBehindTheName(t *testing.T) {
	found := false
	for _, e := range starter.All() {
		for _, in := range e.Recipe("en").Ingredients {
			if strings.Contains(in.Text, "(optional)") {
				found = true
			}
		}
	}
	if !found {
		t.Error("no ingredient carries its note")
	}
}

// Acceptance: the source credit and link show on every starter recipe.
func TestEveryRecipeNamesItsSourceWithALink(t *testing.T) {
	for _, e := range starter.All() {
		if e.Site == "" || recipe.SourceLink(e.URL) == "" {
			t.Errorf("%s: source %q %q", e.Slug, e.Site, e.URL)
		}
		if got := e.Recipe("de").Source; got != e.URL {
			t.Errorf("%s: source = %q", e.Slug, got)
		}
	}
}

func TestCategoriesAndSoupTags(t *testing.T) {
	counts := map[recipe.Category]int{}
	for _, e := range starter.All() {
		counts[e.Recipe("de").Category]++
	}
	want := map[recipe.Category]int{recipe.Main: 126, recipe.Baking: 20, recipe.Breakfast: 15, recipe.Dessert: 15, recipe.Side: 14, recipe.Snack: 10}
	for k, n := range want {
		if counts[k] != n {
			t.Errorf("%s: %d recipes, want %d", k, counts[k], n)
		}
	}
	soups := 0
	for _, e := range starter.All() {
		if slices.Contains(e.Recipe("en").Tags, "Soup") && slices.Contains(e.Recipe("de").Tags, "Suppe") {
			soups++
		}
	}
	if soups != 15 {
		t.Errorf("%d soups tagged, want 15", soups)
	}
}

func TestByIDFindsOnlyStarterRecipes(t *testing.T) {
	if _, ok := starter.ByID("rahmspinat"); ok || starter.IsID("rahmspinat") {
		t.Error("an own recipe's id is no starter id")
	}
	if _, ok := starter.ByID("starter-gibt-es-nicht"); ok {
		t.Error("unknown starter id found")
	}
}

// "Water or milk" is no article: a milk allergy must not be missed because the first word is water.
func TestALineWithAnAlternativeStaysFreeText(t *testing.T) {
	for _, e := range starter.All() {
		for _, in := range e.Recipe("de").Ingredients {
			if in.ArticleID != "" && (strings.Contains(in.Text, " oder ") || strings.Contains(in.Text, " und ")) && !strings.Contains(in.Text, "(") {
				t.Errorf("%s: %q is an article", e.Slug, in.Text)
			}
		}
	}
}
