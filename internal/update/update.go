// Package update is the box's side of auto-update (ADR 0018). The host script
// that installs releases and the box exchange two small files in the data
// volume, nothing else: the box writes the setting, the script writes the
// status. Both have fixed keys and fixed values; anything else is ignored and
// never interpreted.
package update

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// File names in the data volume.
const (
	SettingsFile = "update-settings" // written by the box: mode=on|off|notify, and offline=yes while the script must stay offline
	StatusFile   = "update-status"   // written by the host script
)

// State is what the host script last did.
type State string

// States the script reports.
const (
	Installing State = "installing"  // an uploaded update file is being installed (Step says where; Target is the version)
	Idle       State = "idle"        // checked, nothing newer
	Available  State = "available"   // notify only: a newer version exists (Target)
	Updated    State = "updated"     // installed Version
	RolledBack State = "rolled_back" // Target failed; Version runs again with its database
	Rejected   State = "rejected"    // Target was not installed: its signature did not verify
	Failed     State = "failed"      // the update could not be done; Version still runs
)

var states = map[State]bool{Installing: true, Idle: true, Available: true, Updated: true, RolledBack: true, Rejected: true, Failed: true}

// Reasons are the fixed causes of a rollback, rejection or failure; the web
// interface has a translated text for each.
var Reasons = []string{"start_failed", "health_check_failed", "signature_invalid", "backup_failed", "pull_failed", "rollback_failed", "box_not_running", "file_invalid", "load_failed", "verifier_missing"}

// Status is the script's report. Every field is empty if absent or invalid.
type Status struct {
	State   State
	Version string // the version that runs now
	Target  string // the version offered or attempted
	Reason  string
	// Step is where an installation of an uploaded file is: one of Steps.
	Step string
	// Backup is when the script finished saving the data before an installation (RFC 3339 UTC).
	Backup string
	Time   string // RFC 3339 UTC; identifies the event, so a notice is shown once
	// Checked is when the script last looked for a new version (RFC 3339 UTC);
	// it is the last contact of the update check (ticket 201).
	Checked string
}

// Steps are the stages of installing an uploaded file, in order: the host
// script checks the signature again, saves the data, starts the new version and
// waits until it answers.
var Steps = []string{"verify", "backup", "start", "health"}

func validStep(v string) bool {
	for _, x := range Steps {
		if v == x {
			return true
		}
	}
	return false
}

var (
	versionRe = regexp.MustCompile(`^[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4}$`)
	maxSize   = 4096
)

func validReason(r string) bool {
	for _, v := range Reasons {
		if r == v {
			return true
		}
	}
	return false
}

// ParseStatus reads the status file's content, accepting only the fixed keys
// with valid values.
func ParseStatus(data []byte) Status {
	var s Status
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		k, v, ok := bytes.Cut(sc.Bytes(), []byte("="))
		if !ok {
			continue
		}
		val := string(v)
		switch string(k) {
		case "state":
			if states[State(val)] {
				s.State = State(val)
			}
		case "version":
			if versionRe.MatchString(val) {
				s.Version = val
			}
		case "target":
			if versionRe.MatchString(val) {
				s.Target = val
			}
		case "reason":
			if validReason(val) {
				s.Reason = val
			}
		case "backup":
			if _, err := time.Parse(time.RFC3339, val); err == nil && len(val) <= 25 {
				s.Backup = val
			}
		case "step":
			if validStep(val) {
				s.Step = val
			}
		case "time":
			if _, err := time.Parse(time.RFC3339, val); err == nil && len(val) <= 25 {
				s.Time = val
			}
		case "checked":
			if _, err := time.Parse(time.RFC3339, val); err == nil && len(val) <= 25 {
				s.Checked = val
			}
		}
	}
	return s
}

// ReadStatus reads the status file in dir. A missing, oversized or
// non-regular file (a link, say) gives the zero Status.
func ReadStatus(dir string) Status {
	if dir == "" {
		return Status{}
	}
	path := filepath.Join(dir, StatusFile)
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > int64(maxSize) {
		return Status{}
	}
	f, err := os.Open(path)
	if err != nil {
		return Status{}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(maxSize)))
	if err != nil {
		return Status{}
	}
	return ParseStatus(data)
}

// Install is a request to the host script to install the update file the box
// keeps at PendingFile: the version it holds, the SHA-256 of the file and when
// the parent said yes. The script checks the file again on its own.
type Install struct {
	Version string // x.y.z
	SHA256  string // hex, of the whole file
	Time    time.Time
}

// PendingFile is the name of the uploaded update file in the data volume.
const PendingFile = "update-file"

// Settings is what the box tells the host script.
type Settings struct {
	Mode    string // on, off or notify
	Offline bool   // the script must contact nothing (offline mode, or the update check is off)
	Install *Install
}

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// WriteSettings tells the host script the household's mode (on, off or
// notify). An empty dir means updates are not managed by a host script and
// does nothing.
func WriteSettings(dir, mode string) error { return Write(dir, Settings{Mode: mode}) }

// WriteState is WriteSettings that also says whether the script must stay
// offline (offline mode, or the update check switched off): then the file has a
// second line, offline=yes, and the script contacts nothing. Online the line
// is absent, so the file is the one of earlier versions.
func WriteState(dir, mode string, offline bool) error {
	return Write(dir, Settings{Mode: mode, Offline: offline})
}

// Write replaces the settings file. Every value is checked against its fixed
// shape first; nothing else is ever written. With an install request the file
// has three more lines (install, install_sha256, install_time).
func Write(dir string, st Settings) error {
	if dir == "" {
		return nil
	}
	switch st.Mode {
	case "on", "off", "notify":
	default:
		return fmt.Errorf("update: unknown mode %q", st.Mode)
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "mode=%s\n", st.Mode)
	if st.Offline {
		b.WriteString("offline=yes\n")
	}
	if in := st.Install; in != nil {
		if !versionRe.MatchString(in.Version) || !sha256Re.MatchString(in.SHA256) || in.Time.IsZero() {
			return errors.New("update: bad install request")
		}
		fmt.Fprintf(&b, "install=%s\ninstall_sha256=%s\ninstall_time=%s\n", in.Version, in.SHA256, in.Time.UTC().Format(time.RFC3339))
	}
	tmp, err := os.CreateTemp(dir, ".update-settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, SettingsFile))
}
