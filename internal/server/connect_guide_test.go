package server_test

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/calsync"
)

// Ticket #135: connecting has its own page with a guide per service.

var (
	anchorRe  = regexp.MustCompile(`(?s)<a\b[^>]*>.*?</a>`)
	tagRe     = regexp.MustCompile(`(?s)<[^>]*>`)
	addressRe = regexp.MustCompile(`(?i)https?://[a-z0-9]|www\.|\b(apple|nextcloud)\.com\b`)
)

// connectBlock is the part of the page inside the .connect block.
func connectBlock(t *testing.T, page string) string {
	t.Helper()
	i := strings.Index(page, `<div class="connect">`)
	if i < 0 {
		t.Fatalf("no .connect block:\n%s", page)
	}
	return page[i:]
}

func TestConnectNewShowsBothGuidesWithExactlyOneServiceChecked(t *testing.T) {
	b := signedInAPI(t).b
	for path, want := range map[string]string{
		"/connections/new":                "icloud",
		"/connections/new?service=icloud": "icloud",
		"/connections/new?service=caldav": "caldav",
		"/connections/new?service=bogus":  "icloud",
	} {
		r := b.get(path)
		if r.status != http.StatusOK {
			t.Fatalf("%s = %d\n%s", path, r.status, r.body)
		}
		block := connectBlock(t, r.body)
		if n := strings.Count(block, `name="service"`); n != 2 {
			t.Errorf("%s: %d service radios, want 2", path, n)
		}
		checked := regexp.MustCompile(`<input type="radio" name="service" value="(\w+)" checked>`).FindAllStringSubmatch(block, -1)
		if len(checked) != 1 || checked[0][1] != want {
			t.Errorf("%s: checked = %v, want only %s", path, checked, want)
		}
		for _, svc := range []string{"icloud", "caldav"} {
			if !strings.Contains(block, `<form class="guide" data-service="`+svc+`" method="post" action="/connections"`) {
				t.Errorf("%s: no guide form for %s", path, svc)
			}
			if !strings.Contains(block, `<input type="hidden" name="provider" value="`+svc+`">`) {
				t.Errorf("%s: no provider input for %s", path, svc)
			}
		}
		if strings.Contains(block, `form="`) {
			t.Errorf("%s: the choice cards must not use a form attribute", path)
		}
		if !strings.Contains(r.body, `<a class="back" href="/calendars">Zurück zu den Kalendern</a>`) {
			t.Errorf("%s: no frame back link to the calendars:\n%s", path, r.body)
		}
		if !strings.Contains(r.body, "<h1>Kalender verbinden</h1>") {
			t.Errorf("%s: no title", path)
		}
		if strings.Contains(r.body, "connection-form.js") || strings.Contains(r.body, "data-other-only") {
			t.Errorf("%s: the old form script is still used", path)
		}
	}
}

func TestConnectNewNeedsAParent(t *testing.T) {
	c := signedInAPI(t)
	c.b.logout()
	if r := c.b.get("/connections/new"); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Fatalf("signed out = %d %q", r.status, r.location)
	}
}

func TestConnectGuideLinksOpenInANewTab(t *testing.T) {
	b := signedInAPI(t).b
	for lang, want := range map[string][]string{
		"de": {"https://account.apple.com", "https://support.apple.com/de-de/102654", "https://docs.nextcloud.com/server/latest/user_manual/de/groupware/calendar.html"},
		"en": {"https://account.apple.com", "https://support.apple.com/en-us/102654", "https://docs.nextcloud.com/server/latest/user_manual/en/groupware/calendar.html"},
	} {
		r := b.do(http.MethodGet, "/connections/new", nil, map[string]string{"Accept-Language": lang})
		block := connectBlock(t, r.body)
		for _, href := range want {
			re := regexp.MustCompile(`<a [^>]*href="` + regexp.QuoteMeta(href) + `"[^>]*>`)
			a := re.FindString(block)
			if a == "" {
				t.Errorf("%s: no link to %s", lang, href)
				continue
			}
			if !strings.Contains(a, `target="_blank"`) || !strings.Contains(a, `rel="noopener"`) {
				t.Errorf("%s: link %s lacks target=_blank rel=noopener: %s", lang, href, a)
			}
		}
		if !strings.Contains(block, `class="button secondary out"`) || !strings.Contains(block, `class="vh"`) {
			t.Errorf("%s: link buttons lack the .out style or the screen-reader note", lang)
		}
	}
}

