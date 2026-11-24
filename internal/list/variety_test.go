package list_test

import (
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
)

// Ticket 277 (ADR 0019): varieties on list entries.

func TestAVarietyKeepsAnEntryApartFromTheArticleWithoutOne(t *testing.T) {
	es := []list.Entry{{ID: "a", Text: "Mehl", ArticleID: "flour", Qty: q(500, "g")}}
	p := list.PlanAdd(es, "flour", "Type 405", "Mehl", q(1, "kg"), showFor("g", "kg"))
	if p.Action != list.AddedNew || p.Target.ID != "" || p.After != "" {
		t.Errorf("Mehl and Mehl · Type 405 never merge: %+v", p)
	}
	if p := list.PlanAdd(es, "flour", "", "Mehl", q(1, "kg"), showFor("g", "kg")); p.Action != list.AddedTo {
		t.Errorf("no variety still adds up: %+v", p)
	}
}

func TestTheSameVarietyAddsUpIgnoringCaseSpacesAndUmlauts(t *testing.T) {
	es := []list.Entry{{ID: "a", Text: "Mehl", ArticleID: "flour", Variety: "Type 405", Qty: q(500, "g")}}
	for _, v := range []string{"Type 405", "type 405", "  TYPE   405 "} {
		p := list.PlanAdd(es, "flour", v, "Mehl", q(1, "kg"), showFor("g", "kg"))
		if p.Action != list.AddedTo || p.Result.Base != 1500 {
			t.Errorf("%q: %+v", v, p)
		}
	}
	es = []list.Entry{{ID: "a", Text: "Mehl", ArticleID: "flour", Variety: "Mühle Süd"}}
	if p := list.PlanAdd(es, "flour", "muhle sud", "Mehl", list.Quantity{}, showFor("g")); p.Action != list.Exists {
		t.Errorf("umlauts are folded: %+v", p)
	}
}

func TestAddingWithoutQuantityExistsOnlyForArticleAndVarietyBothEqual(t *testing.T) {
	es := []list.Entry{{ID: "a", Text: "Mehl", ArticleID: "flour", Variety: "Type 405"}}
	if p := list.PlanAdd(es, "flour", "Type 405", "Mehl", list.Quantity{}, showFor("g")); p.Action != list.Exists {
		t.Errorf("%+v", p)
	}
	if p := list.PlanAdd(es, "flour", "", "Mehl", list.Quantity{}, showFor("g")); p.Action != list.AddedNew {
		t.Errorf("Mehl next to Mehl · Type 405: %+v", p)
	}
	if p := list.PlanAdd(es, "flour", "Type 550", "Mehl", list.Quantity{}, showFor("g")); p.Action != list.AddedNew {
		t.Errorf("another variety: %+v", p)
	}
}

func TestChangingTheVarietyOfAnEntryMergesIntoTheEntryOfTheSameVariety(t *testing.T) {
	es := []list.Entry{
		{ID: "a", Text: "Mehl", ArticleID: "flour", Qty: q(500, "g")},
		{ID: "b", Text: "Mehl", ArticleID: "flour", Variety: "Type 405", Qty: q(1, "kg")},
	}
	p, err := list.PlanSet(es, "a", q(500, "g"), "type 405", showFor("g", "kg"))
	if err != nil || p.Action != list.SetMerged || p.Into.ID != "b" || p.Result.Base != 1500 {
		t.Errorf("%+v %v", p, err)
	}
	p, _ = list.PlanSet(es, "a", q(500, "g"), "Type 550", showFor("g", "kg"))
	if p.Action != list.SetOnly || p.Variety != "Type 550" {
		t.Errorf("a new variety is only set: %+v", p)
	}
	p, _ = list.PlanSet(es, "b", q(1, "kg"), "", showFor("g", "kg"))
	if p.Action != list.SetMerged || p.Into.ID != "a" || p.Result.Base != 1500 {
		t.Errorf("taking the variety away merges into Mehl: %+v", p)
	}
}

func TestValidateVariety(t *testing.T) {
	if err := list.ValidateVariety("Type 405"); err != nil {
		t.Error(err)
	}
	for in, msg := range map[string]string{"": "required", "   ": "required", strings.Repeat("ä", 61): "too long"} {
		err, ok := list.ValidateVariety(in).(*list.InvalidError)
		if !ok || err.Field != "variety" || err.Msg != msg {
			t.Errorf("%q: %v", in, err)
		}
	}
	if err := list.ValidateVariety(strings.Repeat("ä", 60)); err != nil {
		t.Errorf("60 runes are fine: %v", err)
	}
}

func TestNormalizeVarietyTrimsAndCollapsesInnerSpaces(t *testing.T) {
	if got := list.NormalizeVariety("  Type \t 405  "); got != "Type 405" {
		t.Errorf("%q", got)
	}
}

func TestAlphabeticalOrderPutsNoVarietyFirstThenTheFoldedVariety(t *testing.T) {
	es := []list.Entry{
		{ID: "1", Text: "Mehl", Variety: "Type 550", Position: 0},
		{ID: "2", Text: "Milch", Position: 1},
		{ID: "3", Text: "Mehl", Variety: "Type 405", Position: 2},
		{ID: "4", Text: "Mehl", Position: 3},
		{ID: "5", Text: "mehl", Variety: "Äpfel", Position: 4},
	}
	var got []string
	for _, e := range list.Order(es, list.Alphabetical) {
		got = append(got, e.ID)
	}
	if want := "4 5 3 1 2"; strings.Join(got, " ") != want {
		t.Errorf("got %v, want %s", got, want)
	}
}
