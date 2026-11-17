package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

var phoneT0 = time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)

func householdWithMia(t *testing.T) (*store.DB, store.Household, store.Person) {
	t.Helper()
	db, h := householdForDisplays(t)
	mia, err := db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	if err != nil {
		t.Fatal(err)
	}
	return db, h, mia
}

// pairPhone runs the whole pairing and returns the phone's session hash.
func pairPhone(t *testing.T, db *store.DB, h store.Household, person store.Person, name string) store.Phone {
	t.Helper()
	pr, err := db.CreatePhonePairing(h.ID, "code-"+name, phoneT0)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.JoinPhonePairing("code-"+name, "claim-"+name, "Safari on iPhone", phoneT0); err != nil {
		t.Fatal(err)
	}
	confirmed, err := db.ConfirmPhonePairing(h.ID, pr.ID, person.ID, name, phoneT0)
	if err != nil {
		t.Fatal(err)
	}
	ph, err := db.CompletePhonePairing("code-"+name, "claim-"+name, "session-"+name, phoneT0, phoneT0.Add(store.PhoneSessionLifetime))
	if err != nil {
		t.Fatal(err)
	}
	if ph.ID != confirmed.ID {
		t.Fatalf("phone id %s, confirmed as %s", ph.ID, confirmed.ID)
	}
	return ph
}

func TestConfirmedPairingGivesThePhoneADeviceSessionForThePerson(t *testing.T) {
	db, h, mia := householdWithMia(t)
	ph := pairPhone(t, db, h, mia, "Mia's phone")
	if ph.PersonID != mia.ID || ph.Name != "Mia's phone" {
		t.Fatalf("phone = %+v", ph)
	}
	id, err := db.SessionPerson("session-Mia's phone", phoneT0.Add(24*time.Hour))
	if err != nil || id != mia.ID {
		t.Fatalf("session person = %q, %v", id, err)
	}
	list, err := db.Phones(h.ID)
	if err != nil || len(list) != 1 || list[0].ID != ph.ID {
		t.Fatalf("phones = %+v, %v", list, err)
	}
}

func TestPairingWaitsForTheParentsConfirmation(t *testing.T) {
	db, h, mia := householdWithMia(t)
	pr, _ := db.CreatePhonePairing(h.ID, "c", phoneT0)
	if _, err := db.CompletePhonePairing("c", "x", "s", phoneT0, phoneT0.Add(time.Hour)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("complete before open = %v, want ErrNotFound", err)
	}
	if err := db.JoinPhonePairing("c", "x", "Chrome on Android", phoneT0); err != nil {
		t.Fatal(err)
	}
	pending, _ := db.PendingPhonePairings(h.ID, phoneT0)
	if len(pending) != 1 || pending[0].ID != pr.ID || pending[0].Device != "Chrome on Android" {
		t.Fatalf("pending = %+v", pending)
	}
	if _, err := db.CompletePhonePairing("c", "x", "s", phoneT0, phoneT0.Add(time.Hour)); !errors.Is(err, store.ErrPhonePending) {
		t.Fatalf("complete before confirm = %v, want ErrPhonePending", err)
	}
	if _, err := db.ConfirmPhonePairing(h.ID, pr.ID, mia.ID, "Mia", phoneT0); err != nil {
		t.Fatal(err)
	}
	if pending, _ = db.PendingPhonePairings(h.ID, phoneT0); len(pending) != 0 {
		t.Fatalf("confirmed pairing still pending: %+v", pending)
	}
}

func TestAPairingCodeWorksForOnePhoneOnly(t *testing.T) {
	db, h, _ := householdWithMia(t)
	_, _ = db.CreatePhonePairing(h.ID, "c", phoneT0)
	if err := db.JoinPhonePairing("c", "first", "d", phoneT0); err != nil {
		t.Fatal(err)
	}
	if err := db.JoinPhonePairing("c", "first", "d", phoneT0); err != nil {
		t.Fatalf("same phone again = %v, want ok", err)
	}
	if err := db.JoinPhonePairing("c", "second", "d", phoneT0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second phone = %v, want ErrNotFound", err)
	}
}

