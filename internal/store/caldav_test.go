package store_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/secret"
	"github.com/hejmo-home/hejmo/internal/store"
)

func openSealed(t *testing.T) (*store.DB, string) {
	t.Helper()
	db, path := openTemp(t)
	box, err := secret.Open(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetSecrets(box)
	return db, path
}

type davFixture struct {
	db   *store.DB
	path string
	h    store.Household
	conn store.Connection
	a, b store.SyncedCalendar
}

func newDAVFixture(t *testing.T) davFixture {
	t.Helper()
	db, path := openSealed(t)
	h, _ := seedParent(t, db)
	conn, err := db.CreateCalDAVConnection(h.ID, store.NewCalDAVConnection{
		URL: "https://caldav.example.org/", Username: "mama@example.org", Password: "xxxx-hunter2-xxxx", Now: t0,
	})
	if err != nil {
		t.Fatal(err)
	}
	add := func(name, remote string) store.SyncedCalendar {
		c, err := db.AddCalDAVCalendar(h.ID, conn.ID, store.NewCalDAVCalendar{
			Name: name, RemoteURL: remote, Color: "#2F5E7A", IntervalMinutes: 5, Now: t0,
		})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	return davFixture{db, path, h, conn,
		add("Family", "https://caldav.example.org/mama/family/"), add("School", "https://caldav.example.org/mama/school/")}
}

func TestStoredCredentialsAreNotReadableWithoutTheKey(t *testing.T) {
	f := newDAVFixture(t)
	f.db.SQL().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	f.db.Close()
	for _, name := range []string{f.path, f.path + "-wal"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		if bytes.Contains(raw, []byte("xxxx-hunter2-xxxx")) {
			t.Fatalf("%s holds the password in plain text", filepath.Base(name))
		}
	}
}

func TestCredentialsComeBackWithTheKeyAndNeverFromTheListing(t *testing.T) {
	f := newDAVFixture(t)
	user, pass, err := f.db.ConnectionCredentials(f.h.ID, f.conn.ID)
	if err != nil || user != "mama@example.org" || pass != "xxxx-hunter2-xxxx" {
		t.Fatalf("credentials = %q, %q, %v", user, pass, err)
	}
	conns, err := f.db.Connections(f.h.ID)
	if err != nil || len(conns) != 1 || conns[0].Username != "mama@example.org" || conns[0].Kind != "caldav" || conns[0].URL != "https://caldav.example.org/" {
		t.Fatalf("Connections() = %+v, %v", conns, err)
	}
}

func TestCreatingAConnectionWithoutAKeyFailsInsteadOfStoringPlainText(t *testing.T) {
	db, _ := openTemp(t) // no secrets set
	h, _ := seedParent(t, db)
	if _, err := db.CreateCalDAVConnection(h.ID, store.NewCalDAVConnection{URL: "https://x.example/", Username: "u", Password: "p", Now: t0}); err == nil {
		t.Fatal("stored a credential without an encryption key")
	}
}

func TestCalDAVCalendarsAreReadOnlyUntilWriteBackExistsAndDueAtOnce(t *testing.T) {
	f := newDAVFixture(t)
	if f.a.Kind != "caldav" || !f.a.ReadOnly || f.a.URL != "https://caldav.example.org/mama/family/" || f.a.ConnectionID != f.conn.ID || f.a.Sync.IntervalMinutes != 5 {
		t.Fatalf("calendar = %+v", f.a)
	}
	due, _ := f.db.DueCalendars(t0)
	if len(due) != 2 {
		t.Fatalf("due = %+v", due)
	}
	if _, err := f.db.AddCalDAVCalendar(f.h.ID, f.conn.ID, store.NewCalDAVCalendar{Name: "Again", RemoteURL: f.a.URL, IntervalMinutes: 5, Now: t0}); err == nil {
		t.Fatal("added the same remote calendar twice")
	}
}

func obj(url, etag string, items ...store.SyncItem) store.ObjectChange {
	return store.ObjectChange{URL: url, ETag: etag, Raw: "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n", Items: items}
}

func TestObjectSyncAddsChangesAndDeletesPerObject(t *testing.T) {
	f := newDAVFixture(t)
	next := t0.Add(5 * time.Minute)
	u1, u2 := f.a.URL+"a.ics", f.a.URL+"b.ics"

	if err := f.db.CompleteObjectSync(f.h.ID, f.a.ID, []store.ObjectChange{
		obj(u1, "e1", item("a@x", "", "Swimming", at(2026, 11, 20, 15, 0))),
		obj(u2, "e2", item("b@x", "", "Piano", at(2026, 11, 21, 15, 0))),
	}, nil, nil, t0, next); err != nil {
		t.Fatal(err)
	}
	known, err := f.db.RemoteObjects(f.a.ID)
	if err != nil || len(known) != 2 || known[u1] != "e1" || known[u2] != "e2" {
		t.Fatalf("RemoteObjects = %v, %v", known, err)
	}
	first, _ := f.db.EventsBetween(f.h.ID, at(2026, 11, 1, 0, 0), at(2026, 12, 1, 0, 0))
	id := map[string]string{}
	for _, e := range first {
		id[e.Title] = e.ID
	}

	// Next sync: object 1 is renamed, object 2 was deleted outside.
	if err := f.db.CompleteObjectSync(f.h.ID, f.a.ID, []store.ObjectChange{
		obj(u1, "e1b", item("a@x", "", "Swimming lesson", at(2026, 11, 20, 15, 0))),
	}, []string{u2}, nil, t0.Add(5*time.Minute), next.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got := titles(t, f2(f), f.a.ID)
	if len(got) != 1 || got[0] != "Swimming lesson" {
		t.Fatalf("events = %v", got)
	}
	after, _ := f.db.EventsBetween(f.h.ID, at(2026, 11, 1, 0, 0), at(2026, 12, 1, 0, 0))
	if after[0].ID != id["Swimming"] {
		t.Fatal("event id changed across syncs")
	}
	known, _ = f.db.RemoteObjects(f.a.ID)
	if len(known) != 1 || known[u1] != "e1b" {
		t.Fatalf("RemoteObjects after = %v", known)
	}
	s, _ := f.db.SyncedCalendar(f.h.ID, f.a.ID)
	if !s.Sync.LastSuccess.Equal(t0.Add(5*time.Minute)) || s.Sync.Failures != 0 {
		t.Fatalf("sync state = %+v", s.Sync)
	}
}

func TestUnreadableObjectKeepsItsEventsButRemembersItsETag(t *testing.T) {
	f := newDAVFixture(t)
	u1, u2 := f.a.URL+"a.ics", f.a.URL+"new.ics"
	_ = f.db.CompleteObjectSync(f.h.ID, f.a.ID, []store.ObjectChange{obj(u1, "e1", item("a@x", "", "Swimming", at(2026, 11, 20, 15, 0)))}, nil, nil, t0, t0)
	var goodRaw string
	if err := f.db.SQL().QueryRow(`SELECT raw FROM remote_objects WHERE url = ?`, u1).Scan(&goodRaw); err != nil || goodRaw == "junk" {
		t.Fatalf("raw = %q, %v", goodRaw, err)
	}

	// The object changed on the server but cannot be read; a new one never could.
	keep := func(url, etag string) store.ObjectChange {
		return store.ObjectChange{URL: url, ETag: etag, Raw: "junk", KeepEvents: true}
	}
	if err := f.db.CompleteObjectSync(f.h.ID, f.a.ID, []store.ObjectChange{keep(u1, "e2"), keep(u2, "n1")}, nil, nil, t0, t0); err != nil {
		t.Fatal(err)
	}
	if got := titles(t, f2(f), f.a.ID); len(got) != 1 || got[0] != "Swimming" {
		t.Fatalf("events = %v, want the last good Swimming", got)
	}
	known, _ := f.db.RemoteObjects(f.a.ID)
	if len(known) != 2 || known[u1] != "e2" || known[u2] != "n1" {
		t.Fatalf("RemoteObjects = %v", known)
	}
	var raw string
	if err := f.db.SQL().QueryRow(`SELECT raw FROM remote_objects WHERE url = ?`, u1).Scan(&raw); err != nil || raw != goodRaw {
		t.Fatalf("raw = %q, %v; want the last good text kept", raw, err)
	}
}

func TestEventMovedBetweenObjectsKeepsOneCopy(t *testing.T) {
	f := newDAVFixture(t)
	u1, u2 := f.a.URL+"a.ics", f.a.URL+"b.ics"
	_ = f.db.CompleteObjectSync(f.h.ID, f.a.ID, []store.ObjectChange{obj(u1, "e1", item("a@x", "", "Swimming", at(2026, 11, 20, 15, 0)))}, nil, nil, t0, t0)
	// The server now serves the same UID from another file.
	if err := f.db.CompleteObjectSync(f.h.ID, f.a.ID, []store.ObjectChange{obj(u2, "e2", item("a@x", "", "Swimming", at(2026, 11, 20, 15, 0)))}, []string{u1}, nil, t0, t0); err != nil {
		t.Fatal(err)
	}
	if got := titles(t, f2(f), f.a.ID); len(got) != 1 {
		t.Fatalf("events = %v", got)
	}
}

func f2(f davFixture) calFixture { return calFixture{db: f.db, h: f.h} }

func TestRemovingAConnectionRemovesItsCalendarsEventsAndNothingElse(t *testing.T) {
	f := newDAVFixture(t)
	for _, c := range []store.SyncedCalendar{f.a, f.b} {
		if err := f.db.CompleteObjectSync(f.h.ID, c.ID, []store.ObjectChange{obj(c.URL+"x.ics", "e", item("x@"+c.Name, "", "Event of "+c.Name, at(2026, 11, 20, 15, 0)))}, nil, nil, t0, t0); err != nil {
			t.Fatal(err)
		}
	}
	family, _ := f.db.FamilyCalendar(f.h.ID)
	if _, err := f.db.CreateEvent(f.h.ID, calendar.Event{CalendarID: family.ID, Title: "Own event", Start: at(2026, 11, 22, 9, 0), End: at(2026, 11, 22, 10, 0)}); err != nil {
		t.Fatal(err)
	}
	other, _ := f.db.CreateICSCalendar(f.h.ID, store.NewICS{Name: "Holidays", URL: "https://example.org/h.ics", Color: "#2F5E7A", IntervalMinutes: 60, Now: t0})
	_ = f.db.CompleteSync(f.h.ID, other.ID, []store.SyncItem{item("h", "", "Holiday", at(2026, 11, 23, 0, 0))}, nil, t0, t0)

	if err := f.db.DeleteConnection(f.h.ID, f.conn.ID); err != nil {
		t.Fatal(err)
	}
	evs, _ := f.db.EventsBetween(f.h.ID, at(2026, 11, 1, 0, 0), at(2026, 12, 1, 0, 0))
	var got []string
	for _, e := range evs {
		got = append(got, e.Title)
	}
	if len(got) != 2 {
		t.Fatalf("events left = %v, want only the family event and the ICS holiday", got)
	}
	if _, err := f.db.SyncedCalendar(f.h.ID, f.a.ID); err == nil {
		t.Fatal("calendar survived its connection")
	}
	if left, _ := f.db.RemoteObjects(f.a.ID); len(left) != 0 {
		t.Fatalf("remote objects survived: %v", left)
	}
	if conns, _ := f.db.Connections(f.h.ID); len(conns) != 0 {
		t.Fatalf("connections = %+v", conns)
	}
}

func TestRemovingOneCalDAVCalendarLeavesTheOtherAndTheConnection(t *testing.T) {
	f := newDAVFixture(t)
	if err := f.db.DeleteSyncedCalendar(f.h.ID, f.a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.SyncedCalendar(f.h.ID, f.b.ID); err != nil {
		t.Fatalf("sibling calendar gone: %v", err)
	}
	if conns, _ := f.db.Connections(f.h.ID); len(conns) != 1 {
		t.Fatalf("connection removed with one calendar: %+v", conns)
	}
}
