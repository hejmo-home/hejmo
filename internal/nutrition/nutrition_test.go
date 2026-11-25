package nutrition

import (
	"math"
	"os"
	"strings"
	"testing"
)

func vals(kj, kcal, fat, sat, carbs, sugars, protein, salt float64) Values {
	var v Values
	for k, x := range []float64{kj, kcal, fat, sat, carbs, sugars, protein, salt} {
		if !math.IsNaN(x) {
			v.Set(Key(k), x)
		}
	}
	return v
}

var nan = math.NaN()

func food(code string, v Values) *Food {
	return &Food{Source: BLS, Code: code, De: "Essen " + code, Per100: v}
}

func TestMissingValueIsNotZero(t *testing.T) {
	var v Values
	if x, ok := v.Get(Sugars); ok || x != 0 {
		t.Fatalf("a value never set must be missing, got %v %v", x, ok)
	}
	v.Set(Sugars, 0)
	if x, ok := v.Get(Sugars); !ok || x != 0 {
		t.Fatal("a value set to a real 0 is known and 0")
	}
	if _, ok := v.Get(Fat); ok {
		t.Fatal("setting sugars must not set fat")
	}
}

func TestGrams(t *testing.T) {
	cases := []struct {
		amount         float64
		unit           string
		pieceG, gPerML float64
		want           float64
		ok             bool
	}{
		{200, "g", 0, 0, 200, true},
		{1.5, "kg", 0, 0, 1500, true},
		{2, "oz", 0, 0, 56.699, true},
		{500, "ml", 0, 1.03, 515, true},
		{500, "ml", 0, 0, 0, false}, // no density known
		{1, "l", 0, 0.92, 920, true},
		{3, "tbsp", 0, 0.9, 40.5, true}, // 15 ml
		{2, "tsp", 0, 0.9, 9, true},     // 5 ml
		{1, "pinch", 0, 1.2, 0.36, true},
		{1, "tbsp", 0, 0, 0, false},
		{3, "piece", 60, 0, 180, true},
		{3, "", 60, 0, 180, true}, // no unit counts pieces
		{1, "dozen", 60, 0, 720, true},
		{1, "jar", 200, 0, 200, true},
		{2, "tub", 150, 0, 300, true},
		{3, "piece", 0, 0, 0, false},
		{1, "handful", 30, 1, 0, false}, // never converts
		{1, "knifetip", 30, 1, 0, false},
		{1, "dash", 30, 1, 0, false},
		{1, "splash", 30, 1, 0, false},
		{1, "cup", 30, 1, 0, false},
		{2, "clove", 30, 1, 0, false},
		{1, "pack", 30, 1, 0, false},
		{1, "can", 30, 1, 0, false},
	}
	for _, c := range cases {
		got, ok := Grams(c.amount, c.unit, c.pieceG, c.gPerML)
		if ok != c.ok || math.Abs(got-c.want) > 0.01 {
			t.Errorf("Grams(%v %q, piece %v, density %v) = %v, %v; want %v, %v", c.amount, c.unit, c.pieceG, c.gPerML, got, ok, c.want, c.ok)
		}
	}
}

func TestCalculateSumsPerPortion(t *testing.T) {
	flour := food("F", vals(1500, 360, 1.5, 0.2, 73, 1, 10, 0.01))
	milk := food("M", vals(270, 64, 3.5, 2.3, 4.8, 4.8, 3.4, 0.11))
	r := Calculate(4, []Line{
		{Name: "Mehl", Amount: 400, Unit: "g", Food: flour},
		{Name: "Milch", Amount: 500, Unit: "ml", Food: milk, GPerML: 1.03},
	})
	if r.Counted != 2 || len(r.Skipped) != 0 || len(r.Gaps) != 0 {
		t.Fatalf("result = %+v", r)
	}
	// 400 g flour and 500 ml milk of 1.03 g/ml (515 g), shared by 4 portions.
	kcal, ok := r.Values.Get(KCal)
	if !ok || math.Abs(kcal-(360*4+64*5.15)/4) > 0.001 {
		t.Fatalf("kcal per portion = %v %v", kcal, ok)
	}
	fat, _ := r.Values.Get(Fat)
	if math.Abs(fat-(1.5*4+3.5*5.15)/4) > 0.001 {
		t.Fatalf("fat = %v", fat)
	}
	if len(r.Sources) != 1 || r.Sources[0] != BLS {
		t.Fatalf("sources = %v", r.Sources)
	}
}

