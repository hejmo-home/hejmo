package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/caldavclient/caldavtest"
	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 5.17 (ADR 0017): Choose calendars is the single place per connection.

type countJSON struct {
	URL   string `json:"url"`
	State string `json:"state"`
	Count int    `json:"count"`
	Text  string `json:"text"`
}

// eventCounts asks for the event counts of a connection the way the page does
// (the signed-in session, no token), in English.
func eventCounts(t *testing.T, b *box, connID string) (int, []countJSON) {
	t.Helper()
	r := b.do(http.MethodGet, "/api/v1/calendar-connections/"+connID+"/event-counts", nil, map[string]string{"Accept-Language": "en"})
	var out []countJSON
	if r.status == http.StatusOK {
		if err := json.Unmarshal([]byte(r.body), &out); err != nil {
			t.Fatalf("event counts: %v\n%s", err, r.body)
		}
	}
	return r.status, out
}

func countOf(t *testing.T, counts []countJSON, address string) countJSON {
	t.Helper()
	for _, c := range counts {
		if c.URL == address {
			return c
		}
	}
	t.Fatalf("no count for %s in %+v", address, counts)
	return countJSON{}
}

// davConnection connects a box to Radicale through the web form and returns
// the connection id.
func davConnection(t *testing.T, c *apiClient, srv *caldavtest.Server) string {
	t.Helper()
	r := c.b.post("/connections", url.Values{"provider": {"caldav"}, "url": {srv.URL}, "username": {caldavtest.User}, "password": {caldavtest.Password}})
	if r.status != http.StatusOK {
		t.Fatalf("connect = %d\n%s", r.status, r.body)
	}
	conns, _ := c.b.db.Connections(mustHousehold(t, c).ID)
	return conns[len(conns)-1].ID
}

