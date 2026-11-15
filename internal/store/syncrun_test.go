package store_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

func wantRun(t *testing.T, what string, got, want store.SyncRun) {
	t.Helper()
	if got != want {
		t.Errorf("%s: run = %+v, want %+v", what, got, want)
	}
}

func TestSyncStoresPerRunEventCountAndChanges(t *testing.T) {
	f := newCalFixture(t)
	c := newICS(t, f)

	run, err := f.db.CompleteSync(f.h.ID, c.ID, []store.SyncItem{
		item("a", "", "Choir", at(2026, 11, 20, 17, 0)),
		item("b", "", "Fair", at(2026, 11, 21, 10, 0)),
	}, nil, t0, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	wantRun(t, "first sync", run, store.SyncRun{Events: 2, New: 2})

	// Choir renamed, Fair gone, Swim new.
	run, err = f.db.CompleteSync(f.h.ID, c.ID, []store.SyncItem{
		item("a", "", "Choir rehearsal", at(2026, 11, 20, 17, 0)),
		item("c", "", "Swim", at(2026, 11, 22, 9, 0)),
	}, nil, t0.Add(time.Hour), t0.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	wantRun(t, "second sync", run, store.SyncRun{Events: 2, New: 1, Changed: 1, Removed: 1})

	// The same feed again changes nothing.
	run, err = f.db.CompleteSync(f.h.ID, c.ID, []store.SyncItem{
		item("a", "", "Choir rehearsal", at(2026, 11, 20, 17, 0)),
		item("c", "", "Swim", at(2026, 11, 22, 9, 0)),
	}, nil, t0.Add(2*time.Hour), t0.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	wantRun(t, "third sync", run, store.SyncRun{Events: 2})

	got, _ := f.db.SyncedCalendar(f.h.ID, c.ID)
	wantRun(t, "stored", got.Sync.LastRun, store.SyncRun{Events: 2})

	// A failed run leaves the last successful run's numbers in place.
	if err := f.db.FailSync(f.h.ID, c.ID, i18n.Msg("sync.fail.fetch", "detail", "x"), t0.Add(3*time.Hour), t0.Add(4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ = f.db.SyncedCalendar(f.h.ID, c.ID)
	wantRun(t, "after a failure", got.Sync.LastRun, store.SyncRun{Events: 2})
}

func TestASeriesCountsOnceAndAChangedOccurrenceChangesTheSeries(t *testing.T) {
	f := newCalFixture(t)
	c := newICS(t, f)
	master := item("a", "", "Choir", at(2026, 11, 2, 17, 0))
	master.Event.RRule = "FREQ=WEEKLY;COUNT=3"
	moved := item("a#x", "a", "Choir (moved)", at(2026, 11, 9, 19, 0))
	moved.Event.RecurrenceID = at(2026, 11, 9, 17, 0)

	run, err := f.db.CompleteSync(f.h.ID, c.ID, []store.SyncItem{master, moved}, nil, t0, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	wantRun(t, "series with a moved occurrence", run, store.SyncRun{Events: 1, New: 1})

	// The moved occurrence moves again: the series changed, once.
	moved.Event.Start, moved.Event.End = at(2026, 11, 9, 20, 0), at(2026, 11, 9, 21, 0)
	run, err = f.db.CompleteSync(f.h.ID, c.ID, []store.SyncItem{master, moved}, nil, t0.Add(time.Hour), t0.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	wantRun(t, "occurrence moved again", run, store.SyncRun{Events: 1, Changed: 1})

	// The whole series goes away.
	run, err = f.db.CompleteSync(f.h.ID, c.ID, nil, nil, t0.Add(2*time.Hour), t0.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	wantRun(t, "series removed", run, store.SyncRun{Removed: 1})
}

func TestUpdateSyncedCalendarChangesNameColourAndAudience(t *testing.T) {
	f := newCalFixture(t)
	c := newICS(t, f)
	if err := f.db.UpdateSyncedCalendar(f.h.ID, c.ID, "School and club", "#4E7D52", calendar.Audience{People: []string{f.mia.ID}}); err != nil {
		t.Fatal(err)
	}
	got, _ := f.db.SyncedCalendar(f.h.ID, c.ID)
	if got.Name != "School and club" || got.Color != "#4E7D52" || len(got.Audience.People) != 1 {
		t.Fatalf("calendar = %+v", got)
	}
	// Everyone again: the audience is emptied.
	if err := f.db.UpdateSyncedCalendar(f.h.ID, c.ID, "School and club", "#4E7D52", calendar.Audience{}); err != nil {
		t.Fatal(err)
	}
	got, _ = f.db.SyncedCalendar(f.h.ID, c.ID)
	if len(got.Audience.People) != 0 {
		t.Fatalf("audience = %+v", got.Audience)
	}
	if err := f.db.UpdateSyncedCalendar(f.h.ID, c.ID, " ", "#4E7D52", calendar.Audience{}); err == nil {
		t.Error("a blank name was accepted")
	}
	if err := f.db.UpdateSyncedCalendar(f.h.ID, "unknown", "X", "#4E7D52", calendar.Audience{}); err != store.ErrNotFound {
		t.Errorf("unknown calendar: %v", err)
	}
}
