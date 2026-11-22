package server

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/library"
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
	ID, Text  string
	ArticleID string // the stored article, "" for free text
	Done      bool
	First     bool
	Last      bool
	Added     bool // the entry just added or changed, framed until the next tap
	// Suggest is set on a list with suggestions: the entry shows its quantity
	// as a button that opens the editor (ticket 165).
	Suggest bool
	Qty     string // the quantity in words, "" without
	Label   string // the accessible name of the quantity button
	Href    string // where the button leads without script
	Warns   []template.HTML
	Editing bool
	Editor  *editorView
	Result  *resultView // the line after changing the quantity of this entry
	Look    entryLook
}

func (s *server) newListView(lang string, l list.List, c store.EntryCount) listView {
	return listView{State: s.listState(lang, c.Open, c.Done), ID: l.ID, Name: l.Name, Color: l.Color, Symbol: l.Symbol, Sort: string(l.Sort), Slot: list.Slot(l.Color), Open: c.Open, Done: c.Done, Total: c.Open + c.Done}
}

// listFormValues is what the new-list and list-settings forms show.
func listFormValues(l list.List) map[string]any {
	return map[string]any{"Name": l.Name, "Color": l.Color, "Symbol": l.Symbol, "Sort": string(l.Sort), "Suggestions": l.Suggestions}
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
		form = listFormValues(list.List{Color: list.Colors[n%len(list.Colors)], Symbol: list.Symbols[n%len(list.Symbols)], Sort: list.Manual, Suggestions: true})
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
	return list.List{Name: r.PostFormValue("name"), Color: r.PostFormValue("color"), Symbol: r.PostFormValue("symbol"), Sort: list.Sort(r.PostFormValue("sort")), Suggestions: r.PostFormValue("suggestions") != ""}
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
	in.Suggestions = true // a new list suggests articles; a parent switches it off in its settings
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
	lang := s.langOf(r)
	child := p.person.Role == store.RoleChild
	if form == nil {
		form = map[string]any{"Text": ""}
	}
	if _, ok := form["Name"]; !ok {
		for k, v := range listFormValues(l) {
			form[k] = v
		}
	}
	view, err := s.listViewOf(p)
	if err != nil {
		s.fail(w, "list view", err)
		return
	}
	data := s.listChoices()
	tiles := view == list.Tiles && !edit
	// Open entries come first, ticked-off ones in a group below. Stored
	// positions stay as they are, so unticking returns an entry to its place.
	var open, ticked []entryView
	var ic *itemCtx
	if l.Suggestions && !edit {
		if ic, err = s.itemContext(p, lang); err != nil {
			s.fail(w, "library state", err)
			return
		}
		ic.listID = l.ID
		out := s.outcomeOf(r) // a result line is shown once
		open, ticked = ic.entryViews(es, entryOpts{Base: "/lists/" + l.ID, Hide: hideTicked(r), Parent: !child, Added: r.URL.Query().Get("added"), Edit: r.URL.Query().Get("qty"), Out: out, Post: editPostOf(form), HideQ: hideQuery(r), ErrField: errField, ErrKey: errKey})
		data["Suggest"], data["Base"] = true, "/lists/"+l.ID
		data["A"] = ic.adder(es, adderOpts{Base: "/lists/" + l.ID, Form: form, Out: out, ErrField: errField, Editing: editingOpen(open), Parent: !child})
		data["FreeBox"] = map[string]any{"Title": t(s, lang, "lq.unchecked.title"), "Hint": t(s, lang, "lq.unchecked.hint"), "People": s.freeText(ic.st)}
	} else {
		for _, e := range es {
			ev := entryView{ID: e.ID, Text: e.Text, ArticleID: e.ArticleID, Done: e.Done}
			if !e.Qty.None() {
				ev.Qty = s.qtyWordsFor(p, lang, e.Qty)
			}
			if e.Done {
				ticked = append(ticked, ev)
			} else {
				open = append(open, ev)
			}
		}
	}
	if len(open) > 0 {
		open[0].First, open[len(open)-1].Last = true, true
	}
	lv := s.newListView(lang, l, c)
	data["Meta"] = s.listMeta(lang, l, lv.State, child)
	if child && tiles {
		data["Meta"] = s.tilesMeta(lang, lv.State, c)
	}
	if !edit { // looks, warnings and the free-text note belong to the everyday view
		lk, err := s.newListLooks(p, lang, "/articles/")
		if err != nil {
			s.fail(w, "list looks", err)
			return
		}
		food := lk.apply(open, ticked)
		if !l.Suggestions { // a list with suggestions has its own note, FreeBox
			data["FreeText"] = lk.freeTextNote(food)
		}
	}
	if child && len(es) == 0 {
		data["ChildEmpty"] = s.childEmptyText(lang, p.household.ID)
	}
	data["Person"], data["List"], data["Open"], data["Ticked"], data["Form"] = p.person, lv, open, ticked, form
	data["Child"], data["Hide"], data["Edit"] = child, hideTicked(r), edit
	data["View"], data["Tiles"], data["AllDone"] = string(view), tiles, len(es) > 0 && c.Open == 0
	data["ViewError"] = ""
	if _, bad := form["ViewError"]; bad {
		data["ViewError"] = s.viewErrorText(lang, view)
	}
	data["SuccessInline"] = edit
	data["Manual"] = l.Sort == list.Manual
	data["Suggestions"] = l.Suggestions
	data["Summary"] = s.listSummary(r, form)
	data["SyncStale"] = s.syncStale(p.household.ID)
	data["Error"], data["ErrorField"] = errKey, errField
	s.render(w, r, status, "list.html", data)
}

