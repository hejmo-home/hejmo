package nutrition

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/library"
)

// Acceptance criterion: every shipped article is mapped to a food. An article
// that is not food (a toothbrush, nappies) has none and says so.
func TestEveryShippedArticleIsMappedToAFood(t *testing.T) {
	for _, a := range library.Shipped() {
		l, ok := ForArticle(a.ID)
		if a.Kind == library.NonFood {
			if ok {
				t.Errorf("%s is not food and must have no food", a.ID)
			}
			continue
		}
		if !ok {
			t.Errorf("shipped article %q (%s) has no food in data/articles.tsv", a.ID, a.De)
			continue
		}
		f, found := Lookup(l.FoodID)
		if !found {
			t.Errorf("%s: food %q does not exist", a.ID, l.FoodID)
			continue
		}
		if _, ok := f.Per100.Get(KCal); !ok {
			t.Errorf("%s: food %q (%s) has no energy", a.ID, l.FoodID, f.De)
		}
		if l.PieceG < 0 || l.PieceG > 5000 || l.GPerML < 0 || l.GPerML > 3 {
			t.Errorf("%s: implausible weights %+v", a.ID, l)
		}
	}
}

func TestNoStrayLines(t *testing.T) {
	for key := range articleLinks() {
		if _, ok := library.ShippedByID(key); !ok {
			t.Errorf("data/articles.tsv names %q, which is no shipped article", key)
		}
	}
}

func TestWeightsOfAnArticle(t *testing.T) {
	l, ok := ForArticle("eggs")
	if !ok || l.PieceG <= 0 {
		t.Fatalf("eggs = %+v %v: an egg has a piece weight", l, ok)
	}
	if g, ok := Grams(3, "piece", l.PieceG, l.GPerML); !ok || g != 3*l.PieceG {
		t.Fatalf("3 eggs = %v %v", g, ok)
	}
	if l, _ := ForArticle("salt"); l.GPerML <= 0 {
		t.Fatal("salt has a density, so a pinch counts")
	}
	if l, _ := ForArticle("garlic"); l.PieceG != 0 {
		t.Fatal("a piece of garlic is not known (bulb or clove?): it must not be guessed")
	}
}

// Beans and leaves are not the brewed drink: a weight of them has no value of
// its own, so the line is listed as not counted instead of counting about 2 kcal/100 g.
func TestGramsOfCoffeeBeansAndTeaAreNotCounted(t *testing.T) {
	for _, key := range []string{"coffee-beans", "coffee", "tea"} {
		l, ok := ForArticle(key)
		if !ok {
			t.Fatalf("%s has no food", key)
		}
		f, _ := Lookup(l.FoodID)
		res := Calculate(1, []Line{{Name: key, Amount: 20, Unit: "g", ArticleID: key, Food: &f, PieceG: l.PieceG, GPerML: l.GPerML, NoWeight: l.NoWeight}})
		if res.Counted != 0 || len(res.Skipped) != 1 || res.Skipped[0].Reason != ReasonNoWeight {
			t.Errorf("%s: 20 g = counted %d skipped %+v, want listed as not counted (no weight)", key, res.Counted, res.Skipped)
		}
	}
}
