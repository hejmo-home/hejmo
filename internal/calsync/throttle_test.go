package calsync_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldavclient/caldavtest"
	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/store"
)

func TestRetryAfterReadsSecondsAndDatesAndBoundsAbsurdValues(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name, header string
		want         time.Duration
	}{
		{"seconds", "120", 2 * time.Minute},
		{"date", now.Add(time.Hour).UTC().Format(http.TimeFormat), time.Hour},
		{"missing", "", 0},
		{"not a value", "soon", 0},
		{"negative", "-5", 0},
		{"a date in the past", now.Add(-time.Hour).UTC().Format(http.TimeFormat), 0},
		{"absurd seconds", "99999999999", calsync.MaxRetryAfter},
		{"absurd date", now.AddDate(5, 0, 0).UTC().Format(http.TimeFormat), calsync.MaxRetryAfter},
	} {
		if got := calsync.RetryAfter(c.header, now); got != c.want {
			t.Errorf("%s: RetryAfter(%q) = %v, want %v", c.name, c.header, got, c.want)
		}
	}
}

// threeQueued holds three queued changes of the calendar.
func (fx *davFixture) threeQueued(t *testing.T) {
	t.Helper()
	for _, n := range []string{"a", "b", "c"} {
		fx.srv.Put(t, fx.remote, n+".ics", caldavtest.Event(n+"@test", "Event "+n, "20261120T150000", "20261120T160000"))
	}
	fx.syncAfter(t, 0)
	for _, n := range []string{"a", "b", "c"} {
		e := fx.boxEvent(t, "Event "+n)
		e.Title = "Changed " + n
		if err := fx.db.ApplyChange(fx.h.ID, calendar.Change{Update: []calendar.Event{e}}); err != nil {
			t.Fatal(err)
		}
	}
}

func failures(t *testing.T, db *store.DB, householdID string) []store.SyncLogEntry {
	t.Helper()
	log, err := db.SyncLog(householdID, 50)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.SyncLogEntry
	for _, l := range log {
		if l.Kind == "failure" {
			out = append(out, l)
		}
	}
	return out
}

func TestCalDAVThrottleStopsThePushAndKeepsTheRestQueued(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			fx := newDAVFixture(t, caldavtest.Password)
			fx.threeQueued(t)
			fx.tamper.RefuseWritesWait(status, "1800")
			before := fx.tamper.Count("PUT")

			if err := fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID); err == nil {
				t.Fatal("push to a throttling server succeeded")
			}
			if n := fx.tamper.Count("PUT") - before; n != 1 {
				t.Fatalf("%d writes reached the throttling server, want 1", n)
			}
			if pend, _ := fx.db.PendingObjects(fx.cal.ID); len(pend) != 3 {
				t.Fatalf("pending = %d, want all 3 kept", len(pend))
			}
			log := failures(t, fx.db, fx.h.ID)
			if len(log) != 1 || log[0].Message.Key != "sync.fail.throttled" {
				t.Fatalf("log = %+v, want one sync.fail.throttled", log)
			}
			cal, _ := fx.db.SyncedCalendar(fx.h.ID, fx.cal.ID)
			if want := fx.clock.Add(30 * time.Minute); cal.Sync.NextAttempt.Before(want) {
				t.Fatalf("next attempt %v, want at least %v (Retry-After)", cal.Sync.NextAttempt, want)
			}
		})
	}
}

