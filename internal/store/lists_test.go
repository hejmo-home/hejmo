package store_test

import (
	"errors"
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

func newListFixture(t *testing.T) (*store.DB, store.Household) {
	t.Helper()
	db, _ := openTemp(t)
	h, _ := seedParent(t, db)
	return db, h
}

func mustList(t *testing.T, db *store.DB, h store.Household, name string, sort list.Sort) list.List {
	t.Helper()
	l, err := db.CreateList(h.ID, list.List{Name: name, Color: "green", Symbol: "square-filled", Sort: sort})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func mustEntry(t *testing.T, db *store.DB, h store.Household, l list.List, text string) list.Entry {
	t.Helper()
	e, err := db.AddEntry(h.ID, l.ID, text)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func entryTexts(t *testing.T, db *store.DB, h store.Household, l list.List) []string {
	t.Helper()
	es, err := db.Entries(h.ID, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Text)
	}
	return out
}

func sameTexts(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestListsRoundTripInCreationOrder(t *testing.T) {
	db, h := newListFixture(t)
	a, b := mustList(t, db, h, "Shopping", list.Manual), mustList(t, db, h, "To-dos", list.Alphabetical)
	got, err := db.Lists(h.ID)
	if err != nil || len(got) != 2 || got[0].ID != a.ID || got[1].ID != b.ID || got[1].Sort != list.Alphabetical || got[0].Color != "green" {
		t.Fatalf("Lists = %+v, %v", got, err)
	}
	if _, err := db.CreateList(h.ID, list.List{Name: " ", Color: "green", Symbol: "square-filled", Sort: list.Manual}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("blank name = %v", err)
	}
	if _, err := db.List(h.ID, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown = %v", err)
	}
}

func TestManualListKeepsInsertionOrderAndMovesEntries(t *testing.T) {
	db, h := newListFixture(t)
	l := mustList(t, db, h, "Shopping", list.Manual)
	mustEntry(t, db, h, l, "Milk")
	mustEntry(t, db, h, l, "Bread")
	c := mustEntry(t, db, h, l, "Apples")
	if got := entryTexts(t, db, h, l); !sameTexts(got, []string{"Milk", "Bread", "Apples"}) {
		t.Fatalf("order = %v", got)
	}
	if err := db.MoveEntry(h.ID, l.ID, c.ID, 0); err != nil {
		t.Fatal(err)
	}
	if got := entryTexts(t, db, h, l); !sameTexts(got, []string{"Apples", "Milk", "Bread"}) {
		t.Fatalf("after move = %v", got)
	}
}

// Acceptance: Given a list with alphabetical sorting, when an entry is added,
// then it appears in the correct alphabetical position.
func TestAlphabeticalListInsertsEntriesInPlace(t *testing.T) {
	db, h := newListFixture(t)
	l := mustList(t, db, h, "Shopping", list.Alphabetical)
	for _, s := range []string{"Milk", "apples", "Zucchini"} {
		mustEntry(t, db, h, l, s)
	}
	mustEntry(t, db, h, l, "Bread")
	if got := entryTexts(t, db, h, l); !sameTexts(got, []string{"apples", "Bread", "Milk", "Zucchini"}) {
		t.Fatalf("order = %v", got)
	}
	// Switching to manual keeps the order the entries were added in.
	l.Sort = list.Manual
	if _, err := db.UpdateList(h.ID, l); err != nil {
		t.Fatal(err)
	}
	if got := entryTexts(t, db, h, l); !sameTexts(got, []string{"Milk", "apples", "Zucchini", "Bread"}) {
		t.Fatalf("manual = %v", got)
	}
	// Moving in an alphabetical list is refused.
	l.Sort = list.Alphabetical
	_, _ = db.UpdateList(h.ID, l)
	es, _ := db.Entries(h.ID, l.ID)
	if err := db.MoveEntry(h.ID, l.ID, es[0].ID, 2); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("move in alphabetical list = %v", err)
	}
}

// Acceptance: Given a checked-off entry, when "Delete completed" is selected,
// then only the checked-off entries disappear.
func TestDeleteCompletedRemovesOnlyTickedEntries(t *testing.T) {
	db, h := newListFixture(t)
	l := mustList(t, db, h, "Shopping", list.Manual)
	milk, bread := mustEntry(t, db, h, l, "Milk"), mustEntry(t, db, h, l, "Bread")
	mustEntry(t, db, h, l, "Eggs")
	other := mustList(t, db, h, "To-dos", list.Manual)
	oe := mustEntry(t, db, h, other, "Call")
	for _, id := range []string{milk.ID, bread.ID} {
		if err := db.SetEntryDone(h.ID, l.ID, id, true); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.SetEntryDone(h.ID, other.ID, oe.ID, true)
	n, err := db.DeleteCompleted(h.ID, l.ID)
	if err != nil || n != 2 {
		t.Fatalf("DeleteCompleted = %d, %v", n, err)
	}
	if got := entryTexts(t, db, h, l); !sameTexts(got, []string{"Eggs"}) {
		t.Fatalf("left = %v", got)
	}
	if got := entryTexts(t, db, h, other); len(got) != 1 {
		t.Fatalf("another list lost entries: %v", got)
	}
}

func TestTickingTwiceAndUntickingAndDeletingEntries(t *testing.T) {
	db, h := newListFixture(t)
	l := mustList(t, db, h, "Shopping", list.Manual)
	e := mustEntry(t, db, h, l, "Milk")
	for i := 0; i < 2; i++ {
		if err := db.SetEntryDone(h.ID, l.ID, e.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	if es, _ := db.Entries(h.ID, l.ID); !es[0].Done {
		t.Fatal("not done")
	}
	_ = db.SetEntryDone(h.ID, l.ID, e.ID, false)
	if es, _ := db.Entries(h.ID, l.ID); es[0].Done {
		t.Fatal("still done")
	}
	if err := db.SetEntryDone(h.ID, l.ID, "nope", true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown = %v", err)
	}
	if err := db.DeleteEntry(h.ID, l.ID, e.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteEntry(h.ID, l.ID, e.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
	if _, err := db.AddEntry(h.ID, l.ID, "  "); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("blank = %v", err)
	}
}

func TestDeletingAListDeletesItsEntries(t *testing.T) {
	db, h := newListFixture(t)
	l := mustList(t, db, h, "Shopping", list.Manual)
	mustEntry(t, db, h, l, "Milk")
	if err := db.DeleteList(h.ID, l.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteList(h.ID, l.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
	var n int
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM list_entries`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d entries left", n)
	}
}

func TestEntryCountsPerList(t *testing.T) {
	db, h := newListFixture(t)
	l := mustList(t, db, h, "Shopping", list.Manual)
	a := mustEntry(t, db, h, l, "Milk")
	mustEntry(t, db, h, l, "Bread")
	_ = db.SetEntryDone(h.ID, l.ID, a.ID, true)
	counts, err := db.EntryCounts(h.ID)
	if err != nil || counts[l.ID].Open != 1 || counts[l.ID].Done != 1 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
}
