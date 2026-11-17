package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/brightness"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/kiosk"
	"github.com/hejmo-home/hejmo/internal/quiet"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/theme"
)

// renderDisplays shows the overview of paired displays and the pairing form.
func (s *server) renderDisplays(w http.ResponseWriter, r *http.Request, p principal, status int, form map[string]any) {
	list, err := s.db.Displays(p.household.ID)
	if err != nil {
		s.fail(w, "displays", err)
		return
	}
	var rows []rowView
	for _, d := range list {
		row := rowView{Href: "/displays/" + d.ID, Title: d.Name, Lines: []rowLine{{Text: s.say(r, i18n.Msg("displays.type."+string(d.Type)))}}}
		switch {
		case d.LastSeen.IsZero():
			row.Lines = append(row.Lines, rowLine{Text: s.say(r, i18n.Msg("displays.never"))})
		case s.now().Sub(d.LastSeen) > silentAfter:
			row.Flag = s.silentFlag(r, p, d)
		default:
			row.Lines = append(row.Lines, rowLine{Text: s.say(r, i18n.Msg("displays.last_seen_at", "when", p.when(d.LastSeen)))})
		}
		rows = append(rows, row)
	}
	data := map[string]any{
		"Person": p.person, "Rows": rows, "SyncStale": s.syncStale(p.household.ID), "Form": form,
		"Types": []store.DisplayType{store.DisplayEInkBW, store.DisplayEInkColor, store.DisplayScreen},
	}
	data["Error"], data["ErrorField"] = form["Error"], form["ErrorField"] // form is never nil
	s.render(w, r, status, "displays.html", data)
}

// silentFlag warns that a display has not been in contact for a while.
func (s *server) silentFlag(r *http.Request, p principal, d store.Display) *flagView {
	return &flagView{Kind: "warn", Text: s.say(r, i18n.Msg("displays.flag.silent", "when", p.when(d.LastSeen)))}
}

// displayPage is what the detail page of one display shows. The editable
// values come from what was submitted when the page is shown again after an
// error, else from what is stored; offsets are text so a wrong entry is kept.
type displayPage struct {
	ID, Name, Type, Theme     string
	ShowTomorrow, QuietOn     bool
	QuietFrom, QuietTo        string
	KioskURL, SunRise, SunSet string
	KioskKeySet               bool
	Types                     []store.DisplayType
	Themes                    []store.Theme
	// The screensaver block (ticket 178).
	SaverOn                              bool
	SaverIdle                            int
	SaverIdleText, SaverIntervalText     string
	SaverOnText, SaverSummary, QuietNote string
	AllAlbums, NoPhotos, EInk            bool
	Albums                               []saverAlbum
}

// saverAlbum is one album chip of the screensaver block.
type saverAlbum struct {
	ID, Name, Count string
	Checked         bool
}

func newDisplayPage(d store.Display, in url.Values) displayPage {
	v := displayPage{ID: d.ID, Name: d.Name, Type: string(d.Type), Theme: string(d.Theme), ShowTomorrow: d.ShowTomorrow, QuietOn: d.Quiet.Enabled,
		QuietFrom: d.Quiet.FromText(), QuietTo: d.Quiet.ToText(), KioskURL: d.KioskURL, KioskKeySet: d.KioskKeySet,
		SunRise: strconv.Itoa(d.SunriseOffset), SunSet: strconv.Itoa(d.SunsetOffset)}
	if in != nil {
		str := func(dst *string, key string) {
			if in.Has(key) {
				*dst = in.Get(key)
			}
		}
		str(&v.Name, "name")
		str(&v.Type, "type")
		str(&v.Theme, "theme")
		str(&v.QuietFrom, "quiet_from")
		str(&v.QuietTo, "quiet_to")
		str(&v.KioskURL, "kiosk_url")
		str(&v.SunRise, "sunrise_offset")
		str(&v.SunSet, "sunset_offset")
		v.ShowTomorrow, v.QuietOn = in.Get("show_tomorrow") != "", in.Get("quiet") != ""
	}
	v.Types = []store.DisplayType{store.DisplayEInkBW, store.DisplayEInkColor, store.DisplayScreen}
	v.Themes = []store.Theme{theme.Light, theme.Dark, theme.Auto}
	return v
}

