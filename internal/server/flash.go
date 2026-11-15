package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/task"
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

// flashTexts holds the text a success line names (an entry of a list) for the
// redirect that follows. The cookie carries only a random token, never the
// text itself, so a forged cookie shows nothing. A token works once.
type flashTexts struct {
	mu sync.Mutex
	m  map[string]flashText
}

type flashText struct {
	text  string
	until time.Time
}

const flashTextTTL = 2 * time.Minute

func (f *flashTexts) put(text string, now time.Time) string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	token := hex.EncodeToString(raw)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]flashText{}
	}
	for k, v := range f.m {
		if now.After(v.until) {
			delete(f.m, k)
		}
	}
	f.m[token] = flashText{text, now.Add(flashTextTTL)}
	return token
}

func (f *flashTexts) take(token string, now time.Time) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.m[token]
	delete(f.m, token)
	return v.text, ok && !now.After(v.until)
}

// redirectSavedText is redirectSaved for a success line that names text from
// the request, such as the entry that was added.
func (s *server) redirectSavedText(w http.ResponseWriter, r *http.Request, to, key, text string) {
	s.setFlash(w, r, key+":"+s.flashTexts.put(text, s.now()))
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// redirectSynced redirects to a page and has it show the result of the last
// sync run of one calendar: the cookie holds the calendar's id, and the line
// is built from what the box stored, so nothing from the request is shown.
func (s *server) redirectSynced(w http.ResponseWriter, r *http.Request, to, calendarID string) {
	s.redirectSaved(w, r, to, "flash.calendar_synced:"+calendarID)
}

var flashArg = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// takeFlash returns the success line waiting for this browser, as the key of
// its text and an optional argument, and clears it. Unknown values are
// ignored, so a forged cookie shows nothing.
func (s *server) takeFlash(w http.ResponseWriter, r *http.Request) (key, arg string) {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return "", ""
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
	key, arg, _ = strings.Cut(c.Value, ":")
	if !strings.HasPrefix(key, "flash.") || s.i18n.T(i18n.Default, key) == key || (arg != "" && !flashArg.MatchString(arg)) {
		return "", ""
	}
	return key, arg
}

// successText is the text of a success line. After "Sync now" it says when
// the calendar was updated and what the run did.
func (s *server) successText(lang, key, arg string) string {
	if strings.HasPrefix(key, "flash.entry_") && arg != "" {
		text, ok := s.flashTexts.take(arg, s.now())
		if !ok {
			return "" // the box restarted or the line is old: better none than a wrong one
		}
		return s.i18n.Format(lang, i18n.Msg(key, "text", text))
	}
	if key == "flash.calendar_synced" && arg != "" {
		if h, err := s.db.HomeHousehold(); err == nil {
			if c, err := s.db.SyncedCalendar(h.ID, arg); err == nil && !c.Sync.LastSuccess.IsZero() {
				p := principal{household: h}
				run := c.Sync.LastRun
				return s.i18n.Format(lang, i18n.Msg("flash.calendar_synced_run", "time", c.Sync.LastSuccess.In(p.location()).Format(p.clockLayout()),
					"events", strconv.Itoa(run.Events), "new", strconv.Itoa(run.New), "changed", strconv.Itoa(run.Changed), "removed", strconv.Itoa(run.Removed)))
			}
		}
	}
	if key == "flash.meals_saved" {
		// The line names the week that was saved; the cookie holds its Monday.
		monday := requestedWeek(arg, s.now().Format(task.DateLayout))
		return fmt.Sprintf(s.i18n.T(lang, key), isoWeek(monday))
	}
	return s.i18n.T(lang, key)
}
