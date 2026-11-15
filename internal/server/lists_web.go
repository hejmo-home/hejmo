package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

type listView struct {
	ID, Name, Color, Symbol, Sort string
	Slot, Open, Done, Total       int
	State                         string // what the list holds, in words
}

// entryView is one entry; First and Last tell whether no other open entry
// comes before or after it, which is what moving up and down depends on.
type entryView struct {
	ID, Text string
	Done     bool
	First    bool
	Last     bool
}

func (s *server) newListView(lang string, l list.List, c store.EntryCount) listView {
	return listView{State: s.listState(lang, c.Open, c.Done), ID: l.ID, Name: l.Name, Color: l.Color, Symbol: l.Symbol, Sort: string(l.Sort), Slot: list.Slot(l.Color), Open: c.Open, Done: c.Done, Total: c.Open + c.Done}
}

// listFormValues is what the new-list and list-settings forms show.
func listFormValues(l list.List) map[string]any {
	return map[string]any{"Name": l.Name, "Color": l.Color, "Symbol": l.Symbol, "Sort": string(l.Sort)}
}

// listState says what a list holds: "3 open", "5 open, 2 ticked off", "All 5
// ticked off" or "No entries yet".
func (s *server) listState(lang string, open, done int) string {
	switch {
	case open == 0 && done == 0:
		return s.i18n.T(lang, "lists.empty")
	case open == 0:
		return fmt.Sprintf(s.i18n.T(lang, "lists.all_done"), done)
	case done == 0:
		return fmt.Sprintf(s.i18n.T(lang, "lists.open_count"), open)
	}
	return fmt.Sprintf(s.i18n.T(lang, "lists.open_done_count"), open, done)
}

// listMeta is the line under a list's title: how it is sorted and what it
// holds; a child gets a hint for the boxes instead of the sorting.
func (s *server) listMeta(lang string, l list.List, state string, child bool) string {
	switch {
	case !child:
		return fmt.Sprintf(s.i18n.T(lang, "lists.meta"), s.i18n.T(lang, "lists.sort."+string(l.Sort)+".hint"), state)
	case state == s.i18n.T(lang, "lists.empty"):
		return fmt.Sprintf(s.i18n.T(lang, "lists.meta.plain"), state)
	}
	return fmt.Sprintf(s.i18n.T(lang, "lists.meta.child"), state)
}

// listSummary names the colour, symbol and order of a list form in one line, so
// a closed disclosure shows what is chosen. Choices the box does not know are
// left out.
func (s *server) listSummary(r *http.Request, form map[string]any) string {
	lang := s.langOf(r)
	var parts []string
	if c, _ := form["Color"].(string); slices.Contains(list.Colors, c) {
		parts = append(parts, s.i18n.T(lang, "lists.color."+c))
	}
	if sym, _ := form["Symbol"].(string); slices.Contains(list.Symbols, sym) {
		parts = append(parts, s.i18n.T(lang, "lists.summary.symbol."+sym))
	}
	if so, _ := form["Sort"].(string); so == string(list.Manual) || so == string(list.Alphabetical) {
		parts = append(parts, s.i18n.T(lang, "lists.summary.sort."+so))
	}
	return strings.Join(parts, ", ")
}

func (s *server) listChoices() map[string]any {
	return map[string]any{"Colors": listColors(), "Symbols": list.Symbols, "Sorts": []string{string(list.Manual), string(list.Alphabetical)}}
}

type colorChoice struct {
	Name string
	Slot int
}

func listColors() []colorChoice {
	out := make([]colorChoice, 0, len(list.Colors))
	for i, c := range list.Colors {
		out = append(out, colorChoice{c, i + 1})
	}
	return out
}

func (s *server) renderLists(w http.ResponseWriter, r *http.Request, p principal, status int, form map[string]any, errKey, errField string) {
	lists, err := s.db.Lists(p.household.ID)
	if err != nil {
		s.fail(w, "lists", err)
		return
	}
	counts, err := s.db.EntryCounts(p.household.ID)
	if err != nil {
		s.fail(w, "list counts", err)
		return
	}
	views := make([]listView, 0, len(lists))
	for _, l := range lists {
		views = append(views, s.newListView(s.langOf(r), l, counts[l.ID]))
	}
	if form == nil { // the next list gets the next colour and symbol, as a person does
		n := len(lists)
		form = listFormValues(list.List{Color: list.Colors[n%len(list.Colors)], Symbol: list.Symbols[n%len(list.Symbols)], Sort: list.Manual})
	}
	data := s.listChoices()
	data["Person"], data["Lists"], data["Form"] = p.person, views, form
	data["Summary"] = s.listSummary(r, form)
	data["Child"] = p.person.Role == store.RoleChild
	data["SyncStale"] = s.syncStale(p.household.ID)
	data["Error"], data["ErrorField"] = errKey, errField
	s.render(w, r, status, "lists.html", data)
}

