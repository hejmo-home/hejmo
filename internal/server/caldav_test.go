package server_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	ical "github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/store"
)

// phone is a CalDAV client such as iOS Calendar or DAVx5: the parent's
// username with a device token as the password.
type phone struct {
	t      *testing.T
	b      *box
	user   string
	secret string
	client *caldav.Client
}

func (c *apiClient) phone(t *testing.T, scopes string) *phone {
	t.Helper()
	return c.phoneWith(t, "mama", c.newToken(scopes))
}

func (c *apiClient) phoneWith(t *testing.T, user, secret string) *phone {
	t.Helper()
	http := webdav.HTTPClientWithBasicAuth(c.b.bareClient(), user, secret)
	cl, err := caldav.NewClient(http, c.b.srv.URL+"/dav/")
	if err != nil {
		t.Fatal(err)
	}
	return &phone{t, c.b, user, secret, cl}
}

// calendars discovers the calendars the way a phone does: principal, home set, calendars.
func (p *phone) calendars() ([]caldav.Calendar, error) {
	ctx := context.Background()
	principal, err := p.client.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	home, err := p.client.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return nil, err
	}
	return p.client.FindCalendars(ctx, home)
}

func (p *phone) calendarNamed(name string) caldav.Calendar {
	p.t.Helper()
	cals, err := p.calendars()
	if err != nil {
		p.t.Fatalf("discover calendars: %v", err)
	}
	for _, c := range cals {
		if c.Name == name {
			return c
		}
	}
	p.t.Fatalf("calendar %q not listed, got %+v", name, cals)
	return caldav.Calendar{}
}

func (p *phone) titles(calPath string) []string {
	p.t.Helper()
	objs, err := p.client.QueryCalendar(context.Background(), calPath, &caldav.CalendarQuery{
		CompFilter: caldav.CompFilter{Name: "VCALENDAR", Comps: []caldav.CompFilter{{Name: "VEVENT"}}},
	})
	if err != nil {
		p.t.Fatalf("query %s: %v", calPath, err)
	}
	var out []string
	for _, o := range objs {
		for _, e := range o.Data.Events() {
			s, _ := e.Props.Text(ical.PropSummary)
			out = append(out, s)
		}
	}
	return out
}

// raw sends one DAV request with the phone's credentials.
func (p *phone) raw(method, path, body string, header map[string]string) (int, http.Header, string) {
	p.t.Helper()
	req, _ := http.NewRequest(method, p.b.srv.URL+path, strings.NewReader(body))
	req.SetBasicAuth(p.user, p.secret)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := p.b.bareClient().Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, string(raw)
}

func sameTitles(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	seen := map[string]int{}
	for _, g := range got {
		seen[g]++
	}
	for _, w := range want {
		if seen[w] == 0 {
			t.Fatalf("got %v, want %v", got, want)
		}
		seen[w]--
	}
}

