package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/i18n"
)

// acceptOnly makes the stub CalDAV server accept one password and refuse the
// others the way a real server does after an app-specific password is revoked.
func acceptOnly(b *box, password string) {
	good := hostedRemote{found: []caldavclient.Calendar{{URL: flowCalendar, Name: "Home"}, {URL: flowCalendar + "2/", Name: "Work"}}}
	b.sync.SetDialer(func(_, _, pass string) (calsync.Remote, error) {
		if pass != password {
			return errRemote{err: &caldavclient.StatusError{Code: http.StatusUnauthorized, Status: "401 Unauthorized"}}, nil
		}
		return good, nil
	})
}

// chosenConnection connects, switches Home on and returns the connection's id.
func chosenConnection(t *testing.T, b *box) string {
	t.Helper()
	id := connectedBox(t, b)
	form := url.Values{"calendar": {flowCalendar}, "shown": {flowCalendar, flowCalendar + "2/"}, "name_" + flowCalendar: {"Home"}, "interval": {"60"}}
	if r := b.post("/connections/"+id+"/calendars", form); r.status != http.StatusSeeOther {
		t.Fatalf("choose = %d\n%s", r.status, r.body)
	}
	return id
}

func TestConnectionOverviewRowsShowServiceCalendarsAndLastSync(t *testing.T) {
	b := signedInBox(t)
	id := chosenConnection(t, b)
	body := getEN(b, "/calendars").body
	for _, want := range []string{`<a href="/connections/` + id + `">`, "<b>u</b>", "Nextcloud/CalDAV: caldav.example.org", "On the box: Home"} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q\n%s", want, body)
		}
	}
	for _, gone := range []string{"/connections/" + id + "/delete", "Choose calendars</a>"} {
		if strings.Contains(body, gone) {
			t.Errorf("overview still has %q", gone)
		}
	}
	noDestructiveForm(t, "/calendars", body)
}

func TestConnectionDetailHoldsFactsChooseCalendarsPasswordAndDisconnect(t *testing.T) {
	b := signedInBox(t)
	id := chosenConnection(t, b)
	acceptOnly(b, "pw-1")
	r := getEN(b, "/connections/"+id)
	if r.status != http.StatusOK {
		t.Fatalf("GET = %d\n%s", r.status, r.body)
	}
	for _, want := range []string{
		`<a class="back" href="/calendars">`, "Back to calendar connections", "<h1>u</h1>",
		"<dt>Service</dt>", "<dt>User name</dt>", "<dt>Last synced</dt>", "<dt>Connected since</dt>",
		`<form method="post" action="/connections/` + id + `/calendars" class="steps"`, `class="calchoice"`, `role="switch"`, `name="calendar"`,
		"Save", `<section id="login"`, `name="password"`, "Check and save password", `action="/connections/` + id + `/credentials"`,
		`<details class="danger">`, "Disconnect", "<strong>Home</strong>", `action="/connections/` + id + `/delete"`, "Yes, disconnect", "No, change nothing",
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("detail lacks %q\n%s", want, r.body)
		}
	}
	if strings.Contains(r.body, `<a class="back" href="/settings">`) {
		t.Error("the detail page also shows the hub link")
	}
	if strings.Contains(r.body, `<section id="login" class="steps"><details class="more" open>`) {
		t.Error("the credentials section is open without a refused login")
	}
	if c := b.get("/connections/nope"); c.status != http.StatusNotFound {
		t.Errorf("unknown connection = %d", c.status)
	}
}

