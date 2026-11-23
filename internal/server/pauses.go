package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/streak"
	"github.com/hejmo-home/hejmo/internal/task"
)

// Pause days (ticket 294): parents enter them under Tasks. The pages are
// /tasks/pauses/new and /tasks/pauses/{id}; the forms of a change and of the
// removal post to /tasks/pauses/edit/{id} and /tasks/pauses/remove/{id},
// because POST /tasks/pauses/{id} would clash with POST /tasks/{id}/done and
// the other task routes.

// pauseRow is a pause as the Tasks page lists it (a rowLink).
type pauseRow struct {
	Href, Title string
	Lines       []pauseLine
	Flag        *pauseFlag
}

type pauseLine struct {
	Text   string
	Whole  bool
	People []personChip
}

type pauseFlag struct{ Kind, Text, After string }

// pauseDay is a day for a row: "Wed 25/11" and, in another year than today's,
// with the year.
func (s *server) pauseDay(lang string, f store.DateFormat, day, today string) string {
	d, err := time.Parse(task.DateLayout, day)
	if err != nil {
		return day
	}
	dayName := s.i18n.T(lang, "tasks.weekday."+[]string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}[d.Weekday()])
	if len(day) >= 4 && len(today) >= 4 && day[:4] != today[:4] {
		return dayName + " " + datefmt.Date(f, d)
	}
	return dayName + " " + datefmt.Short(f, d)
}

// pauseRange says the days of a pause: one day, or "from to".
func (s *server) pauseRange(lang string, f store.DateFormat, pa streak.Pause, today string) string {
	from := s.pauseDay(lang, f, pa.From, today)
	if pa.From == pa.To {
		return from
	}
	return s.tr(lang, "pause.range", "from", from, "to", s.pauseDay(lang, f, pa.To, today))
}

// pauseWho says for whom a pause is: names joined, or the whole family.
func (s *server) pauseWho(lang string, pa streak.Pause, people []store.Person) string {
	if len(pa.People) == 0 {
		return s.i18n.T(lang, "chips.whole_family")
	}
	var names []string
	for _, p := range people {
		if slices.Contains(pa.People, p.ID) {
			names = append(names, p.Name)
		}
	}
	return s.joinWords(lang, names)
}

// pauseRows lists the pauses of the household for the Tasks page: those that
// run now or lie ahead, earliest first, and the past ones, latest first.
func (s *server) pauseRows(lang string, p principal, today string) (current, past []pauseRow, lastPast string, err error) {
	list, err := s.db.Pauses(p.household.ID)
	if err != nil {
		return nil, nil, "", err
	}
	people, err := s.db.People(p.household.ID)
	if err != nil {
		return nil, nil, "", err
	}
	chips := chipsOf(people)
	row := func(pa streak.Pause) pauseRow {
		r := pauseRow{Href: "/tasks/pauses/" + pa.ID, Title: s.pauseRange(lang, p.household.DateFormat, pa, today)}
		line := pauseLine{Whole: len(pa.People) == 0}
		for _, c := range chips {
			if slices.Contains(pa.People, c.ID) {
				line.People = append(line.People, c)
			}
		}
		r.Lines = []pauseLine{line}
		if pa.From <= today && today <= pa.To {
			r.Flag = &pauseFlag{Kind: "paused", Text: s.i18n.T(lang, "pause.running")}
		}
		return r
	}
	for _, pa := range list {
		if pa.To >= today {
			current = append(current, row(pa))
		}
	}
	for i := len(list) - 1; i >= 0; i-- {
		if list[i].To < today {
			if lastPast == "" {
				lastPast = s.pauseRange(lang, p.household.DateFormat, list[i], today)
			}
			past = append(past, row(list[i]))
		}
	}
	return current, past, lastPast, nil
}

