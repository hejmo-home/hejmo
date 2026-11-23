package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

// The savings goal over REST (ticket 293).

type goalEndedJSON struct {
	Reason string `json:"reason"`
	At     string `json:"at"`
	Name   string `json:"name"`
}

type goalJSON struct {
	PersonID string         `json:"person_id"`
	RewardID *string        `json:"reward_id"`
	Name     *string        `json:"name"`
	Symbol   *string        `json:"symbol"`
	Price    *int           `json:"price"`
	Balance  int            `json:"balance"`
	Reached  bool           `json:"reached"`
	SetAt    *string        `json:"set_at"`
	Ended    *goalEndedJSON `json:"ended"`
}

func (s *server) goalToJSON(p principal, personID string, g store.Goal, balance int) goalJSON {
	loc := p.location()
	j := goalJSON{PersonID: personID, Balance: balance}
	if g.Active() {
		id, name, symbol, price, at := g.Reward.ID, g.Reward.Name, g.Reward.Symbol, g.Reward.Price, g.SetAt.In(loc).Format(time.RFC3339)
		j.RewardID, j.Name, j.Symbol, j.Price, j.SetAt = &id, &name, &symbol, &price, &at
		j.Reached = balance >= price
	}
	if g.EndedShown(s.now()) {
		j.Ended = &goalEndedJSON{Reason: g.EndedReason, At: g.EndedAt.In(loc).Format(time.RFC3339), Name: g.EndedName}
	}
	return j
}

// goalOf loads a person's goal with their balance.
func (s *server) goalOf(p principal, personID string) (store.Goal, int, error) {
	g, err := s.db.Goal(p.household.ID, personID)
	if err != nil {
		return g, 0, err
	}
	sums, err := s.db.StarSummaries(p.household.ID)
	return g, sums[personID].Balance, err
}

func (s *server) goalFail(w http.ResponseWriter, what string, err error) {
	var fe *store.FieldError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "person not found")
	case errors.As(err, &fe):
		writeFieldError(w, http.StatusUnprocessableEntity, fe.Field, fe.Msg)
	default:
		s.fail(w, what, err)
	}
}

func (s *server) writeGoal(w http.ResponseWriter, p principal, personID string) {
	g, balance, err := s.goalOf(p, personID)
	if err != nil {
		s.goalFail(w, "savings goal", err)
		return
	}
	writeJSON(w, http.StatusOK, s.goalToJSON(p, personID, g, balance))
}

func (s *server) apiGetGoal(w http.ResponseWriter, r *http.Request, p principal) {
	s.writeGoal(w, p, r.PathValue("id"))
}

// ownGoalOnly refuses a child who changes the goal of someone else: 404 for an
// unknown person first, then 403.
func (s *server) ownGoalOnly(w http.ResponseWriter, p principal, personID string) bool {
	if isChild(p) && personID != p.person.ID {
		if _, err := s.db.Person(p.household.ID, personID); err != nil {
			s.goalFail(w, "person", err)
			return false
		}
		writeError(w, http.StatusForbidden, "everyone chooses their own savings goal")
		return false
	}
	return true
}

func (s *server) apiPutGoal(w http.ResponseWriter, r *http.Request, p principal) {
	id := r.PathValue("id")
	if !s.ownGoalOnly(w, p, id) {
		return
	}
	var in struct {
		RewardID string `json:"reward_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := s.db.SetGoal(p.household.ID, id, in.RewardID); err != nil {
		s.goalFail(w, "set savings goal", err)
		return
	}
	s.writeGoal(w, p, id)
}

func (s *server) apiDeleteGoal(w http.ResponseWriter, r *http.Request, p principal) {
	id := r.PathValue("id")
	if !s.ownGoalOnly(w, p, id) {
		return
	}
	if err := s.db.ClearGoal(p.household.ID, id); err != nil {
		s.goalFail(w, "clear savings goal", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
