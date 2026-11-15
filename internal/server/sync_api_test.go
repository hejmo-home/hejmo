package server_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

const schoolFeed = "https://example.org/school.ics"

func feedOf(events ...string) string {
	s := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\n"
	for _, e := range events {
		s += "BEGIN:VEVENT\r\n" + e + "\r\nEND:VEVENT\r\n"
	}
	return s + "END:VCALENDAR\r\n"
}

const choirSeries = "UID:choir\r\nSUMMARY:Choir\r\nDTSTART;TZID=W. Europe Standard Time:20261102T170000\r\nDTEND;TZID=W. Europe Standard Time:20261102T180000\r\n" +
	"RRULE:FREQ=WEEKLY;COUNT=4\r\nEXDATE;TZID=W. Europe Standard Time:20261109T170000"

func (c *apiClient) addICS(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	status, cal, _ := c.call(http.MethodPost, "/calendars", body)
	if status != http.StatusCreated {
		t.Fatalf("add ICS calendar = %d %v", status, cal)
	}
	return cal
}

func (c *apiClient) syncOf(t *testing.T, id string) map[string]any {
	t.Helper()
	_, _, list := c.call(http.MethodGet, "/calendars", nil)
	for _, x := range list {
		if cal := x.(map[string]any); cal["id"] == id {
			sync, _ := cal["sync"].(map[string]any)
			return sync
		}
	}
	t.Fatalf("calendar %s not listed", id)
	return nil
}

// Acceptance: a feed with a weekly series and a cancelled occurrence shows the
// series without that occurrence; webcal:// addresses are accepted.
func TestAddingAnICSCalendarShowsItsEvents(t *testing.T) {
	c := signedInAPI(t)
	c.b.feeds.set(schoolFeed, feedOf(choirSeries), nil)

	cal := c.addICS(t, map[string]any{"kind": "ics", "name": "School", "url": "webcal://example.org/school.ics", "color": "#B4583A"})
	if cal["kind"] != "ics" || cal["read_only"] != true || cal["color"] != "#B4583A" {
		t.Fatalf("calendar = %v", cal)
	}
	if sync := cal["sync"].(map[string]any); sync["state"] != "ok" || sync["interval_minutes"] != float64(60) || sync["last_success_at"] == nil {
		t.Fatalf("sync = %v", sync)
	}
	if got := c.b.feeds.asked; len(got) != 1 || got[0] != schoolFeed {
		t.Fatalf("fetched %v, want %s", got, schoolFeed)
	}
	sameList(t, occTitles(c.occurrences(t, november)), []string{"11-02T17:00 Choir", "11-16T17:00 Choir", "11-23T17:00 Choir"})

	// Stored as Europe/Berlin, not as a Windows zone name.
	evs, _ := c.b.db.EventsBetween(mustHousehold(t, c).ID, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC))
	if len(evs) != 1 || evs[0].Start.Location().String() != "Europe/Berlin" {
		t.Fatalf("stored events = %+v", evs)
	}
}

func mustHousehold(t *testing.T, c *apiClient) store.Household {
	t.Helper()
	h, err := c.b.db.HomeHousehold()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestAddICSCalendarValidatesInput(t *testing.T) {
	c := signedInAPI(t)
	h := mustHousehold(t, c)
	for name, body := range map[string]map[string]any{
		"no name":           {"kind": "ics", "name": " ", "url": schoolFeed},
		"no url":            {"kind": "ics", "name": "School", "url": ""},
		"ftp":               {"kind": "ics", "name": "School", "url": "ftp://example.org/a.ics"},
		"bad interval":      {"kind": "ics", "name": "School", "url": schoolFeed, "sync_interval_minutes": 1},
		"unknown person":    {"kind": "ics", "name": "School", "url": schoolFeed, "audience": map[string]any{"people": []string{"00000000-0000-4000-8000-000000000000"}, "groups": []string{}}},
		"unknown group":     {"kind": "ics", "name": "School", "url": schoolFeed, "audience": map[string]any{"people": []string{}, "groups": []string{"00000000-0000-4000-8000-000000000000"}}},
		"foreign person id": {"kind": "ics", "name": "School", "url": schoolFeed, "audience": map[string]any{"people": []string{h.ID}, "groups": []string{}}},
	} {
		if status, _, _ := c.call(http.MethodPost, "/calendars", body); status != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want 422", name, status)
		}
	}
}

