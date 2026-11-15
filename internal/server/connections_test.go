package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/caldavclient/caldavtest"
	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/store"
)

func (c *apiClient) connect(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	status, conn, _ := c.call(http.MethodPost, "/calendar-connections", body)
	if status != http.StatusCreated {
		t.Fatalf("connect = %d %v", status, conn)
	}
	return conn
}

func calendarNamed(t *testing.T, conn map[string]any, name string) map[string]any {
	t.Helper()
	for _, x := range conn["calendars"].([]any) {
		if cal := x.(map[string]any); cal["name"] == name {
			return cal
		}
	}
	t.Fatalf("no discovered calendar %q in %v", name, conn)
	return nil
}

func radicaleBox(t *testing.T) (*apiClient, *caldavtest.Server) {
	t.Helper()
	srv := caldavtest.Start(t)
	return signedInAPI(t), srv
}

func davCreds(srv *caldavtest.Server) map[string]any {
	return map[string]any{"provider": "caldav", "url": srv.URL, "username": caldavtest.User, "password": caldavtest.Password}
}

func TestConnectingShowsDiscoveredCalendarsAndSyncsOnlyTheTickedOnes(t *testing.T) {
	c, srv := radicaleBox(t)
	family := srv.MakeCalendar(t, "family", "Family")
	srv.MakeCalendar(t, "school", "School")
	srv.Put(t, family, "swim.ics", caldavtest.Event("swim@test", "Swimming", "20261120T150000", "20261120T160000"))
	h := mustHousehold(t, c)
	mia, _ := c.b.db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})

	conn := c.connect(t, davCreds(srv))
	if conn["kind"] != "caldav" || conn["username"] != caldavtest.User || len(conn["calendars"].([]any)) != 2 {
		t.Fatalf("connection = %v", conn)
	}
	if strings.Contains(strings.ToLower(jsonOf(conn)), strings.ToLower(caldavtest.Password)) {
		t.Fatal("the response carries the password")
	}
	// Nothing syncs until a parent ticks it.
	if _, _, cals := c.call(http.MethodGet, "/calendars", nil); len(cals) != 1 {
		t.Fatalf("calendars before ticking = %v", cals)
	}

	pick := calendarNamed(t, conn, "Family")
	status, cal, _ := c.call(http.MethodPost, "/calendar-connections/"+conn["id"].(string)+"/calendars", map[string]any{
		"url": pick["url"], "name": "Family", "color": "#B4583A", "sync_interval_minutes": 5,
		"audience": map[string]any{"people": []string{mia.ID}, "groups": []string{}},
	})
	if status != http.StatusCreated || cal["kind"] != "caldav" || cal["color"] != "#B4583A" || cal["name"] != "Family" {
		t.Fatalf("add calendar = %d %v", status, cal)
	}
	if s := cal["sync"].(map[string]any); s["state"] != "ok" || s["interval_minutes"] != float64(5) || s["last_success_at"] == nil {
		t.Fatalf("sync = %v", s)
	}
	if aud := cal["audience"].(map[string]any); len(aud["people"].([]any)) != 1 {
		t.Fatalf("audience = %v", aud)
	}
	sameList(t, occTitles(c.occurrences(t, november)), []string{"11-20T15:00 Swimming"})

	// The discovery now marks what is already synced.
	status, again, _ := c.call(http.MethodGet, "/calendar-connections/"+conn["id"].(string)+"/calendars", nil)
	if status != http.StatusOK || calendarNamed(t, again, "Family")["added"] != true || calendarNamed(t, again, "School")["added"] != false {
		t.Fatalf("rediscovery = %d %v", status, again)
	}
	// The same calendar cannot be added twice.
	if status, _, _ := c.call(http.MethodPost, "/calendar-connections/"+conn["id"].(string)+"/calendars", map[string]any{"url": pick["url"], "name": "Family again"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("add twice = %d, want 422", status)
	}
}

