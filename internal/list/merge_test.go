package list_test

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// showFor is how the box shows a total: the article's default units first.
func showFor(defaults ...string) list.Show {
	return func(base float64, kind unit.Kind) string {
		return unit.ShowUnit(base, kind, defaults, unit.DefaultActive)
	}
}

func q(amount float64, u string) list.Quantity { return list.NewQuantity(amount, u) }

func TestQuantityKeepsTheExactAmountInTheBaseUnit(t *testing.T) {
	got := q(1.5, "kg")
	if got.Base != 1500 || got.Unit != "kg" || got.Amount() != 1.5 || got.Kind() != unit.Weight || got.None() {
		t.Errorf("%+v", got)
	}
	if !(list.Quantity{}).None() {
		t.Error("the zero quantity is none")
	}
}

func TestTheSameArticleWithTheSameKindOfUnitAddsUp(t *testing.T) {
	es := []list.Entry{{ID: "a", Text: "Mehl", ArticleID: "flour", Qty: q(500, "g")}}
	p := list.PlanAdd(es, "flour", "Mehl", q(1, "kg"), showFor("g", "kg"))
	if p.Action != list.AddedTo || p.Target.ID != "a" || p.Result.Unit != "kg" || p.Result.Base != 1500 || p.Before.Base != 500 {
		t.Errorf("%+v", p)
	}
}

func TestADozenAndPiecesAddUpToPieces(t *testing.T) {
	es := []list.Entry{{ID: "a", Text: "Eier", ArticleID: "eggs", Qty: q(6, "piece")}}
	p := list.PlanAdd(es, "eggs", "Eier", q(1, "dozen"), showFor("dozen", "piece"))
	if p.Action != list.AddedTo || p.Result.Unit != "piece" || p.Result.Base != 18 {
		t.Errorf("%+v", p)
	}
	p = list.PlanAdd(es, "eggs", "Eier", q(18, "piece"), showFor("dozen", "piece"))
	if p.Result.Unit != "dozen" || p.Result.Base != 24 {
		t.Errorf("24 eggs are 2 dozen: %+v", p)
	}
}

func TestOtherKindsStandSideBySideDirectlyUnderTheOldEntry(t *testing.T) {
	es := []list.Entry{
		{ID: "a", Text: "Haferflocken", ArticleID: "oats", Qty: q(2, "pack"), Position: 0},
		{ID: "b", Text: "Milch", ArticleID: "milk", Position: 1},
	}
	p := list.PlanAdd(es, "oats", "Haferflocken", q(500, "g"), showFor("pack", "g"))
	if p.Action != list.AddedNew || p.After != "a" || p.Result.Unit != "g" || p.Result.Base != 500 {
		t.Errorf("%+v", p)
	}
	// A pack adds up only with packs, a bunch only with bunches.
	es = append(es, list.Entry{ID: "c", Text: "Petersilie", ArticleID: "parsley", Qty: q(1, "bunch")})
	if p := list.PlanAdd(es, "parsley", "Petersilie", q(2, "pack"), showFor("bunch")); p.Action != list.AddedNew || p.After != "c" {
		t.Errorf("bunch + pack: %+v", p)
	}
	if p := list.PlanAdd(es, "parsley", "Petersilie", q(2, "bunch"), showFor("bunch")); p.Action != list.AddedTo || p.Result.Base != 3 {
		t.Errorf("bunch + bunch: %+v", p)
	}
}

func TestTheNewEntryGoesUnderTheLastMatchingEntry(t *testing.T) {
	es := []list.Entry{
		{ID: "a", Text: "Haferflocken", ArticleID: "oats", Qty: q(2, "pack")},
		{ID: "b", Text: "Haferflocken", ArticleID: "oats", Qty: q(500, "g")},
		{ID: "c", Text: "Milch"},
	}
	p := list.PlanAdd(es, "oats", "Haferflocken", q(1, "l"), showFor("pack", "g"))
	if p.Action != list.AddedNew || p.After != "b" {
		t.Errorf("%+v", p)
	}
}

func TestAnEntryWithoutQuantityTakesTheQuantityAddedLater(t *testing.T) {
	es := []list.Entry{{ID: "a", Text: "Bananen", ArticleID: "bananas"}}
	p := list.PlanAdd(es, "bananas", "Bananen", q(5, "piece"), showFor("piece", "kg"))
	if p.Action != list.Filled || p.Target.ID != "a" || p.Result.Base != 5 {
		t.Errorf("%+v", p)
	}
}

func TestAddingWithoutQuantityNeverMakesASecondEntry(t *testing.T) {
	es := []list.Entry{{ID: "a", Text: "Bananen", ArticleID: "bananas", Qty: q(5, "piece")}}
	p := list.PlanAdd(es, "bananas", "Bananen", list.Quantity{}, showFor("piece"))
	if p.Action != list.Exists || p.Target.ID != "a" {
		t.Errorf("%+v", p)
	}
	if p := list.PlanAdd(es, "bananas", "Bananen", q(1, "kg"), showFor("piece", "kg")); p.Action != list.AddedNew {
		t.Errorf("a new kind is a new entry: %+v", p)
	}
}

