package store_test

import (
	"errors"
	"testing"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/store"
)

func exampleFamily(t *testing.T) (*store.DB, store.Household, map[string]store.Person) {
	t.Helper()
	db, _ := openTemp(t)
	h, err := db.CreateHousehold(store.Household{Name: "Family"})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]store.Person{}
	for _, n := range []string{"Mama", "Papa", "Sebastian", "Mia"} {
		role := store.RoleParent
		if n == "Sebastian" || n == "Mia" {
			role = store.RoleChild
		}
		p, err := db.AddPerson(h.ID, store.Person{Name: n, Role: role})
		if err != nil {
			t.Fatal(err)
		}
		out[n] = p
	}
	return db, h, out
}

func names(t *testing.T, db *store.DB, h store.Household) []string {
	t.Helper()
	ps, err := db.People(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

func TestUpdatePersonChangesNameColourAndShape(t *testing.T) {
	db, h, f := exampleFamily(t)
	if err := db.UpdatePerson(h.ID, f["Mia"].ID, "Mia Maier", "#4E7D52", "square-filled"); err != nil {
		t.Fatal(err)
	}
	got, err := db.Person(h.ID, f["Mia"].ID)
	if err != nil || got.Name != "Mia Maier" || got.Color != "#4E7D52" || got.Shape != "square-filled" || got.Role != store.RoleChild {
		t.Fatalf("Person = %+v, %v", got, err)
	}
	if err := db.UpdatePerson(h.ID, f["Mia"].ID, "  ", "", ""); err == nil {
		t.Fatal("blank name accepted")
	}
	if err := db.UpdatePerson(h.ID, "missing", "X", "", ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown person: %v", err)
	}
}

func TestSetPeopleOrderReordersTheFamilyView(t *testing.T) {
	db, h, f := exampleFamily(t)
	if err := db.SetPeopleOrder(h.ID, []string{f["Mia"].ID, f["Mama"].ID, f["Sebastian"].ID, f["Papa"].ID}); err != nil {
		t.Fatal(err)
	}
	if got := names(t, db, h); len(got) != 4 || got[0] != "Mia" || got[1] != "Mama" || got[2] != "Sebastian" || got[3] != "Papa" {
		t.Fatalf("order = %v", got)
	}
	// A person added later goes to the end.
	if _, err := db.AddPerson(h.ID, store.Person{Name: "Oma", Role: store.RoleParent}); err != nil {
		t.Fatal(err)
	}
	if got := names(t, db, h); got[4] != "Oma" {
		t.Fatalf("order = %v", got)
	}
}

func TestSetPeopleOrderNeedsEveryPersonOnce(t *testing.T) {
	db, h, f := exampleFamily(t)
	for name, ids := range map[string][]string{
		"missing":   {f["Mia"].ID, f["Mama"].ID},
		"duplicate": {f["Mia"].ID, f["Mia"].ID, f["Mama"].ID, f["Papa"].ID},
		"unknown":   {f["Mia"].ID, f["Mama"].ID, f["Papa"].ID, "nobody"},
	} {
		if err := db.SetPeopleOrder(h.ID, ids); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if got := names(t, db, h); got[0] != "Mama" {
		t.Fatalf("order changed: %v", got)
	}
}

func TestGroupCanBeRenamedRegroupedAndDeleted(t *testing.T) {
	db, h, f := exampleFamily(t)
	g, err := db.CreateGroup(h.ID, "School children", []string{f["Mia"].ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateGroup(h.ID, g.ID, "Pupils", []string{f["Mia"].ID, f["Sebastian"].ID}); err != nil {
		t.Fatal(err)
	}
	got, err := db.Group(h.ID, g.ID)
	if err != nil || got.Name != "Pupils" || len(got.Members) != 2 {
		t.Fatalf("Group = %+v, %v", got, err)
	}
	if err := db.UpdateGroup(h.ID, g.ID, "Pupils", []string{f["Mia"].ID}); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.Group(h.ID, g.ID); len(got.Members) != 1 || got.Members[0] != f["Mia"].ID {
		t.Fatalf("members = %+v", got.Members)
	}
	if err := db.UpdateGroup(h.ID, g.ID, " ", nil); err == nil {
		t.Fatal("blank name accepted")
	}
	if err := db.UpdateGroup(h.ID, "missing", "X", nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown group: %v", err)
	}
	if err := db.DeleteGroup(h.ID, g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Group(h.ID, g.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if err := db.DeleteGroup(h.ID, g.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

// A group that still decides who sees a calendar cannot be deleted: its audience
// would silently become the whole household.
func TestGroupInUseByACalendarIsNotDeleted(t *testing.T) {
	db, h, f := exampleFamily(t)
	g, _ := db.CreateGroup(h.ID, "School children", []string{f["Mia"].ID})
	_, err := db.CreateCalendar(store.Calendar{HouseholdID: h.ID, Name: "School", Kind: "ics", Audience: calendar.Audience{Groups: []string{g.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteGroup(h.ID, g.ID); !errors.Is(err, store.ErrInUse) {
		t.Fatalf("delete used group: %v, want ErrInUse", err)
	}
	other, _ := db.CreateGroup(h.ID, "Nursery", nil)
	if err := db.DeleteGroup(h.ID, other.ID); err != nil {
		t.Fatalf("delete unused group: %v", err)
	}
	if _, err := db.Group(h.ID, g.ID); err != nil {
		t.Fatalf("used group is gone: %v", err)
	}
}

// Another household's group id is unknown, not in use.
func TestDeletingAnotherHouseholdsGroupIsNotFound(t *testing.T) {
	db, h, f := exampleFamily(t)
	g, _ := db.CreateGroup(h.ID, "School children", []string{f["Mia"].ID})
	if _, err := db.CreateCalendar(store.Calendar{HouseholdID: h.ID, Name: "School", Kind: "ics", Audience: calendar.Audience{Groups: []string{g.ID}}}); err != nil {
		t.Fatal(err)
	}
	other, err := db.CreateHousehold(store.Household{Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteGroup(other.ID, g.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete foreign group: %v, want ErrNotFound", err)
	}
}
