package store_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

// imported puts one synced event into a CalDAV calendar as an import does.
func imported(t *testing.T, f davFixture, cal store.SyncedCalendar, file, uid, title string) string {
	t.Helper()
	u := cal.URL + file
	if err := f.db.CompleteObjectSync(f.h.ID, cal.ID, []store.ObjectChange{obj(u, "e1", item(uid, "", title, at(2026, 11, 20, 15, 0)))}, nil, nil, t0, t0); err != nil {
		t.Fatal(err)
	}
	return u
}

func eventOf(t *testing.T, f davFixture, title string) calendar.Event {
	t.Helper()
	evs, err := f.db.EventsBetween(f.h.ID, at(2026, 1, 1, 0, 0), at(2027, 12, 31, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Title == title {
			return e
		}
	}
	t.Fatalf("no event %q", title)
	return calendar.Event{}
}

func TestOnlyCalDAVCalendarsCanBeSwitchedToReadOnly(t *testing.T) {
	f := newDAVFixture(t)
	if err := f.db.SetCalendarReadOnly(f.h.ID, f.a.ID, true); err != nil {
		t.Fatal(err)
	}
	if c, _ := f.db.Calendar(f.h.ID, f.a.ID); !c.ReadOnly {
		t.Fatal("not read-only after the switch")
	}
	if err := f.db.SetCalendarReadOnly(f.h.ID, f.a.ID, false); err != nil {
		t.Fatal(err)
	}
	ics, _ := f.db.CreateICSCalendar(f.h.ID, store.NewICS{Name: "H", URL: "https://example.org/h.ics", Color: "#2F5E7A", IntervalMinutes: 60, Now: t0})
	if err := f.db.SetCalendarReadOnly(f.h.ID, ics.ID, false); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("ICS calendar switched: %v", err)
	}
	family, _ := f.db.FamilyCalendar(f.h.ID)
	if err := f.db.SetCalendarReadOnly(f.h.ID, family.ID, true); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("family calendar switched: %v", err)
	}
	if err := f.db.SetCalendarReadOnly(f.h.ID, "nope", true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown calendar: %v", err)
	}
}

func TestEditingASyncedEventQueuesItsObjectForPushing(t *testing.T) {
	f := newDAVFixture(t)
	u := imported(t, f, f.a, "swim.ics", "swim@x", "Swimming")
	e := eventOf(t, f, "Swimming")
	e.Title = "Swimming lesson"
	if err := f.db.ApplyChange(f.h.ID, calendar.Change{Update: []calendar.Event{e}}); err != nil {
		t.Fatal(err)
	}
	pend, err := f.db.PendingObjects(f.a.ID)
	if err != nil || len(pend) != 1 || pend[0].URL != u || pend[0].ETag != "e1" || pend[0].Rev != 1 || pend[0].Raw == "" || pend[0].ChangedAt.IsZero() {
		t.Fatalf("pending = %+v, %v", pend, err)
	}
	if known, _ := f.db.RemoteObjects(f.a.ID, false); len(known) != 0 {
		t.Fatalf("a pending object is still listed as known: %v", known)
	}
	obj, err := f.db.ObjectEvents(f.h.ID, f.a.ID, u)
	if err != nil || obj.UID != "swim@x" || len(obj.Events) != 1 || obj.Events[0].Title != "Swimming lesson" {
		t.Fatalf("object = %+v, %v", obj, err)
	}
}

func TestANewEventInATwoWayCalendarGetsItsOwnObject(t *testing.T) {
	f := newDAVFixture(t)
	e, err := f.db.CreateEvent(f.h.ID, calendar.Event{CalendarID: f.a.ID, Title: "Piano", Start: at(2026, 11, 21, 15, 0), End: at(2026, 11, 21, 16, 0)})
	if err != nil {
		t.Fatal(err)
	}
	pend, _ := f.db.PendingObjects(f.a.ID)
	want := f.a.URL + e.ID + ".ics"
	if len(pend) != 1 || pend[0].URL != want || pend[0].ETag != "" || pend[0].Raw != "" {
		t.Fatalf("pending = %+v, want one new object at %s", pend, want)
	}
	obj, _ := f.db.ObjectEvents(f.h.ID, f.a.ID, want)
	if obj.UID != e.ID || len(obj.Events) != 1 {
		t.Fatalf("object = %+v", obj)
	}
}