func TestExpiredAndUsedPairingCodesAreRefused(t *testing.T) {
	db, h, mia := householdWithMia(t)
	_, _ = db.CreatePhonePairing(h.ID, "old", phoneT0)
	late := phoneT0.Add(store.PairingCodeLifetime + time.Second)
	if err := db.JoinPhonePairing("old", "x", "d", late); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired = %v, want ErrNotFound", err)
	}
	if err := db.JoinPhonePairing("nope", "x", "d", phoneT0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown = %v, want ErrNotFound", err)
	}
	pairPhone(t, db, h, mia, "A")
	if err := db.JoinPhonePairing("code-A", "claim-A", "d", phoneT0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("used = %v, want ErrNotFound", err)
	}
	if _, err := db.CompletePhonePairing("code-A", "claim-A", "again", phoneT0, phoneT0.Add(time.Hour)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("complete twice = %v, want ErrNotFound", err)
	}
}

func TestConfirmedPairingExpiresIfThePhoneNeverCollectsIt(t *testing.T) {
	db, h, mia := householdWithMia(t)
	pr, _ := db.CreatePhonePairing(h.ID, "c", phoneT0)
	_ = db.JoinPhonePairing("c", "x", "d", phoneT0)
	_, _ = db.ConfirmPhonePairing(h.ID, pr.ID, mia.ID, "Mia", phoneT0)
	late := phoneT0.Add(store.PairingCodeLifetime + time.Second)
	if _, err := db.CompletePhonePairing("c", "x", "s", late, late.Add(time.Hour)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("late complete = %v, want ErrNotFound", err)
	}
	if _, err := db.ConfirmPhonePairing(h.ID, pr.ID, mia.ID, "Mia", late); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("late confirm = %v, want ErrNotFound", err)
	}
}

func TestConfirmingNeedsAHouseholdMemberAndAName(t *testing.T) {
	db, h, mia := householdWithMia(t)
	pr, _ := db.CreatePhonePairing(h.ID, "c", phoneT0)
	_ = db.JoinPhonePairing("c", "x", "d", phoneT0)
	var fe *store.FieldError
	if _, err := db.ConfirmPhonePairing(h.ID, pr.ID, mia.ID, "  ", phoneT0); !errors.As(err, &fe) || fe.Field != "name" {
		t.Fatalf("blank name = %v, want name field error", err)
	}
	if _, err := db.ConfirmPhonePairing(h.ID, pr.ID, "stranger", "Phone", phoneT0); !errors.As(err, &fe) || fe.Field != "person" {
		t.Fatalf("unknown person = %v, want person field error", err)
	}
	other, _ := db.CreateHousehold(store.Household{Name: "Other"})
	if _, err := db.ConfirmPhonePairing(other.ID, pr.ID, mia.ID, "Phone", phoneT0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("other household = %v, want ErrNotFound", err)
	}
}