// The product owner's rule (15/11/2026): an address in a guide is a link, never plain text.
func TestConnectGuideHasNoBareAddress(t *testing.T) {
	b := signedInAPI(t).b
	for _, lang := range []string{"de", "en"} {
		r := b.do(http.MethodGet, "/connections/new?service=caldav", nil, map[string]string{"Accept-Language": lang})
		text := tagRe.ReplaceAllString(anchorRe.ReplaceAllString(connectBlock(t, r.body), " "), " ")
		if m := addressRe.FindString(text); m != "" {
			t.Errorf("%s: bare address %q in the guide text:\n%s", lang, m, text)
		}
		if strings.Contains(r.body, "appleid.apple.com") {
			t.Errorf("%s: the old address appleid.apple.com is still shown", lang)
		}
	}
}

func TestConnectGuideFields(t *testing.T) {
	b := signedInAPI(t).b
	block := connectBlock(t, b.get("/connections/new").body)
	icloud := block[:strings.Index(block, `data-service="caldav"`)]
	for name, attrs := range map[string][]string{
		"username": {`type="email"`, `autocomplete="username"`, `autocapitalize="none"`, `spellcheck="false"`},
		"password": {`type="password"`, `autocomplete="off"`},
	} {
		tag := regexp.MustCompile(`<input name="` + name + `"[^>]*>`).FindString(icloud)
		if tag == "" {
			t.Errorf("iCloud guide has no %s input", name)
		}
		for _, a := range attrs {
			if !strings.Contains(tag, a) {
				t.Errorf("iCloud %s input lacks %s: %s", name, a, tag)
			}
		}
	}
	for _, want := range []string{
		"E-Mail-Adresse deines Apple Accounts", "App-spezifisches Passwort",
		"Vier Gruppen aus je vier Buchstaben, durch Bindestriche getrennt.",
		"Mit iCloud verbinden", "Verbinden und Kalender suchen",
		"Adresse des Servers", "Weitere Optionen", "Ganzer Server", "Du findest „App-spezifische Passwörter“ nicht?",
		`name="single_calendar"`, `<p class="why">`, `<ol class="howto">`, `<p class="done-hint">`,
	} {
		if !strings.Contains(block, want) {
			t.Errorf("guide lacks %q", want)
		}
	}
}

func TestConnectICloudLoginFailureShowsAtThePasswordAndKeepsTheUsername(t *testing.T) {
	b := signedInAPI(t).b
	b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) {
		return errRemote{err: &caldavclient.StatusError{Code: http.StatusUnauthorized}}, nil
	})
	long := "konstantin-alexander.mueller-luedenscheidt@icloud.com"
	r := b.post("/connections", url.Values{"provider": {"icloud"}, "username": {long}, "password": {"secret-pw-1"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d\n%s", r.status, r.body)
	}
	block := connectBlock(t, r.body)
	if !strings.Contains(r.body, `<input type="radio" name="service" value="icloud" checked>`) {
		t.Error("the iCloud card is not checked")
	}
	form := formOf(t, block, "/connections")
	if strings.Count(r.body, `class="ferr"`) != 1 || !strings.Contains(form, `id="err-password"`) || !strings.Contains(form, `aria-describedby="err-password"`) {
		t.Fatalf("want one error at the password:\n%s", form)
	}
	if !strings.Contains(r.body, "iCloud hat die Anmeldung abgelehnt.") || !strings.Contains(r.body, "erstelle in Schritt 3 ein neues") {
		t.Errorf("error text missing:\n%s", form)
	}
	if !strings.Contains(r.body, `value="`+long+`"`) {
		t.Error("the email address was not kept")
	}
	if strings.Contains(r.body, "secret-pw-1") || regexp.MustCompile(`name="password"[^>]*value=`).MatchString(r.body) {
		t.Error("the password came back")
	}
	// The error sits directly under the field, before its hint.
	e, h := strings.Index(form, `class="ferr"`), strings.Index(form, "Vier Gruppen")
	if e < 0 || h < 0 || e > h {
		t.Errorf("error at %d, hint at %d: the error must come first", e, h)
	}
	if strings.Contains(r.body, "appleid.apple.com") {
		t.Error("old address in the error")
	}
}

func TestConnectCalDAVLoginFailureDoesNotMentionApple(t *testing.T) {
	b := signedInAPI(t).b
	b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) {
		return errRemote{err: &caldavclient.StatusError{Code: http.StatusUnauthorized}}, nil
	})
	r := b.post("/connections", url.Values{"provider": {"caldav"}, "url": {"https://caldav.example.org"}, "username": {"u"}, "password": {"p"}})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-password"`) {
		t.Fatalf("status %d\n%s", r.status, r.body)
	}
	if strings.Contains(r.body, "app-spezifische") && strings.Contains(r.body, "Der Server hat Benutzername oder Passwort abgelehnt. Bei iCloud") {
		t.Error("a CalDAV login failure shows the generic text that sends the user to Apple")
	}
	if !strings.Contains(r.body, "Schritt 2") {
		t.Errorf("the error does not point to step 2:\n%s", r.body)
	}
}

// downRemote fails both ways of finding calendars (the whole server, one calendar).
type downRemote struct{ errRemote }

