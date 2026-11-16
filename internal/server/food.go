package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/hejmo-home/hejmo/internal/food"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Diet and allergies per person (ticket 163, ADR 0019). They are health data:
// only parents change them, and the API gives them only to the explicit scope
// "health" (never to "read" or "write"), so MCP tokens and other clients get
// them only when a parent grants it.

// uncheckedNote is part of every answer: free text is never checked.
const uncheckedNote = "Freitext-Allergien (other) sind nicht automatisch geprüft und nicht filterbar. Free-text allergies (other) are not checked automatically and cannot be filtered by."

type foodJSON struct {
	PersonID  string   `json:"person_id"`
	Diet      string   `json:"diet"`
	Allergens []string `json:"allergens"`
	Other     []string `json:"other"`
	OtherNote string   `json:"other_note"`
}

func foodToJSON(personID string, f store.Food) foodJSON {
	other, _ := food.ParseOther(f.Other)
	return foodJSON{PersonID: personID, Diet: f.Diet, Allergens: nonNil(f.Allergens), Other: nonNil(other), OtherNote: uncheckedNote}
}

// foodOtherText joins the entries of the free text as it is stored and shown.
func foodOtherText(items []string) string { return strings.Join(items, ", ") }

func (s *server) apiGetFood(w http.ResponseWriter, r *http.Request, p principal) {
	id := r.PathValue("id")
	f, err := s.db.PersonFood(p.household.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "person not found")
		return
	} else if err != nil {
		s.fail(w, "get food", err)
		return
	}
	writeJSON(w, http.StatusOK, foodToJSON(id, f))
}

func (s *server) apiListFood(w http.ResponseWriter, _ *http.Request, p principal) {
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "list people", err)
		return
	}
	all, err := s.db.HouseholdFood(p.household.ID)
	if err != nil {
		s.fail(w, "list food", err)
		return
	}
	out := make([]foodJSON, 0, len(people))
	for _, x := range people {
		f, ok := all[x.ID]
		if !ok {
			f = store.Food{Diet: string(food.Omnivore)}
		}
		out = append(out, foodToJSON(x.ID, f))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiPutFood(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Diet      string   `json:"diet"`
		Allergens []string `json:"allergens"`
		Other     []string `json:"other"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if !food.ValidDiet(in.Diet) {
		writeError(w, http.StatusUnprocessableEntity, "invalid diet: omnivore, vegetarian, pescatarian or vegan")
		return
	}
	allergens, ok := food.NormalizeAllergens(in.Allergens)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "invalid allergens: use the keys of the 14 main EU allergens")
		return
	}
	for _, o := range in.Other {
		if strings.Contains(o, ",") {
			writeError(w, http.StatusUnprocessableEntity, "invalid other: one entry per item, without commas")
			return
		}
	}
	items, conflict := food.ParseOther(foodOtherText(in.Other))
	if conflict != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid other: \""+conflict.Word+"\" is one of the 14 main allergens; send the key \""+conflict.Allergen+"\" in allergens")
		return
	}
	text := foodOtherText(items)
	if !food.OtherFits(text) {
		writeError(w, http.StatusUnprocessableEntity, "invalid other: at most 200 characters")
		return
	}
	id := r.PathValue("id")
	err := s.db.SetPersonFood(p.household.ID, id, store.Food{Diet: in.Diet, Allergens: allergens, Other: text})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "person not found")
		return
	} else if err != nil {
		s.fail(w, "set food", err)
		return
	}
	writeJSON(w, http.StatusOK, foodToJSON(id, store.Food{Diet: in.Diet, Allergens: allergens, Other: text}))
}