func TestRevokingAPhoneEndsItsSessionOnly(t *testing.T) {
	db, h, mia := householdWithMia(t)
	a := pairPhone(t, db, h, mia, "A")
	pairPhone(t, db, h, mia, "B")
	if err := db.RevokePhone(h.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SessionPerson("session-A", phoneT0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked session = %v, want ErrNotFound", err)
	}
	if _, err := db.SessionPerson("session-B", phoneT0); err != nil {
		t.Fatalf("other phone's session = %v", err)
	}
	if err := db.RevokePhone(h.ID, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second revoke = %v, want ErrNotFound", err)
	}
	if list, _ := db.Phones(h.ID); len(list) != 1 || list[0].Name != "B" {
		t.Fatalf("phones = %+v", list)
	}
}

func TestPhoneLastSeenIsRecordedOncePerMinute(t *testing.T) {
	db, h, mia := householdWithMia(t)
	ph := pairPhone(t, db, h, mia, "A")
	if !ph.LastSeen.Equal(phoneT0) {
		t.Fatalf("last seen at pairing = %v", ph.LastSeen)
	}
	db.TouchPhoneSession("session-A", phoneT0.Add(10*time.Second))
	db.TouchPhoneSession("session-A", phoneT0.Add(2*time.Minute))
	list, _ := db.Phones(h.ID)
	if !list[0].LastSeen.Equal(phoneT0.Add(2 * time.Minute)) {
		t.Fatalf("last seen = %v", list[0].LastSeen)
	}
}

func TestCreatingPairingsKeepsOnlyRecentOnes(t *testing.T) {
	db, h, _ := householdWithMia(t)
	for i := range store.MaxPhonePairings + 5 {
		if _, err := db.CreatePhonePairing(h.ID, "c"+string(rune('a'+i)), phoneT0.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.JoinPhonePairing("ca", "x", "d", phoneT0.Add(time.Minute)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("oldest pairing survived: %v", err)
	}
}

func TestDeletingTheSessionOfAPhoneRemovesThePhone(t *testing.T) {
	db, h, mia := householdWithMia(t)
	pairPhone(t, db, h, mia, "A")
	if err := db.DeleteSession("session-A"); err != nil {
		t.Fatal(err)
	}
	if list, _ := db.Phones(h.ID); len(list) != 0 {
		t.Fatalf("phones after sign-out = %+v", list)
	}
}

// #198: re-pairing ends the phone and its device session at once and leaves a
// pairing code that says whom the phone is for next.
func TestRepairingAPhoneEndsItsSessionAndLeavesACodeForTheNextPerson(t *testing.T) {
	db, h, mia := householdWithMia(t)
	sebastian, _ := db.AddPerson(h.ID, store.Person{Name: "Sebastian", Role: store.RoleChild})
	ph := pairPhone(t, db, h, sebastian, "Sebastian's phone")
	later := phoneT0.Add(time.Hour)

	pr, err := db.RepairPhone(h.ID, ph.ID, mia.ID, "repair-code", later)
	if err != nil {
		t.Fatal(err)
	}
	if pr.ForPersonID != mia.ID || !pr.ExpiresAt.Equal(later.Add(store.PairingCodeLifetime)) {
		t.Fatalf("pairing = %+v, want for Mia, expiring after the usual lifetime", pr)
	}
	if _, err := db.SessionPerson("session-Sebastian's phone", later); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("old session after re-pairing = %v, want ErrNotFound", err)
	}
	if list, _ := db.Phones(h.ID); len(list) != 0 {
		t.Errorf("phones after re-pairing = %+v, want none", list)
	}
	// The new code works like any other: the phone opens it, a parent confirms.
	if err := db.JoinPhonePairing("repair-code", "claim-new", "Safari on iPhone", later); err != nil {
		t.Fatal(err)
	}
	if for_, ok, err := db.PairingForPerson("repair-code", "claim-new", later); err != nil || !ok || for_.ID != mia.ID || for_.Name != "Mia" {
		t.Fatalf("pairing for = %+v %v %v, want Mia", for_, ok, err)
	}
	waiting, _ := db.PendingPhonePairings(h.ID, later)
	if len(waiting) != 1 || waiting[0].ForPersonID != mia.ID {
		t.Fatalf("waiting = %+v, want one for Mia", waiting)
	}
}

func TestRepairingNeedsAnotherPersonOfTheHousehold(t *testing.T) {
	db, h, mia := householdWithMia(t)
	ph := pairPhone(t, db, h, mia, "Mia's phone")
	var fe *store.FieldError
	if _, err := db.RepairPhone(h.ID, ph.ID, mia.ID, "c1", phoneT0); !errors.As(err, &fe) || fe.Field != "person" {
		t.Errorf("same person = %v, want a person field error", err)
	}
	if _, err := db.RepairPhone(h.ID, ph.ID, "", "c2", phoneT0); !errors.As(err, &fe) || fe.Field != "person" {
		t.Errorf("no person = %v, want a person field error", err)
	}
	if _, err := db.RepairPhone(h.ID, ph.ID, "nobody", "c3", phoneT0); !errors.As(err, &fe) || fe.Field != "person" {
		t.Errorf("unknown person = %v, want a person field error", err)
	}
	if list, _ := db.Phones(h.ID); len(list) != 1 {
		t.Fatalf("a refused re-pairing removed the phone: %+v", list)
	}
	if _, err := db.RepairPhone(h.ID, "unknown", mia.ID, "c4", phoneT0); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown phone = %v, want ErrNotFound", err)
	}
}
