package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

type listJSON struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Color     string       `json:"color"`
	Symbol    string       `json:"symbol"`
	Sort      string       `json:"sort"`
	OpenCount int          `json:"open_count"`
	DoneCount int          `json:"done_count"`
	Entries   *[]entryJSON `json:"entries,omitempty"`
}

type entryJSON struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Done bool   `json:"done"`
	// ArticleID, Symbol and Picture say what a tile shows (ADR 0019): the
	// library article the text names, its symbol or the address of its own
	// picture. All three are null for free text.
	ArticleID *string `json:"article_id"`
	Symbol    *string `json:"symbol"`
	Picture   *string `json:"picture"`
}

func listToJSON(l list.List, c store.EntryCount) listJSON {
	return listJSON{ID: l.ID, Name: l.Name, Color: l.Color, Symbol: l.Symbol, Sort: string(l.Sort), OpenCount: c.Open, DoneCount: c.Done}
}

// entriesToJSON turns entries into their JSON; with a library their articles
// are looked up (tiles need them), without it they are left null.
func (s *server) entriesToJSON(p principal, es []list.Entry) ([]entryJSON, error) {
	lk, err := s.newListLooks(p, i18n.Default, "/articles/")
	if err != nil {
		return nil, err
	}
	all := lk.articles()
	out := make([]entryJSON, 0, len(es))
	for _, e := range es {
		j := entryJSON{ID: e.ID, Text: e.Text, Done: e.Done}
		if look, _, ok := lk.resolve(e.Text, all); ok {
			j.ArticleID = &look.ArticleID
			if look.Symbol != "" {
				j.Symbol = &look.Symbol
			}
			if look.Picture != "" {
				pic := strings.SplitN(look.Picture, "?", 2)[0]
				j.Picture = &pic
			}
		}
		out = append(out, j)
	}
	return out, nil
}

// listError answers a failed list operation with the matching status.
func (s *server) listError(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "list or entry not found")
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		s.fail(w, what, err)
	}
}

