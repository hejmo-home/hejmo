package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/hejmo-home/hejmo/internal/always"
)

// The rules of "always on the list", the household's time and its holiday
// over the REST API (ticket 168, ADR 0015).

type alwaysRuleJSON struct {
	ArticleID string   `json:"article_id"`
	Enabled   bool     `json:"enabled"`
	Paused    bool     `json:"paused"`
	ListID    *string  `json:"list_id"`
	ListName  string   `json:"list_name"`
	Weekday   int      `json:"weekday"`
	Every     int      `json:"every"`
	StartWeek string   `json:"start_week"`
	Quantity  *qtyJSON `json:"quantity"`
	NextDate  *string  `json:"next_date"`
}

func (a *alwaysCtx) ruleJSON(r always.Rule) alwaysRuleJSON {
	j := alwaysRuleJSON{ArticleID: r.ArticleID, Enabled: r.Enabled, Paused: r.Paused(), ListName: r.ListName, Weekday: r.Weekday, Every: r.Every, StartWeek: r.Start, Quantity: previousJSON(r.Qty)}
	if r.ListID != "" {
		j.ListID = &r.ListID
	}
	if r.Running() {
		if d, _, ok := a.next(r); ok {
			s := d.Format(always.DateLayout)
			j.NextDate = &s
		}
	}
	return j
}

type alwaysSettingsJSON struct {
	Time    string             `json:"time"`
	Holiday *alwaysHolidayJSON `json:"holiday"`
	Rules   []alwaysRuleJSON   `json:"rules"`
}

type alwaysHolidayJSON struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (s *server) apiGetAlways(w http.ResponseWriter, r *http.Request, p principal) {
	rule, err := s.storedRule(p, r.PathValue("id"))
	if err != nil {
		s.fail(w, "always rule", err)
		return
	}
	if rule == nil {
		writeError(w, http.StatusNotFound, "this article has no rule")
		return
	}
	a, err := s.alwaysContext(p, s.langOf(r))
	if err != nil {
		s.fail(w, "always context", err)
		return
	}
	writeJSON(w, http.StatusOK, a.ruleJSON(*rule))
}

// apiPutAlways sets the rule of an article, or switches it off with
// {"enabled": false}, which keeps what it says.
func (s *server) apiPutAlways(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Enabled   *bool  `json:"enabled"`
		ListID    string `json:"list_id"`
		Weekday   int    `json:"weekday"`
		Every     *int   `json:"every"`
		StartWeek string `json:"start_week"`
		Quantity  *qtyIn `json:"quantity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	lang := s.langOf(r)
	a, err := s.alwaysContext(p, lang)
	if err != nil {
		s.fail(w, "always context", err)
		return
	}
	art, ok := a.c.article(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "article not found")
		return
	}
	stored, err := s.storedRule(p, art.ID)
	if err != nil {
		s.fail(w, "always rule", err)
		return
	}
	if in.Enabled != nil && !*in.Enabled {
		if stored == nil {
			writeError(w, http.StatusNotFound, "this article has no rule")
			return
		}
		if err := s.db.DisableAlwaysRule(p.household.ID, art.ID); err != nil {
			s.fail(w, "switch off rule", err)
			return
		}
		off := *stored
		off.Enabled = false
		writeJSON(w, http.StatusOK, a.ruleJSON(off))
		return
	}
	f := autoForm{On: true, List: in.ListID, Weekday: in.Weekday, Every: "1", Start: weekOf(a.today).Format(always.DateLayout), QMode: "none"}
	if in.Every != nil {
		f.Every = strconv.Itoa(*in.Every)
	}
	if in.StartWeek != "" {
		f.Start = in.StartWeek
	}
	if in.Quantity != nil {
		f.QMode, f.Amount, f.Unit = "qty", numberText(in.Quantity.Amount), in.Quantity.Unit
	}
	rule, errs := a.validate(art, f, stored)
	for _, field := range []string{"list", "weekday", "every", "start", "amount", "unit"} {
		if msg, bad := errs[field]; bad {
			api := map[string]string{"list": "list_id", "start": "start_week"}[field]
			if api == "" {
				api = field
			}
			writeFieldError(w, http.StatusUnprocessableEntity, api, msg)
			return
		}
	}
	saved, err := s.db.SaveAlwaysRule(p.household.ID, rule, a.todayStr())
	if err != nil {
		s.fail(w, "save rule", err)
		return
	}
	writeJSON(w, http.StatusOK, a.ruleJSON(saved))
}

type qtyIn struct {
	Amount float64 `json:"amount"`
	Unit   string  `json:"unit"`
}

func (s *server) apiDeleteAlways(w http.ResponseWriter, r *http.Request, p principal) {
	rule, err := s.storedRule(p, r.PathValue("id"))
	if err != nil {
		s.fail(w, "always rule", err)
		return
	}
	if rule == nil {
		writeError(w, http.StatusNotFound, "this article has no rule")
		return
	}
	if err := s.db.DeleteAlwaysRule(p.household.ID, rule.ArticleID); err != nil {
		s.fail(w, "delete rule", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *alwaysCtx) settingsJSON() (alwaysSettingsJSON, error) {
	rules, err := a.rules()
	if err != nil {
		return alwaysSettingsJSON{}, err
	}
	out := alwaysSettingsJSON{Time: a.set.At, Rules: []alwaysRuleJSON{}}
	if h := a.holiday(); h.On() {
		out.Holiday = &alwaysHolidayJSON{h.From, h.To}
	}
	for _, r := range rules {
		out.Rules = append(out.Rules, a.ruleJSON(r.Rule))
	}
	return out, nil
}

func (s *server) apiGetAlwaysSettings(w http.ResponseWriter, r *http.Request, p principal) {
	a, err := s.alwaysContext(p, s.langOf(r))
	if err != nil {
		s.fail(w, "always context", err)
		return
	}
	out, err := a.settingsJSON()
	if err != nil {
		s.fail(w, "always settings", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// apiPutAlwaysSettings sets the time of day and the holiday (null for none).
func (s *server) apiPutAlwaysSettings(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Time    string             `json:"time"`
		Holiday *alwaysHolidayJSON `json:"holiday"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	a, err := s.alwaysContext(p, s.langOf(r))
	if err != nil {
		s.fail(w, "always context", err)
		return
	}
	if !always.ValidTime(in.Time) {
		writeFieldError(w, http.StatusUnprocessableEntity, "time", "time must be a time of day such as 03:00")
		return
	}
	st := always.Settings{At: in.Time}
	if in.Holiday != nil {
		h := always.Holiday{From: in.Holiday.From, To: in.Holiday.To}
		if err := h.Validate(a.today); err != nil {
			fe, _ := err.(*always.FieldError)
			field := "holiday.from"
			if fe.Field == "to" {
				field = "holiday.to"
			}
			writeFieldError(w, http.StatusUnprocessableEntity, field, "holiday: "+fe.Code)
			return
		}
		st.Holiday = h
	}
	if err := s.db.SetAlwaysSettings(p.household.ID, st); err != nil {
		s.fail(w, "save always settings", err)
		return
	}
	a.set = st
	out, err := a.settingsJSON()
	if err != nil {
		s.fail(w, "always settings", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
