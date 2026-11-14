package server

import (
	"net/http"
	"strings"

	"github.com/hejmo-home/hejmo/internal/auth"
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
	if err != nil || !passwordOK || !acc.TOTPConfirmed || !auth.VerifyTOTP(acc.TOTPSecret, r.PostFormValue("code"), s.now()) {
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
	if next == "" {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
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
