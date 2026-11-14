package server

import (
	"encoding/json"
	"net/http"

	"github.com/hejmo-home/hejmo/internal/store"
)

type settingsJSON struct {
	TimeFormat      store.TimeFormat      `json:"time_format"`
	TemperatureUnit store.TemperatureUnit `json:"temperature_unit"`
	AutoUpdate      store.UpdateMode      `json:"auto_update"`
}

func (s *server) apiGetSettings(w http.ResponseWriter, _ *http.Request, p principal) {
	writeJSON(w, http.StatusOK, settingsJSON{p.household.TimeFormat, p.household.TemperatureUnit, p.household.AutoUpdate})
}

func (s *server) apiUpdateSettings(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		TimeFormat      *store.TimeFormat      `json:"time_format"`
		TemperatureUnit *store.TemperatureUnit `json:"temperature_unit"`
		AutoUpdate      *store.UpdateMode      `json:"auto_update"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	f, u := p.household.TimeFormat, p.household.TemperatureUnit
	if in.TimeFormat != nil {
		f = *in.TimeFormat
	}
	if in.TemperatureUnit != nil {
		u = *in.TemperatureUnit
	}
	a := p.household.AutoUpdate
	if in.AutoUpdate != nil {
		a = *in.AutoUpdate
	}
	if !f.Valid() || !u.Valid() || !a.Valid() {
		writeError(w, http.StatusUnprocessableEntity, "time_format must be 24h or 12h, temperature_unit c or f and auto_update on, off or notify")
		return
	}
	if err := s.saveSettings(p.household, f, u, a); err != nil {
		s.fail(w, "update settings", err)
		return
	}
	writeJSON(w, http.StatusOK, settingsJSON{f, u, a})
}

func (s *server) renderSettings(w http.ResponseWriter, r *http.Request, p principal, status int, errKey string) {
	s.render(w, r, status, "settings.html", map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Error": errKey,
		"TimeFormat": string(p.household.TimeFormat), "TemperatureUnit": string(p.household.TemperatureUnit),
		"AutoUpdate": string(p.household.AutoUpdate), "Update": s.updateNotice(p.household),
	})
}

func (s *server) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.renderSettings(w, r, p, http.StatusOK, "")
}

func (s *server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	f, u := store.TimeFormat(r.PostFormValue("time_format")), store.TemperatureUnit(r.PostFormValue("temperature_unit"))
	a := p.household.AutoUpdate
	if v := r.PostFormValue("auto_update"); v != "" { // a form without the field leaves it alone
		a = store.UpdateMode(v)
	}
	if !f.Valid() || !u.Valid() || !a.Valid() {
		s.renderSettings(w, r, p, http.StatusUnprocessableEntity, "settings.error.invalid")
		return
	}
	if err := s.saveSettings(p.household, f, u, a); err != nil {
		s.fail(w, "update settings", err)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// saveSettings stores all household settings; the update mode also goes to the host script.
func (s *server) saveSettings(h store.Household, f store.TimeFormat, u store.TemperatureUnit, a store.UpdateMode) error {
	if err := s.db.UpdateHouseholdSettings(h.ID, f, u); err != nil {
		return err
	}
	if a == h.AutoUpdate {
		return nil
	}
	return s.setAutoUpdate(h, a)
}
