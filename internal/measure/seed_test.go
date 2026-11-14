package measure_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/icsfeed"
	"github.com/hejmo-home/hejmo/internal/measure"
	"github.com/hejmo-home/hejmo/internal/pki"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/store"
)

func TestFeedParsesAsICS(t *testing.T) {
	body := measure.Feed(300, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	res, err := icsfeed.Parse(body, icsfeed.Options{Zone: time.UTC, Now: time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) < 300 {
		t.Fatalf("want at least 300 events, got %d", len(res.Items))
	}
}

// The seeded box is a configured household that the REST API accepts the
// token of, with the example family and synced calendars that fetch the feed.
func TestSeededHouseholdIsServedByTheBox(t *testing.T) {
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(measure.Feed(200, time.Now()))
	}))
	defer feed.Close()

	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "hejmo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seeded, err := measure.Seed(db, measure.SeedOptions{FeedURL: feed.URL + "/cal.ics", Calendars: 4, EventsPerFeed: 200})
	if err != nil {
		t.Fatal(err)
	}
	if !db.Configured() {
		t.Fatal("seeded box must count as set up")
	}
	if len(seeded.CalendarIDs) != 4 {
		t.Fatalf("want 4 synced calendars, got %d", len(seeded.CalendarIDs))
	}
	certs, err := pki.Open(filepath.Join(dir, "tls"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.New(server.Options{DB: db, PKI: certs}))
	defer srv.Close()

	call := func(method, path string) *http.Response {
		req, _ := http.NewRequest(method, srv.URL+"/api/v1"+path, nil)
		req.Header.Set("Authorization", "Bearer "+seeded.Token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := call("GET", "/people")
	var people []map[string]any
	_ = json.NewDecoder(res.Body).Decode(&people)
	res.Body.Close()
	if res.StatusCode != 200 || len(people) != 4 {
		t.Fatalf("people: %d %v (want the example family of four)", res.StatusCode, people)
	}
	for _, id := range seeded.CalendarIDs {
		res := call("POST", "/calendars/"+id+"/sync")
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || !strings.Contains(string(b), `"state":"ok"`) {
			t.Fatalf("sync %s: %d %s", id, res.StatusCode, b)
		}
	}
	from := time.Now().UTC().Truncate(24 * time.Hour)
	res = call("GET", "/events?from="+from.Format(time.RFC3339)+"&to="+from.AddDate(0, 0, 120).Format(time.RFC3339))
	var occ []map[string]any
	_ = json.NewDecoder(res.Body).Decode(&occ)
	res.Body.Close()
	if len(occ) < 4*200 {
		t.Fatalf("after syncing, want at least 800 occurrences, got %d", len(occ))
	}
}

// The scheduler must not sync the seeded calendars on its own: the idle
// measurement needs a quiet box, and the burst is driven through the API.
func TestSeededCalendarsAreNotDueAtStart(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "hejmo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := measure.Seed(db, measure.SeedOptions{FeedURL: "http://feed.invalid/cal.ics", Calendars: 3, EventsPerFeed: 20}); err != nil {
		t.Fatal(err)
	}
	// The measurement samples idle for well under an hour after start.
	due, err := db.DueCalendars(time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("%d seeded calendars are due within the hour; the idle box would be syncing", len(due))
	}
}
