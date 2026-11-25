package store_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

var t0 = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

func newICS(t *testing.T, f calFixture) store.SyncedCalendar {
	t.Helper()
	c, err := f.db.CreateICSCalendar(f.h.ID, store.NewICS{
		Name: "School", URL: "https://example.org/school.ics", Color: "#2F5E7A",
		Audience: calendar.Audience{People: []string{f.mia.ID}}, IntervalMinutes: 60, Now: t0,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func item(key, seriesKey, title string, start time.Time) store.SyncItem {
	return store.SyncItem{Key: key, SeriesKey: seriesKey, Event: calendar.Event{Title: title, Start: start, End: start.Add(time.Hour)}}
}

func titles(t *testing.T, f calFixture, calID string) []string {
	t.Helper()
	evs, err := f.db.EventsBetween(f.h.ID, at(2026, 1, 1, 0, 0), at(2027, 12, 31, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if e.CalendarID == calID {
			out = append(out, e.Title)
		}
	}
	return out
}

func TestICSCalendarIsReadOnlyWithAudienceAndDueNow(t *testing.T) {
	f := newCalFixture(t)
	c := newICS(t, f)
	if c.Kind != "ics" || !c.ReadOnly || c.URL != "https://example.org/school.ics" || len(c.Audience.People) != 1 || c.Sync.IntervalMinutes != 60 {
		t.Fatalf("calendar = %+v", c)
	}
	cals, _ := f.db.Calendars(f.h.ID)
	var found bool
	for _, x := range cals {
		if x.ID == c.ID {
			found = x.ReadOnly && x.Kind == "ics" && x.Color == "#2F5E7A"
		}
	}
	if !found {
		t.Fatalf("Calendars() = %+v", cals)
	}
	due, err := f.db.DueCalendars(t0)
	if err != nil || len(due) != 1 || due[0].ID != c.ID {
		t.Fatalf("due = %+v, %v", due, err)
	}
}

func TestCompleteSyncReplacesEventsAndKeepsIDs(t *testing.T) {
	f := newCalFixture(t)
	c := newICS(t, f)
	next := t0.Add(time.Hour)

	if _, err := f.db.CompleteSync(f.h.ID, c.ID, []store.SyncItem{
		item("a", "", "Choir", at(2026, 11, 20, 17, 0)),
		item("b", "", "Fair", at(2026, 11, 21, 10, 0)),
	}, nil, t0, next); err != nil {
		t.Fatal(err)
	}
	first, _ := f.db.EventsBetween(f.h.ID, at(2026, 11, 1, 0, 0), at(2026, 12, 1, 0, 0))
	ids := map[string]string{}
	for _, e := range first {
		ids[e.Title] = e.ID
	}

	// Second sync: Fair is gone from the feed, Choir was renamed.
	if _, err := f.db.CompleteSync(f.h.ID, c.ID, []store.SyncItem{item("a", "", "Choir rehearsal", at(2026, 11, 20, 17, 0))}, nil, t0.Add(time.Hour), next.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got := titles(t, f, c.ID)
	if len(got) != 1 || got[0] != "Choir rehearsal" {
		t.Fatalf("events after resync = %v", got)
	}
	after, _ := f.db.EventsBetween(f.h.ID, at(2026, 11, 1, 0, 0), at(2026, 12, 1, 0, 0))
	if after[0].ID != ids["Choir"] {
		t.Fatalf("event id changed across syncs: %s -> %s", ids["Choir"], after[0].ID)
	}
}

func TestChangedOccurrencesLinkToTheirSeries(t *testing.T) {
	f := newCalFixture(t)
	c := newICS(t, f)
	master := item("a", "", "Choir", at(2026, 11, 2, 17, 0))
	master.Event.RRule = "FREQ=WEEKLY;COUNT=3"
	moved := item("a#x", "a", "Choir (moved)", at(2026, 11, 9, 19, 0))
	moved.Event.RecurrenceID = at(2026, 11, 9, 17, 0)
	// Overrides may come before their master in the feed.
	if _, err := f.db.CompleteSync(f.h.ID, c.ID, []store.SyncItem{moved, master}, nil, t0, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	evs, _ := f.db.EventsBetween(f.h.ID, at(2026, 11, 1, 0, 0), at(2026, 12, 1, 0, 0))
	occs, err := calendar.Expand(evs, at(2026, 11, 1, 0, 0), at(2026, 12, 1, 0, 0))
	if err != nil || len(occs) != 3 || occs[1].Title != "Choir (moved)" {
		t.Fatalf("occurrences = %+v, %v", occs, err)
	}
}

func TestFailedSyncKeepsEventsAndLogsIt(t *testing.T) {
	f := newCalFixture(t)
	c := newICS(t, f)
	_, _ = f.db.CompleteSync(f.h.ID, c.ID, []store.SyncItem{item("a", "", "Choir", at(2026, 11, 20, 17, 0))}, []i18n.Message{i18n.Msg("sync.note.unknown_zone", "zone", "Mars/Olympus")}, t0, t0.Add(time.Hour))

	if err := f.db.FailSync(f.h.ID, c.ID, i18n.Msg("sync.fail.fetch", "detail", "connection refused"), t0.Add(time.Hour), t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := titles(t, f, c.ID); len(got) != 1 {
		t.Fatalf("events = %v, want the previous ones kept", got)
	}
	s, err := f.db.SyncedCalendar(f.h.ID, c.ID)
	if err != nil || s.Sync.Failures != 1 || s.Sync.LastError.Key != "sync.fail.fetch" || s.Sync.LastError.Params["detail"] != "connection refused" || !s.Sync.LastSuccess.Equal(t0) || !s.Sync.NextAttempt.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("sync state = %+v, %v", s.Sync, err)
	}
	log, err := f.db.SyncLog(f.h.ID, 50)
	if err != nil || len(log) != 2 {
		t.Fatalf("log = %+v, %v", log, err)
	}
	if log[0].Kind != "failure" || log[0].Message.Key != "sync.fail.fetch" || log[0].Message.Params["detail"] != "connection refused" || log[0].CalendarName != "School" || log[1].Kind != "note" {
		t.Fatalf("log (newest first) = %+v", log)
	}

	// One success clears the failure state.
	_, _ = f.db.CompleteSync(f.h.ID, c.ID, nil, nil, t0.Add(2*time.Hour), t0.Add(3*time.Hour))
	s, _ = f.db.SyncedCalendar(f.h.ID, c.ID)
	if s.Sync.Failures != 0 || s.Sync.LastError.Key != "" {
		t.Fatalf("after success: %+v", s.Sync)
	}
}

func TestRemovingTheCalendarRemovesItsEventsAndLog(t *testing.T) {
	f := newCalFixture(t)
	c := newICS(t, f)
	_, _ = f.db.CompleteSync(f.h.ID, c.ID, []store.SyncItem{item("a", "", "Choir", at(2026, 11, 20, 17, 0))}, nil, t0, t0.Add(time.Hour))
	local, _ := f.db.CreateEvent(f.h.ID, calendar.Event{CalendarID: f.cal.ID, Title: "Own", Start: at(2026, 11, 20, 8, 0), End: at(2026, 11, 20, 9, 0)})

	if err := f.db.DeleteSyncedCalendar(f.h.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	evs, _ := f.db.EventsBetween(f.h.ID, at(2026, 1, 1, 0, 0), at(2027, 12, 31, 0, 0))
	if len(evs) != 1 || evs[0].ID != local.ID {
		t.Fatalf("events after removal = %+v", evs)
	}
	if _, err := f.db.SyncedCalendar(f.h.ID, c.ID); err == nil {
		t.Fatal("calendar still there")
	}
	if err := f.db.DeleteSyncedCalendar(f.h.ID, f.cal.ID); err == nil {
		t.Fatal("the family calendar must not be removable here")
	}
}

func TestSameNoteIsLoggedOnce(t *testing.T) {
	f := newCalFixture(t)
	c := newICS(t, f)
	note := i18n.Msg("sync.note.unknown_zone", "zone", "Mars/Olympus")
	other := i18n.Msg("sync.note.unknown_zone", "zone", "Venus/Maxwell")
	for i, n := range [][]i18n.Message{{note}, {note, other}, {note}} {
		at := t0.Add(time.Duration(i) * time.Hour)
		if _, err := f.db.CompleteSync(f.h.ID, c.ID, nil, n, at, at.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if log, err := f.db.SyncLog(f.h.ID, 10); err != nil || len(log) != 2 {
		t.Fatalf("log = %+v, %v", log, err)
	}
}

func TestSyncedEventsMayHaveNoTitle(t *testing.T) {
	f := newCalFixture(t)
	c := newICS(t, f)
	if _, err := f.db.CompleteSync(f.h.ID, c.ID, []store.SyncItem{item("a", "", "", at(2026, 11, 20, 17, 0))}, nil, t0, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := titles(t, f, c.ID); len(got) != 1 || got[0] != "" {
		t.Fatalf("titles = %q", got)
	}
}

func TestDeferConnectionKeepsALaterAttemptAndRemembersTheLongestWait(t *testing.T) {
	f := newDAVFixture(t)
	next := func(c store.SyncedCalendar) time.Time {
		got, err := f.db.SyncedCalendar(f.h.ID, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Sync.NextAttempt
	}
	until := func(c store.SyncedCalendar) time.Time {
		got, _ := f.db.SyncedCalendar(f.h.ID, c.ID)
		return got.ThrottledUntil
	}
	if err := f.db.DeferConnection(f.h.ID, f.conn.ID, t0.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, c := range []store.SyncedCalendar{f.a, f.b} {
		if !next(c).Equal(t0.Add(30*time.Minute)) || !until(c).Equal(t0.Add(30*time.Minute)) {
			t.Fatalf("next %v, throttled until %v, want both %v", next(c), until(c), t0.Add(30*time.Minute))
		}
	}

	// A shorter wait changes nothing; a later attempt of one calendar stays.
	if err := f.db.DeferSync(f.a.ID, t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.db.DeferConnection(f.h.ID, f.conn.ID, t0.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !next(f.a).Equal(t0.Add(2*time.Hour)) || !next(f.b).Equal(t0.Add(30*time.Minute)) {
		t.Fatalf("next attempts %v and %v, want 2h and 30m after t0", next(f.a), next(f.b))
	}
	if !until(f.a).Equal(t0.Add(30 * time.Minute)) {
		t.Fatalf("throttled until %v, want the longer wait", until(f.a))
	}

	// A longer wait moves the earlier calendar and the marker, not the later one.
	if err := f.db.DeferConnection(f.h.ID, f.conn.ID, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !next(f.a).Equal(t0.Add(2*time.Hour)) || !next(f.b).Equal(t0.Add(time.Hour)) || !until(f.b).Equal(t0.Add(time.Hour)) {
		t.Fatalf("next %v and %v, until %v", next(f.a), next(f.b), until(f.b))
	}
}