// t translates a key into the language of a page.
func t(s *server, lang, key string) string { return s.i18n.T(lang, key) }

// qtyWordsFor writes a quantity in words where no item context is at hand (the sort and remove view).
func (s *server) qtyWordsFor(p principal, lang string, q list.Quantity) string {
	c := &itemCtx{s: s, p: p, lang: lang, active: s.activeUnits(p.household)}
	return c.qtyWords(q)
}

// outcomeOf takes the result of the last add or change that the address names.
func (s *server) outcomeOf(r *http.Request) *outcome {
	token := r.URL.Query().Get("r")
	if token == "" {
		return nil
	}
	if o, ok := s.takeOutcome(token); ok {
		return &o
	}
	return nil
}

// tilesMeta is the line under a child's list in tiles: the state and what a
// tap does.
func (s *server) tilesMeta(lang, state string, c store.EntryCount) string {
	switch {
	case c.Open == 0 && c.Done == 0:
		return fmt.Sprintf(s.i18n.T(lang, "lists.meta.plain"), state)
	case c.Open == 0:
		return fmt.Sprintf(s.i18n.T(lang, "lists.meta.child.tiles_done"), state)
	}
	return fmt.Sprintf(s.i18n.T(lang, "lists.meta.child.tiles"), state)
}

// childEmptyText tells a child that nothing is on the list and who can add: the
// parents, by name.
func (s *server) childEmptyText(lang, householdID string) string {
	people, err := s.db.People(householdID)
	var parents []string
	if err == nil {
		for _, x := range people {
			if x.Role == store.RoleParent {
				parents = append(parents, x.Name)
			}
		}
	}
	switch len(parents) {
	case 0:
		return s.i18n.T(lang, "lists.empty.child.none")
	case 1:
		return s.i18n.Format(lang, i18n.Msg("lists.empty.child.one", "name", parents[0]))
	}
	return s.i18n.Format(lang, i18n.Msg("lists.empty.child.many", "names", strings.Join(parents[:len(parents)-1], ", ")+" "+s.i18n.T(lang, "lists.or")+" "+parents[len(parents)-1]))
}

// viewErrorText says that the view was not saved and which one stays.
func (s *server) viewErrorText(lang string, v list.View) string {
	return s.i18n.Format(lang, i18n.Msg("lists.view.error", "view", s.i18n.T(lang, "lists.view."+string(v))))
}

// handleListView remembers the view of lists of the person who asks: tiles or
// the list, for all lists and every device. Children choose for themselves.
func (s *server) handleListView(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	id := r.PostFormValue("list")
	l, lerr := s.db.List(p.household.ID, id)
	if lerr != nil && !errors.Is(lerr, store.ErrNotFound) {
		s.fail(w, "list", lerr)
		return
	}
	view, valid := list.ParseView(r.PostFormValue("view"))
	switch {
	case !valid:
		if lerr != nil {
			http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
			return
		}
		s.renderList(w, r, p, http.StatusUnprocessableEntity, l, false, map[string]any{"Text": "", "ViewError": true}, "", "")
		return
	}
	if err := s.db.SetPersonListView(p.household.ID, p.person.ID, view); err != nil {
		s.fail(w, "save list view", err)
		return
	}
	to := "/lists"
	if lerr == nil {
		to = listURL(r, l.ID, false)
	}
	s.redirectSaved(w, r, to, "flash.list_view_"+string(view))
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
	if l.Suggestions {
		s.addToList(w, r, p, l, text)
		return
	}
	switch e, err := s.db.AddEntry(p.household.ID, l.ID, text); {
	case err == nil:
		s.redirectSavedText(w, r, listURL(r, l.ID, false), "flash.entry_added", e.Text)
	case errors.Is(err, store.ErrInvalid):
		s.renderList(w, r, p, http.StatusUnprocessableEntity, l, false, map[string]any{"Text": text}, listErrorKey("text"), "text")
	default:
		s.fail(w, "add entry", err)
	}
}

