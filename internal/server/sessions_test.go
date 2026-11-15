package server_test

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Issue #108: a parent sees the browsers people are signed in with and ends
// another one; the ended browser is refused at its next request.

const (
	uaMacFirefox     = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:127.0) Gecko/20100101 Firefox/127.0"
	uaIPhoneSafari   = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1"
	uaAndroidChrome  = "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36"
	uaWindowsEdge    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36 Edg/126.0.0.0"
	mamaPassword     = "correct horse battery"
	papaPassword     = "papa's long password"
	sessionsSecretUA = "Mozilla/5.0 (SecretDevice-4711; Win64)"
)

// browser is one browser of its own: its own cookies and its User-Agent.
type browser struct {
	b      *box
	client *http.Client
	ua     string
}

type browserResp struct {
	resp
	cookies []*http.Cookie
}

// fresh drops the sessions the setup wizard left behind, so a test starts with
// no sign-in at all.
func fresh(t *testing.T, b *box) {
	t.Helper()
	if _, err := b.db.SQL().Exec(`DELETE FROM sessions WHERE phone_id IS NULL`); err != nil {
		t.Fatal(err)
	}
}

func newBrowser(b *box, ua string) *browser {
	return &browser{b: b, client: b.trustingClient(), ua: ua}
}

func (br *browser) do(method, path string, form url.Values, lang string) browserResp {
	br.b.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, br.b.srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", br.b.srv.URL)
	}
	req.Header.Set("User-Agent", br.ua)
	if lang != "" {
		req.Header.Set("Accept-Language", lang)
	}
	res, err := br.client.Do(req)
	if err != nil {
		br.b.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return browserResp{resp{res.StatusCode, res.Header.Get("Location"), string(raw)}, res.Cookies()}
}

func (br *browser) get(path, lang string) browserResp { return br.do(http.MethodGet, path, nil, lang) }

func (br *browser) post(path string, form url.Values) browserResp {
	return br.do(http.MethodPost, path, form, "")
}

func (br *browser) signIn(t *testing.T, user, password, secret string, at time.Time) {
	t.Helper()
	br.b.now = at
	code, _ := auth.TOTPCode(secret, at)
	if r := br.post("/login", url.Values{"username": {user}, "password": {password}, "code": {code}}); r.status != http.StatusSeeOther {
		t.Fatalf("%s signs in = %d\n%s", user, r.status, r.body)
	}
}

// sessionsBox has Mama signed in on a Mac with Firefox (the browser the test
// drives), on an iPhone with Safari, and Papa on an Android phone with Chrome.
type sessionsBox struct {
	*box
	mama, mamaPhone, papa *browser
	papaPerson            store.Person
	mamaSecret, papaSec   string
}

func newSessionsBox(t *testing.T) sessionsBox {
	t.Helper()
	b, mamaSecret := configuredBox(t)
	fresh(t, b)
	papaPerson, papaSecret := secondParent(t, b)
	s := sessionsBox{box: b, mamaSecret: mamaSecret, papaSec: papaSecret, papaPerson: papaPerson}
	utc := func(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, time.UTC) }
	s.mama = newBrowser(b, uaMacFirefox)
	s.mama.signIn(t, "mama", mamaPassword, mamaSecret, utc(2, 7, 14))
	s.mamaPhone = newBrowser(b, uaIPhoneSafari)
	s.mamaPhone.signIn(t, "mama", mamaPassword, mamaSecret, time.Date(2026, 10, 28, 19, 30, 0, 0, time.UTC))
	s.b().now = utc(13, 20, 5)
	s.mamaPhone.get("/", "en")
	s.papa = newBrowser(b, uaAndroidChrome)
	s.papa.signIn(t, "papa", papaPassword, papaSecret, utc(11, 18, 2))
	s.b().now = utc(14, 6, 48)
	s.papa.get("/", "en")
	s.b().now = utc(14, 12, 0)
	return s
}

func (s sessionsBox) b() *box { return s.box }

// idOf is the public id of the browser session with the device label.
func (s sessionsBox) idOf(t *testing.T, device string) string {
	t.Helper()
	h, _ := s.db.HomeHousehold()
	list, err := s.db.ListBrowserSessions(h.ID, s.now)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range list {
		if x.Device == device {
			return x.ID
		}
	}
	t.Fatalf("no session %q in %+v", device, list)
	return ""
}

