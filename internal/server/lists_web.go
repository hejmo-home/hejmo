package server

import (
	"errors"
	"net/http"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

type listView struct {
	ID, Name, Color, Symbol, Sort string
	Slot, Open, Done              int
}

type entryView struct {
	ID, Text string
	Done     bool
	First    bool
	Last     bool
}

func newListView(l list.List, c store.EntryCount) listView {
	return listView{ID: l.ID, Name: l.Name, Color: l.Color, Symbol: l.Symbol, Sort: string(l.Sort), Slot: list.Slot(l.Color), Open: c.Open, Done: c.Done}
}

// listFormValues is what the new-list and list-settings forms show.
func listFormValues(l list.List) map[string]any {
	return map[string]any{"Name": l.Name, "Color": l.Color, "Symbol": l.Symbol, "Sort": string(l.Sort)}
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
		views = append(views, newListView(l, counts[l.ID]))
	}
	if form == nil { // the next list gets the next colour and symbol, as a person does
		n := len(lists)
		form = listFormValues(list.List{Color: list.Colors[n%len(list.Colors)], Symbol: list.Symbols[n%len(list.Symbols)], Sort: list.Manual})
	}
	data := s.listChoices()
	data["Person"], data["Lists"], data["Form"] = p.person, views, form
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

// renderList shows one list. form holds what the add-entry field and the
// settings show after an error.
func (s *server) renderList(w http.ResponseWriter, r *http.Request, p principal, status int, l list.List, form map[string]any, errKey, errField string) {
	es, err := s.db.Entries(p.household.ID, l.ID)
	if err != nil {
		s.fail(w, "entries", err)
		return
	}
	c := countOf(es)
	hide := r.URL.Query().Get("completed") == "hide"
	var views []entryView
	for i, e := range es {
		if hide && e.Done {
			continue
		}
		views = append(views, entryView{ID: e.ID, Text: e.Text, Done: e.Done, First: i == 0, Last: i == len(es)-1})
	}
	if form == nil {
		form = map[string]any{"Text": ""}
	}
	if _, ok := form["Name"]; !ok {
		for k, v := range listFormValues(l) {
			form[k] = v
		}
	}
	data := s.listChoices()
	data["Person"], data["List"], data["Entries"], data["Form"] = p.person, newListView(l, c), views, form
	data["Child"], data["Hide"] = p.person.Role == store.RoleChild, hide
	data["Manual"] = l.Sort == list.Manual
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
	if l, ok := s.listPage(w, r, p); ok {
		s.renderList(w, r, p, http.StatusOK, l, nil, "", "")
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
		s.redirectSaved(w, r, "/lists/"+l.ID, "flash.saved")
	case errors.Is(err, store.ErrInvalid):
		field := listErrorField(err)
		form := listFormValues(in)
		form["Text"] = ""
		form["SettingsOpen"] = true
		s.renderList(w, r, p, http.StatusUnprocessableEntity, l, form, listErrorKey(field), field)
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
	switch _, err := s.db.AddEntry(p.household.ID, l.ID, text); {
	case err == nil:
		s.redirectSaved(w, r, "/lists/"+l.ID, "flash.entry_added")
	case errors.Is(err, store.ErrInvalid):
		s.renderList(w, r, p, http.StatusUnprocessableEntity, l, map[string]any{"Text": text}, listErrorKey("text"), "text")
	default:
		s.fail(w, "add entry", err)
	}
}

// entryResult redirects back to the list, or answers what went wrong.
func (s *server) entryResult(w http.ResponseWriter, r *http.Request, listID, flashKey string, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
	case err != nil:
		s.fail(w, "list entry", err)
	default:
		s.redirectSaved(w, r, "/lists/"+listID, flashKey)
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
	s.entryResult(w, r, r.PathValue("id"), key, s.db.SetEntryDone(p.household.ID, r.PathValue("id"), r.PathValue("entry"), done))
}

func (s *server) handleEntryDelete(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.entryResult(w, r, r.PathValue("id"), "flash.entry_deleted", s.db.DeleteEntry(p.household.ID, r.PathValue("id"), r.PathValue("entry")))
	}
}

func (s *server) handleCompletedDelete(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		_, err := s.db.DeleteCompleted(p.household.ID, r.PathValue("id"))
		s.entryResult(w, r, r.PathValue("id"), "flash.completed_deleted", err)
	}
}

// handleEntryMove moves an entry of a manual list one place up or down.
func (s *server) handleEntryMove(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	step := map[string]int{"up": -1, "down": 1}[r.PostFormValue("direction")]
	if step == 0 {
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
		return
	}
	listID, id := r.PathValue("id"), r.PathValue("entry")
	es, err := s.db.Entries(p.household.ID, listID)
	if err != nil {
		s.entryResult(w, r, listID, "", err)
		return
	}
	at := -1
	for i, e := range es {
		if e.ID == id {
			at = i
		}
	}
	if at < 0 {
		http.NotFound(w, r)
		return
	}
	s.entryResult(w, r, listID, "flash.entry_moved", s.db.MoveEntry(p.household.ID, listID, id, at+step))
}
