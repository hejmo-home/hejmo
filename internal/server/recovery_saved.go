package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 176: the card of recovery codes has a required "I have saved my
// recovery codes" checkbox. The box refuses to continue without it and says so
// at the checkbox, with the same codes still on screen (only if they are the
// current set, see isCurrentSet). It cannot know how the
// codes were kept (PDF, printout, photo, password manager), only that the
// person says they were; that is stored as a date on the account and My
// account shows it.

var recoveryCodeShape = regexp.MustCompile(`^[A-Z2-9]{5}-[A-Z2-9]{5}$`)

// postedRecoveryCodes returns the codes of the card that came back in the form
// and whether every posted value was a well-formed code. Nothing else is ever
// echoed to the page.
func postedRecoveryCodes(r *http.Request) ([]string, bool) {
	var out []string
	for _, c := range r.PostForm["codes"] {
		c = strings.ToUpper(strings.TrimSpace(c))
		if !recoveryCodeShape.MatchString(c) || len(out) >= recoveryCodeCount {
			return nil, false
		}
		out = append(out, c)
	}
	return out, len(out) > 0
}

// isCurrentSet reports whether the posted codes are exactly the person's
// unused codes, in the order they were made. A card that was shown before a
// newer set was made (a stale tab, the Back button), or codes that were never
// the box's, do not pass.
func (s *server) isCurrentSet(personID string, codes []string) bool {
	stored, err := s.db.UnusedRecoveryCodes(personID)
	if err != nil || len(codes) == 0 || len(stored) != len(codes) {
		return false
	}
	// Not rate-limited on purpose: the route needs a session, the work is
	// capped at 10 checks, and a limiter would lock out a person who retries
	// after a failed upload or a flaky connection.
	ok := make([]bool, len(codes))
	var wg sync.WaitGroup
	gate := make(chan struct{}, 4) // Argon2id is slow on purpose, as when the codes are made
	for i := range codes {
		wg.Add(1)
		gate <- struct{}{}
		go func() {
			defer func() { <-gate; wg.Done() }()
			ok[i] = auth.VerifyRecoveryCode(stored[i].Hash, codes[i])
		}()
	}
	wg.Wait()
	return !slices.Contains(ok, false)
}

func (s *server) handleRecoveryCodesSaved(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	flow := r.PostFormValue("flow")
	ticked := r.PostFormValue("saved") != ""
	// The tick confirms the set the person was shown, nothing else. Codes that
	// are not the current set are replaced by a new one, and the person is told.
	codes, shaped := postedRecoveryCodes(r)
	if !shaped || !s.isCurrentSet(p.person.ID, codes) {
		fresh, err := s.newRecoveryCodes(p.person.ID)
		if err != nil {
			s.fail(w, "recovery codes", err)
			return
		}
		s.showRecoveryCodes(w, r, p.person.ID, recoveryPage{Flow: flow, Codes: fresh, Stale: true, Refused: !ticked})
		return
	}
	if !ticked {
		s.showRecoveryCodes(w, r, p.person.ID, recoveryPage{Flow: flow, Codes: codes, Refused: true})
		return
	}
	if err := s.db.ConfirmRecoveryCodesSaved(p.person.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, "recovery codes saved", err)
		return
	}
	switch flow {
	case flowWizard:
		s.redirectSaved(w, r, "/", "flash.setup_done:"+p.person.ID)
	case flowInvite:
		s.redirectSaved(w, r, "/", "flash.invite_done:"+p.person.ID)
	default:
		s.redirectSaved(w, r, "/account", "flash.recovery_codes_saved")
	}
}

// apiRecoveryCodesSaved is the API side of the checkbox: the caller says they
// saved the set they were given.
func (s *server) apiRecoveryCodesSaved(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Saved bool `json:"saved"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !in.Saved {
		writeError(w, http.StatusUnprocessableEntity, "saved must be true: confirm only once the codes are saved")
		return
	}
	switch err := s.db.ConfirmRecoveryCodesSaved(p.person.ID); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusForbidden, "this person has no account")
	case err != nil:
		s.fail(w, "recovery codes saved", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
