package store_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 221: an uploaded update file waits for a parent's yes, then installs.
var uploadT0 = time.Date(2026, 11, 23, 21, 0, 0, 0, time.UTC)

func upload() store.UpdateUpload {
	return store.UpdateUpload{
		Version: "0.2.4", FromVersion: "0.2.3", Arch: "arm64", FileName: "hejmo-0.2.4-arm64.update",
		Size: 61 << 20, SHA256: "abcd", Released: "2026-11-20", Signer: "https://github.com/hejmo-home/hejmo/.github/workflows/release.yml@refs/tags/v0.2.4",
		UploadedAt: uploadT0,
	}
}

func TestThereIsNoUploadOnAFreshBox(t *testing.T) {
	db, _ := openTemp(t)
	if _, ok, err := db.UpdateUpload(); ok || err != nil {
		t.Fatalf("upload = %v, %v", ok, err)
	}
}

func TestAnUploadIsKeptUntilItIsStartedAndThenCleared(t *testing.T) {
	db, _ := openTemp(t)
	u := upload()
	if err := db.SaveUpdateUpload(u); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.UpdateUpload()
	if err != nil || !ok {
		t.Fatalf("got %v, %v", ok, err)
	}
	if got.State != store.UploadReady || got.Version != "0.2.4" || got.FromVersion != "0.2.3" || got.Size != 61<<20 || !got.UploadedAt.Equal(uploadT0) || got.Signer != u.Signer || got.FileName != u.FileName {
		t.Errorf("got %+v", got)
	}
	started, err := db.StartUpdateUpload("Papa", uploadT0.Add(5*time.Minute))
	if err != nil || !started {
		t.Fatalf("start = %v, %v", started, err)
	}
	// One installation at a time: a second yes changes nothing.
	if again, err := db.StartUpdateUpload("Mama", uploadT0.Add(6*time.Minute)); err != nil || again {
		t.Fatalf("second start = %v, %v", again, err)
	}
	got, _, _ = db.UpdateUpload()
	if got.State != store.UploadInstalling || got.StartedBy != "Papa" || !got.StartedAt.Equal(uploadT0.Add(5*time.Minute)) {
		t.Errorf("got %+v", got)
	}
	// A new file cannot replace one that is being installed.
	if err := db.SaveUpdateUpload(u); err == nil {
		t.Error("an upload replaced a running installation")
	}
	if err := db.ClearUpdateUpload(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := db.UpdateUpload(); ok {
		t.Error("still there after clearing")
	}
	if started, _ := db.StartUpdateUpload("Papa", uploadT0); started {
		t.Error("started without a file")
	}
}

func TestANewUploadReplacesOneThatWaits(t *testing.T) {
	db, _ := openTemp(t)
	u := upload()
	_ = db.SaveUpdateUpload(u)
	u.Version = "0.2.5"
	if err := db.SaveUpdateUpload(u); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := db.UpdateUpload(); got.Version != "0.2.5" || got.State != store.UploadReady {
		t.Errorf("got %+v", got)
	}
}

func TestTheReleaseDateOfAnInstalledUploadIsRemembered(t *testing.T) {
	db, _ := openTemp(t)
	if box, _ := db.OutboundBox(); box.InstalledVersion != "" || box.InstalledReleased != "" {
		t.Fatalf("box = %+v", box)
	}
	if err := db.SetInstalledRelease("0.2.4", "2026-11-20"); err != nil {
		t.Fatal(err)
	}
	if box, _ := db.OutboundBox(); box.InstalledVersion != "0.2.4" || box.InstalledReleased != "2026-11-20" {
		t.Errorf("box = %+v", box)
	}
}
