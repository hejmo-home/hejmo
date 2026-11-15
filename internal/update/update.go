// Package update is the box's side of auto-update (ADR 0018). The host script
// that installs releases and the box exchange two small files in the data
// volume, nothing else: the box writes the setting, the script writes the
// status. Both have fixed keys and fixed values; anything else is ignored and
// never interpreted.
package update

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// File names in the data volume.
const (
	SettingsFile = "update-settings" // written by the box: mode=on|off|notify
	StatusFile   = "update-status"   // written by the host script
)

// State is what the host script last did.
type State string

// States the script reports.
const (
	Idle       State = "idle"        // checked, nothing newer
	Available  State = "available"   // notify only: a newer version exists (Target)
	Updated    State = "updated"     // installed Version
	RolledBack State = "rolled_back" // Target failed; Version runs again with its database
	Rejected   State = "rejected"    // Target was not installed: its signature did not verify
	Failed     State = "failed"      // the update could not be done; Version still runs
)

var states = map[State]bool{Idle: true, Available: true, Updated: true, RolledBack: true, Rejected: true, Failed: true}

// Reasons are the fixed causes of a rollback, rejection or failure; the web
// interface has a translated text for each.
var Reasons = []string{"start_failed", "health_check_failed", "signature_invalid", "backup_failed", "pull_failed", "rollback_failed", "box_not_running"}

// Status is the script's report. Every field is empty if absent or invalid.
type Status struct {
	State   State
	Version string // the version that runs now
	Target  string // the version offered or attempted
	Reason  string
	Time    string // RFC 3339 UTC; identifies the event, so a notice is shown once
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
		case "time":
			if _, err := time.Parse(time.RFC3339, val); err == nil && len(val) <= 25 {
				s.Time = val
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

// WriteSettings tells the host script the household's mode (on, off or
// notify). An empty dir means updates are not managed by a host script and
// does nothing.
func WriteSettings(dir, mode string) error {
	if dir == "" {
		return nil
	}
	switch mode {
	case "on", "off", "notify":
	default:
		return fmt.Errorf("update: unknown mode %q", mode)
	}
	tmp, err := os.CreateTemp(dir, ".update-settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := fmt.Fprintf(tmp, "mode=%s\n", mode); err != nil {
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
