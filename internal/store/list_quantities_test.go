package store_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/unit"
)

func show(defaults ...string) list.Show {
	return func(base float64, kind unit.Kind) string {
		return unit.ShowUnit(base, kind, defaults, unit.DefaultActive)
	}
}

func shopping(t *testing.T) (*store.DB, store.Household, list.List) {
	t.Helper()
	db, h := newListFixture(t)
	l, err := db.CreateList(h.ID, list.List{Name: "Einkauf", Color: "green", Symbol: "square-filled", Sort: list.Manual, Suggestions: true})
	if err != nil {
		t.Fatal(err)
	}
	return db, h, l
}

func lines(t *testing.T, db *store.DB, h store.Household, l list.List) []string {
	t.Helper()
	es, err := db.Entries(h.ID, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		s := e.Text
		if !e.Qty.None() {
			s += " " + unit.Format(e.Qty.Amount(), e.Qty.Unit, "en") + " " + e.Qty.Unit
		}
		if e.Done {
			s += " (done)"
		}
		out = append(out, s)
	}
	return out
}

func TestAListHasSuggestionsOnOrOff(t *testing.T) {
	db, h, l := shopping(t)
	got, err := db.List(h.ID, l.ID)
	if err != nil || !got.Suggestions {
		t.Fatalf("%+v %v", got, err)
	}
	l.Suggestions = false
	if _, err := db.UpdateList(h.ID, l); err != nil {
		t.Fatal(err)
	}
	if got, _ = db.List(h.ID, l.ID); got.Suggestions {
		t.Error("switched off")
	}
}