func inOrder(t *testing.T, page string, parts ...string) {
	t.Helper()
	at := 0
	for _, p := range parts {
		i := strings.Index(page[at:], p)
		if i < 0 {
			t.Fatalf("page has no %q after %q:\n%s", p, page[:at], page)
		}
		at += i + len(p)
	}
}

func TestSettingsHubListsSignedInDevicesForParents(t *testing.T) {
	s := newSessionsBox(t)
	if r := s.mama.get("/settings", "en"); !strings.Contains(r.body, `href="/sessions"`) || !strings.Contains(r.body, "Signed-in devices") {
		t.Fatalf("hub has no row for the sessions:\n%s", r.body)
	}
	// After one-time codes: last.
	r := s.mama.get("/settings", "en")
	inOrder(t, r.body, `href="/otp"`, `href="/sessions"`)
}

func TestSessionsListGroupsBrowsersByPerson(t *testing.T) {
	s := newSessionsBox(t)
	r := s.mama.get("/sessions", "en")
	if r.status != http.StatusOK {
		t.Fatalf("GET /sessions = %d\n%s", r.status, r.body)
	}
	inOrder(t, r.body,
		`<h1>Signed-in devices</h1>`,
		`class="notice"`, `href="/phones"`,
		`class="who-head"`, "Mama",
		`class="cards sessions"`,
		"Mac · Firefox", `<span class="tag">This browser</span>`, "Last used: today, 13:00 · Signed in 02/11/2026",
		"iPhone · Safari", "Last used: yesterday, 21:05 · Signed in 28/10/2026",
		`class="who-head"`, "Papa",
		"Android phone · Chrome", "Last used: today, 07:48 · Signed in 11/11/2026",
	)
	if strings.Count(r.body, `<span class="tag">`) != 1 {
		t.Errorf("only the current browser carries the tag:\n%s", r.body)
	}
	if strings.Contains(r.body, "class=\"empty\"") || strings.Contains(r.body, "data-empty") {
		t.Errorf("empty sentence shown although other browsers are signed in")
	}
	// Rows open the detail page by public id, never by token.
	if m := regexp.MustCompile(`href="/sessions/([0-9a-f-]{32,})"`).FindAllStringSubmatch(r.body, -1); len(m) != 3 {
		t.Errorf("want 3 rows with random ids, got %v", m)
	}
	de := s.mama.get("/sessions", "de").body
	inOrder(t, de, `<h1>Angemeldete Geräte</h1>`, "Mac · Firefox", `<span class="tag">Dieser Browser</span>`,
		"Zuletzt benutzt: heute, 13:00 · Angemeldet am 02.11.2026", "gestern, 21:05", "Android-Handy · Chrome")
}

func TestSessionsListSaysWhenOnlyThisBrowserIsSignedIn(t *testing.T) {
	b, secret := configuredBox(t)
	fresh(t, b)
	mama := newBrowser(b, uaWindowsEdge)
	mama.signIn(t, "mama", mamaPassword, secret, b.now)
	r := mama.get("/sessions", "de")
	if !strings.Contains(r.body, `class="empty"`) || !strings.Contains(r.body, "Nur dieser Browser ist angemeldet.") {
		t.Fatalf("no sentence for the only browser:\n%s", r.body)
	}
	if strings.Contains(strings.SplitN(r.body, `class="empty"`, 2)[1], `class="button`) {
		t.Errorf("the empty state has a button")
	}
	if !strings.Contains(r.body, "Windows-PC · Edge") {
		t.Errorf("list misses the browser:\n%s", r.body)
	}
}

