package calsync_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/store"
)

// A box with a real feed server and the gate of the real database: offline
// mode pauses calendars (ticket 201, Q143); it does not fail them.
type gateFixture struct {
	*fixture
	srv  *httptest.Server
	hits *atomic.Int32
}

func newGateFixture(t *testing.T) *gateFixture {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(ics("UID:a\r\nSUMMARY:Choir\r\nDTSTART:20261120T170000"))
	}))
	t.Cleanup(srv.Close)
	fx := newFixture(t)
	cal, err := fx.db.CreateICSCalendar(fx.h.ID, store.NewICS{Name: "Feed", URL: srv.URL + "/feed.ics", IntervalMinutes: 60, Now: fx.clock})
	if err != nil {
		t.Fatal(err)
	}
	fx.cal = cal
	g := outbound.New(fx.db.Outbound(), fx.db.Outbound())
	fx.svc = calsync.New(fx.db, nil, func() time.Time { return fx.clock })
	fx.svc.SetGate(g)
	return &gateFixture{fixture: fx, srv: srv, hits: &hits}
}

func (fx *gateFixture) refused(t *testing.T) []store.RefusedAttempt {
	t.Helper()
	r, err := fx.db.RefusedAttempts(50)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAFeedIsFetchedThroughTheGateAndTheContactIsRecorded(t *testing.T) {
	fx := newGateFixture(t)
	if err := fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	svcs, _ := fx.db.OutboundServices()
	u, _ := url.Parse(fx.srv.URL)
	if got := svcs[outbound.ICS]; got.LastContact.IsZero() || got.LastTarget != u.Host {
		t.Errorf("ICS last contact = %+v, want a contact with %s", got, u.Host)
	}
}

func TestOfflineModePausesAnICSFeedWithoutFailingIt(t *testing.T) {
	fx := newGateFixture(t)
	if err := fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	before := fx.hits.Load()
	if err := fx.db.SetOffline(true, fx.clock); err != nil {
		t.Fatal(err)
	}
	fx.clock = fx.clock.Add(2 * time.Hour)

	err := fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID)
	if !errors.Is(err, outbound.ErrRefused) {
		t.Fatalf("Sync = %v, want a refused connection", err)
	}
	if fx.hits.Load() != before {
		t.Error("the feed server was contacted in offline mode")
	}
	if got := fx.titles(t); len(got) != 1 || got[0] != "Choir" {
		t.Errorf("events = %v, the last synced events must stay", got)
	}
	if s := fx.state(t); s.Sync.Failures != 0 || s.Sync.LastError.Key != "" {
		t.Errorf("sync state = %+v: a paused calendar has not failed", s.Sync)
	}
	if log, _ := fx.db.SyncLog(fx.h.ID, 10); len(log) != 0 && log[0].Kind == "failure" {
		t.Errorf("a failure was logged: %+v", log)
	}
	r := fx.refused(t)
	u, _ := url.Parse(fx.srv.URL)
	if len(r) != 1 || r[0].Service != outbound.ICS || r[0].Target != u.Host {
		t.Errorf("refused attempts = %+v", r)
	}
}

func TestTheSchedulerSkipsPausedCalendarsAndTriesNothing(t *testing.T) {
	fx := newGateFixture(t)
	if err := fx.db.SetServiceEnabled(outbound.ICS, false); err != nil {
		t.Fatal(err)
	}
	fx.clock = fx.clock.Add(48 * time.Hour) // everything is due
	fx.svc.RunDue(context.Background())
	if fx.hits.Load() != 0 {
		t.Error("a switched-off feed was fetched")
	}
	if r := fx.refused(t); len(r) != 0 {
		t.Errorf("the scheduler must not try a paused calendar; refused = %+v", r)
	}
	if err := fx.db.SetServiceEnabled(outbound.ICS, true); err != nil {
		t.Fatal(err)
	}
	fx.svc.RunDue(context.Background())
	if fx.hits.Load() == 0 {
		t.Error("after switching on, the calendar syncs again at once")
	}
}

func TestCalDAVDiscoveryGoesThroughTheGate(t *testing.T) {
	fx := newGateFixture(t)
	if err := fx.db.SetServiceEnabled(outbound.Calendars, false); err != nil {
		t.Fatal(err)
	}
	_, err := fx.svc.Discover(context.Background(), fx.srv.URL+"/dav/", "mama", "pw", true)
	if !errors.Is(err, outbound.ErrRefused) {
		t.Fatalf("Discover = %v, want refused", err)
	}
	if fx.hits.Load() != 0 {
		t.Error("the CalDAV server was contacted although calendar services are off")
	}
	r := fx.refused(t)
	if len(r) == 0 || r[0].Service != outbound.Calendars {
		t.Errorf("refused attempts = %+v", r)
	}
}

func TestSyncMessageSaysOfflineForARefusedConnection(t *testing.T) {
	err := &outbound.RefusedError{Service: outbound.Calendars, Target: "x", Verdict: outbound.VerdictOffline}
	if k := calsync.MessageOf(err).Key; k != "sync.fail.offline" {
		t.Errorf("message = %q, want sync.fail.offline", k)
	}
	err.Verdict = outbound.VerdictSwitchedOff
	if k := calsync.MessageOf(err).Key; k != "sync.fail.switched_off" {
		t.Errorf("message = %q, want sync.fail.switched_off", k)
	}
}
