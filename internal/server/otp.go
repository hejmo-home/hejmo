package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/store"
)

// recoveryCodeCount is the size of a set of recovery codes (ADR 0017).
const recoveryCodeCount = 10

// newRecoveryCodes stores a fresh set for the person, which voids the old
// one, and returns the codes in plain text: this is the only time they exist.
func (s *server) newRecoveryCodes(personID string) ([]string, error) {
	codes := auth.NewRecoveryCodes(recoveryCodeCount)
	// Argon2id is slow on purpose; four at a time keeps a Pi's memory in check.
	hashes := make([]string, len(codes))
	var wg sync.WaitGroup
	gate := make(chan struct{}, 4)
	for i, c := range codes {
		wg.Add(1)
		gate <- struct{}{}
		go func() {
			defer func() { <-gate; wg.Done() }()
			hashes[i] = auth.HashRecoveryCode(c)
		}()
	}
	wg.Wait()
	return codes, s.db.ReplaceRecoveryCodes(personID, hashes)
}

// issueRecoveryCodes makes a new set and shows it once.
func (s *server) issueRecoveryCodes(w http.ResponseWriter, r *http.Request, personID string) {
	// A pending account sets up new codes first; the phone must not mint any.
	if acc, err := s.db.AccountByPerson(personID); err == nil && !acc.TOTPConfirmed {
		http.Redirect(w, r, "/setup/totp", http.StatusSeeOther)
		return
	}
	codes, err := s.newRecoveryCodes(personID)
	if err != nil {
		s.fail(w, "recovery codes", err)
		return
	}
	s.showRecoveryCodes(w, r, codes, false)
}

// showRecoveryCodes renders a set that was just stored. It is the only time
// the codes are shown. In the first setup (wizard) it is the second half of
// step 3 and leads on to the first steps; otherwise it sits in the frame.
func (s *server) showRecoveryCodes(w http.ResponseWriter, r *http.Request, codes []string, wizard bool) {
	if wizard {
		s.showRecoveryCodesIn(w, r, codes, wizardAt(3), "recovery.continue.wizard")
		return
	}
	s.showRecoveryCodesIn(w, r, codes, nil, "recovery.continue")
}

// showRecoveryCodesIn shows a set of recovery codes in the given wizard shell
// (nil: the page frame) with the given text on the last button.
func (s *server) showRecoveryCodesIn(w http.ResponseWriter, r *http.Request, codes []string, shell *wizardView, continueKey string) {
	w.Header().Set("Cache-Control", "no-store")
	data := map[string]any{"Codes": codes, "ContinueKey": continueKey}
	if shell != nil {
		data["Wizard"] = shell
	} else {
		data["SuccessKey"] = "flash.recovery_codes_created"
	}
	s.render(w, r, http.StatusOK, "recovery_codes.html", data)
}

func (s *server) apiNewRecoveryCodes(w http.ResponseWriter, _ *http.Request, p principal) {
	// A token outlives a reset; until the new codes are confirmed the account
	// may only set them up, not mint recovery codes.
	acc, err := s.db.AccountByPerson(p.person.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusForbidden, "this person has no account")
		return
	}
	if err != nil {
		s.fail(w, "account", err)
		return
	}
	if !acc.TOTPConfirmed {
		writeError(w, http.StatusForbidden, "confirm your new one-time codes first")
		return
	}
	codes, err := s.newRecoveryCodes(p.person.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusForbidden, "this person has no account")
		return
	}
	if err != nil {
		s.fail(w, "recovery codes", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string][]string{"codes": codes})
}

func (s *server) apiResetOTP(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Password       string   `json:"password"`
		Code           string   `json:"code"`
		RemovePasskeys []string `json:"remove_passkeys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	_, outcome, err := s.resetOtherOTP(p.person, p.household.ID, r.PathValue("id"), resetConfirmation{Password: in.Password, Code: in.Code}, in.RemovePasskeys)
	switch {
	case err != nil:
		s.fail(w, "reset one-time codes", err)
	case outcome == resetDone:
		w.WriteHeader(http.StatusNoContent)
	case outcome == resetSelf:
		writeError(w, http.StatusForbidden, "a parent cannot reset their own one-time codes this way")
	case outcome == resetUnknown:
		writeError(w, http.StatusNotFound, "parent not found")
	case outcome == resetLimited:
		writeError(w, http.StatusTooManyRequests, "too many failed attempts")
	default:
		writeError(w, http.StatusUnprocessableEntity, "password or one-time code is wrong")
	}
}
