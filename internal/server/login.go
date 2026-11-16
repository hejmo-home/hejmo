package server

import (
	"net/http"
	"strings"
	"sync"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/store"
)

// dummyHash keeps sign-in timing equal for unknown usernames. It is made on
// first use (New does that), with the cost new hashes have at that time.
var dummyHash = sync.OnceValue(func() string {
	h, _ := auth.HashPassword("hejmo-timing-equaliser")
	return h
})

func (s *server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if !s.db.Configured() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	data := map[string]any{"Next": localPath(r.URL.Query().Get("next"))}
	// A session cookie the box no longer knows (ended by a parent or run out):
	// say why the sign-in page is shown, and drop the cookie.
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if _, err := s.db.SessionPerson(auth.HashToken(c.Value), s.now()); err != nil {
			data["SignedOut"] = true
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
		}
	}
	s.render(w, r, http.StatusOK, "login.html", data)
}

func (s *server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.PostFormValue("username"))
	key := strings.ToLower(username)
	next := localPath(r.PostFormValue("next"))
	data := map[string]any{"Username": username, "Next": next}
	if s.limiter.blocked(key) {
		data["Error"] = "login.error.limit"
		s.render(w, r, http.StatusTooManyRequests, "login.html", data)
		return
	}
	acc, err := s.db.AccountByUsername(username)
	hash := acc.PasswordHash
	if err != nil {
		hash = dummyHash()
	}
	passwordOK := auth.VerifyPassword(hash, r.PostFormValue("password"))
	// Always run the second-factor check, whatever the password, so the time
	// it takes does not tell whether the password is right.
	secondOK := s.secondFactorOK(acc, r.PostFormValue("code"), err == nil && passwordOK)
	// After a reset (ADR 0017) the password alone leads on to setting up new
	// one-time codes; until they are confirmed the session reaches nothing else.
	// The same holds for a parent who accepted an invitation and stopped before
	// the one-time code (ticket 145): finished at the next sign-in.
	pendingSetup := err == nil && passwordOK && !acc.TOTPConfirmed && (acc.TOTPReset || s.db.Configured())
	if err != nil || !passwordOK || (!pendingSetup && !secondOK) {
		s.limiter.fail(key)
		// Deliberately one error at the password field: it must not reveal which of
		// username, password or code was wrong; the text names all three.
		data["Error"], data["ErrorField"] = "login.error.credentials", "password"
		s.render(w, r, http.StatusUnauthorized, "login.html", data)
		return
	}
	s.limiter.reset(key)
	if err := s.startSession(w, r, acc.PersonID); err != nil {
		s.fail(w, "login session", err)
		return
	}
	if code := cookieLanguage(r); code != "" && acc.Language == "" {
		// the choice made before sign-in becomes the account's
		if err := s.db.SetAccountLanguage(acc.PersonID, code); err != nil {
			s.logger.Error("save account language", "err", err)
		}
	}
	if pendingSetup {
		http.Redirect(w, r, "/setup/totp", http.StatusSeeOther)
		return
	}
	if next == "" {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// verifyRecoveryCode is a seam: tests count the verifications.
var verifyRecoveryCode = auth.VerifyRecoveryCode

// recoveryCodeSlots is how many recovery codes a set holds. A recovery-shaped
// code is always checked against this many hashes (dummies fill the rest).
const recoveryCodeSlots = 10

// secondFactorOK accepts a current one-time code or an unused recovery code
// of a confirmed account. A recovery code is spent by being accepted, but only
// if spend is set (the password was right too).
//
// A recovery-shaped code always costs recoveryCodeSlots verifies, whether the
// password is right, the account is unknown or unconfirmed, or fewer codes are
// left, so timing reveals nothing about any of them.
func (s *server) secondFactorOK(acc store.Account, code string, spend bool) bool {
	if acc.TOTPConfirmed && auth.VerifyTOTP(acc.TOTPSecret, code, s.now()) {
		return true
	}
	if !auth.LooksLikeRecoveryCode(code) {
		return false
	}
	var codes []store.RecoveryCode
	if acc.TOTPConfirmed {
		codes, _ = s.db.UnusedRecoveryCodes(acc.PersonID) // on error: only dummies
	}
	matched := ""
	for i := 0; i < max(recoveryCodeSlots, len(codes)); i++ { // check all, so timing does not tell which one matched
		hash, id := dummyHash(), ""
		if i < len(codes) {
			hash, id = codes[i].Hash, codes[i].ID
		}
		if verifyRecoveryCode(hash, code) && id != "" {
			matched = id
		}
	}
	return matched != "" && spend && s.db.SpendRecoveryCode(matched) == nil
}

// localPath returns p if it is a path on this box, else "". It keeps the
// sign-in page from sending people to other sites.
func localPath(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.Contains(p, `\`) {
		return ""
	}
	// Browsers drop tabs and newlines inside URLs, so "/\t/host" would become
	// "//host". Refuse every control character and space.
	for _, c := range p {
		if c <= ' ' || c == 0x7f {
			return ""
		}
	}
	return p
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.endSession(w, r)
	// Whoever signs out on the page of an invitation comes back to it (ticket 145).
	if next := localPath(r.PostFormValue("next")); strings.HasPrefix(next, "/invite/") {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