func TestChooseCalendarsShowsNoEventsForACalendarWhoseOnlyEventIsFrom2016(t *testing.T) {
	c, srv := radicaleBox(t)
	old := srv.MakeCalendar(t, "old", "Piano 2016")
	family := srv.MakeCalendar(t, "family", "Family")
	srv.Put(t, old, "play.ics", caldavtest.Event("play@test", "School play", "20160620T150000", "20160620T160000"))
	srv.Put(t, family, "swim.ics", caldavtest.Event("swim@test", "Swimming", "20261120T150000", "20261120T160000"))
	srv.Put(t, family, "choir.ics", caldavtest.Event("choir@test", "Choir", "20261121T150000", "20261121T160000"))
	id := davConnection(t, c, srv)

	status, counts := eventCounts(t, c.b, id)
	if status != http.StatusOK || len(counts) != 2 {
		t.Fatalf("counts = %d %+v", status, counts)
	}
	if got := countOf(t, counts, old); got.State != "empty" || got.Text != "No events in the last 12 months or later" {
		t.Errorf("old calendar = %+v", got)
	}
	if got := countOf(t, counts, family); got.State != "count" || got.Count != 2 || !strings.Contains(got.Text, "2 events") {
		t.Errorf("family calendar = %+v", got)
	}

	// The page itself loads without waiting for the counts and says where it gets them.
	page := getEN(c.b, "/connections/"+id)
	for _, want := range []string{`data-counts-url="/api/v1/calendar-connections/` + id + `/event-counts"`, `data-count-for="` + old + `"`, `src="/static/connection-counts.js"`} {
		if !strings.Contains(page.body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if js := c.b.get("/static/connection-counts.js"); js.status != http.StatusOK || !strings.Contains(js.body, "data-counts-url") {
		t.Errorf("count script = %d", js.status)
	}
	de := c.b.do(http.MethodGet, "/api/v1/calendar-connections/"+id+"/event-counts", nil, map[string]string{"Accept-Language": "de"})
	if !strings.Contains(de.body, "Keine Termine in den letzten 12 Monaten oder später") {
		t.Errorf("German text missing: %s", de.body)
	}
}

// slowCounts finds two calendars; the count of the first never answers.
type slowCounts struct {
	calsync.Remote
	asked atomic.Int32
}

func (s *slowCounts) Calendars(context.Context) ([]caldavclient.Calendar, error) {
	return []caldavclient.Calendar{{URL: flowCalendar, Name: "Home"}, {URL: flowCalendar2, Name: "Work"}}, nil
}

func (s *slowCounts) CountEvents(ctx context.Context, address string, _ time.Time) (int, error) {
	s.asked.Add(1)
	if address == flowCalendar {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	return 4, nil
}

// withCountTimeout serves the box again with a short count timeout.
func withCountTimeout(b *box, d time.Duration) {
	b.handler = server.New(server.Options{DB: b.db, PKI: b.pki, Public: b.public, Now: func() time.Time { return b.now }, Sync: b.sync, Weather: b.weather, CountTimeout: d})
	b.srv.Close()
	b.srv = b.serveTLS()
	b.client = b.trustingClient()
}

func TestEventCountIsUnknownWhenTheServerDoesNotAnswerInTimeAndThePageStillLoads(t *testing.T) {
	b, secret := configuredBox(t)
	withCountTimeout(b, 100*time.Millisecond)
	if r := b.post("/login", loginForm(b, secret, "correct horse battery")); r.status != http.StatusSeeOther {
		t.Fatalf("login = %d", r.status)
	}
	id := connectedBox(t, b)
	remote := &slowCounts{}
	b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) { return remote, nil })

	began := time.Now()
	page := getEN(b, "/connections/"+id)
	if page.status != http.StatusOK || !strings.Contains(page.body, "Work") || remote.asked.Load() != 0 {
		t.Fatalf("page = %d, counts asked while rendering: %d", page.status, remote.asked.Load())
	}
	status, counts := eventCounts(t, b, id)
	if status != http.StatusOK || time.Since(began) > 5*time.Second {
		t.Fatalf("counts = %d after %v", status, time.Since(began))
	}
	if got := countOf(t, counts, flowCalendar); got.State != "unknown" || got.Text != "Number of events unknown" {
		t.Errorf("slow calendar = %+v", got)
	}
	if got := countOf(t, counts, flowCalendar2); got.State != "count" || got.Count != 4 {
		t.Errorf("fast calendar = %+v (one slow calendar must not hide the other)", got)
	}
}

func TestEveryFoundCalendarHasTheSwitchAlsoSyncedOnes(t *testing.T) {
	b, id := twoCalendarBox(t)
	b.post("/connections/"+id+"/calendars", url.Values{"calendar": {flowCalendar}, "shown": {flowCalendar, flowCalendar2}, "name_" + flowCalendar: {"Home"}, "interval": {"60"}})

	page := formOf(t, getEN(b, "/connections/"+id).body, "/connections/"+id+"/calendars")
	if n := strings.Count(page, `role="switch" name="calendar"`); n != 2 {
		t.Fatalf("%d switches, want one per found calendar:\n%s", n, page)
	}
	if !regexp.MustCompile(`name="calendar" value="` + regexp.QuoteMeta(flowCalendar) + `" checked`).MatchString(page) {
		t.Errorf("the synced calendar's switch is not on:\n%s", page)
	}
	if regexp.MustCompile(`name="calendar" value="` + regexp.QuoteMeta(flowCalendar2) + `" checked`).MatchString(page) {
		t.Error("a calendar that is not synced is switched on")
	}
	for _, f := range []string{flowCalendar, flowCalendar2} {
		for _, want := range []string{`name="name_` + f + `"`, `name="people_` + f + `"`, `name="color_` + f + `"`} {
			if !strings.Contains(page, want) {
				t.Errorf("no %s for %s", want, f)
			}
		}
	}
	if !strings.Contains(page, `name="shown" value="`+flowCalendar+`"`) {
		t.Error("the form does not say which calendars it shows")
	}
}

func TestSwitchingASyncedCalendarOffAsksFirstThenRemovesItsEventsAndSendsNothingToTheServer(t *testing.T) {
	c, srv := radicaleBox(t)
	b := c.b
	family := srv.MakeCalendar(t, "family", "Family")
	school := srv.MakeCalendar(t, "school", "School")
	srv.Put(t, family, "swim.ics", caldavtest.Event("swim@test", "Swimming", "20261120T150000", "20261120T160000"))
	srv.Put(t, school, "fair.ics", caldavtest.Event("fair@test", "Fair", "20261121T150000", "20261121T160000"))
	id := davConnection(t, c, srv)
	both := url.Values{"calendar": {family, school}, "shown": {family, school}, "name_" + family: {"Family"}, "name_" + school: {"School"}, "interval": {"5"}}
	if r := b.post("/connections/"+id+"/calendars", both); r.status != http.StatusSeeOther {
		t.Fatalf("add both = %d\n%s", r.status, r.body)
	}
	sameList(t, occTitles(c.occurrences(t, november)), []string{"11-20T15:00 Swimming", "11-21T15:00 Fair"})

	// Switch School off: the box asks, and nothing has happened yet.
	off := url.Values{"calendar": {family}, "shown": {family, school}, "name_" + family: {"Family"}, "name_" + school: {"School"}, "interval": {"5"}}
	r := b.do(http.MethodPost, "/connections/"+id+"/calendars", off, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusOK || !strings.Contains(r.body, "School") || !strings.Contains(r.body, "nothing is deleted at the source") || !strings.Contains(r.body, `name="confirm_off"`) {
		t.Fatalf("confirmation = %d\n%s", r.status, r.body)
	}
	if got := len(c.occurrences(t, november)); got != 2 {
		t.Fatalf("events before the confirmation = %d, want 2", got)
	}

	// Confirmed: the events leave the box and no request reaches the server.
	var dials atomic.Int32
	b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) {
		dials.Add(1)
		return nil, context.Canceled
	})
	off.Set("confirm_off", "1")
	r = b.post("/connections/"+id+"/calendars", off)
	if r.status != http.StatusSeeOther || r.location != "/calendars" {
		t.Fatalf("confirmed = %d %q\n%s", r.status, r.location, r.body)
	}
	sameList(t, occTitles(c.occurrences(t, november)), []string{"11-20T15:00 Swimming"})
	if n := dials.Load(); n != 0 {
		t.Errorf("the box contacted the server %d times while switching a calendar off", n)
	}
	remote, _ := caldavclient.NewClient(srv.URL, caldavtest.User, caldavtest.Password, nil)
	if objs, err := remote.List(context.Background(), school); err != nil || len(objs) != 1 {
		t.Errorf("the source lost its event: %v, %v", objs, err)
	}
	cals, _ := b.db.SyncedCalendars(mustHousehold(t, c).ID)
	if len(cals) != 1 || cals[0].Name != "Family" {
		t.Errorf("calendars = %+v", cals)
	}

	// A calendar the form did not show is never switched off by it.
	r = b.post("/connections/"+id+"/calendars", url.Values{"calendar": {school}, "name_" + school: {"School"}, "interval": {"5"}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("add again = %d\n%s", r.status, r.body)
	}
	if cals, _ := b.db.SyncedCalendars(mustHousehold(t, c).ID); len(cals) != 2 {
		t.Errorf("a calendar the form did not show was switched off: %+v", cals)
	}
}

func TestChooseCalendarsEditsNameColourAndAudienceOfASyncedCalendar(t *testing.T) {
	b, id := twoCalendarBox(t)
	h, _ := b.db.HomeHousehold()
	mia, _ := b.db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	b.post("/connections/"+id+"/calendars", url.Values{"calendar": {flowCalendar}, "shown": {flowCalendar}, "name_" + flowCalendar: {"Home"}, "interval": {"60"}})

	r := b.post("/connections/"+id+"/calendars", url.Values{"calendar": {flowCalendar}, "shown": {flowCalendar, flowCalendar2}, "name_" + flowCalendar: {"Our home"},
		"color_" + flowCalendar: {"#112233"}, "people_" + flowCalendar: {mia.ID}, "interval": {"60"}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("edit = %d\n%s", r.status, r.body)
	}
	cals, _ := b.db.SyncedCalendars(h.ID)
	if len(cals) != 1 || cals[0].Name != "Our home" || cals[0].Color != "#112233" || len(cals[0].Audience.People) != 1 || cals[0].Audience.People[0] != mia.ID {
		t.Fatalf("calendar = %+v", cals)
	}
	// A wrong colour is an error at the field and changes nothing.
	r = b.do(http.MethodPost, "/connections/"+id+"/calendars", url.Values{"calendar": {flowCalendar}, "shown": {flowCalendar}, "name_" + flowCalendar: {"Renamed"}, "color_" + flowCalendar: {"red"}, "interval": {"60"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || strings.Count(r.body, `class="ferr"`) != 1 {
		t.Fatalf("bad colour = %d\n%s", r.status, r.body)
	}
	if cals, _ := b.db.SyncedCalendars(h.ID); cals[0].Name != "Our home" {
		t.Errorf("a failed edit changed the calendar: %+v", cals[0])
	}
}

func TestCalendarColourIsPickedAutomaticallyAndChangedUnderMoreOptions(t *testing.T) {
	b, id := twoCalendarBox(t)
	page := formOf(t, getEN(b, "/connections/"+id).body, "/connections/"+id+"/calendars")
	colour := regexp.MustCompile(`name="color_[^"]*" type="color" value="(#[0-9A-Fa-f]{6})"`).FindAllStringSubmatch(page, -1)
	if len(colour) != 2 || colour[0][1] == colour[1][1] {
		t.Fatalf("automatic colours = %v, want two different ones", colour)
	}
	// The colour picker sits under "More options", not beside the name.
	for _, f := range []string{flowCalendar, flowCalendar2} {
		i, j := strings.Index(page, `name="color_`+f+`"`), strings.LastIndex(page[:strings.Index(page, `name="color_`+f+`"`)], `<details class="more"`)
		if j < 0 || strings.Contains(page[j:i], "</details>") {
			t.Errorf("the colour of %s is not under More options", f)
		}
	}
	// Left out, the box still picks one.
	b.post("/connections/"+id+"/calendars", url.Values{"calendar": {flowCalendar, flowCalendar2}, "name_" + flowCalendar: {"Home"}, "name_" + flowCalendar2: {"Work"}, "interval": {"60"}})
	h, _ := b.db.HomeHousehold()
	cals, _ := b.db.SyncedCalendars(h.ID)
	if len(cals) != 2 || cals[0].Color == "" || cals[0].Color == cals[1].Color {
		t.Errorf("colours = %+v", cals)
	}
	// Adding an ICS calendar no longer asks for a colour above More options.
	cal := getEN(b, "/calendars").body
	if strings.Contains(cal, `name="color"`) && strings.Index(cal, `name="color"`) < strings.Index(cal, `<details class="more"`) {
		t.Error("the add form asks for a colour before More options")
	}
}