// formItem reads what the add card posted.
func formItem(r *http.Request, c *itemCtx) itemIn {
	in := itemIn{Text: r.PostFormValue("text"), ArticleID: r.PostFormValue("article"), Qty: r.PostFormValue("mode") == "qty", Amount: r.PostFormValue("amount"), Unit: r.PostFormValue("unit")}
	if in.ArticleID != "" && strings.TrimSpace(in.Text) != "" { // a picked article goes stale when the text is changed after it
		if it, ok := c.article(in.ArticleID); !ok || (library.Fold(in.Text) != library.Fold(it.De) && library.Fold(in.Text) != library.Fold(it.En)) {
			in.ArticleID = ""
		}
	}
	return in
}

// itemErrorKey is the text of an input that was refused, by field.
func itemErrorKey(f itemField, display bool) string {
	switch f {
	case fieldAmount:
		if display {
			return "lq.err.amount.display"
		}
		return "lq.err.amount"
	case fieldUnit:
		return "lq.err.unit"
	case fieldItem:
		return "lq.err.article"
	}
	return listErrorKey("text")
}

// addToList puts the posted item on a list with suggestions.
func (s *server) addToList(w http.ResponseWriter, r *http.Request, p principal, l list.List, text string) {
	c, err := s.itemContext(p, s.langOf(r))
	if err != nil {
		s.fail(w, "library state", err)
		return
	}
	c.listID = l.ID
	in := formItem(r, c)
	rs, err := c.resolve(in)
	if ie, ok := asItemError(err); ok {
		form := map[string]any{"Text": text, "Mode": r.PostFormValue("mode"), "Amount": in.Amount, "Unit": in.Unit, "Article": in.ArticleID}
		s.renderList(w, r, p, http.StatusUnprocessableEntity, l, false, form, itemErrorKey(ie.Field, false), string(ie.Field))
		return
	} else if err != nil {
		s.fail(w, "add entry", err)
		return
	}
	res, err := s.db.AddItem(p.household.ID, l.ID, rs.Item, c.show(rs.Article))
	switch {
	case errors.Is(err, store.ErrInvalid):
		s.renderList(w, r, p, http.StatusUnprocessableEntity, l, false, map[string]any{"Text": text}, listErrorKey("text"), "text")
		return
	case err != nil:
		s.fail(w, "add entry", err)
		return
	}
	o, err := c.addOutcome(l.ID, rs, res)
	if err != nil {
		s.fail(w, "entries", err)
		return
	}
	o.LibOffer = true
	http.Redirect(w, r, s.resultURL(r, l.ID, o), http.StatusSeeOther)
}

// resultURL is the page of a list after an add or a change: the entry is
// framed (added=) and the line says what happened (r=, kept in the box's memory).
func (s *server) resultURL(r *http.Request, listID string, o outcome) string {
	q := url.Values{"added": {o.EntryID}, "r": {s.putOutcome(o)}}
	if hideTicked(r) {
		q.Set("ticked", "hide")
	}
	return "/lists/" + listID + "?" + q.Encode() + "#e-" + o.EntryID
}

// handleEntryQuantity changes the quantity of an open entry.
func (s *server) handleEntryQuantity(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	l, ok := s.listPage(w, r, p)
	if !ok {
		return
	}
	if !l.Suggestions {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	c, err := s.itemContext(p, s.langOf(r))
	if err != nil {
		s.fail(w, "library state", err)
		return
	}
	c.listID = l.ID
	id := r.PathValue("entry")
	post := &editPost{EntryID: id, Mode: r.PostFormValue("qmode"), Amount: r.PostFormValue("amount"), Unit: r.PostFormValue("qunit"), Converted: r.PostFormValue("converted") == "1"}
	res, o, err := c.changeQuantity(l.ID, id, post)
	switch ie, isItem := asItemError(err); {
	case isItem:
		s.renderList(w, r, p, http.StatusUnprocessableEntity, l, false, map[string]any{"Text": "", "EditPost": post}, itemErrorKey(ie.Field, false), "edit-"+string(ie.Field))
		return
	case errors.Is(err, list.ErrNotOpen):
		s.renderList(w, r, p, http.StatusConflict, l, false, map[string]any{"Text": ""}, "lq.err.gone", "")
		return
	case err != nil:
		s.fail(w, "set quantity", err)
		return
	}
	_ = res
	http.Redirect(w, r, s.resultURL(r, l.ID, o), http.StatusSeeOther)
}

// handleListSuggest answers the suggestions under the text field: the
// markup of the blocks alone, for the page's script to put into the card.
func (s *server) handleListSuggest(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	l, ok := s.listPage(w, r, p)
	if !ok || !l.Suggestions {
		return
	}
	s.writeFragment(w, r, p, l, "suggest")
}

// handleListQuantity answers the chosen article and the quantity part, for the
// script: what is typed and chosen so far comes back with the sentence that
// says the result in advance.
func (s *server) handleListQuantity(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	l, ok := s.listPage(w, r, p)
	if !ok || !l.Suggestions {
		return
	}
	s.writeFragment(w, r, p, l, "quantity")
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

// handleEntryMove moves an open entry of a manual list past the next open entry
// up or down (an insert, not a swap): ticked-off entries between them shift by
// one place, and unticking one later puts it back at its stored position.
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
