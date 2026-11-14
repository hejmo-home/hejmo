package calsync_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldavclient/caldavtest"
	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/store"
)

// iCloudShaped is an object as iCloud keeps it: attendee, alarm and properties
// the box does not know. (Radicale refuses what iCloud holds, such as two STATUS
// lines; the fake in icloud_test.go keeps those.)
func iCloudShaped(uid, summary, lastModified string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Apple Inc.//iOS 18//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:" + lastModified + "\r\nLAST-MODIFIED:" + lastModified + "\r\nSEQUENCE:1\r\n" +
		"DTSTART;TZID=Europe/Berlin:20261120T150000\r\nDTEND;TZID=Europe/Berlin:20261120T160000\r\n" +
		"SUMMARY:" + summary + "\r\nSTATUS:CONFIRMED\r\nX-APPLE-TRAVEL-ADVISORY-BEHAVIOR:AUTOMATIC\r\n" +
		"ATTENDEE;CN=Papa;PARTSTAT=ACCEPTED:mailto:papa@example.org\r\n" +
		"BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT15M\r\nDESCRIPTION:Reminder\r\nEND:VALARM\r\n" +
		"END:VEVENT\r\nEND:VCALENDAR\r\n"
}

// boxEvent finds the event with this title in the calendar.
func (fx *davFixture) boxEvent(t *testing.T, title string) calendar.Event {
	t.Helper()
	evs, err := fx.db.CalendarEvents(fx.h.ID, fx.cal.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Title == title {
			return e
		}
	}
	t.Fatalf("no event %q on the box, have %d events", title, len(evs))
	return calendar.Event{}
}

// edit changes an event on the box the way the API does and pushes at once.
func (fx *davFixture) edit(t *testing.T, e calendar.Event, patch func(*calendar.Event)) {
	t.Helper()
	patch(&e)
	if err := fx.db.ApplyChange(fx.h.ID, calendar.Change{Update: []calendar.Event{e}}); err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
}

func TestEditOnTheBoxIsVisibleOutsideAndKeepsAttendeesAndAlarm(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	obj := fx.srv.Put(t, fx.remote, "swim.ics", iCloudShaped("swim@icloud", "Swimming", "20260101T080000Z"))
	fx.syncAfter(t, 0)

	fx.edit(t, fx.boxEvent(t, "Swimming"), func(e *calendar.Event) { e.Title = "Swimming lesson" })

	got := fx.srv.Get(t, obj)
	for _, want := range []string{"SUMMARY:Swimming lesson", "STATUS:CONFIRMED", "X-APPLE-TRAVEL-ADVISORY-BEHAVIOR:AUTOMATIC", "SEQUENCE:2", "ATTENDEE;CN=Papa;PARTSTAT=ACCEPTED:mailto:papa@example.org", "BEGIN:VALARM", "TRIGGER:-PT15M"} {
		if !strings.Contains(strings.ReplaceAll(got, "\r\n ", ""), want) {
			t.Errorf("outside object lacks %q:\n%s", want, got)
		}
	}
	// The next sync finds nothing new to download: the push left box and server in step.
	before := fx.fetched.Load()
	fx.syncAfter(t, 5*time.Minute)
	if n := fx.fetched.Load() - before; n != 0 {
		t.Fatalf("the sync after a push downloaded %d objects", n)
	}
	if pend, _ := fx.db.PendingObjects(fx.cal.ID); len(pend) != 0 {
		t.Fatalf("pending = %+v", pend)
	}
}

