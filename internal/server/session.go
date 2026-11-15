package server

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/store"
)

const (
	sessionCookie   = "hejmo_session"
	sessionDuration = 30 * 24 * time.Hour
)

func (s *server) startSession(w http.ResponseWriter, r *http.Request, personID string) error {
	token := auth.NewToken()
	expires := s.now().Add(sessionDuration)
	if err := s.db.CreateSession(auth.HashToken(token), personID, auth.DeviceLabel(r.UserAgent()), s.now(), expires); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
	})
	return nil
}

func (s *server) endSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.db.DeleteSession(auth.HashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

// currentPerson resolves the session cookie to a household member. A person
// whose one-time codes were reset has a session only to set up new ones
// (pendingPerson); nothing else is reachable until that is confirmed. A paired
// phone's device session is exempt.
func (s *server) currentPerson(r *http.Request) (store.Person, bool) {
	p, ok := s.pendingPerson(r)
	if !ok {
		return store.Person{}, false
	}
	if s.phoneSession(r) {
		return p, true
	}
	if acc, err := s.db.AccountByPerson(p.ID); err == nil && !acc.TOTPConfirmed {
		return store.Person{}, false
	}
	return p, true
}

// phoneSession reports whether the request carries a paired phone's device session.
func (s *server) phoneSession(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	return err == nil && s.db.IsPhoneSession(auth.HashToken(c.Value))
}

// pendingPerson resolves the session cookie even when the person still has to
// confirm their one-time codes.
func (s *server) pendingPerson(r *http.Request) (store.Person, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return store.Person{}, false
	}
	hash := auth.HashToken(c.Value)
	personID, err := s.db.SessionPerson(hash, s.now())
	if err != nil {
		return store.Person{}, false
	}
	s.db.TouchPhoneSession(hash, s.now())
	return s.member(personID)
}

func (s *server) member(personID string) (store.Person, bool) {
	h, err := s.db.HomeHousehold()
	if err != nil {
		return store.Person{}, false
	}
	p, err := s.db.Person(h.ID, personID)
	return p, err == nil
}

// sameOrigin blocks cross-site form posts (CSRF). Requests carrying an
// Authorization header (API tokens) are not exposed to CSRF and pass.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Authorization") != "" || originMatches(r) {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "cross-site request blocked", http.StatusForbidden)
	})
}

func originMatches(r *http.Request) bool {
	source := r.Header.Get("Origin")
	if source == "" {
		source = r.Header.Get("Referer")
	}
	u, err := url.Parse(source)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

// loginLimiter allows max failures per username within window.
type loginLimiter struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	now      func() time.Time
	failures map[string][]time.Time
}

func newLoginLimiter(max int, window time.Duration, now func() time.Time) *loginLimiter {
	return &loginLimiter{max: max, window: window, now: now, failures: map[string][]time.Time{}}
}

func (l *loginLimiter) blocked(user string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(user)) >= l.max
}

func (l *loginLimiter) fail(user string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[user] = append(l.recent(user), l.now())
}

func (l *loginLimiter) reset(user string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, user)
}

func (l *loginLimiter) recent(user string) []time.Time {
	cutoff := l.now().Add(-l.window)
	kept := l.failures[user][:0]
	for _, t := range l.failures[user] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.failures, user)
		return nil
	}
	l.failures[user] = kept
	return kept
}
