package calsync_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/googleauth"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/secret"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/store/storetest"
)

// fakeToken stands in for Google's token endpoint.
type fakeToken struct {
	mu      sync.Mutex
	asked   []map[string]string
	grant   bool // answers invalid_grant
	expires int
}

func (f *fakeToken) serve(t *testing.T) *googleauth.Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		f.asked = append(f.asked, map[string]string{"grant": r.PostForm.Get("grant_type"), "refresh": r.PostForm.Get("refresh_token"), "client": r.PostForm.Get("client_id"), "secret": r.PostForm.Get("client_secret")})
		w.Header().Set("Content-Type", "application/json")
		if f.grant {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "Token has been expired or revoked."})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-" + string(rune('a'+len(f.asked))), "expires_in": f.expires, "scope": googleauth.Scope})
	}))
	t.Cleanup(srv.Close)
	return googleauth.New(srv.Client(), googleauth.WithEndpoints(googleauth.Endpoints{Token: srv.URL}))
}

func (f *fakeToken) calls() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.asked) }

// googleRemote is Google's CalDAV API as the sync sees it: the scripted fake
// server plus the access token it was dialled with and what was written.
type googleRemote struct {
	fakeRemote
	token string
	g     *googleFixture
}

func (r googleRemote) Put(_ context.Context, objectURL, raw, etag string) (string, error) {
	r.g.puts = append(r.g.puts, r.token+" "+objectURL)
	return "etag-put", nil
}

type googleFixture struct {
	*fakeServer
	db     *store.DB
	h      store.Household
	conn   store.Connection
	cal    store.SyncedCalendar
	svc    *calsync.Service
	clock  time.Time
	token  *fakeToken
	tokens []string // access tokens the sync dialled with
	puts   []string
}

const googleSwim = "https://apidata.googleusercontent.com/caldav/v2/mama@gmail.com/events/swim.ics"

