package recipe_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/food"
	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/recipe"
)

func TestSixCategoriesWithASymbolEach(t *testing.T) {
	want := map[recipe.Category]string{"main": "🍲", "side": "🥗", "dessert": "🍮", "baking": "🥨", "breakfast": "🥣", "snack": "🥕"}
	if len(recipe.Categories) != 6 {
		t.Fatalf("categories = %v", recipe.Categories)
	}
	for _, c := range recipe.Categories {
		if c.Symbol() != want[c] {
			t.Errorf("%s symbol = %q", c, c.Symbol())
		}
		if !recipe.ValidCategory(string(c)) {
			t.Errorf("%s is a category", c)
		}
	}
	if recipe.ValidCategory("") || recipe.ValidCategory("soup") {
		t.Error("only the six are categories")
	}
	if recipe.Categories[0] != recipe.Main {
		t.Error("the main dish comes first and is the default")
	}
}

func TestAmountsAsPeopleTypeThem(t *testing.T) {
	for in, want := range map[string]float64{
		"2": 2, "0,5": 0.5, "0.5": 0.5, "½": 0.5, "1 1/2": 1.5, "1/2": 0.5, "1½": 1.5, "1 ½": 1.5, "¼": 0.25, "¾": 0.75, "⅓": 1.0 / 3, " 3 ": 3, "2 3/4": 2.75, "9999": 9999, "1,25": 1.25,
	} {
		got, err := recipe.ParseAmount(in)
		if err != nil || got < want-1e-9 || got > want+1e-9 {
			t.Errorf("ParseAmount(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"zwei", "0", "-1", "1/0", "10000", "1,,5", "1 1", "abc½", "1/", "/2"} {
		if _, err := recipe.ParseAmount(in); !errors.Is(err, recipe.ErrAmount) {
			t.Errorf("ParseAmount(%q) err = %v, want ErrAmount", in, err)
		}
	}
}

func TestAnAmountIsWrittenTheWayItIsRead(t *testing.T) {
	for _, c := range []struct {
		v          float64
		de, en, tx string
	}{{600, "600", "600", "600"}, {0.5, "0,5", "0.5", "0,5"}, {1.5, "1,5", "1.5", "1,5"}, {1.0 / 3, "0,33", "0.33", "0,33"}, {2.256, "2,26", "2.26", "2,26"}} {
		if got := recipe.FormatAmount(c.v, "de"); got != c.de {
			t.Errorf("de %v = %q", c.v, got)
		}
		if got := recipe.FormatAmount(c.v, "en"); got != c.en {
			t.Errorf("en %v = %q", c.v, got)
		}
	}
}

func TestIngredientsAreCheckedLineByLine(t *testing.T) {
	for _, c := range []struct {
		name string
		in   recipe.IngredientInput
		want recipe.Ingredient
		err  string // field and problem
	}{
		{"empty line is skipped", recipe.IngredientInput{}, recipe.Ingredient{}, "blank"},
		{"all spaces too", recipe.IngredientInput{Amount: " ", Name: "  "}, recipe.Ingredient{}, "blank"},
		{"plain", recipe.IngredientInput{Amount: "250", Unit: "g", Name: "Mehl"}, recipe.Ingredient{Amount: 250, Unit: "g", Text: "Mehl"}, ""},
		{"no unit", recipe.IngredientInput{Amount: "2", Name: "Eier"}, recipe.Ingredient{Amount: 2, Text: "Eier"}, ""},
		{"no quantity (salt)", recipe.IngredientInput{Name: "Salz"}, recipe.Ingredient{Text: "Salz"}, ""},
		{"a unit without a quantity means one", recipe.IngredientInput{Unit: "pinch", Name: "Salz"}, recipe.Ingredient{Amount: 1, Unit: "pinch", Text: "Salz"}, ""},
		{"a kitchen measure", recipe.IngredientInput{Amount: "3", Unit: "tbsp", Name: "Öl"}, recipe.Ingredient{Amount: 3, Unit: "tbsp", Text: "Öl"}, ""},
		{"a container", recipe.IngredientInput{Amount: "2", Unit: "can", Name: "Tomaten"}, recipe.Ingredient{Amount: 2, Unit: "can", Text: "Tomaten"}, ""},
		{"a fraction", recipe.IngredientInput{Amount: "1 1/2", Unit: "l", Name: "Milch"}, recipe.Ingredient{Amount: 1.5, Unit: "l", Text: "Milch"}, ""},
		{"spaces inside the name collapse", recipe.IngredientInput{Name: "  rote   Linsen "}, recipe.Ingredient{Text: "rote Linsen"}, ""},
		{"not a number", recipe.IngredientInput{Amount: "zwei", Name: "Eier"}, recipe.Ingredient{}, "amount"},
		{"a quantity without an ingredient", recipe.IngredientInput{Amount: "500", Unit: "ml"}, recipe.Ingredient{}, "name"},
		{"a unit without an ingredient", recipe.IngredientInput{Unit: "ml"}, recipe.Ingredient{}, "name"},
		{"an unknown unit", recipe.IngredientInput{Amount: "1", Unit: "stone", Name: "Mehl"}, recipe.Ingredient{}, "unit"},
		{"a name that is too long", recipe.IngredientInput{Name: strings.Repeat("x", 201)}, recipe.Ingredient{}, "name"},
		{"variety needs an article", recipe.IngredientInput{Name: "Mehl", Variety: "Type 405"}, recipe.Ingredient{Text: "Mehl"}, ""},
	} {
		got, err := recipe.CheckIngredient(c.in)
		if c.err == "" {
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Errorf("%s: got %+v, %v; want %+v", c.name, got, err, c.want)
			}
			continue
		}
		var fe *recipe.FieldError
		if c.err == "blank" {
			if !errors.Is(err, recipe.ErrBlank) {
				t.Errorf("%s: err = %v, want ErrBlank", c.name, err)
			}
			continue
		}
		if !errors.As(err, &fe) || fe.Field != c.err {
			t.Errorf("%s: err = %v, want field %q", c.name, err, c.err)
		}
	}
}

func TestTheNameIsTheOnlyRequiredField(t *testing.T) {
	r := recipe.Recipe{Name: "  Pfannkuchen ", Category: recipe.Main, Portions: 4}
	got, err := recipe.Check(r)
	if err != nil || got.Name != "Pfannkuchen" {
		t.Fatalf("got %+v, %v", got, err)
	}
	for _, c := range []struct {
		name  string
		mut   func(*recipe.Recipe)
		field string
	}{
		{"no name", func(r *recipe.Recipe) { r.Name = " " }, "name"},
		{"long name", func(r *recipe.Recipe) { r.Name = strings.Repeat("a", 121) }, "name"},
		{"unknown category", func(r *recipe.Recipe) { r.Category = "soup" }, "category"},
		{"no portions", func(r *recipe.Recipe) { r.Portions = 0 }, "portions"},
		{"too many portions", func(r *recipe.Recipe) { r.Portions = 100 }, "portions"},
		{"negative time", func(r *recipe.Recipe) { r.Minutes = -1 }, "minutes"},
		{"time too long", func(r *recipe.Recipe) { r.Minutes = 10000 }, "minutes"},
		{"unknown mealtime", func(r *recipe.Recipe) { r.Mealtimes = []string{"brunch"} }, "mealtimes"},
		{"long tip", func(r *recipe.Recipe) { r.Tips = strings.Repeat("a", 2001) }, "tips"},
		{"long source", func(r *recipe.Recipe) { r.Source = strings.Repeat("a", 301) }, "source"},
		{"long step", func(r *recipe.Recipe) { r.Steps = []string{strings.Repeat("a", 2001)} }, "steps"},
		{"long tag", func(r *recipe.Recipe) { r.Tags = []string{strings.Repeat("a", 31)} }, "tags"},
		{"too many ingredients", func(r *recipe.Recipe) { r.Ingredients = make([]recipe.Ingredient, 101) }, "ingredients"},
	} {
		r := recipe.Recipe{Name: "Pfannkuchen", Category: recipe.Main, Portions: 4}
		c.mut(&r)
		_, err := recipe.Check(r)
		var fe *recipe.FieldError
		if !errors.As(err, &fe) || fe.Field != c.field {
			t.Errorf("%s: err = %v, want field %q", c.name, err, c.field)
		}
	}
}

func TestCheckCleansTheOptionalParts(t *testing.T) {
	got, err := recipe.Check(recipe.Recipe{
		Name: "Curry", Category: recipe.Main, Portions: 4,
		Mealtimes: []string{"dinner", "lunch", "dinner"},
		Steps:     []string{" Zwiebel hacken. ", "", "   ", "Anbraten."},
		Tags:      []string{" schnell ", "Schnell", "scharf", ""},
		Tips:      "  Schmeckt am nächsten Tag besser.  ", Source: " Oma Gerda ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"lunch", "dinner"}; !reflect.DeepEqual(got.Mealtimes, want) {
		t.Errorf("mealtimes = %v: each once, in the order of the day", got.Mealtimes)
	}
	if want := []string{"Zwiebel hacken.", "Anbraten."}; !reflect.DeepEqual(got.Steps, want) {
		t.Errorf("steps = %q: empty ones are not saved", got.Steps)
	}
	if want := []string{"schnell", "scharf"}; !reflect.DeepEqual(got.Tags, want) {
		t.Errorf("tags = %q: once each, ignoring case", got.Tags)
	}
	if got.Tips != "Schmeckt am nächsten Tag besser." || got.Source != "Oma Gerda" {
		t.Errorf("tips %q, source %q", got.Tips, got.Source)
	}
}

func TestNamesAreTheSameIgnoringCaseAndSpaces(t *testing.T) {
	if recipe.NameKey(" Pfannkuchen  ") != recipe.NameKey("PFANNKUCHEN") || recipe.NameKey("Mehl  Brei") != recipe.NameKey("mehl brei") {
		t.Error("case and spaces do not make a second name")
	}
	if recipe.NameKey("Käsespätzle") == recipe.NameKey("Kasespatzle") {
		t.Error("umlauts are letters: Käse is not Kase")
	}
}

func TestNewTagsTakeTheSpellingThatIsAlreadyUsed(t *testing.T) {
	got := recipe.CanonTags([]string{"Schnell", "neu", "NEU", "Kinder Mögen es"}, []string{"schnell", "Kinder mögen es"})
	if want := []string{"schnell", "neu", "Kinder mögen es"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tags = %q, want %q", got, want)
	}
	if got := recipe.SplitTags(" sonntag, Sonntag ,für Gäste,, "); !reflect.DeepEqual(got, []string{"sonntag", "Sonntag", "für Gäste"}) {
		t.Errorf("split = %q", got)
	}
}

func TestASourceIsALinkOnlyWhenItIsAWebAddress(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.beispiel.de/rezept?id=3": "https://www.beispiel.de/rezept?id=3",
		"http://beispiel.de":                  "http://beispiel.de",
		"  https://beispiel.de/x  ":           "https://beispiel.de/x",
		"Oma Gerda":                           "", "Kochbuch „Asiatisch“, Seite 84": "",
		"javascript:alert(1)": "", "ftp://beispiel.de/x": "", "https://": "", "https://a b.de": "", "see https://beispiel.de": "",
	} {
		if got := recipe.SourceLink(in); got != want {
			t.Errorf("SourceLink(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestADietMarkOnlyWhenEveryArticleFits(t *testing.T) {
	art := func(id, name string, k library.Kind) library.Article {
		return library.Article{ID: id, De: name, En: name, Kind: k}
	}
	veg := art("flour", "Mehl", library.Vegan)
	egg := art("eggs", "Eier", library.Veg)
	butter := art("butter", "Butter", library.Veg)
	pork := art("pork", "Schweinefleisch", library.Meat)
	salt := art("salt", "Salz", library.NonFood)
	free := recipe.Ingredient{Text: "Currypaste"}
	at := func(a library.Article) recipe.Ingredient { return recipe.Ingredient{Text: a.De, ArticleID: a.ID} }
	byID := map[string]library.Article{"flour": veg, "eggs": egg, "butter": butter, "pork": pork, "salt": salt}
	lookup := func(id string) (library.Article, bool) { a, ok := byID[id]; return a, ok }

	d := recipe.DietOf([]recipe.Ingredient{at(veg), at(salt), free}, lookup)
	if d.Mark != recipe.MarkVegan || !reflect.DeepEqual(d.Free, []string{"Currypaste"}) || len(d.Prevents) != 0 {
		t.Errorf("vegan with free text: %+v", d)
	}
	d = recipe.DietOf([]recipe.Ingredient{at(veg), at(egg), at(butter), at(egg)}, lookup)
	if d.Mark != recipe.MarkVegetarian || !reflect.DeepEqual(d.Prevents, []string{"Eier", "Butter"}) {
		t.Errorf("vegetarian names what prevents vegan, each once: %+v", d)
	}
	d = recipe.DietOf([]recipe.Ingredient{at(veg), at(pork), at(egg)}, lookup)
	if d.Mark != "" || !reflect.DeepEqual(d.Prevents, []string{"Schweinefleisch"}) {
		t.Errorf("meat: no mark, and it names the meat: %+v", d)
	}
	if d := recipe.DietOf([]recipe.Ingredient{free}, lookup); d.Mark != "" || len(d.Prevents) != 0 || !reflect.DeepEqual(d.Free, []string{"Currypaste"}) {
		t.Errorf("only free text gets no mark: %+v", d)
	}
	if d := recipe.DietOf(nil, lookup); d.Mark != "" {
		t.Errorf("no ingredients: %+v", d)
	}
	gone := recipe.Ingredient{Text: "Altes", ArticleID: "deleted-article"}
	if d := recipe.DietOf([]recipe.Ingredient{at(veg), gone}, lookup); d.Mark != recipe.MarkVegan || !reflect.DeepEqual(d.Free, []string{"Altes"}) {
		t.Errorf("an article that is gone is unknown, like free text: %+v", d)
	}
}

func TestOneWarningPerAllergenWithEveryoneNamed(t *testing.T) {
	flour := library.Article{ID: "flour", Allergens: []string{"gluten"}, Kind: library.Vegan}
	egg := library.Article{ID: "eggs", Allergens: []string{"eggs"}, Kind: library.Veg}
	cheese := library.Article{ID: "cheese", Allergens: []string{"milk"}, Kind: library.Veg}
	butter := library.Article{ID: "butter", Allergens: []string{"milk"}, Kind: library.Veg}
	people := []library.Person{
		{ID: "mama", Allergens: nil},
		{ID: "lotta", Diet: food.Omnivore, Allergens: []string{"gluten", "milk"}},
		{ID: "jonas", Allergens: []string{"eggs"}},
		{ID: "cs", Allergens: []string{"eggs"}},
		{ID: "ben", Allergens: []string{"milk"}},
	}
	got := recipe.Warnings([]library.Article{flour, egg, cheese, butter, egg}, people)
	want := []library.Warning{
		{People: []string{"lotta"}, Allergens: []string{"gluten"}},
		{People: []string{"jonas", "cs"}, Allergens: []string{"eggs"}},
		{People: []string{"lotta", "ben"}, Allergens: []string{"milk"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("warnings = %+v\nwant %+v", got, want)
	}
	if got := recipe.Warnings(nil, people); len(got) != 0 {
		t.Errorf("no articles, no warning: %+v", got)
	}
	nonfood := library.Article{ID: "paper", Kind: library.NonFood, Allergens: []string{"gluten"}}
	if got := recipe.Warnings([]library.Article{nonfood}, people); len(got) != 0 {
		t.Errorf("something that is not food warns nobody: %+v", got)
	}
}

func TestSearchFindsNameIngredientAndTag(t *testing.T) {
	r := recipe.Recipe{Name: "Käsespätzle", Tags: []string{"Sonntagsessen"}, Ingredients: []recipe.Ingredient{{Text: "Bergkäse"}, {Text: "Zwiebeln"}}}
	for _, q := range []string{"spätzle", "SPATZLE", "zwiebel", "sonntag", "bergkase", "  käse "} {
		if !recipe.Matches(r, q) {
			t.Errorf("%q should find the recipe", q)
		}
	}
	for _, q := range []string{"lasagne", "oma"} {
		if recipe.Matches(r, q) {
			t.Errorf("%q should not find it", q)
		}
	}
	if !recipe.Matches(r, "") || !recipe.Matches(r, "   ") {
		t.Error("an empty search finds everything")
	}
}

func TestRecipesAreSortedFromAToZIgnoringCaseAndUmlauts(t *testing.T) {
	list := []recipe.Recipe{{Name: "Zucchinipfanne"}, {Name: "äpfel im Schlafrock"}, {Name: "Apfelkuchen"}, {Name: "Bohnensuppe"}, {Name: "apfelmus"}}
	recipe.SortByName(list)
	var names []string
	for _, r := range list {
		names = append(names, r.Name)
	}
	if want := []string{"äpfel im Schlafrock", "Apfelkuchen", "apfelmus", "Bohnensuppe", "Zucchinipfanne"}; !reflect.DeepEqual(names, want) {
		t.Errorf("order = %q", names)
	}
}

func TestMinutesInWords(t *testing.T) {
	for in, want := range map[int][3]int{45: {0, 45, 0}, 150: {2, 30, 0}, 60: {1, 0, 0}, 5: {0, 5, 0}} {
		h, m := recipe.HoursMinutes(in)
		if h != want[0] || m != want[1] {
			t.Errorf("%d = %d h %d min", in, h, m)
		}
	}
}
