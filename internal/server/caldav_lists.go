package server

import (
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	ical "github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	webcal "github.com/emersion/go-webdav/caldav"

	hcal "github.com/hejmo-home/hejmo/internal/caldav"
	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Shared lists over CalDAV (ticket 167, ADR 0019): a parent switches a list on
// and Apple Reminders and other clients get it as a collection of tasks, below
// the calendar home like the task lists:
//
//	/dav/{person}/calendars/lists-{list id}/            one shared list
//	/dav/{person}/calendars/lists-{list id}/{entry}.ics one entry
//
// An entry is a VTODO titled "Name · quantity" with the note as its
// description. Unlike tasks, a client may create and delete entries (an entry
// needs no person); a new one is matched to a library article by name and
// added up by the rule of adding. Dates, times and flags a client sets are not
// stored. Lists themselves are created, renamed and deleted in Hejmo only.
// Only parents' tokens carry the lists scope, and only the lists a parent
// switched on are served. It is no outbound connection: the phone fetches from
// the box (ADR 0024).
const listCollectionPrefix = "lists-"

// itemModified is the fixed time stamp of every entry: entries keep no change
// time, and the same entry must render the same bytes so its ETag follows its
// content.
var itemModified = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func isListPath(p string) bool { return davResource(p) == "lists" }

type listAddress struct {
	list  string
	entry string // "" for the list itself
}

func parseListPath(sess *davSession, p string) (listAddress, bool) {
	rest, ok := strings.CutPrefix(p, davPrefix+"/"+sess.person.ID+"/calendars/"+listCollectionPrefix)
	if !ok {
		return listAddress{}, false
	}
	parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] != "":
		return listAddress{list: parts[0]}, true
	case len(parts) == 2 && !strings.HasSuffix(rest, "/"):
		id, isICS := strings.CutSuffix(parts[1], ".ics")
		return listAddress{list: parts[0], entry: id}, isICS && id != ""
	}
	return listAddress{}, false
}

func (b *davBackend) listPath(sess *davSession, l list.List) string {
	return b.homePath(sess) + listCollectionPrefix + l.ID + "/"
}

func (b *davBackend) toSharedList(sess *davSession, l list.List) webcal.Calendar {
	return webcal.Calendar{Path: b.listPath(sess, l), Name: l.Name, SupportedComponentSet: []string{ical.CompToDo}}
}

func (b *davBackend) sharedLists(sess *davSession) ([]webcal.Calendar, error) {
	all, err := b.s.db.Lists(sess.house.ID)
	if err != nil {
		return nil, err
	}
	var out []webcal.Calendar
	for _, l := range all {
		if l.Shared {
			out = append(out, b.toSharedList(sess, l))
		}
	}
	return out, nil
}

// sharedListAt resolves a path to a list that is switched on (404 for others).
func (b *davBackend) sharedListAt(sess *davSession, p string) (list.List, listAddress, error) {
	addr, ok := parseListPath(sess, p)
	if !ok {
		return list.List{}, listAddress{}, httpError(http.StatusNotFound, "no such resource")
	}
	l, err := b.s.db.List(sess.house.ID, addr.list)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !l.Shared) {
		return list.List{}, listAddress{}, httpError(http.StatusNotFound, "no such list")
	}
	return l, addr, err
}

func (b *davBackend) getSharedList(sess *davSession, p string) (*webcal.Calendar, error) {
	l, addr, err := b.sharedListAt(sess, p)
	if err != nil {
		return nil, err
	}
	if addr.entry != "" {
		return nil, httpError(http.StatusNotFound, "not a list")
	}
	out := b.toSharedList(sess, l)
	return &out, nil
}

// davLang is the language of the titles of a person's reminders: the language
// of the account that owns the device token.
func (b *davBackend) davLang(sess *davSession) string {
	acc, err := b.s.db.AccountByPerson(sess.person.ID)
	if err != nil {
		return ""
	}
	return acc.Language
}

func (b *davBackend) itemCtx(sess *davSession, full bool) (*itemCtx, error) {
	p := principal{household: sess.house, person: sess.person, scopes: sess.scopes}
	lang := b.davLang(sess)
	if full {
		return b.s.itemContext(p, lang)
	}
	return &itemCtx{s: b.s, p: p, lang: lang, active: b.s.activeUnits(sess.house)}, nil
}

