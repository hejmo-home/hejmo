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
	if err := db.CreateSession("hash1", mama.ID, "", at, at.Add(time.Hour)); err != nil {
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

func confirmedParent(t *testing.T, db *store.DB) store.Person {
	t.Helper()
	_, mama := seedParent(t, db)
	if err := db.CreateAccount(store.Account{PersonID: mama.ID, Username: "mama", PasswordHash: "h", TOTPSecret: "S"}); err != nil {
		t.Fatal(err)
	}
	if err := db.ConfirmTOTP(mama.ID); err != nil {
		t.Fatal(err)
	}
	return mama
}

func TestRecoveryCodesAreSpentOnceAndANewSetVoidsTheOld(t *testing.T) {
	db, _ := openTemp(t)
	mama := confirmedParent(t, db)
	if err := db.ReplaceRecoveryCodes(mama.ID, []string{"h1", "h2", "h3"}); err != nil {
		t.Fatal(err)
	}
	codes, err := db.UnusedRecoveryCodes(mama.ID)
	if err != nil || len(codes) != 3 {
		t.Fatalf("unused = %v, %v", codes, err)
	}
	if err := db.SpendRecoveryCode(codes[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := db.SpendRecoveryCode(codes[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("spending twice: %v, want ErrNotFound", err)
	}
	if left, _ := db.UnusedRecoveryCodes(mama.ID); len(left) != 2 {
		t.Fatalf("%d codes left, want 2", len(left))
	}
	if err := db.ReplaceRecoveryCodes(mama.ID, []string{"n1"}); err != nil {
		t.Fatal(err)
	}
	left, _ := db.UnusedRecoveryCodes(mama.ID)
	if len(left) != 1 || left[0].Hash != "n1" {
		t.Fatalf("after a new set: %v", left)
	}
}

func TestResetOTPRemovesSecretCodesAndBrowserSessionsButKeepsTokensAndTheBoxConfigured(t *testing.T) {
	db, _ := openTemp(t)
	mama := confirmedParent(t, db)
	_ = db.ReplaceRecoveryCodes(mama.ID, []string{"h1"})
	if err := db.CreateSession("tok", mama.ID, "", time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAPIToken(store.APIToken{Name: "personal_device", Hash: "x", PersonID: mama.ID, Scopes: "calendars"}); err != nil {
		t.Fatal(err)
	}
	h, err := db.HomeHousehold()
	if err != nil {
		t.Fatal(err)
	}
	pairPersonalDevice(t, db, h, mama, "Mama's tablet")
	if err := db.ResetOTP(mama.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SessionPerson("session-Mama's tablet", personalDeviceT0.Add(time.Hour)); err != nil {
		t.Fatalf("personal device device session ended by the reset: %v", err)
	}
	acc, _ := db.AccountByPerson(mama.ID)
	if acc.TOTPConfirmed || acc.TOTPSecret != "" {
		t.Fatalf("account after reset = %+v", acc)
	}
	if codes, _ := db.UnusedRecoveryCodes(mama.ID); len(codes) != 0 {
		t.Fatal("recovery codes survived the reset")
	}
	if _, err := db.SessionPerson("tok", time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("browser session survived the reset")
	}
	if _, err := db.APITokenByHash("x"); err != nil {
		t.Fatalf("device token lost: %v", err)
	}
	if !db.Configured() {
		t.Fatal("the box counts as unconfigured after the only parent was reset")
	}
	if err := db.SetTOTPSecret(mama.ID, "NEW"); err != nil {
		t.Fatal(err)
	}
	if err := db.ConfirmTOTP(mama.ID); err != nil {
		t.Fatal(err)
	}
	if acc, _ := db.AccountByPerson(mama.ID); !acc.TOTPConfirmed || acc.TOTPSecret != "NEW" {
		t.Fatalf("after re-setup = %+v", acc)
	}
}

func TestAccountLanguage(t *testing.T) {
	db, _ := openTemp(t)
	_, mama := seedParent(t, db)
	_ = db.CreateAccount(store.Account{PersonID: mama.ID, Username: "mama", PasswordHash: "h", TOTPSecret: "S"})
	acc, err := db.AccountByPerson(mama.ID)
	if err != nil || acc.Language != "" {
		t.Fatalf("new account language = %q, %v; want none", acc.Language, err)
	}
	if err := db.SetAccountLanguage(mama.ID, "en"); err != nil {
		t.Fatal(err)
	}
	for _, get := range []func() (store.Account, error){
		func() (store.Account, error) { return db.AccountByPerson(mama.ID) },
		func() (store.Account, error) { return db.AccountByUsername("mama") },
	} {
		if acc, err := get(); err != nil || acc.Language != "en" {
			t.Fatalf("language = %q, %v; want en", acc.Language, err)
		}
	}
	if err := db.SetAccountLanguage("nobody", "en"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown person: %v", err)
	}
}

// Ticket 176: a new set of recovery codes is "not confirmed as saved" until the
// person says so; a newer set starts unconfirmed again.
func TestRecoveryCodesAreUnconfirmedUntilConfirmedAsSaved(t *testing.T) {
	db, _ := openTemp(t)
	mama := confirmedParent(t, db)
	if set, err := db.RecoverySet(mama.ID); err != nil || set.Exists {
		t.Fatalf("before any set: %+v, %v", set, err)
	}
	if err := db.ReplaceRecoveryCodes(mama.ID, []string{"h1", "h2"}); err != nil {
		t.Fatal(err)
	}
	set, err := db.RecoverySet(mama.ID)
	if err != nil || !set.Exists || set.Unused != 2 || set.CreatedAt.IsZero() || !set.SavedAt.IsZero() {
		t.Fatalf("new set: %+v, %v", set, err)
	}
	if err := db.ConfirmRecoveryCodesSaved(mama.ID); err != nil {
		t.Fatal(err)
	}
	if set, _ := db.RecoverySet(mama.ID); set.SavedAt.IsZero() {
		t.Fatal("not saved after the confirmation")
	}
	if err := db.ReplaceRecoveryCodes(mama.ID, []string{"n1"}); err != nil {
		t.Fatal(err)
	}
	if set, _ := db.RecoverySet(mama.ID); !set.SavedAt.IsZero() {
		t.Fatal("a newer set counts as saved")
	}
	if err := db.ConfirmRecoveryCodesSaved("nobody"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("confirming for nobody: %v", err)
	}
}
