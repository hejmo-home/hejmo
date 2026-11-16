package server

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/places"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/update"
)

type settingsJSON struct {
	DateFormat      store.DateFormat      `json:"date_format"`
	TimeFormat      store.TimeFormat      `json:"time_format"`
	TemperatureUnit store.TemperatureUnit `json:"temperature_unit"`
	AutoUpdate      store.UpdateMode      `json:"auto_update"`
	HomeName        string                `json:"home_name"`
	// HomePlaceID is the id of the place in the list on the box (GET /places);
	// null while the location was set by coordinates only.
	HomePlaceID *int `json:"home_place_id"`
	// Latitude and Longitude are null until the home has a location.
	Latitude    *float64    `json:"latitude"`
	Longitude   *float64    `json:"longitude"`
	MealCutoffs cutoffsJSON `json:"meal_cutoffs"`
	// FirstStepsHidden is whether the first steps card on the start page is hidden.
	FirstStepsHidden bool `json:"first_steps_hidden"`
	// FirstStepsSkipped lists the steps of that card a parent skipped, in the order of the card.
	FirstStepsSkipped []string `json:"first_steps_skipped"`
}

func settingsOf(h store.Household) settingsJSON {
	out := settingsJSON{DateFormat: h.DateFormat, TimeFormat: h.TimeFormat, TemperatureUnit: h.TemperatureUnit, AutoUpdate: h.AutoUpdate, HomeName: h.HomeName,
		MealCutoffs: cutoffsToJSON(h.MealCutoffs), FirstStepsHidden: h.FirstStepsHidden, FirstStepsSkipped: skippedOrEmpty(h.FirstStepsSkipped)}
	if h.Latitude != 0 || h.Longitude != 0 {
		out.Latitude, out.Longitude = &h.Latitude, &h.Longitude
	}
	if h.HomePlaceID != 0 {
		out.HomePlaceID = &h.HomePlaceID
	}
	return out
}

