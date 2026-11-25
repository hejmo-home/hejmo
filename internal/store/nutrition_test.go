package store_test

import (
	"errors"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 288: the switch "Nährwerte bei Rezepten" per person (off for everyone
// until a parent switches it on) and the food of an own article.

func TestNutritionIsOffForEveryoneUntilSwitchedOn(t *testing.T) {
	db, _ := openTemp(t)
	h, mama := seedParent(t, db)
	mia, err := db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []store.Person{mama, mia} {
		got, err := db.Person(h.ID, p.ID)
		if err != nil || got.NutritionShown {
			t.Fatalf("%s: shown = %v %v, want off", p.Name, got.NutritionShown, err)
		}
	}
	if err := db.SetNutritionShown(h.ID, mia.ID, true); err != nil {
		t.Fatal(err)
	}
	people, _ := db.People(h.ID)
	for _, p := range people {
		if want := p.ID == mia.ID; p.NutritionShown != want {
			t.Errorf("%s shown = %v, want %v", p.Name, p.NutritionShown, want)
		}
	}
	if err := db.SetNutritionShown(h.ID, mia.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.Person(h.ID, mia.ID); got.NutritionShown {
		t.Error("switched off again")
	}
}

func TestNutritionSwitchStaysInItsHousehold(t *testing.T) {
	db, _ := openTemp(t)
	h, mama := seedParent(t, db)
	other, _ := db.CreateHousehold(store.Household{Name: "Other"})
	if err := db.SetNutritionShown(other.ID, mama.ID, true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want not found", err)
	}
	if got, _ := db.Person(h.ID, mama.ID); got.NutritionShown {
		t.Fatal("a foreign household switched a person")
	}
}

func TestAnOwnArticleKeepsItsFoodAndPieceWeight(t *testing.T) {
	db, h, mama := newArticleFixture(t)
	in := store.OwnArticle{Name: "Omas Pflaumenmus", Dept: "pantry", Kind: "vegan", Unit1: "pack", Food: "bls:S161000", PieceG: 350}
	a, err := db.CreateArticle(h.ID, mama.ID, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := db.Article(h.ID, a.ID)
	if got.Food != "bls:S161000" || got.PieceG != 350 {
		t.Fatalf("read back = %q %v", got.Food, got.PieceG)
	}
	for _, change := range []store.PictureChange{store.KeepPicture, store.RemovePicture} {
		got.Food, got.PieceG = "usda:170848", 12.5
		upd, err := db.UpdateArticle(h.ID, got, change, nil)
		if err != nil || upd.Food != "usda:170848" || upd.PieceG != 12.5 {
			t.Fatalf("update (%v) = %+v %v", change, upd, err)
		}
		got = upd
	}
	got.Food, got.PieceG = "", 0
	upd, err := db.UpdateArticle(h.ID, got, store.KeepPicture, nil)
	if err != nil || upd.Food != "" || upd.PieceG != 0 {
		t.Fatalf("cleared = %+v %v", upd, err)
	}
}

func TestAnOwnArticleWithoutFoodIsFine(t *testing.T) {
	db, h, mama := newArticleFixture(t)
	a, err := db.CreateArticle(h.ID, mama.ID, katzenfutter(), nil)
	if err != nil || a.Food != "" || a.PieceG != 0 {
		t.Fatalf("created = %+v %v", a, err)
	}
}

func TestAnOwnArticleRefusesAnUnknownFoodOrAWeirdWeight(t *testing.T) {
	db, h, mama := newArticleFixture(t)
	for name, in := range map[string]store.OwnArticle{
		"unknown food":     {Name: "A", Dept: "pantry", Kind: "vegan", Unit1: "pack", Food: "bls:NOPE"},
		"malformed food":   {Name: "A", Dept: "pantry", Kind: "vegan", Unit1: "pack", Food: "pflaumenmus"},
		"negative weight":  {Name: "A", Dept: "pantry", Kind: "vegan", Unit1: "pack", PieceG: -1},
		"huge weight":      {Name: "A", Dept: "pantry", Kind: "vegan", Unit1: "pack", PieceG: 5001},
		"weight, no food?": {Name: "A", Dept: "pantry", Kind: "vegan", Unit1: "pack", PieceG: 0.001},
	} {
		_, err := db.CreateArticle(h.ID, mama.ID, in, nil)
		if name == "weight, no food?" {
			// a piece weight may stand without a food (the food can come later): small positive weights are fine
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s: err = %v, want invalid", name, err)
		}
	}
}
