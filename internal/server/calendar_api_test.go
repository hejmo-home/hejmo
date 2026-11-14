package server_test

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/store"
)

func (c *apiClient) createEvent(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	status, ev, _ := c.call(http.MethodPost, "/events", body)
	if status != http.StatusCreated {
		t.Fatalf("create event = %d %v", status, ev)
	}
	return ev
}

func (c *apiClient) occurrences(t *testing.T, query string) []map[string]any {
	t.Helper()
	status, body, list := c.call(http.MethodGet, "/events?"+query, nil)
	if status != http.StatusOK {
		t.Fatalf("list events = %d %v", status, body)
	}
	out := make([]map[string]any, 0, len(list))
	for _, x := range list {
		out = append(out, x.(map[string]any))
	}
	return out
}

func occTitles(occs []map[string]any) []string {
	var out []string
	for _, o := range occs {
		start := o["start"].(string)
		out = append(out, start[5:min(16, len(start))]+" "+o["title"].(string))
	}
	return out
}

func sameList(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v\nwant %v", got, want)
		}
	}
}

const november = "from=2026-11-01T00:00:00%2B01:00&to=2026-12-01T00:00:00%2B01:00"

func TestCreateAndListEvents(t *testing.T) {
	c := signedInAPI(t)
	c.createEvent(t, map[string]any{"title": "Swimming", "start": "2026-11-02T16:00:00+01:00", "end": "2026-11-02T17:00:00+01:00", "rrule": "FREQ=WEEKLY;COUNT=3"})
	c.createEvent(t, map[string]any{"title": "Grandma visits", "all_day": true, "start": "2026-11-13", "end": "2026-11-16"})

	sameList(t, occTitles(c.occurrences(t, november)), []string{
		"11-02T16:00 Swimming", "11-09T16:00 Swimming", "11-13 Grandma visits", "11-16T16:00 Swimming",
	})
	if status, _, _ := c.call(http.MethodPost, "/events", map[string]any{"title": "Bad", "start": "2026-11-02T16:00:00+01:00", "end": "2026-11-02T15:00:00+01:00"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("end before start = %d, want 422", status)
	}
	if status, _, _ := c.call(http.MethodPost, "/events", map[string]any{"title": "Bad", "start": "2026-11-02T16:00:00+01:00", "end": "2026-11-02T17:00:00+01:00", "rrule": "FREQ=SOMETIMES"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("bad rrule = %d, want 422", status)
	}
}

// Acceptance (Calendar and sync), end to end through the API.
func TestEditThisAndFollowingThroughAPI(t *testing.T) {
	c := signedInAPI(t)
	ev := c.createEvent(t, map[string]any{"title": "Football", "start": "2026-11-02T16:00:00+01:00", "end": "2026-11-02T17:00:00+01:00", "rrule": "FREQ=WEEKLY"})

	q := "?occurrence=" + url.QueryEscape("2026-11-16T16:00:00+01:00") + "&scope=following"
	status, body, _ := c.call(http.MethodPatch, "/events/"+ev["id"].(string)+q, map[string]any{"title": "Football (new coach)", "start": "2026-11-16T17:00:00+01:00", "end": "2026-11-16T18:00:00+01:00"})
	if status != http.StatusNoContent {
		t.Fatalf("patch = %d %v", status, body)
	}
	sameList(t, occTitles(c.occurrences(t, november)), []string{
		"11-02T16:00 Football", "11-09T16:00 Football",
		"11-16T17:00 Football (new coach)", "11-23T17:00 Football (new coach)", "11-30T17:00 Football (new coach)",
	})

	q = "?occurrence=" + url.QueryEscape("2026-11-23T17:00:00+01:00") + "&scope=this"
	occs := c.occurrences(t, november)
	newSeries := occs[3]["series_id"].(string)
	if status, _, _ := c.call(http.MethodDelete, "/events/"+newSeries+q, nil); status != http.StatusNoContent {
		t.Fatalf("delete this = %d", status)
	}
	if got := len(c.occurrences(t, november)); got != 4 {
		t.Fatalf("after deleting one occurrence: %d, want 4", got)
	}
	if status, _, _ := c.call(http.MethodDelete, "/events/"+newSeries+"?occurrence=2026-11-24T17:00:00%2B01:00&scope=this", nil); status != http.StatusNotFound {
		t.Fatalf("delete unknown occurrence = %d, want 404", status)
	}
}

// Acceptance (Platform): Given an API token with "read only", when it is used
// to create an event, then the API answers 403.
func TestReadOnlyTokenCannotCreateEvent(t *testing.T) {
	c := signedInAPI(t)
	ro := c.withToken(c.newToken("read"))
	status, _, _ := ro.call(http.MethodPost, "/events", map[string]any{"title": "Nope", "start": "2026-11-02T16:00:00+01:00", "end": "2026-11-02T17:00:00+01:00"})
	if status != http.StatusForbidden {
		t.Fatalf("create event with read token = %d, want 403", status)
	}
	if got := ro.occurrences(t, november); len(got) != 0 {
		t.Fatalf("read token sees %d events", len(got))
	}
}

// Acceptance (People and permissions): Given a child profile, when it tries
// to delete an event, then this is refused.
func TestChildCannotDeleteEvent(t *testing.T) {
	c := signedInAPI(t)
	ev := c.createEvent(t, map[string]any{"title": "Dentist", "start": "2026-11-18T09:00:00+01:00", "end": "2026-11-18T10:00:00+01:00"})
	child := c.withToken(childToken(t, c.b.db))

	q := "?occurrence=" + url.QueryEscape("2026-11-18T09:00:00+01:00") + "&scope=all"
	if status, _, _ := child.call(http.MethodDelete, "/events/"+ev["id"].(string)+q, nil); status != http.StatusForbidden {
		t.Fatalf("child delete = %d, want 403", status)
	}
	if got := child.occurrences(t, november); len(got) != 1 {
		t.Fatalf("child sees %d events, want 1", len(got))
	}
}

// childToken adds Mia and a full-scope token acting for her (children have
// no password; displays and kids act through device tokens).
func childToken(t *testing.T, db *store.DB) string {
	t.Helper()
	h, _ := db.HomeHousehold()
	mia, err := db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	if err != nil {
		t.Fatal(err)
	}
	token := auth.NewToken()
	if _, err := db.CreateAPIToken(store.APIToken{Name: "Mia", Hash: auth.HashToken(token), PersonID: mia.ID, Scopes: "write"}); err != nil {
		t.Fatal(err)
	}
	return token
}

// Acceptance (Calendar and sync): Bavarian holidays, and no others.
func TestHolidaysForHomeRegion(t *testing.T) {
	c := signedInAPI(t)
	status, body, list := c.call(http.MethodGet, "/holidays?from=2026-10-01&to=2026-12-01", nil)
	if status != http.StatusOK {
		t.Fatalf("holidays = %d %v", status, body)
	}
	var got []string
	for _, x := range list {
		h := x.(map[string]any)
		got = append(got, h["date"].(string)+" "+h["name"].(string))
	}
	sameList(t, got, []string{"2026-10-03 Tag der Deutschen Einheit", "2026-11-01 Allerheiligen"})
}

func TestPersonViewHonoursSubgroups(t *testing.T) {
	c := signedInAPI(t)
	db := c.b.db
	h, _ := db.HomeHousehold()
	seb, _ := db.AddPerson(h.ID, store.Person{Name: "Sebastian", Role: store.RoleChild})
	mia, _ := db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})

	status, g, _ := c.call(http.MethodPost, "/groups", map[string]any{"name": "School children", "members": []string{seb.ID}})
	if status != http.StatusCreated {
		t.Fatalf("create group = %d %v", status, g)
	}
	school, _ := db.CreateCalendar(store.Calendar{HouseholdID: h.ID, Name: "School holidays", Kind: "ics", Audience: calendar.Audience{Groups: []string{g["id"].(string)}}})
	berlin, _ := time.LoadLocation("Europe/Berlin")
	if _, err := db.CreateEvent(h.ID, calendar.Event{CalendarID: school.ID, Title: "Autumn break", AllDay: true,
		Start: time.Date(2026, 11, 2, 0, 0, 0, 0, berlin), End: time.Date(2026, 11, 7, 0, 0, 0, 0, berlin)}); err != nil {
		t.Fatal(err)
	}
	c.createEvent(t, map[string]any{"title": "Family dinner", "start": "2026-11-05T18:00:00+01:00", "end": "2026-11-05T19:00:00+01:00"})

	titles := func(person string) []string {
		var out []string
		for _, o := range c.occurrences(t, november+"&person="+person) {
			out = append(out, o["title"].(string))
		}
		return out
	}
	sameList(t, titles(seb.ID), []string{"Autumn break", "Family dinner"})
	sameList(t, titles(mia.ID), []string{"Family dinner"})
	if got := len(c.occurrences(t, november)); got != 2 {
		t.Fatalf("family view shows %d events, want 2", got)
	}
}

func TestSearchAPI(t *testing.T) {
	c := signedInAPI(t)
	c.createEvent(t, map[string]any{"title": "Schwimmkurs", "location": "Westbad", "start": "2026-11-02T16:00:00+01:00", "end": "2026-11-02T17:00:00+01:00"})
	status, body, list := c.call(http.MethodGet, "/search?q=westbad", nil)
	if status != http.StatusOK || len(list) != 1 || list[0].(map[string]any)["title"] != "Schwimmkurs" {
		t.Fatalf("search = %d %v %v", status, body, list)
	}
}
