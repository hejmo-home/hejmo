package server

import (
	"fmt"
	"html"
	"html/template"
	"strings"
	"sync"
	"time"

	"github.com/hejmo-home/hejmo/internal/food"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// The article library on the web and over the API (ticket 164, ADR 0019).
// libState gathers what the library needs of a household: the people with
// their diets and allergies, the articles it made, which shipped ones were
// hidden by hand and the experimental setting that hides articles with an
// allergen.

// articleItem is a shipped or an own article, ready to be shown.
type articleItem struct {
	library.Article
	own        *store.OwnArticle
	handHidden bool
}

type libState struct {
	p        principal
	lang     string
	people   []store.Person
	chips    map[string]personChip
	persons  []library.Person // in the order of the household
	foods    map[string]store.Food
	settings store.LibrarySettings
	hiding   library.Hiding
	hidden   map[string]bool
	own      []store.OwnArticle
}

func (s *server) libState(p principal, lang string) (libState, error) {
	st := libState{p: p, lang: lang, chips: map[string]personChip{}}
	var err error
	if st.people, err = s.db.People(p.household.ID); err != nil {
		return st, err
	}
	for _, c := range chipsOf(st.people) {
		st.chips[c.ID] = c
	}
	if st.foods, err = s.db.HouseholdFood(p.household.ID); err != nil {
		return st, err
	}
	for _, x := range st.people {
		f := st.foods[x.ID]
		d := food.Diet(f.Diet)
		if !food.ValidDiet(f.Diet) {
			d = food.Omnivore
		}
		st.persons = append(st.persons, library.Person{ID: x.ID, Diet: d, Allergens: f.Allergens})
	}
	if st.settings, err = s.db.LibrarySettings(p.household.ID); err != nil {
		return st, err
	}
	st.hiding = library.Hiding{Enabled: st.settings.HideAllergen, Everyone: st.settings.Everyone, People: st.settings.People}
	if st.hidden, err = s.db.HiddenArticles(p.household.ID); err != nil {
		return st, err
	}
	st.own, err = s.db.Articles(p.household.ID)
	return st, err
}

// ownArticle turns a stored own article into the library's article.
func ownArticle(o store.OwnArticle) library.Article {
	units := []string{o.Unit1}
	if o.Unit2 != "" {
		units = append(units, o.Unit2)
	}
	sym := ""
	if o.PicSource == "symbol" {
		sym = o.Symbol
	}
	return library.Article{ID: o.ID, Own: true, De: o.Name, En: o.Name, Dept: o.Dept, Kind: library.Kind(o.Kind), Symbol: sym, Allergens: o.Allergens, Units: units}
}

// items returns own articles (oldest first) and then the shipped ones.
func (st libState) items() []articleItem {
	var out []articleItem
	for i := range st.own {
		o := st.own[i]
		out = append(out, articleItem{Article: ownArticle(o), own: &o})
	}
	for _, a := range library.Shipped() {
		out = append(out, articleItem{Article: a, handHidden: st.hidden[a.ID]})
	}
	return out
}

// item finds a shipped article by key or an own one by id.
func (st libState) item(id string) (articleItem, bool) {
	for i := range st.own {
		if st.own[i].ID == id {
			o := st.own[i]
			return articleItem{Article: ownArticle(o), own: &o}, true
		}
	}
	if a, ok := library.ShippedByID(id); ok {
		return articleItem{Article: a, handHidden: st.hidden[id]}, true
	}
	return articleItem{}, false
}

func (st libState) suggestion(it articleItem) library.Suggestion {
	return library.Suggest(it.Article, st.persons, it.handHidden, st.hiding)
}

func (st libState) warnings(it articleItem) []library.Warning {
	return library.Warnings(it.Article, st.persons)
}

// names returns the plain names of the people with these ids, in the given order.
func (st libState) names(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, st.chips[id].Name)
	}
	return out
}

// allergenWords is the allergens in words, in the alphabetical order of the page language.
func (s *server) allergenWords(lang string, keys []string) string {
	names := make([]string, len(keys))
	for i, k := range keys {
		names[i] = s.allergenName(lang, k)
	}
	sortNames(lang, names)
	return s.joinWords(lang, names)
}

