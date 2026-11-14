package store_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

// A changed occurrence can outlive its series master in the feed. Deleting the
// master cascades to the stored occurrence, which must be stored again.
func TestSyncSurvivesRemovedMasterOfStoredOccurrence(t *testing.T) {
	f := newCalFixture(t)
	c := newICS(t, f)
	master := item("a", "", "Swim", at(2026, 11, 2, 17, 0))
	master.Event.RRule = "FREQ=WEEKLY;COUNT=3"
	moved := item("a#x", "a", "Swim moved", at(2026, 11, 9, 19, 0))
	moved.Event.RecurrenceID = at(2026, 11, 9, 17, 0)
	if err := f.db.CompleteSync(f.h.ID, c.ID, []store.SyncItem{master, moved}, nil, t0, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Second sync: the master is gone, the occurrence is now a single event.
	single := moved
	single.SeriesKey = ""
	single.Event.SeriesID = ""
	single.Event.RecurrenceID = time.Time{}
	if err := f.db.CompleteSync(f.h.ID, c.ID, []store.SyncItem{single}, nil, t0.Add(time.Hour), t0.Add(2*time.Hour)); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if got := titles(t, f, c.ID); len(got) != 1 || got[0] != "Swim moved" {
		t.Fatalf("titles = %v", got)
	}
	if err := f.db.CompleteSync(f.h.ID, c.ID, []store.SyncItem{single}, nil, t0.Add(2*time.Hour), t0.Add(3*time.Hour)); err != nil {
		t.Fatalf("third sync: %v", err)
	}
}