// skippedOrEmpty is the list for JSON: never null.
func skippedOrEmpty(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// cutoffsJSON is the time per mealtime from which it is no longer shown on
// displays (ADR 0016); empty means no cut-off.
type cutoffsJSON struct {
	Breakfast string `json:"breakfast"`
	Lunch     string `json:"lunch"`
	Snack     string `json:"snack"`
	Dinner    string `json:"dinner"`
}

func cutoffsToJSON(c meal.Cutoffs) cutoffsJSON { return cutoffsJSON(c) }

func (s *server) apiGetSettings(w http.ResponseWriter, _ *http.Request, p principal) {
	writeJSON(w, http.StatusOK, settingsOf(p.household))
}

func (s *server) apiUpdateSettings(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		DateFormat      *store.DateFormat      `json:"date_format"`
		TimeFormat      *store.TimeFormat      `json:"time_format"`
		TemperatureUnit *store.TemperatureUnit `json:"temperature_unit"`
		AutoUpdate      *store.UpdateMode      `json:"auto_update"`
		HomeName        *string                `json:"home_name"`
		HomePlaceID     *int                   `json:"home_place_id"`
		Latitude        *float64               `json:"latitude"`
		Longitude       *float64               `json:"longitude"`
		MealCutoffs     *struct {
			Breakfast, Lunch, Snack, Dinner *string
		} `json:"meal_cutoffs"`
		// FirstStepsHidden hides or shows the first steps card on the start page.
		FirstStepsHidden *bool `json:"first_steps_hidden"`
		// FirstStepsSkipped replaces the list of skipped steps.
		FirstStepsSkipped *[]string `json:"first_steps_skipped"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	d, f, u := p.household.DateFormat, p.household.TimeFormat, p.household.TemperatureUnit
	if in.DateFormat != nil {
		d = *in.DateFormat
	}
	if in.TimeFormat != nil {
		f = *in.TimeFormat
	}
	if in.TemperatureUnit != nil {
		u = *in.TemperatureUnit
	}
	a := p.household.AutoUpdate
	if in.AutoUpdate != nil {
		a = *in.AutoUpdate
	}
	c := p.household.MealCutoffs
	if in.MealCutoffs != nil {
		for m, v := range map[meal.Mealtime]*string{meal.Breakfast: in.MealCutoffs.Breakfast, meal.Lunch: in.MealCutoffs.Lunch, meal.Snack: in.MealCutoffs.Snack, meal.Dinner: in.MealCutoffs.Dinner} {
			if v != nil {
				c = c.With(m, *v)
			}
		}
	}
	if !d.Valid() {
		writeError(w, http.StatusBadRequest, "date_format must be dmy or mdy")
		return
	}
	if !f.Valid() || !u.Valid() || !a.Valid() {
		writeError(w, http.StatusUnprocessableEntity, "time_format must be 24h or 12h, temperature_unit c or f and auto_update on, off or notify")
		return
	}
	if err := c.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "meal_cutoffs: "+err.Error())
		return
	}
	home, errMsg := s.homeOfPatch(p.household, in.HomeName, in.HomePlaceID, in.Latitude, in.Longitude)
	if errMsg != "" {
		writeError(w, http.StatusUnprocessableEntity, errMsg)
		return
	}
	if err := s.saveSettings(p.household, store.HouseholdSettings{DateFormat: d, TimeFormat: f, TemperatureUnit: u, AutoUpdate: a, MealCutoffs: c, FirstStepsHidden: in.FirstStepsHidden, FirstStepsSkipped: in.FirstStepsSkipped, Home: home}); err != nil {
		var unknown *store.UnknownStepError
		if errors.As(err, &unknown) {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("first_steps_skipped: unknown step %q; steps are %s", unknown.Step, strings.Join(store.FirstStepIDs, ", ")))
			return
		}
		if errors.Is(err, store.ErrInvalid) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		s.fail(w, "update settings", err)
		return
	}
	saved, err := s.db.Household(p.household.ID)
	if err != nil {
		s.fail(w, "update settings", err)
		return
	}
	if home != nil {
		s.refreshWeather(home.Latitude, home.Longitude)
	}
	writeJSON(w, http.StatusOK, settingsOf(saved))
}

// homeOfPatch reads the home of a settings patch: nil if it does not touch
// the home, else the new home, or what is wrong with it. The home is either a
// place of the list on the box (home_place_id: name and coordinates come from
// the list) or explicit coordinates; latitude and longitude come together, and
// a new name needs the coordinates of its place (find them with GET /places), so
// the name and the location never disagree. Coordinates without a place id
// clear the stored place id.
func (s *server) homeOfPatch(h store.Household, name *string, placeID *int, lat, lon *float64) (*store.Home, string) {
	if placeID != nil {
		if name != nil || lat != nil || lon != nil {
			return nil, "home_place_id replaces home_name, latitude and longitude: give only the place id"
		}
		pl, ok := s.places.Get(*placeID, "en")
		if !ok {
			return nil, "home_place_id is not a place in the list (find places with GET /places)"
		}
		return &store.Home{Name: pl.Name, Latitude: pl.Lat, Longitude: pl.Lon, PlaceID: pl.ID}, ""
	}
	if name == nil && lat == nil && lon == nil {
		return nil, ""
	}
	if (lat == nil) != (lon == nil) {
		return nil, "latitude and longitude must be given together"
	}
	if lat == nil {
		return nil, "a new home_name needs latitude and longitude, or a home_place_id (find places with GET /places)"
	}
	if !validCoordinates(*lat, *lon) {
		return nil, "latitude must be between -90 and 90 and longitude between -180 and 180"
	}
	home := store.Home{Name: h.HomeName, Latitude: *lat, Longitude: *lon}
	if name != nil {
		home.Name = strings.TrimSpace(*name)
	}
	if home.Name == "" {
		return nil, "home_name must not be empty"
	}
	return &home, ""
}

// validCoordinates reports whether lat and lon are real degrees. (0, 0) is
// refused: the stored home treats it as "no location yet".
func validCoordinates(lat, lon float64) bool {
	if lat == 0 && lon == 0 {
		return false
	}
	return !math.IsNaN(lat) && !math.IsNaN(lon) && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
}

// settingsPage is what the household page shows: the stored settings, or what
// was typed when the page is shown again.
type settingsPage struct {
	DateFormat, TimeFormat, TemperatureUnit, AutoUpdate string
	// Place is what the home location picker shows; nil shows the stored home.
	Place   *placePick
	Cutoffs meal.Cutoffs // what the cut-off fields hold
}

// renderSettings shows the household settings. form is what the page holds
// (what was typed, after a refused save); errField names the field at fault.
func (s *server) renderSettings(w http.ResponseWriter, r *http.Request, p principal, status int, errKey, errField string, form settingsPage) {
	fields := make([]cutoffInput, 0, 4)
	for _, m := range meal.Mealtimes() {
		// A mealtime without a cut-off is switched off; its field waits with a
		// sensible time, so switching it on starts there.
		at := form.Cutoffs.For(m)
		fields = append(fields, cutoffInput{Name: cutoffField(m), Mealtime: string(m), Value: cmp.Or(at, meal.SuggestedCutoff(m)), On: at != ""})
	}
	sample := s.dateSample(p.household)
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Error": errKey, "ErrorField": errField, "Cutoffs": fields,
		"DateFormat": form.DateFormat, "DateDMY": datefmt.Date(store.DateDMY, sample), "DateMDY": datefmt.Date(store.DateMDY, sample), "SampleYear": sample.Year(),
		"TimeFormat": form.TimeFormat, "TemperatureUnit": form.TemperatureUnit,
		"AutoUpdate": form.AutoUpdate, "UpdateManaged": s.updateDir != "", "Update": s.updateNotice(p.household),
		"Place": cmp.Or(form.Place, s.storedPicker(r, p)),
	}
	if hasLocation(p.household) {
		data["StoredHomeName"] = p.household.HomeName // what stays in use while another place is picked
	}
	s.render(w, r, status, "household.html", data)
}

// hasLocation reports whether the household has a home location.
func hasLocation(h store.Household) bool { return h.Latitude != 0 || h.Longitude != 0 }

// storedPicker is the picker for the stored home: the place in use with
// today's sun times, or, for a household without a location (set up before the
// list of places), a dashed box and the open field with suggestions for the
// stored name.
func (s *server) storedPicker(r *http.Request, p principal) *placePick {
	h, lang := p.household, s.langOf(r)
	if !hasLocation(h) {
		v := s.openPicker(lang, h.HomeName)
		v.Missing = &placeMissing{Title: s.i18n.T(lang, "place.missing"), Hint: s.i18n.T(lang, "place.missing.hint")}
		return v
	}
	pl := places.Place{ID: h.HomePlaceID, Name: h.HomeName}
	if got, ok := s.places.Get(h.HomePlaceID, lang); ok {
		pl.Region, pl.RegionCode = got.Region, got.RegionCode
	}
	// A home set through the API has no place id, and so no region line.
	v := chosenPicker(pl, s.sunLine(r, p, h.Latitude, h.Longitude))
	v.Chosen.Name = h.HomeName
	return v
}

// pendingPicker is the picker for a place that was picked and is not saved yet.
func (s *server) pendingPicker(r *http.Request, p principal, pl places.Place) *placePick {
	h, lang := p.household, s.langOf(r)
	switch {
	case hasLocation(h) && h.HomePlaceID == pl.ID:
		return s.storedPicker(r, p)
	case hasLocation(h):
		return chosenPicker(pl, s.i18n.Format(lang, i18n.Msg("place.pending", "old", h.HomeName)))
	}
	return chosenPicker(pl, s.i18n.T(lang, "place.chosen"))
}

// withKeep adds the row that offers to keep the saved place, if there is one.
func (s *server) withKeep(r *http.Request, p principal, v *placePick) *placePick {
	if !hasLocation(p.household) {
		return v
	}
	lang, old := s.langOf(r), p.household.HomeName
	v.Keep = &placeKeep{Hint: s.i18n.Format(lang, i18n.Msg("place.keep_hint", "old", old)), Button: s.i18n.Format(lang, i18n.Msg("place.keep", "old", old))}
	return v
}

// cutoffField is the name of a mealtime's time field in the settings form.
func cutoffField(m meal.Mealtime) string { return "cutoff_" + string(m) }

type cutoffInput struct {
	Name, Mealtime, Value string
	On                    bool // the switch: the mealtime disappears from displays at Value
}

// storedSettings is the page as stored.
func storedSettings(h store.Household) settingsPage {
	return settingsPage{string(h.DateFormat), string(h.TimeFormat), string(h.TemperatureUnit), string(h.AutoUpdate), nil, h.MealCutoffs}
}

// hubEntry is a hub row; State, when set, is the key of the word that opens its hint.
type hubEntry struct {
	hubItem
	State string
}

// handleSettingsHub shows the Settings hub: one calm row per setup screen.
func (s *server) handleSettingsHub(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	var items []hubEntry
	for _, it := range hubItems {
		if auth.RoleAllows(p.person.Role, it.Needs) {
			e := hubEntry{hubItem: it}
			if it.Href == mcpPath { // the state comes first, so it shows without opening the page
				e.State = "hub.mcp.off"
				if p.household.MCPEnabled {
					e.State = "hub.mcp.on"
				}
			}
			items = append(items, e)
		}
	}
	s.render(w, r, http.StatusOK, "settings.html", map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Hub": items, "Update": s.updateNotice(p.household), "FirstStepsHidden": p.household.FirstStepsHidden,
	})
}

func (s *server) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.renderSettings(w, r, p, http.StatusOK, "", "", storedSettings(p.household))
}

func (s *server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	f, u := store.TimeFormat(r.PostFormValue("time_format")), store.TemperatureUnit(r.PostFormValue("temperature_unit"))
	d := p.household.DateFormat
	if v := r.PostFormValue("date_format"); v != "" { // a form without the field leaves it alone
		d = store.DateFormat(v)
	}
	a := p.household.AutoUpdate
	if v := r.PostFormValue("auto_update"); v != "" { // a form without the field leaves it alone
		a = store.UpdateMode(v)
	}
	_ = r.ParseForm()
	c := p.household.MealCutoffs
	for _, m := range meal.Mealtimes() {
		c = c.With(m, postedCutoff(r.PostForm, m, c.For(m)))
	}
	typed := settingsPage{string(d), string(f), string(u), string(a), nil, c}
	if !d.Valid() {
		s.renderSettings(w, r, p, http.StatusUnprocessableEntity, "settings.error.date_format", "date_format", typed)
		return
	}
	if !f.Valid() || !u.Valid() || !a.Valid() {
		s.renderSettings(w, r, p, http.StatusUnprocessableEntity, "settings.error.invalid", "", typed)
		return
	}
	if err := c.Validate(); err != nil {
		var bad *meal.InvalidError
		field := ""
		if errors.As(err, &bad) {
			field = cutoffField(meal.Mealtime(bad.Field))
		}
		s.renderSettings(w, r, p, http.StatusUnprocessableEntity, "settings.error.cutoff", field, typed)
		return
	}
	// The home place (ADR 0021): the page posts the id of a place of the list
	// and never coordinates. A form without place fields leaves the home alone.
	home, done := s.homeOfForm(w, r, p, typed)
	if done {
		return
	}
	if err := s.saveSettings(p.household, store.HouseholdSettings{DateFormat: d, TimeFormat: f, TemperatureUnit: u, AutoUpdate: a, MealCutoffs: c, Home: home}); err != nil {
		s.fail(w, "update settings", err)
		return
	}
	if home != nil { // the success line names the new place
		s.refreshWeather(home.Latitude, home.Longitude)
		s.redirectSaved(w, r, "/settings/household", "flash.home_saved")
		return
	}
	s.redirectSaved(w, r, "/settings/household", "flash.household_saved")
}

// homeOfForm reads what the form posted about the home place. It either
// renders the page again (a pick, Change or Keep that saves nothing, or an
// error at the picker) and says so with done, or returns the home to save:
// nil when the home stays as it is.
func (s *server) homeOfForm(w http.ResponseWriter, r *http.Request, p principal, typed settingsPage) (home *store.Home, done bool) {
	in, lang := readPickerInput(r), s.langOf(r)
	show := func(status int, errKey string, v *placePick) {
		typed.Place = v
		errField := ""
		if errKey != "" {
			errField = "home_name"
		}
		s.renderSettings(w, r, p, status, errKey, errField, typed)
	}
	switch {
	case in.Keep: // back to the saved place; nothing is saved
		show(http.StatusOK, "", nil)
	case in.Change: // open the field for the place that was shown
		text := p.household.HomeName
		if pl, ok := s.places.Get(in.ID, lang); ok {
			text = pl.Name
		}
		show(http.StatusOK, "", s.withKeep(r, p, s.openPicker(lang, text)))
	case in.HasText && in.HasID: // a suggestion was picked: show it, nothing is saved
		pl, ok := s.places.Get(in.ID, lang)
		if !ok {
			show(http.StatusUnprocessableEntity, "place.error.pick", s.withKeep(r, p, s.pickError(lang, in.Text)))
			break
		}
		show(http.StatusOK, "", s.pendingPicker(r, p, pl))
	case in.HasText: // text, but no place picked
		v := s.openPickerError(lang, in.Text)
		key := "place.error.pick"
		switch {
		case in.Text == "":
			key = "setup.error.home"
		case len(v.Options) == 0:
			key = "place.error.none"
		}
		show(http.StatusUnprocessableEntity, key, s.withKeep(r, p, v))
	case in.HasID: // Save with the confirmed place
		if hasLocation(p.household) && in.ID == p.household.HomePlaceID {
			return nil, false
		}
		pl, ok := s.places.Get(in.ID, lang)
		if !ok {
			show(http.StatusUnprocessableEntity, "place.error.pick", s.withKeep(r, p, s.pickError(lang, "")))
			break
		}
		return &store.Home{Name: pl.Name, Latitude: pl.Lat, Longitude: pl.Lon, PlaceID: pl.ID}, false
	default:
		return nil, false
	}
	return nil, true
}

// postedCutoff is the cut-off of a mealtime as the form sends it: a switch
// (cutoff_<m>_on) and a time (cutoff_<m>). A mealtime switched off has no
// cut-off, whatever the time field holds; an unchecked box is not sent at all,
// so a form that carries the time field but not the switch says "off". A
// switch that is on with an empty time starts at the suggested time. A form
// with neither leaves the stored value alone (current).
func postedCutoff(form url.Values, m meal.Mealtime, current string) string {
	at, hasTime := form[cutoffField(m)]
	on, hasSwitch := form[cutoffField(m)+"_on"]
	switch {
	case !hasTime && !hasSwitch:
		return current
	case !hasSwitch || len(on) == 0 || on[0] == "off":
		return ""
	case !hasTime || len(at) == 0 || at[0] == "":
		return cmp.Or(current, meal.SuggestedCutoff(m))
	}
	return at[0]
}

// refreshWeather fetches the weather of a new home at once, so the chip does
// not wait for the next round; a failure is left to that round.
func (s *server) refreshWeather(lat, lon float64) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.weather.Refresh(ctx, lat, lon); err != nil {
		s.logger.Warn("weather not refreshed for the new home", "err", err)
	}
}

// saveSettings stores the household settings (general ones, meal cut-offs and
// the first steps state) in one write. The update mode also goes to the host script; only a
// failing settings file is tolerated.
func (s *server) saveSettings(h store.Household, in store.HouseholdSettings) error {
	if err := s.db.SaveHouseholdSettings(h.ID, in); err != nil {
		return err
	}
	if in.AutoUpdate == h.AutoUpdate {
		return nil
	}
	// The change is stored; a settings file that cannot be written must not turn
	// it into an error. The box writes the file again at its next start.
	if err := update.WriteSettings(s.updateDir, string(in.AutoUpdate)); err != nil {
		s.logger.Error("write update settings", "err", err)
	}
	return nil
}

// dateSample is 24 December of the current year in the household's zone: the
// sample date of the date format setting (ticket #147). 24 is above 12, so no
// one reads it the wrong way round.
func (s *server) dateSample(h store.Household) time.Time {
	return time.Date(s.householdNow(h).Year(), time.December, 24, 12, 0, 0, 0, time.UTC)
}