func (s *server) handleListsPage(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.memberPage(w, r); ok {
		s.renderLists(w, r, p, http.StatusOK, nil, "", "")
	}
}

// listFromForm reads the list fields of a form.
func listFromForm(r *http.Request) list.List {
	_ = r.ParseForm()
	return list.List{Name: r.PostFormValue("name"), Color: r.PostFormValue("color"), Symbol: r.PostFormValue("symbol"), Sort: list.Sort(r.PostFormValue("sort"))}
}

// listErrorField is the field an invalid list or entry is about, or "".
func listErrorField(err error) string {
	var bad *list.InvalidError
	if errors.As(err, &bad) {
		return bad.Field
	}
	return ""
}

func listErrorKey(field string) string {
	switch field {
	case "name", "color", "symbol", "sort", "text":
		return "lists.error." + field
	}
	return "lists.error.invalid"
}

func (s *server) handleListCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	in := listFromForm(r)
	if in.Sort == "" {
		in.Sort = list.Manual
	}
	switch l, err := s.db.CreateList(p.household.ID, in); {
	case err == nil:
		s.redirectSaved(w, r, "/lists/"+l.ID, "flash.list_created")
	case errors.Is(err, store.ErrInvalid):
		field := listErrorField(err)
		s.renderLists(w, r, p, http.StatusUnprocessableEntity, listFormValues(in), listErrorKey(field), field)
	default:
		s.fail(w, "create list", err)
	}
}

// hideTicked tells whether the reader chose to hide the ticked-off entries:
// "?ticked=hide" on a page, a hidden field "ticked" in a form.
func hideTicked(r *http.Request) bool { return r.FormValue("ticked") == "hide" }

// listURL is the page of a list: the everyday view, or the sort-or-remove view
// when edit is set. It keeps the choice to hide ticked-off entries.
func listURL(r *http.Request, id string, edit bool) string {
	q := url.Values{}
	if edit {
		q.Set("edit", "1")
	}
	if hideTicked(r) {
		q.Set("ticked", "hide")
	}
	if len(q) == 0 {
		return "/lists/" + id
	}
	return "/lists/" + id + "?" + q.Encode()
}

// renderList shows one list: the everyday view, or with edit the view where a
// parent sorts and removes entries. form holds what the add-entry field and the
// settings show after an error.
func (s *server) renderList(w http.ResponseWriter, r *http.Request, p principal, status int, l list.List, edit bool, form map[string]any, errKey, errField string) {
	es, err := s.db.Entries(p.household.ID, l.ID)
	if err != nil {
		s.fail(w, "entries", err)
		return
	}
	c := countOf(es)
	// Open entries come first, ticked-off ones in a group below. Stored
	// positions stay as they are, so unticking returns an entry to its place.
	var open, ticked []entryView
	for _, e := range es {
		if e.Done {
			ticked = append(ticked, entryView{ID: e.ID, Text: e.Text, Done: true})
		} else {
			open = append(open, entryView{ID: e.ID, Text: e.Text})
		}
	}
	if len(open) > 0 {
		open[0].First, open[len(open)-1].Last = true, true
	}
	if form == nil {
		form = map[string]any{"Text": ""}
	}
	if _, ok := form["Name"]; !ok {
		for k, v := range listFormValues(l) {
			form[k] = v
		}
	}
	child := p.person.Role == store.RoleChild
	lv := s.newListView(s.langOf(r), l, c)
	data := s.listChoices()
	data["Meta"] = s.listMeta(s.langOf(r), l, lv.State, child)
	data["Person"], data["List"], data["Open"], data["Ticked"], data["Form"] = p.person, lv, open, ticked, form
	data["Child"], data["Hide"], data["Edit"] = child, hideTicked(r), edit
	data["SuccessInline"] = edit
	data["Manual"] = l.Sort == list.Manual
	data["Summary"] = s.listSummary(r, form)
	data["SyncStale"] = s.syncStale(p.household.ID)
	data["Error"], data["ErrorField"] = errKey, errField
	s.render(w, r, status, "list.html", data)
}

// listPage loads the list of a request or answers 404.
func (s *server) listPage(w http.ResponseWriter, r *http.Request, p principal) (list.List, bool) {
	l, err := s.db.List(p.household.ID, r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return list.List{}, false
	case err != nil:
		s.fail(w, "list", err)
		return list.List{}, false
	}
	return l, true
}

func (s *server) handleListPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	edit := r.URL.Query().Get("edit") == "1"
	if edit && p.person.Role != store.RoleParent {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	if l, ok := s.listPage(w, r, p); ok {
		s.renderList(w, r, p, http.StatusOK, l, edit, nil, "", "")
	}
}

