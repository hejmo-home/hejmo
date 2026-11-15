package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

type starEntryJSON struct {
	ID        string `json:"id"`
	PersonID  string `json:"person_id"`
	Delta     int    `json:"delta"`
	Note      string `json:"note"`
	TaskID    string `json:"task_id"`
	TaskTitle string `json:"task_title"`
	Day       string `json:"day"`
	BookedBy  string `json:"booked_by"`
	CreatedAt string `json:"created_at"`
}

type starAccountJSON struct {
	PersonID string          `json:"person_id"`
	Balance  int             `json:"balance"`
	Shown    bool            `json:"shown"`
	Entries  []starEntryJSON `json:"entries"`
}

func starEntryToJSON(e store.StarEntry, loc *time.Location) starEntryJSON {
	return starEntryJSON{ID: e.ID, PersonID: e.PersonID, Delta: e.Delta, Note: e.Note, TaskID: e.TaskID, TaskTitle: e.TaskTitle,
		Day: e.Day, BookedBy: e.BookedByName, CreatedAt: e.At.In(loc).Format(time.RFC3339)}
}

func (s *server) apiGetStars(w http.ResponseWriter, r *http.Request, p principal) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			writeError(w, http.StatusUnprocessableEntity, "limit must be between 1 and 200")
			return
		}
		limit = n
	}
	acc, err := s.db.StarAccount(p.household.ID, r.PathValue("id"), limit)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "person not found")
	case err != nil:
		s.fail(w, "star account", err)
	default:
		out := starAccountJSON{PersonID: acc.PersonID, Balance: acc.Balance, Shown: acc.Shown, Entries: make([]starEntryJSON, 0, len(acc.Entries))}
		for _, e := range acc.Entries {
			out.Entries = append(out.Entries, starEntryToJSON(e, p.location()))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (s *server) apiBookStars(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Delta int    `json:"delta"`
		Note  string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	e, err := s.db.BookStars(p.household.ID, r.PathValue("id"), in.Delta, in.Note, p.person.ID, s.now())
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "person not found")
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case err != nil:
		s.fail(w, "book stars", err)
	default:
		e.BookedByName = p.person.Name
		writeJSON(w, http.StatusCreated, starEntryToJSON(e, p.location()))
	}
}
