package server

import (
	"cmp"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/update"
)

type settingsJSON struct {
	DateFormat      store.DateFormat      `json:"date_format"`
	TimeFormat      store.TimeFormat      `json:"time_format"`
	TemperatureUnit store.TemperatureUnit `json:"temperature_unit"`
	AutoUpdate      store.UpdateMode      `json:"auto_update"`
	MealCutoffs     cutoffsJSON           `json:"meal_cutoffs"`
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
	writeJSON(w, http.StatusOK, settingsJSON{p.household.DateFormat, p.household.TimeFormat, p.household.TemperatureUnit, p.household.AutoUpdate, cutoffsToJSON(p.household.MealCutoffs)})
}

func (s *server) apiUpdateSettings(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		DateFormat      *store.DateFormat      `json:"date_format"`
		TimeFormat      *store.TimeFormat      `json:"time_format"`
		TemperatureUnit *store.TemperatureUnit `json:"temperature_unit"`
		AutoUpdate      *store.UpdateMode      `json:"auto_update"`
		MealCutoffs     *struct {
			Breakfast, Lunch, Snack, Dinner *string
		} `json:"meal_cutoffs"`
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
	if err := s.saveSettings(p.household, d, f, u, a, c); err != nil {
		s.fail(w, "update settings", err)
		return
	}
	writeJSON(w, http.StatusOK, settingsJSON{d, f, u, a, cutoffsToJSON(c)})
}

// cutoffField is the name of a mealtime's time field in the settings form.
func cutoffField(m meal.Mealtime) string { return "cutoff_" + string(m) }

// renderSettings shows the household settings. cutoffs is what the cut-off
// fields hold (what was typed, after a refused save); errField names the
// field at fault.
func (s *server) renderSettings(w http.ResponseWriter, r *http.Request, p principal, status int, errKey, errField string, cutoffs meal.Cutoffs) {
	fields := make([]cutoffInput, 0, 4)
	for _, m := range meal.Mealtimes() {
		// A mealtime without a cut-off is switched off; its field waits with a
		// sensible time, so switching it on starts there.
		at := cutoffs.For(m)
		fields = append(fields, cutoffInput{Name: cutoffField(m), Mealtime: string(m), Value: cmp.Or(at, meal.SuggestedCutoff(m)), On: at != ""})
	}
	sample := s.dateSample(p.household)
	s.render(w, r, status, "household.html", map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Error": errKey, "ErrorField": errField, "Cutoffs": fields,
		"DateFormat": string(p.household.DateFormat), "DateDMY": datefmt.Date(store.DateDMY, sample), "DateMDY": datefmt.Date(store.DateMDY, sample), "SampleYear": sample.Year(), "TimeFormat": string(p.household.TimeFormat), "TemperatureUnit": string(p.household.TemperatureUnit),
		"AutoUpdate": string(p.household.AutoUpdate), "UpdateManaged": s.updateDir != "", "Update": s.updateNotice(p.household),
	})
}

type cutoffInput struct {
	Name, Mealtime, Value string
	On                    bool // the switch: the mealtime disappears from displays at Value
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
	s.renderSettings(w, r, p, http.StatusOK, "", "", p.household.MealCutoffs)
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
	if !d.Valid() {
		p.household.DateFormat = "" // nothing is checked after a value the form should never send
		s.renderSettings(w, r, p, http.StatusUnprocessableEntity, "settings.error.date_format", "date_format", c)
		return
	}
	if !f.Valid() || !u.Valid() || !a.Valid() {
		s.renderSettings(w, r, p, http.StatusUnprocessableEntity, "settings.error.invalid", "", c)
		return
	}
	if err := c.Validate(); err != nil {
		var bad *meal.InvalidError
		field := ""
		if errors.As(err, &bad) {
			field = cutoffField(meal.Mealtime(bad.Field))
		}
		s.renderSettings(w, r, p, http.StatusUnprocessableEntity, "settings.error.cutoff", field, c)
		return
	}
	if err := s.saveSettings(p.household, d, f, u, a, c); err != nil {
		s.fail(w, "update settings", err)
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

// saveSettings stores the household settings: the general ones and the meal
// cut-offs are two writes. The update mode also goes to the host script; only a
// failing settings file is tolerated.
func (s *server) saveSettings(h store.Household, d store.DateFormat, f store.TimeFormat, u store.TemperatureUnit, a store.UpdateMode, c meal.Cutoffs) error {
	if err := s.db.UpdateHouseholdSettings(h.ID, d, f, u, a); err != nil {
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

// dateSample is 24 December of the current year in the household's zone: the
// sample date of the date format setting (ticket #147). 24 is above 12, so no
// one reads it the wrong way round.
func (s *server) dateSample(h store.Household) time.Time {
	return time.Date(s.householdNow(h).Year(), time.December, 24, 12, 0, 0, 0, time.UTC)
}
