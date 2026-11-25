package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/hejmo-home/hejmo/internal/reward"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Redeeming rewards over REST (ticket 292): a child asks, withdraws; a parent
// confirms, declines or redeems directly.

// answerDays is how long a decided request stays in the child's list.
const answerDays = 7

type redemptionJSON struct {
	ID            string  `json:"id"`
	PersonID      string  `json:"person_id"`
	RewardID      *string `json:"reward_id"`
	Name          string  `json:"name"`
	Symbol        string  `json:"symbol"`
	Price         int     `json:"price"`
	Status        string  `json:"status"`
	Note          string  `json:"note"`
	AskedAt       string  `json:"asked_at"`
	DecidedAt     *string `json:"decided_at"`
	DecidedBy     *string `json:"decided_by"`
	DecidedByName string  `json:"decided_by_name"`
	Direct        bool    `json:"direct"`
	DeclineReason *string `json:"decline_reason"`
}

func (s *server) redemptionToJSON(p principal, r store.Redemption) redemptionJSON {
	loc := p.location()
	j := redemptionJSON{ID: r.ID, PersonID: r.PersonID, Name: r.Name, Symbol: r.Symbol, Price: r.Price, Status: string(r.Status), Note: r.Note,
		AskedAt: r.AskedAt.In(loc).Format(time.RFC3339), DecidedByName: r.DecidedByName, Direct: r.Direct}
	if r.RewardID != "" {
		j.RewardID = &r.RewardID
	}
	if !r.DecidedAt.IsZero() {
		at := r.DecidedAt.In(loc).Format(time.RFC3339)
		j.DecidedAt = &at
	}
	if r.DecidedBy != "" {
		j.DecidedBy = &r.DecidedBy
	}
	if r.DeclineReason != "" {
		j.DeclineReason = &r.DeclineReason
	}
	return j
}

// redemptionError answers a refused ask, decision or redemption.
func (s *server) redemptionError(w http.ResponseWriter, p principal, what string, err error) {
	var re *store.RedeemError
	var de *store.DecidedError
	var fe *store.FieldError
	loc := p.location()
	switch {
	case errors.As(err, &re):
		body := map[string]any{"error": "cannot redeem now", "code": string(re.Check.Reason)}
		if re.Check.Reason == reward.NotEnoughStars {
			body["missing"] = re.Check.Missing
		} else {
			body["available_again"] = re.Check.AvailableAgain.In(loc).Format(time.RFC3339)
		}
		writeJSON(w, http.StatusConflict, body)
	case errors.As(err, &de):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "already decided", "code": "decided", "redemption": s.redemptionToJSON(p, de.Redemption)})
	case errors.Is(err, store.ErrAlreadyAsked):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "there is already an open request for this reward", "code": "asked"})
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrArchived):
		writeError(w, http.StatusNotFound, "reward not found")
	case errors.As(err, &fe):
		writeFieldError(w, http.StatusUnprocessableEntity, fe.Field, fe.Msg)
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		s.fail(w, what, err)
	}
}

func (s *server) apiListRedemptions(w http.ResponseWriter, _ *http.Request, p principal) {
	var list []store.Redemption
	var err error
	if isChild(p) {
		list, err = s.db.RedemptionsOf(p.household.ID, p.person.ID, s.now().AddDate(0, 0, -answerDays))
	} else {
		list, err = s.db.OpenRedemptions(p.household.ID)
	}
	if err != nil {
		s.fail(w, "list requests", err)
		return
	}
	out := make([]redemptionJSON, 0, len(list))
	for _, r := range list {
		out = append(out, s.redemptionToJSON(p, r))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiAskReward(w http.ResponseWriter, r *http.Request, p principal) {
	if !isChild(p) {
		writeError(w, http.StatusForbidden, "only a child asks; a parent redeems with POST /rewards/{id}/redeem")
		return
	}
	got, err := s.db.AskReward(p.household.ID, p.person.ID, r.PathValue("id"), s.now(), p.location())
	if err != nil {
		s.redemptionError(w, p, "ask reward", err)
		return
	}
	writeJSON(w, http.StatusCreated, s.redemptionToJSON(p, got))
}

func (s *server) apiWithdrawRedemption(w http.ResponseWriter, r *http.Request, p principal) {
	if !isChild(p) {
		writeError(w, http.StatusForbidden, "only a child takes a request back")
		return
	}
	got, err := s.db.WithdrawRedemption(p.household.ID, r.PathValue("id"), p.person.ID, s.now())
	if err != nil {
		s.redemptionError(w, p, "withdraw request", err)
		return
	}
	writeJSON(w, http.StatusOK, s.redemptionToJSON(p, got))
}

func (s *server) apiConfirmRedemption(w http.ResponseWriter, r *http.Request, p principal) {
	got, err := s.db.ConfirmRedemption(p.household.ID, r.PathValue("id"), p.person.ID, s.now(), p.location())
	if err != nil {
		s.redemptionError(w, p, "confirm request", err)
		return
	}
	writeJSON(w, http.StatusOK, s.redemptionToJSON(p, got))
}

func (s *server) apiDeclineRedemption(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Note string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	got, err := s.db.DeclineRedemption(p.household.ID, r.PathValue("id"), p.person.ID, in.Note, s.now())
	if err != nil {
		s.redemptionError(w, p, "decline request", err)
		return
	}
	writeJSON(w, http.StatusOK, s.redemptionToJSON(p, got))
}

func (s *server) apiRedeemReward(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		PersonID string `json:"person_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if in.PersonID == "" {
		writeFieldError(w, http.StatusUnprocessableEntity, "person_id", "say who redeems")
		return
	}
	got, err := s.db.RedeemNow(p.household.ID, r.PathValue("id"), in.PersonID, p.person.ID, s.now(), p.location())
	if err != nil {
		s.redemptionError(w, p, "redeem reward", err)
		return
	}
	writeJSON(w, http.StatusCreated, s.redemptionToJSON(p, got))
}