// renderDisplay shows one display's detail page. in is the submitted form of a
// refused save (nil on a plain visit), errKey the text of the error and
// errField the input it belongs to.
func (s *server) renderDisplay(w http.ResponseWriter, r *http.Request, p principal, status int, d store.Display, in url.Values, errKey, errField string) {
	page := newDisplayPage(d, in)
	s.fillSaver(r, p, &page, d, in)
	facts := []factView{}
	if d.LastSeen.IsZero() {
		facts = append(facts, factView{Label: s.say(r, i18n.Msg("displays.facts.last_seen")), Value: s.say(r, i18n.Msg("displays.never"))})
	} else if s.now().Sub(d.LastSeen) > silentAfter {
		facts = append(facts, factView{Label: s.say(r, i18n.Msg("displays.facts.last_seen")), Flag: s.silentFlag(r, p, d)})
	} else {
		facts = append(facts, factView{Label: s.say(r, i18n.Msg("displays.facts.last_seen")), Value: p.when(d.LastSeen)})
	}
	facts = append(facts, factView{Label: s.say(r, i18n.Msg("displays.facts.paired")), Value: p.day(d.PairedAt)})
	s.render(w, r, status, "display.html", map[string]any{
		"Person": p.person, "D": page, "Facts": facts, "Error": errKey, "ErrorField": errField,
		"Title": d.Name,
	})
}

func (s *server) handleDisplayPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	d, ok := s.displayOf(w, r, p.household.ID)
	if !ok {
		return
	}
	s.renderDisplay(w, r, p, http.StatusOK, d, nil, "", "")
}

func (s *server) handleDisplaysPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.renderDisplays(w, r, p, http.StatusOK, map[string]any{"Code": r.URL.Query().Get("code"), "Type": string(store.DisplayEInkBW)})
}

func (s *server) handleDisplayPair(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	code := strings.TrimSpace(r.PostFormValue("code"))
	in := store.Display{Name: r.PostFormValue("name"), Type: store.DisplayType(r.PostFormValue("type"))}
	form := func(key, field string) map[string]any {
		return map[string]any{"Error": key, "ErrorField": field, "Code": code, "Name": in.Name, "Type": string(in.Type)}
	}
	var fieldErr *store.FieldError
	switch _, err := s.pairDisplay(p, code, in); {
	case err == nil:
		s.redirectSaved(w, r, "/displays", "flash.display_paired")
	case errors.Is(err, errTooManyAttempts):
		s.renderDisplays(w, r, p, http.StatusTooManyRequests, form("displays.error.too_many", "code"))
	case errors.Is(err, store.ErrNotFound):
		s.renderDisplays(w, r, p, http.StatusUnprocessableEntity, form("displays.error.code", "code"))
	case errors.As(err, &fieldErr):
		s.renderDisplays(w, r, p, http.StatusUnprocessableEntity, form("displays.error."+fieldErr.Field, fieldErr.Field))
	default:
		s.fail(w, "pair display", err)
	}
}

func (s *server) handleDisplayUnpair(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	d, ok := s.displayOf(w, r, p.household.ID)
	if !ok {
		return
	}
	switch err := s.db.RevokeDisplay(p.household.ID, d.ID, s.now()); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, "unpair display", err)
	default:
		s.redirectSavedNamed(w, r, "/displays", "flash.display_unpaired", d.Name)
	}
}

var (
	errQuietTimes = errors.New("quiet mode needs two different times")
	errKioskURL   = errors.New("the kiosk address must be an http or https address")
	errTheme      = errors.New("theme must be light, dark or auto and the offsets between -180 and 180 minutes")
)