func jsonOf(v map[string]any) string {
	var b strings.Builder
	var walk func(any)
	walk = func(x any) {
		switch x := x.(type) {
		case map[string]any:
			for k, v := range x {
				b.WriteString(k + "=")
				walk(v)
			}
		case []any:
			for _, v := range x {
				walk(v)
			}
		case string:
			b.WriteString(x + ";")
		}
	}
	walk(v)
	return b.String()
}

func TestWrongPasswordIsRefusedAndNothingIsStored(t *testing.T) {
	c, srv := radicaleBox(t)
	bad := davCreds(srv)
	bad["password"] = "wrong"
	status, body, _ := c.call(http.MethodPost, "/calendar-connections", bad)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d %v, want 422", status, body)
	}
	if status, _, list := c.call(http.MethodGet, "/calendar-connections", nil); status != http.StatusOK || len(list) != 0 {
		t.Fatalf("connections after a refused login = %d %v", status, list)
	}
}

func TestSingleCalendarAddressCanBeAddedByHand(t *testing.T) {
	c, srv := radicaleBox(t)
	family := srv.MakeCalendar(t, "family", "Family")
	srv.MakeCalendar(t, "school", "School")
	creds := davCreds(srv)
	creds["url"], creds["single_calendar"] = family, true

	conn := c.connect(t, creds)
	if cals := conn["calendars"].([]any); len(cals) != 1 || cals[0].(map[string]any)["name"] != "Family" {
		t.Fatalf("calendars = %v, want only the one given", cals)
	}
}

func TestICloudNeedsNoAddressAndUsesAppleServer(t *testing.T) {
	c := signedInAPI(t)
	var asked string
	c.b.sync.SetDialer(func(serverURL, user, pass string) (calsync.Remote, error) {
		asked = serverURL
		return nil, errors.New("offline in test")
	})
	status, _, _ := c.call(http.MethodPost, "/calendar-connections", map[string]any{"provider": "icloud", "username": "mama@example.org", "password": "abcd-efgh-ijkl-mnop"})
	if status != http.StatusUnprocessableEntity || asked != "https://caldav.icloud.com" {
		t.Fatalf("status = %d, asked %q", status, asked)
	}
}

func TestConnectionInputIsValidated(t *testing.T) {
	c, srv := radicaleBox(t)
	for name, body := range map[string]map[string]any{
		"blank user":     {"provider": "caldav", "url": srv.URL, "username": " ", "password": "p"},
		"blank password": {"provider": "caldav", "url": srv.URL, "username": "u", "password": ""},
		"no address":     {"provider": "caldav", "username": "u", "password": "p"},
		"ftp address":    {"provider": "caldav", "url": "ftp://x.example/", "username": "u", "password": "p"},
	} {
		if status, _, _ := c.call(http.MethodPost, "/calendar-connections", body); status != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want 422", name, status)
		}
	}
	conn := c.connect(t, davCreds(srv))
	for name, body := range map[string]map[string]any{
		"no name":          {"url": srv.CalendarURL("x"), "name": " "},
		"bad colour":       {"url": srv.CalendarURL("x"), "name": "X", "color": "red"},
		"bad interval":     {"url": srv.CalendarURL("x"), "name": "X", "sync_interval_minutes": 1},
		"unknown audience": {"url": srv.CalendarURL("x"), "name": "X", "audience": map[string]any{"people": []string{"00000000-0000-4000-8000-000000000000"}, "groups": []string{}}},
	} {
		if status, _, _ := c.call(http.MethodPost, "/calendar-connections/"+conn["id"].(string)+"/calendars", body); status != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want 422", name, status)
		}
	}
	if status, _, _ := c.call(http.MethodPost, "/calendar-connections/00000000-0000-4000-8000-000000000000/calendars", map[string]any{"url": srv.CalendarURL("x"), "name": "X"}); status != http.StatusNotFound {
		t.Errorf("unknown connection = %d, want 404", status)
	}
}