func TestICSCalendarKeepsColourAudienceAndInterval(t *testing.T) {
	c := signedInAPI(t)
	h := mustHousehold(t, c)
	mia, _ := c.b.db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	c.b.feeds.set(schoolFeed, feedOf(), nil)
	cal := c.addICS(t, map[string]any{"kind": "ics", "name": "Club", "url": schoolFeed, "sync_interval_minutes": 15,
		"audience": map[string]any{"people": []string{mia.ID}, "groups": []string{}}})
	if sync := c.syncOf(t, cal["id"].(string)); sync["interval_minutes"] != float64(15) {
		t.Fatalf("sync = %v", sync)
	}
	if aud := cal["audience"].(map[string]any); len(aud["people"].([]any)) != 1 {
		t.Fatalf("audience = %v", aud)
	}
}

// Acceptance: an event removed from the feed is gone after "sync now".
func TestSyncNowRemovesEventsGoneFromTheFeed(t *testing.T) {
	c := signedInAPI(t)
	c.b.feeds.set(schoolFeed, feedOf("UID:a\r\nSUMMARY:Choir\r\nDTSTART:20261120T170000", "UID:b\r\nSUMMARY:Fair\r\nDTSTART:20261121T100000"), nil)
	cal := c.addICS(t, map[string]any{"kind": "ics", "name": "School", "url": schoolFeed})
	if got := len(c.occurrences(t, november)); got != 2 {
		t.Fatalf("events = %d, want 2", got)
	}
	c.b.feeds.set(schoolFeed, feedOf("UID:a\r\nSUMMARY:Choir\r\nDTSTART:20261120T170000"), nil)
	status, body, _ := c.call(http.MethodPost, "/calendars/"+cal["id"].(string)+"/sync", nil)
	if status != http.StatusOK || body["sync"].(map[string]any)["state"] != "ok" {
		t.Fatalf("sync now = %d %v", status, body)
	}
	sameList(t, occTitles(c.occurrences(t, november)), []string{"11-20T17:00 Choir"})
}

// Acceptance: an unreachable feed keeps the previous events; the failure is
// in the sync log.
func TestFailedSyncKeepsEventsAndIsInTheSyncLog(t *testing.T) {
	c := signedInAPI(t)
	c.b.feeds.set(schoolFeed, feedOf("UID:a\r\nSUMMARY:Choir\r\nDTSTART:20261120T170000"), nil)
	cal := c.addICS(t, map[string]any{"kind": "ics", "name": "School", "url": schoolFeed})
	id := cal["id"].(string)

	c.b.feeds.set(schoolFeed, "", errors.New("dial tcp: no route to host"))
	c.b.now = c.b.now.Add(time.Hour)
	status, body, _ := c.call(http.MethodPost, "/calendars/"+id+"/sync", nil)
	if status != http.StatusOK || body["sync"].(map[string]any)["last_error"] != "Der Kalender konnte nicht abgerufen werden: dial tcp: no route to host" {
		t.Fatalf("sync now = %d %v", status, body)
	}
	if got := len(c.occurrences(t, november)); got != 1 {
		t.Fatalf("events after failure = %d, want the previous 1", got)
	}
	status, _, log := c.call(http.MethodGet, "/sync-log", nil)
	if status != http.StatusOK || len(log) != 1 {
		t.Fatalf("sync log = %d %v", status, log)
	}
	e := log[0].(map[string]any)
	if e["kind"] != "failure" || e["message"] != "Der Kalender konnte nicht abgerufen werden: dial tcp: no route to host" || e["calendar_name"] != "School" || e["calendar_id"] != id {
		t.Fatalf("entry = %v", e)
	}
}

// Acceptance: after 24 hours without success the sync state is stale and
// parents see a notice; one success clears it.
func TestStaleAfter24HoursShowsNoticeUntilNextSuccess(t *testing.T) {
	c := signedInAPI(t)
	c.b.feeds.set(schoolFeed, feedOf("UID:a\r\nSUMMARY:Choir\r\nDTSTART:20261120T170000"), nil)
	cal := c.addICS(t, map[string]any{"kind": "ics", "name": "School", "url": schoolFeed})
	id := cal["id"].(string)
	const notice = `data-sync-stale`

	if r := c.b.get("/"); strings.Contains(r.body, notice) {
		t.Fatal("notice shown while everything is fine")
	}
	c.b.feeds.set(schoolFeed, "", errors.New("gone"))
	c.b.now = c.b.now.Add(23 * time.Hour)
	c.call(http.MethodPost, "/calendars/"+id+"/sync", nil)
	if s := c.syncOf(t, id); s["state"] != "ok" {
		t.Fatalf("after 23h: %v", s)
	}
	c.b.now = c.b.now.Add(time.Hour)
	if s := c.syncOf(t, id); s["state"] != "stale" {
		t.Fatalf("after 24h: %v", s)
	}
	for _, page := range []string{"/", "/calendars"} {
		if r := c.b.get(page); !strings.Contains(r.body, notice) {
			t.Fatalf("no stale notice on %s:\n%s", page, r.body)
		}
	}

	c.b.feeds.set(schoolFeed, feedOf("UID:a\r\nSUMMARY:Choir\r\nDTSTART:20261120T170000"), nil)
	c.call(http.MethodPost, "/calendars/"+id+"/sync", nil)
	if s := c.syncOf(t, id); s["state"] != "ok" || s["last_error"] != "" {
		t.Fatalf("after success: %v", s)
	}
	if r := c.b.get("/"); strings.Contains(r.body, notice) {
		t.Fatal("notice still shown after a success")
	}
}