func TestEventCreatedAndDeletedOnTheBoxIsCreatedAndDeletedOutside(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	fx.syncAfter(t, 0)
	e, err := fx.db.CreateEvent(fx.h.ID, calendar.Event{CalendarID: fx.cal.ID, Title: "Piano",
		Start: time.Date(2026, 11, 21, 15, 0, 0, 0, fx.berlin(t)), End: time.Date(2026, 11, 21, 16, 0, 0, 0, fx.berlin(t))})
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	obj := fx.remote + e.ID + ".ics"
	if got := fx.srv.Get(t, obj); !strings.Contains(got, "SUMMARY:Piano") || !strings.Contains(got, "UID:"+e.ID) {
		t.Fatalf("outside = %q", got)
	}
	fx.syncAfter(t, 5*time.Minute)
	if got := fx.titles(t); len(got) != 1 || got[0] != "Piano" {
		t.Fatalf("events after the sync = %v, want the one event, not a copy", got)
	}

	if err := fx.db.ApplyChange(fx.h.ID, calendar.Change{Delete: []string{e.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	if got := fx.srv.Get(t, obj); got != "" {
		t.Fatalf("object still outside: %q", got)
	}
	if pend, _ := fx.db.PendingObjects(fx.cal.ID); len(pend) != 0 {
		t.Fatalf("pending = %+v", pend)
	}
}

func (fx *davFixture) berlin(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestAudienceGoesOutAsPropertyAndReadableLineAndComesBackOnImport(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	mia, _ := fx.db.AddPerson(fx.h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	seb, _ := fx.db.AddPerson(fx.h.ID, store.Person{Name: "Sebastian", Role: store.RoleChild})
	obj := fx.srv.Put(t, fx.remote, "swim.ics", iCloudShaped("swim@icloud", "Swimming", "20260101T080000Z"))
	fx.syncAfter(t, 0)

	fx.edit(t, fx.boxEvent(t, "Swimming"), func(e *calendar.Event) { e.People = []string{mia.ID, seb.ID} })

	got := strings.ReplaceAll(fx.srv.Get(t, obj), "\r\n ", "")
	if !strings.Contains(got, "X-HEJMO-AUDIENCE:person:"+mia.ID+"|person:"+seb.ID) || !strings.Contains(got, "Für: Mia\\, Sebastian") || !strings.Contains(got, "SUMMARY:Swimming\r\n") {
		t.Fatalf("outside object:\n%s", got)
	}
	// A later sync (after the object changes outside) reads the audience back and
	// strips the readable line from the description.
	fx.srv.Put(t, fx.remote, "swim.ics", strings.Replace(fx.srv.Get(t, obj), "SUMMARY:Swimming", "SUMMARY:Swimming club", 1))
	fx.syncAfter(t, 5*time.Minute)
	e := fx.boxEvent(t, "Swimming club")
	if len(e.People) != 2 || e.Description != "" {
		t.Fatalf("event after import = %+v", e)
	}
}

func TestConflictOutsideLaterWinsAndTheBoxVersionStaysInTheLog(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	obj := fx.srv.Put(t, fx.remote, "swim.ics", iCloudShaped("swim@icloud", "Swimming", "20260101T080000Z"))
	fx.syncAfter(t, 0)

	// Changed outside later than anything the box does now (LAST-MODIFIED far ahead).
	fx.srv.Put(t, fx.remote, "swim.ics", iCloudShaped("swim@icloud", "Swim outside", "20990101T080000Z"))
	fx.edit(t, fx.boxEvent(t, "Swimming"), func(e *calendar.Event) { e.Title = "Swim on the box" })

	if got := fx.titles(t); len(got) != 1 || got[0] != "Swim outside" {
		t.Fatalf("box events = %v, want the outside version", got)
	}
	if got := fx.srv.Get(t, obj); !strings.Contains(got, "SUMMARY:Swim outside") {
		t.Fatalf("outside was overwritten:\n%s", got)
	}
	log, _ := fx.db.SyncLog(fx.h.ID, 10)
	if len(log) != 1 || log[0].Kind != "conflict" || log[0].Message.Key != "sync.conflict.outside_won" || !strings.Contains(log[0].Detail, "SUMMARY:Swim on the box") {
		t.Fatalf("log = %+v", log)
	}
	if pend, _ := fx.db.PendingObjects(fx.cal.ID); len(pend) != 0 {
		t.Fatalf("pending = %+v", pend)
	}
}

func TestConflictBoxLaterWinsAndTheOutsideVersionStaysInTheLog(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	obj := fx.srv.Put(t, fx.remote, "swim.ics", iCloudShaped("swim@icloud", "Swimming", "20260101T080000Z"))
	fx.syncAfter(t, 0)

	fx.srv.Put(t, fx.remote, "swim.ics", iCloudShaped("swim@icloud", "Swim outside", "20200101T080000Z"))
	fx.edit(t, fx.boxEvent(t, "Swimming"), func(e *calendar.Event) { e.Title = "Swim on the box" })

	got := fx.srv.Get(t, obj)
	if !strings.Contains(got, "SUMMARY:Swim on the box") || !strings.Contains(got, "ATTENDEE;CN=Papa") {
		t.Fatalf("outside = %s", got)
	}
	log, _ := fx.db.SyncLog(fx.h.ID, 10)
	if len(log) != 1 || log[0].Kind != "conflict" || log[0].Message.Key != "sync.conflict.box_won" || !strings.Contains(log[0].Detail, "SUMMARY:Swim outside") {
		t.Fatalf("log = %+v", log)
	}
	fx.syncAfter(t, 5*time.Minute)
	if got := fx.titles(t); len(got) != 1 || got[0] != "Swim on the box" {
		t.Fatalf("box events = %v", got)
	}
}

func TestDeletedOnTheBoxWhileChangedLaterOutsideComesBack(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	fx.srv.Put(t, fx.remote, "swim.ics", iCloudShaped("swim@icloud", "Swimming", "20260101T080000Z"))
	fx.syncAfter(t, 0)
	fx.srv.Put(t, fx.remote, "swim.ics", iCloudShaped("swim@icloud", "Swim outside", "20990101T080000Z"))
	if err := fx.db.ApplyChange(fx.h.ID, calendar.Change{Delete: []string{fx.boxEvent(t, "Swimming").ID}}); err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	if got := fx.titles(t); len(got) != 1 || got[0] != "Swim outside" {
		t.Fatalf("box events = %v", got)
	}
	if log, _ := fx.db.SyncLog(fx.h.ID, 10); len(log) != 1 || log[0].Kind != "conflict" {
		t.Fatalf("log = %+v", log)
	}
}

func TestChangeMadeWhileTheServerIsDownIsPushedBySyncAndOutsideChangesAreNotLost(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	fx.srv.Put(t, fx.remote, "swim.ics", iCloudShaped("swim@icloud", "Swimming", "20260101T080000Z"))
	fx.srv.Put(t, fx.remote, "piano.ics", caldavtest.Event("piano@test", "Piano", "20261121T150000", "20261121T160000"))
	fx.syncAfter(t, 0)

	e := fx.boxEvent(t, "Swimming")
	e.Title = "Swim on the box"
	_ = fx.db.ApplyChange(fx.h.ID, calendar.Change{Update: []calendar.Event{e}})
	fx.tamper.RefuseWrites(503)
	if err := fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID); err == nil {
		t.Fatal("push to a failing server succeeded")
	}
	if pend, _ := fx.db.PendingObjects(fx.cal.ID); len(pend) != 1 {
		t.Fatalf("pending = %+v, want the change kept", pend)
	}
	if got := fx.titles(t); len(got) != 2 {
		t.Fatalf("events = %v", got)
	}

	fx.tamper.RefuseWrites(0)
	fx.srv.Put(t, fx.remote, "piano.ics", caldavtest.Event("piano@test", "Piano lesson", "20261121T150000", "20261121T160000"))
	fx.syncAfter(t, 5*time.Minute)
	if got := fx.srv.Get(t, fx.remote+"swim.ics"); !strings.Contains(got, "SUMMARY:Swim on the box") {
		t.Fatalf("pending change not pushed by the sync:\n%s", got)
	}
	if got := fx.titles(t); len(got) != 2 || got[0] != "Swim on the box" || got[1] != "Piano lesson" {
		t.Fatalf("events = %v", got)
	}
}

func TestSeriesEditsRoundTripThroughTheServer(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	fx.srv.Put(t, fx.remote, "choir.ics", "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\n"+
		"BEGIN:VEVENT\r\nUID:choir@x\r\nDTSTAMP:20260101T080000Z\r\nDTSTART;TZID=Europe/Berlin:20261102T170000\r\nDTEND;TZID=Europe/Berlin:20261102T180000\r\nRRULE:FREQ=WEEKLY;COUNT=6\r\nSUMMARY:Choir\r\nATTENDEE:mailto:a@example.org\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	fx.syncAfter(t, 0)
	loc := fx.berlin(t)
	ctx := context.Background()
	ed := calendar.Editor{NewID: store.NewID}

	apply := func(f func(series []calendar.Event) (calendar.Change, error)) {
		t.Helper()
		master := fx.boxEvent(t, "Choir")
		series, err := fx.db.Series(fx.h.ID, master.ID)
		if err != nil {
			t.Fatal(err)
		}
		ch, err := f(series)
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.db.ApplyChange(fx.h.ID, ch); err != nil {
			t.Fatal(err)
		}
		if err := fx.svc.Push(ctx, fx.h.ID, fx.cal.ID); err != nil {
			t.Fatal(err)
		}
	}
	// Only this one: delete 9 November, move 16 November to 18:00.
	apply(func(s []calendar.Event) (calendar.Change, error) {
		return ed.Delete(s, time.Date(2026, 11, 9, 17, 0, 0, 0, loc), calendar.ScopeThis)
	})
	newStart := time.Date(2026, 11, 16, 18, 0, 0, 0, loc)
	newEnd := newStart.Add(time.Hour)
	apply(func(s []calendar.Event) (calendar.Change, error) {
		return ed.Edit(s, time.Date(2026, 11, 16, 17, 0, 0, 0, loc), calendar.ScopeThis, calendar.Patch{Start: &newStart, End: &newEnd})
	})
	got := strings.ReplaceAll(fx.srv.Get(t, fx.remote+"choir.ics"), "\r\n ", "")
	for _, want := range []string{"RRULE:FREQ=WEEKLY;COUNT=6", "EXDATE;TZID=Europe/Berlin:20261109T170000", "RECURRENCE-ID;TZID=Europe/Berlin:20261116T170000", "DTSTART;TZID=Europe/Berlin:20261116T180000", "ATTENDEE:mailto:a@example.org"} {
		if !strings.Contains(got, want) {
			t.Errorf("outside object lacks %q:\n%s", want, got)
		}
	}
	// Everything the server holds reads back to the same events on the box.
	before := fx.occurrences(t)
	fx.srv.Put(t, fx.remote, "choir.ics", fx.srv.Get(t, fx.remote+"choir.ics")) // touch: new ETag
	fx.syncAfter(t, 5*time.Minute)
	if after := fx.occurrences(t); after != before {
		t.Fatalf("occurrences changed by the round trip:\nbefore %s\nafter  %s", before, after)
	}

	// This and following: the series is split in two objects.
	apply(func(s []calendar.Event) (calendar.Change, error) {
		title := "Choir (new)"
		return ed.Edit(s, time.Date(2026, 11, 23, 17, 0, 0, 0, loc), calendar.ScopeFollowing, calendar.Patch{Title: &title})
	})
	fx.syncAfter(t, 5*time.Minute)
	if got := fx.srv.Get(t, fx.remote+"choir.ics"); !strings.Contains(got, "COUNT=3") || strings.Contains(got, "Choir (new)") {
		t.Fatalf("old series should end before the split:\n%s", got)
	}
	if after := fx.occurrences(t); !strings.Contains(after, "Choir (new)") {
		t.Fatalf("occurrences = %s", after)
	}
}

// occurrences lists what the calendar shows for the autumn, as one string.
func (fx *davFixture) occurrences(t *testing.T) string {
	t.Helper()
	evs, err := fx.db.EventsBetween(fx.h.ID, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	occs, err := calendar.Expand(evs, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, o := range occs {
		b.WriteString(o.Title + " " + o.Start.Format(time.RFC3339) + "\n")
	}
	return b.String()
}

func TestAReadOnlyCalendarIsNeverWrittenTo(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	obj := fx.srv.Put(t, fx.remote, "swim.ics", iCloudShaped("swim@icloud", "Swimming", "20260101T080000Z"))
	fx.syncAfter(t, 0)
	if err := fx.db.SetCalendarReadOnly(fx.h.ID, fx.cal.ID, true); err != nil {
		t.Fatal(err)
	}
	e := fx.boxEvent(t, "Swimming")
	e.Title = "Changed on the box"
	_ = fx.db.ApplyChange(fx.h.ID, calendar.Change{Update: []calendar.Event{e}})
	if err := fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	if got := fx.srv.Get(t, obj); !strings.Contains(got, "SUMMARY:Swimming\r\n") {
		t.Fatalf("outside = %s", got)
	}
}
