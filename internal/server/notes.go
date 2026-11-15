package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

// maxNoteRunes keeps the note to a line or two in the tomorrow tile.
const maxNoteRunes = 120

type dayNoteJSON struct {
	Day  string `json:"day"`
	Text string `json:"text"`
}

func (s *server) apiListDayNotes(w http.ResponseWriter, r *http.Request, p principal) {
	if err := s.db.PruneDayNotes(p.household.ID, s.householdNow(p.household).Format(store.DayLayout)); err != nil { // only today and later
		s.fail(w, "day notes", err)
		return
	}
	notes, err := s.db.DayNotes(p.household.ID)
	if err != nil {
		s.fail(w, "day notes", err)
		return
	}
	out := []dayNoteJSON{}
	for _, n := range notes {
		out = append(out, dayNoteJSON{n.Day, n.Text})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiSetDayNote(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	in.Text = strings.TrimSpace(in.Text)
	if utf8.RuneCountInString(in.Text) > maxNoteRunes || in.Text == "" {
		writeError(w, http.StatusUnprocessableEntity, "text: 1 to 120 characters")
		return
	}
	day := r.PathValue("day")
	if s.storeError(w, "day note", s.db.SetDayNote(p.household.ID, day, in.Text)) {
		return
	}
	n, _, err := s.db.DayNote(p.household.ID, day)
	if err != nil {
		s.fail(w, "day note", err)
		return
	}
	writeJSON(w, http.StatusOK, dayNoteJSON{day, n})
}

func (s *server) apiDeleteDayNote(w http.ResponseWriter, r *http.Request, p principal) {
	if s.storeError(w, "delete day note", s.db.SetDayNote(p.household.ID, r.PathValue("day"), "")) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleCalendarWaste(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	on := r.PostFormValue("waste") == "on"
	id := r.PathValue("id")
	switch err := s.db.SetCalendarWaste(p.household.ID, id, on); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	case err != nil:
		s.fail(w, "waste calendar", err)
	case on:
		s.redirectSaved(w, r, "/calendars", "flash.waste_on:"+id) // says when the next collection shows
	default:
		s.redirectSaved(w, r, "/calendars", "flash.waste_off")
	}
}

// wasteLookahead is how far the calendars page looks for the next collection.
const wasteLookahead = 30

// nextWaste is the next waste collection of a calendar: its day (midnight in the household's zone) and titles.
type nextWaste struct {
	Day    time.Time
	Titles []string
}

// nextWastes finds, for each of the given waste calendars, the next collection from tomorrow on within
// the next 30 days. A calendar without one is not in the result.
func (s *server) nextWastes(h store.Household, lang string, ids []string) (map[string]nextWaste, error) {
	out := map[string]nextWaste{}
	if len(ids) == 0 {
		return out, nil
	}
	now := s.householdNow(h)
	d1 := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	occs, err := s.occurrences(h, lang, d1, d1.AddDate(0, 0, wasteLookahead))
	if err != nil {
		return nil, err
	}
	sort.SliceStable(occs, func(i, j int) bool { return occs[i].Start.Before(occs[j].Start) })
	for _, id := range ids {
		var mine []calendar.Occurrence
		for _, o := range occs {
			if o.CalendarID == id {
				mine = append(mine, o)
			}
		}
		if len(mine) == 0 {
			continue
		}
		st := mine[0].Start.In(now.Location())
		day := time.Date(st.Year(), st.Month(), st.Day(), 0, 0, 0, 0, now.Location())
		if day.Before(d1) {
			day = d1
		}
		out[id] = nextWaste{Day: day, Titles: familyview.WasteTitles(mine, day, day.AddDate(0, 0, 1))}
	}
	return out, nil
}

// weekdayName is the full weekday in the language.
func (s *server) weekdayName(lang string, d time.Time) string {
	return s.i18n.T(lang, fmt.Sprintf("weekday.%d", int(d.Weekday())))
}

// dayLabel is "Monday 16/11".
func (s *server) dayLabel(lang string, d time.Time) string {
	return s.weekdayName(lang, d) + " " + d.Format("02/01")
}

// midnight is the start of now's day.
func midnight(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}

// shownFrom says when a note for the day starts to show: "today" or the weekday of the evening before, at
// 18:00. now is true when it shows already (the day is today, or the evening before has come).
// label names the evening before (weekdayName or dayLabel).
func (s *server) shownFrom(lang string, now, day time.Time, label func(string, time.Time) string) (when string, shownNow bool) {
	today := midnight(now)
	eve := day.AddDate(0, 0, -1)
	switch {
	case !day.After(today):
		return "", true
	case eve.Equal(today) && now.Hour() >= 18:
		return "", true
	case eve.Equal(today):
		return s.i18n.T(lang, "when.today"), false
	}
	return label(lang, eve), false
}

// parseDay reads a YYYY-MM-DD day in the household's zone.
func parseDay(loc *time.Location, v string) (time.Time, bool) {
	d, err := time.ParseInLocation(store.DayLayout, v, loc)
	return d, err == nil && d.Format(store.DayLayout) == v
}

// noteSavedText is the success line after a note was saved for the day (arg, YYYY-MM-DD).
func (s *server) noteSavedText(lang, arg string) string {
	h, err := s.db.HomeHousehold()
	if err != nil {
		return s.i18n.T(lang, "flash.note_saved")
	}
	now := s.householdNow(h)
	day, ok := parseDay(now.Location(), arg)
	if !ok {
		return s.i18n.T(lang, "flash.note_saved")
	}
	when, shownNow := s.shownFrom(lang, now, day, s.weekdayName)
	if shownNow {
		return s.i18n.T(lang, "flash.note_saved_now")
	}
	return s.i18n.Format(lang, i18n.Msg("flash.note_saved_from", "when", when))
}

// wasteOnText is the success line after a calendar was marked as waste collection (arg, its id): it says
// when the next collection shows on the display.
func (s *server) wasteOnText(lang, id string) string {
	plain := s.i18n.T(lang, "flash.waste_on")
	h, err := s.db.HomeHousehold()
	if err != nil {
		return plain
	}
	next, err := s.nextWastes(h, lang, []string{id})
	if err != nil || len(next) == 0 {
		return plain
	}
	// The collection can be weeks away: name the day of the evening before with its date, as the note does.
	now := s.householdNow(h)
	when, shownNow := s.shownFrom(lang, now, next[id].Day, s.dayLabel)
	if shownNow {
		return s.i18n.T(lang, "flash.waste_on_now")
	}
	return s.i18n.Format(lang, i18n.Msg("flash.waste_on_next", "when", when))
}

// noteRow is a planned note on Home.
type noteRow struct{ Day, Label, Text string }

// noteCard is the day note card on Home. Choice is today, tomorrow or other.
type noteCard struct {
	ShowToday                 bool
	TodayShort, TomorrowShort string
	Choice, Other, Text       string
	Count                     int
	Rows                      []noteRow
}

// noteForm is what a failed save keeps of the form.
type noteForm struct {
	Choice, Other, Text, Error, ErrorField string
}

func (s *server) renderHome(w http.ResponseWriter, r *http.Request, status int, person store.Person, h store.Household, form *noteForm) {
	data := map[string]any{"Person": person, "SyncStale": s.syncStale(h.ID), "HTTPSWarning": person.Role == store.RoleParent && s.httpsWarning(), "Update": s.parentUpdateNotice(person, h)}
	first, err := s.firstSteps(person, h, s.langOf(r))
	if err != nil {
		s.fail(w, "first steps", err)
		return
	}
	data["FirstSteps"] = first
	if person.Role == store.RoleParent {
		lang := s.langOf(r)
		now := s.householdNow(h)
		if err := s.db.PruneDayNotes(h.ID, now.Format(store.DayLayout)); err != nil { // only today and later
			s.fail(w, "day notes", err)
			return
		}
		notes, err := s.db.DayNotes(h.ID)
		if err != nil {
			s.fail(w, "day notes", err)
			return
		}
		short := func(d time.Time) string {
			return s.i18n.T(lang, fmt.Sprintf("weekday.short.%d", int(d.Weekday()))) + " " + d.Format("02/01")
		}
		card := noteCard{ShowToday: now.Hour() < tomorrowFromHour, TodayShort: short(now), TomorrowShort: short(now.AddDate(0, 0, 1)), Choice: "tomorrow"}
		for _, n := range notes {
			if day, ok := parseDay(now.Location(), n.Day); ok {
				card.Rows = append(card.Rows, noteRow{Day: n.Day, Label: s.dayLabel(lang, day), Text: n.Text})
			}
		}
		if form != nil {
			card.Choice, card.Other, card.Text = form.Choice, form.Other, form.Text
			if card.Choice == "today" && !card.ShowToday {
				card.Choice = "tomorrow"
			}
			data["Error"], data["ErrorField"] = form.Error, form.ErrorField
		}
		card.Count = utf8.RuneCountInString(strings.TrimSpace(card.Text))
		data["Note"] = card
	}
	s.render(w, r, status, "home.html", data)
}

// tomorrowFromHour is when the evening before begins: a note for the next day shows from then.
const tomorrowFromHour = 18

// handleNoteSave sets the note of a day from the card on Home. An empty note is an error at the field: a
// note is removed on its own page.
func (s *server) handleNoteSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	now := s.householdNow(p.household)
	loc := now.Location()
	f := noteForm{Choice: r.PostFormValue("day"), Other: strings.TrimSpace(r.PostFormValue("other")), Text: r.PostFormValue("text")}
	day := ""
	switch f.Choice {
	case "today":
		day = now.Format(store.DayLayout)
		if now.Hour() >= tomorrowFromHour {
			f.Error, f.ErrorField = "home.note.error.late", "day"
		}
	case "tomorrow":
		day = now.AddDate(0, 0, 1).Format(store.DayLayout)
	case "other":
		day = f.Other
	default: // a day given as YYYY-MM-DD
		day, f.Other, f.Choice = f.Choice, f.Choice, "other"
	}
	if f.Error == "" {
		d, valid := parseDay(loc, day)
		switch {
		case !valid && f.Choice == "other":
			f.Error, f.ErrorField = "home.note.error.day", "other"
		case !valid:
			f.Choice, f.Error, f.ErrorField = "tomorrow", "home.note.error.day", "day"
		case d.Before(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)):
			f.Error, f.ErrorField = "home.note.error.day", "other"
		}
	}
	text := strings.TrimSpace(f.Text)
	switch {
	case f.Error != "":
	case text == "":
		f.Error, f.ErrorField = "home.note.error.empty", "text"
	case utf8.RuneCountInString(text) > maxNoteRunes:
		f.Error, f.ErrorField = "home.note.error.long", "text"
	}
	if f.Error != "" {
		s.renderHome(w, r, http.StatusUnprocessableEntity, p.person, p.household, &f)
		return
	}
	if err := s.db.SetDayNote(p.household.ID, day, text); err != nil {
		s.fail(w, "day note", err)
		return
	}
	s.redirectSaved(w, r, "/", "flash.note_saved:"+day)
}