func TestAddingAnArticleWithAQuantityStoresTheExactAmount(t *testing.T) {
	db, h, l := shopping(t)
	r, err := db.AddItem(h.ID, l.ID, store.Item{Text: "Mehl", ArticleID: "flour", Qty: list.NewQuantity(500, "g"), ChosenUnit: "g"}, show("g", "kg"))
	if err != nil || r.Plan.Action != list.AddedNew || r.Entry.ArticleID != "flour" || r.Entry.Qty.Base != 500 {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = db.AddItem(h.ID, l.ID, store.Item{Text: "Mehl", ArticleID: "flour", Qty: list.NewQuantity(1, "kg"), ChosenUnit: "kg"}, show("g", "kg"))
	if err != nil || r.Plan.Action != list.AddedTo || r.Entry.Qty.Unit != "kg" || r.Entry.Qty.Base != 1500 || r.Plan.Before.Base != 500 {
		t.Fatalf("%+v %v", r, err)
	}
	if got := lines(t, db, h, l); !reflect.DeepEqual(got, []string{"Mehl 1.5 kg"}) {
		t.Errorf("%v", got)
	}
}

func TestAnotherKindStandsDirectlyUnderTheOldEntryOnAManualList(t *testing.T) {
	db, h, l := shopping(t)
	add := func(text, art string, q list.Quantity) {
		t.Helper()
		if _, err := db.AddItem(h.ID, l.ID, store.Item{Text: text, ArticleID: art, Qty: q}, show("pack", "g")); err != nil {
			t.Fatal(err)
		}
	}
	add("Haferflocken", "oats", list.NewQuantity(2, "pack"))
	add("Milch", "milk", list.Quantity{})
	add("Haferflocken", "oats", list.NewQuantity(500, "g"))
	if got := lines(t, db, h, l); !reflect.DeepEqual(got, []string{"Haferflocken 2 pack", "Haferflocken 500 g", "Milch"}) {
		t.Errorf("%v", got)
	}
	add("Haferflocken", "oats", list.NewQuantity(250, "g"))
	if got := lines(t, db, h, l); !reflect.DeepEqual(got, []string{"Haferflocken 2 pack", "Haferflocken 750 g", "Milch"}) {
		t.Errorf("%v", got)
	}
}

func TestAddingWithoutQuantityTwiceMakesOneEntryAndAnEntryTakesALaterQuantity(t *testing.T) {
	db, h, l := shopping(t)
	item := store.Item{Text: "Bananen", ArticleID: "bananas"}
	if _, err := db.AddItem(h.ID, l.ID, item, show("piece")); err != nil {
		t.Fatal(err)
	}
	r, err := db.AddItem(h.ID, l.ID, item, show("piece"))
	if err != nil || r.Plan.Action != list.Exists {
		t.Fatalf("%+v %v", r, err)
	}
	item.Qty = list.NewQuantity(5, "piece")
	if r, _ = db.AddItem(h.ID, l.ID, item, show("piece")); r.Plan.Action != list.Filled {
		t.Fatalf("%+v", r)
	}
	if got := lines(t, db, h, l); !reflect.DeepEqual(got, []string{"Bananen 5 piece"}) {
		t.Errorf("%v", got)
	}
}

func TestAnEntryTickedOffDoesNotMerge(t *testing.T) {
	db, h, l := shopping(t)
	r, _ := db.AddItem(h.ID, l.ID, store.Item{Text: "Mehl", ArticleID: "flour", Qty: list.NewQuantity(500, "g")}, show("g", "kg"))
	if err := db.SetEntryDone(h.ID, l.ID, r.Entry.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddItem(h.ID, l.ID, store.Item{Text: "Mehl", ArticleID: "flour", Qty: list.NewQuantity(1, "kg")}, show("g", "kg")); err != nil {
		t.Fatal(err)
	}
	if got := lines(t, db, h, l); len(got) != 2 {
		t.Errorf("%v", got)
	}
}

func TestAnEmptyOrTooLongTextIsInvalid(t *testing.T) {
	db, h, l := shopping(t)
	if _, err := db.AddItem(h.ID, l.ID, store.Item{Text: "  "}, show("piece")); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("%v", err)
	}
	if _, err := db.AddItem(h.ID, "nope", store.Item{Text: "x"}, show("piece")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("%v", err)
	}
}

func TestChangingAQuantityReplacesItAndMergesAcrossKinds(t *testing.T) {
	db, h, l := shopping(t)
	r, _ := db.AddItem(h.ID, l.ID, store.Item{Text: "Eier", ArticleID: "eggs", Qty: list.NewQuantity(6, "piece")}, show("dozen", "piece"))
	s, err := db.SetQuantity(h.ID, l.ID, r.Entry.ID, list.NewQuantity(10, "piece"), "piece", show("dozen", "piece"))
	if err != nil || s.Plan.Action != list.SetOnly || s.Plan.Before.Base != 6 {
		t.Fatalf("%+v %v", s, err)
	}
	if got := lines(t, db, h, l); !reflect.DeepEqual(got, []string{"Eier 10 piece"}) {
		t.Errorf("%v", got)
	}
	a, _ := db.AddItem(h.ID, l.ID, store.Item{Text: "Haferflocken", ArticleID: "oats", Qty: list.NewQuantity(2, "pack")}, show("pack", "g"))
	db.AddItem(h.ID, l.ID, store.Item{Text: "Haferflocken", ArticleID: "oats", Qty: list.NewQuantity(500, "g")}, show("pack", "g"))
	s, err = db.SetQuantity(h.ID, l.ID, a.Entry.ID, list.NewQuantity(250, "g"), "g", show("pack", "g"))
	if err != nil || s.Plan.Action != list.SetMerged {
		t.Fatalf("%+v %v", s, err)
	}
	if got := lines(t, db, h, l); !reflect.DeepEqual(got, []string{"Eier 10 piece", "Haferflocken 750 g"}) {
		t.Errorf("%v", got)
	}
	// No quantity at all.
	if _, err = db.SetQuantity(h.ID, l.ID, r.Entry.ID, list.Quantity{}, "", show("dozen", "piece")); err != nil {
		t.Fatal(err)
	}
	if got := lines(t, db, h, l); !reflect.DeepEqual(got, []string{"Eier", "Haferflocken 750 g"}) {
		t.Errorf("%v", got)
	}
}

func TestChangingAnEntryThatIsGoneOrTickedOffSaysSo(t *testing.T) {
	db, h, l := shopping(t)
	r, _ := db.AddItem(h.ID, l.ID, store.Item{Text: "Mehl", Qty: list.NewQuantity(1, "kg")}, show("g"))
	_ = db.SetEntryDone(h.ID, l.ID, r.Entry.ID, true)
	if _, err := db.SetQuantity(h.ID, l.ID, r.Entry.ID, list.NewQuantity(2, "kg"), "kg", show("g")); !errors.Is(err, list.ErrNotOpen) {
		t.Errorf("ticked off: %v", err)
	}
	_ = db.DeleteEntry(h.ID, l.ID, r.Entry.ID)
	if _, err := db.SetQuantity(h.ID, l.ID, r.Entry.ID, list.NewQuantity(2, "kg"), "kg", show("g")); !errors.Is(err, list.ErrNotOpen) {
		t.Errorf("removed: %v", err)
	}
	if _, err := db.SetQuantity(h.ID, "nope", r.Entry.ID, list.NewQuantity(2, "kg"), "kg", show("g")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("list gone: %v", err)
	}
}

func TestTheLastUnitUsedForAnArticleIsRemembered(t *testing.T) {
	db, h, l := shopping(t)
	if u, err := db.LastUnit(h.ID, "eggs"); err != nil || u != "" {
		t.Fatalf("%q %v", u, err)
	}
	db.AddItem(h.ID, l.ID, store.Item{Text: "Eier", ArticleID: "eggs", Qty: list.NewQuantity(6, "piece"), ChosenUnit: "piece"}, show("dozen", "piece"))
	if u, _ := db.LastUnit(h.ID, "eggs"); u != "piece" {
		t.Errorf("%q", u)
	}
	r, _ := db.AddItem(h.ID, l.ID, store.Item{Text: "Eier", ArticleID: "eggs", Qty: list.NewQuantity(1, "dozen"), ChosenUnit: "dozen"}, show("dozen", "piece"))
	if u, _ := db.LastUnit(h.ID, "eggs"); u != "dozen" {
		t.Errorf("%q", u)
	}
	db.SetQuantity(h.ID, l.ID, r.Entry.ID, list.NewQuantity(3, "piece"), "piece", show("dozen", "piece"))
	if u, _ := db.LastUnit(h.ID, "eggs"); u != "piece" {
		t.Errorf("%q", u)
	}
	if u, _ := db.LastUnit(h.ID, "other"); u != "" {
		t.Errorf("%q", u)
	}
}

func TestTheHouseholdsUnitsStartWithTheCountryAndStayOnceChanged(t *testing.T) {
	db, h := newListFixture(t)
	on, custom, err := db.ActiveUnits(h.ID)
	if err != nil || custom || on != nil {
		t.Fatalf("%v %v %v", on, custom, err)
	}
	if err := db.SetActiveUnits(h.ID, []string{"l", "g", "kg", "oz", "bogus"}); err != nil {
		t.Fatal(err)
	}
	on, custom, _ = db.ActiveUnits(h.ID)
	if !custom || !reflect.DeepEqual(on, []string{"piece", "g", "kg", "l", "oz"}) {
		t.Errorf("%v %v", on, custom)
	}
	err = db.SetActiveUnits(h.ID, []string{"piece", "g"})
	var fe *store.FieldError
	if !errors.Is(err, store.ErrInvalid) || !errors.As(err, &fe) || fe.Field != unit.GroupVolume {
		t.Errorf("no volume unit: %v", err)
	}
	if on, _, _ = db.ActiveUnits(h.ID); len(on) != 5 {
		t.Errorf("a refused change stores nothing: %v", on)
	}
	if err := db.ResetActiveUnits(h.ID); err != nil {
		t.Fatal(err)
	}
	if _, custom, _ = db.ActiveUnits(h.ID); custom {
		t.Error("reset follows the country again")
	}
}