func newGoogleFixture(t *testing.T) *googleFixture {
	t.Helper()
	db, err := store.Open(storetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	box, err := secret.Open(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetSecrets(box)
	h, _ := db.CreateHousehold(store.Household{Name: "Home", Region: "DE-BY", Timezone: "Europe/Berlin"})
	g := &googleFixture{fakeServer: newFakeServer(), db: db, h: h, clock: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC), token: &fakeToken{expires: 3600}}
	if _, err := db.SetGoogleApp(h.ID, "", "cid.apps.googleusercontent.com", "GOCSPX-secret", g.clock); err != nil {
		t.Fatal(err)
	}
	g.conn, err = db.CreateGoogleConnection(h.ID, "mama@gmail.com", "1//refresh-1", g.clock)
	if err != nil {
		t.Fatal(err)
	}
	g.cal, err = db.AddGoogleCalendar(h.ID, g.conn.ID, store.NewGoogleCalendar{Name: "Mama", CalendarID: "mama@gmail.com", IntervalMinutes: 5, Now: g.clock})
	if err != nil {
		t.Fatal(err)
	}
	g.svc = calsync.New(db, nil, func() time.Time { return g.clock })
	g.svc.SetGate(outbound.AllowAll())
	g.svc.SetGoogle(g.token.serve(t), func(access string) (calsync.Remote, error) {
		g.tokens = append(g.tokens, access)
		return googleRemote{fakeRemote{g.fakeServer}, access, g}, nil
	})
	return g
}

func (g *googleFixture) sync(t *testing.T) error {
	t.Helper()
	g.clock = g.clock.Add(5 * time.Minute)
	return g.svc.Sync(context.Background(), g.h.ID, g.cal.ID)
}

func (g *googleFixture) titles(t *testing.T) []string {
	evs, err := g.db.EventsBetween(g.h.ID, g.clock.AddDate(-1, 0, 0), g.clock.AddDate(1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		out = append(out, e.Title)
	}
	return out
}

func TestGoogleSyncRenewsTheAccessTokenWithTheStoredRefreshTokenAndImportsEvents(t *testing.T) {
	g := newGoogleFixture(t)
	g.put(googleSwim, "swim@google", "Swimming")
	if err := g.sync(t); err != nil {
		t.Fatal(err)
	}
	if got := g.titles(t); !slices.Equal(got, []string{"Swimming"}) {
		t.Fatalf("events = %v", got)
	}
	if g.token.calls() != 1 {
		t.Fatalf("token calls = %d", g.token.calls())
	}
	a := g.token.asked[0]
	if a["grant"] != "refresh_token" || a["refresh"] != "1//refresh-1" || a["client"] != "cid.apps.googleusercontent.com" || a["secret"] != "GOCSPX-secret" {
		t.Fatalf("token request = %v", a)
	}
	if len(g.tokens) != 1 || g.tokens[0] != "access-b" {
		t.Fatalf("dialled with %v", g.tokens)
	}
	// The access token is good for an hour: the next sync does not ask Google again.
	if err := g.sync(t); err != nil || g.token.calls() != 1 {
		t.Fatalf("second sync: %v, token calls %d", err, g.token.calls())
	}
	conn, _ := g.db.Connection(g.h.ID, g.conn.ID)
	if conn.TokenRenewedAt.IsZero() || !conn.SignInNeededAt.IsZero() {
		t.Fatalf("connection = %+v", conn)
	}
	// An hour later it is renewed.
	g.clock = g.clock.Add(time.Hour)
	if err := g.sync(t); err != nil || g.token.calls() != 2 {
		t.Fatalf("after an hour: %v, token calls %d", err, g.token.calls())
	}
}

func TestInvalidGrantMarksTheConnectionSignInAgainAndLogsIt(t *testing.T) {
	g := newGoogleFixture(t)
	g.put(googleSwim, "swim@google", "Swimming")
	if err := g.sync(t); err != nil {
		t.Fatal(err)
	}
	g.token.grant = true
	g.clock = g.clock.Add(2 * time.Hour) // the access token ran out
	err := g.sync(t)
	if err == nil || !errors.Is(err, googleauth.ErrInvalidGrant) {
		t.Fatalf("err = %v", err)
	}
	conn, _ := g.db.Connection(g.h.ID, g.conn.ID)
	if !conn.SignInNeededAt.Equal(g.clock) {
		t.Fatalf("connection not marked: %+v (clock %v)", conn, g.clock)
	}
	log, _ := g.db.SyncLog(g.h.ID, 10)
	if len(log) != 1 || log[0].Kind != "failure" || log[0].Message.Key != "sync.fail.google_signin" {
		t.Fatalf("sync log = %+v", log)
	}
	cal, _ := g.db.SyncedCalendar(g.h.ID, g.cal.ID)
	if cal.Sync.LastError.Key != "sync.fail.google_signin" || cal.Sync.Failures != 1 {
		t.Fatalf("calendar state = %+v", cal.Sync)
	}
	// The events from before stay.
	if got := g.titles(t); !slices.Equal(got, []string{"Swimming"}) {
		t.Fatalf("events = %v", got)
	}
	// While the connection waits for a new sign-in, Google is not asked again.
	n := g.token.calls()
	if err := g.sync(t); err == nil || g.token.calls() != n {
		t.Fatalf("asked Google again: %v, %d calls", err, g.token.calls())
	}
	// A new sign-in stores a new refresh token: the next sync works and Google is asked with it.
	g.token.grant = false
	if err := g.db.SetGoogleRefreshToken(g.h.ID, g.conn.ID, "1//refresh-2", g.clock); err != nil {
		t.Fatal(err)
	}
	g.put(googleSwim+"2", "piano@google", "Piano")
	if err := g.sync(t); err != nil {
		t.Fatal(err)
	}
	if last := g.token.asked[len(g.token.asked)-1]; last["refresh"] != "1//refresh-2" {
		t.Fatalf("asked with %v", last)
	}
	if got := g.titles(t); len(got) != 2 {
		t.Fatalf("events = %v", got)
	}
}

func TestTheClientSecretRefusedIsNotASignInProblem(t *testing.T) {
	g := newGoogleFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer srv.Close()
	g.svc.SetGoogle(googleauth.New(srv.Client(), googleauth.WithEndpoints(googleauth.Endpoints{Token: srv.URL})), func(string) (calsync.Remote, error) { return nil, errors.New("not dialled") })
	if err := g.sync(t); !errors.Is(err, googleauth.ErrInvalidClient) {
		t.Fatalf("err = %v", err)
	}
	conn, _ := g.db.Connection(g.h.ID, g.conn.ID)
	if !conn.SignInNeededAt.IsZero() {
		t.Fatal("a refused client secret marked the sign-in as dead")
	}
	cal, _ := g.db.SyncedCalendar(g.h.ID, g.cal.ID)
	if cal.Sync.LastError.Key != "sync.fail.google_client" {
		t.Fatalf("error = %+v", cal.Sync.LastError)
	}
}

func TestChangesOnTheBoxAreWrittenToGoogleWithTheAccessToken(t *testing.T) {
	g := newGoogleFixture(t)
	if err := g.sync(t); err != nil {
		t.Fatal(err)
	}
	berlin, _ := time.LoadLocation("Europe/Berlin")
	e, err := g.db.CreateEvent(g.h.ID, calendar.Event{CalendarID: g.cal.ID, Title: "Piano",
		Start: time.Date(2026, 11, 21, 15, 0, 0, 0, berlin), End: time.Date(2026, 11, 21, 16, 0, 0, 0, berlin)})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.svc.Push(context.Background(), g.h.ID, g.cal.ID); err != nil {
		t.Fatal(err)
	}
	want := "access-b " + "https://apidata.googleusercontent.com/caldav/v2/mama@gmail.com/events/" + e.ID + ".ics"
	if len(g.puts) != 1 || g.puts[0] != want {
		t.Fatalf("puts = %v, want %s", g.puts, want)
	}
	if pend, _ := g.db.PendingObjects(g.cal.ID); len(pend) != 0 {
		t.Fatalf("pending = %+v", pend)
	}
}

func TestGoogleCalendarsStopWhenTheCalendarServicesAreSwitchedOff(t *testing.T) {
	g := newGoogleFixture(t)
	g.svc.SetGate(outbound.New(outbound.Closed{}, nil))
	if err := g.sync(t); !errors.Is(err, outbound.ErrRefused) {
		t.Fatalf("err = %v", err)
	}
	if g.token.calls() != 0 {
		t.Fatal("asked Google while the connection is off")
	}
	_ = caldavclient.Calendar{}
}

// Review of ticket 18: a 401 for a cached access token is not a wrong password.
// The token is forgotten and the next sync renews it.
func TestGoogleRefusingACachedAccessTokenRenewsItAtTheNextSync(t *testing.T) {
	g := newGoogleFixture(t)
	g.put(googleSwim, "swim@google", "Swimming")
	if err := g.sync(t); err != nil {
		t.Fatal(err)
	}
	g.stateErr = &caldavclient.StatusError{Code: 401, Status: "401 Unauthorized"}
	if err := g.sync(t); err == nil {
		t.Fatal("a refused token did not fail the sync")
	}
	log, _ := g.db.SyncLog(g.h.ID, 10)
	if len(log) != 1 || log[0].Message.Key != "sync.fail.google_token" {
		t.Fatalf("sync log = %+v", log)
	}
	g.stateErr = nil
	if err := g.sync(t); err != nil {
		t.Fatalf("next sync: %v", err)
	}
	if g.token.calls() != 2 || len(g.tokens) != 3 || g.tokens[2] != "access-c" {
		t.Fatalf("token calls %d, dialled with %v", g.token.calls(), g.tokens)
	}
}
