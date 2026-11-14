package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

func seedParent(t *testing.T, db *store.DB) (store.Household, store.Person) {
	t.Helper()
	h, err := db.CreateHousehold(store.Household{Name: "Family"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := db.AddPerson(h.ID, store.Person{Name: "Mama", Role: store.RoleParent})
	if err != nil {
		t.Fatal(err)
	}
	return h, p
}

func TestConfiguredOnlyAfterTOTPConfirmation(t *testing.T) {
	db, _ := openTemp(t)
	if db.Configured() {
		t.Fatal("fresh box reports configured")
	}
	_, mama := seedParent(t, db)
	if err := db.CreateAccount(store.Account{PersonID: mama.ID, Username: "mama", PasswordHash: "h", TOTPSecret: "S"}); err != nil {
		t.Fatal(err)
	}
	if db.Configured() {
		t.Fatal("configured before TOTP was confirmed")
	}
	if err := db.ConfirmTOTP(mama.ID); err != nil {
		t.Fatal(err)
	}
	if !db.Configured() {
		t.Fatal("not configured after TOTP confirmation")
	}
}

func TestAccountByUsernameIgnoresCase(t *testing.T) {
	db, _ := openTemp(t)
	_, mama := seedParent(t, db)
	_ = db.CreateAccount(store.Account{PersonID: mama.ID, Username: "Mama", PasswordHash: "h", TOTPSecret: "S"})
	acc, err := db.AccountByUsername("mAMA")
	if err != nil || acc.PersonID != mama.ID {
		t.Fatalf("AccountByUsername = %+v, %v", acc, err)
	}
	if _, err := db.AccountByUsername("papa"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
	if err := db.CreateAccount(store.Account{PersonID: mama.ID, Username: "MAMA", PasswordHash: "h", TOTPSecret: "S"}); err == nil {
		t.Fatal("duplicate username accepted")
	}
}

func TestSessions(t *testing.T) {
	db, _ := openTemp(t)
	_, mama := seedParent(t, db)
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	if err := db.CreateSession("hash1", mama.ID, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if id, err := db.SessionPerson("hash1", at); err != nil || id != mama.ID {
		t.Fatalf("SessionPerson = %q, %v", id, err)
	}
	if _, err := db.SessionPerson("hash1", at.Add(2*time.Hour)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired session: %v", err)
	}
	_ = db.DeleteSession("hash1")
	if _, err := db.SessionPerson("hash1", at); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted session: %v", err)
	}
}

func TestAPITokens(t *testing.T) {
	db, _ := openTemp(t)
	_, mama := seedParent(t, db)
	tok, err := db.CreateAPIToken(store.APIToken{Name: "Assistant", Hash: "th", PersonID: mama.ID, Scopes: "read"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.APITokenByHash("th")
	if err != nil || got.PersonID != mama.ID || got.Scopes != "read" || got.ID != tok.ID {
		t.Fatalf("APITokenByHash = %+v, %v", got, err)
	}
	if err := db.RevokeAPIToken(tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.APITokenByHash("th"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked token: %v", err)
	}
}

func TestDisplayPIN(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := seedParent(t, db)
	if err := db.SetDisplayPIN(h.ID, "pinhash"); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.DisplayPINHash(h.ID); got != "pinhash" {
		t.Fatalf("DisplayPINHash = %q", got)
	}
}

func TestResetUnfinishedSetup(t *testing.T) {
	db, _ := openTemp(t)
	_, mama := seedParent(t, db)
	_ = db.CreateAccount(store.Account{PersonID: mama.ID, Username: "mama", PasswordHash: "h", TOTPSecret: "S"})
	if err := db.ResetUnfinishedSetup(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.HomeHousehold(); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("household survived reset")
	}

	_, papa := seedParent(t, db)
	_ = db.CreateAccount(store.Account{PersonID: papa.ID, Username: "papa", PasswordHash: "h", TOTPSecret: "S"})
	_ = db.ConfirmTOTP(papa.ID)
	if err := db.ResetUnfinishedSetup(); err == nil {
		t.Fatal("reset allowed on a configured box")
	}
}