// pauseFormData collects what the pause form shows: the people to pick from,
// the pause being changed (nil for a new one) and the posted values with their error.
func (s *server) pauseFormData(lang string, p principal, existing *streak.Pause, form map[string]any) (map[string]any, error) {
	people, err := s.db.People(p.household.ID)
	if err != nil {
		return nil, err
	}
	data := map[string]any{"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "People": chipsOf(people), "New": existing == nil,
		"Error": form["Error"], "ErrorField": form["ErrorField"], "ErrorText": form["ErrorText"]}
	data["From"], data["To"], data["Selected"] = form["From"], form["To"], form["Selected"]
	switch {
	case existing == nil:
		data["Title"] = s.i18n.T(lang, "pause.new.title")
		data["Action"] = "/tasks/pauses/new"
	default:
		who := s.pauseWho(lang, *existing, people)
		data["Title"] = s.tr(lang, "pause.edit.title", "name", who)
		if len(existing.People) == 0 {
			data["Title"] = s.i18n.T(lang, "pause.edit.title.whole")
		}
		data["Action"] = "/tasks/pauses/edit/" + existing.ID
		data["RemoveAction"] = "/tasks/pauses/remove/" + existing.ID
		data["RemoveConfirm"] = s.tr(lang, "pause.remove.confirm", "name", who)
		data["RemoveExplain"] = s.tr(lang, "pause.remove.explain", "name", who, "range", s.pauseRange(lang, p.household.DateFormat, *existing, s.today(p)))
		data["PauseID"] = existing.ID
	}
	return data, nil
}

func (s *server) renderPause(w http.ResponseWriter, r *http.Request, p principal, status int, existing *streak.Pause, form map[string]any) {
	lang := s.langOf(r)
	data, err := s.pauseFormData(lang, p, existing, form)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	s.render(w, r, status, "pause.html", data)
}

func (s *server) handlePauseNew(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	today := s.today(p)
	form := map[string]any{"From": today, "To": today, "Selected": []string{}}
	if who := r.URL.Query().Get("person"); who != "" {
		if _, err := s.db.Person(p.household.ID, who); err == nil {
			form["Selected"] = []string{who}
		}
	}
	s.renderPause(w, r, p, http.StatusOK, nil, form)
}

func (s *server) handlePauseEditPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	pa, err := s.db.Pause(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "pause", err)
		return
	}
	s.renderPause(w, r, p, http.StatusOK, &pa, map[string]any{"From": pa.From, "To": pa.To, "Selected": pa.People})
}

// pauseFromForm reads the days and the people of the form. The whole family
// is chosen by its chip, or by choosing nobody.
func pauseFromForm(r *http.Request) streak.Pause {
	_ = r.ParseForm()
	pa := streak.Pause{From: strings.TrimSpace(r.PostFormValue("from")), To: strings.TrimSpace(r.PostFormValue("to"))}
	if r.PostFormValue("whole_family") == "" {
		pa.People = r.PostForm["people"]
	}
	return pa
}

// pauseFailure shows the form again with the error at its field.
func (s *server) pauseFailure(w http.ResponseWriter, r *http.Request, p principal, existing *streak.Pause, in streak.Pause, err error) {
	lang := s.langOf(r)
	form := map[string]any{"From": in.From, "To": in.To, "Selected": in.People}
	var pe *streak.PauseError
	var bad *task.InvalidError
	switch {
	case errors.As(err, &pe):
		form["ErrorField"] = pe.Field
		switch pe.Code {
		case "order":
			from := in.From
			if d, err := time.Parse(task.DateLayout, in.From); err == nil {
				from = datefmt.Date(p.household.DateFormat, d)
			}
			form["ErrorText"], form["Error"] = s.tr(lang, "pause.err.order", "from", from), "pause.err.order"
		case "long":
			form["ErrorText"], form["Error"] = s.i18n.T(lang, "pause.err.long"), "pause.err.long"
		default:
			form["ErrorText"], form["Error"] = s.i18n.T(lang, "pause.err.date"), "pause.err.date"
		}
	case errors.Is(err, store.ErrInvalid) && errors.As(err, &bad):
		form["ErrorField"], form["ErrorText"], form["Error"] = "people", s.i18n.T(lang, "pause.err.people"), "pause.err.people"
	default:
		s.fail(w, "save pause", err)
		return
	}
	s.renderPause(w, r, p, http.StatusUnprocessableEntity, existing, form)
}

