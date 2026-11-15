package server

import (
	"net/http"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/update"
)

const releasesURL = "https://github.com/hejmo-home/hejmo/releases/tag/v"

// updateView is what parents are told about auto-update (ADR 0018), from the
// status the host script left in the data volume.
type updateView struct {
	update.Status
	Mode        store.UpdateMode
	Notice      bool   // a notice is due
	Dismissible bool   // false for "available": it stays until the update is installed
	NotesURL    string // release notes of the version offered or installed
}

func (s *server) updateView(h store.Household) updateView {
	st := update.ReadStatus(s.updateDir)
	if st.State == "" {
		st.State = update.Idle
	}
	v := updateView{Status: st, Mode: h.AutoUpdate}
	switch st.State {
	case update.Updated, update.RolledBack, update.Rejected, update.Failed:
		v.Notice, v.Dismissible = st.Time != "" && st.Time != h.UpdateAck, true
	case update.Available:
		// A report from before the family switched auto-update on is stale.
		v.Notice = h.AutoUpdate == store.UpdateNotify && st.Target != ""
	}
	switch {
	case st.State == update.Updated && st.Version != "":
		v.NotesURL = releasesURL + st.Version
	case st.State == update.Available && st.Target != "":
		v.NotesURL = releasesURL + st.Target
	}
	return v
}

// noticeData is the template value of the update notice, nil if none is due.
func (s *server) updateNotice(h store.Household) *updateView {
	v := s.updateView(h)
	if !v.Notice {
		return nil
	}
	return &v
}

// setAutoUpdate stores the mode and tells the host script.
func (s *server) setAutoUpdate(h store.Household, m store.UpdateMode) error {
	if err := s.db.SetAutoUpdate(h.ID, m); err != nil {
		return err
	}
	return update.WriteSettings(s.updateDir, string(m))
}

func (s *server) handleUpdateDismiss(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if err := s.dismissUpdate(p.household); err != nil {
		s.fail(w, "dismiss update notice", err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) dismissUpdate(h store.Household) error {
	v := s.updateView(h)
	if !v.Dismissible || v.Time == "" {
		return nil
	}
	return s.db.AckUpdate(h.ID, v.Time)
}

type updateJSON struct {
	Mode     store.UpdateMode `json:"mode"`
	Managed  bool             `json:"managed"`
	State    update.State     `json:"state"`
	Version  string           `json:"version,omitempty"`
	Target   string           `json:"target,omitempty"`
	Reason   string           `json:"reason,omitempty"`
	Time     string           `json:"time,omitempty"`
	Notice   bool             `json:"notice"`
	NotesURL string           `json:"release_notes_url,omitempty"`
}

func (s *server) apiGetUpdate(w http.ResponseWriter, _ *http.Request, p principal) {
	v := s.updateView(p.household)
	writeJSON(w, http.StatusOK, updateJSON{v.Mode, s.updateDir != "", v.State, v.Version, v.Target, v.Reason, v.Time, v.Notice, v.NotesURL})
}

func (s *server) apiDismissUpdate(w http.ResponseWriter, _ *http.Request, p principal) {
	if err := s.dismissUpdate(p.household); err != nil {
		s.fail(w, "dismiss update notice", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// syncUpdateSettings writes the household's mode to the settings file at
// start, so the host script follows the database also after a restore.
func (s *server) syncUpdateSettings() {
	h, err := s.db.HomeHousehold()
	if err != nil {
		return // not set up yet: the script's default (on) applies
	}
	if err := update.WriteSettings(s.updateDir, string(h.AutoUpdate)); err != nil {
		s.logger.Error("write update settings", "err", err)
	}
}
