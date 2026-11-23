package store_test

import (
	"errors"
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

func TestNewListsAreNotSharedAndSharingIsSwitchedPerList(t *testing.T) {
	db, h, l := shopping(t)
	other := mustList(t, db, h, "Packen", list.Manual)
	if l.Shared {
		t.Fatal("a new list is off")
	}
	if err := db.SetListsShared(h.ID, map[string]bool{l.ID: true}); err != nil {
		t.Fatal(err)
	}
	got, _ := db.List(h.ID, l.ID)
	o, _ := db.List(h.ID, other.ID)
	if !got.Shared || o.Shared {
		t.Fatalf("shared %v %v", got.Shared, o.Shared)
	}
	// Renaming keeps the switch.
	got.Name = "Wocheneinkauf"
	if _, err := db.UpdateList(h.ID, got); err != nil {
		t.Fatal(err)
	}
	if got, _ = db.List(h.ID, l.ID); !got.Shared {
		t.Error("update lost the switch")
	}
}

func TestSharingAListThatIsGoneStoresNothing(t *testing.T) {
	db, h, l := shopping(t)
	err := db.SetListsShared(h.ID, map[string]bool{l.ID: true, "gone": true})
	var miss *store.MissingListError
	if !errors.As(err, &miss) || miss.ID != "gone" || !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if got, _ := db.List(h.ID, l.ID); got.Shared {
		t.Error("partly stored")
	}
}

func TestEntryNotesAreKeptTickedAndLimited(t *testing.T) {
	db, h, l := shopping(t)
	e, _ := db.AddEntry(h.ID, l.ID, "Milch")
	if err := db.SetEntryNote(h.ID, l.ID, e.ID, "  laktosefrei "); err != nil {
		t.Fatal(err)
	}
	if err := db.SetEntryDone(h.ID, l.ID, e.ID, true); err != nil {
		t.Fatal(err)
	}
	es, _ := db.Entries(h.ID, l.ID)
	if es[0].Note != "laktosefrei" || !es[0].Done {
		t.Fatalf("%+v", es[0])
	}
	if err := db.SetEntryNote(h.ID, l.ID, e.ID, ""); err != nil {
		t.Fatal(err)
	}
	if es, _ = db.Entries(h.ID, l.ID); es[0].Note != "" {
		t.Error("empty removes the note")
	}
	long := make([]rune, list.MaxNote+1)
	for i := range long {
		long[i] = 'x'
	}
	if err := db.SetEntryNote(h.ID, l.ID, e.ID, string(long)); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("501 characters: %v", err)
	}
}

func TestANewReminderIsAddedUpByTheRuleOfAdding(t *testing.T) {
	db, h, l := shopping(t)
	if _, err := db.AddItem(h.ID, l.ID, store.Item{Text: "Mehl", ArticleID: "flour", Qty: list.NewQuantity(1, "kg")}, show("kg")); err != nil {
		t.Fatal(err)
	}
	res, err := db.SyncNewEntry(h.ID, l.ID, "reminder-1", store.SyncEdit{Text: "Mehl", ArticleID: "flour", Qty: list.NewQuantity(500, "g"), Note: "Type 405"}, show("kg"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan.Action != list.AddedTo || res.Entry.ID == "reminder-1" {
		t.Fatalf("%+v", res)
	}
	got := lines(t, db, h, l)
	if len(got) != 1 || got[0] != "Mehl 1.5 kg" {
		t.Fatalf("%v", got)
	}
	es, _ := db.Entries(h.ID, l.ID)
	if es[0].Note != "Type 405" {
		t.Errorf("note %q", es[0].Note)
	}
}

func TestANewReminderKeepsItsIDAndNote(t *testing.T) {
	db, h, l := shopping(t)
	res, err := db.SyncNewEntry(h.ID, l.ID, "reminder-1", store.SyncEdit{Text: "Bananen", Note: "grün"}, show("piece"))
	if err != nil || res.Entry.ID != "reminder-1" {
		t.Fatalf("%+v %v", res, err)
	}
	es, _ := db.Entries(h.ID, l.ID)
	if es[0].ID != "reminder-1" || es[0].Note != "grün" {
		t.Fatalf("%+v", es[0])
	}
	if _, err := db.SyncNewEntry(h.ID, l.ID, "reminder-1", store.SyncEdit{Text: "Bananen"}, show("piece")); !errors.Is(err, store.ErrIDTaken) {
		t.Errorf("same id twice: %v", err)
	}
}

func TestEditingAReminderChangesTextQuantityNoteAndTick(t *testing.T) {
	db, h, l := shopping(t)
	e, _ := db.AddItem(h.ID, l.ID, store.Item{Text: "Mehl", Qty: list.NewQuantity(1, "kg")}, show("kg"))
	got, merged, err := db.SyncEntry(h.ID, l.ID, e.Entry.ID, store.SyncEdit{Text: "Mehl", Qty: list.NewQuantity(2, "kg"), Note: "Type 405", Done: true}, show("kg"))
	if err != nil || merged {
		t.Fatalf("%v %v", merged, err)
	}
	es, _ := db.Entries(h.ID, l.ID)
	if es[0].Qty.Base != 2000 || es[0].Note != "Type 405" || !es[0].Done || got.ID != es[0].ID {
		t.Fatalf("%+v", es[0])
	}
	if _, _, err := db.SyncEntry(h.ID, l.ID, "zzz", store.SyncEdit{Text: "x"}, show()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("gone: %v", err)
	}
}

func TestEditingAReminderIntoAnotherEntryOfTheArticleMergesThem(t *testing.T) {
	db, h, l := shopping(t)
	a, _ := db.AddItem(h.ID, l.ID, store.Item{Text: "Mehl", ArticleID: "flour", Qty: list.NewQuantity(1, "kg")}, show("kg"))
	b, _ := db.AddItem(h.ID, l.ID, store.Item{Text: "Zucker", Qty: list.NewQuantity(1, "kg")}, show("kg"))
	got, merged, err := db.SyncEntry(h.ID, l.ID, b.Entry.ID, store.SyncEdit{Text: "Mehl", ArticleID: "flour", Qty: list.NewQuantity(1, "kg")}, show("kg"))
	if err != nil || !merged || got.ID != a.Entry.ID {
		t.Fatalf("%+v %v %v", got, merged, err)
	}
	if l := lines(t, db, h, l); len(l) != 1 || l[0] != "Mehl 2 kg" {
		t.Fatalf("%v", l)
	}
}
