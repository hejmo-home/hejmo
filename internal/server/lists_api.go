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
	ID     string `json:"id"`
	Name   string `json:"name"`
	Color  string `json:"color"`
	Symbol string `json:"symbol"`
	Sort   string `json:"sort"`
	// Suggestions says the list offers articles of the library and takes quantities.
	Suggestions bool `json:"suggestions"`
	// Shared says the list is also in Apple Reminders and other CalDAV apps.
	Shared    bool         `json:"shared"`
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
	// Amount and Unit are the quantity of an entry of a list with suggestions.
	Amount *float64 `json:"amount,omitempty"`
	Unit   string   `json:"unit,omitempty"`
	// Note is the note under the entry, "" without one (at most 500 characters).
	Note string `json:"note"`
}

// qtyJSON is a quantity: the number as shown, in its unit.
type qtyJSON struct {
	Amount float64 `json:"amount"`
	Unit   string  `json:"unit"`
}

// entryResultJSON is an entry after adding to a list or changing a quantity,
// with what happened.
type entryResultJSON struct {
	entryJSON
	// Result is added, added_to, filled or exists after adding; set or merged
	// after changing a quantity.
	Result string `json:"result"`
	// Previous is the quantity the entry had before, when it changed.
	Previous *qtyJSON `json:"previous,omitempty"`
}

func entryToJSON(e list.Entry) entryJSON {
	j := entryJSON{ID: e.ID, Text: e.Text, Done: e.Done, Note: e.Note}
	if e.ArticleID != "" {
		j.ArticleID = &e.ArticleID
	}
	if !e.Qty.None() {
		a := formatJSONAmount(e.Qty)
		j.Amount, j.Unit = &a, e.Qty.Unit
	}
	return j
}

func previousJSON(q list.Quantity) *qtyJSON {
	if q.None() {
		return nil
	}
	return &qtyJSON{formatJSONAmount(q), q.Unit}
}

func listToJSON(l list.List, c store.EntryCount) listJSON {
	return listJSON{ID: l.ID, Name: l.Name, Color: l.Color, Symbol: l.Symbol, Sort: string(l.Sort), Suggestions: l.Suggestions, Shared: l.Shared, OpenCount: c.Open, DoneCount: c.Done}
}