func TestCalDAVThrottledPushSkipsTheImportAndTheNextSyncWaits(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	fx.threeQueued(t)
	other := fx.srv.MakeCalendar(t, "other", "Other")
	conn2, err := fx.db.CreateCalDAVConnection(fx.h.ID, store.NewCalDAVConnection{URL: fx.srv.URL, Username: caldavtest.User, Password: caldavtest.Password, Now: fx.clock})
	if err != nil {
		t.Fatal(err)
	}
	cal2, err := fx.db.AddCalDAVCalendar(fx.h.ID, conn2.ID, store.NewCalDAVCalendar{Name: "Other", RemoteURL: other, IntervalMinutes: 5, Now: fx.clock})
	if err != nil {
		t.Fatal(err)
	}
	fx.tamper.RefuseWritesWait(429, "1800")
	reads := func() int { return fx.tamper.Count("REPORT") + fx.tamper.Count("PROPFIND") }
	before := reads()

	if err := fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID); err == nil {
		t.Fatal("sync with a throttled push succeeded")
	}
	if n := reads() - before; n != 0 {
		t.Fatalf("%d reads went to the server after it throttled", n)
	}

	// Another connection keeps syncing while this one waits.
	fx.srv.Put(t, other, "swim.ics", caldavtest.Event("swim@test", "Swimming", "20261121T150000", "20261121T160000"))
	fx.clock = fx.clock.Add(10 * time.Minute)
	fx.svc.RunDue(context.Background())
	evs, _ := fx.db.CalendarEvents(fx.h.ID, cal2.ID)
	if len(evs) != 1 {
		t.Fatalf("the other calendar has %d events, want 1", len(evs))
	}
	if n := len(failures(t, fx.db, fx.h.ID)); n != 1 {
		t.Fatalf("%d failures logged, want only the throttle", n)
	}
	if pend, _ := fx.db.PendingObjects(fx.cal.ID); len(pend) != 3 {
		t.Fatalf("pending = %d before the wait is over, want 3", len(pend))
	}

	// After the wait the queue goes out.
	fx.tamper.RefuseWritesWait(0, "")
	fx.clock = fx.clock.Add(21 * time.Minute)
	fx.svc.RunDue(context.Background())
	if pend, _ := fx.db.PendingObjects(fx.cal.ID); len(pend) != 0 {
		t.Fatalf("pending = %+v after the wait", pend)
	}
	if got := fx.srv.Get(t, fx.remote+"b.ics"); !strings.Contains(got, "SUMMARY:Changed b") {
		t.Fatalf("change not pushed:\n%s", got)
	}
}

func (fx *outlookFx) threeQueued(t *testing.T) {
	t.Helper()
	start := time.Date(2026, 11, 24, 8, 30, 0, 0, berlinZone)
	for _, n := range []string{"A", "B", "C"} {
		if _, err := fx.db.CreateEvent(fx.h.ID, calendar.Event{CalendarID: fx.cal.ID, Title: "Event " + n, Start: start, End: start.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
}

func (fx *outlookFx) count(prefix string) int {
	n := 0
	for _, r := range fx.fake.Requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func TestOutlookThrottleStopsThePushAndSkipsTheImport(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			fx := newOutlookFx(t)
			fx.threeQueued(t)
			fx.fake.FailWrites, fx.fake.RetryAfter = status, "1800"

			if err := fx.sync(t); err == nil {
				t.Fatal("sync with a throttled push succeeded")
			}
			if n := fx.count("POST /v1.0"); n != 1 {
				t.Fatalf("%d writes reached Graph, want 1", n)
			}
			if n := fx.count("GET "); n != 0 {
				t.Fatalf("%d reads went to Graph after it throttled", n)
			}
			if pend, _ := fx.db.PendingObjects(fx.cal.ID); len(pend) != 3 {
				t.Fatalf("pending = %d, want all 3 kept", len(pend))
			}
			log := failures(t, fx.db, fx.h.ID)
			if len(log) != 1 || log[0].Message.Key != "sync.fail.throttled" {
				t.Fatalf("log = %+v, want one sync.fail.throttled", log)
			}
			cal, _ := fx.db.SyncedCalendar(fx.h.ID, fx.cal.ID)
			if want := fx.clock.Add(30 * time.Minute); cal.Sync.NextAttempt.Before(want) {
				t.Fatalf("next attempt %v, want at least %v (Retry-After)", cal.Sync.NextAttempt, want)
			}
		})
	}
}

func TestOutlookPushAloneStopsAtTheThrottle(t *testing.T) {
	fx := newOutlookFx(t)
	fx.threeQueued(t)
	fx.fake.FailWrites = 429
	if err := fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID); err == nil {
		t.Fatal("push to a throttling server succeeded")
	}
	if n := fx.count("POST /v1.0"); n != 1 {
		t.Fatalf("%d writes reached Graph, want 1", n)
	}
	cal, _ := fx.db.SyncedCalendar(fx.h.ID, fx.cal.ID)
	if cal.Sync.NextAttempt.Before(fx.clock.Add(5 * time.Minute)) {
		t.Fatalf("without Retry-After the next attempt is %v, want the default wait", cal.Sync.NextAttempt)
	}
}

