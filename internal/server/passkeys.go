package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/passkey"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Passkeys (ticket 95, ADR 0018). A passkey is an additional sign-in for
// adults. It is valid only for the box name and only offered when the box is
// reached by that name, never by IP address. The pages and the ceremonies of
// the browser live here; the protocol is internal/passkey.

// passkeyUsedCookie is the flag a browser keeps after a sign-in with a
// passkey: no secret, no name, so a shared device does not tell who signed in.
// With it the sign-in page makes the passkey the main action.
const passkeyUsedCookie = "hejmo_passkey_used"

// maxPasskeyName is the longest name of a passkey, in characters.
const maxPasskeyName = 80

// boxName is the name the box is served under (GLOSSARY: Box name): its public
// name, or the own CA's DNS name. It is the relying-party ID of passkeys.
func (s *server) boxName() string {
	if s.publicReady() {
		return strings.ToLower(s.public.Name())
	}
	return passkey.LocalName
}

// atBoxName reports whether the request reached the box over HTTPS under the
// box name: the only place passkeys are created or used.
func (s *server) atBoxName(r *http.Request) bool {
	return r.TLS != nil && strings.EqualFold(hostOf(r), s.boxName()) && passkey.UsableName(s.boxName())
}

// boxURL is the address of a page under the box name.
func (s *server) boxURL(path string) string {
	host := s.boxName()
	if s.httpsPort != "443" {
		host += ":" + s.httpsPort
	}
	return "https://" + host + path
}

// relying is the relying party for a request that reached the box by its name.
func (s *server) relying(r *http.Request) (*webauthn.WebAuthn, error) {
	return passkey.New(s.boxName(), "https://"+strings.ToLower(r.Host))
}

// anyPasskey reports whether any parent of the box has a passkey; only then
// does the sign-in page over an IP address say why it offers none.
func (s *server) anyPasskey() bool {
	n, err := s.db.PasskeyCount()
	return err == nil && n > 0
}

// userOf is the person as the protocol sees them, with the credentials that
// work under the current box name. ok is false for someone without an account.
func (s *server) userOf(personID string) (user passkey.User, all []store.Passkey, ok bool) {
	h, err := s.db.HomeHousehold()
	if err != nil {
		return passkey.User{}, nil, false
	}
	person, err := s.db.Person(h.ID, personID)
	if err != nil || person.Role != store.RoleParent {
		return passkey.User{}, nil, false
	}
	acc, err := s.db.AccountByPerson(personID)
	if err != nil {
		return passkey.User{}, nil, false
	}
	all, err = s.db.Passkeys(personID)
	if err != nil {
		return passkey.User{}, nil, false
	}
	user = passkey.User{ID: personID, Name: acc.Username, DisplayName: person.Name}
	for _, pk := range all {
		if pk.RPID != s.boxName() {
			continue
		}
		var c webauthn.Credential
		if json.Unmarshal(pk.Credential, &c) == nil {
			user.Credentials = append(user.Credentials, c)
		}
	}
	return user, all, true
}

// passkeyWorks reports whether a passkey works under the current box name.
func (s *server) passkeyWorks(pk store.Passkey) bool { return pk.RPID == s.boxName() }

// ceremonyAnswer is what the box sends to start a ceremony: the ID to send
// back, and the options for the browser's credentials API.
type ceremonyAnswer struct {
	Ceremony string `json:"ceremony"`
	Options  any    `json:"options"`
}

// passkeyError is a refusal of a ceremony: the status and a short key the page
// script reads.
type passkeyError struct {
	Status int
	Key    string
}

func (e *passkeyError) Error() string { return e.Key }

var (
	errNotBoxName   = &passkeyError{http.StatusConflict, "name"}
	errBadCeremony  = &passkeyError{http.StatusBadRequest, "ceremony"}
	errNoAccount    = &passkeyError{http.StatusForbidden, "account"}
	errCreateFailed = &passkeyError{http.StatusUnprocessableEntity, "failed"}
)

