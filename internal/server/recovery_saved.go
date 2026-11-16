package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 176: the card of recovery codes has a required "I have saved my
// recovery codes" checkbox. The box refuses to continue without it and says so
// at the checkbox, with the same codes still on screen. It cannot know how the
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

func (s *server) handleRecoveryCodesSaved(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	flow := r.PostFormValue("flow")
	if r.PostFormValue("saved") == "" {
		s.refuseUnsaved(w, r, p, flow)
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

// refuseUnsaved answers a continue without the tick: the same card, the same
// codes, the error at the checkbox. If the codes did not come back intact (a
// crafted request), a new set is made instead of echoing anything.
func (s *server) refuseUnsaved(w http.ResponseWriter, r *http.Request, p principal, flow string) {
	codes, ok := postedRecoveryCodes(r)
	pg := recoveryPage{Flow: flow, Codes: codes, Refused: true}
	if !ok {
		fresh, err := s.newRecoveryCodes(p.person.ID)
		if err != nil {
			s.fail(w, "recovery codes", err)
			return
		}
		pg.Codes, pg.Reloaded = fresh, true
	}
	s.showRecoveryCodes(w, r, p.person.ID, pg)
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