func TestSessionsOfBeforeTheMigrationShowAsUnknown(t *testing.T) {
	b, secret := configuredBox(t)
	fresh(t, b)
	mama := newBrowser(b, uaWindowsEdge)
	mama.signIn(t, "mama", mamaPassword, secret, b.now)
	if _, err := b.db.SQL().Exec(`INSERT INTO sessions (id, token_hash, person_id, created_at, expires_at, device)
		SELECT 'aaaa0000aaaa0000aaaa0000aaaa0000', 'old', person_id, '2026-10-16T08:00:00Z', '2026-12-16T08:00:00Z', '' FROM sessions LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	r := mama.get("/sessions", "de")
	inOrder(t, r.body, "Unbekanntes Gerät", "Zuletzt benutzt: noch nicht erfasst · Angemeldet am 16.10.2026")
	if en := mama.get("/sessions", "en").body; !strings.Contains(en, "Unknown device") || !strings.Contains(en, "Last used: not recorded yet") {
		t.Errorf("english:\n%s", en)
	}
}

func TestSessionDetailOfAnotherBrowserOffersTheConfirmation(t *testing.T) {
	s := newSessionsBox(t)
	id := s.idOf(t, "android_phone|chrome")
	r := s.mama.get("/sessions/"+id, "de")
	if r.status != http.StatusOK {
		t.Fatalf("detail = %d\n%s", r.status, r.body)
	}
	inOrder(t, r.body,
		`class="back" href="/sessions"`, "Zurück zu den angemeldeten Geräten",
		"<h1>Android-Handy · Chrome</h1>",
		`<dl class="facts">`, "Person", "Papa", "Gerät und Browser", "Android-Handy · Chrome",
		"Zuletzt benutzt", "heute, 07:48", "Angemeldet am", "11.11.2026, 19:02", "Meldet sich von selbst ab am", "11.12.2026",
		`<details class="danger">`, "Anmeldung beenden",
		"„Android-Handy · Chrome“ von Papa ist sofort abgemeldet.",
		"Gekoppelte Handys und Kalender-Apps bleiben verbunden.",
		`action="/sessions/`+id+`/end"`, `class="button danger"`, "Ja, Anmeldung beenden")
	if strings.Contains(r.body, `class="tag"`) {
		t.Errorf("another browser carries the own-browser tag")
	}
}

func TestSessionDetailOfTheOwnBrowserOffersSignOutNotEnding(t *testing.T) {
	s := newSessionsBox(t)
	id := s.idOf(t, "mac|firefox")
	r := s.mama.get("/sessions/"+id, "de")
	inOrder(t, r.body, `<h1>Mac · Firefox <span class="tag">Dieser Browser</span></h1>`,
		"Das ist der Browser, mit dem du gerade hier bist. Um ihn abzumelden, nutze Abmelden.",
		`action="/logout"`)
	if strings.Contains(r.body, `class="danger"`) || strings.Contains(r.body, "/end") {
		t.Errorf("the own browser can be ended here:\n%s", r.body)
	}
}

// T8: parent A ends parent B's browser session; B's next request is refused.
func TestParentEndsAnotherParentsBrowserSession(t *testing.T) {
	s := newSessionsBox(t)
	id := s.idOf(t, "android_phone|chrome")
	if r := s.papa.get("/settings", "en"); r.status != http.StatusOK {
		t.Fatalf("papa before = %d", r.status)
	}
	r := s.mama.post("/sessions/"+id+"/end", url.Values{})
	if r.status != http.StatusSeeOther || r.location != "/sessions" {
		t.Fatalf("end = %d %q\n%s", r.status, r.location, r.body)
	}
	list := s.mama.get("/sessions", "de")
	if !strings.Contains(list.body, "Die Anmeldung „Android-Handy · Chrome“ von Papa ist beendet. Der Browser ist ab sofort abgemeldet.") {
		t.Errorf("no success line:\n%s", list.body)
	}
	if strings.Contains(list.body, "Android-Handy") && strings.Contains(list.body, `href="/sessions/`+id+`"`) {
		t.Errorf("the ended session is still listed")
	}
	// Papa's browser is refused on its next request ...
	for _, path := range []string{"/", "/settings", "/sessions", "/calendar"} {
		next := s.papa.get(path, "en")
		if next.status != http.StatusSeeOther || !strings.HasPrefix(next.location, "/login") {
			t.Errorf("papa's next request to %s = %d %q, want a redirect to /login", path, next.status, next.location)
		}
	}
	// ... and the sign-in page says why, once, and clears the cookie.
	login := s.papa.get("/login", "en")
	if !strings.Contains(login.body, "You are no longer signed in in this browser. Please sign in again.") || !strings.Contains(login.body, `role="status"`) {
		t.Fatalf("login page gives no reason:\n%s", login.body)
	}
	cleared := false
	for _, c := range login.cookies {
		if c.Name == "hejmo_session" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Errorf("the session cookie is not cleared")
	}
	if again := s.papa.get("/login", "de"); strings.Contains(again.body, "nicht mehr angemeldet") {
		t.Errorf("the notice shows again after the cookie was cleared")
	}
	// Mama stays signed in.
	if r := s.mama.get("/settings", "en"); r.status != http.StatusOK {
		t.Errorf("mama = %d", r.status)
	}
}

func TestLoginPageHasNoNoticeForAVisitorWithoutASession(t *testing.T) {
	b, _ := configuredBox(t)
	if r := newBrowser(b, uaWindowsEdge).get("/login", "en"); strings.Contains(r.body, "no longer signed in") {
		t.Errorf("a new visitor sees the signed-out notice")
	}
}

func TestEndingTheOwnBrowserSessionIsRefused(t *testing.T) {
	s := newSessionsBox(t)
	id := s.idOf(t, "mac|firefox")
	r := s.mama.post("/sessions/"+id+"/end", url.Values{})
	if r.status != http.StatusSeeOther || r.location != "/sessions/"+id {
		t.Fatalf("end own = %d %q, want a redirect to its detail page", r.status, r.location)
	}
	if r := s.mama.get("/settings", "en"); r.status != http.StatusOK {
		t.Fatalf("mama was signed out = %d", r.status)
	}
}

func TestEndingASessionThatIsGoneIsACalmNotice(t *testing.T) {
	s := newSessionsBox(t)
	id := s.idOf(t, "iphone|safari")
	s.mama.post("/sessions/"+id+"/end", url.Values{})
	for _, req := range []browserResp{
		s.mama.post("/sessions/"+id+"/end", url.Values{}),
		s.mama.get("/sessions/"+id, "de"),
		s.mama.get("/sessions/does-not-exist", "de"),
	} {
		if req.status != http.StatusSeeOther || req.location != "/sessions?gone=1" {
			t.Fatalf("gone = %d %q, want a redirect to /sessions?gone=1", req.status, req.location)
		}
	}
	page := s.mama.get("/sessions?gone=1", "de")
	if page.status != http.StatusOK || !strings.Contains(page.body, `<p class="notice top" role="status" data-session-gone>Diese Anmeldung gibt es nicht mehr: Sie wurde schon beendet oder ist abgelaufen. Hier siehst du alle, die noch gelten.</p>`) {
		t.Fatalf("no notice:\n%s", page.body)
	}
	if strings.Contains(s.mama.get("/sessions", "de").body, "gibt es nicht mehr") {
		t.Errorf("the notice shows without ?gone")
	}
}

func TestExpiredSessionsAreNotListed(t *testing.T) {
	s := newSessionsBox(t)
	id := s.idOf(t, "android_phone|chrome")
	s.now = time.Date(2026, 12, 12, 12, 0, 0, 0, time.UTC) // Papa's signs out by itself on 11/12
	s.mama.signIn(t, "mama", mamaPassword, s.mamaSecret, s.now)
	r := s.mama.get("/sessions", "en")
	if strings.Contains(r.body, "Android phone") || strings.Contains(r.body, id) {
		t.Errorf("expired session listed:\n%s", r.body)
	}
	if got := s.mama.get("/sessions/"+id, "en"); got.location != "/sessions?gone=1" {
		t.Errorf("expired detail = %d %q", got.status, got.location)
	}
}

func TestSessionPagesAreForSignedInParentsOnly(t *testing.T) {
	s := newSessionsBox(t)
	id := s.idOf(t, "android_phone|chrome")
	anon := newBrowser(s.box, uaWindowsEdge)
	for _, p := range []string{"/sessions", "/sessions/" + id} {
		if r := anon.get(p, "en"); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
			t.Errorf("anonymous GET %s = %d %q", p, r.status, r.location)
		}
	}
	if r := anon.post("/sessions/"+id+"/end", url.Values{}); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Errorf("anonymous end = %d %q", r.status, r.location)
	}
	// A child's device session is not a parent.
	h, _ := s.db.HomeHousehold()
	mia, err := s.db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	if err != nil {
		t.Fatal(err)
	}
	loginWith(s.box, "mama", mamaPassword, totpNow(s.box, s.mamaSecret)) // the parent who pairs the phone
	kid := pairedPhone(t, s.box, mia, "Mia's phone")
	for _, p := range []string{"/sessions", "/sessions/" + id} {
		if r := kid.get(p, "en"); r.status != http.StatusForbidden {
			t.Errorf("child GET %s = %d, want 403", p, r.status)
		}
	}
	// A phone's device session is not a browser session: it is not listed and
	// cannot be ended here.
	if list := s.mama.get("/sessions", "en").body; strings.Contains(list, "Mia") {
		t.Errorf("a paired phone is listed:\n%s", list)
	}
	var phoneID string
	if err := s.db.SQL().QueryRow(`SELECT id FROM sessions WHERE phone_id IS NOT NULL LIMIT 1`).Scan(&phoneID); err != nil || phoneID == "" {
		t.Fatalf("no phone session row: %v", err)
	}
	if r := s.mama.post("/sessions/"+phoneID+"/end", url.Values{}); r.status != http.StatusSeeOther || r.location != "/sessions?gone=1" {
		t.Errorf("ending a phone session here = %d %q, want the gone redirect", r.status, r.location)
	}
}

// A parent on a paired phone has no browser session of their own: with one
// other browser signed in, "only this browser" would be wrong.
func TestSessionsListOnAPairedPhoneDoesNotClaimItIsTheOnlyBrowser(t *testing.T) {
	b, secret := configuredBox(t)
	fresh(t, b)
	h, _ := b.db.HomeHousehold()
	people, err := b.db.People(h.ID)
	if err != nil || len(people) == 0 {
		t.Fatal(people, err)
	}
	loginWith(b, "mama", mamaPassword, totpNow(b, secret))
	phone := pairedPhone(t, b, people[0], "Mama's phone")
	r := phone.get("/sessions", "de")
	if r.status != http.StatusOK {
		t.Fatalf("parent phone GET /sessions = %d\n%s", r.status, r.body)
	}
	if strings.Contains(r.body, "Nur dieser Browser ist angemeldet.") || !strings.Contains(r.body, `href="/sessions/`) {
		t.Errorf("wrong sentence or list on a paired phone:\n%s", r.body)
	}
}

