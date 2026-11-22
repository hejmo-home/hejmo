package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// The units of the household over the API (ticket 165, ADR 0019): the units
// that can be chosen when adding to a list. They follow the country of the
// home until a parent changes them.

type unitInfoJSON struct {
	Key   string  `json:"key"`
	Group string  `json:"group"`
	Kind  string  `json:"kind"`
	Size  float64 `json:"size"`
}

type unitsJSON struct {
	Country string         `json:"country"`
	Custom  bool           `json:"custom"`
	Active  []string       `json:"active"`
	Units   []unitInfoJSON `json:"units"`
}

func (s *server) unitsToJSON(h store.Household) unitsJSON {
	_, custom, _ := s.db.ActiveUnits(h.ID)
	out := unitsJSON{Country: homeCountry(h), Custom: custom, Active: s.activeUnits(h), Units: []unitInfoJSON{}}
	for _, k := range unit.All {
		out.Units = append(out.Units, unitInfoJSON{Key: k, Group: unit.GroupOf(k), Kind: string(unit.KindOf(k)), Size: unit.Size(k)})
	}
	return out
}

func (s *server) apiGetUnits(w http.ResponseWriter, _ *http.Request, p principal) {
	writeJSON(w, http.StatusOK, s.unitsToJSON(p.household))
}

func (s *server) apiPutUnits(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Active []string `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Active == nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: active is a list of unit keys")
		return
	}
	if err := s.db.SetActiveUnits(p.household.ID, in.Active); err != nil {
		var fe *store.FieldError
		if errors.As(err, &fe) {
			writeFieldError(w, http.StatusUnprocessableEntity, fe.Field, fe.Msg)
			return
		}
		s.fail(w, "set units", err)
		return
	}
	writeJSON(w, http.StatusOK, s.unitsToJSON(p.household))
}

func (s *server) apiResetUnits(w http.ResponseWriter, _ *http.Request, p principal) {
	if err := s.db.ResetActiveUnits(p.household.ID); err != nil {
		s.fail(w, "reset units", err)
		return
	}
	writeJSON(w, http.StatusOK, s.unitsToJSON(p.household))
}