// displayChange is a change of a display's settings; nil fields stay as they are.
type displayChange struct {
	// Name and Type are where it hangs and what kind it is (ticket 111).
	Name, Type         *string
	ShowTomorrow       *bool
	QuietEnabled       *bool
	QuietFrom, QuietTo *string
	KioskURL, KioskKey *string // a nil key keeps the stored one, an empty address removes the kiosk
	// Theme and the sun offsets (ADR 0015).
	Theme                       *string
	SunriseOffset, SunsetOffset *int
	// Saver is the photo screensaver (ticket 178).
	Saver *saverChange
	// Brightness is what the kiosk app sets by time of day and in quiet mode (ticket 179).
	Brightness *brightChange
}

// brightChange is a change of a display's brightness values; nil fields stay as they are.
type brightChange struct {
	Day, Evening, QuietLevel, WakeMinutes *int
	QuietMode, Source                     *string
}

// apply returns the brightness values after the change, or a *store.FieldError naming the wrong one.
func (c *brightChange) apply(cur brightness.Settings) (brightness.Settings, error) {
	if c == nil {
		return cur, nil
	}
	if c.Day != nil {
		cur.Day = *c.Day
	}
	if c.Evening != nil {
		cur.Evening = *c.Evening
	}
	if c.QuietLevel != nil {
		cur.QuietLevel = *c.QuietLevel
	}
	if c.WakeMinutes != nil {
		cur.WakeMinutes = *c.WakeMinutes
	}
	if c.QuietMode != nil {
		cur.QuietMode = brightness.QuietMode(*c.QuietMode)
	}
	if c.Source != nil {
		cur.Source = brightness.Source(*c.Source)
	}
	var refused *brightness.Refused
	if err := cur.Validate(); errors.As(err, &refused) {
		return cur, &store.FieldError{Field: refused.Field, Msg: refused.Msg}
	}
	return cur, nil
}

// saverChange is a change of a display's screensaver; nil fields stay as they are.
type saverChange struct {
	Enabled               *bool
	IdleMinutes, Interval *int
	Albums                *[]string
}

// apply returns the screensaver setting after the change, or the field that is wrong.
func (c *saverChange) apply(cur store.Saver) (store.Saver, error) {
	if c == nil {
		return cur, nil
	}
	if c.Enabled != nil {
		cur.Enabled = *c.Enabled
	}
	if c.IdleMinutes != nil {
		cur.IdleMinutes = *c.IdleMinutes
	}
	if c.Interval != nil {
		cur.IntervalMinutes = *c.Interval
	}
	if c.Albums != nil {
		cur.Albums = *c.Albums
	}
	switch {
	case cur.IdleMinutes < 1 || cur.IdleMinutes > 240:
		return cur, &store.FieldError{Field: "saver_idle", Msg: "the screensaver starts after 1 to 240 minutes without a touch"}
	case cur.IntervalMinutes < 1 || cur.IntervalMinutes > 240:
		return cur, &store.FieldError{Field: "saver_interval", Msg: "a photo stays 1 to 240 minutes"}
	}
	return cur, nil
}

