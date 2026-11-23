package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/googleauth"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/pki"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket #18: the Google connection (ADR 0013). Everything runs against fakes:
// a fake Google (token service, calendar list) and a fake CalDAV API. No test
// uses a real Google account.

const (
	gClientID = "318046275519-k7q2m9xlb4tn0c8e1rvd6hf3ys5pjw2a.apps.googleusercontent.com"
	gSecret   = "GOCSPX-test-secret-1234"
)

type googleFake struct {
	t   *testing.T
	b   *box
	srv *httptest.Server

	mu         sync.Mutex
	challenges map[string]string // code -> the code_challenge the sign-in was started with
	account    string            // the account the next sign-in is for
	exchange   []url.Values
	refreshes  int
	failToken  string // "", "invalid_grant" or "invalid_client": what the token service answers to a renewal
	failCode   string // what the exchange answers: "", "invalid_grant", "invalid_client"
	noScope    bool   // the exchange grants no calendar scope
	cals       []map[string]any
	custom     []map[string]any // the calendar list of a test that needs its own
}

func newGoogleFake(t *testing.T, b *box) *googleFake {
	t.Helper()
	g := &googleFake{t: t, b: b, challenges: map[string]string{}, account: "mama@gmail.com"}
	g.cals = []map[string]any{
		{"id": "mama@gmail.com", "summary": "mama@gmail.com", "accessRole": "owner", "primary": true},
		{"id": "papa@gmail.com", "summary": "Papa Arbeit", "accessRole": "reader"},
		{"id": "addressbook#contacts@group.v.calendar.google.com", "summary": "Geburtstage", "accessRole": "reader"},
		{"id": "abc123@group.calendar.google.com", "summary": "Familie Maier", "accessRole": "writer"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", g.token)
	mux.HandleFunc("GET /calendarList", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		items := g.cals
		if g.custom != nil {
			items = g.custom
		} else if g.account != "mama@gmail.com" { // a sign-in with another account: that account's own primary calendar
			items = []map[string]any{{"id": g.account, "summary": g.account, "accessRole": "owner", "primary": true}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	})
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	b.sync.SetGoogle(googleauth.New(g.srv.Client(), googleauth.WithEndpoints(googleauth.Endpoints{
		Token: g.srv.URL + "/token", CalendarList: g.srv.URL + "/calendarList",
	})), func(string) (calsync.Remote, error) { return googleRemote{}, nil })
	return g
}

// googleRemote is Google's CalDAV API with no events.
type googleRemote struct{ hostedRemote }

func (googleRemote) CountEvents(context.Context, string, time.Time) (int, error) { return 214, nil }

func (g *googleFake) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	g.mu.Lock()
	defer g.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	fail := func(code string, status int) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
	}
	if r.PostForm.Get("client_id") != gClientID || r.PostForm.Get("client_secret") != gSecret {
		fail("invalid_client", http.StatusUnauthorized)
		return
	}
	if r.PostForm.Get("grant_type") == "refresh_token" {
		g.refreshes++
		if g.failToken != "" {
			fail(g.failToken, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-r", "expires_in": 3600, "scope": googleauth.Scope})
		return
	}
	g.exchange = append(g.exchange, r.PostForm)
	if g.failCode != "" {
		fail(g.failCode, http.StatusBadRequest)
		return
	}
	code := r.PostForm.Get("code")
	if challenge, ok := g.challenges[code]; !ok || googleauth.Challenge(r.PostForm.Get("code_verifier")) != challenge {
		fail("invalid_grant", http.StatusBadRequest) // PKCE: the verifier must belong to the challenge
		return
	}
	delete(g.challenges, code) // a code works once
	scope := googleauth.Scope
	if g.noScope {
		scope = "openid"
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-1", "refresh_token": "1//refresh-" + code, "expires_in": 3599, "scope": scope})
}

// hand has the "browser" come back from Google for the sign-in that started at
// authorizeURL: the code Google would send, remembered with the challenge it belongs to.
func (g *googleFake) hand(authorizeURL, code string) (state string, q url.Values) {
	g.t.Helper()
	u, err := url.Parse(authorizeURL)
	if err != nil || u.Host != "accounts.google.com" {
		g.t.Fatalf("not an address at Google: %s", authorizeURL)
	}
	q = u.Query()
	g.mu.Lock()
	g.challenges[code] = q.Get("code_challenge")
	g.mu.Unlock()
	return q.Get("state"), q
}

func (g *googleFake) set(f func(*googleFake)) { g.mu.Lock(); defer g.mu.Unlock(); f(g) }

// googleBox is a signed-in box with a fake Google, an app registration and nothing connected.
type googleBox struct {
	b *box
	g *googleFake
	h store.Household
}

func newGoogleBox(t *testing.T) googleBox {
	t.Helper()
	b := signedInAPI(t).b
	h, err := b.db.HomeHousehold()
	if err != nil {
		t.Fatal(err)
	}
	return googleBox{b, newGoogleFake(t, b), h}
}

// register enters the app registration through the web form.
func (gb googleBox) register(t *testing.T) {
	t.Helper()
	r := gb.b.post("/connections/google/app", url.Values{"action": {"save"}, "client_id": {gClientID}, "client_secret": {gSecret}})
	if r.status != http.StatusSeeOther || r.location != "/connections/google/signin" {
		t.Fatalf("register = %d %q\n%s", r.status, r.location, r.body)
	}
}

// start presses "Google öffnen" and returns the address at Google.
func (gb googleBox) start(t *testing.T, form url.Values) string {
	t.Helper()
	if form == nil {
		form = url.Values{}
	}
	r := gb.b.post("/connections/google/start", form)
	if r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "https://accounts.google.com/") {
		t.Fatalf("start = %d %q\n%s", r.status, r.location, r.body)
	}
	return r.location
}

// signIn runs a whole sign-in on the relay way and returns the connection id.
func (gb googleBox) signIn(t *testing.T) string {
	t.Helper()
	gb.register(t)
	state, _ := gb.g.hand(gb.start(t, nil), "code-1")
	r := gb.b.get(googleCallback(state, "code-1"))
	m := regexp.MustCompile(`^/connections/([0-9a-f-]+)$`).FindStringSubmatch(r.location)
	if r.status != http.StatusSeeOther || m == nil {
		t.Fatalf("callback = %d %q\n%s", r.status, r.location, r.body)
	}
	return m[1]
}

func googleCallback(state, code string) string {
	return "/connections/google/callback?" + url.Values{"state": {state}, "code": {code}, "scope": {googleauth.Scope}}.Encode()
}

func (gb googleBox) connections(t *testing.T) []store.Connection {
	t.Helper()
	conns, err := gb.b.db.Connections(gb.h.ID)
	if err != nil {
		t.Fatal(err)
	}
	return conns
}

// ---- the app registration ----

func TestConnectPageOffersGoogleAfterICloudAndBeforeOutlook(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	for lang, want := range map[string][]string{
		"de": {"Google verbinden", "Einmal einrichten, etwa 20 Minuten", "Warum eine eigene App-Registrierung?", "Hejmo-Box"},
		"en": {"Connect Google", "Set up once, about 20 minutes", "Why your own app registration?", "Hejmo box"},
	} {
		r := gb.b.do(http.MethodGet, "/connections/new?service=google", nil, map[string]string{"Accept-Language": lang})
		if r.status != http.StatusOK {
			t.Fatalf("%s: %d", lang, r.status)
		}
		block := connectBlock(t, r.body)
		for _, w := range want {
			if !strings.Contains(block, w) {
				t.Errorf("%s: page lacks %q", lang, w)
			}
		}
		radios := regexp.MustCompile(`name="service" value="(\w+)"`).FindAllStringSubmatch(block, -1)
		if len(radios) != 5 || radios[0][1] != "icloud" || radios[1][1] != "google" || radios[2][1] != "outlook" || radios[3][1] != "caldav" || radios[4][1] != "ics" {
			t.Errorf("%s: choice cards = %v, want icloud, google, outlook, caldav, ics", lang, radios)
		}
		if !strings.Contains(block, `<input type="radio" name="service" value="google" checked>`) {
			t.Errorf("%s: Google is not the checked card", lang)
		}
		if n := strings.Count(block, `<ol class="howto">`); n != 5 {
			t.Errorf("%s: %d guides on the page, want 5", lang, n)
		}
	}
}

// Q155: values the parent types in at Google are shown as values with a copy
// button, never as links: a link to http://127.0.0.1:8099/ would only show an error.
func TestGoogleGuideShowsValuesToCopyAndNotLinks(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	guide := formOf(t, gb.b.get("/connections/new?service=google").body, "/connections/google/app")
	for _, v := range []string{"hejmo-home.github.io", "https://www.googleapis.com/auth/calendar", "https://hejmo-home.github.io/oauth/", "http://127.0.0.1:8099/"} {
		if !strings.Contains(guide, "<code>"+v+"</code>") {
			t.Errorf("no value %q", v)
		}
		if !strings.Contains(guide, `data-copy="`+v+`"`) {
			t.Errorf("no copy button for %q", v)
		}
		for _, a := range anchorRe.FindAllString(guide, -1) {
			if strings.Contains(a, `href="`+v) {
				t.Errorf("%q is a link: %s", v, a)
			}
		}
	}
	// Eight steps, each with the one link to the right page at Google, opening in a new tab.
	if n := strings.Count(guide, `<li><div class="s">`); n != 8 {
		t.Errorf("%d steps, want 8", n)
	}
	for _, page := range []string{"projectcreate", "caldav.googleapis.com", "calendar-json.googleapis.com", "auth/overview", "auth/branding", "auth/scopes", "auth/audience", "auth/clients/create"} {
		if !regexp.MustCompile(`<a class="button secondary out" href="https://console\.cloud\.google\.com/[^"]*` + regexp.QuoteMeta(page) + `[^"]*" target="_blank" rel="noopener">`).MatchString(guide) {
			t.Errorf("no link to %s that opens in a new tab", page)
		}
	}
	for _, w := range []string{"In Produktion", "Testen", "nur dieses eine Mal"} {
		if !strings.Contains(guide, w) {
			t.Errorf("the guide does not name %q", w)
		}
	}
	if strings.Contains(guide, `value="`+gSecret) || !strings.Contains(guide, `name="client_secret" type="password"`) {
		t.Error("the client secret field is not a hidden field")
	}
}

func TestRegisteringTheAppStoresTheSecretSealedAndGoesToTheSignIn(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	gb.register(t)
	id, secret, err := gb.b.db.GoogleAppSecret(gb.h.ID)
	if err != nil || id != gClientID || secret != gSecret {
		t.Fatalf("stored = %q %q %v", id, secret, err)
	}
	// With an app registration the page says what is set up and asks for the sign-in only.
	r := gb.b.get("/connections/new?service=google")
	block := formOf(t, r.body, "/connections/google/app")
	for _, want := range []string{gClientID, "Weiter zur Google-Anmeldung", "Andere App-Registrierung eintragen"} {
		if !strings.Contains(block, want) {
			t.Errorf("registered page lacks %q", want)
		}
	}
	if strings.Contains(block, `<ol class="howto">`) || strings.Contains(r.body, gSecret) {
		t.Error("a registered box shows the setup steps again or the secret")
	}
}

func TestAnIncompleteClientIDIsAnErrorAtTheFieldAndKeepsWhatWasTyped(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	r := gb.b.post("/connections/google/app", url.Values{"action": {"save"}, "client_id": {"318046275519-k7q2m9xlb4tn0c8e1rvd6hf3ys5pjw2a"}, "client_secret": {gSecret}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", r.status)
	}
	form := formOf(t, r.body, "/connections/google/app")
	if !strings.Contains(form, `id="err-client_id"`) || !strings.Contains(form, `value="318046275519-k7q2m9xlb4tn0c8e1rvd6hf3ys5pjw2a"`) {
		t.Errorf("error not at the field, or the value lost:\n%s", form)
	}
	if strings.Contains(r.body, gSecret) || !strings.Contains(form, "noch einmal ein") {
		t.Error("the secret came back, or the page does not ask for it again")
	}
	if _, err := gb.b.db.GoogleApp(gb.h.ID); err == nil {
		t.Error("stored a registration that failed")
	}
	if r := gb.b.post("/connections/google/app", url.Values{"action": {"save"}, "client_id": {gClientID}}); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-client_secret"`) {
		t.Errorf("no secret: %d", r.status)
	}
}

// ---- the sign-in ----

func TestSignInPageShowsEveryStepAndCreatesNothingUntilGoogleIsOpened(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	gb.register(t)
	for i := 0; i < 3; i++ { // reloading uses nothing up
		r := gb.b.get("/connections/google/signin")
		if r.status != http.StatusOK {
			t.Fatalf("status = %d", r.status)
		}
		for _, want := range []string{"Lies die Schritte vorher einmal durch", "Die Warnung ist bei euch richtig", "Google hat diese App nicht überprüft", "Zugriff auf die Kalender erlauben", `class="button go"`, "Die Anmeldung gilt 15 Minuten.", "redirect_uri_mismatch", "invalid_client", "access_denied"} {
			if !strings.Contains(r.body, want) {
				t.Errorf("page lacks %q", want)
			}
		}
		if !strings.Contains(r.body, `<form class="guide" method="post" action="/connections/google/start"`) {
			t.Error("Open Google is not a form button")
		}
	}
	var n int
	gb.b.db.SQL().QueryRow(`SELECT COUNT(*) FROM google_login`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d sign-ins were started by looking at the page", n)
	}
	if r := gb.b.do(http.MethodGet, "/connections/google/signin", nil, map[string]string{"Accept-Language": "en"}); !strings.Contains(r.body, "Read the steps once before you start") || !strings.Contains(r.body, "Open Google") {
		t.Error("English page missing")
	}
	// Without an app registration there is nothing to sign in with.
	gb2 := newGoogleBox(t)
	if r := gb2.b.get("/connections/google/signin"); r.status != http.StatusSeeOther || r.location != "/connections/new?service=google" {
		t.Errorf("without a registration: %d %q", r.status, r.location)
	}
}

func TestOpeningGoogleStartsASignInWithPKCEAndASignedState(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	gb.register(t)
	loc := gb.start(t, nil)
	_, q := gb.g.hand(loc, "c")
	want := map[string]string{
		"client_id": gClientID, "redirect_uri": "https://hejmo-home.github.io/oauth/", "response_type": "code", "scope": googleauth.Scope,
		"code_challenge_method": "S256", "access_type": "offline",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	origin, token, err := googleauth.ParseState(q.Get("state"))
	if err != nil || !strings.HasPrefix(origin, "https://127.0.0.1:") || token == "" {
		t.Fatalf("state = %q (%q, %v)", q.Get("state"), origin, err)
	}
	if q.Get("code_challenge") == "" {
		t.Error("no PKCE challenge")
	}
	var n int
	gb.b.db.SQL().QueryRow(`SELECT COUNT(*) FROM google_login`).Scan(&n)
	if n != 1 {
		t.Errorf("%d started sign-ins, want 1", n)
	}
}

// Acceptance: a valid callback with a matching state; the code is exchanged
// (PKCE) and a refresh token is stored encrypted.
func TestValidCallbackExchangesTheCodeWithPKCEAndStoresTheRefreshTokenEncrypted(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	id := gb.signIn(t)
	if n := len(gb.g.exchange); n != 1 {
		t.Fatalf("%d exchanges", n)
	}
	x := gb.g.exchange[0]
	if x.Get("grant_type") != "authorization_code" || x.Get("code") != "code-1" || x.Get("code_verifier") == "" || x.Get("redirect_uri") != googleauth.RelayRedirectURI {
		t.Fatalf("exchange = %v", x)
	}
	conns := gb.connections(t)
	if len(conns) != 1 || conns[0].ID != id || conns[0].Kind != "google" || conns[0].Username != "mama@gmail.com" {
		t.Fatalf("connections = %+v", conns)
	}
	_, token, err := gb.b.db.ConnectionCredentials(gb.h.ID, id)
	if err != nil || token != "1//refresh-code-1" {
		t.Fatalf("refresh token = %q, %v", token, err)
	}
	var raw string
	gb.b.db.SQL().QueryRow(`SELECT credential FROM calendar_connections WHERE id = ?`, id).Scan(&raw)
	if raw == "" || strings.Contains(raw, "refresh") {
		t.Fatalf("the refresh token is not stored encrypted: %q", raw)
	}
}

// Acceptance: an unknown, expired or reused state is rejected.
func TestCallbackWithAnUnknownExpiredOrReusedStateIsRejected(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	gb.register(t)

	// A state the box never made.
	unknown := gb.b.get(googleCallback("hj1.aHR0cHM6Ly94.not-made-by-this-box", "x"))
	if unknown.status != http.StatusBadRequest || !strings.Contains(unknown.body, "Diese Anmeldung gilt nicht mehr.") {
		t.Errorf("unknown: %d\n%s", unknown.status, unknown.body)
	}

	// An expired one.
	state, _ := gb.g.hand(gb.start(t, nil), "late")
	gb.b.now = gb.b.now.Add(16 * time.Minute)
	expired := gb.b.get(googleCallback(state, "late"))
	if expired.status != http.StatusBadRequest || !strings.Contains(expired.body, "Diese Anmeldung gilt nicht mehr.") || !strings.Contains(expired.body, "15 Minuten") {
		t.Errorf("expired: %d\n%s", expired.status, expired.body)
	}
	gb.b.now = gb.b.now.Add(-16 * time.Minute)

	// One used before: the second time it is "already used" and nothing is created twice.
	state, _ = gb.g.hand(gb.start(t, nil), "once")
	if first := gb.b.get(googleCallback(state, "once")); first.status != http.StatusSeeOther {
		t.Fatalf("first use: %d", first.status)
	}
	gb.g.hand(gb.start(t, nil), "twice") // so the code would be valid again
	again := gb.b.get(googleCallback(state, "once"))
	if again.status != http.StatusBadRequest || !strings.Contains(again.body, "Diese Anmeldung wurde schon benutzt.") || !strings.Contains(again.body, `href="/calendars"`) {
		t.Errorf("reused: %d\n%s", again.status, again.body)
	}
	if n := len(gb.connections(t)); n != 1 {
		t.Errorf("%d connections after the rejected callbacks, want the one from the first use", n)
	}
	if n := len(gb.g.exchange); n != 1 {
		t.Errorf("%d code exchanges, want one: a rejected state never reaches Google", n)
	}
}

func TestACallbackNeedsASignedInParent(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	gb.register(t)
	state, _ := gb.g.hand(gb.start(t, nil), "c")
	gb.b.logout()
	r := gb.b.get(googleCallback(state, "c"))
	if r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login?next=") {
		t.Fatalf("signed out: %d %q", r.status, r.location)
	}
	if len(gb.connections(t)) != 0 {
		t.Fatal("a connection without a parent")
	}
}

func TestWhatGoesWrongAtGoogleIsSaidInWordsAndNothingIsStored(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		query func(state string) url.Values
		fake  func(*googleFake)
		want  string
	}{
		{"cancelled", func(s string) url.Values { return url.Values{"state": {s}, "error": {"access_denied"}} }, func(*googleFake) {}, "Du hast bei Google abgebrochen."},
		{"code not accepted", func(s string) url.Values { return url.Values{"state": {s}, "code": {"c"}, "scope": {googleauth.Scope}} }, func(g *googleFake) { g.failCode = "invalid_grant" }, "Google hat die Anmeldung nicht bestätigt."},
		{"calendar tick missing", func(s string) url.Values { return url.Values{"state": {s}, "code": {"c"}, "scope": {"openid"}} }, func(*googleFake) {}, "Hejmo darf deine Kalender noch nicht sehen."},
		{"scope missing in the exchange", func(s string) url.Values { return url.Values{"state": {s}, "code": {"c"}} }, func(g *googleFake) { g.noScope = true }, "Hejmo darf deine Kalender noch nicht sehen."},
	} {
		t.Run(c.name, func(t *testing.T) {
			gb := newGoogleBox(t)
			gb.register(t)
			state, _ := gb.g.hand(gb.start(t, nil), "c")
			gb.g.set(c.fake)
			r := gb.b.get("/connections/google/callback?" + c.query(state).Encode())
			if r.status != http.StatusBadRequest || !strings.Contains(r.body, c.want) || !strings.Contains(r.body, "Nichts wurde gespeichert.") {
				t.Fatalf("%d\n%s", r.status, r.body)
			}
			if !strings.Contains(r.body, `<form class="guide" method="post" action="/connections/google/start"`) || !strings.Contains(r.body, "Noch einmal bei Google anmelden") {
				t.Error("no button to try again")
			}
			if strings.Contains(r.body, `<ol class="howto">`) {
				t.Error("the steps are still on the page: an error replaces them")
			}
			if len(gb.connections(t)) != 0 {
				t.Error("stored a connection")
			}
		})
	}
}

func TestGoogleRefusingTheClientSecretSendsTheParentBackToTheSecretField(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	gb.register(t)
	state, _ := gb.g.hand(gb.start(t, nil), "c")
	gb.g.set(func(g *googleFake) { g.failCode = "invalid_client" })
	r := gb.b.get(googleCallback(state, "c"))
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", r.status)
	}
	form := formOf(t, r.body, "/connections/google/app")
	if !strings.Contains(form, `id="err-client_secret"`) || !strings.Contains(form, "Google hat diesen Clientschlüssel nicht angenommen.") || !strings.Contains(form, `value="`+gClientID+`"`) {
		t.Errorf("not back at the secret field with the client ID:\n%s", form)
	}
	if len(gb.connections(t)) != 0 {
		t.Error("stored a connection")
	}
}

// ---- the fallback ----

// Acceptance: a pasted fallback URL with a matching state creates the connection.
func TestPastedFallbackAddressWithAMatchingStateCreatesTheConnection(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	gb.register(t)
	page := gb.b.get("/connections/google/signin?way=fallback")
	for _, want := range []string{"Ersatzweg: Adresse einfügen", "„Nicht erreichbar“ ist hier richtig", `<textarea class="addr-in"`, "http://127.0.0.1:8099/", "Zurück zum normalen Weg"} {
		if !strings.Contains(page.body, want) {
			t.Errorf("fallback page lacks %q", want)
		}
	}
	loc := gb.start(t, url.Values{"way": {"fallback"}})
	state, q := gb.g.hand(loc, "fb-code")
	if q.Get("redirect_uri") != "http://127.0.0.1:8099/" {
		t.Fatalf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	pasted := "http://127.0.0.1:8099/?" + url.Values{"state": {state}, "code": {"fb-code"}, "scope": {googleauth.Scope}}.Encode()
	r := gb.b.post("/connections/google/paste", url.Values{"address": {pasted}})
	if r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/connections/") {
		t.Fatalf("paste = %d %q\n%s", r.status, r.location, r.body)
	}
	if conns := gb.connections(t); len(conns) != 1 || conns[0].Username != "mama@gmail.com" {
		t.Fatalf("connections = %+v", conns)
	}
	if x := gb.g.exchange[0]; x.Get("redirect_uri") != "http://127.0.0.1:8099/" {
		t.Errorf("the code was exchanged for %q, the fallback address Google was asked with", x.Get("redirect_uri"))
	}
}

func TestPastedAddressProblemsAreShownAtTheField(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	gb.register(t)
	state, _ := gb.g.hand(gb.start(t, url.Values{"way": {"fallback"}}), "fb")
	for name, c := range map[string]struct{ addr, want string }{
		"not an address with a sign-in": {"https://accounts.google.com/signin/oauth/consent?authuser=0", "Diese Adresse enthält keine Anmeldung."},
		"cancelled at Google":           {"http://127.0.0.1:8099/?error=access_denied&state=" + state, "Laut dieser Adresse hast du bei Google abgebrochen."},
		"an older sign-in":              {"http://127.0.0.1:8099/?code=x&state=hj1.b2xkZXI.Zq1&scope=" + googleauth.Scope, "Diese Adresse gehört zu einer älteren Anmeldung."},
	} {
		r := gb.b.post("/connections/google/paste", url.Values{"address": {c.addr}})
		if r.status != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", name, r.status)
			continue
		}
		form := formOf(t, r.body, "/connections/google/paste")
		if !strings.Contains(form, `id="err-address"`) || !strings.Contains(form, c.want) || !strings.Contains(form, `aria-invalid="true"`) {
			t.Errorf("%s: no error at the field:\n%s", name, form)
		}
		if !strings.Contains(form, escaped(c.addr)) {
			t.Errorf("%s: the pasted address was lost", name)
		}
	}
	if len(gb.connections(t)) != 0 {
		t.Error("stored a connection")
	}
}

