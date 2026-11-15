package server

import (
	"net/http"
	"strings"

	"github.com/hejmo-home/hejmo/internal/i18n"
)

// flashCookie carries the success line across the redirect that follows a
// saved change (post/redirect/get). It holds only the key of a text in the
// locales, never text from the request, and is cleared when the line is shown.
const flashCookie = "hejmo_flash"

// redirectSaved redirects to a page and has it show the success line key.
func (s *server) redirectSaved(w http.ResponseWriter, r *http.Request, to, key string) {
	s.setFlash(w, r, key)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// setFlash has the next page this browser opens show the success line key.
func (s *server) setFlash(w http.ResponseWriter, r *http.Request, key string) {
	http.SetCookie(w, &http.Cookie{
		Name: flashCookie, Value: key, Path: "/", MaxAge: 120,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
	})
}

// takeFlash returns the success line key waiting for this browser and clears
// it. Unknown values are ignored, so a forged cookie shows nothing.
func (s *server) takeFlash(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
	if !strings.HasPrefix(c.Value, "flash.") || s.i18n.T(i18n.Default, c.Value) == c.Value {
		return ""
	}
	return c.Value
}
