package store_test

import (
	"path/filepath"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

// ADR 0018: auto-update is on by default, and offers off and notify only.
func TestAutoUpdateDefaultsToOnAndPersists(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	got, _ := db.Household(h.ID)
	if got.AutoUpdate != store.UpdateOn {
		t.Fatalf("default = %q, want on", got.AutoUpdate)
	}
	for _, m := range []store.UpdateMode{store.UpdateNotify, store.UpdateOff, store.UpdateOn} {
		if err := db.SetAutoUpdate(h.ID, m); err != nil {
			t.Fatal(err)
		}
		if got, _ := db.Household(h.ID); got.AutoUpdate != m {
			t.Fatalf("saved = %q, want %q", got.AutoUpdate, m)
		}
	}
	if err := db.SetAutoUpdate(h.ID, "sometimes"); err == nil {
		t.Fatal("unknown mode accepted")
	}
	if err := db.SetAutoUpdate("missing", store.UpdateOn); err == nil {
		t.Fatal("unknown household accepted")
	}
}

// After an update the web interface shows what is new once: the household
// remembers which update event parents have seen.
func TestUpdateNoticeAcknowledgementPersists(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	if got, _ := db.Household(h.ID); got.UpdateAck != "" {
		t.Fatalf("fresh ack = %q", got.UpdateAck)
	}
	if err := db.AckUpdate(h.ID, "2026-11-16T03:30:12Z"); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.Household(h.ID); got.UpdateAck != "2026-11-16T03:30:12Z" {
		t.Fatalf("ack = %q", got.UpdateAck)
	}
}

// SQLite online backup: the copy has everything committed, also what still
// sits in the write-ahead log, and the box keeps running meanwhile.
func TestBackupIsAConsistentCopyOfALiveDatabase(t *testing.T) {
	db, _ := openTemp(t)
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	dest := filepath.Join(t.TempDir(), "backup.db")
	if err := db.Backup(dest); err != nil {
		t.Fatal(err)
	}
	// The live database stays usable and later writes do not reach the copy.
	if _, err := db.CreateHousehold(store.Household{Name: "Later"}); err != nil {
		t.Fatal(err)
	}
	cp, err := store.Open(dest)
	if err != nil {
		t.Fatalf("backup does not open: %v", err)
	}
	defer cp.Close()
	got, err := cp.HomeHousehold()
	if err != nil || got.ID != h.ID {
		t.Fatalf("backup lacks the household: %+v %v", got, err)
	}
	var n int
	_ = cp.SQL().QueryRow(`SELECT COUNT(*) FROM households`).Scan(&n)
	if n != 1 {
		t.Fatalf("backup has %d households, want 1", n)
	}
}

func TestBackupRefusesToOverwrite(t *testing.T) {
	db, _ := openTemp(t)
	dest := filepath.Join(t.TempDir(), "backup.db")
	if err := db.Backup(dest); err != nil {
		t.Fatal(err)
	}
	if err := db.Backup(dest); err == nil {
		t.Fatal("second backup overwrote the first")
	}
}