func TestARefusedLoginIsToldWithTheTimeAndLinksToTheCredentials(t *testing.T) {
	b := signedInBox(t)
	b.now = time.Date(2026, 11, 14, 21, 10, 5, 0, time.UTC) // 22:10:05 in Munich
	id := chosenConnection(t, b)
	h, _ := b.db.HomeHousehold()
	cals, _ := b.db.SyncedCalendars(h.ID)
	if err := b.db.FailSync(h.ID, cals[0].ID, i18n.Msg("sync.fail.login"), b.now, b.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	acceptOnly(b, "other") // discovery with the stored password is refused now
	for _, page := range []string{"/connections/" + id, "/calendars"} {
		body := getEN(b, page).body
		if !strings.Contains(body, `class="flag problem"`) && page == "/calendars" {
			t.Errorf("%s: no problem flag\n%s", page, body)
		}
		if page != "/calendars" {
			for _, want := range []string{`class="error"`, "14/11/2026, 22:10:05", `href="#login"`, `<section id="login" class="steps"><details class="more" open>`} {
				if !strings.Contains(body, want) {
					t.Errorf("%s lacks %q\n%s", page, want, body)
				}
			}
		}
	}
}

func TestNewPasswordIsCheckedAgainstTheServerAndStoredEncrypted(t *testing.T) {
	b := signedInBox(t)
	id := chosenConnection(t, b)
	acceptOnly(b, "pw-1")
	h, _ := b.db.HomeHousehold()

	r := b.do(http.MethodPost, "/connections/"+id+"/credentials", url.Values{"password": {"wrong-pw"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-password"`) || !strings.Contains(r.body, `<section id="login" class="steps"><details class="more" open>`) {
		t.Fatalf("refused password = %d, no error at the field or section closed\n%s", r.status, r.body)
	}
	if strings.Contains(r.body, "wrong-pw") {
		t.Error("the page gives the password back")
	}
	if _, pw, _ := b.db.ConnectionCredentials(h.ID, id); pw != "pw-1" {
		t.Errorf("a refused password was stored: %q", pw)
	}

	if r := b.post("/connections/"+id+"/credentials", url.Values{"password": {""}}); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-password"`) {
		t.Errorf("empty password = %d", r.status)
	}

	acceptOnly(b, "new-pw")
	r = b.post("/connections/"+id+"/credentials", url.Values{"password": {"new-pw"}})
	if r.status != http.StatusSeeOther || r.location != "/connections/"+id {
		t.Fatalf("accepted password = %d to %q\n%s", r.status, r.location, r.body)
	}
	if _, pw, _ := b.db.ConnectionCredentials(h.ID, id); pw != "new-pw" {
		t.Errorf("stored password = %q, want the new one", pw)
	}
	var sealed string
	if err := b.db.SQL().QueryRow(`SELECT credential FROM calendar_connections WHERE id = ?`, id).Scan(&sealed); err != nil || strings.Contains(sealed, "new-pw") {
		t.Errorf("the password is not stored encrypted: %q %v", sealed, err)
	}
	if got := successLine(getEN(b, "/connections/"+id).body); got != "Password checked and saved." {
		t.Errorf("success line = %q", got)
	}
	if r := b.post("/connections/nope/credentials", url.Values{"password": {"x"}}); r.status != http.StatusNotFound {
		t.Errorf("unknown connection = %d", r.status)
	}
}

func TestDisconnectingNamesTheConnectionInTheSuccessLine(t *testing.T) {
	b := signedInBox(t)
	id := chosenConnection(t, b)
	if r := b.post("/connections/"+id+"/delete", url.Values{}); r.status != http.StatusSeeOther || r.location != "/calendars" {
		t.Fatalf("disconnect = %d %q", r.status, r.location)
	}
	if got := successLine(getEN(b, "/calendars").body); got != "“u” disconnected." {
		t.Errorf("success line = %q", got)
	}
}

func TestSwitchingACalendarOffStillAsksAndCancelsToTheConnectionPage(t *testing.T) {
	b := signedInBox(t)
	id := chosenConnection(t, b)
	form := url.Values{"shown": {flowCalendar, flowCalendar + "2/"}, "interval": {"60"}, "calendar": {flowCalendar + "2/"}, "name_" + flowCalendar + "2/": {"Work"}}
	r := b.do(http.MethodPost, "/connections/"+id+"/calendars", form, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusOK || !strings.Contains(r.body, `href="/connections/`+id+`"`) {
		t.Fatalf("switch off = %d\n%s", r.status, r.body)
	}
}

func TestConnectionPasswordWorksThroughTheAPI(t *testing.T) {
	c := signedInAPI(t)
	id := chosenConnection(t, c.b)
	acceptOnly(c.b, "new-pw")
	if status, _, _ := c.call(http.MethodPut, "/calendar-connections/"+id+"/credentials", map[string]any{"password": "wrong"}); status != http.StatusUnprocessableEntity {
		t.Errorf("refused password = %d, want 422", status)
	}
	if status, _, _ := c.call(http.MethodPut, "/calendar-connections/"+id+"/credentials", map[string]any{"password": "new-pw"}); status != http.StatusNoContent {
		t.Errorf("accepted password = %d, want 204", status)
	}
	h := mustHousehold(t, c)
	if _, pw, _ := c.b.db.ConnectionCredentials(h.ID, id); pw != "new-pw" {
		t.Errorf("stored password = %q", pw)
	}
	if status, _, _ := c.call(http.MethodPut, "/calendar-connections/00000000-0000-4000-8000-000000000000/credentials", map[string]any{"password": "x"}); status != http.StatusNotFound {
		t.Errorf("unknown connection = %d, want 404", status)
	}
	ro := c.withToken(c.newToken("read"))
	if status, _, _ := ro.call(http.MethodPut, "/calendar-connections/"+id+"/credentials", map[string]any{"password": "new-pw"}); status != http.StatusForbidden {
		t.Errorf("read-only token = %d, want 403", status)
	}
}

func TestAnOnCalendarShowsItsLastSyncUnderTheSwitch(t *testing.T) {
	b := signedInBox(t)
	id := chosenConnection(t, b)
	acceptOnly(b, "pw-1")
	body := getEN(b, "/connections/"+id).body
	home := body[strings.Index(body, `<b>Home</b>`):]
	home = home[:strings.Index(home, `name="name_`)]
	if !strings.Contains(home, `<span class="status">Last synced `) {
		t.Errorf("the calendar that is on shows no last sync\n%s", home)
	}
	work := body[strings.Index(body, `<b>Work</b>`):]
	work = work[:strings.Index(work, `name="name_`)]
	if strings.Contains(work, "Last synced") {
		t.Errorf("a calendar that is off shows a last sync\n%s", work)
	}
}
