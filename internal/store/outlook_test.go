package store_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/store"
)

type outlookFixture struct {
	db   *store.DB
	h    store.Household
	conn store.Connection
	own  store.SyncedCalendar
}

func newOutlookFixture(t *testing.T) outlookFixture {
	t.Helper()
	db, _ := openSealed(t)
	h, _ := seedParent(t, db)
	conn, err := db.CreateOutlookConnection(h.ID, store.NewOutlookConnection{Account: "mama@example.org", RefreshToken: "refresh-secret-1", Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	own, err := db.AddOutlookCalendar(h.ID, conn.ID, store.NewOutlookCalendar{Name: "Mama", RemoteID: "cal-1", Color: "#2F5E7A", IntervalMinutes: 5, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	return outlookFixture{db, h, conn, own}
}

func TestOutlookConnectionKeepsTheRefreshTokenSealedAndNamesTheAccount(t *testing.T) {
	f := newOutlookFixture(t)
	if f.conn.Kind != "outlook" || f.conn.Username != "mama@example.org" || f.conn.ClientID != "" {
		t.Fatalf("connection = %+v", f.conn)
	}
	user, token, err := f.db.ConnectionCredentials(f.h.ID, f.conn.ID)
	if err != nil || user != "mama@example.org" || token != "refresh-secret-1" {
		t.Fatalf("credentials = %q %q %v", user, token, err)
	}
	conns, _ := f.db.Connections(f.h.ID)
	if len(conns) != 1 || conns[0].Kind != "outlook" {
		t.Fatalf("Connections() = %+v", conns)
	}
	got, err := f.db.FindOutlookConnection(f.h.ID, "MAMA@Example.org")
	if err != nil || got.ID != f.conn.ID {
		t.Fatalf("find by account (any case) = %+v, %v", got, err)
	}
	if _, err := f.db.FindOutlookConnection(f.h.ID, "papa@example.org"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("find another account: err = %v", err)
	}
}

func TestOutlookCalendarsAreTwoWayUnlessTheSourceLocksThem(t *testing.T) {
	f := newOutlookFixture(t)
	if f.own.Kind != "outlook" || f.own.ReadOnly || f.own.SourceLocked || f.own.URL != "cal-1" {
		t.Fatalf("own = %+v", f.own)
	}
	shared, err := f.db.AddOutlookCalendar(f.h.ID, f.conn.ID, store.NewOutlookCalendar{Name: "Papa Arbeit", RemoteID: "cal-2", SourceLocked: true, SourceOwner: "Papa Maier (papa@example.org)", IntervalMinutes: 5, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	if !shared.ReadOnly || !shared.SourceLocked || shared.SourceOwner != "Papa Maier (papa@example.org)" {
		t.Fatalf("shared = %+v", shared)
	}
	if err := f.db.SetCalendarReadOnly(f.h.ID, shared.ID, false); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("making a shared calendar two-way: err = %v, want ErrInvalid", err)
	}
	if err := f.db.SetCalendarReadOnly(f.h.ID, f.own.ID, true); err != nil {
		t.Fatalf("an own Outlook calendar can be made read-only: %v", err)
	}
}

func TestTheRotatedRefreshTokenReplacesTheOldOne(t *testing.T) {
	f := newOutlookFixture(t)
	if err := f.db.SaveRefreshToken(f.conn.ID, "refresh-secret-2", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, token, _ := f.db.ConnectionCredentials(f.h.ID, f.conn.ID)
	c, _ := f.db.Connection(f.h.ID, f.conn.ID)
	if token != "refresh-secret-2" || !c.RenewedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("token %q renewed %v", token, c.RenewedAt)
	}
}

func TestAnExpiredSignInIsRememberedFromTheFirstTimeAndClearedByReconnecting(t *testing.T) {
	f := newOutlookFixture(t)
	if err := f.db.MarkReauth(f.conn.ID, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.db.MarkReauth(f.conn.ID, t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	need, err := f.db.ReauthConnections(f.h.ID)
	if err != nil || len(need) != 1 || !need[0].ReauthSince.Equal(t0.Add(time.Hour)) {
		t.Fatalf("needing sign-in = %+v, %v (want the first time kept)", need, err)
	}
	if err := f.db.FailSync(f.h.ID, f.own.ID, msg("sync.fail.outlook_signin"), t0, t0.Add(6*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Reconnected(f.h.ID, f.conn.ID, "refresh-secret-3", t0.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	need, _ = f.db.ReauthConnections(f.h.ID)
	if len(need) != 0 {
		t.Fatalf("still needing sign-in: %+v", need)
	}
	due, _ := f.db.DueCalendars(t0.Add(3 * time.Hour))
	if len(due) != 1 || due[0].Sync.Failures != 0 {
		t.Fatalf("after reconnecting the calendar is due at once with no failures: %+v", due)
	}
}

func TestAnInvitationIsStoredWithTheEventAndCannotBeChangedOnTheBox(t *testing.T) {
	f := newOutlookFixture(t)
	start := at(2026, 11, 19, 19, 30)
	inv := &calendar.Invitation{Organizer: "Huber, Annemarie", Response: "accepted", WebLink: "https://outlook.live.com/owa/?itemid=a1"}
	it := store.SyncItem{Key: "a1", Event: calendar.Event{Title: "Elternabend", Start: start, End: start.Add(90 * time.Minute), Invitation: inv}}
	if _, err := f.db.CompleteObjectSync(f.h.ID, f.own.ID, []store.ObjectChange{{URL: "a1", ETag: "ck-1", Items: []store.SyncItem{it}}}, nil, nil, t0, t0); err != nil {
		t.Fatal(err)
	}
	ev := eventTitled(t, f.db, f.h.ID, "Elternabend")
	if ev.Invitation == nil || *ev.Invitation != *inv {
		t.Fatalf("invitation = %+v", ev.Invitation)
	}
	patch := ev
	patch.Title = "Neu"
	err := f.db.ApplyChange(f.h.ID, calendar.Change{Update: []calendar.Event{patch}})
	if !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("changing an invitation: err = %v, want ErrInvalid", err)
	}
	if err := f.db.ApplyChange(f.h.ID, calendar.Change{Delete: []string{ev.ID}}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("deleting an invitation: err = %v, want ErrInvalid", err)
	}
	// Outlook turns it into a plain event (the attendees were removed): the box may change it again.
	plain := store.SyncItem{Key: "a1", Event: calendar.Event{Title: "Elternabend", Start: start, End: start.Add(90 * time.Minute)}}
	if _, err := f.db.CompleteObjectSync(f.h.ID, f.own.ID, []store.ObjectChange{{URL: "a1", ETag: "ck-2", Items: []store.SyncItem{plain}}}, nil, nil, t0, t0); err != nil {
		t.Fatal(err)
	}
	if ev = eventTitled(t, f.db, f.h.ID, "Elternabend"); ev.Invitation != nil {
		t.Fatalf("invitation stayed: %+v", ev.Invitation)
	}
}

func TestANewEventInAnOutlookCalendarIsQueuedAsANewObjectAndRenamedWhenCreated(t *testing.T) {
	f := newOutlookFixture(t)
	start := at(2026, 11, 20, 8, 30)
	created, err := f.db.CreateEvent(f.h.ID, calendar.Event{CalendarID: f.own.ID, Title: "Zahnarzt", Start: start, End: start.Add(30 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := f.db.PendingObjects(f.own.ID)
	if len(pending) != 1 || pending[0].ETag != "" {
		t.Fatalf("pending = %+v", pending)
	}
	addr := pending[0].URL
	if err := f.db.OutlookCreated(f.own.ID, addr, "graph-id-1", pending[0].Rev, "ck-1"); err != nil {
		t.Fatal(err)
	}
	pending, _ = f.db.PendingObjects(f.own.ID)
	if len(pending) != 0 {
		t.Fatalf("pending after create = %+v", pending)
	}
	objs, _ := f.db.RemoteObjects(f.own.ID, true)
	if objs["graph-id-1"] != "ck-1" || len(objs) != 1 {
		t.Fatalf("objects = %v", objs)
	}
	content, _ := f.db.ObjectEvents(f.h.ID, f.own.ID, "graph-id-1")
	if len(content.Events) != 1 || content.Events[0].ID != created.ID || content.UID != "graph-id-1" {
		t.Fatalf("content = %+v", content)
	}
}

func eventTitled(t *testing.T, db *store.DB, householdID, title string) calendar.Event {
	t.Helper()
	evs, err := db.EventsBetween(householdID, at(2026, 1, 1, 0, 0), at(2027, 12, 31, 0, 0))
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

func TestTheRefreshTokenIsNotReadableInTheDatabaseFileWithoutTheKey(t *testing.T) {
	db, path := openSealed(t)
	h, _ := seedParent(t, db)
	if _, err := db.CreateOutlookConnection(h.ID, store.NewOutlookConnection{Account: "mama@example.org", RefreshToken: "xxxx-refresh-secret-xxxx", Now: t0}); err != nil {
		t.Fatal(err)
	}
	db.SQL().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	db.Close()
	for _, name := range []string{path, path + "-wal"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		if bytes.Contains(raw, []byte("xxxx-refresh-secret-xxxx")) {
			t.Fatalf("%s holds the refresh token in plain text", filepath.Base(name))
		}
	}
}