func familyOf(t *testing.T, c *apiClient) (store.Household, store.Calendar) {
	t.Helper()
	h := mustHousehold(t, c)
	cal, err := c.b.db.FamilyCalendar(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	return h, cal
}

func eventICS(uid, title, start, end string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//EN\r\nBEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:20261114T120000Z\r\nSUMMARY:" + title +
		"\r\nDTSTART:" + start + "\r\nDTEND:" + end + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
}

const icsType = "text/calendar; charset=utf-8"

// Acceptance: Given the box as a CalDAV server, when a phone subscribes to the
// family calendar, then it sees all events its role is allowed to see, and no
// others.
func TestPhoneSeesTheFamilyCalendarAndOnlyWhatIsShared(t *testing.T) {
	c := signedInAPI(t)
	h, family := familyOf(t, c)
	mia, _ := c.b.db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	for _, e := range []calendar.Event{
		{CalendarID: family.ID, Title: "Dentist", Start: at(2026, 11, 20, 9, 0), End: at(2026, 11, 20, 10, 0)},
		// A parent sees events meant for one child as well.
		{CalendarID: family.ID, Title: "Swimming", Start: at(2026, 11, 21, 16, 0), End: at(2026, 11, 21, 17, 0), People: []string{mia.ID}},
	} {
		if _, err := c.b.db.CreateEvent(h.ID, e); err != nil {
			t.Fatal(err)
		}
	}
	c.b.feeds.set(schoolFeed, feedOf("UID:trip\r\nSUMMARY:School trip\r\nDTSTART:20261125T080000Z\r\nDTEND:20261125T120000Z"), nil)
	school := c.addICS(t, map[string]any{"kind": "ics", "name": "School", "url": schoolFeed})
	c.b.feeds.set("https://example.org/club.ics", feedOf("UID:m\r\nSUMMARY:Match\r\nDTSTART:20261126T080000Z\r\nDTEND:20261126T120000Z"), nil)
	club := c.addICS(t, map[string]any{"kind": "ics", "name": "Club", "url": "https://example.org/club.ics"})
	if err := c.b.db.SetCalendarShared(h.ID, club["id"].(string), false); err != nil {
		t.Fatal(err)
	}

	ph := c.phone(t, "calendars")
	cals, err := ph.calendars()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, cal := range cals {
		names = append(names, cal.Name)
	}
	sameTitles(t, names, []string{"Family", "School"})

	sameTitles(t, ph.titles(ph.calendarNamed("Family").Path), []string{"Dentist", "Swimming"})
	sameTitles(t, ph.titles(ph.calendarNamed("School").Path), []string{"School trip"})
	_ = school
}

// Acceptance: Given a calendar with "share through the box" off, then it is
// not listed for phones (and cannot be reached by its address).
func TestUnsharedCalendarIsNeitherListedNorReachable(t *testing.T) {
	c := signedInAPI(t)
	h, _ := familyOf(t, c)
	c.b.feeds.set(schoolFeed, feedOf("UID:trip\r\nSUMMARY:School trip\r\nDTSTART:20261125T080000Z\r\nDTEND:20261125T120000Z"), nil)
	school := c.addICS(t, map[string]any{"kind": "ics", "name": "School", "url": schoolFeed})
	ph := c.phone(t, "calendars")
	listed := ph.calendarNamed("School")

	if err := c.b.db.SetCalendarShared(h.ID, school["id"].(string), false); err != nil {
		t.Fatal(err)
	}
	cals, _ := ph.calendars()
	for _, cal := range cals {
		if cal.Name == "School" {
			t.Fatalf("unshared calendar still listed: %+v", cal)
		}
	}
	if status, _, _ := ph.raw("PROPFIND", listed.Path, "", map[string]string{"Depth": "1"}); status != http.StatusNotFound {
		t.Fatalf("PROPFIND on unshared calendar = %d, want 404", status)
	}
}

// Acceptance: Given a revoked device token, when the phone syncs, then it is refused.
func TestRevokedDeviceTokenIsRefused(t *testing.T) {
	c := signedInAPI(t)
	status, created, _ := c.call(http.MethodPost, "/tokens", map[string]any{"name": "iPhone Mama", "scopes": "calendars"})
	if status != http.StatusCreated {
		t.Fatalf("create token = %d", status)
	}
	ph := c.phoneWith(t, "mama", created["token"].(string))
	if _, err := ph.calendars(); err != nil {
		t.Fatalf("before revoking: %v", err)
	}
	if status, _, _ := c.call(http.MethodDelete, "/tokens/"+created["id"].(string), nil); status != http.StatusNoContent {
		t.Fatalf("revoke = %d", status)
	}
	if status, hdr, _ := ph.raw("PROPFIND", "/dav/", "", map[string]string{"Depth": "0"}); status != http.StatusUnauthorized || !strings.HasPrefix(hdr.Get("WWW-Authenticate"), "Basic") {
		t.Fatalf("after revoking: %d %q, want 401 with a Basic challenge", status, hdr.Get("WWW-Authenticate"))
	}
	if _, err := ph.calendars(); err == nil {
		t.Fatal("a revoked token still syncs")
	}
}

func TestOnlyDeviceCredentialsOpenTheCalendarServer(t *testing.T) {
	c := signedInAPI(t)
	token := c.newToken("calendars")
	other := func(user, secret string) int {
		s, _, _ := c.phoneWith(t, user, secret).raw("PROPFIND", "/dav/", "", map[string]string{"Depth": "0"})
		return s
	}
	if got := other("mama", token); got != http.StatusMultiStatus {
		t.Fatalf("valid credentials = %d, want 207", got)
	}
	if got := other("mama", "hejmo_wrong"); got != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d, want 401", got)
	}
	if got := other("mama", "correct horse battery"); got != http.StatusUnauthorized {
		t.Fatalf("the account password is not a device credential, got %d, want 401", got)
	}
	if got := other("papa", token); got != http.StatusUnauthorized {
		t.Fatalf("token with another person's username = %d, want 401", got)
	}
	if got := other("mama", c.newToken("people")); got != http.StatusForbidden {
		t.Fatalf("token without the calendars scope = %d, want 403", got)
	}
	// The browser session is no credential for CalDAV.
	req, _ := http.NewRequest("PROPFIND", c.b.srv.URL+"/dav/", nil)
	res, err := c.b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized && res.StatusCode != http.StatusForbidden {
		t.Fatalf("session cookie on CalDAV = %d, want a refusal", res.StatusCode)
	}
	// A read-only token syncs but does not write.
	ro := c.phoneWith(t, "mama", c.newToken("calendars:read"))
	cal := ro.calendarNamed("Family")
	if status, _, _ := ro.raw("PUT", cal.Path+"x.ics", eventICS("x", "X", "20261120T090000Z", "20261120T100000Z"), map[string]string{"Content-Type": icsType}); status != http.StatusForbidden {
		t.Fatalf("PUT with a read-only token = %d, want 403", status)
	}
}

