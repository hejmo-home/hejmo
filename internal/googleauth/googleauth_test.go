package googleauth_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/googleauth"
)

// The fixed values of ADR 0013 and ticket #18.
func TestFixedValues(t *testing.T) {
	t.Parallel()
	if googleauth.RelayRedirectURI != "https://hejmo-home.github.io/oauth/" ||
		googleauth.FallbackRedirectURI != "http://127.0.0.1:8099/" ||
		googleauth.Scope != "https://www.googleapis.com/auth/calendar" {
		t.Fatal("fixed values changed")
	}
}

// RFC 7636, appendix B: the verifier and challenge of the specification's example.
func TestChallengeIsS256OfTheVerifier(t *testing.T) {
	t.Parallel()
	got := googleauth.Challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
	if got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("challenge = %s", got)
	}
	v1, err := googleauth.NewVerifier()
	v2, _ := googleauth.NewVerifier()
	if err != nil || len(v1) < 43 || v1 == v2 {
		t.Fatalf("verifier = %q, %q, %v", v1, v2, err)
	}
}

type signer struct{ key byte }

func (s signer) Seal(b []byte) ([]byte, error) {
	out := make([]byte, len(b)+1)
	out[0] = s.key
	for i, c := range b {
		out[i+1] = c ^ s.key
	}
	return out, nil
}
func (s signer) Open(b []byte) ([]byte, error) {
	if len(b) == 0 || b[0] != s.key {
		return nil, errors.New("not ours")
	}
	out := make([]byte, len(b)-1)
	for i, c := range b[1:] {
		out[i] = c ^ s.key
	}
	return out, nil
}

func TestStateCarriesTheBoxAddressAndIsSignedByTheBox(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	tok, err := googleauth.NewStateToken(signer{7}, now)
	if err != nil {
		t.Fatal(err)
	}
	state := googleauth.State("https://192.168.178.20", tok.Token)
	if !strings.HasPrefix(state, "hj1.") || strings.Count(state, ".") != 2 {
		t.Fatalf("state = %q", state)
	}
	origin, token, err := googleauth.ParseState(state)
	if err != nil || origin != "https://192.168.178.20" || token != tok.Token {
		t.Fatalf("parsed = %q %q %v", origin, token, err)
	}
	nonce, err := googleauth.VerifyStateToken(signer{7}, token, now.Add(14*time.Minute))
	if err != nil || nonce != tok.Nonce || nonce == "" {
		t.Fatalf("verify = %q, %v", nonce, err)
	}
}

func TestStateIsRejectedWhenForeignAlteredOrExpired(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	tok, _ := googleauth.NewStateToken(signer{7}, now)
	if _, err := googleauth.VerifyStateToken(signer{9}, tok.Token, now); !errors.Is(err, googleauth.ErrStateUnknown) {
		t.Errorf("foreign key: %v", err)
	}
	if _, err := googleauth.VerifyStateToken(signer{7}, "not base64 !", now); !errors.Is(err, googleauth.ErrStateUnknown) {
		t.Errorf("garbage: %v", err)
	}
	if _, err := googleauth.VerifyStateToken(signer{7}, tok.Token, now.Add(googleauth.StateLifetime+time.Second)); !errors.Is(err, googleauth.ErrStateExpired) {
		t.Errorf("late: %v", err)
	}
	for _, bad := range []string{"", "abc", "hj2.x.y", "hj1.x", "hj1..y", "hj1.!!.y"} {
		if _, _, err := googleauth.ParseState(bad); !errors.Is(err, googleauth.ErrStateUnknown) {
			t.Errorf("ParseState(%q) = %v", bad, err)
		}
	}
}

func TestAuthURLAsksForOfflineAccessWithPKCE(t *testing.T) {
	t.Parallel()
	c := googleauth.New(nil)
	raw := c.AuthURL("id.apps.googleusercontent.com", googleauth.RelayRedirectURI, "hj1.a.b", "chal", "mama@gmail.com")
	u, err := url.Parse(raw)
	if err != nil || u.Host != "accounts.google.com" {
		t.Fatalf("url = %s %v", raw, err)
	}
	q := u.Query()
	for k, want := range map[string]string{
		"client_id": "id.apps.googleusercontent.com", "redirect_uri": googleauth.RelayRedirectURI, "response_type": "code",
		"scope": googleauth.Scope, "state": "hj1.a.b", "code_challenge": "chal", "code_challenge_method": "S256",
		"access_type": "offline", "prompt": "consent", "login_hint": "mama@gmail.com",
	} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	if q2, _ := url.Parse(c.AuthURL("x", googleauth.RelayRedirectURI, "s", "c", "")); q2.Query().Has("login_hint") {
		t.Error("login_hint without an account")
	}
}

