package update_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/update"
)

// ADR 0018: box and host script exchange a small file with fixed keys and values.
func TestParseStatusReadsTheFixedKeys(t *testing.T) {
	got := update.ParseStatus([]byte("state=rolled_back\nversion=0.1.0\ntarget=0.1.1\nreason=health_check_failed\ntime=2026-11-16T03:30:12Z\n"))
	want := update.Status{State: update.RolledBack, Version: "0.1.0", Target: "0.1.1", Reason: "health_check_failed", Time: "2026-11-16T03:30:12Z"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParseStatusAcceptsNothingElse(t *testing.T) {
	cases := map[string]string{
		"unknown state":       "state=exploded\nversion=0.1.0\n",
		"unknown reason":      "state=failed\nreason=<script>alert(1)</script>\n",
		"markup in version":   "state=updated\nversion=0.1.0<b>\n",
		"pre-release version": "state=updated\nversion=0.1.0-rc.1\n",
		"bad time":            "state=updated\nversion=0.1.0\ntime=yesterday\n",
		"unknown key":         "state=updated\nversion=0.1.0\nrun=rm -rf /\n",
		"no equals":           "state=updated\nversion\n",
		"shell syntax":        "state=updated\nversion=$(id)\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got := update.ParseStatus([]byte(in))
			if got.Version != "" && got.Version != "0.1.0" {
				t.Errorf("accepted version %q", got.Version)
			}
			if got.Reason != "" || got.Target != "" || got.Time != "" {
				t.Errorf("accepted %+v", got)
			}
			if name == "unknown state" && got.State != "" {
				t.Errorf("accepted state %q", got.State)
			}
		})
	}
}

func TestReadStatusHandlesMissingOversizedAndLinkedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, update.StatusFile)
	if got := update.ReadStatus(dir); got != (update.Status{}) {
		t.Fatalf("missing file: %+v", got)
	}
	if err := os.WriteFile(path, []byte("state=updated\nversion=0.2.0\ntime=2026-11-16T03:30:12Z\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := update.ReadStatus(dir); got.State != update.Updated || got.Version != "0.2.0" {
		t.Fatalf("got %+v", got)
	}
	big := "state=updated\n" + strings.Repeat("x", 1<<20)
	if err := os.WriteFile(path, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := update.ReadStatus(dir); got != (update.Status{}) {
		t.Fatalf("oversized file accepted: %+v", got)
	}
}

func TestWriteSettingsWritesOneFixedLine(t *testing.T) {
	dir := t.TempDir()
	for _, m := range []string{"on", "off", "notify"} {
		if err := update.WriteSettings(dir, m); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dir, update.SettingsFile))
		if err != nil || string(data) != "mode="+m+"\n" {
			t.Fatalf("file = %q, %v", data, err)
		}
	}
	if err := update.WriteSettings(dir, "on\nmode=off"); err == nil {
		t.Fatal("a mode outside the fixed values was written")
	}
	if err := update.WriteSettings("", "on"); err != nil {
		t.Fatalf("no directory (updates not managed) must be a no-op, got %v", err)
	}
}

// Ticket 201: offline mode reaches the host script as one more fixed key, and
// the script says when it last looked for a new version.
func TestWriteStateAddsTheOfflineKeyOnlyWhenOffline(t *testing.T) {
	dir := t.TempDir()
	read := func() string {
		data, err := os.ReadFile(filepath.Join(dir, update.SettingsFile))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if err := update.WriteState(dir, "on", true); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "mode=on\noffline=yes\n" {
		t.Errorf("offline: %q", got)
	}
	if err := update.WriteState(dir, "notify", false); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "mode=notify\n" {
		t.Errorf("online: %q", got)
	}
	if err := update.WriteState(dir, "sometimes", true); err == nil {
		t.Error("an unknown mode was written")
	}
	if err := update.WriteState("", "on", true); err != nil {
		t.Errorf("no directory means no host script: %v", err)
	}
}

func TestParseStatusReadsWhenTheScriptLastLooked(t *testing.T) {
	got := update.ParseStatus([]byte("state=idle\nchecked=2026-11-17T03:31:22Z\n"))
	if got.Checked != "2026-11-17T03:31:22Z" {
		t.Errorf("checked = %q", got.Checked)
	}
	if got := update.ParseStatus([]byte("checked=soon\n")); got.Checked != "" {
		t.Errorf("accepted %q", got.Checked)
	}
}
