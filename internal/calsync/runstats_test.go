package calsync_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldavclient/caldavtest"
	"github.com/hejmo-home/hejmo/internal/store"
)

// runLines returns the "calendar sync" log lines written so far.
func runLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if m["msg"] == "calendar sync" {
			out = append(out, m)
		}
	}
	return out
}

func wantLine(t *testing.T, line map[string]any, want map[string]float64, result string) {
	t.Helper()
	if line["level"] != "INFO" || line["result"] != result || line["calendar"] == "" {
		t.Errorf("line = %v, want an INFO line with result %q and the calendar id", line, result)
	}
	for k, v := range want {
		if line[k] != v {
			t.Errorf("%s = %v, want %v (line %v)", k, line[k], v, line)
		}
	}
	if _, ok := line["duration"]; !ok {
		t.Errorf("no duration in %v", line)
	}
}

func TestEverySyncRunStoresItsNumbersAndWritesOneContentFreeLogLine(t *testing.T) {
	fx := newFixture(t)
	var buf bytes.Buffer
	fx.svc.SetLogger(slog.New(slog.NewJSONHandler(&buf, nil)))
	ctx := context.Background()

	fx.f.feeds[feedURL] = ics("UID:a\r\nSUMMARY:Choir\r\nDESCRIPTION:Secret plans\r\nDTSTART:20261120T170000", "UID:b\r\nSUMMARY:Fair\r\nDTSTART:20261121T100000", "SUMMARY:No uid but no start")
	if err := fx.svc.Sync(ctx, fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	if got := fx.state(t).Sync.LastRun; got != (store.SyncRun{Events: 2, New: 2}) {
		t.Errorf("stored run = %+v", got)
	}

	fx.clock = fx.clock.Add(time.Hour)
	fx.f.feeds[feedURL] = ics("UID:a\r\nSUMMARY:Choir rehearsal\r\nDTSTART:20261120T170000")
	if err := fx.svc.Sync(ctx, fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
	if got := fx.state(t).Sync.LastRun; got != (store.SyncRun{Events: 1, Changed: 1, Removed: 1}) {
		t.Errorf("stored run = %+v", got)
	}

	fx.clock = fx.clock.Add(time.Hour)
	fx.f.errs[feedURL] = errors.New("Get https://example.org/school.ics: connection refused")
	if err := fx.svc.Sync(ctx, fx.h.ID, fx.cal.ID); err == nil {
		t.Fatal("want a failure")
	}
	if got := fx.state(t).Sync.LastRun; got != (store.SyncRun{Events: 1, Changed: 1, Removed: 1}) {
		t.Errorf("a failed run changed the stored numbers: %+v", got)
	}

	lines := runLines(t, &buf)
	if len(lines) != 3 {
		t.Fatalf("%d run lines, want one per run:\n%s", len(lines), buf.String())
	}
	wantLine(t, lines[0], map[string]float64{"listed": 2, "fetched": 1, "new": 2, "changed": 0, "removed": 0, "skipped": 1}, "ok")
	wantLine(t, lines[1], map[string]float64{"listed": 1, "fetched": 1, "new": 0, "changed": 1, "removed": 1, "skipped": 0}, "ok")
	wantLine(t, lines[2], map[string]float64{"new": 0, "changed": 0, "removed": 0}, "failed")
	if lines[0]["calendar"] != fx.cal.ID {
		t.Errorf("calendar = %v, want %s", lines[0]["calendar"], fx.cal.ID)
	}
	for _, leak := range []string{"Choir", "Fair", "Secret plans", "example.org", "school.ics"} {
		if strings.Contains(buf.String(), leak) {
			t.Errorf("the log contains %q:\n%s", leak, buf.String())
		}
	}
}

func TestCalDAVRunLogsListedAndFetchedObjects(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	var buf bytes.Buffer
	fx.svc.SetLogger(slog.New(slog.NewJSONHandler(&buf, nil)))
	fx.srv.Put(t, fx.remote, "a.ics", caldavtest.Event("a@test", "Swimming", "20261120T150000", "20261120T160000"))
	fx.srv.Put(t, fx.remote, "b.ics", caldavtest.Event("b@test", "Piano", "20261121T150000", "20261121T160000"))
	fx.syncAfter(t, 0)
	fx.syncAfter(t, 5*time.Minute)
	fx.srv.Put(t, fx.remote, "a.ics", caldavtest.Event("a@test", "Swimming lesson", "20261120T150000", "20261120T160000"))
	fx.syncAfter(t, 5*time.Minute)

	lines := runLines(t, &buf)
	if len(lines) != 3 {
		t.Fatalf("%d run lines:\n%s", len(lines), buf.String())
	}
	wantLine(t, lines[0], map[string]float64{"listed": 2, "fetched": 2, "new": 2}, "ok")
	wantLine(t, lines[1], map[string]float64{"listed": 2, "fetched": 0, "new": 0, "changed": 0}, "ok")
	wantLine(t, lines[2], map[string]float64{"listed": 2, "fetched": 1, "new": 0, "changed": 1}, "ok")
	got, err := fx.db.SyncedCalendar(fx.h.ID, fx.cal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := got.Sync.LastRun; got != (store.SyncRun{Events: 2, Changed: 1}) {
		t.Errorf("stored run = %+v", got)
	}
}

func TestCountEventsCountsTheImportWindowOnly(t *testing.T) {
	fx := newDAVFixture(t, caldavtest.Password)
	old := fx.srv.MakeCalendar(t, "old", "Old")
	fx.srv.Put(t, old, "2016.ics", caldavtest.Event("old@test", "School play", "20160620T150000", "20160620T160000"))
	fx.srv.Put(t, fx.remote, "a.ics", caldavtest.Event("a@test", "Swimming", "20261120T150000", "20261120T160000"))
	fx.srv.Put(t, fx.remote, "b.ics", caldavtest.Event("b@test", "Piano", "20260101T150000", "20260101T160000"))
	fx.srv.Put(t, fx.remote, "before.ics", caldavtest.Event("c@test", "Too old", "20250101T150000", "20250101T160000"))

	ctx := context.Background()
	for _, c := range []struct {
		calendar string
		want     int
	}{{old, 0}, {fx.remote, 2}} {
		got, err := fx.svc.CountEvents(ctx, fx.srv.URL, caldavtest.User, caldavtest.Password, c.calendar)
		if err != nil || got != c.want {
			t.Errorf("%s: count = %d, %v, want %d", c.calendar, got, err, c.want)
		}
	}
}

func TestAFailedScheduledRunLogsNoUrlAndNoTitle(t *testing.T) {
	fx := newFixture(t)
	var buf bytes.Buffer
	fx.svc.SetLogger(slog.New(slog.NewJSONHandler(&buf, nil)))
	fx.f.errs[feedURL] = errors.New("Get https://example.org/school.ics?token=s3cret: connection refused; event \"Choir\"")

	fx.svc.RunDue(context.Background())

	if len(runLines(t, &buf)) != 1 {
		t.Fatalf("want one run line through RunDue:\n%s", buf.String())
	}
	for _, leak := range []string{"s3cret", "example.org", "school.ics", "Choir", "https://"} {
		if strings.Contains(buf.String(), leak) {
			t.Errorf("the log contains %q:\n%s", leak, buf.String())
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if strings.Contains(line, `"level":"WARN"`) {
			t.Errorf("unexpected WARN line: %s", line)
		}
	}
}