// changeDisplay validates and saves a change, for the web interface and the API alike.
func (s *server) changeDisplay(householdID, id string, c displayChange) error {
	cur, err := s.db.Display(householdID, id)
	if err != nil {
		return err
	}
	name, typ := cur.Name, cur.Type
	if c.Name != nil {
		name = *c.Name
	}
	if c.Type != nil {
		typ = store.DisplayType(*c.Type)
	}
	sched := cur.Quiet
	if c.QuietEnabled != nil || c.QuietFrom != nil || c.QuietTo != nil {
		enabled, from, to := sched.Enabled, sched.FromText(), sched.ToText()
		if c.QuietEnabled != nil {
			enabled = *c.QuietEnabled
		}
		if c.QuietFrom != nil {
			from = *c.QuietFrom
		}
		if c.QuietTo != nil {
			to = *c.QuietTo
		}
		if sched, err = quiet.Parse(enabled, from, to); err != nil {
			return errQuietTimes
		}
	}
	if c.KioskURL != nil && *c.KioskURL != "" && !kiosk.ValidURL(*c.KioskURL) {
		return errKioskURL
	}
	th, rise, set := cur.Theme, cur.SunriseOffset, cur.SunsetOffset
	if c.Theme != nil || c.SunriseOffset != nil || c.SunsetOffset != nil {
		if c.Theme != nil {
			th = store.Theme(*c.Theme)
		}
		if c.SunriseOffset != nil {
			rise = *c.SunriseOffset
		}
		if c.SunsetOffset != nil {
			set = *c.SunsetOffset
		}
		if !th.Valid() || !theme.ValidOffset(rise) || !theme.ValidOffset(set) {
			return errTheme
		}
	}
	saver, err := c.Saver.apply(cur.Saver)
	if err != nil {
		return err
	}
	bright, err := c.Brightness.apply(cur.Brightness)
	if err != nil {
		return err
	}
	if name != cur.Name || typ != cur.Type { // the store trims and validates both
		if err := s.db.SetDisplayIdentity(householdID, id, name, typ); err != nil {
			return err
		}
	}
	tomorrow := cur.ShowTomorrow
	if c.ShowTomorrow != nil {
		tomorrow = *c.ShowTomorrow
	}
	if tomorrow != cur.ShowTomorrow || th != cur.Theme || rise != cur.SunriseOffset || set != cur.SunsetOffset {
		if err := s.db.SetDisplayLook(householdID, id, store.DisplayLook{ShowTomorrow: tomorrow, Theme: th, SunriseOffset: rise, SunsetOffset: set}); err != nil {
			return err
		}
	}
	if sched != cur.Quiet {
		if err := s.db.SetDisplayQuiet(householdID, id, sched); err != nil {
			return err
		}
	}
	if !sameSaver(saver, cur.Saver) {
		if err := s.db.SetDisplaySaver(householdID, id, saver); err != nil {
			if errors.Is(err, store.ErrInvalid) && c.Saver != nil && c.Saver.Albums != nil {
				return &store.FieldError{Field: "saver_albums", Msg: "an album of the screensaver is unknown"}
			}
			return err
		}
	}
	if bright != cur.Brightness {
		if err := s.db.SetDisplayBrightness(householdID, id, bright); err != nil {
			return err
		}
	}
	if c.KioskURL != nil {
		key := c.KioskKey
		if key != nil && *key == "" {
			key = nil // an empty field keeps the key; removing the kiosk removes it
		}
		if err := s.db.SetDisplayKiosk(householdID, id, *c.KioskURL, key); err != nil {
			return err
		}
	}
	return nil
}

func sameSaver(a, b store.Saver) bool {
	if a.Enabled != b.Enabled || a.IdleMinutes != b.IdleMinutes || a.IntervalMinutes != b.IntervalMinutes || len(a.Albums) != len(b.Albums) {
		return false
	}
	for i := range a.Albums {
		if a.Albums[i] != b.Albums[i] {
			return false
		}
	}
	return true
}

// optional returns the form field's value, or nil when the form has no such field.
func optional(r *http.Request, name string) *string {
	if _, ok := r.PostForm[name]; !ok {
		return nil
	}
	v := strings.TrimSpace(r.PostForm.Get(name))
	return &v
}