func TestCalculateNamesWhatIsNotCounted(t *testing.T) {
	salt := food("S", vals(0, 0, 0, 0, 0, 0, 0, 100))
	oats := food("O", vals(1500, 360, 7, 1.3, 59, 1, 13, 0.01))
	r := Calculate(2, []Line{
		{Name: "Hafer", Amount: 100, Unit: "g", Food: oats},
		{Name: "Gewürz", Amount: 1, Unit: "g", Free: true},
		{Name: "Etwas Gewürz", Free: true},
		{Name: "Salz", ArticleID: "salt", Food: salt}, // no quantity
		{Name: "Heidelbeeren", Amount: 1, Unit: "handful", ArticleID: "blueberries", Food: oats},
		{Name: "Pflaumenmus", Amount: 1, Unit: "piece", ArticleID: "own-1", Own: true}, // own article without food
		{Name: "Ei", Amount: 2, Unit: "piece", ArticleID: "eggs", Food: oats},          // no piece weight known
	})
	got := map[string]Reason{}
	for _, s := range r.Skipped {
		got[s.Name] = s.Reason
	}
	want := map[string]Reason{"Gewürz": ReasonFree, "Etwas Gewürz": ReasonFreeNoAmount, "Salz": ReasonNoAmount, "Heidelbeeren": ReasonNoWeight, "Pflaumenmus": ReasonNoFood, "Ei": ReasonNoWeight}
	if len(got) != len(want) {
		t.Fatalf("skipped = %v", r.Skipped)
	}
	for n, w := range want {
		if got[n] != w {
			t.Errorf("%s: reason %v, want %v", n, got[n], w)
		}
	}
	if r.Counted != 1 {
		t.Fatalf("counted = %d", r.Counted)
	}
	// Only the oats count: 100 g over 2 portions.
	if kcal, _ := r.Values.Get(KCal); math.Abs(kcal-180) > 0.001 {
		t.Fatalf("kcal = %v, want 180", kcal)
	}
	for _, s := range r.Skipped {
		if s.Name == "Heidelbeeren" && s.Unit != "handful" {
			t.Fatalf("the reason names the unit: %+v", s)
		}
		if s.Name == "Pflaumenmus" && s.ArticleID != "own-1" {
			t.Fatalf("the own article is named so the page can link to it: %+v", s)
		}
	}
}

func TestACountedFoodThatLacksAValueMakesTheSumUnknown(t *testing.T) {
	a := food("A", vals(500, 120, 2, 1, 20, nan, 5, 0.1))
	b := food("B", vals(300, 70, 1, nan, 10, 4, 3, 0.05))
	a.Per100.Set(Sugars, 3) // A has sugars
	b.De = "Haferdrink"
	r := Calculate(1, []Line{{Name: "a", Amount: 100, Unit: "g", Food: a}, {Name: "b", Amount: 100, Unit: "g", Food: b}})
	if _, ok := r.Values.Get(Saturates); ok {
		t.Fatal("saturates is missing for B, so the total is unknown, not A's value and never 0")
	}
	if _, ok := r.Values.Get(Fat); !ok {
		t.Fatal("fat is known for both")
	}
	if len(r.Gaps) != 1 || r.Gaps[0].Key != Saturates || len(r.Gaps[0].Foods) != 1 || r.Gaps[0].Foods[0].Code != "B" {
		t.Fatalf("gaps = %+v", r.Gaps)
	}
}

func TestNothingCountedHasNoValues(t *testing.T) {
	r := Calculate(4, []Line{{Name: "Wurzel", Free: true, Amount: 3, Unit: "g"}})
	if r.Counted != 0 {
		t.Fatal("nothing counted")
	}
	for k := Key(0); k < NumKeys; k++ {
		if _, ok := r.Values.Get(k); ok {
			t.Fatalf("value %d must be missing when nothing is counted", k)
		}
	}
	if len(r.Sources) != 0 {
		t.Fatalf("no source is named when nothing is counted: %v", r.Sources)
	}
}

func TestEnergyIsTwoValuesOneGap(t *testing.T) {
	a := food("A", vals(nan, nan, 1, 1, 1, 1, 1, 1))
	r := Calculate(1, []Line{{Name: "a", Amount: 100, Unit: "g", Food: a}})
	if _, ok := r.Values.Get(KCal); ok {
		t.Fatal("no energy")
	}
	energy := 0
	for _, g := range r.Gaps {
		if g.Key == KCal || g.Key == KJ {
			energy++
		}
	}
	if energy != 1 {
		t.Fatalf("energy is one gap, got %+v", r.Gaps)
	}
}

func TestSourcesAreOnlyTheOnesUsed(t *testing.T) {
	u := &Food{Source: USDA, Code: "1", En: "x", Per100: vals(1, 1, 1, 1, 1, 1, 1, 1)}
	b := food("B", vals(1, 1, 1, 1, 1, 1, 1, 1))
	r := Calculate(1, []Line{{Name: "u", Amount: 10, Unit: "g", Food: u}, {Name: "b", Amount: 10, Unit: "g", Food: b}, {Name: "skip", Free: true}})
	if len(r.Sources) != 2 || r.Sources[0] != BLS || r.Sources[1] != USDA {
		t.Fatalf("sources = %v", r.Sources)
	}
	r = Calculate(1, []Line{{Name: "u", Amount: 10, Unit: "g", Food: u}})
	if len(r.Sources) != 1 || r.Sources[0] != USDA {
		t.Fatalf("sources = %v", r.Sources)
	}
}

