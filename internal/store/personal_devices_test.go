package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

var personalDeviceT0 = time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)

func householdWithMia(t *testing.T) (*store.DB, store.Household, store.Person) {
	t.Helper()
	db, h := householdForDisplays(t)
	mia, err := db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	if err != nil {
		t.Fatal(err)
	}
	return db, h, mia
}

// pairPersonalDevice runs the whole pairing and returns the personal device's session hash.
func pairPersonalDevice(t *testing.T, db *store.DB, h store.Household, person store.Person, name string) store.PersonalDevice {
	t.Helper()
	pr, err := db.CreatePersonalDevicePairing(h.ID, "code-"+name, personalDeviceT0)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.JoinPersonalDevicePairing("code-"+name, "claim-"+name, "safari|iphone", "phone", personalDeviceT0); err != nil {
		t.Fatal(err)
	}
	confirmed, err := db.ConfirmPersonalDevicePairing(h.ID, pr.ID, person.ID, name, personalDeviceT0)
	if err != nil {
		t.Fatal(err)
	}
	ph, err := db.CompletePersonalDevicePairing("code-"+name, "claim-"+name, "session-"+name, personalDeviceT0, personalDeviceT0.Add(store.PersonalDeviceSessionLifetime))
	if err != nil {
		t.Fatal(err)
	}
	if ph.ID != confirmed.ID {
		t.Fatalf("personal device id %s, confirmed as %s", ph.ID, confirmed.ID)
	}
	return ph
}

func TestConfirmedPairingGivesThePersonalDeviceADeviceSessionForThePerson(t *testing.T) {
	db, h, mia := householdWithMia(t)
	ph := pairPersonalDevice(t, db, h, mia, "Mia's tablet")
	if ph.PersonID != mia.ID || ph.Name != "Mia's tablet" {
		t.Fatalf("personal device = %+v", ph)
	}
	id, err := db.SessionPerson("session-Mia's tablet", personalDeviceT0.Add(24*time.Hour))
	if err != nil || id != mia.ID {
		t.Fatalf("session person = %q, %v", id, err)
	}
	list, err := db.PersonalDevices(h.ID)
	if err != nil || len(list) != 1 || list[0].ID != ph.ID {
		t.Fatalf("personal devices = %+v, %v", list, err)
	}
}

func TestPairingWaitsForTheParentsConfirmation(t *testing.T) {
	db, h, mia := householdWithMia(t)
	pr, _ := db.CreatePersonalDevicePairing(h.ID, "c", personalDeviceT0)
	if _, err := db.CompletePersonalDevicePairing("c", "x", "s", personalDeviceT0, personalDeviceT0.Add(time.Hour)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("complete before open = %v, want ErrNotFound", err)
	}
	if err := db.JoinPersonalDevicePairing("c", "x", "chrome|android_tablet", "tablet", personalDeviceT0); err != nil {
		t.Fatal(err)
	}
	pending, _ := db.PendingPersonalDevicePairings(h.ID, personalDeviceT0)
	if len(pending) != 1 || pending[0].ID != pr.ID || pending[0].Device != "chrome|android_tablet" || pending[0].Kind != "tablet" {
		t.Fatalf("pending = %+v", pending)
	}
	if _, err := db.CompletePersonalDevicePairing("c", "x", "s", personalDeviceT0, personalDeviceT0.Add(time.Hour)); !errors.Is(err, store.ErrPersonalDevicePending) {
		t.Fatalf("complete before confirm = %v, want ErrPersonalDevicePending", err)
	}
	if _, err := db.ConfirmPersonalDevicePairing(h.ID, pr.ID, mia.ID, "Mia", personalDeviceT0); err != nil {
		t.Fatal(err)
	}
	if pending, _ = db.PendingPersonalDevicePairings(h.ID, personalDeviceT0); len(pending) != 0 {
		t.Fatalf("confirmed pairing still pending: %+v", pending)
	}
}

func TestAPairingCodeWorksForOnePersonalDeviceOnly(t *testing.T) {
	db, h, _ := householdWithMia(t)
	_, _ = db.CreatePersonalDevicePairing(h.ID, "c", personalDeviceT0)
	if err := db.JoinPersonalDevicePairing("c", "first", "d", "device", personalDeviceT0); err != nil {
		t.Fatal(err)
	}
	if err := db.JoinPersonalDevicePairing("c", "first", "d", "device", personalDeviceT0); err != nil {
		t.Fatalf("same personal device again = %v, want ok", err)
	}
	if err := db.JoinPersonalDevicePairing("c", "second", "d", "device", personalDeviceT0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second personal device = %v, want ErrNotFound", err)
	}
}

