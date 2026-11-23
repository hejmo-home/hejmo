package server_test

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/caldavclient/caldavtest"
	"github.com/hejmo-home/hejmo/internal/i18n"
)

// twoWayCalendar connects Radicale and ticks its "Family" calendar.
func twoWayCalendar(t *testing.T) (*apiClient, *caldavtest.Server, string, string) {
	t.Helper()
	c, srv := radicaleBox(t)
	remote := srv.MakeCalendar(t, "family", "Family")
	conn := c.connect(t, davCreds(srv))
	pick := calendarNamed(t, conn, "Family")
	status, cal, _ := c.call(http.MethodPost, "/calendar-connections/"+conn["id"].(string)+"/calendars",
		map[string]any{"url": pick["url"], "name": "Family", "sync_interval_minutes": 5})
	if status != http.StatusCreated {
		t.Fatalf("add calendar = %d %v", status, cal)
	}
	return c, srv, remote, cal["id"].(string)
}

func TestACalDAVCalendarIsTwoWayAndEventsEditedOnTheBoxReachTheServer(t *testing.T) {
	t.Parallel()
	c, srv, remote, calID := twoWayCalendar(t)
	_, cal, _ := c.call(http.MethodGet, "/calendars", nil)
	for _, x := range cal {
		if m := x.(map[string]any); m["id"] == calID && m["read_only"] != false {
			t.Fatalf("calendar = %v, want two-way", m)
		}
	}
	status, ev, _ := c.call(http.MethodPost, "/events", map[string]any{"calendar_id": calID, "title": "Piano", "start": "2026-11-21T15:00:00+01:00", "end": "2026-11-21T16:00:00+01:00"})
	if status != http.StatusCreated {
		t.Fatalf("create = %d %v", status, ev)
	}
	c.b.sync.Wait()
	id := ev["id"].(string)
	if got := srv.Get(t, remote+id+".ics"); !strings.Contains(got, "SUMMARY:Piano") {
		t.Fatalf("created event outside = %q", got)
	}
	if status, body, _ := c.call(http.MethodPatch, "/events/"+id, map[string]any{"title": "Piano lesson"}); status != http.StatusNoContent {
		t.Fatalf("edit = %d %v", status, body)
	}
	c.b.sync.Wait()
	if got := srv.Get(t, remote+id+".ics"); !strings.Contains(got, "SUMMARY:Piano lesson") {
		t.Fatalf("edited event outside = %q", got)
	}
	if status, _, _ := c.call(http.MethodDelete, "/events/"+id, nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
	c.b.sync.Wait()
	if got := srv.Get(t, remote+id+".ics"); got != "" {
		t.Fatalf("deleted event still outside: %q", got)
	}
}

func TestAReadOnlyCalDAVCalendarRefusesEditsAndTwoWayAllowsThemAgain(t *testing.T) {
	t.Parallel()
	c, _, _, calID := twoWayCalendar(t)
	event := map[string]any{"calendar_id": calID, "title": "Piano", "start": "2026-11-21T15:00:00+01:00", "end": "2026-11-21T16:00:00+01:00"}

	status, cal, _ := c.call(http.MethodPatch, "/calendars/"+calID, map[string]any{"read_only": true})
	if status != http.StatusOK || cal["read_only"] != true {
		t.Fatalf("switch to read-only = %d %v", status, cal)
	}
	if status, _, _ := c.call(http.MethodPost, "/events", event); status != http.StatusUnprocessableEntity {
		t.Fatalf("create in a read-only calendar = %d, want 422", status)
	}
	status, cal, _ = c.call(http.MethodPatch, "/calendars/"+calID, map[string]any{"read_only": false})
	if status != http.StatusOK || cal["read_only"] != false {
		t.Fatalf("switch back = %d %v", status, cal)
	}
	if status, _, _ := c.call(http.MethodPost, "/events", event); status != http.StatusCreated {
		t.Fatalf("create after switching back = %d", status)
	}
	c.b.sync.Wait()
}

func TestOnlyCalDAVCalendarsHaveTheReadOnlySwitch(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	c.b.feeds.set(schoolFeed, feedOf(), nil)
	ics := c.addICS(t, map[string]any{"kind": "ics", "name": "School", "url": schoolFeed})
	if status, _, _ := c.call(http.MethodPatch, "/calendars/"+ics["id"].(string), map[string]any{"read_only": false}); status != http.StatusUnprocessableEntity {
		t.Fatalf("ICS switch = %d, want 422", status)
	}
	if status, _, _ := c.call(http.MethodPatch, "/calendars/"+ics["id"].(string), map[string]any{}); status != http.StatusUnprocessableEntity {
		t.Fatalf("empty patch = %d, want 422", status)
	}
}

func TestConnectionPageSwitchesTwoWayAndReadOnly(t *testing.T) {
	t.Parallel()
	c, _, _, calID := twoWayCalendar(t)
	synced, err := c.b.db.SyncedCalendar(mustHousehold(t, c).ID, calID)
	if err != nil {
		t.Fatal(err)
	}
	connID := synced.ConnectionID
	if connID == "" {
		t.Fatal("calendar has no connection")
	}
	page := "/connections/" + connID
	for lang, want := range map[string]string{"en": "Make read-only", "de": "Nur lesbar machen"} {
		r := c.b.do(http.MethodGet, page, nil, map[string]string{"Accept-Language": lang})
		if !strings.Contains(r.body, want) {
			t.Fatalf("%s: no %q on the page:\n%s", lang, want, r.body)
		}
	}
	// The calendars page no longer carries the switch (ADR 0017).
	if r := c.b.do(http.MethodGet, "/calendars", nil, map[string]string{"Accept-Language": "en"}); strings.Contains(r.body, "Make read-only") {
		t.Fatalf("switch still on the calendars page:\n%s", r.body)
	}
	r := c.b.post("/calendars/"+calID+"/read-only", url.Values{"read_only": {"1"}})
	if r.status != http.StatusSeeOther || r.location != page {
		t.Fatalf("switch = %d %q, want 303 to %s", r.status, r.location, page)
	}
	if cal, _ := c.b.db.Calendar(mustHousehold(t, c).ID, calID); !cal.ReadOnly {
		t.Fatal("calendar not read-only after the form")
	}
	body := c.b.do(http.MethodGet, page, nil, map[string]string{"Accept-Language": "en"}).body
	if !strings.Contains(body, "Allow changes on the box") {
		t.Fatalf("no way back on the page:\n%s", body)
	}
	if r := c.b.post("/calendars/"+calID+"/read-only", url.Values{"read_only": {"0"}}); r.status != http.StatusSeeOther || r.location != page {
		t.Fatalf("switch back = %d %q, want 303 to %s", r.status, r.location, page)
	}
	if cal, _ := c.b.db.Calendar(mustHousehold(t, c).ID, calID); cal.ReadOnly {
		t.Fatal("calendar still read-only")
	}
	body = c.b.do(http.MethodGet, page, nil, map[string]string{"Accept-Language": "en"}).body
	if !strings.Contains(body, "Make read-only") || !strings.Contains(body, "Two-way") || !strings.Contains(body, "kept in the sync log for 30 days") {
		t.Fatalf("button, hint or two-way label missing after switching back:\n%s", body)
	}
	if r := c.b.post("/calendars/00000000-0000-4000-8000-000000000000/read-only", url.Values{"read_only": {"1"}}); r.status != http.StatusNotFound {
		t.Fatalf("unknown calendar = %d", r.status)
	}
}

func TestEventsWrittenOnTheBoxCountInTheAPIsEventCount(t *testing.T) {
	t.Parallel()
	c, _, _, calID := twoWayCalendar(t)
	for i, day := range []string{"21", "22"} {
		status, ev, _ := c.call(http.MethodPost, "/events", map[string]any{"calendar_id": calID, "title": "Piano " + strconv.Itoa(i), "start": "2026-11-" + day + "T15:00:00+01:00", "end": "2026-11-" + day + "T16:00:00+01:00"})
		if status != http.StatusCreated {
			t.Fatalf("create = %d %v", status, ev)
		}
	}
	c.b.sync.Wait()
	sync, _ := c.calendarJSON(t, calID)["sync"].(map[string]any)
	if sync["event_count"] != float64(2) {
		t.Fatalf("sync = %v, want event_count 2 (written on the box)", sync)
	}
}

func TestSyncLogShowsTheVersionThatLostAConflict(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	h := mustHousehold(t, c)
	c.b.feeds.set(schoolFeed, feedOf(), nil)
	cal := c.addICS(t, map[string]any{"kind": "ics", "name": "School", "url": schoolFeed})
	if err := c.b.db.LogConflict(h.ID, cal["id"].(string), msgOf("sync.conflict.box_won", "title", "Piano"), "BEGIN:VCALENDAR lost", c.b.now); err != nil {
		t.Fatal(err)
	}
	_, _, log := c.call(http.MethodGet, "/sync-log", nil)
	if len(log) != 1 {
		t.Fatalf("log = %v", log)
	}
	e := log[0].(map[string]any)
	if e["kind"] != "conflict" || e["detail"] != "BEGIN:VCALENDAR lost" || !strings.Contains(e["message"].(string), "Piano") {
		t.Fatalf("entry = %v", e)
	}
	page := c.b.do(http.MethodGet, "/calendars", nil, map[string]string{"Accept-Language": "en"})
	if !strings.Contains(page.body, "Version that lost") || !strings.Contains(page.body, "BEGIN:VCALENDAR lost") {
		t.Fatalf("page lacks the losing version:\n%s", page.body)
	}
}

func msgOf(key string, kv ...string) i18n.Message { return i18n.Msg(key, kv...) }

func TestAnEventSavedOnAPhoneInASharedTwoWayCalendarReachesTheServer(t *testing.T) {
	t.Parallel()
	c, srv := radicaleBox(t)
	remote := srv.MakeCalendar(t, "shared", "Shared")
	conn := c.connect(t, davCreds(srv))
	pick := calendarNamed(t, conn, "Shared")
	_, cal, _ := c.call(http.MethodPost, "/calendar-connections/"+conn["id"].(string)+"/calendars", map[string]any{"url": pick["url"], "name": "Radicale shared"})
	if status, body, _ := c.call(http.MethodPatch, "/calendars/"+cal["id"].(string), map[string]any{"share_through_box": true}); status != http.StatusOK {
		t.Fatalf("share = %d %v", status, body)
	}
	ph := c.phone(t, "calendars")
	box := ph.calendarNamed("Radicale shared")

	status, _, body := ph.raw("PUT", box.Path+"dentist.ics", eventICS("dentist", "Dentist", "20261120T090000Z", "20261120T100000Z"), map[string]string{"Content-Type": icsType, "If-None-Match": "*"})
	if status != http.StatusCreated {
		t.Fatalf("phone create = %d %s", status, body)
	}
	c.b.sync.Wait()
	if got := srv.Get(t, remote+"dentist.ics"); !strings.Contains(got, "SUMMARY:Dentist") {
		t.Fatalf("outside = %q", got)
	}
	if status, _, _ := ph.raw("DELETE", box.Path+"dentist.ics", "", nil); status != http.StatusNoContent {
		t.Fatalf("phone delete = %d", status)
	}
	c.b.sync.Wait()
	if got := srv.Get(t, remote+"dentist.ics"); got != "" {
		t.Fatalf("outside after delete = %q", got)
	}
}

// The week view's own screens (create, edit, delete) must write back to a
// two-way CalDAV calendar just as the API does.
func TestEventsMadeInTheWebScreensOfATwoWayCalendarReachTheServer(t *testing.T) {
	t.Parallel()
	c, srv, remote, calID := twoWayCalendar(t)
	b := c.b
	post := func(path string, form url.Values) {
		t.Helper()
		if r := b.post(path, form); r.status != http.StatusSeeOther {
			t.Fatalf("POST %s = %d\n%s", path, r.status, r.body)
		}
		b.sync.Wait()
	}

	form := eventForm("Piano", "2026-11-21", "15:00", "16:00")
	form.Set("calendar", calID)
	post("/calendar/events", form)

	path, occ := occurrenceOf(t, linkTo(t, getEN(b, "/calendar?week=2026-11-16").body, "Piano"))
	id := strings.TrimPrefix(path, "/calendar/events/")
	if got := srv.Get(t, remote+id+".ics"); !strings.Contains(got, "SUMMARY:Piano") {
		t.Fatalf("created in the web screen, outside = %q", got)
	}

	edit := eventForm("Piano lesson", "2026-11-21", "15:00", "16:00")
	edit.Set("calendar", calID)
	edit.Set("occurrence", occ)
	edit.Set("scope", "all")
	post(path, edit)
	if got := srv.Get(t, remote+id+".ics"); !strings.Contains(got, "SUMMARY:Piano lesson") {
		t.Fatalf("edited in the web screen, outside = %q", got)
	}

	post(path+"/delete", url.Values{"occurrence": {occ}})
	if got := srv.Get(t, remote+id+".ics"); got != "" {
		t.Fatalf("deleted in the web screen, still outside: %q", got)
	}
}

// The flash text after "Sync now" counts what the calendar holds, box writes included. (Only CalDAV
// calendars take box writes and they have no page of their own, ADR 0017; the fact on the page of a
// subscribed calendar is covered in sync_feedback_test.go.)
func TestEventsWrittenOnTheBoxCountInTheSyncedFlash(t *testing.T) {
	t.Parallel()
	c, _, _, calID := twoWayCalendar(t)
	for i, day := range []string{"21", "22"} {
		status, ev, _ := c.call(http.MethodPost, "/events", map[string]any{"calendar_id": calID, "title": "Piano " + strconv.Itoa(i), "start": "2026-11-" + day + "T15:00:00+01:00", "end": "2026-11-" + day + "T16:00:00+01:00"})
		if status != http.StatusCreated {
			t.Fatalf("create = %d %v", status, ev)
		}
	}
	c.b.sync.Wait()
	r := c.b.post("/calendars/"+calID+"/sync", url.Values{})
	if r.status != http.StatusSeeOther {
		t.Fatalf("sync now = %d\n%s", r.status, r.body)
	}
	if got := successLine(getEN(c.b, r.location).body); !strings.Contains(got, "Events: 2 ") {
		t.Errorf("flash after sync = %q, want it to count 2 events", got)
	}
}