func TestCalDAVThrottleDelaysEveryCalendarOfTheConnection(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	fx.threeQueued(t)
	sibling := fx.srv.MakeCalendar(t, "sibling", "Sibling")
	cal2, err := fx.db.AddCalDAVCalendar(fx.h.ID, fx.cal.ConnectionID, store.NewCalDAVCalendar{Name: "Sibling", RemoteURL: sibling, IntervalMinutes: 5, Now: fx.clock})
	if err != nil {
		t.Fatal(err)
	}
	fx.tamper.RefuseWritesWait(429, "1800")
	if err := fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID); err == nil {
		t.Fatal("push to a throttling server succeeded")
	}
	got, _ := fx.db.SyncedCalendar(fx.h.ID, cal2.ID)
	if want := fx.clock.Add(30 * time.Minute); got.Sync.NextAttempt.Before(want) {
		t.Fatalf("sibling next attempt %v, want at least %v (Retry-After of the connection)", got.Sync.NextAttempt, want)
	}

	reads := func() int { return fx.tamper.Count("REPORT") + fx.tamper.Count("PROPFIND") }
	before := reads()
	fx.clock = fx.clock.Add(10 * time.Minute)
	fx.svc.RunDue(context.Background())
	if n := reads() - before; n != 0 {
		t.Fatalf("%d requests went to the throttling server while it asked to wait", n)
	}
}

func TestCalDAVPushKeepsChangesQueuedWhileThrottled(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	fx.threeQueued(t)
	fx.tamper.RefuseWritesWait(429, "1800")
	if err := fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID); err == nil {
		t.Fatal("push to a throttling server succeeded")
	}
	before := fx.tamper.Count("PUT")

	fx.clock = fx.clock.Add(time.Minute)
	if err := fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID); err != nil {
		t.Fatalf("push during the wait: %v", err)
	}
	if n := fx.tamper.Count("PUT") - before; n != 0 {
		t.Fatalf("%d writes reached the server during its wait", n)
	}
	if pend, _ := fx.db.PendingObjects(fx.cal.ID); len(pend) != 3 {
		t.Fatalf("pending = %d, want all 3 kept", len(pend))
	}
	if n := len(failures(t, fx.db, fx.h.ID)); n != 1 {
		t.Fatalf("%d failures logged, want 1", n)
	}

	fx.tamper.RefuseWritesWait(0, "")
	fx.clock = fx.clock.Add(30 * time.Minute)
	if err := fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID); err != nil {
		t.Fatalf("push after the wait: %v", err)
	}
	if pend, _ := fx.db.PendingObjects(fx.cal.ID); len(pend) != 0 {
		t.Fatalf("pending = %d after the wait", len(pend))
	}
}

func TestOutlookPushKeepsChangesQueuedWhileThrottled(t *testing.T) {
	fx := newOutlookFx(t)
	fx.threeQueued(t)
	fx.fake.FailWrites, fx.fake.RetryAfter = 429, "1800"
	if err := fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID); err == nil {
		t.Fatal("push to a throttling server succeeded")
	}
	fx.clock = fx.clock.Add(time.Minute)
	if err := fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID); err != nil {
		t.Fatalf("push during the wait: %v", err)
	}
	if n := fx.count("POST /v1.0"); n != 1 {
		t.Fatalf("%d writes reached Graph, want 1 (none during the wait)", n)
	}
	if n := len(failures(t, fx.db, fx.h.ID)); n != 1 {
		t.Fatalf("%d failures logged, want 1", n)
	}
	if pend, _ := fx.db.PendingObjects(fx.cal.ID); len(pend) != 3 {
		t.Fatalf("pending = %d, want all 3 kept", len(pend))
	}
}