// notePage loads the note a request names: the day, the household's today and the note's text.
func (s *server) notePage(w http.ResponseWriter, r *http.Request) (p principal, day time.Time, text string, ok bool) {
	p, ok = s.parentPage(w, r)
	if !ok {
		return p, day, "", false
	}
	now := s.householdNow(p.household)
	day, valid := parseDay(now.Location(), r.PathValue("day"))
	if !valid {
		http.NotFound(w, r)
		return p, day, "", false
	}
	if day.Before(midnight(now)) { // not pruned yet
		http.NotFound(w, r)
		return p, day, "", false
	}
	text, found, err := s.db.DayNote(p.household.ID, day.Format(store.DayLayout))
	switch {
	case err != nil:
		s.fail(w, "day note", err)
		return p, day, "", false
	case !found:
		http.NotFound(w, r)
		return p, day, "", false
	}
	return p, day, text, true
}

func (s *server) renderNote(w http.ResponseWriter, r *http.Request, status int, p principal, day time.Time, text, errKey string) {
	lang := s.langOf(r)
	now := s.householdNow(p.household)
	title := fmt.Sprintf(s.i18n.T(lang, "note.title"), s.weekdayName(lang, day), day.Format("02/01"))
	lead := s.i18n.T(lang, "note.lead_now")
	if when, shownNow := s.shownFrom(lang, now, day, s.weekdayName); !shownNow {
		lead = s.i18n.Format(lang, i18n.Msg("note.lead_from", "when", when))
	}
	data := map[string]any{"Title": title, "Lead": lead, "Day": day.Format(store.DayLayout), "Text": text, "Count": utf8.RuneCountInString(strings.TrimSpace(text))}
	if errKey != "" {
		data["Error"], data["ErrorField"] = errKey, "text"
	}
	s.render(w, r, status, "note.html", data)
}

