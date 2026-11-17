package calsync_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/store"
)

func TestStateIsStaleAfter24HoursWithoutSuccess(t *testing.T) {
	start := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		name         string
		now, success time.Time
		want         calsync.State
	}{
		{"fresh success", start.Add(time.Hour), start, calsync.OK},
		{"just under 24h", start.Add(24*time.Hour - time.Second), start, calsync.OK},
		{"24h", start.Add(24 * time.Hour), start, calsync.Stale},
		{"never succeeded, young", start.Add(2 * time.Hour), time.Time{}, calsync.OK},
		{"never succeeded, 24h", start.Add(24 * time.Hour), time.Time{}, calsync.Stale},
	}
	for _, c := range cases {
		if got := calsync.StateAt(c.now, start, c.success); got != c.want {
			t.Errorf("%s: state = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestBackoffGrowsExponentiallyAndIsCapped(t *testing.T) {
	hour := time.Hour
	var got []time.Duration
	for n := 1; n <= 9; n++ {
		got = append(got, calsync.Backoff(hour, n))
	}
	want := []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 40 * time.Minute, 80 * time.Minute, 160 * time.Minute, 320 * time.Minute, 6 * time.Hour, 6 * time.Hour}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("backoff = %v, want %v", got, want)
		}
	}
	if d := calsync.Backoff(time.Minute, 1); d != time.Minute {
		t.Errorf("short intervals are not exceeded on the first retry: %v", d)
	}
}

type fakeFetcher struct {
	mu    sync.Mutex
	feeds map[string][]byte
	errs  map[string]error
	calls int
}

func (f *fakeFetcher) Fetch(_ context.Context, url string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if err := f.errs[url]; err != nil {
		return nil, err
	}
	return f.feeds[url], nil
}

const feedURL = "https://example.org/school.ics"

func ics(events ...string) []byte {
	s := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\n"
	for _, e := range events {
		s += "BEGIN:VEVENT\r\n" + e + "\r\nEND:VEVENT\r\n"
	}
	return []byte(s + "END:VCALENDAR\r\n")
}

type fixture struct {
	db    *store.DB
	h     store.Household
	f     *fakeFetcher
	svc   *calsync.Service
	cal   store.SyncedCalendar
	clock time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "hejmo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	h, err := db.CreateHousehold(store.Household{Name: "Home", Region: "DE-BY", Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatal(err)
	}
	fx := &fixture{db: db, h: h, f: &fakeFetcher{feeds: map[string][]byte{}, errs: map[string]error{}}, clock: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	fx.svc = calsync.New(db, fx.f, func() time.Time { return fx.clock })
	fx.svc.SetGate(outbound.AllowAll()) // the tests use their own fetcher or dialer, or a local server
	fx.cal, err = db.CreateICSCalendar(h.ID, store.NewICS{Name: "School", URL: feedURL, IntervalMinutes: 60, Now: fx.clock})
	if err != nil {
		t.Fatal(err)
	}
	return fx
}

func (fx *fixture) titles(t *testing.T) []string {
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

func (fx *fixture) state(t *testing.T) store.SyncedCalendar {
	t.Helper()
	c, err := fx.db.SyncedCalendar(fx.h.ID, fx.cal.ID)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Acceptance: a feed event removed from the feed is gone after the next sync.
func TestRemovedFeedEventIsGoneAfterNextSync(t *testing.T) {
	fx := newFixture(t)
	fx.f.feeds[feedURL] = ics("UID:a\r\nSUMMARY:Choir\r\nDTSTART:20261120T170000", "UID:b\r\nSUMMARY:Fair\r\nDTSTART:20261121T100000")
	if err := fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	if got := fx.titles(t); len(got) != 2 {
		t.Fatalf("after first sync: %v", got)
	}
	fx.f.feeds[feedURL] = ics("UID:a\r\nSUMMARY:Choir\r\nDTSTART:20261120T170000")
	fx.clock = fx.clock.Add(time.Hour)
	if err := fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	if got := fx.titles(t); len(got) != 1 || got[0] != "Choir" {
		t.Fatalf("after second sync: %v", got)
	}
}

// Acceptance: an unreachable feed keeps the previous events and logs the failure.
func TestUnreachableFeedKeepsEventsAndLogs(t *testing.T) {
	fx := newFixture(t)
	fx.f.feeds[feedURL] = ics("UID:a\r\nSUMMARY:Choir\r\nDTSTART:20261120T170000")
	if err := fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	fx.f.errs[feedURL] = errors.New("dial tcp: connection refused")
	fx.clock = fx.clock.Add(time.Hour)
	if err := fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID); err == nil {
		t.Fatal("want the failure returned")
	}
	if got := fx.titles(t); len(got) != 1 {
		t.Fatalf("events after failure = %v", got)
	}
	log, _ := fx.db.SyncLog(fx.h.ID, 10)
	if len(log) != 1 || log[0].Kind != "failure" || log[0].Message.Key != "sync.fail.fetch" || log[0].Message.Params["detail"] != "dial tcp: connection refused" {
		t.Fatalf("log = %+v", log)
	}
	if s := fx.state(t); s.Sync.Failures != 1 || !s.Sync.NextAttempt.Equal(fx.clock.Add(5*time.Minute)) {
		t.Fatalf("state = %+v", s.Sync)
	}
}

func TestGarbageFeedCountsAsFailure(t *testing.T) {
	fx := newFixture(t)
	fx.f.feeds[feedURL] = []byte("<html>please sign in</html>")
	if err := fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID); err == nil {
		t.Fatal("want an error")
	}
	if log, _ := fx.db.SyncLog(fx.h.ID, 10); len(log) != 1 || log[0].Kind != "failure" || log[0].Message.Key != "sync.fail.not_ics" {
		t.Fatalf("log = %+v", log)
	}
}

