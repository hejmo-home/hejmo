package server

import (
	"net/http"
	"slices"
)

// langCookie remembers the interface language chosen in the language switcher
// on this browser. It is how the choice reaches pages before sign-in.
const langCookie = "hejmo_lang"

// language is one interface language the switcher offers. The own name and the
// flag file are not translated: a flag names a country, so each language is
// listed under its own name (ticket 134).
type language struct {
	Code, Name, Flag string
}

// languages lists the supported interface languages, in the order the menu
// shows them. Every code has a catalogue in internal/i18n/locales.
var languages = []language{
	{"de", "Deutsch", "de"},
	{"en", "English", "gb"},
}

func languageByCode(code string) (language, bool) {
	i := slices.IndexFunc(languages, func(l language) bool { return l.Code == code })
	if i < 0 {
		return language{}, false
	}
	return languages[i], true
}

// setLanguage fills the switcher of a frame: the active language and the others.
func (f *frame) setLanguage(lang string, r *http.Request) {
	cur, _ := languageByCode(lang)
	f.LangName, f.LangFlag = cur.Name, cur.Flag
	for _, l := range languages {
		if l.Code != cur.Code {
			f.Others = append(f.Others, l)
		}
	}
	// A page that was the answer to a post has no page of its own to come back
	// to; the switcher then leads to the start page.
	f.Next = "/"
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if p := localPath(r.URL.RequestURI()); p != "" {
			f.Next = p
		}
	}
}

// pageLanguage is the language of a web page: the signed-in person's own
// choice, else the one this browser remembers, else the browser's language,
// else German.
func (s *server) pageLanguage(r *http.Request) string {
	if p, ok := s.currentPerson(r); ok {
		if acc, err := s.db.AccountByPerson(p.ID); err == nil {
			if _, ok := languageByCode(acc.Language); ok {
				return acc.Language
			}
		}
	}
	if code := cookieLanguage(r); code != "" {
		return code
	}
	return s.i18n.Match(r.Header.Get("Accept-Language"))
}

// cookieLanguage is the language this browser remembers, or "".
func cookieLanguage(r *http.Request) string {
	if c, err := r.Cookie(langCookie); err == nil {
		if l, ok := languageByCode(c.Value); ok {
			return l.Code
		}
	}
	return ""
}

// handleLanguageSwitch applies a choice of the language switcher: this browser
// remembers it, and a signed-in account keeps it for every device. Displays and
// the children's phones are not touched by a parent's choice.
func (s *server) handleLanguageSwitch(w http.ResponseWriter, r *http.Request) {
	l, ok := languageByCode(r.PostFormValue("lang"))
	if !ok {
		http.Error(w, "unsupported language", http.StatusBadRequest)
		return
	}
	next := localPath(r.PostFormValue("next"))
	if next == "" {
		next = "/"
	}
	http.SetCookie(w, &http.Cookie{
		Name: langCookie, Value: l.Code, Path: "/", MaxAge: 365 * 24 * 3600,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
	})
	key := "flash.lang.device"
	if p, ok := s.currentPerson(r); ok {
		if _, err := s.db.AccountByPerson(p.ID); err == nil { // a phone's device session has no account
			if err := s.db.SetAccountLanguage(p.ID, l.Code); err != nil {
				s.logger.Error("save account language", "err", err)
				key = "flash.lang.error"
			} else {
				key = "flash.lang.account"
			}
		}
	}
	s.redirectSaved(w, r, next, key)
}
