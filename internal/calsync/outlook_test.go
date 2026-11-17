package calsync_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/graph"
	"github.com/hejmo-home/hejmo/internal/graph/graphtest"
	"github.com/hejmo-home/hejmo/internal/secret"
	"github.com/hejmo-home/hejmo/internal/store"
)

type outlookFx struct {
	fake  *graphtest.Server
	db    *store.DB
	h     store.Household
	mia   store.Person
	conn  store.Connection
	cal   store.SyncedCalendar
	svc   *calsync.Service
	clock time.Time
}

func newOutlookFx(t *testing.T) *outlookFx {
	t.Helper()
	fake := graphtest.New()
	t.Cleanup(fake.Close)
	fake.AddCalendar("cal-1", "Kalender")
	db, err := store.Open(filepath.Join(t.TempDir(), "hejmo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	box, _ := secret.Open(filepath.Join(t.TempDir(), "secret.key"))
	db.SetSecrets(box)
	h, err := db.CreateHousehold(store.Household{Name: "Home", Region: "DE-BY", Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatal(err)
	}
	mia, _ := db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	fx := &outlookFx{fake: fake, db: db, h: h, mia: mia, clock: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
	fx.conn, err = db.CreateOutlookConnection(h.ID, store.NewOutlookConnection{Account: "mama@example.org", RefreshToken: "refresh-0", Now: fx.clock})
	if err != nil {
		t.Fatal(err)
	}
	fx.cal, err = db.AddOutlookCalendar(h.ID, fx.conn.ID, store.NewOutlookCalendar{Name: "Mama", RemoteID: "cal-1", IntervalMinutes: 5, Now: fx.clock})
	if err != nil {
		t.Fatal(err)
	}
	fx.svc = calsync.New(db, nil, func() time.Time { return fx.clock })
	fx.svc.SetGraph(&graph.Client{AuthBase: fake.AuthBase(), GraphBase: fake.GraphBase(), DefaultClientID: "app-1"})
	return fx
}

func (fx *outlookFx) sync(t *testing.T) error {
	t.Helper()
	return fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID)
}

func (fx *outlookFx) mustSync(t *testing.T) {
	t.Helper()
	if err := fx.sync(t); err != nil {
		t.Fatalf("sync: %v", err)
	}
}

func dt(day, hhmm string) map[string]any {
	return map[string]any{"dateTime": "2026-" + day + "T" + hhmm + ":00.0000000", "timeZone": "W. Europe Standard Time"}
}

func gev(subject, day, from, to string) map[string]any {
	return map[string]any{"subject": subject, "start": dt(day, from), "end": dt(day, to), "body": map[string]any{"contentType": "text", "content": ""}}
}

func (fx *outlookFx) events(t *testing.T) map[string]calendar.Event {
	t.Helper()
	evs, err := fx.db.CalendarEvents(fx.h.ID, fx.cal.ID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]calendar.Event{}
	for _, e := range evs {
		out[e.Title] = e
	}
	return out
}

func (fx *outlookFx) series() {
	fx.fake.Put("cal-1", map[string]any{"id": "m1", "type": "seriesMaster", "subject": "Fußball",
		"start": dt("11-02", "15:00"), "end": dt("11-02", "16:00"),
		"recurrence": map[string]any{"pattern": map[string]any{"type": "weekly", "interval": 1, "daysOfWeek": []string{"monday"}, "firstDayOfWeek": "monday"}, "range": map[string]any{"type": "endDate", "endDate": "2026-11-30"}}})
	for _, d := range []string{"11-02", "11-23", "11-30"} {
		in := gev("Fußball", d, "15:00", "16:00")
		in["id"], in["seriesMasterId"], in["type"] = "i-"+d, "m1", "occurrence"
		fx.fake.Put("cal-1", in)
	}
	moved := gev("Fußball (Halle)", "11-16", "16:00", "17:00")
	moved["id"], moved["seriesMasterId"], moved["type"], moved["originalStart"] = "i-11-16", "m1", "exception", "2026-11-16T14:00:00Z"
	fx.fake.Put("cal-1", moved)
}

func TestFirstSyncImportsSingleEventsSeriesAndInvitations(t *testing.T) {
	fx := newOutlookFx(t)
	fx.fake.Put("cal-1", gev("Zahnarzt", "11-20", "08:30", "09:00"))
	inv := gev("Elternabend", "11-19", "19:30", "21:00")
	inv["attendees"] = []map[string]any{{"emailAddress": map[string]any{"name": "Papa", "address": "papa@example.org"}}}
	inv["organizer"] = map[string]any{"emailAddress": map[string]any{"name": "Huber, Annemarie", "address": "h@example.org"}}
	inv["responseStatus"] = map[string]any{"response": "accepted"}
	inv["webLink"] = "https://outlook.live.com/owa/?itemid=x"
	fx.fake.Put("cal-1", inv)
	fx.series()
	fx.mustSync(t)

	evs := fx.events(t)
	if _, ok := evs["Zahnarzt"]; !ok || len(evs) != 4 {
		t.Fatalf("events = %v", keys(evs))
	}
	e := evs["Elternabend"]
	if e.Invitation == nil || e.Invitation.Organizer != "Huber, Annemarie" || e.Invitation.WebLink == "" {
		t.Errorf("invitation = %+v", e.Invitation)
	}
	master := evs["Fußball"]
	if master.RRule == "" || len(master.ExDates) != 1 || master.ExDates[0].Format("2006-01-02") != "2026-11-09" {
		t.Errorf("series = %+v", master)
	}
	if o := evs["Fußball (Halle)"]; o.SeriesID != master.ID || o.Start.Hour() != 16 {
		t.Errorf("override = %+v", o)
	}
	cal, _ := fx.db.SyncedCalendar(fx.h.ID, fx.cal.ID)
	if cal.Sync.LastSuccess.IsZero() || !cal.Sync.NextAttempt.Equal(fx.clock.Add(5*time.Minute)) || cal.Sync.EventCount != 3 {
		t.Errorf("sync state = %+v", cal.Sync)
	}
}

func keys(m map[string]calendar.Event) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestLaterSyncsAskOnlyForWhatChangedAndRefreshAWholeSeries(t *testing.T) {
	fx := newOutlookFx(t)
	zahn := fx.fake.Put("cal-1", gev("Zahnarzt", "11-20", "08:30", "09:00"))
	fx.fake.Put("cal-1", gev("Schwimmen", "11-21", "10:00", "11:00"))
	fx.series()
	fx.mustSync(t)
	mastersBefore := fx.fake.Count("GET /v1.0/me/events/m1/instances")

	// A change to one occurrence refreshes the whole series (Q51); a removed single event goes.
	changed := fx.fake.Event("cal-1", "i-11-23")
	changed["subject"] = "Fußball im Regen"
	fx.fake.Put("cal-1", changed)
	fx.fake.Remove("cal-1", zahn)
	fx.clock = fx.clock.Add(5 * time.Minute)
	fx.mustSync(t)

	evs := fx.events(t)
	if _, ok := evs["Zahnarzt"]; ok {
		t.Error("removed event is still on the box")
	}
	if _, ok := evs["Schwimmen"]; !ok {
		t.Error("untouched event vanished")
	}
	if fx.fake.Count("GET /v1.0/me/events/m1/instances") != mastersBefore+1 {
		t.Errorf("series read %d times after the change, want once", fx.fake.Count("GET /v1.0/me/events/m1/instances")-mastersBefore)
	}
	// Nothing changed: no event is read at all.
	reads := fx.fake.Count("GET /v1.0/me/events/")
	fx.clock = fx.clock.Add(5 * time.Minute)
	fx.mustSync(t)
	if fx.fake.Count("GET /v1.0/me/events/") != reads {
		t.Error("an unchanged calendar read events")
	}
}

func TestAnExpiredDeltaLinkStartsOverAndDropsWhatIsGone(t *testing.T) {
	fx := newOutlookFx(t)
	a := fx.fake.Put("cal-1", gev("Zahnarzt", "11-20", "08:30", "09:00"))
	fx.fake.Put("cal-1", gev("Schwimmen", "11-21", "10:00", "11:00"))
	fx.mustSync(t)
	fx.fake.Remove("cal-1", a)
	fx.fake.Set(func(s *graphtest.Server) { s.ExpireDeltaTokens = true })
	fx.clock = fx.clock.Add(5 * time.Minute)
	fx.mustSync(t)
	evs := fx.events(t)
	if _, ok := evs["Zahnarzt"]; ok || len(evs) != 1 {
		t.Fatalf("after starting over: %v", keys(evs))
	}
}

func TestAudienceLineIsReadAndStrippedOnImport(t *testing.T) {
	fx := newOutlookFx(t)
	e := gev("Schwimmkurs", "11-20", "15:00", "16:00")
	e["body"] = map[string]any{"contentType": "text", "content": "Schwimmbad\nFür: Mia"}
	fx.fake.Put("cal-1", e)
	fx.mustSync(t)
	got := fx.events(t)["Schwimmkurs"]
	if got.Description != "Schwimmbad" || len(got.People) != 1 || got.People[0] != fx.mia.ID {
		t.Fatalf("description %q, people %v", got.Description, got.People)
	}
}

func TestBoxChangesAreWrittenToOutlook(t *testing.T) {
	fx := newOutlookFx(t)
	ctx := context.Background()
	start := time.Date(2026, 11, 24, 8, 30, 0, 0, berlinZone)
	created, err := fx.db.CreateEvent(fx.h.ID, calendar.Event{CalendarID: fx.cal.ID, Title: "Kinderarzt", Start: start, End: start.Add(30 * time.Minute), People: []string{fx.mia.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.Push(ctx, fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	ids := fx.fake.Events("cal-1")
	if len(ids) != 1 {
		t.Fatalf("Outlook holds %v", ids)
	}
	out := fx.fake.Event("cal-1", ids[0])
	if out["subject"] != "Kinderarzt" || !strings.Contains(out["body"].(map[string]any)["content"].(string), "Für: Mia") {
		t.Fatalf("created = %+v", out)
	}
	// The box event now belongs to the Outlook event.
	pending, _ := fx.db.PendingObjects(fx.cal.ID)
	if len(pending) != 0 {
		t.Fatalf("still pending: %+v", pending)
	}

	// Change and delete.
	created.Title = "Kinderarzt (Kontrolle)"
	if err := fx.db.ApplyChange(fx.h.ID, calendar.Change{Update: []calendar.Event{created}}); err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.Push(ctx, fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	if got := fx.fake.Event("cal-1", ids[0])["subject"]; got != "Kinderarzt (Kontrolle)" {
		t.Fatalf("after change subject = %v", got)
	}
	if err := fx.db.ApplyChange(fx.h.ID, calendar.Change{Delete: []string{created.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.Push(ctx, fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	if len(fx.fake.Events("cal-1")) != 0 {
		t.Fatalf("Outlook still holds %v", fx.fake.Events("cal-1"))
	}
}

func TestOutsideChangeWinsAConflictAndTheLoserStaysInTheLog(t *testing.T) {
	fx := newOutlookFx(t)
	id := fx.fake.Put("cal-1", gev("Zahnarzt", "11-20", "08:30", "09:00"))
	fx.mustSync(t)
	e := fx.events(t)["Zahnarzt"]
	e.Title = "Zahnarzt (Box)"
	if err := fx.db.ApplyChange(fx.h.ID, calendar.Change{Update: []calendar.Event{e}}); err != nil {
		t.Fatal(err)
	}
	// Outlook changes it later than the box did.
	later := fx.fake.Event("cal-1", id)
	later["subject"] = "Zahnarzt (Outlook)"
	fx.fake.PutAt("cal-1", later, time.Now().Add(time.Hour))
	_ = fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID)
	if got := fx.fake.Event("cal-1", id)["subject"]; got != "Zahnarzt (Outlook)" {
		t.Fatalf("Outlook now says %v, want its own later change to win", got)
	}
	if _, ok := fx.events(t)["Zahnarzt (Outlook)"]; !ok {
		t.Fatalf("the box did not take Outlook's version: %v", keys(fx.events(t)))
	}
	log, _ := fx.db.SyncLog(fx.h.ID, 10)
	var conflict bool
	for _, l := range log {
		conflict = conflict || (l.Kind == "conflict" && strings.Contains(l.Detail, "Zahnarzt (Box)"))
	}
	if !conflict {
		t.Fatalf("no conflict logged with the losing version: %+v", log)
	}
}

func TestBoxChangeWinsWhenItIsTheLaterOne(t *testing.T) {
	fx := newOutlookFx(t)
	id := fx.fake.Put("cal-1", gev("Zahnarzt", "11-20", "08:30", "09:00"))
	fx.mustSync(t)
	e := fx.events(t)["Zahnarzt"]
	e.Title = "Zahnarzt (Box)"
	if err := fx.db.ApplyChange(fx.h.ID, calendar.Change{Update: []calendar.Event{e}}); err != nil {
		t.Fatal(err)
	}
	other := fx.fake.Event("cal-1", id)
	other["subject"] = "Zahnarzt (Outlook)"
	fx.fake.PutAt("cal-1", other, time.Now().Add(-time.Hour))
	if err := fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	if got := fx.fake.Event("cal-1", id)["subject"]; got != "Zahnarzt (Box)" {
		t.Fatalf("Outlook says %v, want the box's later change", got)
	}
}

func TestARevokedSignInStopsTheSyncAndMarksTheConnection(t *testing.T) {
	fx := newOutlookFx(t)
	fx.fake.Put("cal-1", gev("Zahnarzt", "11-20", "08:30", "09:00"))
	fx.mustSync(t)
	fx.fake.Set(func(s *graphtest.Server) { s.RevokeRefresh = true })
	fx.clock = fx.clock.Add(time.Hour)
	if err := fx.sync(t); err == nil {
		t.Fatal("a revoked sign-in synced")
	}
	need, _ := fx.db.ReauthConnections(fx.h.ID)
	if len(need) != 1 || !need[0].ReauthSince.Equal(fx.clock) {
		t.Fatalf("connections needing sign-in = %+v", need)
	}
	cal, _ := fx.db.SyncedCalendar(fx.h.ID, fx.cal.ID)
	if cal.Sync.LastError.Key != "sync.fail.outlook_signin" {
		t.Errorf("last error = %+v", cal.Sync.LastError)
	}
	// The old events stay (ADR 0012), and no further request goes to Microsoft until the sign-in is renewed.
	if _, ok := fx.events(t)["Zahnarzt"]; !ok {
		t.Error("events were removed")
	}
	calls := len(fx.fake.Requests)
	fx.clock = fx.clock.Add(7 * time.Hour)
	_ = fx.sync(t)
	if len(fx.fake.Requests) != calls {
		t.Errorf("synced with a dead sign-in: %d new requests", len(fx.fake.Requests)-calls)
	}
	// A new sign-in clears it; the calendar syncs again.
	fx.fake.Set(func(s *graphtest.Server) { s.RevokeRefresh = false })
	if err := fx.db.Reconnected(fx.h.ID, fx.conn.ID, "refresh-"+itoa(fx.fake.Refreshes), fx.clock); err != nil {
		t.Fatal(err)
	}
	fx.mustSync(t)
}

func itoa(n int) string { return string(rune('0' + n)) }

func TestARotatedRefreshTokenIsKept(t *testing.T) {
	fx := newOutlookFx(t)
	fx.mustSync(t)
	_, token, err := fx.db.ConnectionCredentials(fx.h.ID, fx.conn.ID)
	if err != nil || token != "refresh-1" {
		t.Fatalf("stored token = %q, %v", token, err)
	}
	// And the next sync (a new session) signs in with it.
	fx.clock = fx.clock.Add(2 * time.Hour)
	fx.mustSync(t)
}

func TestDiscoverMarksSharedCalendarsAsLockedAndCountsEvents(t *testing.T) {
	fx := newOutlookFx(t)
	fx.fake.AddShared("cal-2", "Kalender von Papa", "Papa Maier", "papa@example.org")
	fx.fake.Put("cal-1", gev("Zahnarzt", "11-20", "08:30", "09:00"))
	found, err := fx.svc.DiscoverOutlook(context.Background(), fx.h.ID, fx.conn.ID)
	if err != nil || len(found) != 2 {
		t.Fatalf("found = %+v, %v", found, err)
	}
	if found[0].Locked || found[0].ID != "cal-1" {
		t.Errorf("own = %+v", found[0])
	}
	if !found[1].Locked || found[1].SharedBy != "Papa Maier (papa@example.org)" {
		t.Errorf("shared = %+v", found[1])
	}
	n, err := fx.svc.CountOutlook(context.Background(), fx.h.ID, fx.conn.ID, "cal-1")
	if err != nil || n != 1 {
		t.Errorf("count = %d, %v", n, err)
	}
}

var _ = errors.New
var _ = graph.ErrReauth

var berlinZone, _ = time.LoadLocation("Europe/Berlin")

func TestASeriesCreatedOnTheBoxIsWrittenWithItsRecurrence(t *testing.T) {
	fx := newOutlookFx(t)
	start := time.Date(2026, 11, 20, 17, 0, 0, 0, berlinZone)
	if _, err := fx.db.CreateEvent(fx.h.ID, calendar.Event{CalendarID: fx.cal.ID, Title: "Chor", Start: start, End: start.Add(time.Hour), RRule: "FREQ=WEEKLY;BYDAY=FR"}); err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.Push(context.Background(), fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	ids := fx.fake.Events("cal-1")
	if len(ids) != 1 {
		t.Fatalf("Outlook holds %v", ids)
	}
	rec, _ := fx.fake.Event("cal-1", ids[0])["recurrence"].(map[string]any)
	if rec == nil || rec["pattern"].(map[string]any)["type"] != "weekly" {
		t.Fatalf("recurrence = %+v", rec)
	}
}

func TestNothingIsWrittenForASharedReadOnlyCalendar(t *testing.T) {
	fx := newOutlookFx(t)
	fx.fake.AddShared("cal-2", "Papa", "Papa Maier", "papa@example.org")
	shared, err := fx.db.AddOutlookCalendar(fx.h.ID, fx.conn.ID, store.NewOutlookCalendar{Name: "Papa", RemoteID: "cal-2", SourceLocked: true, IntervalMinutes: 5, Now: fx.clock})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 20, 17, 0, 0, 0, berlinZone)
	if _, err := fx.db.CreateEvent(fx.h.ID, calendar.Event{CalendarID: shared.ID, Title: "X", Start: start, End: start.Add(time.Hour)}); err == nil {
		t.Fatal("an event was created in a calendar that Outlook locks")
	}
}