func TestUnknownTimeZoneIsNotedOnceInTheLog(t *testing.T) {
	fx := newFixture(t)
	fx.f.feeds[feedURL] = ics("UID:a\r\nSUMMARY:Choir\r\nDTSTART;TZID=Mars/Olympus:20261120T170000")
	for i := 0; i < 2; i++ {
		if err := fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID); err != nil {
			t.Fatal(err)
		}
		fx.clock = fx.clock.Add(time.Hour)
	}
	log, _ := fx.db.SyncLog(fx.h.ID, 10)
	if len(log) != 1 || log[0].Kind != "note" || log[0].Message.Key != "sync.note.unknown_zone" || log[0].Message.Params["zone"] != "Mars/Olympus" {
		t.Fatalf("log = %+v", log)
	}
}

// Acceptance: 24 hours without success is stale; one success clears it.
func TestStaleAfter24HoursAndClearedByOneSuccess(t *testing.T) {
	fx := newFixture(t)
	fx.f.feeds[feedURL] = ics("UID:a\r\nSUMMARY:Choir\r\nDTSTART:20261120T170000")
	_ = fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID)
	fx.f.errs[feedURL] = errors.New("gone")
	for i := 0; i < 5; i++ {
		fx.clock = fx.clock.Add(5 * time.Hour)
		_ = fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID)
	}
	s := fx.state(t)
	if got := calsync.StateAt(fx.clock, s.Sync.StartedAt, s.Sync.LastSuccess); got != calsync.Stale {
		t.Fatalf("state after 25h = %s", got)
	}
	delete(fx.f.errs, feedURL)
	if err := fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	s = fx.state(t)
	if got := calsync.StateAt(fx.clock, s.Sync.StartedAt, s.Sync.LastSuccess); got != calsync.OK || s.Sync.Failures != 0 {
		t.Fatalf("after success: %s, %+v", got, s.Sync)
	}
}

func TestRunDueSyncsOnlyDueCalendarsOnTheirInterval(t *testing.T) {
	fx := newFixture(t)
	fx.f.feeds[feedURL] = ics("UID:a\r\nSUMMARY:Choir\r\nDTSTART:20261120T170000")
	ctx := context.Background()

	fx.svc.RunDue(ctx) // new calendars are due at once
	if fx.f.calls != 1 {
		t.Fatalf("calls = %d, want 1", fx.f.calls)
	}
	fx.clock = fx.clock.Add(59 * time.Minute)
	fx.svc.RunDue(ctx)
	if fx.f.calls != 1 {
		t.Fatalf("synced before its interval: calls = %d", fx.f.calls)
	}
	fx.clock = fx.clock.Add(time.Minute)
	fx.svc.RunDue(ctx)
	if fx.f.calls != 2 {
		t.Fatalf("calls = %d, want 2 after 60 minutes", fx.f.calls)
	}
}

func TestSyncUsesTheBoxTimeZone(t *testing.T) {
	fx := newFixture(t)
	fx.f.feeds[feedURL] = ics("UID:a\r\nSUMMARY:Floating\r\nDTSTART:20261120T170000")
	_ = fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID)
	evs, _ := fx.db.EventsBetween(fx.h.ID, fx.clock, fx.clock.AddDate(0, 1, 0))
	if len(evs) != 1 || evs[0].Start.Location().String() != "Europe/Berlin" || evs[0].Start.Hour() != 17 {
		t.Fatalf("events = %+v", evs)
	}
	var _ calendar.Event = evs[0]
}
