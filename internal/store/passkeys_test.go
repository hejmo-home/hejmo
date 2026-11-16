package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

var passkeyT0 = time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)

func addPasskey(t *testing.T, db *store.DB, personID, name, rp string, cred byte) store.Passkey {
	t.Helper()
	pk, err := db.AddPasskey(store.Passkey{PersonID: personID, CredentialID: []byte{cred}, Credential: []byte(`{"n":1}`), Name: name, RPID: rp, Device: "mac", Created: passkeyT0})
	if err != nil {
		t.Fatal(err)
	}
	return pk
}

// 7.6: a passkey belongs to one person and is found by its credential ID.
func TestPasskeysBelongToAPersonAndAreFoundByCredential(t *testing.T) {
	db, _ := openTemp(t)
	mama := confirmedParent(t, db)
	a := addPasskey(t, db, mama.ID, "Mac", "hejmo.local", 1)
	addPasskey(t, db, mama.ID, "iPhone", "hejmo.local", 2)
	list, err := db.Passkeys(mama.ID)
	if err != nil || len(list) != 2 || list[0].Name != "Mac" || list[1].Name != "iPhone" {
		t.Fatalf("Passkeys = %+v, %v", list, err)
	}
	if list[0].ID == "" || list[0].RPID != "hejmo.local" || !list[0].LastUsed.IsZero() || !list[0].Created.Equal(passkeyT0) {
		t.Fatalf("passkey = %+v", list[0])
	}
	got, err := db.PasskeyByCredential([]byte{1})
	if err != nil || got.ID != a.ID || got.PersonID != mama.ID {
		t.Fatalf("PasskeyByCredential = %+v, %v", got, err)
	}
	if _, err := db.PasskeyByCredential([]byte{9}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown credential: %v", err)
	}
	if _, err := db.AddPasskey(store.Passkey{PersonID: mama.ID, CredentialID: []byte{1}, Credential: []byte(`{}`), Name: "again", RPID: "hejmo.local", Created: passkeyT0}); err == nil {
		t.Fatal("the same credential was stored twice")
	}
}

// 7.6: using a passkey records when and keeps its updated counters.
func TestUsingAPasskeyRecordsItAndUpdatesTheCredential(t *testing.T) {
	db, _ := openTemp(t)
	mama := confirmedParent(t, db)
	pk := addPasskey(t, db, mama.ID, "Mac", "hejmo.local", 1)
	at := passkeyT0.Add(3 * time.Hour)
	if err := db.UsePasskey(pk.ID, []byte(`{"n":2}`), at); err != nil {
		t.Fatal(err)
	}
	got, _ := db.Passkey(mama.ID, pk.ID)
	if !got.LastUsed.Equal(at) || string(got.Credential) != `{"n":2}` {
		t.Fatalf("after use = %+v", got)
	}
}

// 7.6: renaming and removing work for the owner only; a removed passkey is gone.
func TestRenamingAndRemovingAPasskeyIsForItsOwnerOnly(t *testing.T) {
	db, _ := openTemp(t)
	mama := confirmedParent(t, db)
	h, _ := db.HomeHousehold()
	papa, _ := db.AddPerson(h.ID, store.Person{Name: "Papa", Role: store.RoleParent})
	pk := addPasskey(t, db, mama.ID, "Mac", "hejmo.local", 1)
	if err := db.RenamePasskey(papa.ID, pk.ID, "Mine now"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("another person renamed it: %v", err)
	}
	if err := db.RenamePasskey(mama.ID, pk.ID, "Mac im Arbeitszimmer"); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.Passkey(mama.ID, pk.ID); got.Name != "Mac im Arbeitszimmer" {
		t.Fatalf("name = %q", got.Name)
	}
	if err := db.RemovePasskey(papa.ID, pk.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("another person removed it: %v", err)
	}
	if err := db.RemovePasskey(mama.ID, pk.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PasskeyByCredential([]byte{1}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a removed passkey is still found: %v", err)
	}
}

// 7.6: a session remembers the passkey it was made with.
func TestASessionRemembersItsPasskey(t *testing.T) {
	db, _ := openTemp(t)
	mama := confirmedParent(t, db)
	pk := addPasskey(t, db, mama.ID, "Mac", "hejmo.local", 1)
	if err := db.CreateSession("tok", mama.ID, "mac|safari", passkeyT0, passkeyT0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if id, _ := db.SessionPasskey("tok"); id != "" {
		t.Fatalf("a password session has passkey %q", id)
	}
	if err := db.SetSessionPasskey("tok", pk.ID); err != nil {
		t.Fatal(err)
	}
	if id, _ := db.SessionPasskey("tok"); id != pk.ID {
		t.Fatalf("session passkey = %q", id)
	}
}

// 7.6: a reset can remove passkeys in the same step and remembers who reset.
func TestResetByAParentRemovesChosenPasskeysAndRemembersWho(t *testing.T) {
	db, _ := openTemp(t)
	mama := confirmedParent(t, db)
	h, _ := db.HomeHousehold()
	papa, _ := db.AddPerson(h.ID, store.Person{Name: "Papa", Role: store.RoleParent})
	if err := db.CreateAccount(store.Account{PersonID: papa.ID, Username: "papa", PasswordHash: "h", TOTPSecret: "S"}); err != nil {
		t.Fatal(err)
	}
	_ = db.ConfirmTOTP(papa.ID)
	lost := addPasskey(t, db, papa.ID, "iPhone", "hejmo.local", 1)
	keep := addPasskey(t, db, papa.ID, "Mac im Arbeitszimmer", "hejmo.local", 2)
	mamas := addPasskey(t, db, mama.ID, "Mac", "hejmo.local", 3)
	removed, err := db.ResetOTPBy(papa.ID, mama.ID, []string{lost.ID, mamas.ID, "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0].Name != "iPhone" {
		t.Fatalf("removed = %+v, want only Papa's iPhone (never Mama's passkey, never an unknown one)", removed)
	}
	if left, _ := db.Passkeys(papa.ID); len(left) != 1 || left[0].ID != keep.ID {
		t.Fatalf("Papa's passkeys after = %+v", left)
	}
	if left, _ := db.Passkeys(mama.ID); len(left) != 1 {
		t.Fatal("the reset removed a passkey of the parent who confirmed it")
	}
	acc, _ := db.AccountByPerson(papa.ID)
	if !acc.TOTPReset || acc.TOTPResetBy != mama.ID || acc.TOTPResetAt.IsZero() {
		t.Fatalf("account = %+v", acc)
	}
}
