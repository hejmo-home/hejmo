package store_test

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/googleauth"
	"github.com/hejmo-home/hejmo/internal/store"
)

type googleFixture struct {
	db     *store.DB
	path   string
	h      store.Household
	person store.Person
}

func newGoogleFixture(t *testing.T) googleFixture {
	t.Helper()
	db, path := openSealed(t)
	h, p := seedParent(t, db)
	return googleFixture{db, path, h, p}
}

func TestGoogleAppIsStoredSealedAndTheSecretNeverListed(t *testing.T) {
	f := newGoogleFixture(t)
	if _, err := f.db.GoogleApp(f.h.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("before: %v", err)
	}
	if _, err := f.db.SetGoogleApp(f.h.ID, f.person.ID, "123-abc.apps.googleusercontent.com", "GOCSPX-hunter2-hunter2", t0); err != nil {
		t.Fatal(err)
	}
	app, err := f.db.GoogleApp(f.h.ID)
	if err != nil || app.ClientID != "123-abc.apps.googleusercontent.com" || app.CreatedBy != f.person.ID || !app.CreatedAt.Equal(t0) {
		t.Fatalf("app = %+v, %v", app, err)
	}
	id, secret, err := f.db.GoogleAppSecret(f.h.ID)
	if err != nil || id != app.ClientID || secret != "GOCSPX-hunter2-hunter2" {
		t.Fatalf("secret = %q %q %v", id, secret, err)
	}
	f.db.SQL().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	f.db.Close()
	for _, name := range []string{f.path, f.path + "-wal"} {
		if raw, err := os.ReadFile(name); err == nil && bytes.Contains(raw, []byte("GOCSPX-hunter2-hunter2")) {
			t.Fatalf("%s holds the client secret in plain text", name)
		}
	}
}

func TestAnotherGoogleAppSendsEveryGoogleConnectionToSignInAgain(t *testing.T) {
	f := newGoogleFixture(t)
	f.db.SetGoogleApp(f.h.ID, f.person.ID, "one.apps.googleusercontent.com", "s1", t0)
	conn, err := f.db.CreateGoogleConnection(f.h.ID, "mama@gmail.com", "refresh-1", t0)
	if err != nil {
		t.Fatal(err)
	}
	// The same client with a new secret keeps every sign-in.
	if changed, err := f.db.SetGoogleApp(f.h.ID, f.person.ID, "one.apps.googleusercontent.com", "s2", t0.Add(time.Hour)); err != nil || changed {
		t.Fatalf("same client: changed=%v err=%v", changed, err)
	}
	got, _ := f.db.Connection(f.h.ID, conn.ID)
	if !got.SignInNeededAt.IsZero() {
		t.Fatalf("marked for a new secret: %+v", got)
	}
	// Another client: the refresh tokens belong to the old one.
	if changed, err := f.db.SetGoogleApp(f.h.ID, f.person.ID, "two.apps.googleusercontent.com", "s3", t0.Add(2*time.Hour)); err != nil || !changed {
		t.Fatalf("other client: changed=%v err=%v", changed, err)
	}
	got, _ = f.db.Connection(f.h.ID, conn.ID)
	if !got.SignInNeededAt.Equal(t0.Add(2 * time.Hour)) {
		t.Fatalf("not marked: %+v", got)
	}
}

func TestGoogleConnectionKeepsTheRefreshTokenSealed(t *testing.T) {
	f := newGoogleFixture(t)
	conn, err := f.db.CreateGoogleConnection(f.h.ID, "mama@gmail.com", "1//0g-refresh-secret-token", t0)
	if err != nil {
		t.Fatal(err)
	}
	if conn.Kind != "google" || conn.Username != "mama@gmail.com" || conn.URL != googleauth.CalDAVBase || !conn.SignedInAt.Equal(t0) || !conn.SignInNeededAt.IsZero() {
		t.Fatalf("connection = %+v", conn)
	}
	account, token, err := f.db.ConnectionCredentials(f.h.ID, conn.ID)
	if err != nil || account != "mama@gmail.com" || token != "1//0g-refresh-secret-token" {
		t.Fatalf("credentials = %q %q %v", account, token, err)
	}
	all, _ := f.db.Connections(f.h.ID)
	if len(all) != 1 || all[0].ID != conn.ID || all[0].Kind != "google" {
		t.Fatalf("Connections = %+v", all)
	}
	f.db.SQL().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	f.db.Close()
	for _, name := range []string{f.path, f.path + "-wal"} {
		if raw, err := os.ReadFile(name); err == nil && bytes.Contains(raw, []byte("1//0g-refresh-secret-token")) {
			t.Fatalf("%s holds the refresh token in plain text", name)
		}
	}
}

