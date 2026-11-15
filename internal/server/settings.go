package server

import (
	"encoding/json"
	"net/http"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/store"
)

type settingsJSON struct {
	TimeFormat      store.TimeFormat      `json:"time_format"`
	TemperatureUnit store.TemperatureUnit `json:"temperature_unit"`
}

func (s *server) apiGetSettings(w http.ResponseWriter, _ *http.Request, p principal) {
	writeJSON(w, http.StatusOK, settingsJSON{p.household.TimeFormat, p.household.TemperatureUnit})
}

func (s *server) apiUpdateSettings(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		TimeFormat      *store.TimeFormat      `json:"time_format"`
		TemperatureUnit *store.TemperatureUnit `json:"temperature_unit"`
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
	if !f.Valid() || !u.Valid() {
		writeError(w, http.StatusUnprocessableEntity, "time_format must be 24h or 12h and temperature_unit c or f")
		return
	}
	if err := s.db.UpdateHouseholdSettings(p.household.ID, f, u); err != nil {
		s.fail(w, "update settings", err)
		return
	}
	writeJSON(w, http.StatusOK, settingsJSON{f, u})
}

func (s *server) renderSettings(w http.ResponseWriter, r *http.Request, p principal, status int, errKey string) {
	s.render(w, r, status, "household.html", map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Error": errKey,
		"TimeFormat": string(p.household.TimeFormat), "TemperatureUnit": string(p.household.TemperatureUnit),
	})
}

// handleSettingsHub shows the Settings hub: one calm row per setup screen.
func (s *server) handleSettingsHub(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	var items []hubItem
	for _, it := range hubItems {
		if auth.RoleAllows(p.person.Role, it.Needs) {
			items = append(items, it)
		}
	}
	s.render(w, r, http.StatusOK, "settings.html", map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Hub": items,
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
	if !f.Valid() || !u.Valid() {
		s.renderSettings(w, r, p, http.StatusUnprocessableEntity, "settings.error.invalid")
		return
	}
	if err := s.db.UpdateHouseholdSettings(p.household.ID, f, u); err != nil {
		s.fail(w, "update settings", err)
		return
	}
	s.redirectSaved(w, r, "/settings/household", "flash.saved")
}