func TestChildrenAndOtherAddressesAreRefused(t *testing.T) {
	c := signedInAPI(t)
	h, _ := familyOf(t, c)
	token := c.newToken("calendars")
	ph := c.phoneWith(t, "mama", token)
	if status, _, _ := ph.raw("PROPFIND", "/dav/papa/", "", map[string]string{"Depth": "0"}); status != http.StatusNotFound {
		t.Fatalf("someone else's principal = %d, want 404", status)
	}
	// A token held by a child (not creatable through the API) is refused.
	mia, _ := c.b.db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	_ = c.b.db.CreateAccount(store.Account{PersonID: mia.ID, Username: "mia", PasswordHash: "x", TOTPSecret: "S"})
	_, _ = c.b.db.CreateAPIToken(store.APIToken{Name: "kid", Hash: auth.HashToken("hejmo_kid"), PersonID: mia.ID, Scopes: "calendars"})
	if status, _, _ := c.phoneWith(t, "mia", "hejmo_kid").raw("PROPFIND", "/dav/", "", map[string]string{"Depth": "0"}); status != http.StatusForbidden {
		t.Fatalf("child's token = %d, want 403", status)
	}
}

// Acceptance: Given an event changed on the phone, when it is saved, then it
// appears on the box immediately; a stale If-Match gets 412.
func TestEventSavedOnThePhoneAppearsOnTheBoxAndStaleWritesAreRefused(t *testing.T) {
	c := signedInAPI(t)
	ph := c.phone(t, "calendars")
	family := ph.calendarNamed("Family")
	href := family.Path + "dentist-1.ics"

	status, hdr, body := ph.raw("PUT", href, eventICS("dentist-1", "Dentist", "20261120T090000Z", "20261120T100000Z"), map[string]string{"Content-Type": icsType, "If-None-Match": "*"})
	if status != http.StatusCreated || hdr.Get("ETag") == "" {
		t.Fatalf("create = %d %q %s", status, hdr.Get("ETag"), body)
	}
	if got := titlesOnBox(t, c); !contains(got, "Dentist") {
		t.Fatalf("event is not on the box right after the save: %v", got)
	}
	// Creating again where the event exists is refused.
	if status, _, _ := ph.raw("PUT", href, eventICS("dentist-1", "Dentist", "20261120T090000Z", "20261120T100000Z"), map[string]string{"Content-Type": icsType, "If-None-Match": "*"}); status != http.StatusPreconditionFailed {
		t.Fatalf("If-None-Match on an existing event = %d, want 412", status)
	}

	first := hdr.Get("ETag")
	status, hdr, _ = ph.raw("PUT", href, eventICS("dentist-1", "Dentist (moved)", "20261121T090000Z", "20261121T100000Z"), map[string]string{"Content-Type": icsType, "If-Match": first})
	if status != http.StatusCreated && status != http.StatusNoContent {
		t.Fatalf("update with the current ETag = %d", status)
	}
	if got := titlesOnBox(t, c); !contains(got, "Dentist (moved)") || contains(got, "Dentist") {
		t.Fatalf("box after update: %v", got)
	}
	if hdr.Get("ETag") == "" || hdr.Get("ETag") == first {
		t.Fatalf("the ETag must change with the content: %q after %q", hdr.Get("ETag"), first)
	}

	// The phone still holds the first version: its write is stale.
	if status, _, _ := ph.raw("PUT", href, eventICS("dentist-1", "Stale", "20261122T090000Z", "20261122T100000Z"), map[string]string{"Content-Type": icsType, "If-Match": first}); status != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match = %d, want 412", status)
	}
	if got := titlesOnBox(t, c); contains(got, "Stale") {
		t.Fatal("a refused write changed the box")
	}
	if status, _, _ := ph.raw("DELETE", href, "", map[string]string{"If-Match": first}); status != http.StatusPreconditionFailed {
		t.Fatalf("stale delete = %d, want 412", status)
	}
	if status, _, _ := ph.raw("PUT", family.Path+"ghost.ics", eventICS("ghost", "Ghost", "20261120T090000Z", "20261120T100000Z"), map[string]string{"Content-Type": icsType, "If-Match": first}); status != http.StatusPreconditionFailed {
		t.Fatalf("If-Match on a missing event = %d, want 412", status)
	}

	if status, _, _ := ph.raw("DELETE", href, "", map[string]string{"If-Match": hdr.Get("ETag")}); status != http.StatusNoContent && status != http.StatusOK {
		t.Fatalf("delete = %d", status)
	}
	if got := titlesOnBox(t, c); contains(got, "Dentist (moved)") {
		t.Fatalf("deleted event still on the box: %v", got)
	}
	if status, _, _ := ph.raw("GET", href, "", nil); status != http.StatusNotFound {
		t.Fatalf("GET after delete = %d, want 404", status)
	}
}