func TestExpiredAndUsedPairingCodesAreRefused(t *testing.T) {
	db, h, mia := householdWithMia(t)
	_, _ = db.CreatePersonalDevicePairing(h.ID, "old", personalDeviceT0)
	late := personalDeviceT0.Add(store.PairingCodeLifetime + time.Second)
	if err := db.JoinPersonalDevicePairing("old", "x", "d", "device", late); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired = %v, want ErrNotFound", err)
	}
	if err := db.JoinPersonalDevicePairing("nope", "x", "d", "device", personalDeviceT0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown = %v, want ErrNotFound", err)
	}
	pairPersonalDevice(t, db, h, mia, "A")
	if err := db.JoinPersonalDevicePairing("code-A", "claim-A", "d", "device", personalDeviceT0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("used = %v, want ErrNotFound", err)
	}
	if _, err := db.CompletePersonalDevicePairing("code-A", "claim-A", "again", personalDeviceT0, personalDeviceT0.Add(time.Hour)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("complete twice = %v, want ErrNotFound", err)
	}
}

func TestConfirmedPairingExpiresIfThePersonalDeviceNeverCollectsIt(t *testing.T) {
	db, h, mia := householdWithMia(t)
	pr, _ := db.CreatePersonalDevicePairing(h.ID, "c", personalDeviceT0)
	_ = db.JoinPersonalDevicePairing("c", "x", "d", "device", personalDeviceT0)
	_, _ = db.ConfirmPersonalDevicePairing(h.ID, pr.ID, mia.ID, "Mia", personalDeviceT0)
	late := personalDeviceT0.Add(store.PairingCodeLifetime + time.Second)
	if _, err := db.CompletePersonalDevicePairing("c", "x", "s", late, late.Add(time.Hour)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("late complete = %v, want ErrNotFound", err)
	}
	if _, err := db.ConfirmPersonalDevicePairing(h.ID, pr.ID, mia.ID, "Mia", late); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("late confirm = %v, want ErrNotFound", err)
	}
}

func TestConfirmingNeedsAHouseholdMemberAndAName(t *testing.T) {
	db, h, mia := householdWithMia(t)
	pr, _ := db.CreatePersonalDevicePairing(h.ID, "c", personalDeviceT0)
	_ = db.JoinPersonalDevicePairing("c", "x", "d", "device", personalDeviceT0)
	var fe *store.FieldError
	if _, err := db.ConfirmPersonalDevicePairing(h.ID, pr.ID, mia.ID, "  ", personalDeviceT0); !errors.As(err, &fe) || fe.Field != "name" {
		t.Fatalf("blank name = %v, want name field error", err)
	}
	if _, err := db.ConfirmPersonalDevicePairing(h.ID, pr.ID, "stranger", "Personal device", personalDeviceT0); !errors.As(err, &fe) || fe.Field != "person" {
		t.Fatalf("unknown person = %v, want person field error", err)
	}
	other, _ := db.CreateHousehold(store.Household{Name: "Other"})
	if _, err := db.ConfirmPersonalDevicePairing(other.ID, pr.ID, mia.ID, "Personal device", personalDeviceT0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("other household = %v, want ErrNotFound", err)
	}
}

