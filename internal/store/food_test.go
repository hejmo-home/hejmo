package store_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

func TestPersonFoodDefaultsToOmnivoreWithoutAllergies(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "F", HomeName: "P", Region: "DE-BY"})
	mia, _ := db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	f, err := db.PersonFood(h.ID, mia.ID)
	if err != nil || f.Diet != "omnivore" || len(f.Allergens) != 0 || f.Other != "" {
		t.Errorf("default = %+v, %v", f, err)
	}
}

func TestPersonFoodIsStoredReadBackAndListed(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "F", HomeName: "P", Region: "DE-BY"})
	mia, _ := db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	papa, _ := db.AddPerson(h.ID, store.Person{Name: "Papa", Role: store.RoleParent})
	want := store.Food{Diet: "vegetarian", Allergens: []string{"nuts", "peanuts"}, Other: "Kiwi"}
	if err := db.SetPersonFood(h.ID, mia.ID, want); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPersonFood(h.ID, mia.ID, store.Food{Diet: "vegan", Allergens: []string{"soy"}}); err != nil { // replaces
		t.Fatal(err)
	}
	if err := db.SetPersonFood(h.ID, mia.ID, want); err != nil {
		t.Fatal(err)
	}
	got, _ := db.PersonFood(h.ID, mia.ID)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	all, err := db.HouseholdFood(h.ID)
	if err != nil || len(all) != 1 || !reflect.DeepEqual(all[mia.ID], want) || all[papa.ID].Diet != "" {
		t.Errorf("household food = %+v, %v (people without an entry are left out)", all, err)
	}
}

func TestPersonFoodOfAStrangerIsNotFoundAndNothingIsWritten(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "F", HomeName: "P", Region: "DE-BY"})
	if err := db.SetPersonFood(h.ID, "nobody", store.Food{Diet: "vegan"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	if _, err := db.PersonFood(h.ID, "nobody"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}