func TestSessionsListSaysWhenNoBrowserIsSignedIn(t *testing.T) {
	b, secret := configuredBox(t)
	fresh(t, b)
	h, _ := b.db.HomeHousehold()
	people, err := b.db.People(h.ID)
	if err != nil || len(people) == 0 {
		t.Fatal(people, err)
	}
	loginWith(b, "mama", mamaPassword, totpNow(b, secret))
	phone := pairedPhone(t, b, people[0], "Mama's phone")
	if _, err := b.db.SQL().Exec(`DELETE FROM sessions WHERE phone_id IS NULL`); err != nil {
		t.Fatal(err)
	}
	r := phone.get("/sessions", "de")
	if !strings.Contains(r.body, `data-empty`) || !strings.Contains(r.body, "Kein Browser ist angemeldet.") {
		t.Errorf("no empty sentence:\n%s", r.body)
	}
}

func TestEndingASessionNeedsTheSameOrigin(t *testing.T) {
	s := newSessionsBox(t)
	id := s.idOf(t, "android_phone|chrome")
	req, _ := http.NewRequest(http.MethodPost, s.srv.URL+"/sessions/"+id+"/end", strings.NewReader(""))
	req.Header.Set("Origin", "https://evil.example")
	res, err := s.mama.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site end = %d, want 403", res.StatusCode)
	}
	if r := s.papa.get("/settings", "en"); r.status != http.StatusOK {
		t.Errorf("papa was signed out by a cross-site request")
	}
}

