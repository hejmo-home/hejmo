package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

var sessionT0 = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

// seedHousehold returns a household with Mama, Papa and Sebastian in that order.
func seedHousehold(t *testing.T, db *store.DB) (store.Household, map[string]store.Person) {
	t.Helper()
	h, mama := seedParent(t, db)
	people := map[string]store.Person{"Mama": mama}
	for _, p := range []store.Person{{Name: "Papa", Role: store.RoleParent}, {Name: "Sebastian", Role: store.RoleChild}} {
		added, err := db.AddPerson(h.ID, p)
		if err != nil {
			t.Fatal(err)
		}
		people[p.Name] = added
	}
	return h, people
}

func mustSession(t *testing.T, db *store.DB, hash, personID, device string, created, expires time.Time) {
	t.Helper()
	if err := db.CreateSession(hash, personID, device, created, expires); err != nil {
		t.Fatal(err)
	}
}

// Issue #108: the list shows unexpired browser sessions in household person
// order, then last used, newest first, with the person's marker data.
func TestListBrowserSessionsGroupsByPersonAndLastUsed(t *testing.T) {
	db, _ := openTemp(t)
	h, p := seedHousehold(t, db)
	week := sessionT0.Add(7 * 24 * time.Hour)
	mustSession(t, db, "papa-old", p["Papa"].ID, "windows|edge", sessionT0.Add(-72*time.Hour), week)
	mustSession(t, db, "mama-a", p["Mama"].ID, "mac|firefox", sessionT0.Add(-48*time.Hour), week)
	mustSession(t, db, "mama-b", p["Mama"].ID, "iphone|safari", sessionT0.Add(-24*time.Hour), week)
	mustSession(t, db, "expired", p["Mama"].ID, "ipad|safari", sessionT0.Add(-96*time.Hour), sessionT0.Add(-time.Hour))
	// The older sign-in was used more recently than the newer one.
	db.TouchSession("mama-a", sessionT0.Add(-time.Hour))

	got, err := db.ListBrowserSessions(h.ID, sessionT0)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, s := range got {
		order = append(order, s.Person.Name+"/"+s.Device)
	}
	want := []string{"Mama/mac|firefox", "Mama/iphone|safari", "Papa/windows|edge"}
	if len(order) != len(want) {
		t.Fatalf("list = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("list = %v, want %v", order, want)
		}
	}
	if got[0].ID == "" || got[0].ID == "mama-a" || got[0].ID == got[1].ID {
		t.Errorf("ids = %q, %q: want public ids, not the token hash", got[0].ID, got[1].ID)
	}
	if !got[0].SignedIn.Equal(sessionT0.Add(-48*time.Hour)) || !got[0].Expires.Equal(week) {
		t.Errorf("times = %v / %v", got[0].SignedIn, got[0].Expires)
	}
}

// A paired personal device's device session is not a browser session.
func TestListBrowserSessionsLeavesOutPairedPersonalDevices(t *testing.T) {
	db, _ := openTemp(t)
	h, p := seedHousehold(t, db)
	mustSession(t, db, "browser", p["Mama"].ID, "mac|safari", sessionT0, sessionT0.Add(time.Hour))
	if _, err := db.SQL().Exec(`INSERT INTO personal_devices (id, household_id, person_id, name, created_at) VALUES ('ph1', ?, ?, 'Mia''s personal device', ?)`, h.ID, p["Mama"].ID, sessionT0.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`INSERT INTO sessions (id, token_hash, person_id, created_at, expires_at, personal_device_id) VALUES ('personal-device-sess-id', 'personal-device-tok', ?, ?, ?, 'ph1')`,
		p["Mama"].ID, sessionT0.Format(time.RFC3339), sessionT0.Add(time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	got, err := db.ListBrowserSessions(h.ID, sessionT0)
	if err != nil || len(got) != 1 || got[0].Device != "mac|safari" {
		t.Fatalf("list = %+v, %v", got, err)
	}
	if err := db.EndBrowserSession(h.ID, "personal-device-sess-id"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ending a personal device's session = %v, want not found", err)
	}
	if _, err := db.SessionPerson("personal-device-tok", sessionT0); err != nil {
		t.Fatalf("the personal device's session is gone: %v", err)
	}
}

func TestEndBrowserSessionDeletesItAndReportsNotFound(t *testing.T) {
	db, _ := openTemp(t)
	h, p := seedHousehold(t, db)
	mustSession(t, db, "tok", p["Papa"].ID, "android_phone|chrome", sessionT0, sessionT0.Add(time.Hour))
	list, _ := db.ListBrowserSessions(h.ID, sessionT0)
	id := list[0].ID
	if err := db.EndBrowserSession(h.ID, id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SessionPerson("tok", sessionT0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("session still valid after it was ended: %v", err)
	}
	if err := db.EndBrowserSession(h.ID, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ending twice = %v, want not found", err)
	}
	if err := db.EndBrowserSession(h.ID, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown id = %v, want not found", err)
	}
}

func TestSessionIDOfTheCookiesToken(t *testing.T) {
	db, _ := openTemp(t)
	h, p := seedHousehold(t, db)
	mustSession(t, db, "tok", p["Mama"].ID, "mac|safari", sessionT0, sessionT0.Add(time.Hour))
	list, _ := db.ListBrowserSessions(h.ID, sessionT0)
	if id, err := db.SessionID("tok"); err != nil || id != list[0].ID {
		t.Fatalf("SessionID = %q, %v; want %q", id, err, list[0].ID)
	}
	if _, err := db.SessionID("other"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown token = %v", err)
	}
}

// Last used moves at most every five minutes, so a busy browser does not
// write to the database on every request.
func TestTouchSessionIsThrottledToFiveMinutes(t *testing.T) {
	db, _ := openTemp(t)
	h, p := seedHousehold(t, db)
	mustSession(t, db, "tok", p["Mama"].ID, "mac|safari", sessionT0, sessionT0.Add(24*time.Hour))
	lastSeen := func() time.Time {
		l, err := db.ListBrowserSessions(h.ID, sessionT0)
		if err != nil || len(l) != 1 {
			t.Fatalf("list = %+v, %v", l, err)
		}
		return l[0].LastSeen
	}
	db.TouchSession("tok", sessionT0.Add(4*time.Minute))
	if got := lastSeen(); !got.Equal(sessionT0) {
		t.Errorf("after 4 minutes last seen = %v, want unchanged %v", got, sessionT0)
	}
	db.TouchSession("tok", sessionT0.Add(6*time.Minute))
	if got := lastSeen(); !got.Equal(sessionT0.Add(6 * time.Minute)) {
		t.Errorf("after 6 minutes last seen = %v", got)
	}
	db.TouchSession("tok", sessionT0.Add(8*time.Minute))
	if got := lastSeen(); !got.Equal(sessionT0.Add(6 * time.Minute)) {
		t.Errorf("2 minutes later last seen = %v, want unchanged", got)
	}
}
