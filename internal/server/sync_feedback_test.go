package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 5.17: sync feedback on the calendars page (ADR 0017).

const feedURL172 = "https://example.org/school.ics"

// icsBox signs in and subscribes to a feed with two events; the box's clock
// stands at 21:46:26 local time (20:46:26 UTC, Europe/Berlin in November).
func icsBox(t *testing.T) (*box, store.SyncedCalendar) {
	t.Helper()
	b := signedInAPI(t).b
	b.now = time.Date(2026, 11, 14, 20, 46, 26, 0, time.UTC)
	b.feeds.set(feedURL172, feedOf("UID:a\r\nSUMMARY:Choir\r\nDTSTART:20261120T170000Z", "UID:b\r\nSUMMARY:Fair\r\nDTSTART:20261121T100000Z"), nil)
	if r := b.post("/calendars", url.Values{"name": {"School"}, "url": {feedURL172}, "interval": {"60"}}); r.status != http.StatusSeeOther {
		t.Fatalf("add = %d\n%s", r.status, r.body)
	}
	h, _ := b.db.HomeHousehold()
	cals, _ := b.db.SyncedCalendars(h.ID)
	return b, cals[0]
}

func TestSyncNowShowsASuccessLineWithTimeEventCountAndChanges(t *testing.T) {
	b, cal := icsBox(t)
	b.now = b.now.Add(time.Hour) // 22:46:26 local
	b.feeds.set(feedURL172, feedOf("UID:a\r\nSUMMARY:Choir rehearsal\r\nDTSTART:20261120T170000Z", "UID:c\r\nSUMMARY:Swim\r\nDTSTART:20261122T090000Z", "UID:d\r\nSUMMARY:Piano\r\nDTSTART:20261123T090000Z"), nil)

	if r := b.post("/calendars/"+cal.ID+"/sync", url.Values{}); r.status != http.StatusSeeOther || r.location != "/calendars" {
		t.Fatalf("sync now = %d %q", r.status, r.location)
	}
	page := getEN(b, "/calendars")
	want := "Updated at 22:46:26. Events: 3 (new 2, changed 1, removed 1)."
	if got := successLine(page.body); got != want {
		t.Errorf("success line = %q, want %q", got, want)
	}
	if again := getEN(b, "/calendars"); successLine(again.body) != "" {
		t.Error("the success line is shown twice")
	}
	de := b.do(http.MethodPost, "/calendars/"+cal.ID+"/sync", url.Values{}, map[string]string{"Accept-Language": "de"})
	if de.status != http.StatusSeeOther {
		t.Fatal(de.status)
	}
	if got := successLine(b.do(http.MethodGet, "/calendars", nil, map[string]string{"Accept-Language": "de"}).body); got != "Aktualisiert um 22:46:26. Termine: 3 (neu 0, geändert 0, entfernt 0)." {
		t.Errorf("German success line = %q", got)
	}
}

func TestAFailedSyncNowShowsNoSuccessLine(t *testing.T) {
	b, cal := icsBox(t)
	getEN(b, "/calendars") // the line of adding the calendar is shown and gone
	b.feeds.set(feedURL172, "", http.ErrHandlerTimeout)
	b.post("/calendars/"+cal.ID+"/sync", url.Values{})
	if got := successLine(getEN(b, "/calendars").body); got != "" {
		t.Errorf("success line after a failure: %q", got)
	}
}

func TestCalendarRowShowsLastSuccessEventCountAndLastRunChanges(t *testing.T) {
	b, _ := icsBox(t)
	page := getEN(b, "/calendars").body
	for _, want := range []string{"14/11/2026 21:46:26", "Events: 2", "Last run: 2 new, 0 changed, 0 removed"} {
		if !strings.Contains(page, want) {
			t.Errorf("calendars page lacks %q", want)
		}
	}
	de := b.do(http.MethodGet, "/calendars", nil, map[string]string{"Accept-Language": "de"}).body
	for _, want := range []string{"14/11/2026 21:46:26", "Termine: 2", "Letzter Abgleich: 2 neu, 0 geändert, 0 entfernt"} {
		if !strings.Contains(de, want) {
			t.Errorf("German calendars page lacks %q", want)
		}
	}
}

