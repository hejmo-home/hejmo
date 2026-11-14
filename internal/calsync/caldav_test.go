package calsync_test

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldav"
	"github.com/hejmo-home/hejmo/internal/caldav/caldavtest"
	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/secret"
	"github.com/hejmo-home/hejmo/internal/store"
)

type davFixture struct {
	db      *store.DB
	h       store.Household
	srv     *caldavtest.Server
	remote  string // calendar URL on Radicale
	svc     *calsync.Service
	cal     store.SyncedCalendar
	clock   time.Time
	fetched atomic.Int32 // objects downloaded
	garble  atomic.Bool  // downloaded objects arrive unreadable
}

// countingRemote counts the objects the box downloads.
type countingRemote struct {
	*caldav.Client
	n      *atomic.Int32
	garble *atomic.Bool
}

func (c countingRemote) Fetch(ctx context.Context, cal string, urls []string) ([]caldav.Object, error) {
	c.n.Add(int32(len(urls)))
	objs, err := c.Client.Fetch(ctx, cal, urls)
	if c.garble.Load() {
		for i := range objs {
			objs[i].Data = []byte("this is not a calendar")
		}
	}
	return objs, err
}

func newDAVFixture(t *testing.T, password string) *davFixture {
	t.Helper()
	srv := caldavtest.Start(t)
	db, err := store.Open(filepath.Join(t.TempDir(), "hejmo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	box, err := secret.LoadOrCreate(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetSecrets(box)
	h, err := db.CreateHousehold(store.Household{Name: "Home", Region: "DE-BY", Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatal(err)
	}
	fx := &davFixture{db: db, h: h, srv: srv, clock: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	fx.remote = srv.MakeCalendar(t, "family", "Family")
	conn, err := db.CreateCalDAVConnection(h.ID, store.NewCalDAVConnection{URL: srv.URL, Username: caldavtest.User, Password: password, Now: fx.clock})
	if err != nil {
		t.Fatal(err)
	}
	fx.cal, err = db.AddCalDAVCalendar(h.ID, conn.ID, store.NewCalDAVCalendar{Name: "Family", RemoteURL: fx.remote, IntervalMinutes: 5, Now: fx.clock})
	if err != nil {
		t.Fatal(err)
	}
	fx.svc = calsync.New(db, nil, func() time.Time { return fx.clock })
	fx.svc.SetDialer(func(serverURL, user, pass string) (calsync.Remote, error) {
		c, err := caldav.NewClient(serverURL, user, pass, nil)
		return countingRemote{c, &fx.fetched, &fx.garble}, err
	})
	return fx
}

func (fx *davFixture) titles(t *testing.T) []string {
	t.Helper()
	evs, err := fx.db.EventsBetween(fx.h.ID, fx.clock.AddDate(-1, 0, 0), fx.clock.AddDate(1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		out = append(out, e.Title)
	}
	return out
}

func TestEventCreatedExternallyAppearsAfterTheNextSyncInterval(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	fx.svc.RunDue(context.Background()) // first sync, calendar is empty
	if got := fx.titles(t); len(got) != 0 {
		t.Fatalf("events = %v", got)
	}

	fx.srv.Put(t, fx.remote, "swim.ics", caldavtest.Event("swim@test", "Swimming", "20261120T150000", "20261120T160000"))

	fx.clock = fx.clock.Add(4 * time.Minute)
	fx.svc.RunDue(context.Background())
	if got := fx.titles(t); len(got) != 0 {
		t.Fatalf("event appeared before the sync interval: %v", got)
	}

	fx.clock = fx.clock.Add(time.Minute)
	fx.svc.RunDue(context.Background())
	evs, _ := fx.db.EventsBetween(fx.h.ID, fx.clock, fx.clock.AddDate(0, 1, 0))
	if len(evs) != 1 || evs[0].Title != "Swimming" || evs[0].Start.Format("2006-01-02 15:04 MST") != "2026-11-20 15:00 CET" {
		t.Fatalf("events = %+v", evs)
	}
}

func TestDeltaSyncDownloadsOnlyChangedObjects(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	fx.srv.Put(t, fx.remote, "a.ics", caldavtest.Event("a@test", "Swimming", "20261120T150000", "20261120T160000"))
	fx.srv.Put(t, fx.remote, "b.ics", caldavtest.Event("b@test", "Piano", "20261121T150000", "20261121T160000"))
	ctx := context.Background()
	if err := fx.svc.Sync(ctx, fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	if n := fx.fetched.Load(); n != 2 {
		t.Fatalf("first sync downloaded %d objects, want 2", n)
	}

	fx.clock = fx.clock.Add(5 * time.Minute)
	if err := fx.svc.Sync(ctx, fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	if n := fx.fetched.Load(); n != 2 {
		t.Fatalf("an unchanged calendar downloaded %d more objects", n-2)
	}

	fx.srv.Put(t, fx.remote, "a.ics", caldavtest.Event("a@test", "Swimming lesson", "20261120T150000", "20261120T160000"))
	fx.clock = fx.clock.Add(5 * time.Minute)
	if err := fx.svc.Sync(ctx, fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	if n := fx.fetched.Load(); n != 3 {
		t.Fatalf("a changed object downloaded %d objects in total, want 3", n)
	}
	got := fx.titles(t)
	if len(got) != 2 || got[0] != "Swimming lesson" || got[1] != "Piano" {
		t.Fatalf("events = %v", got)
	}
}

func TestExternalDeletionRemovesTheEventFromTheBox(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	gone := fx.srv.Put(t, fx.remote, "swim.ics", caldavtest.Event("swim@test", "Swimming", "20261120T150000", "20261120T160000"))
	fx.srv.Put(t, fx.remote, "piano.ics", caldavtest.Event("piano@test", "Piano", "20261121T150000", "20261121T160000"))
	ctx := context.Background()
	if err := fx.svc.Sync(ctx, fx.h.ID, fx.cal.ID); err != nil || len(fx.titles(t)) != 2 {
		t.Fatalf("first sync: %v, %v", err, fx.titles(t))
	}

	fx.srv.Delete(t, gone)
	fx.clock = fx.clock.Add(5 * time.Minute)
	if err := fx.svc.Sync(ctx, fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	if got := fx.titles(t); len(got) != 1 || got[0] != "Piano" {
		t.Fatalf("events = %v, want only Piano", got)
	}
}

func TestRefusedPasswordKeepsTheEventsAndLogsAReadableFailure(t *testing.T) {
	fx := newDAVFixture(t, "wrong-password")
	if err := fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID); err == nil {
		t.Fatal("sync with a wrong password succeeded")
	}
	log, _ := fx.db.SyncLog(fx.h.ID, 10)
	if len(log) != 1 || log[0].Kind != "failure" || log[0].Message.Key != "sync.fail.login" {
		t.Fatalf("log = %+v", log)
	}
}

func TestUnreadableObjectKeepsItsLastGoodEventsAndIsNoted(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	fx.srv.Put(t, fx.remote, "swim.ics", caldavtest.Event("swim@test", "Swimming", "20261120T150000", "20261120T160000"))
	ctx := context.Background()
	if err := fx.svc.Sync(ctx, fx.h.ID, fx.cal.ID); err != nil || len(fx.titles(t)) != 1 {
		t.Fatalf("first sync: %v, %v", err, fx.titles(t))
	}

	fx.srv.Put(t, fx.remote, "swim.ics", caldavtest.Event("swim@test", "Swimming lesson", "20261120T150000", "20261120T160000"))
	fx.garble.Store(true)
	fx.clock = fx.clock.Add(5 * time.Minute)
	if err := fx.svc.Sync(ctx, fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	if got := fx.titles(t); len(got) != 1 || got[0] != "Swimming" {
		t.Fatalf("events = %v, want the last good Swimming", got)
	}
	log, _ := fx.db.SyncLog(fx.h.ID, 10)
	var noted bool
	for _, e := range log {
		noted = noted || e.Message.Key == "sync.note.bad_object"
	}
	if !noted {
		t.Fatalf("no sync.note.bad_object in %+v", log)
	}

	// Once the object reads again, the new version replaces the old one.
	fx.garble.Store(false)
	fx.clock = fx.clock.Add(5 * time.Minute)
	if err := fx.svc.Sync(ctx, fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	if got := fx.titles(t); len(got) != 1 || got[0] != "Swimming lesson" {
		t.Fatalf("events = %v, want Swimming lesson", got)
	}
}
