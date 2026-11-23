package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/hejmo-home/hejmo/internal/googleauth"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The REST API of the Google connection (ADR 0015: everything the web pages do
// can be done through the API). The web pages and these handlers share
// startGoogleLogin and finishGoogleLogin.

type googleAppJSON struct {
	Configured   bool              `json:"configured"`
	ClientID     string            `json:"client_id,omitempty"`
	RegisteredAt *time.Time        `json:"registered_at,omitempty"`
	RedirectURIs map[string]string `json:"redirect_uris"`
	Scope        string            `json:"scope"`
	Accounts     []string          `json:"accounts"`
	// SignInAgain names the Google accounts that have to sign in again after a new client ID.
	SignInAgain []string `json:"sign_in_again,omitempty"`
}

func (s *server) googleAppJSON(p principal) (googleAppJSON, error) {
	j := googleAppJSON{
		RedirectURIs: map[string]string{"relay": googleauth.RelayRedirectURI, "fallback": googleauth.FallbackRedirectURI},
		Scope:        googleauth.Scope, Accounts: s.googleAccountsOf(p),
	}
	if j.Accounts == nil {
		j.Accounts = []string{}
	}
	app, err := s.db.GoogleApp(p.household.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return j, nil
	case err != nil:
		return j, err
	}
	j.Configured, j.ClientID = true, app.ClientID
	at := app.CreatedAt
	j.RegisteredAt = &at
	return j, nil
}

func (s *server) apiGetGoogle(w http.ResponseWriter, _ *http.Request, p principal) {
	j, err := s.googleAppJSON(p)
	if err != nil {
		s.fail(w, "google app", err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *server) apiPutGoogle(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if bad := checkGoogleApp(in.ClientID, in.ClientSecret); bad != nil {
		writeFieldError(w, http.StatusUnprocessableEntity, bad.Field, bad.Msg)
		return
	}
	accounts := s.googleAccountsOf(p)
	changed, err := s.db.SetGoogleApp(p.household.ID, p.person.ID, in.ClientID, in.ClientSecret, s.now())
	if err != nil {
		s.fail(w, "google app", err)
		return
	}
	j, err := s.googleAppJSON(p)
	if err != nil {
		s.fail(w, "google app", err)
		return
	}
	if changed {
		j.SignInAgain = accounts
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *server) apiGoogleAuthorize(w http.ResponseWriter, r *http.Request, p principal) {
	if s.refuseOff(w, outbound.Calendars) {
		return
	}
	var in struct {
		ConnectionID string `json:"connection_id"`
		Way          string `json:"way"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if _, err := s.db.GoogleApp(p.household.ID); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusUnprocessableEntity, "enter the app registration first (PUT /google)")
		return
	}
	start, err := s.startGoogleLogin(r, p, in.ConnectionID, in.Way)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "Google connection not found")
	case err != nil:
		s.fail(w, "google sign-in", err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"url": start.URL, "way": start.Way, "redirect_uri": start.RedirectURI, "expires_at": start.Expires})
	}
}

// googleProblemText says in English what a problem means, for the API.
var googleProblemText = map[googleProblem]string{
	gpStateUnknown: "this sign-in was not started by this box",
	gpStateExpired: "this sign-in is older than 15 minutes",
	gpStateUsed:    "this sign-in was already used",
	gpCancelled:    "the sign-in was cancelled at Google",
	gpCodeRejected: "Google did not confirm the sign-in code",
	gpScopeMissing: "access to the calendars was not granted",
	gpClient:       "Google does not accept the client ID or client secret",
	gpWrongAccount: "this is another Google account than the connection's",
	gpNotCallback:  "this is not the address of a finished sign-in",
	gpUnreachable:  "Google could not be reached",
	gpNoApp:        "no app registration is entered (PUT /google)",
}

func (s *server) apiGoogleCallback(w http.ResponseWriter, r *http.Request, p principal) {
	if s.refuseOff(w, outbound.Calendars) {
		return
	}
	var in struct {
		URL   string `json:"url"`
		Code  string `json:"code"`
		State string `json:"state"`
		Error string `json:"error"`
		Scope string `json:"scope"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	cb := googleauth.Callback{Code: in.Code, State: in.State, Error: in.Error, Scope: in.Scope}
	if in.URL != "" {
		var err error
		if cb, err = googleauth.ParseCallbackURL(in.URL); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": googleProblemText[gpNotCallback], "problem": string(gpNotCallback)})
			return
		}
	}
	out, err := s.finishGoogleLogin(r.Context(), p, cb)
	if err != nil {
		s.fail(w, "google sign-in", err)
		return
	}
	if out.Problem != "" {
		body := map[string]string{"error": googleProblemText[out.Problem], "problem": string(out.Problem)}
		if out.Problem == gpWrongAccount {
			body["account"], body["nonce"] = out.Account, out.Nonce
		}
		writeJSON(w, http.StatusUnprocessableEntity, body)
		return
	}
	status := http.StatusCreated
	if out.Reconnected {
		status = http.StatusOK
	}
	conn, err := s.db.Connection(p.household.ID, out.Conn.ID)
	if err != nil {
		s.fail(w, "google sign-in", err)
		return
	}
	writeJSON(w, status, connectionOf(conn))
}

func (s *server) apiGoogleAdopt(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Nonce string `json:"nonce"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	conn, err := s.adoptStashedGoogle(p, in.Nonce)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "there is no such sign-in to add")
	case err != nil:
		s.fail(w, "google sign-in", err)
	default:
		writeJSON(w, http.StatusCreated, connectionOf(conn))
	}
}