func writePasskeyError(w http.ResponseWriter, err error) {
	var pe *passkeyError
	if errors.As(err, &pe) {
		writeJSON(w, pe.Status, map[string]string{"error": pe.Key})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
}

// beginRegistration starts creating a passkey for the person.
func (s *server) beginRegistration(r *http.Request, personID string) (*ceremonyAnswer, error) {
	if !s.atBoxName(r) {
		return nil, errNotBoxName
	}
	user, _, ok := s.userOf(personID)
	if !ok {
		return nil, errNoAccount
	}
	wa, err := s.relying(r)
	if err != nil {
		return nil, err
	}
	options, session, err := passkey.BeginRegistration(wa, user)
	if err != nil {
		return nil, err
	}
	return &ceremonyAnswer{Ceremony: s.ceremonies.Put(passkey.Register, personID, *session), Options: options}, nil
}

// finishRegistration checks the browser's answer and stores the passkey.
func (s *server) finishRegistration(r *http.Request, person store.Person, ceremony string, credential json.RawMessage, name string) (store.Passkey, error) {
	session, ok := s.ceremonies.Take(ceremony, passkey.Register, person.ID)
	if !ok {
		return store.Passkey{}, errBadCeremony
	}
	if !s.atBoxName(r) {
		return store.Passkey{}, errNotBoxName
	}
	user, _, ok := s.userOf(person.ID)
	if !ok {
		return store.Passkey{}, errNoAccount
	}
	wa, err := s.relying(r)
	if err != nil {
		return store.Passkey{}, err
	}
	cred, err := passkey.FinishRegistration(wa, user, session, credential)
	if err != nil {
		s.logger.Info("passkey not created", "person", person.Name, "reason", err.Error())
		return store.Passkey{}, errCreateFailed
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return store.Passkey{}, err
	}
	name = cleanPasskeyName(name)
	device, _ := auth.DeviceFromUserAgent(r.UserAgent())
	if name == "" {
		name = s.passkeyNameSuggestion(s.langOf(r), person.Name, device)
	}
	return s.db.AddPasskey(store.Passkey{PersonID: person.ID, CredentialID: cred.ID, Credential: raw, Name: name, RPID: s.boxName(), Device: device, Created: s.now()})
}

// cleanPasskeyName trims a name and cuts it to a length the pages can hold.
func cleanPasskeyName(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	if utf8.RuneCountInString(name) > maxPasskeyName {
		name = string([]rune(name)[:maxPasskeyName])
	}
	return name
}

// passkeyNameSuggestion is the name a new passkey gets when the parent does not
// choose one: the device it is made on; a phone or tablet is the person's.
func (s *server) passkeyNameSuggestion(lang, personName, device string) string {
	if device == "" || device == auth.Unknown {
		return s.i18n.T(lang, "passkey.name.default")
	}
	label := s.i18n.T(lang, "device."+device)
	switch device {
	case "iphone", "ipad", "android_phone", "android_tablet":
		return s.i18n.Format(lang, i18n.Msg("passkey.name.of", "device", label, "name", personName))
	}
	return label
}

type registrationFinish struct {
	Ceremony   string          `json:"ceremony"`
	Credential json.RawMessage `json:"credential"`
	Name       string          `json:"name"`
}

func readJSON(r *http.Request, v any) bool {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v) == nil
}

// passkeySubject is the parent behind a browser session who may create passkeys:
// a signed-in parent, or the parent of the wizard whose one-time code is not
// confirmed yet. A paired phone's session never qualifies.
func (s *server) passkeySubject(w http.ResponseWriter, r *http.Request) (store.Person, bool) {
	p, ok := s.pendingPerson(r)
	if !ok || s.phoneSession(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return store.Person{}, false
	}
	if p.Role != store.RoleParent {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return store.Person{}, false
	}
	return p, true
}

// handlePasskeyRegisterBegin and handlePasskeyRegisterFinish are the two
// requests of the page script that creates a passkey.
func (s *server) handlePasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	p, ok := s.passkeySubject(w, r)
	if !ok {
		return
	}
	out, err := s.beginRegistration(r, p.ID)
	if err != nil {
		writePasskeyError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handlePasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	p, ok := s.passkeySubject(w, r)
	if !ok {
		return
	}
	var in registrationFinish
	if !readJSON(r, &in) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ceremony"})
		return
	}
	pk, err := s.finishRegistration(r, p, in.Ceremony, in.Credential, in.Name)
	if err != nil {
		writePasskeyError(w, err)
		return
	}
	s.logger.Info("passkey created", "person", p.Name, "box name", pk.RPID)
	// Setting up the one-time code is next in the wizard; otherwise the list.
	to := "/account"
	if acc, err := s.db.AccountByPerson(p.ID); err == nil && !acc.TOTPConfirmed {
		to = "/setup/totp"
		s.setFlash(w, r, "flash.passkey_created_setup:"+flashNamePrefix+b64Name(pk.Name))
	} else {
		s.setFlash(w, r, "flash.passkey_created:"+flashNamePrefix+b64Name(pk.Name))
	}
	writeJSON(w, http.StatusOK, map[string]any{"redirect": to, "id": pk.ID, "name": pk.Name})
}