func escaped(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;").Replace(s)
}

// plainBox serves the signed-in box's data over plain HTTP, as the box before it has a certificate, and sends
// requests as the same browser. (A box that has its certificate redirects plain HTTP to HTTPS.)
type plainBox struct {
	t       *testing.T
	srv     *httptest.Server
	cookies []*http.Cookie
}

func newPlainBox(t *testing.T, b *box) plainBox {
	t.Helper()
	certs, err := pki.Open(t.TempDir()) // not provisioned: no certificate yet
	if err != nil {
		t.Fatal(err)
	}
	h := server.New(server.Options{DB: b.db, PKI: certs, Public: &fakePublic{}, Now: func() time.Time { return b.now }, Sync: b.sync})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(b.srv.URL)
	return plainBox{t, srv, b.client.Jar.Cookies(u)}
}

func (p plainBox) do(method, path string, form url.Values, host string) resp {
	p.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, p.srv.URL+path, body)
	req.Host = host
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "http://"+host)
	}
	for _, c := range p.cookies {
		req.AddCookie(c)
	}
	res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header.Get("Location"), string(raw)}
}

// The relay page forwards over http only to addresses in the home network: a box
// opened as http://hejmo.local gets the fallback straight away.
func TestABoxOpenedOverHTTPByNameShowsTheFallbackStraightAway(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	gb.register(t)
	plain := newPlainBox(t, gb.b)
	r := plain.do(http.MethodGet, "/connections/google/signin", nil, "hejmo.local")
	for _, want := range []string{"Hier geht nur der Ersatzweg", "http://hejmo.local", "Ersatzweg: Adresse einfügen", `<textarea class="addr-in"`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("page lacks %q:\n%s", want, r.body)
		}
	}
	if strings.Contains(r.body, "Zurück zum normalen Weg") {
		t.Error("offers the way that cannot work")
	}
	start := plain.do(http.MethodPost, "/connections/google/start", url.Values{}, "hejmo.local")
	u, _ := url.Parse(start.location)
	if u.Query().Get("redirect_uri") != googleauth.FallbackRedirectURI {
		t.Errorf("redirect_uri = %q, want the fallback (%d %q)", u.Query().Get("redirect_uri"), start.status, start.location)
	}
	// An address in the home network over http can use the relay page.
	start = plain.do(http.MethodPost, "/connections/google/start", url.Values{}, "192.168.178.20")
	u, _ = url.Parse(start.location)
	if u.Query().Get("redirect_uri") != googleauth.RelayRedirectURI {
		t.Errorf("redirect_uri = %q, want the relay page", u.Query().Get("redirect_uri"))
	}
	if origin, _, _ := googleauth.ParseState(u.Query().Get("state")); origin != "http://192.168.178.20" {
		t.Errorf("the state names %q as the way back", origin)
	}
	if page := plain.do(http.MethodGet, "/connections/google/signin", nil, "192.168.178.20"); strings.Contains(page.body, "Hier geht nur der Ersatzweg") {
		t.Error("an address in the home network is told only the fallback works")
	}
}

