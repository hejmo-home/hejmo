package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

func TestCalendarsOverviewListsRowsThatOpenTheirDetailPages(t *testing.T) {
	t.Parallel()
	b, cal := icsBox(t)
	connID := connectedBox(t, b)
	body := getEN(b, "/calendars").body
	for _, want := range []string{
		`<ul class="list-rows">`, `<a href="/calendars/` + cal.ID + `">`, "<b>School</b>", "For ", "Whole family", "Last synced 14.11.2026,\u00a021:46:26",
		`<a href="/connections/` + connID + `">`, "<b>u</b>", "No calendar on the box yet",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q\n%s", want, body)
		}
	}
	noDestructiveForm(t, "/calendars", body)
	for _, gone := range []string{`/sync"`, "Sync now", "Choose calendars</a>"} {
		if strings.Contains(body, gone) {
			t.Errorf("overview still has %q", gone)
		}
	}
}

func TestCalendarDetailShowsFactsSyncNowAndConfirmsRemoving(t *testing.T) {
	t.Parallel()
	b, cal := icsBox(t)
	r := getEN(b, "/calendars/"+cal.ID)
	if r.status != http.StatusOK {
		t.Fatalf("GET = %d", r.status)
	}
	for _, want := range []string{
		`<nav class="crumbs" aria-label="Breadcrumb">`, `<a href="/calendars">Calendar connections</a>`, `<span aria-current="page">School</span>`, "<h1>School</h1>", "<dt>Address</dt>", feedURL172, "<dt>For</dt>", "Whole family",
		"<dt>Last synced</dt>", "21:46:26", "<dt>Events</dt>", "2 (last 12 months and later)", "<dt>Last run</dt>", "2 new, 0 changed, 0 removed",
		`action="/calendars/` + cal.ID + `/sync"`, "Sync now", `<details class="danger">`, "Remove calendar", `action="/calendars/` + cal.ID + `/delete"`,
		"Yes, remove calendar", "No, change nothing",
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("detail lacks %q\n%s", want, r.body)
		}
	}
	if c := b.get("/calendars/nope"); c.status != http.StatusNotFound {
		t.Errorf("unknown calendar = %d", c.status)
	}
}

func TestSyncNowReturnsToTheDetailPageWithTheSuccessLine(t *testing.T) {
	t.Parallel()
	b, cal := icsBox(t)
	b.now = b.now.Add(time.Hour)
	if r := b.post("/calendars/"+cal.ID+"/sync", url.Values{}); r.status != http.StatusSeeOther || r.location != "/calendars/"+cal.ID {
		t.Fatalf("sync now = %d %q", r.status, r.location)
	}
	page := getEN(b, "/calendars/"+cal.ID)
	if got := successLine(page.body); !strings.HasPrefix(got, "Updated at 22:46:26. Events: 2") {
		t.Errorf("success line = %q", got)
	}
	if successLine(getEN(b, "/calendars/"+cal.ID).body) != "" {
		t.Error("the success line is shown twice")
	}
}

func TestRemovingACalendarNamesItAndLeavesTheOverview(t *testing.T) {
	t.Parallel()
	b, cal := icsBox(t)
	getEN(b, "/calendars") // the line of adding is shown and gone
	if r := b.post("/calendars/"+cal.ID+"/delete", url.Values{}); r.status != http.StatusSeeOther || r.location != "/calendars" {
		t.Fatalf("remove = %d %q", r.status, r.location)
	}
	if got := successLine(getEN(b, "/calendars").body); got != "“School” removed." {
		t.Errorf("success line = %q", got)
	}
}

func TestAFailedSyncIsAProblemFlagOnTheRowAndTheFacts(t *testing.T) {
	t.Parallel()
	b, cal := icsBox(t)
	b.now = b.now.Add(time.Hour)
	b.feeds.set(feedURL172, "", http.ErrHandlerTimeout)
	if r := b.post("/calendars/"+cal.ID+"/sync", url.Values{}); r.status != http.StatusSeeOther || r.location != "/calendars/"+cal.ID {
		t.Fatalf("failed sync now = %d %q", r.status, r.location)
	}
	for _, page := range []string{"/calendars", "/calendars/" + cal.ID} {
		if body := getEN(b, page).body; !strings.Contains(body, `class="flag problem"`) || !strings.Contains(body, "Last problem: ") {
			t.Errorf("%s: no problem flag after a failed sync\n%s", page, body)
		}
	}
}

func TestACalendarOfAConnectionHasNoPageOfItsOwn(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	connID := connectedBox(t, b)
	r := b.do(http.MethodPost, "/connections/"+connID+"/calendars", url.Values{"calendar": {flowCalendar}, "name_" + flowCalendar: {"Home"}, "interval": {"60"}}, nil)
	if r.status != http.StatusSeeOther {
		t.Fatalf("choose = %d\n%s", r.status, r.body)
	}
	h, _ := b.db.HomeHousehold()
	cals, _ := b.db.SyncedCalendars(h.ID)
	if got := b.get("/calendars/" + cals[0].ID); got.status != http.StatusNotFound {
		t.Errorf("a connection's calendar page = %d, want 404: it is managed on the connection page", got.status)
	}
}

// ADR 0017: who a subscribed calendar is for is told by the person's marker
// and name, subgroups by name, or "Whole family" when nobody is chosen.
func TestSubscribedCalendarRowsSayWhoTheyAreFor(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	h := mustHousehold(t, c)
	mia, _ := c.b.db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	seb, _ := c.b.db.AddPerson(h.ID, store.Person{Name: "Sebastian", Role: store.RoleChild})
	kids, _ := c.b.db.CreateGroup(h.ID, "Kids", []string{mia.ID, seb.ID})
	c.b.feeds.set(schoolFeed, feedOf(), nil)
	two := func(people, groups []string) map[string]any {
		return map[string]any{"people": people, "groups": groups}
	}
	c.addICS(t, map[string]any{"kind": "ics", "name": "Bins", "url": schoolFeed})
	c.addICS(t, map[string]any{"kind": "ics", "name": "Football", "url": schoolFeed, "audience": two([]string{seb.ID, mia.ID}, []string{})})
	c.addICS(t, map[string]any{"kind": "ics", "name": "Swimming", "url": schoolFeed, "audience": two([]string{}, []string{kids.ID})})
	body := getEN(c.b, "/calendars").body
	row := func(name string) string {
		at := strings.Index(body, "<b>"+name+"</b>")
		if at < 0 {
			t.Fatalf("no row for %s\n%s", name, body)
		}
		return body[at : at+strings.Index(body[at:], "</a>")]
	}
	if r := row("Bins"); !strings.Contains(r, "Whole family") || strings.Contains(r, `class="marker"`) {
		t.Errorf("Bins row = %s", r)
	}
	if r := row("Football"); strings.Count(r, `class="marker"`) != 2 || !strings.Contains(r, "Sebastian") || !strings.Contains(r, "Mia") || strings.Contains(r, "Whole family") {
		t.Errorf("Football row = %s", r)
	}
	if r := row("Swimming"); !strings.Contains(r, "For Kids") {
		t.Errorf("Swimming row = %s", r)
	}
}