func (d downRemote) Calendar(context.Context, string) (caldavclient.Calendar, error) {
	return caldavclient.Calendar{}, d.err
}

func TestConnectUnreachableServerShowsAtTheAddressField(t *testing.T) {
	b := signedInAPI(t).b
	b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) {
		return downRemote{errRemote{err: &caldavclient.StatusError{Code: http.StatusNotFound}}}, nil
	})
	r := b.post("/connections", url.Values{"provider": {"caldav"}, "url": {"https://cloud.example.org/remote.php/da"}, "username": {"mama"}, "password": {"pw-1"}, "single_calendar": {"1"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d\n%s", r.status, r.body)
	}
	if !strings.Contains(r.body, `<input type="radio" name="service" value="caldav" checked>`) {
		t.Error("the other-server card is not checked")
	}
	if strings.Count(r.body, `class="ferr"`) != 1 || !strings.Contains(r.body, `id="err-url"`) {
		t.Fatalf("want one error at the address:\n%s", r.body)
	}
	if !strings.Contains(r.body, "Unter dieser Adresse antwortet kein Kalender-Server. Prüfe die Adresse aus Schritt 1; sie beginnt mit https://.") {
		t.Error("error text not as designed")
	}
	for _, want := range []string{`value="https://cloud.example.org/remote.php/da"`, `value="mama"`, `name="single_calendar" value="1" checked`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("lost %q", want)
		}
	}
	if strings.Contains(r.body, "pw-1") {
		t.Error("the password came back")
	}
}

func TestConnectSuccessLineAndFrameBackLinkOnTheChoosePage(t *testing.T) {
	b := signedInAPI(t).b
	b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) {
		return hostedRemote{found: []caldavclient.Calendar{{URL: flowCalendar, Name: "Home"}, {URL: flowCalendar2, Name: "Work"}}}, nil
	})
	r := b.post("/connections", url.Values{"provider": {"icloud"}, "username": {"mama@example.org"}, "password": {"abcd-efgh-ijkl-mnop"}})
	if r.status != http.StatusOK {
		t.Fatalf("status %d\n%s", r.status, r.body)
	}
	if !strings.Contains(r.body, "Mit iCloud verbunden. 2 Kalender gefunden: Schalte ein, was die Box zeigen soll.") {
		t.Errorf("no success line:\n%s", r.body)
	}
	if !strings.Contains(r.body, `<a class="back" href="/calendars">Zurück zu den Kalendern</a>`) || strings.Contains(r.body, `<p><a href="/calendars">`) {
		t.Error("the choose page has no frame back link")
	}
	r = b.post("/connections", url.Values{"provider": {"caldav"}, "url": {"https://caldav.example.org"}, "username": {"u"}, "password": {"pw-1"}})
	if !strings.Contains(r.body, "Verbunden. 2 Kalender gefunden: Schalte ein, was die Box zeigen soll.") {
		t.Errorf("no success line for CalDAV:\n%s", r.body)
	}
	if one := b.post("/connections", url.Values{"provider": {"caldav"}, "url": {"https://caldav.example.org"}, "username": {"u2"}, "password": {"pw-1"}}); !strings.Contains(one.body, "Verbunden.") {
		t.Error("success line missing")
	}
}

func TestCalendarsPageLinksToTheConnectPage(t *testing.T) {
	b := signedInAPI(t).b
	r := b.get("/calendars")
	for _, want := range []string{"iCloud und andere Kalender-Server", "Noch keine Verbindung. Verbinde iCloud oder einen anderen Kalender-Server, dann erscheinen eure Termine auf der Box.", `<a class="button" href="/connections/new">Kalender verbinden</a>`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("empty state lacks %q", want)
		}
	}
	for _, gone := range []string{`action="/connections"`, "data-connection-form", "connection-form.js", `name="provider"`} {
		if strings.Contains(r.body, gone) {
			t.Errorf("calendars page still has %q", gone)
		}
	}
	// One card per connection, then a secondary button for another one.
	b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) {
		return hostedRemote{found: []caldavclient.Calendar{{URL: flowCalendar, Name: "Home"}}}, nil
	})
	b.post("/connections", url.Values{"provider": {"icloud"}, "username": {"mama@example.org"}, "password": {"abcd-efgh-ijkl-mnop"}})
	r = b.get("/calendars")
	for _, want := range []string{"<strong>iCloud</strong>", `<span class="hint">mama@example.org</span>`, `class="button secondary"`, "Kalender auswählen", `<a class="button secondary" href="/connections/new">Weiteren Kalender verbinden</a>`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("connection card lacks %q:\n%s", want, r.body)
		}
	}
	if strings.Contains(r.body, "data-empty") && strings.Contains(r.body, "Noch keine Verbindung") {
		t.Error("empty state shown next to a connection")
	}
}
