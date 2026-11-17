package server

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Lists on the display (ticket 6.2, ADR 0015, preview-33): a lists view with a
// rail of every list and the chosen list, where entries are ticked off and
// added with large touch targets. It stays current like the personal view,
// returns to the family view after 30 seconds without interaction, and sends a
// display whose list is gone back to the family view.

// listsViewData is what the template "listsView" is built from.
type listsViewData struct {
	Rail                    []listView
	Current                 listView
	Meta                    string
	Open, Ticked            []entryView
	Clock, ClockZone        string
	ClockFormat             string
	Text                    string // the typed text kept in the field after an error
	Error                   string // translated field error, "" without one
	Success                 string // the entry that was just added, named in a line
	AllDone                 bool   // there are entries and all of them are ticked off
	Empty                   bool   // the list has no entries
	TickError, AddErrorText string
	View                    string // "tiles" or "list": the view of this display
	Tiles                   bool
	ViewLine                string // the success line after switching the view
	ViewError               string // the view could not be saved
	FreeText                []freeTextPerson
}

// errNoList means the list of a lists view is gone (deleted meanwhile).
var errNoList = errors.New("no such list")

// listsViewOptions say what the view shows beyond the list itself.
type listsViewOptions struct {
	Added string // id of the entry just added, "" if none
	Text  string // typed text to keep in the field
	Error string // field error, "" if none

	ViewSaved bool // the view of the display was just switched: say so
	ViewError bool // the choice could not be saved
}

// listsView builds the view of one list for a display.
func (s *server) listsView(h store.Household, d store.Display, lang, listID string, o listsViewOptions) (listsViewData, error) {
	lists, err := s.db.Lists(h.ID)
	if err != nil {
		return listsViewData{}, err
	}
	counts, err := s.db.EntryCounts(h.ID)
	if err != nil {
		return listsViewData{}, err
	}
	var v listsViewData
	var cur *list.List
	for i, l := range lists {
		lv := s.newListView(lang, l, counts[l.ID])
		v.Rail = append(v.Rail, lv)
		if l.ID == listID {
			cur, v.Current = &lists[i], lv
		}
	}
	if cur == nil {
		return v, errNoList
	}
	es, err := s.db.Entries(h.ID, cur.ID)
	if err != nil {
		return v, err
	}
	c := countOf(es)
	v.Current = s.newListView(lang, *cur, c)
	view, err := s.db.DisplayListView(h.ID, d.ID)
	if err != nil {
		return v, err
	}
	v.View, v.Tiles = string(view), view == list.Tiles
	for _, e := range es {
		ev := entryView{ID: e.ID, Text: e.Text, Done: e.Done}
		switch {
		case e.Done:
			v.Ticked = append(v.Ticked, ev)
		case e.ID == o.Added:
			ev.Added = true
			v.Open = append(v.Open, ev)
			v.Success = e.Text
		default:
			v.Open = append(v.Open, ev)
		}
	}
	lk, err := s.newListLooks(principal{household: h}, lang, "/display/articles/")
	if err != nil {
		return v, err
	}
	v.FreeText = lk.freeTextNote(lk.apply(v.Open, v.Ticked))
	switch {
	case o.ViewError:
		v.ViewError = s.viewErrorText(lang, view)
	case o.ViewSaved:
		v.ViewLine = s.i18n.Format(lang, i18n.Msg("display.lists.view_saved."+string(view), "name", d.Name))
	}
	v.Meta = s.listMeta(lang, *cur, v.Current.State, false)
	v.Empty = len(es) == 0
	v.AllDone = len(es) > 0 && c.Open == 0
	v.Text, v.Error = o.Text, o.Error
	now := s.householdNow(h)
	v.Clock, v.ClockZone, v.ClockFormat = familyview.ClockText(now, h.TimeFormat), now.Location().String(), familyview.ClockFormat(h.TimeFormat)
	v.TickError = s.i18n.T(lang, "personal.tick_error")
	if v.Tiles {
		v.TickError = s.i18n.T(lang, "lists.tile.error")
	}
	v.AddErrorText = s.i18n.T(lang, "display.lists.add_error")
	return v, nil
}

// renderListsView renders the lists view markup and its version (the markup
// without the clock, like the other views of a display).
func (s *server) renderListsView(h store.Household, d store.Display, lang, listID string, o listsViewOptions) (template.HTML, string, error) {
	v, err := s.listsView(h, d, lang, listID, o)
	if err != nil {
		return "", "", err
	}
	clock := v.Clock
	v.Clock = clockMark
	return s.renderVersioned("listsView", lang, v, clock)
}