func (s *server) handleListSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	l, ok := s.listPage(w, r, p)
	if !ok {
		return
	}
	in := listFromForm(r)
	in.ID = l.ID
	switch _, err := s.db.UpdateList(p.household.ID, in); {
	case err == nil:
		s.redirectSaved(w, r, listURL(r, l.ID, false), "flash.saved")
	case errors.Is(err, store.ErrInvalid):
		field := listErrorField(err)
		form := listFormValues(in)
		form["Text"] = ""
		form["SettingsOpen"] = true
		s.renderList(w, r, p, http.StatusUnprocessableEntity, l, false, form, listErrorKey(field), field)
	default:
		s.fail(w, "update list", err)
	}
}

func (s *server) handleListDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	switch err := s.db.DeleteList(p.household.ID, r.PathValue("id")); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, "delete list", err)
	default:
		s.redirectSaved(w, r, "/lists", "flash.list_deleted")
	}
}

func (s *server) handleEntryAdd(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	l, ok := s.listPage(w, r, p)
	if !ok {
		return
	}
	_ = r.ParseForm()
	text := r.PostFormValue("text")
	switch e, err := s.db.AddEntry(p.household.ID, l.ID, text); {
	case err == nil:
		s.redirectSavedText(w, r, listURL(r, l.ID, false), "flash.entry_added", e.Text)
	case errors.Is(err, store.ErrInvalid):
		s.renderList(w, r, p, http.StatusUnprocessableEntity, l, false, map[string]any{"Text": text}, listErrorKey("text"), "text")
	default:
		s.fail(w, "add entry", err)
	}
}

// entryResult redirects to the list page (or the edit view) with a success
// line, or answers what went wrong. text, when set, is named in the line.
func (s *server) entryResult(w http.ResponseWriter, r *http.Request, to, flashKey, text string, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
	case err != nil:
		s.fail(w, "list entry", err)
	case text != "":
		s.redirectSavedText(w, r, to, flashKey, text)
	default:
		s.redirectSaved(w, r, to, flashKey)
	}
}

// handleEntryDone ticks an entry off or open: allowed for everyone, children included.
func (s *server) handleEntryDone(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	done := r.PostFormValue("done") == "1"
	key := "flash.entry_reopened"
	if done {
		key = "flash.entry_done"
	}
	id := r.PathValue("id")
	s.entryResult(w, r, listURL(r, id, false), key, "", s.db.SetEntryDone(p.household.ID, id, r.PathValue("entry"), done))
}

// entryAt finds an entry of a list by id, in the order the list shows them.
func (s *server) entryAt(w http.ResponseWriter, r *http.Request, p principal, listID, id string) (es []list.Entry, at int, ok bool) {
	es, err := s.db.Entries(p.household.ID, listID)
	if err != nil {
		s.entryResult(w, r, "", "", "", err)
		return nil, 0, false
	}
	at = slices.IndexFunc(es, func(e list.Entry) bool { return e.ID == id })
	if at < 0 {
		http.NotFound(w, r)
		return nil, 0, false
	}
	return es, at, true
}

// handleEntryDelete removes one entry from the sort-or-remove view and goes back there.
func (s *server) handleEntryDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	listID, id := r.PathValue("id"), r.PathValue("entry")
	es, at, ok := s.entryAt(w, r, p, listID, id)
	if !ok {
		return
	}
	s.entryResult(w, r, listURL(r, listID, true), "flash.entry_removed", es[at].Text, s.db.DeleteEntry(p.household.ID, listID, id))
}

func (s *server) handleTickedDelete(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		_, err := s.db.DeleteCompleted(p.household.ID, r.PathValue("id"))
		s.entryResult(w, r, listURL(r, r.PathValue("id"), false), "flash.ticked_deleted", "", err)
	}
}

// handleEntryMove swaps an open entry of a manual list with the next open entry
// up or down; ticked-off entries between them keep their places.
func (s *server) handleEntryMove(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	dir := r.PostFormValue("direction")
	step := map[string]int{"up": -1, "down": 1}[dir]
	if step == 0 {
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
		return
	}
	listID, id := r.PathValue("id"), r.PathValue("entry")
	es, at, ok := s.entryAt(w, r, p, listID, id)
	if !ok {
		return
	}
	if es[at].Done {
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
		return
	}
	to := at + step
	for to >= 0 && to < len(es) && es[to].Done {
		to += step
	}
	if to < 0 || to >= len(es) { // already first or last: nothing to change
		http.Redirect(w, r, listURL(r, listID, true), http.StatusSeeOther)
		return
	}
	s.entryResult(w, r, listURL(r, listID, true), "flash.entry_moved_"+dir, es[at].Text, s.db.MoveEntry(p.household.ID, listID, id, to))
}
