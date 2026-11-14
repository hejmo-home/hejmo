package server

import (
	"net/http"
	"strings"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/store"
)

// dummyHash keeps sign-in timing equal for unknown usernames.
var dummyHash, _ = auth.HashPassword("hejmo-timing-equaliser")

func (s *server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if !s.db.Configured() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "login.html", map[string]any{"Next": localPath(r.URL.Query().Get("next"))})
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
		hash = dummyHash
	}
	passwordOK := auth.VerifyPassword(hash, r.PostFormValue("password"))
	// After a reset (ADR 0017) the password alone leads on to setting up new
	// one-time codes; until they are confirmed the session reaches nothing else.
	pendingSetup := err == nil && passwordOK && acc.TOTPReset && !acc.TOTPConfirmed
	if err != nil || !passwordOK || (!pendingSetup && !s.secondFactorOK(acc, r.PostFormValue("code"))) {
		s.limiter.fail(key)
		data["Error"] = "login.error.credentials"
		s.render(w, r, http.StatusUnauthorized, "login.html", data)
		return
	}
	s.limiter.reset(key)
	if err := s.startSession(w, r, acc.PersonID); err != nil {
		s.fail(w, "login session", err)
		return
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

// secondFactorOK accepts a current one-time code or an unused recovery code
// of a confirmed account. A recovery code is spent by being accepted.
func (s *server) secondFactorOK(acc store.Account, code string) bool {
	if !acc.TOTPConfirmed {
		return false
	}
	if auth.VerifyTOTP(acc.TOTPSecret, code, s.now()) {
		return true
	}
	if !auth.LooksLikeRecoveryCode(code) {
		return false
	}
	codes, err := s.db.UnusedRecoveryCodes(acc.PersonID)
	if err != nil {
		return false
	}
	matched := ""
	for _, c := range codes { // check all, so timing does not tell which one matched
		if auth.VerifyRecoveryCode(c.Hash, code) {
			matched = c.ID
		}
	}
	return matched != "" && s.db.SpendRecoveryCode(matched) == nil
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
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