// listsReturnSeconds is how long a display stays on the lists view without a
// tap or keystroke before it returns to the family view (6.2).
const listsReturnSeconds = personalReturnSeconds

// listsURL is the lists view of a list on a display.
func listsURL(id string) string { return "/display/lists/" + url.PathEscape(id) }

// handleDisplayLists opens the first list in the household's order. The display
// does not remember a choice. Without a list there is nothing to open.
func (s *server) handleDisplayLists(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.pairedDisplayOrRedirect(w, r); !ok {
		return
	}
	h, err := s.db.HomeHousehold()
	if err != nil {
		s.fail(w, "household", err)
		return
	}
	lists, err := s.db.Lists(h.ID)
	if err != nil {
		s.fail(w, "lists", err)
		return
	}
	if len(lists) == 0 {
		http.Redirect(w, r, "/display", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, listsURL(lists[0].ID), http.StatusSeeOther)
}

// listsPage renders the whole page of a list, with the status of the answer.
func (s *server) listsPage(w http.ResponseWriter, r *http.Request, d store.Display, h store.Household, id string, status int, o listsViewOptions) {
	view, version, err := s.renderListsView(h, d, s.displayLang(r), id, o)
	if errors.Is(err, errNoList) {
		// Gone meanwhile: back to the display, never to a bare error page
		// (it has no display.js and would leave the kiosk stuck).
		http.Redirect(w, r, "/display", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.fail(w, "lists view", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, status, "display_lists.html", map[string]any{
		"Display": d, "PollSeconds": displayPollSeconds, "View": view, "Version": version,
		"Theme": s.displayTheme(h, d), "ReturnSeconds": listsReturnSeconds,
		"ViewURL": listsURL(id) + "/view", "EventsURL": "/display/events?list=" + url.QueryEscape(id),
	})
}

// handleDisplayList is the lists view of one list. ?added= names the entry
// that was just added: its row is framed and a line says so.
func (s *server) handleDisplayList(w http.ResponseWriter, r *http.Request) {
	d, ok := s.pairedDisplayOrRedirect(w, r)
	if !ok {
		return
	}
	h, err := s.db.HomeHousehold()
	if err != nil {
		s.fail(w, "household", err)
		return
	}
	s.listsPage(w, r, d, h, r.PathValue("id"), http.StatusOK, listsViewOptions{Added: r.URL.Query().Get("added"), ViewSaved: r.URL.Query().Get("view") == "saved"})
}

// handleDisplayListView serves the lists view markup alone, for the display's
// script to swap in (partial update). 404 when the list is gone: the script
// then returns to the family view.
func (s *server) handleDisplayListView(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.pairedDisplay(w, r)
	if !ok {
		return
	}
	markup, version, err := s.renderListsView(h, d, s.displayLang(r), r.PathValue("id"), listsViewOptions{Added: r.URL.Query().Get("added")})
	if errors.Is(err, errNoList) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "lists view", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Hejmo-View", version)
	_, _ = w.Write([]byte(markup))
}

// displayListOrRedirect finds the paired display and the list of a request. A
// display that is not paired, or a list that is gone, goes to the display
// address.
func (s *server) displayListOrRedirect(w http.ResponseWriter, r *http.Request) (store.Display, store.Household, list.List, bool) {
	d, ok := s.pairedDisplayOrRedirect(w, r)
	if !ok {
		return d, store.Household{}, list.List{}, false
	}
	h, err := s.db.HomeHousehold()
	if err != nil {
		s.fail(w, "household", err)
		return d, h, list.List{}, false
	}
	l, err := s.db.List(h.ID, r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.Redirect(w, r, "/display", http.StatusSeeOther)
		return d, h, l, false
	case err != nil:
		s.fail(w, "list", err)
		return d, h, l, false
	}
	return d, h, l, true
}

// handleDisplayEntryAdd adds an entry to a list. Anyone at a display may; the
// list view then shows the entry named and framed. With partial=1 (the
// display's script) the answer is the markup alone: 200 with the new view, or
// 422 with the field error.
func (s *server) handleDisplayEntryAdd(w http.ResponseWriter, r *http.Request) {
	d, h, l, ok := s.displayListOrRedirect(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	text := r.PostFormValue("text")
	partial := r.PostFormValue("partial") == "1"
	e, err := s.db.AddEntry(h.ID, l.ID, text)
	switch {
	case err == nil && partial:
		markup, version, rerr := s.renderListsView(h, d, s.displayLang(r), l.ID, listsViewOptions{Added: e.ID})
		if rerr != nil {
			s.fail(w, "lists view", rerr)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Hejmo-View", version)
		w.Header().Set("X-Hejmo-Added", e.ID)
		_, _ = w.Write([]byte(markup))
	case err == nil:
		http.Redirect(w, r, listsURL(l.ID)+"?added="+url.QueryEscape(e.ID), http.StatusSeeOther)
	case errors.Is(err, store.ErrInvalid):
		key := "lists.error.text"
		if strings.TrimSpace(text) == "" {
			key = "display.lists.error.text_empty"
		}
		o := listsViewOptions{Text: text, Error: s.i18n.T(s.displayLang(r), key)}
		if partial {
			markup, version, rerr := s.renderListsView(h, d, s.displayLang(r), l.ID, o)
			if rerr != nil {
				s.fail(w, "lists view", rerr)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Hejmo-View", version)
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(markup))
			return
		}
		s.listsPage(w, r, d, h, l.ID, http.StatusUnprocessableEntity, o)
	default:
		s.fail(w, "add entry", err)
	}
}

// handleDisplayEntryDone ticks an entry off or open again on a display. Without
// the parent PIN a display may do this for every entry (ADR 0015).
func (s *server) handleDisplayEntryDone(w http.ResponseWriter, r *http.Request) {
	_, h, l, ok := s.displayListOrRedirect(w, r)
	if !ok {
		return
	}
	back := listsURL(l.ID)
	var done bool
	switch r.PostFormValue("done") {
	case "1":
		done = true
	case "0":
	default:
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	switch err := s.db.SetEntryDone(h.ID, l.ID, r.PathValue("entry"), done); {
	case err == nil, errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrInvalid):
		// Deleted meanwhile: show the current state.
		http.Redirect(w, r, back, http.StatusSeeOther)
	default:
		s.fail(w, "tick entry", err)
	}
}

// handleDisplayTickedDelete deletes the ticked-off entries of a list. Allowed
// on a display without the parent PIN (ADR 0015).
func (s *server) handleDisplayTickedDelete(w http.ResponseWriter, r *http.Request) {
	_, h, l, ok := s.displayListOrRedirect(w, r)
	if !ok {
		return
	}
	if _, err := s.db.DeleteCompleted(h.ID, l.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, "delete ticked-off entries", err)
		return
	}
	http.Redirect(w, r, listsURL(l.ID), http.StatusSeeOther)
}

// handleDisplayListViewChoice remembers the view of lists of this display,
// tiles or the list. A display is not a person: anyone at it may switch it
// without a PIN, and the choice holds for everyone who stands in front of it.
func (s *server) handleDisplayListViewChoice(w http.ResponseWriter, r *http.Request) {
	d, ok := s.pairedDisplayOrRedirect(w, r)
	if !ok {
		return
	}
	h, err := s.db.HomeHousehold()
	if err != nil {
		s.fail(w, "household", err)
		return
	}
	_ = r.ParseForm()
	l, err := s.db.List(h.ID, r.PostFormValue("list"))
	if errors.Is(err, store.ErrNotFound) {
		http.Redirect(w, r, "/display", http.StatusSeeOther)
		return
	} else if err != nil {
		s.fail(w, "list", err)
		return
	}
	view, valid := list.ParseView(r.PostFormValue("view"))
	if !valid {
		s.listsPage(w, r, d, h, l.ID, http.StatusUnprocessableEntity, listsViewOptions{ViewError: true})
		return
	}
	if err := s.db.SetDisplayListView(h.ID, d.ID, view); err != nil {
		s.fail(w, "save display list view", err)
		return
	}
	http.Redirect(w, r, listsURL(l.ID)+"?view=saved", http.StatusSeeOther)
}

// handleDisplayArticlePicture serves the picture of an own article to a paired
// display, which has no session: the tiles show it.
func (s *server) handleDisplayArticlePicture(w http.ResponseWriter, r *http.Request) {
	_, h, ok := s.pairedDisplay(w, r)
	if !ok {
		return
	}
	b, err := s.db.ArticlePicture(h.ID, r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable") // the address changes with the picture
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}