// handleDisplaySettings saves the settings on a display's detail page: where
// it hangs and what type it is, the tomorrow block (ADR 0016), the theme, quiet
// mode and the kiosk app (ADR 0015). An unticked checkbox sends nothing, so
// absent means off; absent texts stay as they are.
func (s *server) handleDisplaySettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	d, ok := s.displayOf(w, r, p.household.ID)
	if !ok {
		return
	}
	refuse := func(key, field string) {
		s.renderDisplay(w, r, p, http.StatusUnprocessableEntity, d, r.PostForm, key, field)
	}
	c := displayChange{Name: optional(r, "name"), Type: optional(r, "type")}
	// The theme and the sun offsets are saved on every display type (ADR 0015); each is read when it is posted.
	if th := r.PostForm.Get("theme"); th != "" {
		c.Theme = &th
	}
	for _, o := range []struct {
		field string
		into  **int
	}{{"sunrise_offset", &c.SunriseOffset}, {"sunset_offset", &c.SunsetOffset}} {
		if _, posted := r.PostForm[o.field]; !posted {
			continue
		}
		v, err := strconv.Atoi(strings.TrimSpace(r.PostForm.Get(o.field)))
		if err != nil || !theme.ValidOffset(v) {
			refuse("displays.error.offset", o.field)
			return
		}
		*o.into = &v
	}
	on := func(name string) *bool { v := r.PostForm.Get(name) != ""; return &v }
	c.ShowTomorrow, c.QuietEnabled = on("show_tomorrow"), on("quiet")
	c.QuietFrom, c.QuietTo = optional(r, "quiet_from"), optional(r, "quiet_to")
	c.KioskURL, c.KioskKey = optional(r, "kiosk_url"), optional(r, "kiosk_key")
	if _, posted := r.PostForm["saver_idle"]; posted { // the whole block is in the form
		c.Saver = &saverChange{Enabled: on("saver")}
		for _, f := range []struct {
			field string
			into  **int
		}{{"saver_idle", &c.Saver.IdleMinutes}, {"saver_interval", &c.Saver.Interval}} {
			v, err := strconv.Atoi(strings.TrimSpace(r.PostForm.Get(f.field)))
			if err != nil {
				refuse("displays.error."+f.field, f.field)
				return
			}
			*f.into = &v
		}
		albums := []string{}
		if r.PostForm.Get("saver_all") == "" {
			albums = append(albums, r.PostForm["saver_album"]...)
		}
		c.Saver.Albums = &albums
	}
	bc := &brightChange{QuietMode: optional(r, "quiet_light"), Source: optional(r, "light_source")}
	for _, f := range []struct {
		field string
		into  **int
	}{{"bright_day", &bc.Day}, {"bright_evening", &bc.Evening}, {"bright_quiet", &bc.QuietLevel}, {"wake_minutes", &bc.WakeMinutes}} {
		if _, posted := r.PostForm[f.field]; !posted {
			continue
		}
		v, err := strconv.Atoi(strings.TrimSpace(r.PostForm.Get(f.field)))
		if err != nil {
			refuse("displays.error."+f.field, f.field)
			return
		}
		*f.into = &v
	}
	c.Brightness = bc
	var fieldErr *store.FieldError
	switch err := s.changeDisplay(p.household.ID, d.ID, c); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.As(err, &fieldErr):
		refuse("displays.error."+fieldErr.Field, fieldErr.Field)
	case errors.Is(err, errQuietTimes):
		refuse("displays.error.quiet", "quiet_to")
	case errors.Is(err, errTheme):
		refuse("displays.error.theme", "theme")
	case errors.Is(err, errKioskURL):
		refuse("displays.error.kiosk", "kiosk_url")
	case err != nil:
		s.fail(w, "display settings", err)
	default:
		s.redirectSaved(w, r, "/displays/"+d.ID, "flash.saved")
	}
}

type displayJSON struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Room          string    `json:"room"`
	Type          string    `json:"type"`
	PairedAt      string    `json:"paired_at"`
	LastSeenAt    *string   `json:"last_seen_at"`
	ShowTomorrow  bool      `json:"show_tomorrow"`
	QuietMode     quietJSON `json:"quiet_mode"`
	Kiosk         kioskJSON `json:"kiosk"`
	Theme         string    `json:"theme"`
	SunriseOffset int       `json:"sunrise_offset"`
	SunsetOffset  int       `json:"sunset_offset"`
	Screensaver   saverJSON `json:"screensaver"`
}

type saverJSON struct {
	Enabled         bool     `json:"enabled"`
	IdleMinutes     int      `json:"idle_minutes"`
	IntervalMinutes int      `json:"interval_minutes"`
	AlbumIDs        []string `json:"album_ids"`
}

type quietJSON struct {
	Enabled bool   `json:"enabled"`
	From    string `json:"from"`
	To      string `json:"to"`
}

// kioskJSON never carries the API key, only whether one is stored.
type kioskJSON struct {
	URL       string `json:"url"`
	APIKeySet bool   `json:"api_key_set"`
}