// whoHTML is people as marker and name, joined like words; the names are escaped.
func (s *server) whoHTML(st libState, ids []string) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		c := st.chips[id]
		parts = append(parts, fmt.Sprintf(`<span class="who"><span class="marker" data-person="%d" data-shape="%s" aria-hidden="true"></span>%s</span>`, c.Slot, html.EscapeString(c.Shape), html.EscapeString(c.Name)))
	}
	return s.joinWords(st.lang, parts)
}

const triangleIcon = `<svg class="icon" width="18" height="18" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 4l9 16H3z"/><path d="M12 10v4M12 17v.5"/></svg>`

// warnHTML is one warning line in words: "Contains peanuts: ● Mia is allergic".
// Several people with the same allergens share a line, each named.
func (s *server) warnHTML(st libState, w library.Warning) template.HTML {
	key := "lib.warn.one"
	if len(w.People) > 1 {
		key = "lib.warn.many"
	}
	text := html.EscapeString(s.i18n.T(st.lang, key))
	text = repl(text, "{allergens}", html.EscapeString(s.allergenWords(st.lang, w.Allergens)), "{who}", s.whoHTML(st, w.People))
	return template.HTML(`<span class="allergen-warn">` + triangleIcon + `<span>` + text + `</span></span>`)
}

func (s *server) warnLines(st libState, ws []library.Warning) []template.HTML {
	out := make([]template.HTML, 0, len(ws))
	for _, w := range ws {
		out = append(out, s.warnHTML(st, w))
	}
	return out
}

// unitName is a unit in a sentence ("Stück", "g"); unitLong is the full name for the chips.
func (s *server) unitName(lang, key string) string { return s.i18n.T(lang, "unit."+key) }
func (s *server) unitLong(lang, key string) string {
	return s.i18n.T(lang, "unit."+key+".long")
}

// offeredUnits are the article's default units that are switched on; piece
// when neither is.
func (s *server) offeredUnits(p principal, a library.Article) []string {
	return unit.Offered(a.Units, s.activeUnits(p.household))
}

func (s *server) unitsWords(lang string, keys []string) string {
	names := make([]string, len(keys))
	for i, k := range keys {
		names[i] = s.unitName(lang, k)
	}
	if len(names) == 2 {
		return s.i18n.Format(lang, i18n.Msg("art.units.pair", "a", names[0], "b", names[1]))
	}
	return strings.Join(names, ", ")
}

// humanBytes writes a size the way a person says it: "41 KB", "38,2 MB".
func humanBytes(lang string, n int64) string {
	if n < 1<<20 {
		return fmt.Sprintf("%d KB", max(1, (n+512)/1024))
	}
	s := fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	if lang != "en" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

func (s *server) kindWord(lang string, k library.Kind) string {
	return s.i18n.T(lang, "lib.kind."+string(k))
}

// pending pictures: a picture made from an upload or a download waits here,
// under a token that travels in the form, until the article is saved. It is
// in the box's memory only, for 30 minutes, and at most 24 of them.
type pendingPicture struct {
	store.ArticlePicture
	File     string // the name of the uploaded file
	ToW, ToH int
	Expires  time.Time
}

type pendingPictures struct {
	mu sync.Mutex
	m  map[string]pendingPicture
}

const (
	pendingTTL = 30 * time.Minute
	pendingMax = 24
)

func (pp *pendingPictures) put(token string, p pendingPicture, now time.Time) {
	pp.mu.Lock()
	defer pp.mu.Unlock()
	if pp.m == nil {
		pp.m = map[string]pendingPicture{}
	}
	for k, v := range pp.m {
		if now.After(v.Expires) {
			delete(pp.m, k)
		}
	}
	for len(pp.m) >= pendingMax {
		oldest, at := "", now.Add(time.Hour)
		for k, v := range pp.m {
			if v.Expires.Before(at) {
				oldest, at = k, v.Expires
			}
		}
		delete(pp.m, oldest)
	}
	p.Expires = now.Add(pendingTTL)
	pp.m[token] = p
}

func (pp *pendingPictures) get(token string, now time.Time) (pendingPicture, bool) {
	pp.mu.Lock()
	defer pp.mu.Unlock()
	p, ok := pp.m[token]
	if !ok || now.After(p.Expires) {
		return pendingPicture{}, false
	}
	return p, true
}

func (pp *pendingPictures) drop(token string) {
	pp.mu.Lock()
	defer pp.mu.Unlock()
	delete(pp.m, token)
}
