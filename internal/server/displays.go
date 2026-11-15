package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/kiosk"
	"github.com/hejmo-home/hejmo/internal/quiet"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/theme"
)

type displayView struct {
	ID, Name, Room, Type, Paired, LastSeen string
	ShowTomorrow                           bool
	QuietOn                                bool
	QuietFrom, QuietTo                     string
	KioskURL                               string
	KioskKeySet                            bool
	Theme                                  string
	SunriseOffset, SunsetOffset            int
}

func (s *server) renderDisplays(w http.ResponseWriter, r *http.Request, p principal, status int, form map[string]any) {
	list, err := s.db.Displays(p.household.ID)
	if err != nil {
		s.fail(w, "displays", err)
		return
	}
	at := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return p.dateTime(t)
	}
	views := make([]displayView, 0, len(list))
	for _, d := range list {
		views = append(views, displayView{d.ID, d.Name, d.Room, string(d.Type), at(d.PairedAt), at(d.LastSeen), d.ShowTomorrow,
			d.Quiet.Enabled, d.Quiet.FromText(), d.Quiet.ToText(), d.KioskURL, d.KioskKeySet, string(d.Theme), d.SunriseOffset, d.SunsetOffset})
	}
	data := map[string]any{
		"Person": p.person, "Displays": views, "SyncStale": s.syncStale(p.household.ID), "Form": form,
		"Types":  []store.DisplayType{store.DisplayEInkBW, store.DisplayEInkColor, store.DisplayScreen},
		"Themes": []store.Theme{theme.Light, theme.Dark, theme.Auto},
	}
	data["Error"], data["ErrorField"] = form["Error"], form["ErrorField"] // form is never nil
	s.render(w, r, status, "displays.html", data)
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
	switch err := s.db.RevokeDisplay(p.household.ID, r.PathValue("id"), s.now()); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, "unpair display", err)
	default:
		s.redirectSaved(w, r, "/displays", "flash.display_unpaired")
	}
}

var (
	errQuietTimes = errors.New("quiet mode needs two different times")
	errKioskURL   = errors.New("the kiosk address must be an http or https address")
	errTheme      = errors.New("theme must be light, dark or auto and the offsets between -180 and 180 minutes")
)

// displayChange is a change of a display's settings; nil fields stay as they are.
type displayChange struct {
	ShowTomorrow       *bool
	QuietEnabled       *bool
	QuietFrom, QuietTo *string
	KioskURL, KioskKey *string // a nil key keeps the stored one, an empty address removes the kiosk
	// Theme and the sun offsets (ADR 0015).
	Theme                       *string
	SunriseOffset, SunsetOffset *int
}

// changeDisplay validates and saves a change, for the web interface and the API alike.
func (s *server) changeDisplay(householdID, id string, c displayChange) error {
	list, err := s.db.Displays(householdID)
	if err != nil {
		return err
	}
	var cur store.Display
	found := false
	for _, d := range list {
		if d.ID == id {
			cur, found = d, true
		}
	}
	if !found {
		return store.ErrNotFound
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
	if c.ShowTomorrow != nil {
		if err := s.db.SetDisplayShowTomorrow(householdID, id, *c.ShowTomorrow); err != nil {
			return err
		}
	}
	if th != cur.Theme || rise != cur.SunriseOffset || set != cur.SunsetOffset {
		if err := s.db.SetDisplayTheme(householdID, id, th, rise, set); err != nil {
			return err
		}
	}
	if sched != cur.Quiet {
		if err := s.db.SetDisplayQuiet(householdID, id, sched); err != nil {
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

// optional returns the form field's value, or nil when the form has no such field.
func optional(r *http.Request, name string) *string {
	if _, ok := r.PostForm[name]; !ok {
		return nil
	}
	v := strings.TrimSpace(r.PostForm.Get(name))
	return &v
}

// handleDisplaySettings saves the per-display settings: the tomorrow block
// (ADR 0016), quiet mode and the kiosk app (ADR 0015). An unticked checkbox
// sends nothing, so absent means off; absent times and addresses stay.
func (s *server) handleDisplaySettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	themeChange := displayChange{}
	if th := r.PostForm.Get("theme"); th != "" {
		rise, err1 := strconv.Atoi(strings.TrimSpace(r.PostForm.Get("sunrise_offset")))
		set, err2 := strconv.Atoi(strings.TrimSpace(r.PostForm.Get("sunset_offset")))
		if err1 != nil || err2 != nil {
			s.renderDisplays(w, r, p, http.StatusUnprocessableEntity, map[string]any{"Error": "displays.error.theme"})
			return
		}
		themeChange = displayChange{Theme: &th, SunriseOffset: &rise, SunsetOffset: &set}
	}
	on := func(name string) *bool { v := r.PostForm.Get(name) != ""; return &v }
	err := s.changeDisplay(p.household.ID, r.PathValue("id"), displayChange{
		ShowTomorrow: on("show_tomorrow"), QuietEnabled: on("quiet"),
		QuietFrom: optional(r, "quiet_from"), QuietTo: optional(r, "quiet_to"),
		KioskURL: optional(r, "kiosk_url"), KioskKey: optional(r, "kiosk_key"),
		Theme: themeChange.Theme, SunriseOffset: themeChange.SunriseOffset, SunsetOffset: themeChange.SunsetOffset,
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, errQuietTimes):
		s.renderDisplays(w, r, p, http.StatusUnprocessableEntity, map[string]any{"Error": "displays.error.quiet", "Type": string(store.DisplayEInkBW)})
	case errors.Is(err, errTheme):
		s.renderDisplays(w, r, p, http.StatusUnprocessableEntity, map[string]any{"Error": "displays.error.theme"})
	case errors.Is(err, errKioskURL):
		s.renderDisplays(w, r, p, http.StatusUnprocessableEntity, map[string]any{"Error": "displays.error.kiosk", "Type": string(store.DisplayEInkBW)})
	case err != nil:
		s.fail(w, "display settings", err)
	default:
		s.redirectSaved(w, r, "/displays", "flash.saved")
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
		Theme: string(d.Theme), SunriseOffset: d.SunriseOffset, SunsetOffset: d.SunsetOffset}
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
		ShowTomorrow *bool `json:"show_tomorrow"`
		QuietMode    *struct {
			Enabled *bool   `json:"enabled"`
			From    *string `json:"from"`
			To      *string `json:"to"`
		} `json:"quiet_mode"`
		Theme         *string `json:"theme"`
		SunriseOffset *int    `json:"sunrise_offset"`
		SunsetOffset  *int    `json:"sunset_offset"`
		Kiosk         *struct {
			URL    *string `json:"url"`
			APIKey *string `json:"api_key"`
		} `json:"kiosk"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	c := displayChange{ShowTomorrow: in.ShowTomorrow, Theme: in.Theme, SunriseOffset: in.SunriseOffset, SunsetOffset: in.SunsetOffset}
	if q := in.QuietMode; q != nil {
		c.QuietEnabled, c.QuietFrom, c.QuietTo = q.Enabled, q.From, q.To
	}
	if k := in.Kiosk; k != nil {
		c.KioskURL, c.KioskKey = k.URL, k.APIKey
	}
	switch err := s.changeDisplay(p.household.ID, r.PathValue("id"), c); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "display not found")
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

// displayOf finds one of the household's paired displays.
func (s *server) displayOf(householdID, id string) (store.Display, bool) {
	list, err := s.db.Displays(householdID)
	if err != nil {
		return store.Display{}, false
	}
	for _, d := range list {
		if d.ID == id {
			return d, true
		}
	}
	return store.Display{}, false
}