// itemTitle is the title of an entry's reminder: its words, then the quantity.
func (c *itemCtx) itemTitle(e list.Entry) string { return list.JoinTitle(e.Text, c.qtyWords(e.Qty)) }

func (c *itemCtx) itemObject(path string, e list.Entry) (webcal.CalendarObject, error) {
	td := hcal.Todo{UID: e.ID, Title: c.itemTitle(e), Notes: e.Note, Done: e.Done, Modified: itemModified}
	cal, err := td.Calendar()
	if err != nil {
		return webcal.CalendarObject{}, err
	}
	tag, err := hcal.ETag(cal)
	if err != nil {
		return webcal.CalendarObject{}, err
	}
	return webcal.CalendarObject{Path: path + e.ID + ".ics", ModTime: itemModified, ETag: tag, Data: cal}, nil
}

func (b *davBackend) listItems(sess *davSession, p string) ([]webcal.CalendarObject, error) {
	l, _, err := b.sharedListAt(sess, p)
	if err != nil {
		return nil, err
	}
	es, err := b.s.db.Entries(sess.house.ID, l.ID)
	if err != nil {
		return nil, err
	}
	c, err := b.itemCtx(sess, false)
	if err != nil {
		return nil, err
	}
	out := make([]webcal.CalendarObject, 0, len(es))
	for _, e := range es {
		co, err := c.itemObject(b.listPath(sess, l), e)
		if err != nil {
			return nil, err
		}
		out = append(out, co)
	}
	return out, nil
}

// entryAt finds the entry a path names: nil if the list has none.
func (b *davBackend) entryAt(sess *davSession, p string) (list.List, listAddress, []list.Entry, *list.Entry, error) {
	l, addr, err := b.sharedListAt(sess, p)
	if err != nil {
		return l, addr, nil, nil, err
	}
	if addr.entry == "" {
		return l, addr, nil, nil, httpError(http.StatusNotFound, "not an entry")
	}
	es, err := b.s.db.Entries(sess.house.ID, l.ID)
	if err != nil {
		return l, addr, nil, nil, err
	}
	if at := slices.IndexFunc(es, func(e list.Entry) bool { return e.ID == addr.entry }); at >= 0 {
		return l, addr, es, &es[at], nil
	}
	return l, addr, es, nil, nil
}

func (b *davBackend) getItem(sess *davSession, p string) (*webcal.CalendarObject, error) {
	l, _, _, e, err := b.entryAt(sess, p)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, httpError(http.StatusNotFound, "no such entry")
	}
	c, err := b.itemCtx(sess, false)
	if err != nil {
		return nil, err
	}
	co, err := c.itemObject(b.listPath(sess, l), *e)
	return &co, err
}

// unitWords maps the words of the active units, in every language, as they
// are written after a number, to the unit.
func (c *itemCtx) unitWords() func(string) (string, bool) {
	m := map[string]string{}
	for _, u := range c.active {
		m[u] = u
		for _, lang := range c.s.i18n.Languages() {
			for _, form := range []string{".one", ".other"} {
				w := strings.ToLower(c.s.i18n.T(lang, "unit."+u+form))
				if unitWordRe.MatchString(w) {
					m[w] = u
				}
			}
		}
	}
	return func(w string) (string, bool) { k, ok := m[w]; return k, ok }
}

var unitWordRe = regexp.MustCompile(`^\p{L}[\p{L}.]*$`)