// queueOn queues one change on cal, as an edit in the box does.
func queueOn(t *testing.T, db *store.DB, householdID, calendarID string) {
	t.Helper()
	start := time.Date(2026, 11, 24, 9, 0, 0, 0, berlinZone)
	if _, err := db.CreateEvent(householdID, calendar.Event{CalendarID: calendarID, Title: "Sibling event", Start: start, End: start.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
}

func TestCalDAVSiblingPushAndSyncSendNothingWhileTheConnectionWaits(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	fx.threeQueued(t)
	sibling := fx.srv.MakeCalendar(t, "sibling", "Sibling")
	cal2, err := fx.db.AddCalDAVCalendar(fx.h.ID, fx.cal.ConnectionID, store.NewCalDAVCalendar{Name: "Sibling", RemoteURL: sibling, IntervalMinutes: 5, Now: fx.clock})
	if err != nil {
		t.Fatal(err)
	}
	queueOn(t, fx.db, fx.h.ID, cal2.ID)
	fx.tamper.RefuseWritesWait(429, "1800")
	if err := fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID); err == nil {
		t.Fatal("push to a throttling server succeeded")
	}
	requests := func() int {
		return fx.tamper.Count("PUT") + fx.tamper.Count("DELETE") + fx.tamper.Count("REPORT") + fx.tamper.Count("PROPFIND")
	}
	before := requests()

	fx.clock = fx.clock.Add(time.Minute)
	if err := fx.svc.Push(context.Background(), fx.h.ID, cal2.ID); err != nil {
		t.Fatalf("push of the sibling during the wait: %v", err)
	}
	// A sync the user starts waits as well, on either calendar.
	for _, id := range []string{cal2.ID, fx.cal.ID} {
		if err := fx.svc.Sync(context.Background(), fx.h.ID, id); err == nil {
			t.Fatal("manual sync during the wait reported success")
		}
	}
	if n := requests() - before; n != 0 {
		t.Fatalf("%d requests reached the server during its wait", n)
	}
	if n := len(failures(t, fx.db, fx.h.ID)); n != 1 {
		t.Fatalf("%d failures logged, want 1", n)
	}
	if pend, _ := fx.db.PendingObjects(cal2.ID); len(pend) != 1 {
		t.Fatalf("sibling pending = %d, want 1 kept", len(pend))
	}

	fx.tamper.RefuseWritesWait(0, "")
	fx.clock = fx.clock.Add(30 * time.Minute)
	if err := fx.svc.Push(context.Background(), fx.h.ID, cal2.ID); err != nil {
		t.Fatalf("push of the sibling after the wait: %v", err)
	}
	if pend, _ := fx.db.PendingObjects(cal2.ID); len(pend) != 0 {
		t.Fatalf("sibling pending = %d after the wait", len(pend))
	}
}

func TestOutlookSiblingPushAndSyncSendNothingWhileTheConnectionWaits(t *testing.T) {
	fx := newOutlookFx(t)
	fx.fake.AddCalendar("cal-2", "Zweiter")
	cal2, err := fx.db.AddOutlookCalendar(fx.h.ID, fx.conn.ID, store.NewOutlookCalendar{Name: "Zweiter", RemoteID: "cal-2", IntervalMinutes: 5, Now: fx.clock})
	if err != nil {
		t.Fatal(err)
	}
	fx.threeQueued(t)
	queueOn(t, fx.db, fx.h.ID, cal2.ID)
	fx.fake.FailWrites, fx.fake.RetryAfter = 429, "1800"
	if err := fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID); err == nil {
		t.Fatal("push to a throttling server succeeded")
	}
	before := len(fx.fake.Requests)

	fx.clock = fx.clock.Add(time.Minute)
	if err := fx.svc.Push(context.Background(), fx.h.ID, cal2.ID); err != nil {
		t.Fatalf("push of the sibling during the wait: %v", err)
	}
	for _, id := range []string{cal2.ID, fx.cal.ID} {
		if err := fx.svc.Sync(context.Background(), fx.h.ID, id); err == nil {
			t.Fatal("manual sync during the wait reported success")
		}
	}
	if n := len(fx.fake.Requests) - before; n != 0 {
		t.Fatalf("%d requests reached Graph during its wait: %v", n, fx.fake.Requests[before:])
	}
	if n := len(failures(t, fx.db, fx.h.ID)); n != 1 {
		t.Fatalf("%d failures logged, want 1", n)
	}
	if pend, _ := fx.db.PendingObjects(cal2.ID); len(pend) != 1 {
		t.Fatalf("sibling pending = %d, want 1 kept", len(pend))
	}
}