func TestSyncTimesOfTheCalendarsPageAndTheSyncProblemsShowSeconds(t *testing.T) {
	b, cal := icsBox(t)
	h, _ := b.db.HomeHousehold()
	at := b.now
	if err := b.db.FailSync(h.ID, cal.ID, i18n.Msg("sync.fail.status", "status", "503"), at, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	page := getEN(b, "/calendars").body
	log := page[strings.Index(page, `id="log"`):]
	if !strings.Contains(page, "21:46:26") || !strings.Contains(log, "14/11/2026 21:46:26") {
		t.Errorf("the page and the problem log must show 21:46:26:\n%s", log)
	}

	if err := b.db.UpdateHouseholdSettings(h.ID, store.Time12h, store.Celsius, store.UpdateOn); err != nil {
		t.Fatal(err)
	}
	page = getEN(b, "/calendars").body
	log = page[strings.Index(page, `id="log"`):]
	if !strings.Contains(page, "9:46:26 PM") || !strings.Contains(log, "9:46:26 PM") {
		t.Errorf("the 12-hour format must show 9:46:26 PM:\n%s", log)
	}
}

func TestSyncLogIsCalledSyncProblemsAndSaysWhereSuccessfulRunsShow(t *testing.T) {
	b, _ := icsBox(t)
	page := getEN(b, "/calendars").body
	log := page[strings.Index(page, `id="log"`):]
	if !strings.Contains(log, ">Sync problems<") || strings.Contains(page, "Sync log") {
		t.Errorf("heading: %s", log)
	}
	if !strings.Contains(log, "Successful runs show for each calendar") || !strings.Contains(log, `href="#synced"`) {
		t.Errorf("the log does not say where successful runs show:\n%s", log)
	}
	de := b.do(http.MethodGet, "/calendars", nil, map[string]string{"Accept-Language": "de"}).body
	if !strings.Contains(de, ">Synchronisationsprobleme<") || strings.Contains(de, "Sync-Protokoll") {
		t.Errorf("German heading missing")
	}
}

func TestAPIReportsEventCountAndLastRunOfASyncedCalendar(t *testing.T) {
	c := signedInAPI(t)
	b := c.b
	b.feeds.set(feedURL172, feedOf("UID:a\r\nSUMMARY:Choir\r\nDTSTART:20261120T170000Z"), nil)
	cal := c.addICS(t, map[string]any{"kind": "ics", "name": "School", "url": feedURL172})
	sync := cal["sync"].(map[string]any)
	run, _ := sync["last_run"].(map[string]any)
	if sync["event_count"] != float64(1) || run["new"] != float64(1) || run["changed"] != float64(0) || run["removed"] != float64(0) {
		t.Fatalf("sync = %v", sync)
	}
}

func TestAPIChangesNameColourAndAudienceOfASyncedCalendarAndNothingElse(t *testing.T) {
	c := signedInAPI(t)
	b := c.b
	b.feeds.set(feedURL172, feedOf("UID:a\r\nSUMMARY:Choir\r\nDTSTART:20261120T170000Z"), nil)
	cal := c.addICS(t, map[string]any{"kind": "ics", "name": "School", "url": feedURL172})
	id := cal["id"].(string)
	h := mustHousehold(t, c)
	mia, _ := b.db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})

	status, got, _ := c.call(http.MethodPatch, "/calendars/"+id, map[string]any{"name": "Club", "color": "#4E7D52", "audience": map[string]any{"people": []string{mia.ID}, "groups": []string{}}})
	if status != http.StatusOK || got["name"] != "Club" || got["color"] != "#4E7D52" || len(got["audience"].(map[string]any)["people"].([]any)) != 1 {
		t.Fatalf("patch = %d %v", status, got)
	}
	for name, body := range map[string]map[string]any{
		"nothing":    {},
		"blank name": {"name": " "},
		"bad colour": {"color": "red"},
		"no colour":  {"color": ""},
		"unknown":    {"audience": map[string]any{"people": []string{"00000000-0000-4000-8000-000000000000"}, "groups": []string{}}},
	} {
		if status, _, _ := c.call(http.MethodPatch, "/calendars/"+id, body); status != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want 422", name, status)
		}
	}
	if status, _, _ := c.call(http.MethodPatch, "/calendars/00000000-0000-4000-8000-000000000000", map[string]any{"name": "Nobody's"}); status != http.StatusNotFound {
		t.Errorf("renaming an unknown calendar = %d, want 404", status)
	}
	family, _ := b.db.FamilyCalendar(h.ID)
	if status, _, _ := c.call(http.MethodPatch, "/calendars/"+family.ID, map[string]any{"name": "Mine"}); status != http.StatusUnprocessableEntity {
		t.Errorf("renaming the family calendar = %d, want 422", status)
	}
	ro := c.withToken(c.newToken("read"))
	if status, _, _ := ro.call(http.MethodPatch, "/calendars/"+id, map[string]any{"name": "X"}); status != http.StatusForbidden {
		t.Errorf("read token = %d, want 403", status)
	}
}
