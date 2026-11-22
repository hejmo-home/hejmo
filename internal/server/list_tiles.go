package server

import (
	"html/template"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The tile view of lists (ticket 166, ADR 0019, preview-166): an entry shows
// the symbol or picture of the article it names, or its first letter when it
// is free text; allergen warnings and the note about free-text allergies come
// with it, in the tiles and in the rows.

// tileWarn is one line of the short warning on a tile: a person and what that
// person must avoid in this article, in words.
type tileWarn struct {
	Chip      personChip
	Allergens string
}

// entryLook is what an entry shows beyond its words.
type entryLook struct {
	ArticleID string
	Symbol    string // emoji of the article; "" for free text and for an article with a picture
	Picture   string // address of the article's own picture, "" without one
	Letter    string // free text: its first letter
	Warns     []tileWarn
	Long      []template.HTML // the long sentences of #164, one per group of people
	Food      bool            // the entry names an article that is food
}

// listLooks resolves entries to articles and builds their looks.
type listLooks struct {
	s     *server
	st    libState
	items []articleItem
	pic   string // prefix of picture addresses: "/articles/" or "/display/articles/"
}

// newListLooks gathers the library and the household's people once per page.
func (s *server) newListLooks(p principal, lang, picturePrefix string) (listLooks, error) {
	st, err := s.libState(p, lang)
	if err != nil {
		return listLooks{}, err
	}
	return listLooks{s: s, st: st, items: st.items(), pic: picturePrefix}, nil
}

func (l listLooks) articles() []library.Article {
	all := make([]library.Article, 0, len(l.items))
	for _, it := range l.items {
		if !it.handHidden { // a name typed as text never finds an article hidden by hand (as when adding)
			all = append(all, it.Article)
		}
	}
	return all
}

// resolve is the article, symbol or picture of one entry, without the
// warnings: the API needs no sentences. The stored article comes first; only an
// entry without one is looked up by its text.
func (l listLooks) resolve(articleID, text string, all []library.Article) (entryLook, library.Article, bool) {
	var a library.Article
	ok := false
	if articleID != "" {
		var it articleItem
		if it, ok = l.st.item(articleID); ok {
			a = it.Article
		}
	}
	if !ok {
		a, ok = library.Resolve(text, all)
	}
	if !ok {
		first, _ := utf8.DecodeRuneInString(strings.TrimSpace(text))
		return entryLook{Letter: string(unicode.ToUpper(first))}, a, false
	}
	it, _ := l.st.item(a.ID)
	look := entryLook{ArticleID: a.ID, Symbol: symbolOf(it), Food: a.Kind != library.NonFood}
	if it.own != nil && it.own.HasPicture {
		look.Picture = l.pic + it.own.ID + "/picture?v=" + strconv.FormatInt(it.own.PictureAt.Unix(), 10)
		look.Symbol = ""
	}
	return look, a, true
}

// lookOf is the look of one entry's text, with its warnings.
func (l listLooks) lookOf(articleID, text string, all []library.Article) entryLook {
	look, a, ok := l.resolve(articleID, text, all)
	if !ok {
		return look
	}
	for _, w := range library.PersonWarnings(a, l.st.persons) {
		look.Warns = append(look.Warns, tileWarn{Chip: l.st.chips[w.Person], Allergens: l.s.allergenWords(l.st.lang, w.Allergens)})
	}
	look.Long = l.s.warnLines(l.st, library.Warnings(a, l.st.persons))
	return look
}

// apply fills the looks of entries and tells whether one of them is food.
func (l listLooks) apply(lists ...[]entryView) (food bool) {
	all := l.articles()
	for _, es := range lists {
		for i := range es {
			es[i].Look = l.lookOf(es[i].ArticleID, es[i].Text, all)
			food = food || es[i].Look.Food
		}
	}
	return food
}

// freeTextNote is what ends a list that holds a food article while someone has
// a free-text allergy: the people and what they wrote.
func (l listLooks) freeTextNote(food bool) []freeTextPerson {
	if !food {
		return nil
	}
	return l.s.freeText(l.st)
}

// listViewOf is the view a person sees lists in: the choice made, else the
// default of the role.
func (s *server) listViewOf(p principal) (list.View, error) {
	v, err := s.db.PersonListView(p.household.ID, p.person.ID)
	if err != nil || v != "" {
		return v, err
	}
	return list.DefaultView(p.person.Role == store.RoleChild), nil
}