func TestFreeTextMatchesByTextIgnoringCaseAndUmlauts(t *testing.T) {
	es := []list.Entry{{ID: "a", Text: "Grüne Bohnen", Qty: q(2, "piece")}}
	p := list.PlanAdd(es, "", "grune bohnen", q(3, "piece"), showFor("piece", "pack"))
	if p.Action != list.AddedTo || p.Result.Base != 5 {
		t.Errorf("%+v", p)
	}
	// An article does not match text that differs, and text matches an article's entry by its text.
	if p := list.PlanAdd(es, "beans", "Bohnen", q(1, "piece"), showFor("piece")); p.Action != list.AddedNew {
		t.Errorf("%+v", p)
	}
}

func TestTickedOffEntriesDoNotMerge(t *testing.T) {
	es := []list.Entry{{ID: "a", Text: "Mehl", ArticleID: "flour", Done: true, Qty: q(500, "g")}}
	if p := list.PlanAdd(es, "flour", "Mehl", q(1, "kg"), showFor("g", "kg")); p.Action != list.AddedNew || p.After != "" {
		t.Errorf("%+v", p)
	}
	if p := list.PlanAdd(es, "flour", "Mehl", list.Quantity{}, showFor("g", "kg")); p.Action != list.AddedNew {
		t.Errorf("%+v", p)
	}
}

func TestChangingAQuantityReplacesIt(t *testing.T) {
	es := []list.Entry{{ID: "a", Text: "Eier", ArticleID: "eggs", Qty: q(6, "piece")}}
	p, err := list.PlanSet(es, "a", q(10, "piece"), showFor("dozen", "piece"))
	if err != nil || p.Action != list.SetOnly || p.Result.Base != 10 || p.Result.Unit != "piece" || p.Before.Base != 6 {
		t.Errorf("%+v %v", p, err)
	}
}

func TestChangingToTheSameAmountInAnotherUnitOfTheSameKindKeepsTheAmount(t *testing.T) {
	es := []list.Entry{{ID: "a", Text: "Mehl", ArticleID: "flour", Qty: q(500, "g")}}
	p, _ := list.PlanSet(es, "a", q(0.5, "kg"), showFor("g", "kg"))
	if p.Result.Base != 500 || p.Result.Unit != "g" {
		t.Errorf("0.5 kg of flour is shown as 500 g: %+v", p)
	}
	p, _ = list.PlanSet(es, "a", q(1000, "g"), showFor("g", "kg"))
	if p.Result.Base != 1000 || p.Result.Unit != "kg" {
		t.Errorf("1000 g are shown as 1 kg: %+v", p)
	}
}

func TestChangingToAnotherKindMergesWithTheEntryOfThatKind(t *testing.T) {
	es := []list.Entry{
		{ID: "a", Text: "Haferflocken", ArticleID: "oats", Qty: q(2, "pack"), Position: 0},
		{ID: "b", Text: "Haferflocken", ArticleID: "oats", Qty: q(500, "g"), Position: 1},
	}
	p, err := list.PlanSet(es, "a", q(250, "g"), showFor("pack", "g"))
	if err != nil || p.Action != list.SetMerged || p.Into.ID != "b" || p.Result.Base != 750 || p.Result.Unit != "g" {
		t.Errorf("%+v %v", p, err)
	}
	// Without a second entry of that kind it is just changed.
	p, _ = list.PlanSet(es[:1], "a", q(250, "g"), showFor("pack", "g"))
	if p.Action != list.SetOnly || p.Result.Base != 250 {
		t.Errorf("%+v", p)
	}
}

func TestRemovingTheQuantityKeepsTheEntryUnlessAnotherOneWithQuantityHasIt(t *testing.T) {
	es := []list.Entry{{ID: "a", Text: "Mehl", ArticleID: "flour", Qty: q(500, "g")}}
	p, _ := list.PlanSet(es, "a", list.Quantity{}, showFor("g"))
	if p.Action != list.SetOnly || !p.Result.None() || p.Before.Base != 500 {
		t.Errorf("%+v", p)
	}
	es = append(es, list.Entry{ID: "b", Text: "Mehl", ArticleID: "flour", Qty: q(2, "pack")})
	p, _ = list.PlanSet(es, "a", list.Quantity{}, showFor("g"))
	if p.Action != list.SetMerged || p.Into.ID != "b" || p.Result.Base != 2 {
		t.Errorf("%+v", p)
	}
}

func TestAnEntryWithoutQuantityGetsOne(t *testing.T) {
	es := []list.Entry{{ID: "a", Text: "Bananen", ArticleID: "bananas"}}
	p, _ := list.PlanSet(es, "a", q(1, "kg"), showFor("piece", "kg"))
	if p.Action != list.SetOnly || p.Result.Base != 1000 || !p.Before.None() {
		t.Errorf("%+v", p)
	}
}

func TestOnlyAnOpenEntryCanChange(t *testing.T) {
	es := []list.Entry{{ID: "a", Text: "Mehl", Done: true}}
	if _, err := list.PlanSet(es, "a", q(1, "kg"), showFor("g")); err != list.ErrNotOpen {
		t.Errorf("ticked off: %v", err)
	}
	if _, err := list.PlanSet(es, "zzz", q(1, "kg"), showFor("g")); err != list.ErrNotOpen {
		t.Errorf("gone: %v", err)
	}
}