func TestSignInNeededIsMarkedAndClearedByANewRefreshToken(t *testing.T) {
	f := newGoogleFixture(t)
	conn, _ := f.db.CreateGoogleConnection(f.h.ID, "mama@gmail.com", "r1", t0)
	cal, err := f.db.AddGoogleCalendar(f.h.ID, conn.ID, store.NewGoogleCalendar{Name: "Mama", CalendarID: "mama@gmail.com", ReadOnly: false, Color: "#B4583A", IntervalMinutes: 15, Now: t0})
	if err != nil || cal.Kind != "google" || cal.ReadOnly || cal.URL != googleauth.CalendarURL(googleauth.CalDAVBase, "mama@gmail.com") {
		t.Fatalf("calendar = %+v, %v", cal, err)
	}
	ro, err := f.db.AddGoogleCalendar(f.h.ID, conn.ID, store.NewGoogleCalendar{Name: "Papa", CalendarID: "papa@gmail.com", SourceReadOnly: true, Color: "#2F5E7A", IntervalMinutes: 15, Now: t0})
	if err != nil || !ro.ReadOnly {
		t.Fatalf("read-only calendar = %+v, %v", ro, err)
	}
	// Both are read-only or two-way as Google says: a parent cannot make a calendar Google shares read-only two-way.
	if err := f.db.SetCalendarReadOnly(f.h.ID, ro.ID, false); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("made a read-only share two-way: %v", err)
	}
	if err := f.db.SetCalendarReadOnly(f.h.ID, cal.ID, true); err != nil {
		t.Fatalf("a writable Google calendar can be made read-only: %v", err)
	}
	if err := f.db.MarkSignInNeeded(f.h.ID, conn.ID, t0.Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ := f.db.Connection(f.h.ID, conn.ID)
	if !got.SignInNeededAt.Equal(t0.Add(7 * 24 * time.Hour)) {
		t.Fatalf("not marked: %+v", got)
	}
	// Marking again keeps the first time: "since" says when it began.
	f.db.MarkSignInNeeded(f.h.ID, conn.ID, t0.Add(8*24*time.Hour))
	if got, _ = f.db.Connection(f.h.ID, conn.ID); !got.SignInNeededAt.Equal(t0.Add(7 * 24 * time.Hour)) {
		t.Fatalf("since moved: %+v", got)
	}
	if err := f.db.SetGoogleRefreshToken(f.h.ID, conn.ID, "r2", t0.Add(9*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ = f.db.Connection(f.h.ID, conn.ID)
	_, token, _ := f.db.ConnectionCredentials(f.h.ID, conn.ID)
	if !got.SignInNeededAt.IsZero() || !got.SignedInAt.Equal(t0.Add(9*24*time.Hour)) || token != "r2" {
		t.Fatalf("after: %+v %q", got, token)
	}
	// The calendars are due at once again.
	due, _ := f.db.DueCalendars(t0.Add(9 * 24 * time.Hour))
	if len(due) != 2 {
		t.Fatalf("due = %d", len(due))
	}
	if err := f.db.DeleteConnection(f.h.ID, conn.ID); err != nil {
		t.Fatalf("a Google connection can be removed: %v", err)
	}
	if all, _ := f.db.Connections(f.h.ID); len(all) != 0 {
		t.Fatalf("still there: %+v", all)
	}
}

func TestGoogleLoginIsUsedOnceAndOnlyBeforeItExpires(t *testing.T) {
	f := newGoogleFixture(t)
	login := store.GoogleLogin{Nonce: "n1", PersonID: f.person.ID, Verifier: "the-verifier-secret", RedirectURI: googleauth.RelayRedirectURI, ExpiresAt: t0.Add(15 * time.Minute)}
	if err := f.db.CreateGoogleLogin(f.h.ID, login, t0); err != nil {
		t.Fatal(err)
	}
	got, err := f.db.TakeGoogleLogin(f.h.ID, "n1", t0.Add(time.Minute))
	if err != nil || got.Verifier != "the-verifier-secret" || got.PersonID != f.person.ID || got.RedirectURI != googleauth.RelayRedirectURI {
		t.Fatalf("take = %+v, %v", got, err)
	}
	if _, err := f.db.TakeGoogleLogin(f.h.ID, "n1", t0.Add(2*time.Minute)); !errors.Is(err, store.ErrUsed) {
		t.Fatalf("second take: %v", err)
	}
	if _, err := f.db.TakeGoogleLogin(f.h.ID, "never-made", t0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	login.Nonce = "n2"
	f.db.CreateGoogleLogin(f.h.ID, login, t0)
	if _, err := f.db.TakeGoogleLogin(f.h.ID, "n2", t0.Add(16*time.Minute)); !errors.Is(err, store.ErrExpired) {
		t.Fatalf("late: %v", err)
	}
	// An expired login is used up too: a second try is "used", not valid.
	if _, err := f.db.TakeGoogleLogin(f.h.ID, "n2", t0.Add(time.Minute)); !errors.Is(err, store.ErrUsed) {
		t.Fatalf("late then early: %v", err)
	}
	other, _ := f.db.CreateHousehold(store.Household{Name: "Other"})
	login.Nonce = "n3"
	f.db.CreateGoogleLogin(f.h.ID, login, t0)
	if _, err := f.db.TakeGoogleLogin(other.ID, "n3", t0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("another household takes the login: %v", err)
	}
	f.db.SQL().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	f.db.Close()
	if raw, err := os.ReadFile(f.path); err == nil && bytes.Contains(raw, []byte("the-verifier-secret")) {
		t.Fatal("the PKCE verifier is stored in plain text")
	}
}

func TestAWrongAccountSignInIsKeptOnceForTheNewConnection(t *testing.T) {
	f := newGoogleFixture(t)
	login := store.GoogleLogin{Nonce: "n9", PersonID: f.person.ID, Verifier: "v", RedirectURI: googleauth.RelayRedirectURI, ExpiresAt: t0.Add(15 * time.Minute)}
	f.db.CreateGoogleLogin(f.h.ID, login, t0)
	if _, err := f.db.TakeGoogleLogin(f.h.ID, "n9", t0); err != nil {
		t.Fatal(err)
	}
	if err := f.db.StashGoogleToken(f.h.ID, "n9", "papa@gmail.com", "1//papa-refresh", t0); err != nil {
		t.Fatal(err)
	}
	account, token, err := f.db.TakeStashedGoogleToken(f.h.ID, "n9", t0)
	if err != nil || account != "papa@gmail.com" || token != "1//papa-refresh" {
		t.Fatalf("stash = %q %q %v", account, token, err)
	}
	if _, _, err := f.db.TakeStashedGoogleToken(f.h.ID, "n9", t0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second take: %v", err)
	}
	other, _ := f.db.CreateHousehold(store.Household{Name: "Other"})
	f.db.StashGoogleToken(f.h.ID, "n9", "papa@gmail.com", "1//papa-refresh", t0)
	if _, _, err := f.db.TakeStashedGoogleToken(other.ID, "n9", t0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("another household takes it: %v", err)
	}
	f.db.SQL().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	f.db.Close()
	if raw, err := os.ReadFile(f.path); err == nil && bytes.Contains(raw, []byte("1//papa-refresh")) {
		t.Fatal("the stashed refresh token is stored in plain text")
	}
}

// Review of ticket 18: a stashed refresh token must not wait for the next sign-in to be forgotten.
func TestExpiredSignInsLoseTheirStashedTokenWithoutANewSignIn(t *testing.T) {
	f := newGoogleFixture(t)
	login := store.GoogleLogin{Nonce: "n10", PersonID: f.person.ID, Verifier: "v", RedirectURI: googleauth.RelayRedirectURI, ExpiresAt: t0.Add(15 * time.Minute)}
	f.db.CreateGoogleLogin(f.h.ID, login, t0)
	f.db.TakeGoogleLogin(f.h.ID, "n10", t0)
	f.db.StashGoogleToken(f.h.ID, "n10", "papa@gmail.com", "1//papa-refresh", t0)
	if err := f.db.PruneGoogleLogins(t0.Add(10 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.db.TakeStashedGoogleToken(f.h.ID, "n10", t0.Add(10*time.Minute)); err != nil {
		t.Fatalf("pruned before the sign-in expired: %v", err)
	}
	f.db.StashGoogleToken(f.h.ID, "n10", "papa@gmail.com", "1//papa-refresh", t0)
	if err := f.db.PruneGoogleLogins(t0.Add(16 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.db.TakeStashedGoogleToken(f.h.ID, "n10", t0.Add(16*time.Minute)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an expired sign-in still holds a token: %v", err)
	}
	if err := f.db.PruneGoogleLogins(t0.Add(26 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	var n int
	f.db.SQL().QueryRow(`SELECT COUNT(*) FROM google_login`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d logins left after a day", n)
	}
}

// Review of ticket 18: the offer to add the other account lives from the stash, not from "Open Google".
func TestAStashedTokenLivesFromTheStashNotFromTheStartOfTheSignIn(t *testing.T) {
	f := newGoogleFixture(t)
	login := store.GoogleLogin{Nonce: "n11", PersonID: f.person.ID, Verifier: "v", RedirectURI: googleauth.RelayRedirectURI, ExpiresAt: t0.Add(15 * time.Minute)}
	f.db.CreateGoogleLogin(f.h.ID, login, t0)
	f.db.TakeGoogleLogin(f.h.ID, "n11", t0)
	late := t0.Add(14 * time.Minute) // the parent finished at Google late
	if err := f.db.StashGoogleToken(f.h.ID, "n11", "papa@gmail.com", "1//papa-refresh", late); err != nil {
		t.Fatal(err)
	}
	if err := f.db.PruneGoogleLogins(late.Add(store.StashLifetime - time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.db.TakeStashedGoogleToken(f.h.ID, "n11", late.Add(store.StashLifetime-time.Minute)); err != nil {
		t.Fatalf("the offer ran out with the sign-in's window, not its own: %v", err)
	}
	f.db.StashGoogleToken(f.h.ID, "n11", "papa@gmail.com", "1//papa-refresh", late)
	if err := f.db.PruneGoogleLogins(late.Add(store.StashLifetime + time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.db.TakeStashedGoogleToken(f.h.ID, "n11", late.Add(store.StashLifetime+time.Minute)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a stashed token outlived its own lifetime: %v", err)
	}
}

// Review of ticket 18: the offer ends after StashLifetime even if no scheduler tick has pruned it yet.
func TestAStashedTokenIsNotTakenAfterItsLifetimeEvenUnpruned(t *testing.T) {
	f := newGoogleFixture(t)
	login := store.GoogleLogin{Nonce: "n12", PersonID: f.person.ID, Verifier: "v", RedirectURI: googleauth.RelayRedirectURI, ExpiresAt: t0.Add(15 * time.Minute)}
	f.db.CreateGoogleLogin(f.h.ID, login, t0)
	f.db.TakeGoogleLogin(f.h.ID, "n12", t0)
	f.db.StashGoogleToken(f.h.ID, "n12", "papa@gmail.com", "1//papa-refresh", t0)
	if _, _, err := f.db.TakeStashedGoogleToken(f.h.ID, "n12", t0.Add(store.StashLifetime+time.Second)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an unpruned expired stash was taken: %v", err)
	}
}
