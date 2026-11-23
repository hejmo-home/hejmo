package store_test

import (
	"errors"
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 277: varieties on list entries.

func TestMehlAndMehlTypeFourOhFiveStayTwoEntriesAndTwoOfTheVarietyAddUp(t *testing.T) {
	db, h, l := shopping(t)
	add := func(variety string, amount float64, u string) store.AddResult {
		t.Helper()
		r, err := db.AddItem(h.ID, l.ID, store.Item{Text: "Mehl", ArticleID: "flour", Variety: variety, Qty: list.NewQuantity(amount, u), ChosenUnit: u}, show("g", "kg"))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	add("", 500, "g")
	if r := add("Type 405", 1, "kg"); r.Plan.Action != list.AddedNew || r.Entry.Variety != "Type 405" {
		t.Fatalf("%+v", r)
	}
	if r := add("  type   405 ", 1, "kg"); r.Plan.Action != list.AddedTo || r.Entry.Qty.Base != 2000 {
		t.Fatalf("two Mehl · Type 405 add up: %+v", r)
	}
	es, _ := db.Entries(h.ID, l.ID)
	if len(es) != 2 || es[0].Variety != "" || es[1].Variety != "Type 405" || es[1].Qty.Base != 2000 || es[0].Qty.Base != 500 {
		t.Errorf("%+v", es)
	}
}

func TestAVarietyNeedsAnArticleAndAtMostSixtyCharacters(t *testing.T) {
	db, h, l := shopping(t)
	if _, err := db.AddItem(h.ID, l.ID, store.Item{Text: "Mehl", Variety: "Type 405"}, show("g")); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("free text has no variety: %v", err)
	}
	long := ""
	for range 61 {
		long += "x"
	}
	if _, err := db.AddItem(h.ID, l.ID, store.Item{Text: "Mehl", ArticleID: "flour", Variety: long}, show("g")); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("too long: %v", err)
	}
}

func TestChangingTheVarietyMergesAndRemembersNothingElse(t *testing.T) {
	db, h, l := shopping(t)
	a, _ := db.AddItem(h.ID, l.ID, store.Item{Text: "Mehl", ArticleID: "flour", Qty: list.NewQuantity(500, "g")}, show("g", "kg"))
	db.AddItem(h.ID, l.ID, store.Item{Text: "Mehl", ArticleID: "flour", Variety: "Type 405", Qty: list.NewQuantity(1, "kg")}, show("g", "kg"))
	v := "Type 550"
	res, err := db.ChangeEntry(h.ID, l.ID, a.Entry.ID, list.NewQuantity(500, "g"), "g", &v, show("g", "kg"))
	if err != nil || res.Plan.Action != list.SetOnly {
		t.Fatalf("%+v %v", res, err)
	}
	es, _ := db.Entries(h.ID, l.ID)
	if es[0].Variety != "Type 550" || len(es) != 2 {
		t.Errorf("%+v", es)
	}
	v = "type 405"
	res, err = db.ChangeEntry(h.ID, l.ID, a.Entry.ID, list.NewQuantity(500, "g"), "g", &v, show("g", "kg"))
	if err != nil || res.Plan.Action != list.SetMerged {
		t.Fatalf("%+v %v", res, err)
	}
	es, _ = db.Entries(h.ID, l.ID)
	if len(es) != 1 || es[0].Variety != "Type 405" || es[0].Qty.Base != 1500 {
		t.Errorf("%+v", es)
	}
	// SetQuantity keeps the variety.
	if _, err := db.SetQuantity(h.ID, l.ID, es[0].ID, list.NewQuantity(2, "kg"), "kg", show("g", "kg")); err != nil {
		t.Fatal(err)
	}
	if es, _ = db.Entries(h.ID, l.ID); es[0].Variety != "Type 405" || es[0].Qty.Base != 2000 {
		t.Errorf("%+v", es)
	}
	// An entry of free text cannot get a variety.
	f, _ := db.AddItem(h.ID, l.ID, store.Item{Text: "Geschenkpapier"}, show("g"))
	v = "bunt"
	if _, err := db.ChangeEntry(h.ID, l.ID, f.Entry.ID, list.Quantity{}, "", &v, show("g")); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("%v", err)
	}
}

func TestKnownVarietiesComeFromAllListsOpenAndTickedNewestFirstAtMostFour(t *testing.T) {
	db, h, l := shopping(t)
	other, _ := db.CreateList(h.ID, list.List{Name: "Backen", Color: "blue", Symbol: "circle-filled", Sort: list.Manual, Suggestions: true})
	add := func(lid, v string) string {
		r, err := db.AddItem(h.ID, lid, store.Item{Text: "Mehl", ArticleID: "flour", Variety: v}, show("g"))
		if err != nil {
			t.Fatal(err)
		}
		return r.Entry.ID
	}
	add(l.ID, "Type 405")
	id := add(other.ID, "Type 550")
	add(l.ID, "Dinkel")
	add(l.ID, "Roggen")
	add(other.ID, "Vollkorn")
	add(l.ID, "type 550") // the same again: one chip
	if err := db.SetEntryDone(h.ID, other.ID, id, true); err != nil {
		t.Fatal(err)
	}
	got, err := db.ArticleVarieties(h.ID, "flour", 4)
	want := []string{"Type 550", "Vollkorn", "Roggen", "Dinkel"}
	if err != nil || len(got) != 4 {
		t.Fatalf("%v %v", got, err)
	}
	for i := range want {
		if i == 0 && got[0] != "type 550" && got[0] != "Type 550" {
			t.Errorf("%v", got)
		} else if i > 0 && got[i] != want[i] {
			t.Errorf("%v", got)
		}
	}
	if none, _ := db.ArticleVarieties(h.ID, "eggs", 4); len(none) != 0 {
		t.Errorf("%v", none)
	}
}