// reminder reads a title as the entry it makes: the name as the library
// writes it, its article, and the quantity (a bare number takes the article's
// first default unit).
func (c *itemCtx) reminder(title string, keep *list.Entry) (store.SyncEdit, error) {
	if keep != nil && title == c.itemTitle(*keep) { // unchanged: also keeps what the title cannot say
		return store.SyncEdit{Text: keep.Text, ArticleID: keep.ArticleID, Qty: keep.Qty}, nil
	}
	sp := list.SplitTitle(title, c.unitWords())
	var art *articleItem
	text := sp.Name
	if keep != nil && library.Fold(keep.Text) == library.Fold(text) {
		text = keep.Text
		if it, ok := c.article(keep.ArticleID); ok {
			art = &it
		}
	} else if it, ok := c.byName(text); ok {
		art = &it
		text = it.Name(c.lang)
		if library.Fold(sp.Name) != library.Fold(text) { // typed in the other language: stays as typed
			text = sp.Name
		}
	}
	out := store.SyncEdit{Text: text}
	if art != nil {
		out.ArticleID = art.ID
	}
	switch {
	case sp.Unit != "":
		out.Qty = list.NewQuantity(sp.Amount, sp.Unit)
	case sp.Bare:
		out.Qty = list.NewQuantity(sp.Amount, c.offered(art)[0])
	}
	return out, nil
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func (b *davBackend) putItem(sess *davSession, p string, cal *ical.Calendar, opts *webcal.PutCalendarObjectOptions) (*webcal.CalendarObject, error) {
	l, addr, _, _, err := b.entryAt(sess, p)
	if err != nil {
		return nil, err
	}
	if !validName.MatchString(addr.entry) || strings.Trim(addr.entry, ".") == "" {
		return nil, httpError(http.StatusBadRequest, "entry names may use letters, digits and . _ @ = + -")
	}
	edit, err := hcal.ParseItemTodo(cal)
	if err != nil {
		return nil, httpError(http.StatusBadRequest, err.Error())
	}
	c, err := b.itemCtx(sess, true)
	if err != nil {
		return nil, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	// Read under the lock: the precondition and the change see the same entry.
	_, _, _, existing, err := b.entryAt(sess, p)
	if err != nil {
		return nil, err
	}
	path := b.listPath(sess, l)
	have, exists := "", existing != nil
	if exists {
		co, err := c.itemObject(path, *existing)
		if err != nil {
			return nil, err
		}
		have = co.ETag
	}
	if err := checkETag(have, exists, opts.IfMatch, opts.IfNoneMatch); err != nil {
		return nil, err
	}
	in, err := c.reminder(edit.Title, existing)
	if err != nil {
		return nil, err
	}
	in.Note, in.Done = truncate(edit.Notes, list.MaxNote), edit.Completed
	in.Text = truncate(in.Text, list.MaxText)

	show := c.show(c.articleItemOf(in.ArticleID))
	var saved list.Entry
	if exists {
		saved, _, err = b.s.db.SyncEntry(sess.house.ID, l.ID, addr.entry, in, show)
	} else {
		var res store.AddResult
		res, err = b.s.db.SyncNewEntry(sess.house.ID, l.ID, addr.entry, in, show)
		saved = res.Entry
	}
	switch {
	case errors.Is(err, store.ErrIDTaken):
		return nil, httpError(http.StatusConflict, "the name is taken by another entry")
	case errors.Is(err, store.ErrInvalid):
		return nil, httpError(http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrNotFound):
		return nil, httpError(http.StatusPreconditionFailed, "the entry is no longer on this list")
	case err != nil:
		return nil, err
	}
	// An entry added up into another one has no resource of its own: the
	// client's copy goes at its next sync. Answer as if it were stored.
	co, err := c.itemObject(path, saved)
	co.Path = path + addr.entry + ".ics"
	return &co, err
}

func (c *itemCtx) articleItemOf(id string) *articleItem {
	if it, ok := c.article(id); ok {
		return &it
	}
	return nil
}

func (b *davBackend) deleteItem(sess *davSession, p string) error {
	if addr, ok := parseListPath(sess, p); ok && addr.entry == "" {
		return httpError(http.StatusForbidden, "lists are created, renamed and deleted in the box's web interface")
	}
	l, _, _, e, err := b.entryAt(sess, p)
	if err != nil {
		return err
	}
	if e == nil {
		return httpError(http.StatusNotFound, "no such entry")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if m := webdav.ConditionalMatch(sess.req.Header.Get("If-Match")); m.IsSet() {
		c, err := b.itemCtx(sess, false)
		if err != nil {
			return err
		}
		co, err := c.itemObject(b.listPath(sess, l), *e)
		if err != nil {
			return err
		}
		if err := checkETag(co.ETag, true, m, ""); err != nil {
			return err
		}
	}
	err = b.s.db.DeleteEntry(sess.house.ID, l.ID, e.ID)
	if errors.Is(err, store.ErrNotFound) {
		return httpError(http.StatusNotFound, "no such entry")
	}
	return err
}
