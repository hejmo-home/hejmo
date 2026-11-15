package store_test

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/quiet"
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
	for _, in := range []store.Display{{Name: " ", Type: store.DisplayScreen}, {Name: strings.Repeat("x", 81), Type: store.DisplayScreen}, {Name: "Hallway", Room: strings.Repeat("x", 81), Type: store.DisplayScreen}, {Name: "Hallway", Type: "hologram"}} {
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

	if err := db.RevokeDisplay(h.ID, paired.ID, pairT0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DisplayByToken("hash-1", pairT0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked token = %v, want ErrNotFound", err)
	}
	if list, _ := db.Displays(h.ID); len(list) != 0 {
		t.Fatalf("revoked display listed: %+v", list)
	}
	if err := db.RevokeDisplay(h.ID, paired.ID, pairT0); !errors.Is(err, store.ErrNotFound) {
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

func TestWaitingDisplaysAreCappedAndTheNewestWins(t *testing.T) {
	db, h := householdForDisplays(t)
	const opened = store.MaxWaitingDisplays + 15
	var codes []string
	for i := 0; i < opened; i++ {
		d, err := db.OpenPairing(h.ID, fmt.Sprintf("hash-%d", i), pairT0.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		codes = append(codes, d.Code)
	}
	at := pairT0.Add(time.Hour)
	alive := 0
	for i := 0; i < opened; i++ {
		if _, err := db.DisplayByToken(fmt.Sprintf("hash-%d", i), at); err == nil {
			alive++
		}
	}
	if alive != store.MaxWaitingDisplays {
		t.Fatalf("%d waiting displays kept, want %d", alive, store.MaxWaitingDisplays)
	}
	if _, err := db.DisplayByToken("hash-0", at); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("oldest waiting display kept: %v", err)
	}
	if _, err := db.ConfirmPairing(h.ID, codes[opened-1], store.Display{Name: "X", Type: store.DisplayScreen}, pairT0.Add(time.Minute)); err != nil {
		t.Fatalf("newest display cannot be paired: %v", err)
	}
}

func TestPairedDisplaysDoNotCountAgainstTheWaitingCap(t *testing.T) {
	db, h := householdForDisplays(t)
	for i := 0; i < store.MaxWaitingDisplays+3; i++ {
		d, _ := db.OpenPairing(h.ID, fmt.Sprintf("p-%d", i), pairT0)
		if _, err := db.ConfirmPairing(h.ID, d.Code, store.Display{Name: "D", Type: store.DisplayScreen}, pairT0); err != nil {
			t.Fatal(err)
		}
	}
	if list, _ := db.Displays(h.ID); len(list) != store.MaxWaitingDisplays+3 {
		t.Fatalf("%d paired displays, want %d", len(list), store.MaxWaitingDisplays+3)
	}
}

// ADR 0016: "show tomorrow block" is a setting per display, off by default.
func TestShowTomorrowBlockIsAPerDisplaySettingOffByDefault(t *testing.T) {
	db, h := householdForDisplays(t)
	pair := func(hash string) store.Display {
		t.Helper()
		w, err := db.OpenPairing(h.ID, hash, pairT0)
		if err != nil {
			t.Fatal(err)
		}
		d, err := db.ConfirmPairing(h.ID, w.Code, store.Display{Name: "Hall", Type: store.DisplayScreen}, pairT0)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	a, b := pair("hash-a"), pair("hash-b")
	if a.ShowTomorrow || b.ShowTomorrow {
		t.Fatal("tomorrow block on by default")
	}
	if err := db.SetDisplayShowTomorrow(h.ID, a.ID, true); err != nil {
		t.Fatal(err)
	}
	gotA, _ := db.DisplayByToken("hash-a", pairT0)
	gotB, _ := db.DisplayByToken("hash-b", pairT0)
	if !gotA.ShowTomorrow || gotB.ShowTomorrow {
		t.Fatalf("setting a=%v b=%v, want true/false", gotA.ShowTomorrow, gotB.ShowTomorrow)
	}
	list, _ := db.Displays(h.ID)
	if len(list) != 2 || !list[0].ShowTomorrow || list[1].ShowTomorrow {
		t.Fatalf("Displays = %+v", list)
	}
	if err := db.SetDisplayShowTomorrow(h.ID, a.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.DisplayByToken("hash-a", pairT0); got.ShowTomorrow {
		t.Fatal("setting not switched off")
	}
	other, _ := db.CreateHousehold(store.Household{Name: "Neighbours"})
	if err := db.SetDisplayShowTomorrow(other.ID, a.ID, true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("other household sets it: %v", err)
	}
	if err := db.SetDisplayShowTomorrow(h.ID, "nope", true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown display: %v", err)
	}
}

func pairedDisplay(t *testing.T) (*store.DB, store.Household, store.Display) {
	t.Helper()
	db, _ := openSealed(t)
	h, err := db.CreateHousehold(store.Household{Name: "Family", HomeName: "Pasing", Region: "DE-BY"})
	if err != nil {
		t.Fatal(err)
	}
	w, _ := db.OpenPairing(h.ID, "hash-q", pairT0)
	d, err := db.ConfirmPairing(h.ID, w.Code, store.Display{Name: "Hallway", Type: store.DisplayEInkBW}, pairT0)
	if err != nil {
		t.Fatal(err)
	}
	return db, h, d
}

// ADR 0015: quiet mode is a per-display setting, off by default, suggesting 22:00 to 06:00.
func TestDisplayQuietModeIsOffByDefaultAndSavedPerDisplay(t *testing.T) {
	db, h, d := pairedDisplay(t)
	if d.Quiet.Enabled || d.Quiet.FromText() != "22:00" || d.Quiet.ToText() != "06:00" {
		t.Fatalf("default = %+v", d.Quiet)
	}
	s, _ := quiet.Parse(true, "21:30", "06:45")
	if err := db.SetDisplayQuiet(h.ID, d.ID, s); err != nil {
		t.Fatal(err)
	}
	list, _ := db.Displays(h.ID)
	if got := list[0].Quiet; got != s {
		t.Fatalf("quiet = %+v, want %+v", got, s)
	}
	if err := db.SetDisplayQuiet(h.ID, "nope", s); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown display = %v", err)
	}
}

func TestDisplayKioskAddressIsStoredAndTheKeySealed(t *testing.T) {
	db, h, d := pairedDisplay(t)
	key := "secret-api-key"
	if err := db.SetDisplayKiosk(h.ID, d.ID, "http://192.168.1.20:8080", &key); err != nil {
		t.Fatal(err)
	}
	list, _ := db.Displays(h.ID)
	if list[0].KioskURL != "http://192.168.1.20:8080" || !list[0].KioskKeySet {
		t.Fatalf("display = %+v", list[0])
	}
	got, err := db.DisplayKioskKey(h.ID, d.ID)
	if err != nil || got != key {
		t.Fatalf("key = %q, %v", got, err)
	}
	var raw string
	if err := db.SQL().QueryRow(`SELECT kiosk_key FROM displays WHERE id = ?`, d.ID).Scan(&raw); err != nil || raw == "" || strings.Contains(raw, key) {
		t.Fatalf("stored key = %q, %v: must be sealed", raw, err)
	}
	// Without a new key the old one stays; an empty key clears it.
	if err := db.SetDisplayKiosk(h.ID, d.ID, "http://192.168.1.21:8080", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.DisplayKioskKey(h.ID, d.ID); got != key {
		t.Fatalf("key after keeping = %q", got)
	}
	empty := ""
	_ = db.SetDisplayKiosk(h.ID, d.ID, "http://192.168.1.21:8080", &empty)
	if got, _ := db.DisplayKioskKey(h.ID, d.ID); got != "" {
		t.Fatalf("key after clearing = %q", got)
	}
	// An empty address removes the kiosk, key included.
	_ = db.SetDisplayKiosk(h.ID, d.ID, "http://x:1", &key)
	_ = db.SetDisplayKiosk(h.ID, d.ID, "", nil)
	list, _ = db.Displays(h.ID)
	if list[0].KioskURL != "" || list[0].KioskKeySet {
		t.Fatalf("kiosk not removed: %+v", list[0])
	}
}

func TestConfirmingNamesTheWrongField(t *testing.T) {
	db, h := householdForDisplays(t)
	d, _ := db.OpenPairing(h.ID, "hash-1", pairT0)
	for _, c := range []struct {
		in    store.Display
		field string
	}{
		{store.Display{Name: " ", Type: store.DisplayScreen}, "name"},
		{store.Display{Name: strings.Repeat("x", 81), Type: store.DisplayScreen}, "name"},
		{store.Display{Name: "Hallway", Room: strings.Repeat("x", 81), Type: store.DisplayScreen}, "room"},
		{store.Display{Name: "Hallway"}, "type"},
		{store.Display{Name: "Hallway", Type: "hologram"}, "type"},
	} {
		_, err := db.ConfirmPairing(h.ID, d.Code, c.in, pairT0)
		var fe *store.FieldError
		if !errors.As(err, &fe) || fe.Field != c.field || !errors.Is(err, store.ErrInvalid) {
			t.Errorf("ConfirmPairing(%+v) = %v, want a FieldError for %q", c.in, err, c.field)
		}
	}
}