func TestFormat(t *testing.T) {
	cases := []struct {
		key  Key
		v    float64
		lang string
		want string
	}{
		{KCal, 340.4, "de", "340 kcal"},
		{KCal, 1421.6, "de", "1.422 kcal"},
		{KJ, 1421, "de", "1.421 kJ"},
		{KJ, 1421, "en", "1,421 kJ"},
		{Fat, 16.4, "de", "16 g"},
		{Fat, 9.96, "de", "10 g"},
		{Fat, 8.34, "de", "8,3 g"},
		{Fat, 8.34, "en", "8.3 g"},
		{Fat, 0.5, "de", "0,5 g"},
		{Fat, 0.49, "de", "unter 0,5 g"},
		{Fat, 0, "de", "unter 0,5 g"},
		{Fat, 0.2, "en", "under 0.5 g"},
		{Salt, 0.43, "de", "0,4 g"},
		{Salt, 12.34, "de", "12,3 g"},
		{Salt, 0.09, "de", "unter 0,1 g"},
		{Salt, 0.04, "en", "under 0.1 g"},
		{Sugars, 10.5, "de", "11 g"},
	}
	for _, c := range cases {
		if got := Format(c.key, c.v, c.lang); got != c.want {
			t.Errorf("Format(%d, %v, %s) = %q, want %q", c.key, c.v, c.lang, got, c.want)
		}
	}
}

func TestEmbeddedFoods(t *testing.T) {
	if Count() < 14000 {
		t.Fatalf("only %d foods", Count())
	}
	f, ok := Lookup("bls:B101000")
	if !ok || f.De != "Vollkornbrot" || f.En != "Wholemeal bread" || f.Source != BLS {
		t.Fatalf("bls:B101000 = %+v %v", f, ok)
	}
	if kcal, ok := f.Per100.Get(KCal); !ok || kcal != 210 {
		t.Fatalf("kcal = %v %v", kcal, ok)
	}
	if f.ID() != "bls:B101000" {
		t.Fatal(f.ID())
	}
	if _, ok := Lookup("bls:NOPE"); ok {
		t.Fatal("unknown food")
	}
	if _, ok := Lookup("nonsense"); ok {
		t.Fatal("malformed id")
	}
	if got := f.Name("de"); got != "Vollkornbrot" {
		t.Fatal(got)
	}
	if got := f.Name("en"); got != "Wholemeal bread" {
		t.Fatal(got)
	}
	u := Food{Source: USDA, Code: "1", En: "Milk"}
	if u.Name("de") != "Milk" {
		t.Fatal("a food without a German name shows its English one")
	}
}

func TestSomeFoodsLackValuesInTheSource(t *testing.T) {
	missing := 0
	for _, f := range All() {
		for k := Key(0); k < NumKeys; k++ {
			if _, ok := f.Per100.Get(k); !ok {
				missing++
			}
		}
	}
	if missing == 0 {
		t.Fatal("the sources lack some values; the extract must keep them missing")
	}
}

func TestSearch(t *testing.T) {
	got := Search("Pflaumenmus", "de", 10)
	if len(got) == 0 || !strings.Contains(strings.ToLower(got[0].De), "pflaumenmus") {
		t.Fatalf("search = %+v", got)
	}
	if got := Search("pflaumen mus", "de", 10); len(got) == 0 {
		t.Fatal("every word must match, in any order, anywhere")
	}
	if got := Search("Müsli", "de", 5); len(got) == 0 {
		t.Fatal("umlauts")
	}
	if got := Search("muesli", "de", 5); len(got) == 0 {
		t.Fatal("ue for ü")
	}
	if got := Search("oat drink", "en", 5); len(got) == 0 {
		t.Fatal("English names")
	}
	if got := Search("a", "de", 5); got != nil {
		t.Fatal("one letter searches nothing")
	}
	if got := Search("zzzzqqq", "de", 5); len(got) != 0 {
		t.Fatal("nothing found")
	}
	if got := Search("brot", "de", 3); len(got) != 3 {
		t.Fatalf("max = 3, got %d", len(got))
	}
	// A food whose name starts with the query comes before one that only holds it.
	got = Search("apfel", "de", 20)
	if len(got) < 2 || !strings.HasPrefix(strings.ToLower(got[0].De), "apfel") {
		t.Fatalf("starts-with first: %q", got[0].De)
	}
}

// The data ships in the binary: it stays small (about 0.5 MB gzipped), and it
// names its sources and licences in the file header and next to it.
func TestTheFoodDataStaysSmallAndNamesItsSources(t *testing.T) {
	if n := len(embedded); n > 1<<20 {
		t.Fatalf("foods.tsv.gz is %d bytes, budget 1 MB", n)
	}
	var bls, usda int
	for _, f := range All() {
		switch f.Source {
		case BLS:
			bls++
		case USDA:
			usda++
		}
	}
	if bls != 7140 || usda < 5000 {
		t.Errorf("%d BLS foods (BLS 4.0 has 7,140), %d USDA foods", bls, usda)
	}
	licence, err := os.ReadFile("data/LICENSE-Nutrition.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Max Rubner-Institut", "CC BY 4.0", "10.25826/Data20251217-134202-0", "CC0", "FoodData Central"} {
		if !strings.Contains(string(licence), want) {
			t.Errorf("LICENSE-Nutrition.txt lacks %q", want)
		}
	}
}