func TestPhoneEditsKeepWhatThePhoneDoesNotShow(t *testing.T) {
	c := signedInAPI(t)
	h, family := familyOf(t, c)
	mia, _ := c.b.db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	ev, err := c.b.db.CreateEvent(h.ID, calendar.Event{CalendarID: family.ID, Title: "Swimming", Start: at(2026, 11, 21, 16, 0), End: at(2026, 11, 21, 17, 0), People: []string{mia.ID}})
	if err != nil {
		t.Fatal(err)
	}
	ph := c.phone(t, "calendars")
	href := ph.calendarNamed("Family").Path + ev.ID + ".ics"
	if status, _, _ := ph.raw("PUT", href, eventICS(ev.ID, "Swimming lesson", "20261121T150000Z", "20261121T160000Z"), map[string]string{"Content-Type": icsType}); status != http.StatusCreated && status != http.StatusNoContent {
		t.Fatalf("update = %d", status)
	}
	series, err := c.b.db.Series(h.ID, ev.ID)
	if err != nil || len(series) != 1 || series[0].Title != "Swimming lesson" || len(series[0].People) != 1 || series[0].People[0] != mia.ID {
		t.Fatalf("after the phone's edit: %+v, %v", series, err)
	}
}

func TestPhoneCanSaveRecurringEventsWithChangedOccurrences(t *testing.T) {
	c := signedInAPI(t)
	ph := c.phone(t, "calendars")
	href := ph.calendarNamed("Family").Path + "choir.ics"
	body := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:choir\r\nDTSTAMP:20261114T120000Z\r\nSUMMARY:Choir\r\nDTSTART;TZID=Europe/Berlin:20261103T170000\r\nDTEND;TZID=Europe/Berlin:20261103T180000\r\nRRULE:FREQ=WEEKLY;COUNT=4\r\nEXDATE;TZID=Europe/Berlin:20261117T170000\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:choir\r\nDTSTAMP:20261114T120000Z\r\nRECURRENCE-ID;TZID=Europe/Berlin:20261110T170000\r\nSUMMARY:Choir (hall B)\r\nDTSTART;TZID=Europe/Berlin:20261110T180000\r\nDTEND;TZID=Europe/Berlin:20261110T190000\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if status, _, resp := ph.raw("PUT", href, body, map[string]string{"Content-Type": icsType}); status != http.StatusCreated {
		t.Fatalf("PUT series = %d %s", status, resp)
	}
	occ := c.occurrences(t, "from=2026-11-01T00:00:00%2B01:00&to=2026-12-01T00:00:00%2B01:00")
	sameList(t, occTitles(occ), []string{"11-03T17:00 Choir", "11-10T18:00 Choir (hall B)", "11-24T17:00 Choir"})

	// The phone drops the changed occurrence and the series' end: a full replace.
	body2 := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:choir\r\nDTSTAMP:20261114T120000Z\r\nSUMMARY:Choir\r\nDTSTART;TZID=Europe/Berlin:20261103T170000\r\nDTEND;TZID=Europe/Berlin:20261103T180000\r\nRRULE:FREQ=WEEKLY;COUNT=2\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if status, _, resp := ph.raw("PUT", href, body2, map[string]string{"Content-Type": icsType}); status != http.StatusCreated && status != http.StatusNoContent {
		t.Fatalf("PUT replace = %d %s", status, resp)
	}
	occ = c.occurrences(t, "from=2026-11-01T00:00:00%2B01:00&to=2026-12-01T00:00:00%2B01:00")
	sameList(t, occTitles(occ), []string{"11-03T17:00 Choir", "11-10T17:00 Choir"})
}

