package server

import (
	"net/http"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/update"
)

// The REST side of uploading an update file (ADR 0015 parity with the pages of
// ticket 221): the same checks, the same one file at a time, the same yes.

type updateFileJSON struct {
	State string `json:"state"` // none, ready, installing
	// Available is whether a file can be uploaded now: offline (or the update check off), a host script that installs files, and no failed rollback.
	Available   bool   `json:"available"`
	Version     string `json:"version,omitempty"`
	FromVersion string `json:"from_version,omitempty"`
	Arch        string `json:"arch,omitempty"`
	FileName    string `json:"file_name,omitempty"`
	Size        int64  `json:"size,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Released    string `json:"released,omitempty"`
	Signer      string `json:"signer,omitempty"`
	UploadedAt  string `json:"uploaded_at,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	StartedBy   string `json:"started_by,omitempty"`
	Step        string `json:"step,omitempty"`
}

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// installStep is where the host script is with the installation of u: the
// first step until it says otherwise.
func installStep(st update.Status, u store.UpdateUpload) string {
	if st.State == update.Installing && st.Target == u.Version {
		if t, err := time.Parse(time.RFC3339, st.Time); err == nil && !t.Before(u.StartedAt) && st.Step != "" {
			return st.Step
		}
	}
	return update.Steps[0]
}

func (s *server) updateFileState() updateFileJSON {
	out := updateFileJSON{State: "none", Available: s.uploadsPossible() && s.hostStaysOffline() && !s.rollbackFailed()}
	u, ok, err := s.db.UpdateUpload()
	if err != nil || !ok {
		return out
	}
	out.State = u.State
	out.Version, out.FromVersion, out.Arch, out.FileName, out.Size, out.SHA256, out.Released, out.Signer = u.Version, u.FromVersion, u.Arch, u.FileName, u.Size, u.SHA256, u.Released, u.Signer
	out.UploadedAt = rfc(u.UploadedAt)
	if u.State == store.UploadReady {
		out.ExpiresAt = rfc(u.UploadedAt.Add(uploadTTL))
		return out
	}
	out.StartedAt, out.StartedBy = rfc(u.StartedAt), u.StartedBy
	out.Step = installStep(update.ReadStatus(s.updateDir), u)
	return out
}

func (s *server) apiGetUpdateFile(w http.ResponseWriter, _ *http.Request, _ principal) {
	s.settleUpload()
	writeJSON(w, http.StatusOK, s.updateFileState())
}

func (s *server) apiUploadUpdateFile(w http.ResponseWriter, r *http.Request, p principal) {
	_, fail := s.receiveUpload(w, r)
	if fail != nil {
		if fail.code == "server" {
			s.fail(w, "update upload", fail.err)
			return
		}
		status := fail.status
		switch fail.code {
		case "too_big_server":
			status = http.StatusRequestEntityTooLarge
		case "no_room":
			status = http.StatusInsufficientStorage
		}
		writeJSON(w, status, map[string]string{"error": s.failureText(s.langOf(r), fail), "field": "update_file", "code": fail.code})
		return
	}
	writeJSON(w, http.StatusCreated, s.updateFileState())
}

func (s *server) apiInstallUpdateFile(w http.ResponseWriter, r *http.Request, p principal) {
	if !uploadOverTLS(r) {
		writeError(w, http.StatusForbidden, "installing an update file works only over HTTPS")
		return
	}
	switch s.startInstall(p) {
	case startNone:
		writeError(w, http.StatusNotFound, "no update file is waiting")
	case startRunning:
		writeError(w, http.StatusConflict, "an update is being installed already")
	case startFailed:
		s.fail(w, "start the installation", http.ErrAbortHandler)
	default:
		writeJSON(w, http.StatusAccepted, s.updateFileState())
	}
}

func (s *server) apiDiscardUpdateFile(w http.ResponseWriter, _ *http.Request, _ principal) {
	s.settleUpload()
	u, ok, _ := s.db.UpdateUpload()
	switch {
	case !ok:
		writeError(w, http.StatusNotFound, "no update file is waiting")
	case u.State != store.UploadReady:
		writeError(w, http.StatusConflict, "an update is being installed")
	case s.discardUpload():
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusNotFound, "no update file is waiting")
	}
}