// handleLoginPasskeyBegin starts a sign-in with a passkey: the browser offers
// the passkeys it holds for the box name.
func (s *server) handleLoginPasskeyBegin(w http.ResponseWriter, r *http.Request) {
	if !s.db.Configured() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "setup"})
		return
	}
	if !s.atBoxName(r) {
		writePasskeyError(w, errNotBoxName)
		return
	}
	wa, err := s.relying(r)
	if err != nil {
		s.fail(w, "passkey sign-in", err)
		return
	}
	options, session, err := passkey.BeginSignIn(wa)
	if err != nil {
		s.fail(w, "passkey sign-in", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, ceremonyAnswer{Ceremony: s.ceremonies.Put(passkey.SignIn, "", *session), Options: options})
}

func passkeyClientKey(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	return "passkey|" + ip
}

// handleLoginPasskeyFinish checks the passkey and signs the parent in. A
// passkey counts as password and one-time code together (ADR 0018), so nothing
// else is asked. After a reset of one-time codes the session is the same as
// after a password: it reaches only the setup of new codes.
func (s *server) handleLoginPasskeyFinish(w http.ResponseWriter, r *http.Request) {
	var in struct {
		registrationFinish
		Next string `json:"next"`
	}
	if !readJSON(r, &in) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ceremony"})
		return
	}
	session, ok := s.ceremonies.Take(in.Ceremony, passkey.SignIn, "")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ceremony"})
		return
	}
	key := passkeyClientKey(r)
	if s.passkeyTries.blocked(key) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "limit"})
		return
	}
	if !s.atBoxName(r) {
		writePasskeyError(w, errNotBoxName)
		return
	}
	wa, err := s.relying(r)
	if err != nil {
		s.fail(w, "passkey sign-in", err)
		return
	}
	var used store.Passkey
	user, cred, err := passkey.FinishSignIn(wa, func(id, handle []byte) (passkey.User, error) {
		pk, err := s.db.PasskeyByCredential(id)
		if err != nil || string(handle) != pk.PersonID {
			return passkey.User{}, passkey.ErrUnknownCredential
		}
		if !s.passkeyWorks(pk) {
			return passkey.User{}, passkey.ErrWrongName
		}
		u, _, ok := s.userOf(pk.PersonID)
		if !ok {
			return passkey.User{}, passkey.ErrUnknownCredential
		}
		used = pk
		return u, nil
	}, session, in.Credential)
	if err != nil {
		s.passkeyTries.fail(key)
		reason := "passkey_failed"
		if errors.Is(err, passkey.ErrUnknownCredential) || errors.Is(err, passkey.ErrWrongName) {
			reason = "passkey_gone"
		}
		s.logger.Info("passkey sign-in refused", "reason", err.Error())
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": reason})
		return
	}
	s.passkeyTries.reset(key)
	raw, err := json.Marshal(cred)
	if err != nil {
		s.fail(w, "passkey sign-in", err)
		return
	}
	if err := s.db.UsePasskey(used.ID, raw, s.now()); err != nil {
		s.fail(w, "passkey sign-in", err)
		return
	}
	hash, err := s.startSessionHash(w, r, user.ID)
	if err != nil {
		s.fail(w, "passkey session", err)
		return
	}
	if err := s.db.SetSessionPasskey(hash, used.ID); err != nil {
		s.fail(w, "passkey session", err)
		return
	}
	acc, err := s.db.AccountByPerson(user.ID)
	if err != nil {
		s.fail(w, "passkey sign-in", err)
		return
	}
	if code := cookieLanguage(r); code != "" && acc.Language == "" {
		if err := s.db.SetAccountLanguage(acc.PersonID, code); err != nil {
			s.logger.Error("save account language", "err", err)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: passkeyUsedCookie, Value: "1", Path: "/", MaxAge: 365 * 24 * 3600, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
	to := localPath(in.Next)
	switch {
	case !acc.TOTPConfirmed:
		to = "/setup/totp" // after a reset, or before the first code was confirmed
	case to == "":
		to = "/"
	}
	s.logger.Info("signed in with a passkey", "account", acc.Username)
	writeJSON(w, http.StatusOK, map[string]string{"redirect": to})
}

// handlePasskeyAssertBegin starts the confirmation of an action (the reset of
// another parent's one-time codes) with one of the parent's own passkeys.
func (s *server) handlePasskeyAssertBegin(w http.ResponseWriter, r *http.Request) {
	p, ok := s.currentPerson(r)
	if !ok || s.phoneSession(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if p.Role != store.RoleParent {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	if !s.atBoxName(r) {
		writePasskeyError(w, errNotBoxName)
		return
	}
	user, _, ok := s.userOf(p.ID)
	if !ok || len(user.Credentials) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "no_passkey"})
		return
	}
	wa, err := s.relying(r)
	if err != nil {
		s.fail(w, "passkey confirmation", err)
		return
	}
	options, session, err := passkey.BeginConfirm(wa, user)
	if err != nil {
		s.fail(w, "passkey confirmation", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, ceremonyAnswer{Ceremony: s.ceremonies.Put(passkey.Confirm, p.ID, *session), Options: options})
}

// confirmWithPasskey checks the answer of a confirmation by the person's own
// passkey. The ceremony is spent either way.
func (s *server) confirmWithPasskey(r *http.Request, person store.Person, ceremony, assertion string) bool {
	session, ok := s.ceremonies.Take(ceremony, passkey.Confirm, person.ID)
	if !ok || !s.atBoxName(r) {
		return false
	}
	user, all, ok := s.userOf(person.ID)
	if !ok {
		return false
	}
	wa, err := s.relying(r)
	if err != nil {
		return false
	}
	cred, err := passkey.FinishConfirm(wa, user, session, []byte(assertion))
	if err != nil {
		s.logger.Info("passkey confirmation refused", "reason", err.Error())
		return false
	}
	for _, pk := range all {
		if string(pk.CredentialID) == string(cred.ID) {
			if raw, err := json.Marshal(cred); err == nil {
				_ = s.db.UsePasskey(pk.ID, raw, s.now())
			}
		}
	}
	return true
}

// passkeyJSON is a passkey as the REST API shows it.
type passkeyJSON struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt *string `json:"last_used_at"`
	// BoxName is the box name the passkey was created for; Works says whether
	// it is the box's name now.
	BoxName string `json:"box_name"`
	Works   bool   `json:"works"`
}