func TestADeletedEventKeepsItsObjectQueuedWithoutEvents(t *testing.T) {
	f := newDAVFixture(t)
	u := imported(t, f, f.a, "swim.ics", "swim@x", "Swimming")
	e := eventOf(t, f, "Swimming")
	if err := f.db.ApplyChange(f.h.ID, calendar.Change{Delete: []string{e.ID}}); err != nil {
		t.Fatal(err)
	}
	pend, _ := f.db.PendingObjects(f.a.ID)
	if len(pend) != 1 || pend[0].URL != u {
		t.Fatalf("pending = %+v", pend)
	}
	if obj, _ := f.db.ObjectEvents(f.h.ID, f.a.ID, u); len(obj.Events) != 0 {
		t.Fatalf("events = %+v", obj.Events)
	}
}

func TestNothingIsQueuedForAReadOnlyCalendar(t *testing.T) {
	f := newDAVFixture(t)
	imported(t, f, f.a, "swim.ics", "swim@x", "Swimming")
	if err := f.db.SetCalendarReadOnly(f.h.ID, f.a.ID, true); err != nil {
		t.Fatal(err)
	}
	e := eventOf(t, f, "Swimming")
	e.Title = "Changed"
	_ = f.db.ApplyChange(f.h.ID, calendar.Change{Update: []calendar.Event{e}})
	if pend, _ := f.db.PendingObjects(f.a.ID); len(pend) != 0 {
		t.Fatalf("pending = %+v", pend)
	}
}

