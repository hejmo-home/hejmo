package server

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

// apiOpenPairing is POST /display/pairing: a display without a token asks for
// a pairing code. The answer carries the display's token once; the box keeps
// only its hash. The per-address limit and the cap on waiting displays are the
// ones of the pairing page.
func (s *server) apiOpenPairing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.db.Configured() {
		writeError(w, http.StatusConflict, "the box is not set up yet")
		return
	}
	h, err := s.db.HomeHousehold()
	if err != nil {
		s.fail(w, "household", err)
		return
	}
	d, token, err := s.openPairing(r, h)
	if errors.Is(err, errPairingsLimited) {
		w.Header().Set("Retry-After", "600")
		writeError(w, http.StatusTooManyRequests, "too many pairings opened, try again later")
		return
	}
	if err != nil {
		s.fail(w, "open pairing", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"token": token, "state": "pairing", "code": d.Code,
		"code_expires_at": d.CodeExpires.UTC().Format(time.RFC3339),
		"pair_url":        s.pairURL(r, d.Code),
	})
}

// apiDisplayState is GET /display/state: the display's state, read with its
// token as bearer token. An unknown or revoked token is "unpaired", as on the
// page route, so a display needs one rule to find out it must pair again.
func (s *server) apiDisplayState(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.displayState(r))
}