func TestPhoneCannotChangeSubscribedCalendarsOrNameClashes(t *testing.T) {
	c := signedInAPI(t)
	c.b.feeds.set(schoolFeed, feedOf("UID:trip\r\nSUMMARY:School trip\r\nDTSTART:20261125T080000Z\r\nDTEND:20261125T120000Z"), nil)
	c.addICS(t, map[string]any{"kind": "ics", "name": "School", "url": schoolFeed})
	ph := c.phone(t, "calendars")
	school, family := ph.calendarNamed("School"), ph.calendarNamed("Family")

	if status, _, _ := ph.raw("PUT", school.Path+"new.ics", eventICS("new", "New", "20261120T090000Z", "20261120T100000Z"), map[string]string{"Content-Type": icsType}); status != http.StatusForbidden {
		t.Fatalf("PUT into a read-only calendar = %d, want 403", status)
	}
	objs, _ := ph.client.QueryCalendar(context.Background(), school.Path, &caldav.CalendarQuery{CompFilter: caldav.CompFilter{Name: "VCALENDAR", Comps: []caldav.CompFilter{{Name: "VEVENT"}}}})
	if len(objs) != 1 {
		t.Fatalf("school objects = %d", len(objs))
	}
	if status, _, _ := ph.raw("DELETE", objs[0].Path, "", nil); status != http.StatusForbidden {
		t.Fatalf("DELETE in a read-only calendar = %d, want 403", status)
	}
	// An event of the school calendar cannot be taken over through the family calendar's address.
	id := strings.TrimSuffix(objs[0].Path[strings.LastIndex(objs[0].Path, "/")+1:], ".ics")
	if status, _, _ := ph.raw("PUT", family.Path+id+".ics", eventICS(id, "Hijack", "20261120T090000Z", "20261120T100000Z"), map[string]string{"Content-Type": icsType}); status != http.StatusConflict {
		t.Fatalf("PUT onto another calendar's event = %d, want 409", status)
	}
	for _, bad := range []string{"../x.ics", "a%2Fb.ics", "x y.ics"} {
		if status, _, _ := ph.raw("PUT", family.Path+bad, eventICS("x", "X", "20261120T090000Z", "20261120T100000Z"), map[string]string{"Content-Type": icsType}); status < 400 {
			t.Fatalf("PUT %q = %d, want an error", bad, status)
		}
	}
	if status, _, _ := ph.raw("PUT", family.Path+"junk.ics", "not a calendar", map[string]string{"Content-Type": icsType}); status != http.StatusBadRequest {
		t.Fatalf("PUT junk = %d, want 400", status)
	}
	if status, _, _ := ph.raw("MKCOL", ph.calendarNamed("Family").Path+"sub/", "", nil); status < 400 {
		t.Fatalf("MKCOL = %d, want an error", status)
	}
}

func TestCalendarServerNeedsHTTPS(t *testing.T) {
	b := newFreshBox(t) // no certificate yet: plain HTTP
	req, _ := http.NewRequest("PROPFIND", b.srv.URL+"/dav/", nil)
	req.SetBasicAuth("mama", "hejmo_x")
	res, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("PROPFIND over plain HTTP = %d, want 403", res.StatusCode)
	}
	// With a certificate, plain HTTP is redirected like everything else.
	b2 := newBox(t)
	plain := b2.plainServer()
	req, _ = http.NewRequest("PROPFIND", plain.URL+"/dav/", nil)
	res, err = b2.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusPermanentRedirect || !strings.HasPrefix(res.Header.Get("Location"), "https://") {
		t.Fatalf("plain HTTP = %d %q, want a redirect to HTTPS", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestWellKnownCalDAVPointsToTheServer(t *testing.T) {
	b := newBox(t)
	if r := b.get("/.well-known/caldav"); r.status != http.StatusMovedPermanently && r.status != http.StatusPermanentRedirect || r.location != "/dav/" {
		t.Fatalf("/.well-known/caldav = %d %q", r.status, r.location)
	}
}

func titlesOnBox(t *testing.T, c *apiClient) []string {
	t.Helper()
	occ := c.occurrences(t, "from=2026-11-01T00:00:00%2B01:00&to=2026-12-31T00:00:00%2B01:00")
	var out []string
	for _, o := range occ {
		out = append(out, o["title"].(string))
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

var berlin, _ = time.LoadLocation("Europe/Berlin")

func at(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, berlin)
}