func TestWhereTheRelayPageMayGoBack(t *testing.T) {
	t.Parallel()
	for origin, want := range map[string]bool{
		"https://hejmo.fritz.box": true, "https://192.168.178.20": true, "https://hejmo.example.com:8443": true,
		"http://192.168.178.20": true, "http://10.0.0.5:8080": true, "http://172.20.1.1": true,
		"http://hejmo.local": false, "http://hejmo.example.com": false, "http://8.8.8.8": false, "http://127.0.0.1": false,
		"ftp://192.168.1.1": false, "": false,
	} {
		if got := server.RelayUsable(origin); got != want {
			t.Errorf("RelayUsable(%q) = %v, want %v", origin, got, want)
		}
	}
}

// ---- Choose calendars ----

// signedInBox is a box with one Google account signed in and calendars to choose from.
func (gb googleBox) chooseCalendars(t *testing.T, lang string) string {
	t.Helper()
	id := gb.signIn(t)
	return gb.b.do(http.MethodGet, "/connections/"+id, nil, map[string]string{"Accept-Language": lang}).body
}

func TestChooseCalendarsShowsWhatTheBoxMayChangeAtGoogle(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	page := gb.chooseCalendars(t, "de")
	for _, want := range []string{
		"Was die Box in Google ändern darf", "Farben aus Google",
		`<span class="src">Kalender in Google</span><b>mama@gmail.com</b>`, "<b>Lesen und ändern:</b> Was du auf der Box einträgst, steht auch in Google.",
		`<span class="src">Geteilt von papa@gmail.com</span><b>Papa Arbeit</b>`, "<b>Nur lesen:</b> Nur zum Ansehen mit dir geteilt. Ändern geht nur in Google.",
		"<b>Nur lesen:</b> Google füllt ihn aus deinen Kontakten.",
		`<span class="src">Mit dir geteilt</span>`, // a calendar of a group, shared by someone: no account to name
		"Verschlüsselt", // the secret is stored encrypted, never shown
	} {
		if want == "Verschlüsselt" {
			want = "verschlüsselt gespeichert; wird nicht mehr angezeigt"
		}
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, want := range []string{"Google-Konto", "Anmeldung", "App-Registrierung", "eure eigene", gClientID, "Clientschlüssel", "Zuletzt abgeglichen", "noch nicht"} {
		if !strings.Contains(page, want) {
			t.Errorf("facts lack %q", want)
		}
	}
	if strings.Contains(page, gSecret) {
		t.Error("the client secret is on the page")
	}
	// Name in Hejmo matters most here: Google names the main calendar after the account.
	if !strings.Contains(page, "In Google heißt er weiter") {
		t.Error("no hint that the name is only in Hejmo")
	}
	if n := strings.Count(page, `name="name_https://apidata.googleusercontent.com/caldav/v2/`); n != 4 {
		t.Errorf("%d calendar cards with a name field, want 4", n)
	}
	en := gb.b.do(http.MethodGet, "/connections/"+gb.connections(t)[0].ID, nil, map[string]string{"Accept-Language": "en"}).body
	for _, want := range []string{"What the box may change at Google", "<b>Read and change:</b>", "<b>Read only:</b>", "Shared by papa@gmail.com"} {
		if !strings.Contains(en, want) {
			t.Errorf("English page lacks %q", want)
		}
	}
}

func TestTickedGoogleCalendarsAreSyncedAndAReadOnlyShareCanNeverBeTwoWay(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	id := gb.signIn(t)
	cal := func(email string) string { return googleauth.CalendarURL(googleauth.CalDAVBase, email) }
	form := url.Values{"calendar": {cal("mama@gmail.com"), cal("papa@gmail.com")}, "interval": {"15"},
		"name_" + cal("mama@gmail.com"): {"Mama"}, "name_" + cal("papa@gmail.com"): {"Papa Arbeit"}, "whole_family": {"1"}}
	if r := gb.b.post("/connections/"+id+"/calendars", form); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	cals, err := gb.b.db.SyncedCalendars(gb.h.ID)
	if err != nil || len(cals) != 2 {
		t.Fatalf("calendars = %+v, %v", cals, err)
	}
	by := map[string]store.SyncedCalendar{}
	for _, c := range cals {
		by[c.Name] = c
	}
	mama, papa := by["Mama"], by["Papa Arbeit"]
	if mama.Kind != "google" || mama.ReadOnly || mama.SourceLocked || mama.URL != cal("mama@gmail.com") || mama.Sync.IntervalMinutes != 15 {
		t.Errorf("own calendar = %+v", mama)
	}
	if !papa.ReadOnly || !papa.SourceLocked {
		t.Errorf("a calendar shared for viewing is not read-only: %+v", papa)
	}
	// The connection page offers "Nur lesbar machen" for the own calendar, and says why the share cannot change.
	page := gb.b.get("/connections/" + id).body
	if !strings.Contains(page, `action="/calendars/`+mama.ID+`/read-only"`) || strings.Contains(page, `action="/calendars/`+papa.ID+`/read-only"`) {
		t.Errorf("the write-back switches are wrong:\n%s", page)
	}
	if !strings.Contains(page, "Nur lesbar: Er ist nur zum Ansehen mit dir geteilt.") {
		t.Error("no reason why the shared calendar is not two-way")
	}
	if r := gb.b.post("/calendars/"+papa.ID+"/read-only", url.Values{"read_only": {"0"}}); r.status < 400 {
		t.Errorf("a read-only share was made two-way: %d", r.status)
	}
	// The event counts come from Google's CalDAV API.
	c := newAPIClient(t, gb.b)
	c.cookie = true
	status, _, list := c.call(http.MethodGet, "/calendar-connections/"+id+"/event-counts", nil)
	if status != http.StatusOK || len(list) != 4 {
		t.Fatalf("event counts = %d %v", status, list)
	}
	if list[0].(map[string]any)["count"].(float64) != 214 {
		t.Errorf("counts = %v", list[0])
	}
}

func TestRemovingAGoogleConnectionSaysWhatHappensAtGoogle(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	id := gb.signIn(t)
	page := gb.b.get("/connections/" + id).body
	for _, want := range []string{"Verbindung trennen", "In Google wird nichts gelöscht", "myaccount.google.com/connections", "Die App-Registrierung bleibt auf der Box", "Ja, „mama@gmail.com“ trennen"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if r := gb.b.post("/connections/"+id+"/delete", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("delete = %d", r.status)
	}
	if len(gb.connections(t)) != 0 {
		t.Fatal("still connected")
	}
	if _, err := gb.b.db.GoogleApp(gb.h.ID); err != nil {
		t.Error("the app registration went with the connection: other accounts would need the setup again")
	}
}

// ---- sign in again ----

// signInAgainBox is a box whose Google account Google has stopped accepting.
func signInAgainBox(t *testing.T) (googleBox, string) {
	t.Helper()
	gb := newGoogleBox(t)
	id := gb.signIn(t)
	cal := googleauth.CalendarURL(googleauth.CalDAVBase, "mama@gmail.com")
	gb.b.post("/connections/"+id+"/calendars", url.Values{"calendar": {cal}, "name_" + cal: {"Mama"}, "interval": {"15"}, "whole_family": {"1"}})
	gb.g.set(func(g *googleFake) { g.failToken = "invalid_grant" })
	gb.b.now = gb.b.now.Add(2 * time.Hour) // the access token ran out
	cals, _ := gb.b.db.SyncedCalendars(gb.h.ID)
	if err := gb.b.sync.Sync(context.Background(), gb.h.ID, cals[0].ID); err == nil {
		t.Fatal("the sync did not fail")
	}
	return gb, id
}

// Acceptance: a refresh token rejected with invalid_grant marks the connection
// "sign in again" and the sync log has an entry.
func TestInvalidGrantMarksTheConnectionAndIsShownToParents(t *testing.T) {
	t.Parallel()
	gb, id := signInAgainBox(t)
	conn, _ := gb.b.db.Connection(gb.h.ID, id)
	if conn.SignInNeededAt.IsZero() {
		t.Fatal("not marked")
	}
	log, _ := gb.b.db.SyncLog(gb.h.ID, 10)
	if len(log) != 1 || log[0].Message.Key != "sync.fail.google_signin" {
		t.Fatalf("sync log = %+v", log)
	}
	notice := func(page string) {
		t.Helper()
		for _, want := range []string{`class="notice act reconnect"`, "Bei Google neu anmelden", "Google nimmt die Anmeldung für <b class=\"host\">mama@gmail.com</b> nicht mehr an.", "Häufige Gründe", `action="/connections/google/signin"`, `name="connection" value="` + id + `"`} {
			if !strings.Contains(page, want) {
				t.Errorf("notice lacks %q", want)
			}
		}
		if strings.Contains(page, "genau 7 Tage") {
			t.Error("the 7 day hint for a sign-in that lasted two hours")
		}
	}
	start := gb.b.get("/")
	notice(start.body)
	if !strings.Contains(start.body, "Neu anmelden kann sich, wer das Passwort dieses Google-Kontos kennt.") {
		t.Error("Start does not say who can sign in again")
	}
	calendars := gb.b.get("/calendars")
	notice(calendars.body)
	if !strings.Contains(calendars.body, `<span class="flag problem">`) || !strings.Contains(calendars.body, "Anmeldung abgelaufen: bei Google neu anmelden") {
		t.Errorf("the connection row has no problem flag:\n%s", calendars.body)
	}
	notice(gb.b.get("/connections/" + id).body)
	// The calendars stay and keep their events: the page of the connection still opens and says what to do.
	if r := gb.b.get("/connections/" + id); r.status != http.StatusOK || !strings.Contains(r.body, "Mama") {
		t.Errorf("connection page = %d", r.status)
	}
}

func TestSignInAgainNoticeIsForParentsOnly(t *testing.T) {
	t.Parallel()
	gb, _ := signInAgainBox(t)
	scr := pairedPhone(t, gb.b, personNamed(t, gb.b, "Mia"), "Mia's phone")
	if r := scr.get("/", "de"); r.status != http.StatusOK || strings.Contains(r.body, "Google") {
		t.Errorf("a child's Start shows the notice: %d", r.status)
	}
}

func TestAfterSevenDaysTheNoticePointsToTesting(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	id := gb.signIn(t)
	cal := googleauth.CalendarURL(googleauth.CalDAVBase, "mama@gmail.com")
	gb.b.post("/connections/"+id+"/calendars", url.Values{"calendar": {cal}, "name_" + cal: {"Mama"}, "interval": {"15"}, "whole_family": {"1"}})
	gb.g.set(func(g *googleFake) { g.failToken = "invalid_grant" })
	gb.b.now = gb.b.now.Add(7*24*time.Hour + 10*time.Minute) // exactly what an app on "Testing" gives
	cals, _ := gb.b.db.SyncedCalendars(gb.h.ID)
	_ = gb.b.sync.Sync(context.Background(), gb.h.ID, cals[0].ID)
	page := gb.b.get("/calendars").body
	if !strings.Contains(page, "Die Anmeldung hielt genau 7 Tage.") || !strings.Contains(page, "https://console.cloud.google.com/auth/audience") || strings.Contains(page, "Häufige Gründe") {
		t.Errorf("no pointer to \"Testen\":\n%s", page)
	}
}

func TestSigningInAgainKeepsTheConnectionAndItsCalendars(t *testing.T) {
	t.Parallel()
	gb, id := signInAgainBox(t)
	gb.g.set(func(g *googleFake) { g.failToken = "" })
	page := gb.b.get("/connections/google/signin?connection=" + id)
	for _, want := range []string{"Bei Google neu anmelden", `<b class="host">mama@gmail.com</b>`, "Mit dem richtigen Konto anmelden", "Anderes Konto verwenden", `name="connection" value="` + id + `"`} {
		if !strings.Contains(page.body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	loc := gb.start(t, url.Values{"connection": {id}})
	if u, _ := url.Parse(loc); u.Query().Get("login_hint") != "mama@gmail.com" {
		t.Errorf("login_hint = %q", u.Query().Get("login_hint"))
	}
	state, _ := gb.g.hand(loc, "again")
	r := gb.b.get(googleCallback(state, "again"))
	if r.status != http.StatusSeeOther || r.location != "/connections/"+id {
		t.Fatalf("callback = %d %q\n%s", r.status, r.location, r.body)
	}
	conns := gb.connections(t)
	if len(conns) != 1 || conns[0].ID != id || !conns[0].SignInNeededAt.IsZero() {
		t.Fatalf("connections = %+v", conns)
	}
	_, token, _ := gb.b.db.ConnectionCredentials(gb.h.ID, id)
	if token != "1//refresh-again" {
		t.Errorf("refresh token = %q", token)
	}
	if cals, _ := gb.b.db.SyncedCalendars(gb.h.ID); len(cals) != 1 || cals[0].Name != "Mama" || cals[0].Sync.LastError.Key != "" {
		t.Errorf("calendars = %+v: they sync again at once", cals)
	}
	if after := gb.b.get("/connections/" + id).body; !strings.Contains(after, "Bei Google angemeldet.") || strings.Contains(after, "notice act reconnect") {
		t.Error("no success line, or the notice is still there")
	}
}

func TestAnotherAccountIsNotThrownAwayButOfferedAsANewConnection(t *testing.T) {
	t.Parallel()
	gb, id := signInAgainBox(t)
	gb.g.set(func(g *googleFake) { g.failToken, g.account = "", "papa@gmail.com" })
	state, _ := gb.g.hand(gb.start(t, url.Values{"connection": {id}}), "papa")
	r := gb.b.get(googleCallback(state, "papa"))
	for _, want := range []string{"Das war ein anderes Google-Konto.", "Du hast dich als papa@gmail.com angemeldet. Diese Verbindung gehört zu mama@gmail.com; nichts wurde geändert.",
		"Noch einmal mit mama@gmail.com anmelden", "papa@gmail.com als neue Verbindung hinzufügen", `formaction="/connections/google/adopt"`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("page lacks %q\n%s", want, r.body)
		}
	}
	if r.status != http.StatusConflict {
		t.Errorf("status = %d", r.status)
	}
	if conns := gb.connections(t); len(conns) != 1 || conns[0].SignInNeededAt.IsZero() {
		t.Fatalf("the connection was changed: %+v", conns)
	}
	m := regexp.MustCompile(`name="nonce" value="([0-9a-f]+)"`).FindStringSubmatch(r.body)
	if m == nil {
		t.Fatal("no nonce to adopt the sign-in")
	}
	add := gb.b.post("/connections/google/adopt", url.Values{"nonce": {m[1]}})
	if add.status != http.StatusSeeOther || !strings.HasPrefix(add.location, "/connections/") {
		t.Fatalf("adopt = %d %q", add.status, add.location)
	}
	conns := gb.connections(t)
	if len(conns) != 2 || conns[1].Username != "papa@gmail.com" || conns[1].Kind != "google" {
		t.Fatalf("connections = %+v", conns)
	}
	if _, token, _ := gb.b.db.ConnectionCredentials(gb.h.ID, conns[1].ID); token != "1//refresh-papa" {
		t.Errorf("token = %q", token)
	}
	// Once only.
	if again := gb.b.post("/connections/google/adopt", url.Values{"nonce": {m[1]}}); again.location == add.location && len(gb.connections(t)) != 2 {
		t.Error("the same sign-in was added twice")
	}
}

// A second account signs in with the same app registration and needs no setup again.
func TestASecondAccountNeedsOnlyTheSignIn(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	gb.signIn(t)
	page := formOf(t, gb.b.get("/connections/new?service=google").body, "/connections/google/app")
	if strings.Contains(page, `<ol class="howto">`) || !strings.Contains(page, "Schon verbunden") || !strings.Contains(page, `class="host">mama@gmail.com`) {
		t.Errorf("a registered box does not offer the short way:\n%s", page)
	}
	gb.g.set(func(g *googleFake) { g.account = "papa@gmail.com" })
	if r := gb.b.post("/connections/google/app", url.Values{"action": {"continue"}}); r.status != http.StatusSeeOther || r.location != "/connections/google/signin" {
		t.Fatalf("continue = %d %q", r.status, r.location)
	}
	state, _ := gb.g.hand(gb.start(t, nil), "p2")
	if r := gb.b.get(googleCallback(state, "p2")); r.status != http.StatusSeeOther {
		t.Fatalf("second sign-in = %d", r.status)
	}
	if conns := gb.connections(t); len(conns) != 2 || conns[1].Username != "papa@gmail.com" {
		t.Fatalf("connections = %+v", conns)
	}
}

func TestAnotherAppRegistrationNamesWhoMustSignInAgain(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	id := gb.signIn(t)
	page := gb.b.get("/connections/" + id).body
	if !strings.Contains(page, "mama@gmail.com</b> muss sich danach neu anmelden") {
		t.Errorf("the page does not say who has to sign in again:\n%s", page)
	}
	r := gb.b.post("/connections/google/app", url.Values{"action": {"replace"}, "connection": {id}, "client_id": {"999-other.apps.googleusercontent.com"}, "client_secret": {"GOCSPX-new"}})
	if r.status != http.StatusSeeOther || r.location != "/connections/google/signin?connection="+id {
		t.Fatalf("replace = %d %q", r.status, r.location)
	}
	if conn, _ := gb.b.db.Connection(gb.h.ID, id); conn.SignInNeededAt.IsZero() {
		t.Error("a sign-in that belongs to the old client still counts")
	}
}

// ---- gates and the API ----

func TestGoogleStopsWhenTheCalendarServicesAreOff(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	gb.register(t)
	if err := gb.b.db.SetServiceEnabled(outbound.Calendars, false); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path string }{{http.MethodPost, "/connections/google/start"}, {http.MethodPost, "/connections/google/paste"}, {http.MethodPost, "/connections/google/app"}} {
		if r := gb.b.do(c.method, c.path, url.Values{"address": {"x"}}, nil); r.status != http.StatusSeeOther || r.location != "/connections/new" {
			t.Errorf("%s %s = %d %q, want back to the page that says why", c.method, c.path, r.status, r.location)
		}
	}
	if r := gb.b.get("/connections/new?service=google"); !strings.Contains(r.body, "offline-note") {
		t.Error("the connect page does not say that calendar services are off")
	}
}

func TestGoogleThroughTheAPI(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	c := newAPIClient(t, gb.b)
	c.cookie = true
	status, app, _ := c.call(http.MethodGet, "/google", nil)
	if status != http.StatusOK || app["configured"] != false || app["redirect_uris"] == nil {
		t.Fatalf("GET /google = %d %v", status, app)
	}
	redirects := app["redirect_uris"].(map[string]any)
	if redirects["relay"] != googleauth.RelayRedirectURI || redirects["fallback"] != googleauth.FallbackRedirectURI || app["scope"] != googleauth.Scope {
		t.Errorf("fixed values = %v", app)
	}
	if status, body, _ := c.call(http.MethodPut, "/google", map[string]any{"client_id": "nope", "client_secret": gSecret}); status != http.StatusUnprocessableEntity || body["field"] != "client_id" {
		t.Fatalf("a bad client ID = %d %v", status, body)
	}
	status, app, _ = c.call(http.MethodPut, "/google", map[string]any{"client_id": gClientID, "client_secret": gSecret})
	if status != http.StatusOK || app["configured"] != true || app["client_id"] != gClientID {
		t.Fatalf("PUT /google = %d %v", status, app)
	}
	if _, has := app["client_secret"]; has {
		t.Error("the secret is returned")
	}
	status, start, _ := c.call(http.MethodPost, "/google/authorize", map[string]any{})
	if status != http.StatusOK || start["way"] != "relay" || start["redirect_uri"] != googleauth.RelayRedirectURI {
		t.Fatalf("authorize = %d %v", status, start)
	}
	state, _ := gb.g.hand(start["url"].(string), "api-code")
	status, conn, _ := c.call(http.MethodPost, "/google/callback", map[string]any{"state": state, "code": "api-code", "scope": googleauth.Scope})
	if status != http.StatusCreated || conn["kind"] != "google" || conn["username"] != "mama@gmail.com" {
		t.Fatalf("callback = %d %v", status, conn)
	}
	id := conn["id"].(string)
	// The same state again: 422 and a word for it.
	if status, body, _ := c.call(http.MethodPost, "/google/callback", map[string]any{"state": state, "code": "api-code"}); status != http.StatusUnprocessableEntity || body["problem"] != "state_used" {
		t.Fatalf("second callback = %d %v", status, body)
	}
	// The calendars come from Google, with their access.
	status, found, _ := c.call(http.MethodGet, "/calendar-connections/"+id+"/calendars", nil)
	if status != http.StatusOK {
		t.Fatalf("discover = %d %v", status, found)
	}
	cals := found["calendars"].([]any)
	if len(cals) != 4 || cals[0].(map[string]any)["access"] != "read_write" || cals[1].(map[string]any)["access"] != "read_only" {
		t.Errorf("calendars = %v", cals)
	}
	// A pasted fallback address works through the API too.
	start2 := func() (string, string) {
		_, s, _ := c.call(http.MethodPost, "/google/authorize", map[string]any{"way": "fallback", "connection_id": id})
		st, _ := gb.g.hand(s["url"].(string), "fb")
		return st, s["redirect_uri"].(string)
	}
	st, redirect := start2()
	if redirect != googleauth.FallbackRedirectURI {
		t.Errorf("fallback redirect = %q", redirect)
	}
	status, conn2, _ := c.call(http.MethodPost, "/google/callback", map[string]any{"url": "http://127.0.0.1:8099/?state=" + url.QueryEscape(st) + "&code=fb&scope=" + url.QueryEscape(googleauth.Scope)})
	if status != http.StatusOK || conn2["id"] != id {
		t.Fatalf("sign in again = %d %v", status, conn2)
	}
	// No password for a Google connection; a password connection cannot be made with Google.
	if status, _, _ := c.call(http.MethodPut, "/calendar-connections/"+id+"/credentials", map[string]any{"password": "x"}); status != http.StatusUnprocessableEntity {
		t.Errorf("a password for a Google connection = %d", status)
	}
	// The connection list says when Google stops accepting it.
	cal := googleauth.CalendarURL(googleauth.CalDAVBase, "mama@gmail.com")
	if status, body, _ := c.call(http.MethodPost, "/calendar-connections/"+id+"/calendars", map[string]any{"url": cal, "name": "Mama"}); status != http.StatusCreated {
		t.Fatalf("tick a calendar = %d %v", status, body)
	}
	gb.g.set(func(g *googleFake) { g.failToken = "invalid_grant" })
	gb.b.now = gb.b.now.Add(3 * time.Hour)
	cals2, _ := gb.b.db.SyncedCalendars(gb.h.ID)
	_ = gb.b.sync.Sync(context.Background(), gb.h.ID, cals2[0].ID)
	_, _, list := c.call(http.MethodGet, "/calendar-connections", nil)
	if len(list) != 1 || list[0].(map[string]any)["needs_signin"] != true || list[0].(map[string]any)["signin_needed_at"] == nil {
		t.Errorf("connection list = %v", list)
	}
}

func TestGoogleAPIIsRefusedWhileCalendarServicesAreOff(t *testing.T) {
	t.Parallel()
	gb := newGoogleBox(t)
	c := newAPIClient(t, gb.b)
	c.cookie = true
	c.call(http.MethodPut, "/google", map[string]any{"client_id": gClientID, "client_secret": gSecret})
	_ = gb.b.db.SetServiceEnabled(outbound.Calendars, false)
	for _, call := range []struct{ method, path string }{{http.MethodPost, "/google/authorize"}, {http.MethodPost, "/google/callback"}} {
		if status, _, _ := c.call(call.method, call.path, map[string]any{"url": "http://127.0.0.1:8099/?state=a&code=b"}); status != http.StatusConflict {
			t.Errorf("%s %s = %d, want 409", call.method, call.path, status)
		}
	}
}