func (s *server) passkeyToJSON(pk store.Passkey) passkeyJSON {
	out := passkeyJSON{ID: pk.ID, Name: pk.Name, CreatedAt: pk.Created.UTC().Format("2006-01-02T15:04:05Z"), BoxName: pk.RPID, Works: s.passkeyWorks(pk)}
	if !pk.LastUsed.IsZero() {
		t := pk.LastUsed.UTC().Format("2006-01-02T15:04:05Z")
		out.LastUsedAt = &t
	}
	return out
}

func (s *server) apiListPasskeys(w http.ResponseWriter, _ *http.Request, p principal) {
	list, err := s.db.Passkeys(p.person.ID)
	if err != nil {
		s.fail(w, "passkeys", err)
		return
	}
	out := make([]passkeyJSON, 0, len(list))
	for _, pk := range list {
		out = append(out, s.passkeyToJSON(pk))
	}
	writeJSON(w, http.StatusOK, out)
}

// browserOnly refuses a token: a passkey signs in for good, so only a signed-in
// browser may create one (a stolen token must not be able to).
func browserOnly(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != "" {
		writeError(w, http.StatusForbidden, "passkeys are created in a signed-in browser, not with a token")
		return false
	}
	return true
}

func (s *server) apiPasskeyBegin(w http.ResponseWriter, r *http.Request, p principal) {
	if !browserOnly(w, r) {
		return
	}
	out, err := s.beginRegistration(r, p.person.ID)
	if err != nil {
		writePasskeyError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiPasskeyCreate(w http.ResponseWriter, r *http.Request, p principal) {
	if !browserOnly(w, r) {
		return
	}
	var in registrationFinish
	if !readJSON(r, &in) {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	pk, err := s.finishRegistration(r, p.person, in.Ceremony, in.Credential, in.Name)
	if err != nil {
		writePasskeyError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.passkeyToJSON(pk))
}

func (s *server) apiRenamePasskey(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(r, &in) {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	name := cleanPasskeyName(in.Name)
	if name == "" {
		writeFieldError(w, http.StatusUnprocessableEntity, "name", "a name is required")
		return
	}
	switch err := s.db.RenamePasskey(p.person.ID, r.PathValue("id"), name); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "passkey not found")
	case err != nil:
		s.fail(w, "rename passkey", err)
	default:
		pk, err := s.db.Passkey(p.person.ID, r.PathValue("id"))
		if err != nil {
			s.fail(w, "passkey", err)
			return
		}
		writeJSON(w, http.StatusOK, s.passkeyToJSON(pk))
	}
}

func (s *server) apiDeletePasskey(w http.ResponseWriter, r *http.Request, p principal) {
	switch err := s.db.RemovePasskey(p.person.ID, r.PathValue("id")); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "passkey not found")
	case err != nil:
		s.fail(w, "remove passkey", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// b64Name is a name as the flash cookie carries it (see redirectSavedNamed).
func b64Name(name string) string {
	if utf8.RuneCountInString(name) > maxFlashName {
		name = string([]rune(name)[:maxFlashName])
	}
	return base64.RawURLEncoding.EncodeToString([]byte(name))
}