func TestRevokingAPersonalDeviceEndsItsSessionOnly(t *testing.T) {
	db, h, mia := householdWithMia(t)
	a := pairPersonalDevice(t, db, h, mia, "A")
	pairPersonalDevice(t, db, h, mia, "B")
	if err := db.RevokePersonalDevice(h.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SessionPerson("session-A", personalDeviceT0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked session = %v, want ErrNotFound", err)
	}
	if _, err := db.SessionPerson("session-B", personalDeviceT0); err != nil {
		t.Fatalf("other personal device's session = %v", err)
	}
	if err := db.RevokePersonalDevice(h.ID, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second revoke = %v, want ErrNotFound", err)
	}
	if list, _ := db.PersonalDevices(h.ID); len(list) != 1 || list[0].Name != "B" {
		t.Fatalf("personal devices = %+v", list)
	}
}

func TestPersonalDeviceLastSeenIsRecordedOncePerMinute(t *testing.T) {
	db, h, mia := householdWithMia(t)
	ph := pairPersonalDevice(t, db, h, mia, "A")
	if !ph.LastSeen.Equal(personalDeviceT0) {
		t.Fatalf("last seen at pairing = %v", ph.LastSeen)
	}
	db.TouchPersonalDeviceSession("session-A", personalDeviceT0.Add(10*time.Second))
	db.TouchPersonalDeviceSession("session-A", personalDeviceT0.Add(2*time.Minute))
	list, _ := db.PersonalDevices(h.ID)
	if !list[0].LastSeen.Equal(personalDeviceT0.Add(2 * time.Minute)) {
		t.Fatalf("last seen = %v", list[0].LastSeen)
	}
}

func TestCreatingPairingsKeepsOnlyRecentOnes(t *testing.T) {
	db, h, _ := householdWithMia(t)
	for i := range store.MaxPersonalDevicePairings + 5 {
		if _, err := db.CreatePersonalDevicePairing(h.ID, "c"+string(rune('a'+i)), personalDeviceT0.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.JoinPersonalDevicePairing("ca", "x", "d", "device", personalDeviceT0.Add(time.Minute)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("oldest pairing survived: %v", err)
	}
}

func TestDeletingTheSessionOfAPersonalDeviceRemovesThePersonalDevice(t *testing.T) {
	db, h, mia := householdWithMia(t)
	pairPersonalDevice(t, db, h, mia, "A")
	if err := db.DeleteSession("session-A"); err != nil {
		t.Fatal(err)
	}
	if list, _ := db.PersonalDevices(h.ID); len(list) != 0 {
		t.Fatalf("personal devices after sign-out = %+v", list)
	}
}

// #198: re-pairing ends the personal device and its device session at once and leaves a
// pairing code that says whom the personal device is for next.
func TestRepairingAPersonalDeviceEndsItsSessionAndLeavesACodeForTheNextPerson(t *testing.T) {
	db, h, mia := householdWithMia(t)
	sebastian, _ := db.AddPerson(h.ID, store.Person{Name: "Sebastian", Role: store.RoleChild})
	ph := pairPersonalDevice(t, db, h, sebastian, "Sebastian's personal device")
	later := personalDeviceT0.Add(time.Hour)

	pr, err := db.RepairPersonalDevice(h.ID, ph.ID, mia.ID, "repair-code", later)
	if err != nil {
		t.Fatal(err)
	}
	if pr.ForPersonID != mia.ID || !pr.ExpiresAt.Equal(later.Add(store.PairingCodeLifetime)) {
		t.Fatalf("pairing = %+v, want for Mia, expiring after the usual lifetime", pr)
	}
	if _, err := db.SessionPerson("session-Sebastian's personal device", later); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("old session after re-pairing = %v, want ErrNotFound", err)
	}
	if list, _ := db.PersonalDevices(h.ID); len(list) != 0 {
		t.Errorf("personal devices after re-pairing = %+v, want none", list)
	}
	// The new code works like any other: the personal device opens it, a parent confirms.
	if err := db.JoinPersonalDevicePairing("repair-code", "claim-new", "safari|iphone", "phone", later); err != nil {
		t.Fatal(err)
	}
	if for_, ok, err := db.PairingForPerson("repair-code", "claim-new", later); err != nil || !ok || for_.ID != mia.ID || for_.Name != "Mia" {
		t.Fatalf("pairing for = %+v %v %v, want Mia", for_, ok, err)
	}
	waiting, _ := db.PendingPersonalDevicePairings(h.ID, later)
	if len(waiting) != 1 || waiting[0].ForPersonID != mia.ID {
		t.Fatalf("waiting = %+v, want one for Mia", waiting)
	}
}

func TestRepairingNeedsAnotherPersonOfTheHousehold(t *testing.T) {
	db, h, mia := householdWithMia(t)
	ph := pairPersonalDevice(t, db, h, mia, "Mia's tablet")
	var fe *store.FieldError
	if _, err := db.RepairPersonalDevice(h.ID, ph.ID, mia.ID, "c1", personalDeviceT0); !errors.As(err, &fe) || fe.Field != "person" {
		t.Errorf("same person = %v, want a person field error", err)
	}
	if _, err := db.RepairPersonalDevice(h.ID, ph.ID, "", "c2", personalDeviceT0); !errors.As(err, &fe) || fe.Field != "person" {
		t.Errorf("no person = %v, want a person field error", err)
	}
	if _, err := db.RepairPersonalDevice(h.ID, ph.ID, "nobody", "c3", personalDeviceT0); !errors.As(err, &fe) || fe.Field != "person" {
		t.Errorf("unknown person = %v, want a person field error", err)
	}
	if list, _ := db.PersonalDevices(h.ID); len(list) != 1 {
		t.Fatalf("a refused re-pairing removed the personal device: %+v", list)
	}
	if _, err := db.RepairPersonalDevice(h.ID, "unknown", mia.ID, "c4", personalDeviceT0); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown personal device = %v, want ErrNotFound", err)
	}
}

// #243: the kind of device is stored with the pairing, next to the device keys; nothing reads
// it back from a label. An unknown kind is stored as "device".
func TestPairingStoresTheKindOfDeviceItsOwn(t *testing.T) {
	db, h, _ := householdWithMia(t)
	for _, tc := range []struct{ kind, want string }{{"phone", "phone"}, {"tablet", "tablet"}, {"watch", "device"}, {"", "device"}} {
		if _, err := db.CreatePersonalDevicePairing(h.ID, "c-"+tc.kind, personalDeviceT0); err != nil {
			t.Fatal(err)
		}
		if err := db.JoinPersonalDevicePairing("c-"+tc.kind, "x-"+tc.kind, "chrome|ipad", tc.kind, personalDeviceT0); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := db.PendingPersonalDevicePairings(h.ID, personalDeviceT0)
	if err != nil || len(pending) != 4 {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	for i, want := range []string{"phone", "tablet", "device", "device"} {
		if pending[i].Kind != want {
			t.Errorf("pairing %d kind = %q, want %q", i, pending[i].Kind, want)
		}
	}
}