// A calendar that falls due during the wait (added to the connection meanwhile)
// is moved to the end of the wait, so no scheduler tick picks it up before.
func TestCalDAVCalendarDueDuringTheWaitIsNotPickedUpOnEveryTick(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	fx.threeQueued(t)
	fx.tamper.RefuseWritesWait(429, "1800")
	if err := fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID); err == nil {
		t.Fatal("push to a throttling server succeeded")
	}
	fx.clock = fx.clock.Add(time.Minute)
	late := fx.srv.MakeCalendar(t, "late", "Late")
	added, err := fx.db.AddCalDAVCalendar(fx.h.ID, fx.cal.ConnectionID, store.NewCalDAVCalendar{Name: "Late", RemoteURL: late, IntervalMinutes: 5, Now: fx.clock})
	if err != nil {
		t.Fatal(err)
	}
	logged := len(failures(t, fx.db, fx.h.ID))
	for tick := 1; tick <= 3; tick++ {
		due, err := fx.db.DueCalendars(fx.clock)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range due {
			if c.ID == added.ID {
				_ = fx.svc.Sync(context.Background(), fx.h.ID, c.ID)
			}
		}
		fx.clock = fx.clock.Add(time.Minute)
	}
	got, err := fx.db.SyncedCalendar(fx.h.ID, added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sync.NextAttempt.Before(got.ThrottledUntil) {
		t.Fatalf("calendar due during the wait: next attempt %v, wait until %v", got.Sync.NextAttempt, got.ThrottledUntil)
	}
	if n := len(failures(t, fx.db, fx.h.ID)) - logged; n != 0 {
		t.Fatalf("%d sync log entries for the waiting calendar, want 0", n)
	}
	due, err := fx.db.DueCalendars(fx.clock)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range due {
		if c.ID == added.ID {
			t.Fatalf("calendar still due at %v, wait until %v", fx.clock, got.ThrottledUntil)
		}
	}
}

func TestCalDAVThrottledStateAbortsTheImportAndTheNextSyncWaits(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			fx := newDAVFixture(t, caldavtest.Password)
			fx.srv.Put(t, fx.remote, "a.ics", caldavtest.Event("a@test", "Event a", "20261120T150000", "20261120T160000"))
			fx.tamper.RefuseState(status, "1800")
			reports := fx.tamper.Count("REPORT")

			if err := fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID); err == nil {
				t.Fatal("sync with a throttled State succeeded")
			}
			if n := fx.tamper.Count("REPORT") - reports; n != 0 {
				t.Fatalf("%d REPORTs went to the server after State was throttled, want 0", n)
			}
			log := failures(t, fx.db, fx.h.ID)
			if len(log) != 1 || log[0].Message.Key != "sync.fail.throttled" {
				t.Fatalf("log = %+v, want one sync.fail.throttled", log)
			}
			cal, _ := fx.db.SyncedCalendar(fx.h.ID, fx.cal.ID)
			if want := fx.clock.Add(30 * time.Minute); cal.Sync.NextAttempt.Before(want) || cal.ThrottledUntil.Before(want) {
				t.Fatalf("next attempt %v, wait until %v, want at least %v (Retry-After)", cal.Sync.NextAttempt, cal.ThrottledUntil, want)
			}
			if evs, _ := fx.db.CalendarEvents(fx.h.ID, fx.cal.ID); len(evs) != 0 {
				t.Fatalf("%d events imported, want none", len(evs))
			}

			// The next sync waits: no request goes out while the connection is throttled.
			before := fx.tamper.Count("PROPFIND") + fx.tamper.Count("REPORT")
			if err := fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID); err == nil {
				t.Fatal("second sync during the wait succeeded")
			}
			if n := fx.tamper.Count("PROPFIND") + fx.tamper.Count("REPORT") - before; n != 0 {
				t.Fatalf("%d requests went out during the wait, want 0", n)
			}
		})
	}
}
