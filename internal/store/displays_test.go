package store_test

import (
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

var sixDigits = regexp.MustCompile(`^[0-9]{6}$`)

var pairT0 = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

func householdForDisplays(t *testing.T) (*store.DB, store.Household) {
	t.Helper()
	db, _ := openTemp(t)
	h, err := db.CreateHousehold(store.Household{Name: "Family", HomeName: "Pasing", Region: "DE-BY"})
	if err != nil {
		t.Fatal(err)
	}
	return db, h
}

func TestNewDisplayWaitsWithASixDigitCode(t *testing.T) {
	db, h := householdForDisplays(t)
	d, err := db.OpenPairing(h.ID, "hash-1", pairT0)
	if err != nil {
		t.Fatal(err)
	}
	if d.Paired || !sixDigits.MatchString(d.Code) {
		t.Fatalf("pending display = %+v, want unpaired with a six-digit code", d)
	}
	if want := pairT0.Add(store.PairingCodeLifetime); !d.CodeExpires.Equal(want) {
		t.Fatalf("code expires %v, want %v", d.CodeExpires, want)
	}
	got, err := db.DisplayByToken("hash-1", pairT0)
	if err != nil || got.Paired || got.Code != d.Code {
		t.Fatalf("DisplayByToken = %+v, %v", got, err)
	}
	if list, _ := db.Displays(h.ID); len(list) != 0 {
		t.Fatalf("unpaired display listed: %+v", list)
	}
}

func TestConfirmedCodePairsTheDisplayOnce(t *testing.T) {
	db, h := householdForDisplays(t)
	d, _ := db.OpenPairing(h.ID, "hash-1", pairT0)

	paired, err := db.ConfirmPairing(h.ID, d.Code, store.Display{Name: "Hallway", Room: "Entrance", Type: store.DisplayEInkBW}, pairT0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !paired.Paired || paired.Name != "Hallway" || paired.Room != "Entrance" || paired.Type != store.DisplayEInkBW {
		t.Fatalf("paired = %+v", paired)
	}
	got, err := db.DisplayByToken("hash-1", pairT0.Add(time.Minute))
	if err != nil || !got.Paired || got.Name != "Hallway" || got.Code != "" {
		t.Fatalf("DisplayByToken = %+v, %v", got, err)
	}
	list, _ := db.Displays(h.ID)
	if len(list) != 1 || list[0].ID != paired.ID {
		t.Fatalf("Displays = %+v", list)
	}
	if _, err := db.ConfirmPairing(h.ID, d.Code, store.Display{Name: "Again", Type: store.DisplayScreen}, pairT0.Add(time.Minute)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second confirmation = %v, want ErrNotFound", err)
	}
}

func TestWrongOrExpiredCodeIsRefused(t *testing.T) {
	db, h := householdForDisplays(t)
	d, _ := db.OpenPairing(h.ID, "hash-1", pairT0)
	wrong := "000000"
	if d.Code == wrong {
		wrong = "000001"
	}
	in := store.Display{Name: "Hallway", Type: store.DisplayScreen}
	if _, err := db.ConfirmPairing(h.ID, wrong, in, pairT0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("wrong code = %v, want ErrNotFound", err)
	}
	if _, err := db.ConfirmPairing(h.ID, d.Code, in, pairT0.Add(store.PairingCodeLifetime+time.Second)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired code = %v, want ErrNotFound", err)
	}
	if got, _ := db.DisplayByToken("hash-1", pairT0); got.Paired {
		t.Fatal("display paired by a refused code")
	}
}

func TestConfirmingNeedsANameAndAKnownType(t *testing.T) {
	db, h := householdForDisplays(t)
	d, _ := db.OpenPairing(h.ID, "hash-1", pairT0)
	for _, in := range []store.Display{{Name: " ", Type: store.DisplayScreen}, {Name: "Hallway", Type: "hologram"}} {
		if _, err := db.ConfirmPairing(h.ID, d.Code, in, pairT0); !errors.Is(err, store.ErrInvalid) {
			t.Fatalf("ConfirmPairing(%+v) = %v, want ErrInvalid", in, err)
		}
	}
	if _, err := db.ConfirmPairing(h.ID, d.Code, store.Display{Name: "Hallway", Type: store.DisplayScreen}, pairT0); err != nil {
		t.Fatalf("code unusable after invalid input: %v", err)
	}
}

func TestExpiredCodeIsReplacedWhileTheDisplayWaits(t *testing.T) {
	db, h := householdForDisplays(t)
	first, _ := db.OpenPairing(h.ID, "hash-1", pairT0)
	later := pairT0.Add(store.PairingCodeLifetime + time.Minute)

	got, err := db.DisplayByToken("hash-1", later)
	if err != nil || got.Paired || got.Code != "" {
		t.Fatalf("after expiry = %+v, %v; want unpaired without a code", got, err)
	}
	renewed, err := db.RenewPairingCode("hash-1", later)
	if err != nil || !sixDigits.MatchString(renewed.Code) || !renewed.CodeExpires.After(later) {
		t.Fatalf("RenewPairingCode = %+v, %v", renewed, err)
	}
	if _, err := db.ConfirmPairing(h.ID, first.Code, store.Display{Name: "X", Type: store.DisplayScreen}, later); first.Code != renewed.Code && !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("old code still works: %v", err)
	}
}

func TestCodesOfWaitingDisplaysDiffer(t *testing.T) {
	db, h := householdForDisplays(t)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		d, err := db.OpenPairing(h.ID, string(rune('a'+i%26))+string(rune('A'+i/26)), pairT0)
		if err != nil {
			t.Fatal(err)
		}
		if seen[d.Code] {
			t.Fatalf("code %s given to two waiting displays", d.Code)
		}
		seen[d.Code] = true
	}
}

func TestRevokedDisplayLosesItsToken(t *testing.T) {
	db, h := householdForDisplays(t)
	d, _ := db.OpenPairing(h.ID, "hash-1", pairT0)
	paired, _ := db.ConfirmPairing(h.ID, d.Code, store.Display{Name: "Hallway", Type: store.DisplayEInkColor}, pairT0)

	if err := db.RevokeDisplay(h.ID, paired.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DisplayByToken("hash-1", pairT0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked token = %v, want ErrNotFound", err)
	}
	if list, _ := db.Displays(h.ID); len(list) != 0 {
		t.Fatalf("revoked display listed: %+v", list)
	}
	if err := db.RevokeDisplay(h.ID, paired.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second revoke = %v, want ErrNotFound", err)
	}
}

func TestDisplayUseIsRecorded(t *testing.T) {
	db, h := householdForDisplays(t)
	d, _ := db.OpenPairing(h.ID, "hash-1", pairT0)
	db.ConfirmPairing(h.ID, d.Code, store.Display{Name: "Hallway", Type: store.DisplayScreen}, pairT0)
	seen := pairT0.Add(time.Hour)
	if err := db.TouchDisplay("hash-1", seen); err != nil {
		t.Fatal(err)
	}
	list, _ := db.Displays(h.ID)
	if len(list) != 1 || !list[0].LastSeen.Equal(seen) {
		t.Fatalf("Displays = %+v, want last seen %v", list, seen)
	}
}
