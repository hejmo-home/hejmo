package store_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/hejmo-home/hejmo/internal/layout"
	"github.com/hejmo-home/hejmo/internal/store"
)

func pairDisplayNamed(t *testing.T, db *store.DB, h store.Household, hash, name string) store.Display {
	t.Helper()
	d, err := db.OpenPairing(h.ID, hash, pairT0)
	if err != nil {
		t.Fatal(err)
	}
	d, err = db.ConfirmPairing(h.ID, d.Code, store.Display{Name: name, Type: store.DisplayScreen}, pairT0)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func personIDs(t *testing.T, db *store.DB, h store.Household) []string {
	t.Helper()
	ps, err := db.People(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}

// Q89: a display without a stored layout shows today's layout: every person, every current block.
func TestADisplayWithoutALayoutShowsTodaysLayout(t *testing.T) {
	db, h := householdForDisplays(t)
	if _, err := db.AddPerson(h.ID, store.Person{Name: "Mama", Role: store.RoleParent}); err != nil {
		t.Fatal(err)
	}
	d := pairedDisplay(t, db, h, "t1", "Kitchen")
	l, err := db.DisplayLayout(h.ID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Whole || !slices.Equal(l.People, personIDs(t, db, h)) || !l.Enabled(layout.Clock) || l.Enabled(layout.List) {
		t.Fatalf("default = %+v", l)
	}
	// A new person joins at the end of "Ganze Familie".
	p, _ := db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	l, _ = db.DisplayLayout(h.ID, d.ID)
	if l.People[len(l.People)-1] != p.ID {
		t.Errorf("new person not at the end: %v", l.People)
	}
}

func TestALayoutIsKeptPerDisplay(t *testing.T) {
	db, h := householdForDisplays(t)
	a, _ := db.AddPerson(h.ID, store.Person{Name: "Mama", Role: store.RoleParent})
	b, _ := db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	kitchen, hall := pairedDisplay(t, db, h, "t1", "Kitchen"), pairedDisplay(t, db, h, "t2", "Hall")

	l := layout.Default(nil)
	l.Whole, l.People, l.Rotate, l.OrderDay = false, []string{b.ID, a.ID}, true, 42
	l.Blocks = []layout.Block{{ID: layout.Weather, On: false}, {ID: layout.Clock, On: true}, {ID: layout.List, On: true}}
	l.ListID, l.ListName = "some-list", "Packing"
	if err := db.SetDisplayLayout(h.ID, kitchen.ID, l); err != nil {
		t.Fatal(err)
	}
	got, err := db.DisplayLayout(h.ID, kitchen.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Whole || !slices.Equal(got.People, []string{b.ID, a.ID}) || !got.Rotate || got.OrderDay != 42 || got.ListID != "some-list" || got.ListName != "Packing" {
		t.Fatalf("stored = %+v", got)
	}
	if got.Enabled(layout.Weather) || !got.Enabled(layout.List) || !got.Enabled(layout.Tasks) {
		t.Errorf("blocks = %+v", got.Blocks)
	}
	if ids := got.In(layout.Header); ids[0].ID != layout.Weather {
		t.Errorf("order of the header = %+v", ids)
	}
	if other, _ := db.DisplayLayout(h.ID, hall.ID); !other.Whole {
		t.Errorf("the other display changed: %+v", other)
	}
	// Nobody is refused.
	if err := db.SetDisplayLayout(h.ID, kitchen.ID, layout.Layout{}); !errors.Is(err, layout.ErrNoPeople) {
		t.Errorf("no people = %v", err)
	}
	// A person who is deleted drops out of the layout.
	if _, err := db.SQL().Exec(`DELETE FROM people WHERE id = ?`, a.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ = db.DisplayLayout(h.ID, kitchen.ID); !slices.Equal(got.People, []string{b.ID}) {
		t.Errorf("after deleting = %v", got.People)
	}
	// Unknown displays.
	if _, err := db.DisplayLayout(h.ID, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown display = %v", err)
	}
}

// Q84: copy a layout from another display.
func TestACopiedLayoutIsOwnedByTheTargetDisplay(t *testing.T) {
	db, h := householdForDisplays(t)
	a, _ := db.AddPerson(h.ID, store.Person{Name: "Mama", Role: store.RoleParent})
	b, _ := db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	from, to := pairedDisplay(t, db, h, "t1", "Kitchen"), pairedDisplay(t, db, h, "t2", "Hall")
	l := layout.Default(nil)
	l.Whole, l.People = false, []string{b.ID, a.ID}
	if err := db.SetDisplayLayout(h.ID, from.ID, l); err != nil {
		t.Fatal(err)
	}
	if err := db.CopyDisplayLayout(h.ID, from.ID, to.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := db.DisplayLayout(h.ID, to.ID)
	if !slices.Equal(got.People, []string{b.ID, a.ID}) || got.Whole {
		t.Fatalf("copy = %+v", got)
	}
	// Changing the copy leaves the source alone: no shared templates.
	l.People = []string{a.ID}
	if err := db.SetDisplayLayout(h.ID, to.ID, l); err != nil {
		t.Fatal(err)
	}
	if src, _ := db.DisplayLayout(h.ID, from.ID); len(src.People) != 2 {
		t.Errorf("source changed: %+v", src)
	}
	if err := db.CopyDisplayLayout(h.ID, "nope", to.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown source = %v", err)
	}
}

// A display says how it hangs; the settings page draws the preview that way.
func TestADisplayRemembersHowItHangs(t *testing.T) {
	db, h := householdForDisplays(t)
	d := pairedDisplay(t, db, h, "t1", "Kitchen")
	if d.Portrait {
		t.Fatal("landscape until it says otherwise")
	}
	if err := db.SetDisplayOrientation("t1", true); err != nil {
		t.Fatal(err)
	}
	got, _ := db.Display(h.ID, d.ID)
	if !got.Portrait {
		t.Fatal("portrait not kept")
	}
}