func TestRemovingACalendarRemovesItsEvents(t *testing.T) {
	c := signedInAPI(t)
	c.b.feeds.set(schoolFeed, feedOf("UID:a\r\nSUMMARY:Choir\r\nDTSTART:20261120T170000"), nil)
	cal := c.addICS(t, map[string]any{"kind": "ics", "name": "School", "url": schoolFeed})
	c.createEvent(t, map[string]any{"title": "Own", "start": "2026-11-05T18:00:00+01:00", "end": "2026-11-05T19:00:00+01:00"})

	if status, _, _ := c.call(http.MethodDelete, "/calendars/"+cal["id"].(string), nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
	sameList(t, occTitles(c.occurrences(t, november)), []string{"11-05T18:00 Own"})
	if status, _, _ := c.call(http.MethodDelete, "/calendars/"+cal["id"].(string), nil); status != http.StatusNotFound {
		t.Fatalf("delete again = %d, want 404", status)
	}
	_, _, cals := c.call(http.MethodGet, "/calendars", nil)
	if len(cals) != 1 || cals[0].(map[string]any)["kind"] != "local" {
		t.Fatalf("calendars = %v", cals)
	}
	family := cals[0].(map[string]any)["id"].(string)
	if status, _, _ := c.call(http.MethodDelete, "/calendars/"+family, nil); status != http.StatusNotFound {
		t.Fatalf("deleting the family calendar = %d, want 404", status)
	}
	if status, _, _ := c.call(http.MethodPost, "/calendars/"+family+"/sync", nil); status != http.StatusNotFound {
		t.Fatalf("syncing the family calendar = %d, want 404", status)
	}
}

func TestEventsOfAReadOnlyCalendarCannotBeCreatedOrEdited(t *testing.T) {
	c := signedInAPI(t)
	c.b.feeds.set(schoolFeed, feedOf("UID:a\r\nSUMMARY:Choir\r\nDTSTART:20261120T170000"), nil)
	cal := c.addICS(t, map[string]any{"kind": "ics", "name": "School", "url": schoolFeed})
	if status, _, _ := c.call(http.MethodPost, "/events", map[string]any{"calendar_id": cal["id"], "title": "Mine", "start": "2026-11-05T18:00:00+01:00", "end": "2026-11-05T19:00:00+01:00"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("create in read-only calendar = %d, want 422", status)
	}
	ev := c.occurrences(t, november)[0]
	if status, _, _ := c.call(http.MethodPatch, "/events/"+ev["event_id"].(string)+"?scope=all", map[string]any{"title": "Changed"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("edit synced event = %d, want 422", status)
	}
	if status, _, _ := c.call(http.MethodDelete, "/events/"+ev["event_id"].(string)+"?scope=all", nil); status != http.StatusUnprocessableEntity {
		t.Fatalf("delete synced event = %d, want 422", status)
	}
}

func TestSyncEndpointsNeedTheRightRights(t *testing.T) {
	c := signedInAPI(t)
	c.b.feeds.set(schoolFeed, feedOf(), nil)
	cal := c.addICS(t, map[string]any{"kind": "ics", "name": "School", "url": schoolFeed})
	id := cal["id"].(string)

	ro := c.withToken(c.newToken("read"))
	if status, _, _ := ro.call(http.MethodPost, "/calendars", map[string]any{"kind": "ics", "name": "X", "url": schoolFeed}); status != http.StatusForbidden {
		t.Fatalf("read token adds calendar = %d, want 403", status)
	}
	if status, _, _ := ro.call(http.MethodPost, "/calendars/"+id+"/sync", nil); status != http.StatusForbidden {
		t.Fatalf("read token syncs = %d, want 403", status)
	}
	if status, _, _ := ro.call(http.MethodDelete, "/calendars/"+id, nil); status != http.StatusForbidden {
		t.Fatalf("read token deletes = %d, want 403", status)
	}
	// The read token may see the calendars and their sync state (displays need it) ...
	if s := ro.syncOf(t, id); s["state"] != "ok" {
		t.Fatalf("read token sees sync state %v", s)
	}
	// ... but the log belongs to parents.
	evs := c.withToken(c.newToken("events:read"))
	if status, _, _ := evs.call(http.MethodGet, "/sync-log", nil); status != http.StatusForbidden {
		t.Fatalf("events:read token reads the log = %d, want 403", status)
	}
	child := c.withToken(childToken(t, c.b.db))
	if status, _, _ := child.call(http.MethodGet, "/sync-log", nil); status != http.StatusForbidden {
		t.Fatalf("child reads the log = %d, want 403", status)
	}
}

func TestWebAddsAndListsICSCalendars(t *testing.T) {
	c := signedInAPI(t)
	b := c.b
	h := mustHousehold(t, c)
	mia, _ := b.db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	b.feeds.set(schoolFeed, feedOf("UID:a\r\nSUMMARY:Choir\r\nDTSTART:20261120T170000"), nil)

	r := b.post("/calendars", url.Values{"name": {"School"}, "url": {"webcal://example.org/school.ics"}, "color": {"#B4583A"}, "people": {mia.ID}, "interval": {"60"}})
	if r.status != http.StatusSeeOther || r.location != "/calendars" {
		t.Fatalf("add = %d %q", r.status, r.location)
	}
	page := b.do(http.MethodGet, "/calendars", nil, map[string]string{"Accept-Language": "en"})
	for _, want := range []string{"School", "Mia"} {
		if page.status != http.StatusOK || !strings.Contains(page.body, want) {
			t.Fatalf("calendars page misses %q:\n%s", want, page.body)
		}
	}
	if got := len(c.occurrences(t, november)); got != 1 {
		t.Fatalf("events = %d, want 1", got)
	}
	// The colour is on the calendar's own page (the overview rows are calm and carry none).
	added, _ := b.db.SyncedCalendars(h.ID)
	if detail := b.do(http.MethodGet, "/calendars/"+added[0].ID, nil, map[string]string{"Accept-Language": "en"}); !strings.Contains(detail.body, "#B4583A") {
		t.Fatalf("calendar page misses the colour:\n%s", detail.body)
	}

	// A failure shows in the log on the page, in the viewer's language.
	cal, _ := b.db.SyncedCalendars(h.ID)
	b.feeds.set(schoolFeed, "", errors.New("timeout"))
	if r := b.post("/calendars/"+cal[0].ID+"/sync", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("sync now = %d", r.status)
	}
	if page := b.get("/calendars"); !strings.Contains(page.body, "Der Kalender konnte nicht abgerufen werden: timeout") {
		t.Fatalf("log not on page:\n%s", page.body)
	}
	if page := b.do(http.MethodGet, "/calendars", nil, map[string]string{"Accept-Language": "en"}); !strings.Contains(page.body, "The calendar could not be fetched: timeout") {
		t.Fatalf("log not in English on page:\n%s", page.body)
	}

	// A bad address is explained, not stored.
	r = b.post("/calendars", url.Values{"name": {"Broken"}, "url": {"ftp://x"}, "interval": {"60"}})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `role="alert"`) {
		t.Fatalf("bad url = %d\n%s", r.status, r.body)
	}

	if r := b.post("/calendars/"+cal[0].ID+"/delete", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("delete = %d", r.status)
	}
	if got := len(c.occurrences(t, november)); got != 0 {
		t.Fatalf("events after removal = %d", got)
	}
}

func TestCalendarsPageNeedsASignedInParent(t *testing.T) {
	b, _ := configuredBox(t)
	if r := b.get("/calendars"); r.status != http.StatusSeeOther || r.location != "/login?next=%2Fcalendars" {
		t.Fatalf("signed out = %d %q", r.status, r.location)
	}
	if r := b.post("/calendars", url.Values{"name": {"x"}, "url": {schoolFeed}}); r.status != http.StatusSeeOther || r.location != "/login" {
		t.Fatalf("signed out post = %d %q", r.status, r.location)
	}
}

func TestUntitledFeedEventsGetALocalisedPlaceholder(t *testing.T) {
	c := signedInAPI(t)
	c.b.feeds.set(schoolFeed, feedOf("UID:a\r\nDTSTART:20261120T170000"), nil)
	c.addICS(t, map[string]any{"kind": "ics", "name": "School", "url": schoolFeed})
	if got := occTitles(c.occurrences(t, november)); len(got) != 1 || !strings.HasSuffix(got[0], "(ohne Titel)") {
		t.Fatalf("titles = %v", got)
	}
}

func TestICSColorMustBeHexRGB(t *testing.T) {
	c := signedInAPI(t)
	c.b.feeds.set(schoolFeed, feedOf(), nil)
	for _, bad := range []string{"red", "#12345", "#GGGGGG", "javascript:1"} {
		status, _, _ := c.call(http.MethodPost, "/calendars", map[string]any{"kind": "ics", "name": "School", "url": schoolFeed, "color": bad})
		if status != http.StatusUnprocessableEntity {
			t.Errorf("colour %q = %d, want 422", bad, status)
		}
	}
}