func displayToJSON(d store.Display, loc *time.Location) displayJSON {
	j := displayJSON{ID: d.ID, Name: d.Name, Room: d.Room, Type: string(d.Type), PairedAt: d.PairedAt.In(loc).Format(time.RFC3339), ShowTomorrow: d.ShowTomorrow,
		QuietMode: quietJSON{d.Quiet.Enabled, d.Quiet.FromText(), d.Quiet.ToText()}, Kiosk: kioskJSON{d.KioskURL, d.KioskKeySet},
		Theme: string(d.Theme), SunriseOffset: d.SunriseOffset, SunsetOffset: d.SunsetOffset,
		Screensaver: saverJSON{d.Saver.Enabled, d.Saver.IdleMinutes, d.Saver.IntervalMinutes, append([]string{}, d.Saver.Albums...)}}
	if !d.LastSeen.IsZero() {
		seen := d.LastSeen.In(loc).Format(time.RFC3339)
		j.LastSeenAt = &seen
	}
	return j
}

func (s *server) apiListDisplays(w http.ResponseWriter, _ *http.Request, p principal) {
	list, err := s.db.Displays(p.household.ID)
	if err != nil {
		s.fail(w, "list displays", err)
		return
	}
	loc := p.location()
	out := make([]displayJSON, 0, len(list))
	for _, d := range list {
		out = append(out, displayToJSON(d, loc))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiPairDisplay(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct{ Code, Name, Room, Type string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	d, err := s.pairDisplay(p, strings.TrimSpace(in.Code), store.Display{Name: in.Name, Room: in.Room, Type: store.DisplayType(in.Type)})
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, displayToJSON(d, p.location()))
	case errors.Is(err, errTooManyAttempts):
		writeError(w, http.StatusTooManyRequests, "too many wrong codes, try again later")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no display is waiting with this code, or the code has expired")
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, "a display needs a name and a type of eink_bw, eink_color or screen")
	default:
		s.fail(w, "pair display", err)
	}
}