func TestRemovingAConnectionRemovesItsEventsAndDeletesNothingOutside(t *testing.T) {
	c, srv := radicaleBox(t)
	family := srv.MakeCalendar(t, "family", "Family")
	school := srv.MakeCalendar(t, "school", "School")
	srv.Put(t, family, "swim.ics", caldavtest.Event("swim@test", "Swimming", "20261120T150000", "20261120T160000"))
	srv.Put(t, school, "fair.ics", caldavtest.Event("fair@test", "Fair", "20261121T150000", "20261121T160000"))
	c.createEvent(t, map[string]any{"title": "Own", "start": "2026-11-05T18:00:00+01:00", "end": "2026-11-05T19:00:00+01:00"})
	conn := c.connect(t, davCreds(srv))
	id := conn["id"].(string)
	for _, name := range []string{"Family", "School"} {
		pick := calendarNamed(t, conn, name)
		if status, cal, _ := c.call(http.MethodPost, "/calendar-connections/"+id+"/calendars", map[string]any{"url": pick["url"], "name": name}); status != http.StatusCreated {
			t.Fatalf("add %s = %d %v", name, status, cal)
		}
	}
	if got := len(c.occurrences(t, november)); got != 3 {
		t.Fatalf("events before removal = %d, want 3", got)
	}

	if status, _, _ := c.call(http.MethodDelete, "/calendar-connections/"+id, nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
	sameList(t, occTitles(c.occurrences(t, november)), []string{"11-05T18:00 Own"})
	if _, _, cals := c.call(http.MethodGet, "/calendars", nil); len(cals) != 1 {
		t.Fatalf("calendars after removal = %v", cals)
	}
	if status, _, _ := c.call(http.MethodDelete, "/calendar-connections/"+id, nil); status != http.StatusNotFound {
		t.Fatalf("delete again = %d, want 404", status)
	}

	// Outside, nothing changed.
	remote, _ := caldavclient.NewClient(srv.URL, caldavtest.User, caldavtest.Password, nil)
	for _, cal := range []string{family, school} {
		if objs, err := remote.List(context.Background(), cal); err != nil || len(objs) != 1 {
			t.Fatalf("objects in %s after removal = %v, %v", cal, objs, err)
		}
	}
}

func TestRemovingOneCalendarKeepsTheConnectionAndTheOtherCalendar(t *testing.T) {
	c, srv := radicaleBox(t)
	srv.MakeCalendar(t, "family", "Family")
	srv.MakeCalendar(t, "school", "School")
	conn := c.connect(t, davCreds(srv))
	id := conn["id"].(string)
	var ids []string
	for _, name := range []string{"Family", "School"} {
		_, cal, _ := c.call(http.MethodPost, "/calendar-connections/"+id+"/calendars", map[string]any{"url": calendarNamed(t, conn, name)["url"], "name": name})
		ids = append(ids, cal["id"].(string))
	}
	if status, _, _ := c.call(http.MethodDelete, "/calendars/"+ids[0], nil); status != http.StatusNoContent {
		t.Fatalf("delete calendar = %d", status)
	}
	if _, _, cals := c.call(http.MethodGet, "/calendars", nil); len(cals) != 2 { // family calendar + School
		t.Fatalf("calendars = %v", cals)
	}
	if _, _, conns := c.call(http.MethodGet, "/calendar-connections", nil); len(conns) != 1 {
		t.Fatalf("connections = %v", conns)
	}
}

func TestConnectionEndpointsBelongToParents(t *testing.T) {
	c, srv := radicaleBox(t)
	conn := c.connect(t, davCreds(srv))
	id := conn["id"].(string)

	ro := c.withToken(c.newToken("read"))
	if status, _, _ := ro.call(http.MethodPost, "/calendar-connections", davCreds(srv)); status != http.StatusForbidden {
		t.Errorf("read token connects = %d, want 403", status)
	}
	if status, _, _ := ro.call(http.MethodGet, "/calendar-connections/"+id+"/calendars", nil); status != http.StatusForbidden {
		t.Errorf("read token discovers = %d, want 403", status)
	}
	if status, _, _ := ro.call(http.MethodDelete, "/calendar-connections/"+id, nil); status != http.StatusForbidden {
		t.Errorf("read token deletes = %d, want 403", status)
	}
	child := c.withToken(childToken(t, c.b.db))
	if status, _, _ := child.call(http.MethodGet, "/calendar-connections", nil); status != http.StatusForbidden {
		t.Errorf("child lists = %d, want 403", status)
	}
}

func TestWebConnectTickAndRemove(t *testing.T) {
	c, srv := radicaleBox(t)
	b := c.b
	family := srv.MakeCalendar(t, "family", "Family")
	srv.MakeCalendar(t, "school", "School")
	srv.Put(t, family, "swim.ics", caldavtest.Event("swim@test", "Swimming", "20261120T150000", "20261120T160000"))

	// Wrong password: explained on the page, nothing stored.
	r := b.post("/connections", url.Values{"provider": {"caldav"}, "url": {srv.URL}, "username": {caldavtest.User}, "password": {"pw-not-accepted-1"}})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `role="alert"`) {
		t.Fatalf("wrong password = %d\n%s", r.status, r.body)
	}
	if alert, add := strings.Index(r.body, `role="alert"`), strings.Index(r.body, `id="add"`); alert > add {
		t.Fatal("the error is shown in the ICS section, not next to the connection form")
	}
	if strings.Contains(r.body, "pw-not-accepted-1") {
		t.Fatal("the page echoes the password back")
	}

	r = b.do(http.MethodPost, "/connections", url.Values{"provider": {"caldav"}, "url": {srv.URL}, "username": {caldavtest.User}, "password": {caldavtest.Password}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusOK || !strings.Contains(r.body, "Family") || !strings.Contains(r.body, "School") {
		t.Fatalf("connect = %d\n%s", r.status, r.body)
	}
	h := mustHousehold(t, c)
	conns, _ := b.db.Connections(h.ID)
	if len(conns) != 1 {
		t.Fatalf("connections = %+v", conns)
	}
	// The selection page offers a tick box per calendar.
	if strings.Count(r.body, `name="calendar"`) != 2 {
		t.Fatalf("selection page lacks tick boxes:\n%s", r.body)
	}

	r = b.post("/connections/"+conns[0].ID+"/calendars", url.Values{"calendar": {family}, "name_" + family: {"Family"}, "color_" + family: {"#B4583A"}, "interval": {"5"}})
	if r.status != http.StatusSeeOther || r.location != "/calendars" {
		t.Fatalf("add ticked = %d %q\n%s", r.status, r.location, r.body)
	}
	page := b.get("/calendars")
	if !strings.Contains(page.body, "Family") || strings.Contains(page.body, "School") || !strings.Contains(page.body, "#B4583A") || !strings.Contains(page.body, caldavtest.User) {
		t.Fatalf("calendars page:\n%s", page.body)
	}
	sameList(t, occTitles(c.occurrences(t, november)), []string{"11-20T15:00 Swimming"})

	// Ticking a calendar that is already synced changes nothing and is no error.
	if r = b.post("/connections/"+conns[0].ID+"/calendars", url.Values{"calendar": {family}, "name_" + family: {"Family"}, "interval": {"5"}}); r.status != http.StatusSeeOther {
		t.Fatalf("keep = %d\n%s", r.status, r.body)
	}
	if cals, _ := b.db.SyncedCalendars(h.ID); len(cals) != 1 {
		t.Fatalf("calendars = %+v", cals)
	}
	// An address on another server says so too.
	r = b.do(http.MethodPost, "/connections/"+conns[0].ID+"/calendars", url.Values{"calendar": {"https://elsewhere.example/cal/"}, "name_https://elsewhere.example/cal/": {"X"}, "interval": {"5"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "server of this connection") || strings.Contains(r.body, "valid address") {
		t.Fatalf("other server = %d\n%s", r.status, r.body)
	}

	if r := b.post("/connections/"+conns[0].ID+"/delete", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("remove = %d", r.status)
	}
	if got := len(c.occurrences(t, november)); got != 0 {
		t.Fatalf("events after removal = %d", got)
	}
}

func TestConnectionPagesNeedASignedInParent(t *testing.T) {
	b, _ := configuredBox(t)
	if r := b.post("/connections", url.Values{"provider": {"caldav"}}); r.status != http.StatusSeeOther || r.location != "/login" {
		t.Fatalf("signed out = %d %q", r.status, r.location)
	}
}

// hostedRemote is a server that, like iCloud, lists calendars on other hosts.
type hostedRemote struct {
	calsync.Remote
	found []caldavclient.Calendar
}

func (h hostedRemote) Calendars(context.Context) ([]caldavclient.Calendar, error) {
	return h.found, nil
}
func (h hostedRemote) List(context.Context, string) ([]caldavclient.Object, error) {
	return nil, nil
}

func (h hostedRemote) Fetch(context.Context, string, []string) ([]caldavclient.Object, error) {
	return nil, nil
}

func TestDiscoveredCalendarOnAnotherHostOfTheServersDomainCanBeAdded(t *testing.T) {
	c := signedInAPI(t)
	home := "https://p123-caldav.example.org/1/calendars/home/"
	c.b.sync.SetDialer(func(serverURL, user, pass string) (calsync.Remote, error) {
		return hostedRemote{found: []caldavclient.Calendar{{URL: home, Name: "Home"}}}, nil
	})
	conn := c.connect(t, map[string]any{"provider": "caldav", "url": "https://caldav.example.org", "username": "u", "password": "p"})
	id := conn["id"].(string)
	status, cal, _ := c.call(http.MethodPost, "/calendar-connections/"+id+"/calendars", map[string]any{"url": home, "name": "Home"})
	if status != http.StatusCreated {
		t.Fatalf("add calendar on a second host = %d %v", status, cal)
	}
	for name, foreign := range map[string]string{
		"foreign domain": "https://calendar.evil.test/home/",
		"http downgrade": "http://p123-caldav.example.org/1/calendars/other/",
	} {
		status, body, _ := c.call(http.MethodPost, "/calendar-connections/"+id+"/calendars", map[string]any{"url": foreign, "name": "X"})
		if status != http.StatusUnprocessableEntity {
			t.Errorf("%s: add = %d %v, want 422", name, status, body)
		}
	}
}

// iCloud has one fixed address and no single-calendar mode. The form hides
// those two fields with a small script; without it they are still sent, and
// the server must ignore them.
func TestConnectionFormMarksFieldsOnlyOtherServersNeed(t *testing.T) {
	c := signedInAPI(t)
	r := c.b.get("/calendars")
	if r.status != http.StatusOK {
		t.Fatalf("calendars = %d", r.status)
	}
	if n := strings.Count(r.body, "data-other-only"); n != 2 {
		t.Fatalf("%d fields marked other-server-only, want 2 (address, single calendar):\n%s", n, r.body)
	}
	if !strings.Contains(r.body, `src="/static/connection-form.js"`) {
		t.Fatal("the page does not load the form script")
	}
	if js := c.b.get("/static/connection-form.js"); js.status != http.StatusOK || !strings.Contains(js.body, "data-other-only") {
		t.Fatalf("form script = %d", js.status)
	}
	if css := c.b.get("/static/hejmo.css"); !strings.Contains(css.body, "overflow-wrap: anywhere") || !strings.Contains(css.body, "[hidden]") {
		t.Fatal("stylesheet lacks wrapping of long words or the hidden rule")
	}
}

type oneCalendarRemote struct {
	calendarAsked *bool
}

func (o oneCalendarRemote) Calendars(context.Context) ([]caldavclient.Calendar, error) {
	return []caldavclient.Calendar{{URL: "https://caldav.icloud.com/1/calendars/home/", Name: "Home"}}, nil
}
func (o oneCalendarRemote) Calendar(context.Context, string) (caldavclient.Calendar, error) {
	*o.calendarAsked = true
	return caldavclient.Calendar{}, errors.New("single-calendar mode must not be used for iCloud")
}
func (o oneCalendarRemote) List(context.Context, string) ([]caldavclient.Object, error) {
	return nil, nil
}
func (o oneCalendarRemote) Fetch(context.Context, string, []string) ([]caldavclient.Object, error) {
	return nil, nil
}
func (o oneCalendarRemote) Put(context.Context, string, string, string) (string, error) {
	return "", errors.New("not used")
}
func (o oneCalendarRemote) Delete(context.Context, string, string) error {
	return errors.New("not used")
}
func (o oneCalendarRemote) CountEvents(context.Context, string, time.Time) (int, error) {
	return 0, nil
}

func TestWebICloudIgnoresAddressAndSingleCalendarFlag(t *testing.T) {
	c := signedInAPI(t)
	var asked string
	singleUsed := false
	c.b.sync.SetDialer(func(serverURL, user, pass string) (calsync.Remote, error) {
		asked = serverURL
		return oneCalendarRemote{&singleUsed}, nil
	})
	r := c.b.post("/connections", url.Values{
		"provider": {"icloud"}, "url": {"https://somewhere.example/dav"}, "single_calendar": {"1"},
		"username": {"mama@example.org"}, "password": {"abcd-efgh-ijkl-mnop"},
	})
	if r.status != http.StatusOK || asked != "https://caldav.icloud.com" || singleUsed {
		t.Fatalf("status %d, asked %q, single mode used %v\n%s", r.status, asked, singleUsed, r.body)
	}
	conns, _ := c.b.db.Connections(mustHousehold(t, c).ID)
	if len(conns) != 1 || conns[0].URL != "https://caldav.icloud.com" || conns[0].Single {
		t.Fatalf("stored connection = %+v", conns)
	}
}

func TestWebICloudErrorDoesNotEchoHiddenFields(t *testing.T) {
	c := signedInAPI(t)
	c.b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) { return nil, errors.New("offline in test") })
	r := c.b.post("/connections", url.Values{
		"provider": {"icloud"}, "url": {"https://somewhere.example/dav"}, "single_calendar": {"1"},
		"username": {"mama@example.org"}, "password": {"abcd-efgh-ijkl-mnop"},
	})
	// Only the connection form is checked: the page's calendar form has its own
	// defaults ("Whole family" starts ticked).
	form := formOf(t, r.body, "/connections")
	if r.status != http.StatusUnprocessableEntity || strings.Contains(r.body, "somewhere.example") || strings.Contains(form, " checked") {
		t.Fatalf("error page = %d\n%s", r.status, r.body)
	}
}

// errRemote fails to list calendars with the given error.
type errRemote struct {
	calsync.Remote
	err error
}

func (e errRemote) Calendars(context.Context) ([]caldavclient.Calendar, error) { return nil, e.err }

func TestWebConnectExplainsAServerThatPointsToAnotherDomain(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"foreign domain": {&caldavclient.ForeignHostError{Host: "evil.test", Domain: "example.org"}, "andere Domain"},
		"http downgrade": {&caldavclient.ForeignHostError{Host: "p1.example.org", Domain: "example.org", Downgrade: true}, "unverschlüsselte"},
	} {
		t.Run(name, func(t *testing.T) {
			c := signedInAPI(t)
			c.b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) { return errRemote{err: tc.err}, nil })
			r := c.b.post("/connections", url.Values{
				"provider": {"caldav"}, "url": {"https://caldav.example.org"}, "username": {"u"}, "password": {"secret-pw-1"},
			})
			if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, tc.want) {
				t.Fatalf("connect = %d, want 422 saying %q\n%s", r.status, tc.want, r.body)
			}
			if strings.Contains(r.body, "secret-pw-1") || strings.Contains(r.body, "refused to contact") {
				t.Fatalf("page leaks the password or the raw error:\n%s", r.body)
			}
			if conns, _ := c.b.db.Connections(mustHousehold(t, c).ID); len(conns) != 0 {
				t.Fatalf("a refused connection was stored: %+v", conns)
			}
		})
	}
}
