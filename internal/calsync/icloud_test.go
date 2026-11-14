package calsync_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/secret"
	"github.com/hejmo-home/hejmo/internal/store"
)

// fakeICloud keeps objects exactly as they were sent, repeated lines and all
// (Radicale refuses those), and answers a PUT without an ETag header, as iCloud
// may. It speaks just enough CalDAV for the box: the two REPORTs, GET, PUT,
// DELETE, with If-Match and If-None-Match.
type fakeICloud struct {
	mu      sync.Mutex
	objects map[string]string // path -> text
	srv     *httptest.Server
}

func newFakeICloud(t *testing.T) *fakeICloud {
	f := &fakeICloud{objects: map[string]string{}}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	return f
}

func etagOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

func (f *fakeICloud) set(path, text string) { f.mu.Lock(); f.objects[path] = text; f.mu.Unlock() }
func (f *fakeICloud) get(path string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.objects[path]
}

func (f *fakeICloud) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case "REPORT":
		body, _ := io.ReadAll(r.Body)
		multiget := strings.Contains(string(body), "calendar-multiget")
		var b strings.Builder
		b.WriteString(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">`)
		for p, text := range f.objects {
			b.WriteString("<d:response><d:href>" + p + "</d:href><d:propstat><d:prop><d:getetag>" + etagOf(text) + "</d:getetag>")
			if multiget {
				b.WriteString("<c:calendar-data>")
				_ = xml.EscapeText(&b, []byte(text))
				b.WriteString("</c:calendar-data>")
			}
			b.WriteString("</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>")
		}
		b.WriteString("</d:multistatus>")
		w.WriteHeader(http.StatusMultiStatus)
		io.WriteString(w, b.String())
	case http.MethodGet:
		text, ok := f.objects[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", etagOf(text))
		io.WriteString(w, text)
	case http.MethodPut:
		text, exists := f.objects[r.URL.Path]
		if (r.Header.Get("If-None-Match") == "*" && exists) || (r.Header.Get("If-Match") != "" && (!exists || r.Header.Get("If-Match") != etagOf(text))) {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.objects[r.URL.Path] = string(body)
		w.WriteHeader(http.StatusCreated) // no ETag header
	case http.MethodDelete:
		text, exists := f.objects[r.URL.Path]
		switch {
		case !exists:
			http.NotFound(w, r)
		case r.Header.Get("If-Match") != "" && r.Header.Get("If-Match") != etagOf(text):
			w.WriteHeader(http.StatusPreconditionFailed)
		default:
			delete(f.objects, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		}
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// iCloudWithDuplicates holds what iCloud keeps for old events: STATUS twice.
const iCloudWithDuplicates = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Apple Inc.//iOS 18//EN\r\n" +
	"BEGIN:VEVENT\r\nUID:old@icloud\r\nDTSTAMP:20250101T080000Z\r\nLAST-MODIFIED:20250101T080000Z\r\n" +
	"DTSTART;TZID=Europe/Berlin:20261120T150000\r\nDTEND;TZID=Europe/Berlin:20261120T160000\r\n" +
	"SUMMARY:Dentist\r\nSTATUS:CONFIRMED\r\nSTATUS:CONFIRMED\r\nMETHOD:PUBLISH\r\nMETHOD:PUBLISH\r\n" +
	"ATTENDEE;CN=Papa:mailto:papa@example.org\r\n" +
	"BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT15M\r\nDESCRIPTION:Reminder\r\nEND:VALARM\r\n" +
	"END:VEVENT\r\nEND:VCALENDAR\r\n"

func TestWriteBackToAnICloudShapedServerKeepsRepeatedLinesAndNeedsNoETagInTheAnswer(t *testing.T) {
	srv := newFakeICloud(t)
	srv.set("/cal/old.ics", iCloudWithDuplicates)

	db, err := store.Open(filepath.Join(t.TempDir(), "hejmo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	keys, _ := secret.Open(filepath.Join(t.TempDir(), "secret.key"))
	db.SetSecrets(keys)
	h, _ := db.CreateHousehold(store.Household{Name: "Home", Region: "DE-BY", Timezone: "Europe/Berlin"})
	clock := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	conn, err := db.CreateCalDAVConnection(h.ID, store.NewCalDAVConnection{URL: srv.srv.URL, Username: "mama", Password: "pw", Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	cal, err := db.AddCalDAVCalendar(h.ID, conn.ID, store.NewCalDAVCalendar{Name: "Home", RemoteURL: srv.srv.URL + "/cal/", IntervalMinutes: 5, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	svc := calsync.New(db, nil, func() time.Time { return clock })
	ctx := context.Background()
	if err := svc.Sync(ctx, h.ID, cal.ID); err != nil {
		t.Fatal(err)
	}

	evs, _ := db.CalendarEvents(h.ID, cal.ID)
	if len(evs) != 1 || evs[0].Title != "Dentist" {
		t.Fatalf("events = %+v", evs)
	}
	e := evs[0]
	e.Title = "Dentist (check-up)"
	if err := db.ApplyChange(h.ID, calendar.Change{Update: []calendar.Event{e}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Push(ctx, h.ID, cal.ID); err != nil {
		t.Fatal(err)
	}
	got := srv.get("/cal/old.ics")
	for _, line := range []string{"STATUS:CONFIRMED\r\nSTATUS:CONFIRMED\r\nMETHOD:PUBLISH\r\nMETHOD:PUBLISH", "ATTENDEE;CN=Papa:mailto:papa@example.org", "BEGIN:VALARM", "SUMMARY:Dentist (check-up)"} {
		if !strings.Contains(got, line) {
			t.Errorf("lost %q:\n%s", line, got)
		}
	}

	// The server gave no ETag for the write; the box asked for it, so the next
	// sync has nothing to download and nothing is queued.
	if pend, _ := db.PendingObjects(cal.ID); len(pend) != 0 {
		t.Fatalf("pending = %+v", pend)
	}
	known, _ := db.RemoteObjects(cal.ID)
	if known[srv.srv.URL+"/cal/old.ics"] != strings.Trim(etagOf(got), `"`) {
		t.Fatalf("known = %v, want the server's ETag of the new text", known)
	}
}
