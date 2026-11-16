package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

// 6.7: a parent marks a read-only calendar as waste collection and can undo it.
func TestWasteCollectionMarkingOnAReadOnlyCalendar(t *testing.T) {
	f := newCalFixture(t)
	ics, err := f.db.CreateICSCalendar(f.h.ID, store.NewICS{Name: "Waste", URL: "https://example.org/w.ics", IntervalMinutes: 60, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := f.db.Calendar(f.h.ID, ics.ID)
	if got.Waste {
		t.Fatal("a new calendar is not a waste collection calendar")
	}
	if err := f.db.SetCalendarWaste(f.h.ID, ics.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ = f.db.Calendar(f.h.ID, ics.ID); !got.Waste {
		t.Fatal("marking did not stick")
	}
	if err := f.db.SetCalendarWaste(f.h.ID, ics.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, _ = f.db.Calendar(f.h.ID, ics.ID); got.Waste {
		t.Fatal("unmarking did not stick")
	}
	if err := f.db.SetCalendarWaste(f.h.ID, f.cal.ID, true); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("family calendar = %v, want ErrInvalid (only read-only calendars)", err)
	}
	if err := f.db.SetCalendarWaste(f.h.ID, "nope", true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown calendar = %v", err)
	}
}

// ADR 0015/0016: a note belongs to a day; a day has one note; past notes are deleted.
func TestDayNotesAreSetReplacedAndPruned(t *testing.T) {
	f := newCalFixture(t)
	if err := f.db.SetDayNote(f.h.ID, "2026-11-16", "Bring swimming bags"); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetDayNote(f.h.ID, "2026-11-16", "Bring the football kit"); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetDayNote(f.h.ID, "2026-11-14", "Old"); err != nil {
		t.Fatal(err)
	}
	if n, ok, _ := f.db.DayNote(f.h.ID, "2026-11-16"); !ok || n != "Bring the football kit" {
		t.Fatalf("note = %q %v", n, ok)
	}
	if err := f.db.PruneDayNotes(f.h.ID, "2026-11-15"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := f.db.DayNote(f.h.ID, "2026-11-14"); ok {
		t.Fatal("past note survived")
	}
	if _, ok, _ := f.db.DayNote(f.h.ID, "2026-11-16"); !ok {
		t.Fatal("future note was pruned")
	}
	if err := f.db.SetDayNote(f.h.ID, "2026-11-16", "  "); err != nil { // empty text deletes
		t.Fatal(err)
	}
	if _, ok, _ := f.db.DayNote(f.h.ID, "2026-11-16"); ok {
		t.Fatal("empty text did not delete")
	}
	if err := f.db.SetDayNote(f.h.ID, "tomorrow", "x"); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("bad date = %v", err)
	}
	list, _ := f.db.DayNotes(f.h.ID)
	if len(list) != 0 {
		t.Fatalf("notes = %v", list)
	}
}