// pauseSaved redirects to the Tasks page with the success line that names who
// and which days.
func (s *server) pauseSaved(w http.ResponseWriter, r *http.Request, p principal, pa streak.Pause) {
	lang := s.langOf(r)
	people, _ := s.db.People(p.household.ID)
	s.redirectSavedFields(w, r, "/tasks", "flash.pause_saved", s.pauseWho(lang, pa, people), s.pauseRange(lang, p.household.DateFormat, pa, s.today(p)))
}

func (s *server) handlePauseCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	in := pauseFromForm(r)
	saved, err := s.db.SavePause(p.household.ID, in, p.person.ID)
	if err != nil {
		s.pauseFailure(w, r, p, nil, in, err)
		return
	}
	s.pauseSaved(w, r, p, saved)
}

func (s *server) handlePauseUpdate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	existing, err := s.db.Pause(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "pause", err)
		return
	}
	in := pauseFromForm(r)
	in.ID = existing.ID
	saved, err := s.db.UpdatePause(p.household.ID, in)
	if err != nil {
		s.pauseFailure(w, r, p, &existing, in, err)
		return
	}
	s.pauseSaved(w, r, p, saved)
}

func (s *server) handlePauseRemove(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	switch err := s.db.DeletePause(p.household.ID, r.PathValue("id")); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, "remove pause", err)
	default:
		s.redirectSaved(w, r, "/tasks", "flash.pause_removed")
	}
}

// REST: GET/POST /tasks/pauses and PATCH/DELETE /tasks/pauses/{id}.

type pauseJSON struct {
	ID          string   `json:"id"`
	From        string   `json:"from"`
	To          string   `json:"to"`
	PersonIDs   []string `json:"person_ids"`
	WholeFamily bool     `json:"whole_family"`
}

func pauseToJSON(pa streak.Pause) pauseJSON {
	return pauseJSON{ID: pa.ID, From: pa.From, To: pa.To, PersonIDs: nonNil(pa.People), WholeFamily: len(pa.People) == 0}
}

func (s *server) pauseError(w http.ResponseWriter, what string, err error) {
	var pe *streak.PauseError
	var bad *task.InvalidError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, what+" not found")
	case errors.As(err, &pe):
		writeFieldError(w, http.StatusUnprocessableEntity, pe.Field, err.Error())
	case errors.Is(err, store.ErrInvalid) && errors.As(err, &bad):
		writeFieldError(w, http.StatusUnprocessableEntity, bad.Field, err.Error())
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		s.fail(w, what, err)
	}
}

func (s *server) apiListPauses(w http.ResponseWriter, _ *http.Request, p principal) {
	list, err := s.db.Pauses(p.household.ID)
	if err != nil {
		s.fail(w, "list pauses", err)
		return
	}
	out := make([]pauseJSON, 0, len(list))
	for _, pa := range list {
		out = append(out, pauseToJSON(pa))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiCreatePause(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		From      string   `json:"from"`
		To        string   `json:"to"`
		PersonIDs []string `json:"person_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	saved, err := s.db.SavePause(p.household.ID, streak.Pause{From: in.From, To: in.To, People: in.PersonIDs}, p.person.ID)
	if err != nil {
		s.pauseError(w, "pause", err)
		return
	}
	writeJSON(w, http.StatusCreated, pauseToJSON(saved))
}

func (s *server) apiPatchPause(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		From      *string   `json:"from"`
		To        *string   `json:"to"`
		PersonIDs *[]string `json:"person_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	cur, err := s.db.Pause(p.household.ID, r.PathValue("id"))
	if err != nil {
		s.pauseError(w, "pause", err)
		return
	}
	if in.From != nil {
		cur.From = *in.From
	}
	if in.To != nil {
		cur.To = *in.To
	}
	if in.PersonIDs != nil {
		cur.People = *in.PersonIDs
	}
	saved, err := s.db.UpdatePause(p.household.ID, cur)
	if err != nil {
		s.pauseError(w, "pause", err)
		return
	}
	writeJSON(w, http.StatusOK, pauseToJSON(saved))
}

func (s *server) apiDeletePause(w http.ResponseWriter, r *http.Request, p principal) {
	if err := s.db.DeletePause(p.household.ID, r.PathValue("id")); err != nil {
		s.pauseError(w, "pause", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
