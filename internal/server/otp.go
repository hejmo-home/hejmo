package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/i18n"
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
	codes, err := s.newRecoveryCodes(personID)
	if err != nil {
		s.fail(w, "recovery codes", err)
		return
	}
	s.showRecoveryCodes(w, r, codes)
}

// showRecoveryCodes renders a set that was just stored. It is the only time
// the codes are shown.
func (s *server) showRecoveryCodes(w http.ResponseWriter, r *http.Request, codes []string) {
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, http.StatusOK, "recovery_codes.html", map[string]any{"Codes": codes})
}

type otherParent struct{ ID, Name, Username string }

type otpPage struct {
	Unused  int
	Parents []otherParent
	// ResetName is the person whose codes were just reset.
	ResetName          string
	DoneText, LeftText string
	// Error and ErrorFor tell which reset form failed.
	Error, ErrorFor string
}

func (s *server) renderOTP(w http.ResponseWriter, r *http.Request, p principal, status int, v otpPage) {
	codes, err := s.db.UnusedRecoveryCodes(p.person.ID)
	if err != nil {
		s.fail(w, "recovery codes", err)
		return
	}
	v.Unused = len(codes)
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	for _, x := range people {
		if x.ID == p.person.ID || x.Role != store.RoleParent {
			continue
		}
		if acc, err := s.db.AccountByPerson(x.ID); err == nil {
			v.Parents = append(v.Parents, otherParent{x.ID, x.Name, acc.Username})
		}
	}
	lang := s.langOf(r)
	v.LeftText = s.i18n.Format(lang, i18n.Msg("otp.recovery.left", "count", strconv.Itoa(v.Unused)))
	if v.ResetName != "" {
		v.DoneText = s.i18n.Format(lang, i18n.Msg("otp.reset.done", "name", v.ResetName))
	}
	s.render(w, r, status, "otp.html", map[string]any{
		"Person": p.person, "Page": v, "SyncStale": s.syncStale(p.household.ID),
	})
}

func (s *server) handleOTPPage(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.renderOTP(w, r, p, http.StatusOK, otpPage{})
	}
}

func (s *server) handleRecoveryCodesNew(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.issueRecoveryCodes(w, r, p.person.ID)
	}
}

type resetOutcome int

const (
	resetDone resetOutcome = iota
	resetSelf
	resetUnknown
	resetWrongConfirmation
	resetLimited
)

// resetOtherOTP resets another parent's one-time codes after the acting parent
// confirmed with their own password and one-time code (ADR 0017).
func (s *server) resetOtherOTP(actor store.Person, household, targetID, password, code string) (store.Person, resetOutcome, error) {
	if targetID == actor.ID {
		return store.Person{}, resetSelf, nil
	}
	target, err := s.db.Person(household, targetID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && target.Role != store.RoleParent) {
		return store.Person{}, resetUnknown, nil
	}
	if err != nil {
		return store.Person{}, 0, err
	}
	targetAcc, err := s.db.AccountByPerson(target.ID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Person{}, resetUnknown, nil
	}
	if err != nil {
		return store.Person{}, 0, err
	}
	own, err := s.db.AccountByPerson(actor.ID)
	if err != nil {
		return store.Person{}, 0, err
	}
	key := strings.ToLower(own.Username)
	if s.limiter.blocked(key) {
		return store.Person{}, resetLimited, nil
	}
	if !auth.VerifyPassword(own.PasswordHash, password) || !s.secondFactorOK(own, code) {
		s.limiter.fail(key)
		return store.Person{}, resetWrongConfirmation, nil
	}
	s.limiter.reset(key)
	if err := s.db.ResetOTP(target.ID); err != nil {
		return store.Person{}, 0, err
	}
	s.logger.Info("one-time codes reset", "account", targetAcc.Username, "by", own.Username, "via", "web interface")
	return target, resetDone, nil
}

func (s *server) handleOTPReset(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	target, outcome, err := s.resetOtherOTP(p.person, p.household.ID, r.PathValue("id"), r.PostFormValue("password"), r.PostFormValue("code"))
	switch {
	case err != nil:
		s.fail(w, "reset one-time codes", err)
	case outcome == resetDone:
		s.renderOTP(w, r, p, http.StatusOK, otpPage{ResetName: target.Name})
	case outcome == resetSelf:
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
	case outcome == resetUnknown:
		http.NotFound(w, r)
	case outcome == resetLimited:
		s.renderOTP(w, r, p, http.StatusTooManyRequests, otpPage{Error: "login.error.limit", ErrorFor: r.PathValue("id")})
	default:
		s.renderOTP(w, r, p, http.StatusUnprocessableEntity, otpPage{Error: "otp.error.confirm", ErrorFor: r.PathValue("id")})
	}
}

func (s *server) apiNewRecoveryCodes(w http.ResponseWriter, _ *http.Request, p principal) {
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
	var in struct{ Password, Code string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	_, outcome, err := s.resetOtherOTP(p.person, p.household.ID, r.PathValue("id"), in.Password, in.Code)
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