func TestPastedFallbackAddress(t *testing.T) {
	t.Parallel()
	cb, err := googleauth.ParseCallbackURL("  http://127.0.0.1:8099/?state=hj1.Zm9v.Kq7&code=4%2F0AVMB&scope=https://www.googleapis.com/auth/calendar \n")
	if err != nil || cb.Code != "4/0AVMB" || cb.State != "hj1.Zm9v.Kq7" || cb.Error != "" || cb.Scope != googleauth.Scope {
		t.Fatalf("callback = %+v, %v", cb, err)
	}
	cb, err = googleauth.ParseCallbackURL("http://127.0.0.1:8099/?error=access_denied&state=hj1.Zm9v.Kq7")
	if err != nil || cb.Error != "access_denied" || cb.State == "" {
		t.Fatalf("cancelled = %+v, %v", cb, err)
	}
	for _, bad := range []string{
		"", "https://accounts.google.com/signin/oauth/consent?authuser=0", "http://127.0.0.1:8099/", "http://127.0.0.1:8099/?foo=bar",
		"http://127.0.0.1:8100/?code=x&state=y", "http://evil.example/?code=x&state=y", "code=x&state=y", "http://127.0.0.1:8099/?code=x",
	} {
		if _, err := googleauth.ParseCallbackURL(bad); !errors.Is(err, googleauth.ErrNotACallback) {
			t.Errorf("ParseCallbackURL(%q) = %v", bad, err)
		}
	}
}

// fakeGoogle stands in for Google's token endpoint and the Calendar API.
type fakeGoogle struct {
	t      *testing.T
	srv    *httptest.Server
	form   url.Values
	reply  func(url.Values) (int, any)
	cals   []map[string]any
	bearer string
}

