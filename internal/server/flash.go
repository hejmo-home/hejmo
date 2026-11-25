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

	"github.com/hejmo-home/hejmo/internal/badge"
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

// peek returns the text of a token without using it up: a display asks for
// its view again and again while the line it shows stays.
func (f *flashTexts) peek(token string, now time.Time) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.m[token]
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

// flashFields names the placeholders of success lines that carry several
// values (the people, the routine and the template they are about). The values
// travel in the box's memory like the text of an entry, never in the cookie.
var flashFields = map[string][]string{
	"flash.routine_applied":        {"owner", "routine", "person", "tasks", "template"},
	"flash.routine_applied_own":    {"owner", "routine", "person", "tasks", "template", "origin"},
	"flash.pause_saved":            {"names", "range"},
	"flash.contact_added_for":      {"name", "sentence"},
	"flash.contacts_imported":      {"file", "summary"},
	"flash.recipe_imported":        {"name", "host"},
	"flash.rtpl_moved":             {"title", "place", "total"},
	"flash.routine_template_saved": {"name", "id"},
	"flash.rph_added_first.one":    {"n"},
	"flash.rph_added_first.other":  {"n"},
	"flash.rph_added_keep.one":     {"n"},
	"flash.rph_added_keep.other":   {"n"},
	"flash.rph_removed_cover":      {"name", "date"},
	"flash.rph_removed_cover_web":  {"date"},
	"flash.rph_removed_cover_anon": {"date"},
	"flash.photo_moved":            {"n", "album"},
	"flash.photos_moved":           {"n", "album"},
	"flash.photo_deleted":          {"n"},
	"flash.photos_deleted":         {"n"},
	"flash.goal_set":               {"name"},
	"flash.goal_switched":          {"name", "old"},
	"flash.goal_set_for":           {"possessive", "name"},
	"flash.goal_cleared_for":       {"name"},
	"flash.otp_reset":              {"name"},
	"flash.nd_on":                  {"church", "people", "total", "others"},
	"flash.nd_changed":             {"church", "dropped"},
	"flash.otp_reset_passkey":      {"name", "possessive", "passkeys"},
	"flash.otp_reset_passkeys":     {"name", "possessive", "passkeys"},
}