// entriesToJSON turns entries into their JSON with their looks (tiles need
// them): the stored article first, else the article the text names exactly;
// free text stays null.
func (s *server) entriesToJSON(p principal, es []list.Entry) ([]entryJSON, error) {
	lk, err := s.newListLooks(p, i18n.Default, "/articles/")
	if err != nil {
		return nil, err
	}
	all := lk.articles()
	out := make([]entryJSON, 0, len(es))
	for _, e := range es {
		j := entryToJSON(e)
		if look, _, ok := lk.resolve(e.ArticleID, e.Text, all); ok {
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

// entryJSONOf is one entry with its looks.
func (s *server) entryJSONOf(p principal, e list.Entry) (entryJSON, error) {
	one, err := s.entriesToJSON(p, []list.Entry{e})
	if err != nil {
		return entryJSON{}, err
	}
	return one[0], nil
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
		Suggestions               *bool
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if in.Sort == "" {
		in.Sort = string(list.Manual)
	}
	suggestions := in.Suggestions == nil || *in.Suggestions // on for a new list
	l, err := s.db.CreateList(p.household.ID, list.List{Name: in.Name, Color: in.Color, Symbol: in.Symbol, Sort: list.Sort(in.Sort), Suggestions: suggestions})
	if err != nil {
		s.listError(w, "create list", err)
		return
	}
	writeJSON(w, http.StatusCreated, listToJSON(l, store.EntryCount{}))
}

func (s *server) apiUpdateList(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Name, Color, Symbol, Sort *string
		Suggestions               *bool
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
	if in.Suggestions != nil {
		l.Suggestions = *in.Suggestions
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

// apiAddEntry puts an entry on a list. A list with suggestions takes an
// article of the library (article_id, or the text of its exact name) and a
// quantity; the same article with a unit of the same kind adds up, and adding
// something that is already there makes no second entry. A list without
// suggestions takes plain text.
func (s *server) apiAddEntry(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Text      string   `json:"text"`
		ArticleID string   `json:"article_id"`
		Amount    *float64 `json:"amount"`
		Unit      string   `json:"unit"`
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
	if !l.Suggestions {
		if in.ArticleID != "" || in.Amount != nil || in.Unit != "" {
			writeFieldError(w, http.StatusUnprocessableEntity, "article_id", "this list has no articles and no quantities")
			return
		}
		e, err := s.db.AddEntry(p.household.ID, l.ID, in.Text)
		if err != nil {
			s.listError(w, "add entry", err)
			return
		}
		j, err := s.entryJSONOf(p, e)
		if err != nil {
			s.fail(w, "entry looks", err)
			return
		}
		writeJSON(w, http.StatusCreated, entryResultJSON{entryJSON: j, Result: "added"})
		return
	}
	c, err := s.itemContext(p, s.langOf(r))
	if err != nil {
		s.fail(w, "library state", err)
		return
	}
	it := itemIn{Text: in.Text, ArticleID: in.ArticleID, Unit: in.Unit}
	if in.Amount != nil {
		it.Qty, it.Amount = true, numberText(*in.Amount)
	} else if in.Unit != "" {
		writeFieldError(w, http.StatusUnprocessableEntity, "amount", "a unit needs an amount")
		return
	}
	rs, err := c.resolve(it)
	if ie, ok := asItemError(err); ok {
		writeFieldError(w, http.StatusUnprocessableEntity, apiField(ie.Field), ie.Error())
		return
	} else if err != nil {
		s.fail(w, "add entry", err)
		return
	}
	res, err := s.db.AddItem(p.household.ID, l.ID, rs.Item, c.show(rs.Article))
	if err != nil {
		s.listError(w, "add entry", err)
		return
	}
	j, err := s.entryJSONOf(p, res.Entry)
	if err != nil {
		s.fail(w, "entry looks", err)
		return
	}
	out := entryResultJSON{entryJSON: j, Result: string(res.Plan.Action), Previous: previousJSON(res.Plan.Before)}
	status := http.StatusOK
	if res.Plan.Action == list.AddedNew {
		out.Result, status = "added", http.StatusCreated
	} else if res.Plan.Action == list.AddedTo {
		out.Result = "added_to"
	}
	writeJSON(w, status, out)
}

// apiField is the name of an input field in the JSON of the API.
func apiField(f itemField) string {
	if f == fieldItem {
		return "article_id"
	}
	return string(f)
}

// apiSetQuantity changes the quantity of an open entry: the amount replaces
// the old one and never adds to it. "amount": null takes the quantity away.
func (s *server) apiSetQuantity(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Amount json.RawMessage `json:"amount"`
		Unit   string          `json:"unit"`
		Note   *string         `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || (in.Amount == nil && in.Note == nil) {
		writeError(w, http.StatusBadRequest, "invalid JSON: amount (a number or null) or note is required")
		return
	}
	l, err := s.db.List(p.household.ID, r.PathValue("id"))
	if err != nil {
		s.listError(w, "list", err)
		return
	}
	if in.Amount != nil && !l.Suggestions {
		writeFieldError(w, http.StatusUnprocessableEntity, "amount", "this list has no quantities")
		return
	}
	var note string
	if in.Note != nil {
		if note = cleanNote(*in.Note); list.ValidateNote(note) != nil {
			writeFieldError(w, http.StatusUnprocessableEntity, "note", "a note has at most 500 characters")
			return
		}
	}
	if in.Amount == nil { // the note alone: the quantity stays
		s.apiSetNote(w, p, l, r.PathValue("entry"), note)
		return
	}
	c, err := s.itemContext(p, s.langOf(r))
	if err != nil {
		s.fail(w, "library state", err)
		return
	}
	var to list.Quantity
	var art *articleItem
	listID, id := l.ID, r.PathValue("entry")
	es, err := s.db.Entries(p.household.ID, listID)
	if err != nil {
		s.fail(w, "entries", err)
		return
	}
	for _, e := range es {
		if e.ID == id {
			if a, ok := c.article(e.ArticleID); ok {
				art = &a
			}
		}
	}
	chosen := ""
	if string(in.Amount) != "null" {
		var f float64
		if err := json.Unmarshal(in.Amount, &f); err != nil {
			writeFieldError(w, http.StatusUnprocessableEntity, "amount", "amount must be a number or null")
			return
		}
		if to, chosen, err = c.quantity(art, numberText(f), in.Unit); err != nil {
			ie, _ := asItemError(err)
			writeFieldError(w, http.StatusUnprocessableEntity, apiField(ie.Field), ie.Error())
			return
		}
	}
	// An amount read from the API is rounded; sent back, it is the amount it was read from.
	for _, e := range es {
		if e.ID == id && keepsAmount(e.Qty, to, chosen, true) {
			to.Base = e.Qty.Base
		}
	}
	res, err := s.db.SetQuantity(p.household.ID, listID, id, to, chosen, c.show(art))
	switch {
	case errors.Is(err, list.ErrNotOpen):
		writeError(w, http.StatusConflict, "the entry is ticked off or gone")
		return
	case err != nil:
		s.listError(w, "set quantity", err)
		return
	}
	e, out := res.Plan.Entry, entryResultJSON{Result: string(res.Plan.Action), Previous: previousJSON(res.Plan.Before)}
	if res.Plan.Action == list.SetMerged {
		e = res.Plan.Into
	}
	e.Qty = res.Plan.Result
	if in.Note != nil {
		saved, _, err := s.saveNote(p, l, e.ID, note, res.Plan.Action == list.SetMerged)
		if err != nil {
			s.listError(w, "set note", err)
			return
		}
		e.Note = saved.Note
	}
	if out.entryJSON, err = s.entryJSONOf(p, e); err != nil {
		s.fail(w, "entry looks", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// entryState renders one entry after a change.
func (s *server) entryState(p principal, listID, id string) (entryJSON, error) {
	es, err := s.db.Entries(p.household.ID, listID)
	for _, e := range es {
		if e.ID == id {
			return s.entryJSONOf(p, e)
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

// apiSetNote changes the note of an entry alone, open or ticked off. "" removes it.
func (s *server) apiSetNote(w http.ResponseWriter, p principal, l list.List, id, note string) {
	if err := s.db.SetEntryNote(p.household.ID, l.ID, id, note); err != nil {
		s.listError(w, "set note", err)
		return
	}
	e, err := s.entryState(p, l.ID, id)
	if err != nil {
		s.fail(w, "entry", err)
		return
	}
	writeJSON(w, http.StatusOK, entryResultJSON{entryJSON: e, Result: string(list.SetOnly)})
}

// sharingJSON is a list and whether other apps get it.
type sharingJSON struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Shared    bool   `json:"shared"`
	OpenCount int    `json:"open_count"`
}

func (s *server) sharingToJSON(p principal) ([]sharingJSON, error) {
	lists, err := s.db.Lists(p.household.ID)
	if err != nil {
		return nil, err
	}
	counts, err := s.db.EntryCounts(p.household.ID)
	if err != nil {
		return nil, err
	}
	out := make([]sharingJSON, 0, len(lists))
	for _, l := range lists {
		out = append(out, sharingJSON{ID: l.ID, Name: l.Name, Shared: l.Shared, OpenCount: counts[l.ID].Open})
	}
	return out, nil
}

// apiGetSharing says which lists are also in Apple Reminders.
func (s *server) apiGetSharing(w http.ResponseWriter, _ *http.Request, p principal) {
	out, err := s.sharingToJSON(p)
	if err != nil {
		s.fail(w, "list sharing", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]sharingJSON{"lists": out})
}

// apiPatchSharing switches lists on or off for other apps: {"lists": {"<id>": true}}.
// Lists it does not name stay as they are. A list that is gone stores nothing.
func (s *server) apiPatchSharing(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Lists map[string]bool `json:"lists"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Lists == nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: lists is required (list id to true or false)")
		return
	}
	var missing *store.MissingListError
	switch err := s.db.SetListsShared(p.household.ID, in.Lists); {
	case errors.As(err, &missing):
		writeFieldError(w, http.StatusUnprocessableEntity, "lists", "no such list: "+missing.ID)
		return
	case err != nil:
		s.fail(w, "list sharing", err)
		return
	}
	s.apiGetSharing(w, r, p)
}