func TestSeriesEditsQueueTheOldAndTheNewObject(t *testing.T) {
	f := newDAVFixture(t)
	u := f.a.URL + "choir.ics"
	master := item("choir@x", "", "Choir", at(2026, 11, 2, 17, 0))
	master.Event.RRule = "FREQ=WEEKLY;COUNT=6"
	if err := f.db.CompleteObjectSync(f.h.ID, f.a.ID, []store.ObjectChange{obj(u, "e1", master)}, nil, nil, t0, t0); err != nil {
		t.Fatal(err)
	}
	series, _ := f.db.Series(f.h.ID, eventOf(t, f, "Choir").ID)
	title := "Choir (new)"
	ch, err := calendar.Editor{NewID: store.NewID}.Edit(series, at(2026, 11, 16, 17, 0), calendar.ScopeFollowing, calendar.Patch{Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.ApplyChange(f.h.ID, ch); err != nil {
		t.Fatal(err)
	}
	pend, _ := f.db.PendingObjects(f.a.ID)
	if len(pend) != 2 {
		t.Fatalf("pending = %+v, want the old object and a new one", pend)
	}
	var urls []string
	for _, p := range pend {
		urls = append(urls, p.URL)
	}
	if !strings.Contains(strings.Join(urls, " "), u) {
		t.Fatalf("old object %s not queued: %v", u, urls)
	}
}

func TestFinishingAPushClearsOnlyWhatWasPushed(t *testing.T) {
	f := newDAVFixture(t)
	u := imported(t, f, f.a, "swim.ics", "swim@x", "Swimming")
	current := "Swimming"
	edit := func(title string) {
		e := eventOf(t, f, current)
		current = title
		e.Title = title
		if err := f.db.ApplyChange(f.h.ID, calendar.Change{Update: []calendar.Event{e}}); err != nil {
			t.Fatal(err)
		}
	}
	edit("One")
	pend, _ := f.db.PendingObjects(f.a.ID)
	rev := pend[0].Rev
	edit("Two") // while the push of "One" is on its way
	if err := f.db.PushDone(f.a.ID, u, rev, "e2", "raw-one"); err != nil {
		t.Fatal(err)
	}
	pend, _ = f.db.PendingObjects(f.a.ID)
	if len(pend) != 1 || pend[0].ETag != "e2" || pend[0].Raw != "raw-one" {
		t.Fatalf("pending = %+v, want the object still queued with the pushed ETag", pend)
	}
	if err := f.db.PushDone(f.a.ID, u, pend[0].Rev, "e3", "raw-two"); err != nil {
		t.Fatal(err)
	}
	if pend, _ = f.db.PendingObjects(f.a.ID); len(pend) != 0 {
		t.Fatalf("pending = %+v", pend)
	}
	if known, _ := f.db.RemoteObjects(f.a.ID, false); known[u] != "e3" {
		t.Fatalf("known = %v", known)
	}
}

func TestFinishingADeletePushForgetsTheObject(t *testing.T) {
	f := newDAVFixture(t)
	u := imported(t, f, f.a, "swim.ics", "swim@x", "Swimming")
	_ = f.db.ApplyChange(f.h.ID, calendar.Change{Delete: []string{eventOf(t, f, "Swimming").ID}})
	pend, _ := f.db.PendingObjects(f.a.ID)
	if err := f.db.PushDeleted(f.a.ID, u, pend[0].Rev); err != nil {
		t.Fatal(err)
	}
	if pend, _ = f.db.PendingObjects(f.a.ID); len(pend) != 0 {
		t.Fatalf("pending = %+v", pend)
	}
	if known, _ := f.db.RemoteObjects(f.a.ID, false); len(known) != 0 {
		t.Fatalf("known = %v", known)
	}
}

func TestOutsideWinsReplacesTheBoxVersionAndKeepsTheLosingTextInTheLog(t *testing.T) {
	f := newDAVFixture(t)
	u := imported(t, f, f.a, "swim.ics", "swim@x", "Swimming")
	e := eventOf(t, f, "Swimming")
	e.Title = "Box title"
	_ = f.db.ApplyChange(f.h.ID, calendar.Change{Update: []calendar.Event{e}})
	pend, _ := f.db.PendingObjects(f.a.ID)

	outside := obj(u, "e9", item("swim@x", "", "Outside title", at(2026, 11, 20, 15, 0)))
	conflict := store.Conflict{Message: msg("sync.conflict.outside_won", "title", "Box title"), Detail: "BEGIN:VCALENDAR box version"}
	if err := f.db.ResolveToOutside(f.h.ID, f.a.ID, outside, pend[0].Rev, conflict, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := titles(t, f2(f), f.a.ID); len(got) != 1 || got[0] != "Outside title" {
		t.Fatalf("events = %v", got)
	}
	if pend, _ = f.db.PendingObjects(f.a.ID); len(pend) != 0 {
		t.Fatalf("pending = %+v", pend)
	}
	log, _ := f.db.SyncLog(f.h.ID, 10)
	if len(log) != 1 || log[0].Kind != "conflict" || log[0].Detail != "BEGIN:VCALENDAR box version" || log[0].Message.Key != "sync.conflict.outside_won" {
		t.Fatalf("log = %+v", log)
	}
}

func TestConflictLogEntriesAreKeptForThirtyDays(t *testing.T) {
	f := newDAVFixture(t)
	if err := f.db.LogConflict(f.h.ID, f.a.ID, msg("sync.conflict.box_won", "title", "X"), "losing text", t0); err != nil {
		t.Fatal(err)
	}
	if err := f.db.CompleteObjectSync(f.h.ID, f.a.ID, nil, nil, nil, t0.Add(29*24*time.Hour), t0); err != nil {
		t.Fatal(err)
	}
	if log, _ := f.db.SyncLog(f.h.ID, 10); len(log) != 1 || log[0].Detail != "losing text" {
		t.Fatalf("log after 29 days = %+v", log)
	}
	if err := f.db.CompleteObjectSync(f.h.ID, f.a.ID, nil, nil, nil, t0.Add(31*24*time.Hour), t0); err != nil {
		t.Fatal(err)
	}
	if log, _ := f.db.SyncLog(f.h.ID, 10); len(log) != 0 {
		t.Fatalf("log after 31 days = %+v", log)
	}
}

func msg(key string, kv ...string) i18n.Message { return i18n.Msg(key, kv...) }

func TestChangeToACalendarSwitchedToReadOnlyIsRefusedNotStoredUnqueued(t *testing.T) {
	f := newDAVFixture(t)
	imported(t, f, f.a, "swim.ics", "swim@x", "Swimming")
	e := eventOf(t, f, "Swimming")
	if err := f.db.SetCalendarReadOnly(f.h.ID, f.a.ID, true); err != nil {
		t.Fatal(err)
	}
	e.Title = "Changed"
	if err := f.db.ApplyChange(f.h.ID, calendar.Change{Update: []calendar.Event{e}}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("ApplyChange err = %v, want ErrInvalid", err)
	}
	if _, err := f.db.CreateEvent(f.h.ID, calendar.Event{CalendarID: f.a.ID, Title: "New", Start: e.Start, End: e.End}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("CreateEvent err = %v, want ErrInvalid", err)
	}
	if got := eventOf(t, f, "Swimming"); got.Title != "Swimming" {
		t.Fatalf("the refused change was stored: %q", got.Title)
	}
}