// redirectSavedFields is redirectSaved for a success line with several values,
// given in the order of flashFields.
func (s *server) redirectSavedFields(w http.ResponseWriter, r *http.Request, to, key string, values ...string) {
	s.setFlash(w, r, key+":"+s.flashTexts.put(strings.Join(values, "\x1f"), s.now()))
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// fieldValues takes the values of a success line that carries several values,
// by placeholder; false when the box restarted or the line is old.
func (s *server) fieldValues(key, token string) (map[string]string, bool) {
	text, ok := s.flashTexts.take(token, s.now())
	names := flashFields[key]
	values := strings.Split(text, "\x1f")
	if !ok || len(values) != len(names) {
		return nil, false
	}
	out := make(map[string]string, len(names))
	for i, n := range names {
		out[n] = values[i]
	}
	return out, true
}

// fieldsText is the text of a success line that carries several values; empty
// when the box restarted or the line is old: better none than a wrong one.
func (s *server) fieldsText(lang, key, token string) string {
	values, ok := s.fieldValues(key, token)
	if !ok {
		return ""
	}
	if church, ok := values["church"]; ok && strings.HasPrefix(key, "flash.nd_") { // the church travels as its list, the word is the reader's language
		values["church"] = s.i18n.T(lang, "nd.list."+church+".adj")
	}
	kv := make([]string, 0, 2*len(values))
	for _, n := range flashFields[key] {
		kv = append(kv, n, values[n])
	}
	return s.i18n.Format(lang, i18n.Msg(key, kv...))
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

// pendingFlash is the key and argument of the success line waiting for this
// browser, without using it up.
func (s *server) pendingFlash(r *http.Request) (key, arg string) {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return "", ""
	}
	key, arg, _ = strings.Cut(c.Value, ":")
	return key, arg
}

// pendingFlashKey is the key of the success line waiting for this browser,
// without using it up.
func (s *server) pendingFlashKey(r *http.Request) string {
	key, _ := s.pendingFlash(r)
	return key
}

// successText is the text of a success line. After "Sync now" it says when
// the calendar was updated and what the run did.
func (s *server) successText(lang, key, arg string) string {
	if key == "flash.setup_done" && arg != "" { // greet the parent by name
		if h, err := s.db.HomeHousehold(); err == nil {
			if p, err := s.db.Person(h.ID, arg); err == nil {
				return s.i18n.Format(lang, i18n.Msg("flash.setup_done_named", "name", p.Name))
			}
		}
	}
	if key == "flash.invite_done" && arg != "" { // greet the new parent by name
		if h, err := s.db.HomeHousehold(); err == nil {
			if p, err := s.db.Person(h.ID, arg); err == nil {
				return s.i18n.Format(lang, i18n.Msg("flash.invite_done_named", "name", p.Name))
			}
		}
	}
	if strings.HasPrefix(key, "flash.sub_") && arg != "" {
		return s.subFlashText(lang, key, arg) // "" when the box restarted: better none than a wrong line
	}
	if _, ok := flashFields[key]; ok && arg != "" {
		return s.fieldsText(lang, key, arg)
	}
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
					"events", strconv.Itoa(c.Sync.EventCount), "new", strconv.Itoa(run.New), "changed", strconv.Itoa(run.Changed), "removed", strconv.Itoa(run.Removed)))
			}
		}
	}
	switch key {
	case "flash.home_saved": // names the place that was stored
		if h, err := s.db.HomeHousehold(); err == nil {
			return s.i18n.Format(lang, i18n.Msg(key, "place", h.HomeName))
		}
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
	if key == "flash.badge_won" { // the first badge a tick earned, named in bold (ticket 295)
		if k := badge.Key(arg); k.Valid() {
			return formatHTML(s.i18n, lang, key, "name", s.badgeName(lang, k))
		}
		return "" // a forged value shows nothing
	}
	if _, ok := flashFields[key]; ok && arg != "" && strings.HasPrefix(key, "flash.goal_") || key == "flash.contacts_imported" {
		// The savings goal lines put the name in bold: the values are escaped, the text is ours.
		values, ok := s.fieldValues(key, arg)
		if !ok {
			return "" // the box restarted or the line is old: better none than a wrong one
		}
		kv := make([]string, 0, 2*len(values))
		for _, n := range flashFields[key] {
			kv = append(kv, n, values[n])
		}
		return formatHTML(s.i18n, lang, key, kv...)
	}
	if key == "flash.task_created" && arg != "" {
		if line, ok := s.taskCreatedHTML(lang, arg); ok {
			return template.HTML(line)
		}
		return template.HTML(html.EscapeString(s.i18n.T(lang, "flash.task_added")))
	}
	if key == "flash.routine_template_saved" && arg != "" {
		return s.templateSavedHTML(lang, arg)
	}
	if _, ok := flashFields[key]; ok && strings.HasPrefix(key, "flash.rwd_") && arg != "" {
		return s.rwdFlashHTML(lang, key, arg) // the values escaped, the bold from the locale (ticket 292)
	}
	if strings.HasPrefix(key, "flash.rwd_below_") {
		return template.HTML(s.i18n.T(lang, key))
	}
	if strings.HasPrefix(key, starsSavedPrefix) {
		text := s.i18n.T(lang, key)
		if n, err := strconv.Atoi(arg); err == nil && key == starsSavedPrefix+"other" {
			text = fmt.Sprintf(text, n)
		}
		return template.HTML(text)
	}
	return template.HTML(html.EscapeString(s.successText(lang, key, arg)))
}