func (s *server) handleNotePage(w http.ResponseWriter, r *http.Request) {
	if p, day, text, ok := s.notePage(w, r); ok {
		s.renderNote(w, r, http.StatusOK, p, day, text, "")
	}
}

// handleNoteChange saves the text of a planned note; the day stays.
func (s *server) handleNoteChange(w http.ResponseWriter, r *http.Request) {
	p, day, _, ok := s.notePage(w, r)
	if !ok {
		return
	}
	posted := r.PostFormValue("text")
	text := strings.TrimSpace(posted)
	errKey := ""
	switch {
	case text == "":
		errKey = "home.note.error.empty"
	case utf8.RuneCountInString(text) > maxNoteRunes:
		errKey = "home.note.error.long"
	}
	if errKey != "" {
		s.renderNote(w, r, http.StatusUnprocessableEntity, p, day, posted, errKey)
		return
	}
	if err := s.db.SetDayNote(p.household.ID, day.Format(store.DayLayout), text); err != nil {
		s.fail(w, "day note", err)
		return
	}
	s.redirectSaved(w, r, "/", "flash.note_saved:"+day.Format(store.DayLayout))
}

// handleNoteDelete removes a planned note, after the confirmation on its page.
func (s *server) handleNoteDelete(w http.ResponseWriter, r *http.Request) {
	p, day, _, ok := s.notePage(w, r)
	if !ok {
		return
	}
	if err := s.db.SetDayNote(p.household.ID, day.Format(store.DayLayout), ""); err != nil {
		s.fail(w, "day note", err)
		return
	}
	s.redirectSaved(w, r, "/", "flash.note_removed")
}