func newFake(t *testing.T) *fakeGoogle {
	f := &fakeGoogle{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.form = r.PostForm
		status, body := f.reply(r.PostForm)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("GET /calendar/v3/users/me/calendarList", func(w http.ResponseWriter, r *http.Request) {
		f.bearer = r.Header.Get("Authorization")
		if r.URL.Query().Get("pageToken") == "" && len(f.cals) > 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": f.cals[:1], "nextPageToken": "p2"})
			return
		}
		items := f.cals
		if r.URL.Query().Get("pageToken") != "" {
			items = f.cals[1:]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGoogle) client() *googleauth.Client {
	return googleauth.New(f.srv.Client(), googleauth.WithEndpoints(googleauth.Endpoints{
		Auth: "https://accounts.example/auth", Token: f.srv.URL + "/token", CalendarList: f.srv.URL + "/calendar/v3/users/me/calendarList",
	}))
}

var t0 = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func TestExchangeSendsTheCodeWithPKCEAndReturnsTheRefreshToken(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.reply = func(url.Values) (int, any) {
		return 200, map[string]any{"access_token": "acc-1", "refresh_token": "ref-1", "expires_in": 3599, "scope": googleauth.Scope, "token_type": "Bearer"}
	}
	tok, err := f.client().Exchange(context.Background(), "cid", "sec", "the-code", "the-verifier", googleauth.RelayRedirectURI, t0)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Access != "acc-1" || tok.Refresh != "ref-1" || !tok.Expires.Equal(t0.Add(3599*time.Second)) {
		t.Fatalf("tokens = %+v", tok)
	}
	for k, want := range map[string]string{"grant_type": "authorization_code", "code": "the-code", "code_verifier": "the-verifier",
		"redirect_uri": googleauth.RelayRedirectURI, "client_id": "cid", "client_secret": "sec"} {
		if f.form.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, f.form.Get(k), want)
		}
	}
}

func TestExchangeNamesWhatWentWrong(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		status int
		body   map[string]any
		want   error
	}{
		{"code used or expired", 400, map[string]any{"error": "invalid_grant", "error_description": "Bad Request"}, googleauth.ErrInvalidGrant},
		{"client refused", 401, map[string]any{"error": "invalid_client"}, googleauth.ErrInvalidClient},
		{"no calendar tick", 200, map[string]any{"access_token": "a", "refresh_token": "r", "expires_in": 10, "scope": "openid"}, googleauth.ErrScopeMissing},
		{"no refresh token", 200, map[string]any{"access_token": "a", "expires_in": 10, "scope": googleauth.Scope}, googleauth.ErrNoRefreshToken},
	} {
		f := newFake(t)
		f.reply = func(url.Values) (int, any) { return c.status, c.body }
		if _, err := f.client().Exchange(context.Background(), "cid", "sec", "c", "v", googleauth.RelayRedirectURI, t0); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
}

func TestRefreshAndInvalidGrant(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.reply = func(url.Values) (int, any) {
		return 200, map[string]any{"access_token": "acc-2", "expires_in": 3600, "scope": googleauth.Scope}
	}
	tok, err := f.client().Refresh(context.Background(), "cid", "sec", "ref-1", t0)
	if err != nil || tok.Access != "acc-2" || tok.Refresh != "" || !tok.Expires.Equal(t0.Add(time.Hour)) {
		t.Fatalf("refresh = %+v, %v", tok, err)
	}
	if f.form.Get("grant_type") != "refresh_token" || f.form.Get("refresh_token") != "ref-1" || f.form.Get("client_secret") != "sec" {
		t.Errorf("form = %v", f.form)
	}
	f.reply = func(url.Values) (int, any) {
		return 400, map[string]any{"error": "invalid_grant", "error_description": "Token has been expired or revoked."}
	}
	if _, err := f.client().Refresh(context.Background(), "cid", "sec", "ref-1", t0); !errors.Is(err, googleauth.ErrInvalidGrant) {
		t.Fatalf("err = %v", err)
	}
	f.reply = func(url.Values) (int, any) { return 503, map[string]any{"error": "backend_error"} }
	err = func() error { _, e := f.client().Refresh(context.Background(), "cid", "sec", "r", t0); return e }()
	if err == nil || errors.Is(err, googleauth.ErrInvalidGrant) {
		t.Fatalf("a server error is not invalid_grant: %v", err)
	}
}

func TestCalendarsListsEveryPageWithTheirAccess(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.cals = []map[string]any{
		{"id": "mama@gmail.com", "summary": "mama@gmail.com", "accessRole": "owner", "primary": true},
		{"id": "papa@gmail.com", "summary": "Papa Arbeit", "summaryOverride": "Papa", "accessRole": "reader"},
		{"id": "addressbook#contacts@group.v.calendar.google.com", "summary": "Geburtstage", "accessRole": "reader"},
	}
	got, err := f.client().Calendars(context.Background(), "acc-1")
	if err != nil || len(got) != 3 {
		t.Fatalf("calendars = %+v, %v", got, err)
	}
	if f.bearer != "Bearer acc-1" {
		t.Errorf("authorization = %q", f.bearer)
	}
	if !got[0].Primary || !got[0].Writable() || got[1].Writable() || got[1].Name() != "Papa" || got[2].ID != "addressbook#contacts@group.v.calendar.google.com" {
		t.Errorf("calendars = %+v", got)
	}
	if u := googleauth.CalendarURL("https://apidata.googleusercontent.com/caldav/v2/", got[2].ID); u != "https://apidata.googleusercontent.com/caldav/v2/addressbook%23contacts@group.v.calendar.google.com/events" {
		t.Errorf("url = %s", u)
	}
	if id, ok := googleauth.CalendarIDOf("https://apidata.googleusercontent.com/caldav/v2/addressbook%23contacts@group.v.calendar.google.com/events"); !ok || id != got[2].ID {
		t.Errorf("id = %q %v", id, ok)
	}
	f.cals = nil
	f.reply = nil
}

func TestCalendarsAreRefusedWithAnExpiredToken(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	c := googleauth.New(srv.Client(), googleauth.WithEndpoints(googleauth.Endpoints{CalendarList: srv.URL}))
	if _, err := c.Calendars(context.Background(), "x"); !errors.Is(err, googleauth.ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
}
