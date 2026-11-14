package store_test

import (
	"errors"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

func TestHouseholdWithPeople(t *testing.T) {
	db, _ := openTemp(t)
	h, err := db.CreateHousehold(store.Household{Name: "Family", HomeName: "Pasing", Region: "DE-BY", Latitude: 48.15, Longitude: 11.46})
	if err != nil {
		t.Fatal(err)
	}
	mama, err := db.AddPerson(h.ID, store.Person{Name: "Mama", Role: store.RoleParent, Color: "#B4583A", Shape: "circle-filled"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild, Color: "#9A7420", Shape: "square-empty"}); err != nil {
		t.Fatal(err)
	}

	people, err := db.People(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 2 || people[0].Name != "Mama" || people[1].Name != "Mia" {
		t.Fatalf("people = %+v, want Mama then Mia", people)
	}
	if people[1].Role != store.RoleChild {
		t.Errorf("Mia role = %q", people[1].Role)
	}

	got, err := db.Person(h.ID, mama.ID)
	if err != nil || got.Color != "#B4583A" || got.Shape != "circle-filled" {
		t.Fatalf("Person(mama) = %+v, %v", got, err)
	}

	loaded, err := db.Household(h.ID)
	if err != nil || loaded.Region != "DE-BY" || loaded.HomeName != "Pasing" {
		t.Fatalf("Household = %+v, %v", loaded, err)
	}
}

func TestPersonNotFound(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	if _, err := db.Person(h.ID, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRejectsUnknownRole(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	if _, err := db.AddPerson(h.ID, store.Person{Name: "X", Role: "admin"}); err == nil {
		t.Fatal("unknown role accepted")
	}
}

func TestRejectsEmptyName(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	if _, err := db.AddPerson(h.ID, store.Person{Name: "  ", Role: store.RoleChild}); err == nil {
		t.Fatal("empty name accepted")
	}
}

func TestTheBoxHasOneHousehold(t *testing.T) {
	db, _ := openTemp(t)
	if _, err := db.HomeHousehold(); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("fresh box: err = %v, want ErrNotFound", err)
	}
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	got, err := db.HomeHousehold()
	if err != nil || got.ID != h.ID {
		t.Fatalf("HomeHousehold = %+v, %v", got, err)
	}
}

func TestHouseholdKeepsItsHomeCoordinates(t *testing.T) {
	db, _ := openTemp(t)
	h, err := db.CreateHousehold(store.Household{Name: "Family", HomeName: "Pasing", Region: "DE-BY"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetHouseholdCoordinates(h.ID, 48.15, 11.46); err != nil {
		t.Fatal(err)
	}
	got, err := db.HomeHousehold()
	if err != nil || got.Latitude != 48.15 || got.Longitude != 11.46 {
		t.Fatalf("household = %+v, %v", got, err)
	}
	if err := db.SetHouseholdCoordinates("unknown", 1, 2); err == nil {
		t.Fatal("unknown household accepted")
	}
}
