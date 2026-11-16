package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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

// redirectSavedNamed is redirectSaved for a success line that names the thing
// it is about ("Küche" unpaired): the name travels as base64 text and is shown
// only as plain text, escaped by the template, in the one language of the page.
func (s *server) redirectSavedNamed(w http.ResponseWriter, r *http.Request, to, key, name string) {
	if utf8.RuneCountInString(name) > maxFlashName {
		name = string([]rune(name)[:maxFlashName])
	}
	s.setFlash(w, r, key+":"+flashNamePrefix+base64.RawURLEncoding.EncodeToString([]byte(name)))
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// starsSavedPrefix marks the success lines after a changed star value; their
// argument is the new value.
const starsSavedPrefix = "tasks.stars.saved."

const (
	flashNamePrefix = "n~"
	maxFlashName    = 80
)

var (
	flashArg     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	flashNameArg = regexp.MustCompile(`^n~[A-Za-z0-9_-]{1,` + strconv.Itoa(8*maxFlashName) + `}$`)
)

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
	if !(strings.HasPrefix(key, "flash.") || strings.HasPrefix(key, starsSavedPrefix)) || s.i18n.T(i18n.Default, key) == key || (arg != "" && !flashArg.MatchString(arg) && !flashNameArg.MatchString(arg)) {
		return "", ""
	}
	if strings.HasPrefix(key, starsSavedPrefix) {
		// These lines always carry the new value, and it must be a number.
		if _, err := strconv.Atoi(arg); err != nil {
			return "", ""
		}
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
	if key == flashSessionEnded {
		if text := s.sessionEndedText(lang, arg); text != "" {
			return text
		}
		return s.i18n.T(lang, flashSessionEnded)
	}
	if name, ok := strings.CutPrefix(arg, flashNamePrefix); ok {
		raw, err := base64.RawURLEncoding.DecodeString(name)
		if err != nil || !utf8.Valid(raw) {
			return s.i18n.T(lang, key)
		}
		return s.i18n.Format(lang, i18n.Msg(key, "name", string(raw)))
	}
	if key == "flash.calendar_synced" && arg != "" {
		if h, err := s.db.HomeHousehold(); err == nil {
			if c, err := s.db.SyncedCalendar(h.ID, arg); err == nil && !c.Sync.LastSuccess.IsZero() {
				p := principal{household: h}
				run := c.Sync.LastRun
				return s.i18n.Format(lang, i18n.Msg("flash.calendar_synced_run", "time", p.clockSeconds(c.Sync.LastSuccess),
					"events", strconv.Itoa(run.Events), "new", strconv.Itoa(run.New), "changed", strconv.Itoa(run.Changed), "removed", strconv.Itoa(run.Removed)))
			}
		}
	}
	switch key {
	case "flash.routine_moved":
		if arg != "" {
			return s.routineMovedText(lang, arg)
		}
	case "flash.note_saved":
		if arg != "" {
			return s.noteSavedText(lang, arg)
		}
	case "flash.waste_on":
		if arg != "" {
			return s.wasteOnText(lang, arg)
		}
	case "flash.template_saved":
		if arg != "" {
			return s.templateSavedText(lang, arg)
		}
	case "flash.meal_restored":
		if arg != "" {
			return s.restoredText(lang, arg)
		}
	case "flash.meals_saved":
		// The line names the week that was saved; the cookie holds its Monday.
		monday := requestedWeek(arg, s.now().Format(task.DateLayout))
		return fmt.Sprintf(s.i18n.T(lang, key), isoWeek(monday))
	}
	return s.i18n.T(lang, key)
}

// successHTML is the success line as markup: the text of successText escaped,
// except the lines after a changed star value, which put the new value in bold.
func (s *server) successHTML(lang, key, arg string) template.HTML {
	if strings.HasPrefix(key, starsSavedPrefix) {
		text := s.i18n.T(lang, key)
		if n, err := strconv.Atoi(arg); err == nil && key == starsSavedPrefix+"other" {
			text = fmt.Sprintf(text, n)
		}
		return template.HTML(text)
	}
	return template.HTML(html.EscapeString(s.successText(lang, key, arg)))
}
