package server

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/sun"
	"github.com/hejmo-home/hejmo/internal/update"
)

type settingsJSON struct {
	TimeFormat      store.TimeFormat      `json:"time_format"`
	TemperatureUnit store.TemperatureUnit `json:"temperature_unit"`
	AutoUpdate      store.UpdateMode      `json:"auto_update"`
	HomeName        string                `json:"home_name"`
	// Latitude and Longitude are null until the home has a location.
	Latitude    *float64    `json:"latitude"`
	Longitude   *float64    `json:"longitude"`
	MealCutoffs cutoffsJSON `json:"meal_cutoffs"`
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

func settingsOf(h store.Household) settingsJSON {
	out := settingsJSON{TimeFormat: h.TimeFormat, TemperatureUnit: h.TemperatureUnit, AutoUpdate: h.AutoUpdate, HomeName: h.HomeName, MealCutoffs: cutoffsToJSON(h.MealCutoffs)}
	if h.Latitude != 0 || h.Longitude != 0 {
		out.Latitude, out.Longitude = &h.Latitude, &h.Longitude
	}
	return out
}

func (s *server) apiGetSettings(w http.ResponseWriter, _ *http.Request, p principal) {
	writeJSON(w, http.StatusOK, settingsOf(p.household))
}

func (s *server) apiUpdateSettings(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		TimeFormat      *store.TimeFormat      `json:"time_format"`
		TemperatureUnit *store.TemperatureUnit `json:"temperature_unit"`
		AutoUpdate      *store.UpdateMode      `json:"auto_update"`
		HomeName        *string                `json:"home_name"`
		Latitude        *float64               `json:"latitude"`
		Longitude       *float64               `json:"longitude"`
		MealCutoffs     *struct {
			Breakfast, Lunch, Snack, Dinner *string
		} `json:"meal_cutoffs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	f, u := p.household.TimeFormat, p.household.TemperatureUnit
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
	if !f.Valid() || !u.Valid() || !a.Valid() {
		writeError(w, http.StatusUnprocessableEntity, "time_format must be 24h or 12h, temperature_unit c or f and auto_update on, off or notify")
		return
	}
	if err := c.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "meal_cutoffs: "+err.Error())
		return
	}
	home, errMsg := homeOfPatch(p.household, in.HomeName, in.Latitude, in.Longitude)
	if errMsg != "" {
		writeError(w, http.StatusUnprocessableEntity, errMsg)
		return
	}
	if err := s.saveSettings(p.household, f, u, a, c, home); err != nil {
		s.fail(w, "update settings", err)
		return
	}
	if home != nil {
		s.refreshWeather(home.Latitude, home.Longitude)
	}
	h := p.household
	h.TimeFormat, h.TemperatureUnit, h.AutoUpdate, h.MealCutoffs = f, u, a, c
	if home != nil {
		h.HomeName, h.Latitude, h.Longitude = home.Name, home.Latitude, home.Longitude
	}
	writeJSON(w, http.StatusOK, settingsOf(h))
}

// homeOfPatch reads the home of a settings patch: nil if it does not touch
// the home, else the new home, or what is wrong with it. Latitude and
// longitude come together, and a new name needs the coordinates of its place
// (search with GET /places), so the name and the location never disagree.
func homeOfPatch(h store.Household, name *string, lat, lon *float64) (*store.Home, string) {
	if name == nil && lat == nil && lon == nil {
		return nil, ""
	}
	if (lat == nil) != (lon == nil) {
		return nil, "latitude and longitude must be given together"
	}
	if lat == nil {
		return nil, "a new home_name needs latitude and longitude (find them with GET /places)"
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

// settingsPage is what the household page shows: the stored settings, or what
// was typed when the page is shown again.
type settingsPage struct {
	TimeFormat, TemperatureUnit, AutoUpdate, HomeName string
	Place                                             *placeView
	Cutoffs                                           meal.Cutoffs // what the cut-off fields hold
}

func (s *server) renderSettings(w http.ResponseWriter, r *http.Request, p principal, status int, errKey, errField string, form settingsPage) {
	fields := make([]cutoffInput, 0, 4)
	for _, m := range meal.Mealtimes() {
		// A mealtime without a cut-off is switched off; its field waits with a
		// sensible time, so switching it on starts there.
		at := form.Cutoffs.For(m)
		fields = append(fields, cutoffInput{Name: cutoffField(m), Mealtime: string(m), Value: cmp.Or(at, meal.SuggestedCutoff(m)), On: at != ""})
	}
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Error": errKey, "ErrorField": errField, "Cutoffs": fields,
		"TimeFormat": form.TimeFormat, "TemperatureUnit": form.TemperatureUnit,
		"AutoUpdate": form.AutoUpdate, "UpdateManaged": s.updateDir != "", "Update": s.updateNotice(p.household),
		"HomeName": form.HomeName, "Place": form.Place,
	}
	if p.household.Latitude != 0 || p.household.Longitude != 0 {
		label := "place.in_use"
		if errField == "home_name" { // the search failed: nothing changed
			label = "place.still_in_use"
		}
		data["Location"] = map[string]string{"Name": p.household.HomeName, "Label": label, "Detail": s.locationDetail(r, p)}
	}
	s.render(w, r, status, "household.html", data)
}

// locationDetail is the second line of the location in use: its coordinates and
// today's sunrise and sunset there, in the household's time zone and format.
func (s *server) locationDetail(r *http.Request, p principal) string {
	h := p.household
	coords := coordinatesText(h.Latitude, h.Longitude)
	t := sun.On(s.now().In(p.location()), h.Latitude, h.Longitude)
	if t.Kind != sun.Normal { // polar day or night: no sunrise to name
		return coords
	}
	layout := "15:04"
	if h.TimeFormat == store.Time12h {
		layout = "3:04 PM"
	}
	return s.i18n.Format(s.langOf(r), i18n.Msg("place.in_use.detail", "coords", coords, "rise", t.Rise.Round(time.Minute).In(p.location()).Format(layout), "set", t.Set.Round(time.Minute).In(p.location()).Format(layout)))
}

// cutoffField is the name of a mealtime's time field in the settings form.
func cutoffField(m meal.Mealtime) string { return "cutoff_" + string(m) }

type cutoffInput struct {
	Name, Mealtime, Value string
	On                    bool // the switch: the mealtime disappears from displays at Value
}

// storedSettings is the page as stored.
func storedSettings(h store.Household) settingsPage {
	return settingsPage{string(h.TimeFormat), string(h.TemperatureUnit), string(h.AutoUpdate), h.HomeName, nil, h.MealCutoffs}
}

// handleSettingsHub shows the Settings hub: one calm row per setup screen.
func (s *server) handleSettingsHub(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	var items []hubItem
	for _, it := range hubItems {
		if auth.RoleAllows(p.person.Role, it.Needs) {
			items = append(items, it)
		}
	}
	s.render(w, r, http.StatusOK, "settings.html", map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Hub": items, "Update": s.updateNotice(p.household),
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
	a := p.household.AutoUpdate
	if v := r.PostFormValue("auto_update"); v != "" { // a form without the field leaves it alone
		a = store.UpdateMode(v)
	}
	_ = r.ParseForm()
	c := p.household.MealCutoffs
	for _, m := range meal.Mealtimes() {
		c = c.With(m, postedCutoff(r.PostForm, m, c.For(m)))
	}
	typed := settingsPage{string(f), string(u), string(a), p.household.HomeName, nil, c}
	if _, sent := r.PostForm["home_name"]; sent {
		typed.HomeName = strings.TrimSpace(r.PostFormValue("home_name"))
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
	// The home place (Q59): a form without the field leaves it alone. A new
	// name, or a home that has no location yet, must be searched and
	// confirmed before anything is saved.
	var home *store.Home
	if _, sent := r.PostForm["home_name"]; sent {
		name := strings.TrimSpace(r.PostFormValue("home_name"))
		switch {
		case name == "":
			s.renderSettings(w, r, p, http.StatusUnprocessableEntity, "setup.error.home", "home_name", typed)
			return
		case name != p.household.HomeName || (p.household.Latitude == 0 && p.household.Longitude == 0):
			lat, lon, confirmed, view, errKey := s.resolvePlace(r, name, p.household.Region)
			if errKey != "" {
				s.renderSettings(w, r, p, http.StatusUnprocessableEntity, errKey, "home_name", typed)
				return
			}
			if !confirmed {
				typed.Place = view
				s.renderSettings(w, r, p, http.StatusOK, "", "", typed)
				return
			}
			home = &store.Home{Name: name, Latitude: lat, Longitude: lon}
		}
	}
	if err := s.saveSettings(p.household, f, u, a, c, home); err != nil {
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

// saveSettings stores the household settings: the general ones (with the home) and the meal
// cut-offs are two writes. The update
// mode also goes to the host script; only a failing settings file is tolerated.
func (s *server) saveSettings(h store.Household, f store.TimeFormat, u store.TemperatureUnit, a store.UpdateMode, c meal.Cutoffs, home *store.Home) error {
	var err error
	if home == nil {
		err = s.db.UpdateHouseholdSettings(h.ID, f, u, a)
	} else {
		err = s.db.UpdateHouseholdSettingsAndHome(h.ID, f, u, a, *home)
	}
	if err != nil {
		return err
	}
	if err := s.db.SetMealCutoffs(h.ID, c); err != nil {
		return err
	}
	if a == h.AutoUpdate {
		return nil
	}
	// The change is stored; a settings file that cannot be written must not turn
	// it into an error. The box writes the file again at its next start.
	if err := update.WriteSettings(s.updateDir, string(a)); err != nil {
		s.logger.Error("write update settings", "err", err)
	}
	return nil
}