// Only keys are stored: the User-Agent is never kept or shown.
func TestTheRawUserAgentIsNeverStoredOrShown(t *testing.T) {
	b, secret := configuredBox(t)
	fresh(t, b)
	odd := newBrowser(b, sessionsSecretUA)
	odd.signIn(t, "mama", mamaPassword, secret, b.now)
	var device string
	if err := b.db.SQL().QueryRow(`SELECT device FROM sessions`).Scan(&device); err != nil || device != "unknown|unknown" {
		t.Fatalf("device = %q, %v; want keys only", device, err)
	}
	for _, lang := range []string{"de", "en"} {
		if body := odd.get("/sessions", lang).body; strings.Contains(body, "SecretDevice") || strings.Contains(body, "windows|") {
			t.Errorf("%s page shows the raw device:\n%s", lang, body)
		}
	}
}

// The back link is the first thing inside main (ADR 0017). The detail
// page's back target is covered by TestSessionDetailOfAnotherBrowserOffersTheConfirmation.
func TestBackLinkSitsInsideMain(t *testing.T) {
	s := newSessionsBox(t)
	for _, path := range []string{"/sessions", "/phones", "/otp"} {
		body := s.mama.get(path, "en").body
		back, main := strings.Index(body, `<a class="back" href="/settings">`), strings.Index(body, `<main id="content">`)
		if back < 0 || main < 0 || back < main {
			t.Errorf("%s: the back link must come first inside main (%d, %d)", path, back, main)
		}
	}
}