func (s *server) apiUpdateDisplay(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Name         *string `json:"name"`
		Type         *string `json:"type"`
		ShowTomorrow *bool   `json:"show_tomorrow"`
		QuietMode    *struct {
			Enabled *bool   `json:"enabled"`
			From    *string `json:"from"`
			To      *string `json:"to"`
		} `json:"quiet_mode"`
		Theme         *string `json:"theme"`
		SunriseOffset *int    `json:"sunrise_offset"`
		SunsetOffset  *int    `json:"sunset_offset"`
		Screensaver   *struct {
			Enabled         *bool     `json:"enabled"`
			IdleMinutes     *int      `json:"idle_minutes"`
			IntervalMinutes *int      `json:"interval_minutes"`
			AlbumIDs        *[]string `json:"album_ids"`
		} `json:"screensaver"`
		Kiosk *struct {
			URL    *string `json:"url"`
			APIKey *string `json:"api_key"`
		} `json:"kiosk"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	c := displayChange{Name: in.Name, Type: in.Type, ShowTomorrow: in.ShowTomorrow, Theme: in.Theme, SunriseOffset: in.SunriseOffset, SunsetOffset: in.SunsetOffset}
	if q := in.QuietMode; q != nil {
		c.QuietEnabled, c.QuietFrom, c.QuietTo = q.Enabled, q.From, q.To
	}
	if k := in.Kiosk; k != nil {
		c.KioskURL, c.KioskKey = k.URL, k.APIKey
	}
	if sv := in.Screensaver; sv != nil {
		c.Saver = &saverChange{Enabled: sv.Enabled, IdleMinutes: sv.IdleMinutes, Interval: sv.IntervalMinutes, Albums: sv.AlbumIDs}
	}
	var fieldErr *store.FieldError
	switch err := s.changeDisplay(p.household.ID, r.PathValue("id"), c); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "display not found")
		return
	case errors.As(err, &fieldErr):
		writeError(w, http.StatusUnprocessableEntity, fieldErr.Msg)
		return
	case errors.Is(err, errQuietTimes):
		writeError(w, http.StatusUnprocessableEntity, "quiet mode needs two different times as hh:mm")
		return
	case errors.Is(err, errKioskURL):
		writeError(w, http.StatusUnprocessableEntity, "the kiosk address must start with http:// or https://")
		return
	case errors.Is(err, errTheme):
		writeError(w, http.StatusUnprocessableEntity, "theme must be light, dark or auto and the offsets between -180 and 180 minutes")
		return
	case err != nil:
		s.fail(w, "update display", err)
		return
	}
	list, err := s.db.Displays(p.household.ID)
	if err != nil {
		s.fail(w, "update display", err)
		return
	}
	for _, d := range list {
		if d.ID == r.PathValue("id") {
			writeJSON(w, http.StatusOK, displayToJSON(d, p.location()))
			return
		}
	}
	writeError(w, http.StatusNotFound, "display not found")
}

func (s *server) apiRevokeDisplay(w http.ResponseWriter, r *http.Request, p principal) {
	switch err := s.db.RevokeDisplay(p.household.ID, r.PathValue("id"), s.now()); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "display not found")
	case err != nil:
		s.fail(w, "revoke display", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// displayOf finds the household's paired display the request names. When it
// answers itself it returns false: 404 for an unknown display, 500 for a
// database error.
func (s *server) displayOf(w http.ResponseWriter, r *http.Request, householdID string) (store.Display, bool) {
	d, err := s.db.Display(householdID, r.PathValue("id"))
	switch {
	case err == nil:
		return d, true
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	default:
		s.fail(w, "display", err)
	}
	return store.Display{}, false
}

// fillSaver fills the screensaver block of a display's page: what is chosen
// now, or what was submitted when the page shows a refused save.
func (s *server) fillSaver(r *http.Request, p principal, v *displayPage, d store.Display, in url.Values) {
	lang := s.langOf(r)
	v.EInk = d.Type == store.DisplayEInkBW || d.Type == store.DisplayEInkColor
	v.SaverOn, v.SaverIdle = d.Saver.Enabled, d.Saver.IdleMinutes
	v.SaverIdleText, v.SaverIntervalText = strconv.Itoa(d.Saver.IdleMinutes), strconv.Itoa(d.Saver.IntervalMinutes)
	chosen := map[string]bool{}
	for _, a := range d.Saver.Albums {
		chosen[a] = true
	}
	v.AllAlbums = len(d.Saver.Albums) == 0
	if in != nil {
		v.SaverOn = in.Get("saver") != ""
		v.SaverIdleText, v.SaverIntervalText = in.Get("saver_idle"), in.Get("saver_interval")
		if n, err := strconv.Atoi(strings.TrimSpace(in.Get("saver_idle"))); err == nil {
			v.SaverIdle = n
		}
		chosen = map[string]bool{}
		for _, a := range in["saver_album"] {
			chosen[a] = true
		}
		v.AllAlbums = in.Get("saver_all") != "" || len(chosen) == 0
	}
	albums, _ := s.db.Albums(p.household.ID)
	total := 0
	for _, a := range albums {
		total += a.Photos
		count := s.i18n.T(lang, "photos.n.empty_short")
		if a.Photos > 0 {
			count = s.plural(lang, "photos.n", a.Photos)
		}
		v.Albums = append(v.Albums, saverAlbum{ID: a.ID, Name: a.Name, Count: count, Checked: chosen[a.ID] && !v.AllAlbums})
	}
	v.NoPhotos = total == 0
	v.SaverOnText = s.i18n.Format(lang, i18n.Msg("displays.saver.on", "n", strconv.Itoa(v.SaverIdle)))
	every := s.i18n.T(lang, "displays.saver.every_minute")
	if iv, err := strconv.Atoi(v.SaverIntervalText); err == nil && iv != 1 {
		every = s.i18n.Format(lang, i18n.Msg("displays.saver.every_n", "n", strconv.Itoa(iv)))
	}
	v.SaverSummary = s.i18n.Format(lang, i18n.Msg("displays.saver.summary", "idle", strconv.Itoa(v.SaverIdle), "every", every))
	if d.Quiet.Enabled {
		v.QuietNote = s.i18n.Format(lang, i18n.Msg("displays.saver.quiet_on", "from", d.Quiet.FromText(), "to", d.Quiet.ToText()))
	} else {
		v.QuietNote = s.i18n.T(lang, "displays.saver.quiet_off")
	}
}