func (s *server) apiListLists(w http.ResponseWriter, _ *http.Request, p principal) {
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
	out := make([]listJSON, 0, len(lists))
	for _, l := range lists {
		out = append(out, listToJSON(l, counts[l.ID]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiGetList(w http.ResponseWriter, r *http.Request, p principal) {
	l, err := s.db.List(p.household.ID, r.PathValue("id"))
	if err != nil {
		s.listError(w, "list", err)
		return
	}
	es, err := s.db.Entries(p.household.ID, l.ID)
	if err != nil {
		s.fail(w, "entries", err)
		return
	}
	entries, err := s.entriesToJSON(p, es)
	if err != nil {
		s.fail(w, "entry looks", err)
		return
	}
	j := listToJSON(l, countOf(es))
	j.Entries = &entries
	writeJSON(w, http.StatusOK, j)
}

func countOf(es []list.Entry) store.EntryCount {
	var c store.EntryCount
	for _, e := range es {
		if e.Done {
			c.Done++
		} else {
			c.Open++
		}
	}
	return c
}

func (s *server) apiCreateList(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Name, Color, Symbol, Sort string
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if in.Sort == "" {
		in.Sort = string(list.Manual)
	}
	l, err := s.db.CreateList(p.household.ID, list.List{Name: in.Name, Color: in.Color, Symbol: in.Symbol, Sort: list.Sort(in.Sort)})
	if err != nil {
		s.listError(w, "create list", err)
		return
	}
	writeJSON(w, http.StatusCreated, listToJSON(l, store.EntryCount{}))
}

func (s *server) apiUpdateList(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Name, Color, Symbol, Sort *string
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	l, err := s.db.List(p.household.ID, r.PathValue("id"))
	if err != nil {
		s.listError(w, "list", err)
		return
	}
	if in.Name != nil {
		l.Name = *in.Name
	}
	if in.Color != nil {
		l.Color = *in.Color
	}
	if in.Symbol != nil {
		l.Symbol = *in.Symbol
	}
	if in.Sort != nil {
		l.Sort = list.Sort(*in.Sort)
	}
	if l, err = s.db.UpdateList(p.household.ID, l); err != nil {
		s.listError(w, "update list", err)
		return
	}
	counts, err := s.db.EntryCounts(p.household.ID)
	if err != nil {
		s.listError(w, "count list entries", err)
		return
	}
	writeJSON(w, http.StatusOK, listToJSON(l, counts[l.ID]))
}

func (s *server) apiDeleteList(w http.ResponseWriter, r *http.Request, p principal) {
	if err := s.db.DeleteList(p.household.ID, r.PathValue("id")); err != nil {
		s.listError(w, "delete list", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) apiAddEntry(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct{ Text string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	e, err := s.db.AddEntry(p.household.ID, r.PathValue("id"), in.Text)
	if err != nil {
		s.listError(w, "add entry", err)
		return
	}
	one, err := s.entriesToJSON(p, []list.Entry{e})
	if err != nil {
		s.fail(w, "entry looks", err)
		return
	}
	writeJSON(w, http.StatusCreated, one[0])
}

// entryState renders one entry after a change.
func (s *server) entryState(p principal, listID, id string) (entryJSON, error) {
	es, err := s.db.Entries(p.household.ID, listID)
	for _, e := range es {
		if e.ID == id {
			one, err := s.entriesToJSON(p, []list.Entry{e})
			if err != nil {
				return entryJSON{}, err
			}
			return one[0], nil
		}
	}
	if err == nil {
		err = store.ErrNotFound
	}
	return entryJSON{}, err
}

func (s *server) apiSetEntryDone(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Done *bool `json:"done"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if in.Done == nil {
		writeError(w, http.StatusUnprocessableEntity, "done is required")
		return
	}
	listID, id := r.PathValue("id"), r.PathValue("entry")
	if err := s.db.SetEntryDone(p.household.ID, listID, id, *in.Done); err != nil {
		s.listError(w, "tick entry", err)
		return
	}
	j, err := s.entryState(p, listID, id)
	if err != nil {
		s.listError(w, "entry", err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *server) apiMoveEntry(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Position *int `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if in.Position == nil || *in.Position < 0 {
		writeError(w, http.StatusUnprocessableEntity, "position must be 0 or more")
		return
	}
	listID, id := r.PathValue("id"), r.PathValue("entry")
	if err := s.db.MoveEntry(p.household.ID, listID, id, *in.Position); err != nil {
		s.listError(w, "move entry", err)
		return
	}
	es, err := s.db.Entries(p.household.ID, listID)
	if err != nil {
		s.fail(w, "entries", err)
		return
	}
	entries, err := s.entriesToJSON(p, es)
	if err != nil {
		s.fail(w, "entry looks", err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *server) apiDeleteEntry(w http.ResponseWriter, r *http.Request, p principal) {
	if err := s.db.DeleteEntry(p.household.ID, r.PathValue("id"), r.PathValue("entry")); err != nil {
		s.listError(w, "delete entry", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) apiDeleteCompleted(w http.ResponseWriter, r *http.Request, p principal) {
	n, err := s.db.DeleteCompleted(p.household.ID, r.PathValue("id"))
	if err != nil {
		s.listError(w, "delete completed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"deleted": n})
}

// listViewJSON says how a person sees lists: Default is true while the person
// never chose, so the view is the default of the role.
type listViewJSON struct {
	View    string `json:"view"`
	Default bool   `json:"default"`
}

func (s *server) personListViewJSON(p principal, id string) (listViewJSON, error) {
	person, err := s.db.Person(p.household.ID, id)
	if err != nil {
		return listViewJSON{}, err
	}
	v, err := s.db.PersonListView(p.household.ID, id)
	if err != nil {
		return listViewJSON{}, err
	}
	if v == "" {
		return listViewJSON{View: string(list.DefaultView(person.Role == store.RoleChild)), Default: true}, nil
	}
	return listViewJSON{View: string(v)}, nil
}

func (s *server) apiGetListView(w http.ResponseWriter, r *http.Request, p principal) {
	j, err := s.personListViewJSON(p, r.PathValue("id"))
	if err != nil {
		s.listViewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

// apiPutListView remembers the view of the person who asks: nobody changes
// another person's view, not even a parent.
func (s *server) apiPutListView(w http.ResponseWriter, r *http.Request, p principal) {
	id := r.PathValue("id")
	if id != p.person.ID {
		if _, err := s.db.Person(p.household.ID, id); err != nil {
			s.listViewError(w, err)
			return
		}
		writeError(w, http.StatusForbidden, "everyone chooses their own view of lists")
		return
	}
	var in struct {
		View string `json:"view"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	view, ok := list.ParseView(in.View)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "view must be tiles or list")
		return
	}
	if err := s.db.SetPersonListView(p.household.ID, id, view); err != nil {
		s.listViewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, listViewJSON{View: string(view)})
}

func (s *server) listViewError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "person not found")
		return
	}
	s.fail(w, "list view", err)
}
