package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/store"
)

// maxNoteRunes keeps the note to a line or two in the tomorrow tile.
const maxNoteRunes = 120

type dayNoteJSON struct {
	Day  string `json:"day"`
	Text string `json:"text"`
}

func (s *server) apiListDayNotes(w http.ResponseWriter, r *http.Request, p principal) {
	if err := s.db.PruneDayNotes(p.household.ID, s.householdNow(p.household).Format(store.DayLayout)); err != nil { // only today and later
		s.fail(w, "day notes", err)
		return
	}
	notes, err := s.db.DayNotes(p.household.ID)
	if err != nil {
		s.fail(w, "day notes", err)
		return
	}
	out := []dayNoteJSON{}
	for _, n := range notes {
		out = append(out, dayNoteJSON{n.Day, n.Text})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiSetDayNote(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	in.Text = strings.TrimSpace(in.Text)
	if utf8.RuneCountInString(in.Text) > maxNoteRunes || in.Text == "" {
		writeError(w, http.StatusUnprocessableEntity, "text: 1 to 120 characters")
		return
	}
	day := r.PathValue("day")
	if s.storeError(w, "day note", s.db.SetDayNote(p.household.ID, day, in.Text)) {
		return
	}
	n, _, err := s.db.DayNote(p.household.ID, day)
	if err != nil {
		s.fail(w, "day note", err)
		return
	}
	writeJSON(w, http.StatusOK, dayNoteJSON{day, n})
}

func (s *server) apiDeleteDayNote(w http.ResponseWriter, r *http.Request, p principal) {
	if s.storeError(w, "delete day note", s.db.SetDayNote(p.household.ID, r.PathValue("day"), "")) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleCalendarWaste(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	on := r.PostFormValue("waste") == "on"
	switch err := s.db.SetCalendarWaste(p.household.ID, r.PathValue("id"), on); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	case err != nil:
		s.fail(w, "waste calendar", err)
	default:
		key := "flash.waste_off"
		if on {
			key = "flash.waste_on"
		}
		s.redirectSaved(w, r, "/calendars", key)
	}
}

// handleNoteSave sets (or, with empty text, removes) the note of a day.
func (s *server) handleNoteSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	text := strings.TrimSpace(r.PostFormValue("text"))
	if utf8.RuneCountInString(text) > maxNoteRunes {
		http.Error(w, "note too long", http.StatusUnprocessableEntity)
		return
	}
	switch err := s.db.SetDayNote(p.household.ID, r.PostFormValue("day"), text); {
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	case err != nil:
		s.fail(w, "day note", err)
	default:
		s.redirectSaved(w, r, "/calendars", "flash.note_saved")
	}
}
