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
		if g.account != "mama@gmail.com" { // a sign-in with another account: that account's own primary calendar
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

func TestConnectPageOffersGoogleBetweenICloudAndOtherServers(t *testing.T) {
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
		if len(radios) != 3 || radios[0][1] != "icloud" || radios[1][1] != "google" || radios[2][1] != "caldav" {
			t.Errorf("%s: choice cards = %v, want icloud, google, caldav", lang, radios)
		}
		if !strings.Contains(block, `<input type="radio" name="service" value="google" checked>`) {
			t.Errorf("%s: Google is not the checked card", lang)
		}
		if n := strings.Count(block, `<ol class="howto">`); n != 3 {
			t.Errorf("%s: %d guides on the page, want 3", lang, n)
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
