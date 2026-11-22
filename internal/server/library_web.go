package server

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/text/collate"
	textlang "golang.org/x/text/language"

	"github.com/hejmo-home/hejmo/internal/always"
	"github.com/hejmo-home/hejmo/internal/food"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

// artTag is a word label on an article row.
type artTag struct {
	Text, Class string
	// Icon is "repeat" or "pause" for the tags of "always on the list".
	Icon string
}

// artRow is one article in the library: symbol or picture, name, what it is
// in words, tags, and one warning line per group of affected people.
type artRow struct {
	Href, Symbol, Picture, Name string
	Meta                        []string
	Tags                        []artTag
	Warns                       []template.HTML
}

// deptCard is one department of the shipped articles: closed, with its counts in words.
type deptCard struct {
	Symbol, Name, Meta string
	Rows               []artRow
}

func pictureURL(o store.OwnArticle) string {
	return "/articles/" + o.ID + "/picture?v=" + strconv.FormatInt(o.PictureAt.Unix(), 10)
}

// symbolOf is the emoji an article shows when it has no picture.
func symbolOf(it articleItem) string {
	if it.Symbol != "" {
		return it.Symbol
	}
	return library.DeptSymbol(it.Dept)
}

// artRow builds a row. withDept puts the department and the origin into the
// meta words (search and filters); otherwise only what is in it (in a department).
func (s *server) artRowOf(st libState, it articleItem, flat bool) artRow {
	row := artRow{Href: libraryPath + "/" + it.ID, Name: it.Name(st.lang), Symbol: symbolOf(it)}
	kind := s.kindWord(st.lang, it.Kind)
	switch {
	case it.own != nil && it.own.HasPicture:
		row.Picture = pictureURL(*it.own)
	}
	if flat || it.own != nil {
		row.Meta = append(row.Meta, s.i18n.T(st.lang, "dept."+it.Dept), kind)
		switch {
		case it.own == nil:
			row.Meta = append(row.Meta, s.i18n.T(st.lang, "lib.from.hejmo"))
		case flat:
			row.Meta = append(row.Meta, s.i18n.T(st.lang, "lib.from.own"))
		default:
			row.Meta = append(row.Meta, s.picWord(st.lang, *it.own))
		}
	} else {
		row.Meta = []string{kind}
	}
	if it.handHidden {
		row.Tags = append(row.Tags, artTag{Text: s.i18n.T(st.lang, "lib.tag.hand"), Class: "tag hidden-tag"})
	}
	if by := st.hiding.HiddenFor(it.Article, st.persons); len(by) > 0 {
		row.Tags = append(row.Tags, artTag{Text: s.i18n.Format(st.lang, i18n.Msg("lib.tag.allergy", "names", s.joinWords(st.lang, st.names(by)))), Class: "tag exp"})
	}
	for i := 0; i < len(row.Meta)-1; i++ { // the separator ends a part, so no line starts with a dot
		row.Meta[i] += " ·"
	}
	if r, ok := st.always[it.ID]; ok {
		row.Tags = append(row.Tags, s.alwaysTag(st, r))
	}
	row.Warns = s.warnLines(st, st.warnings(it))
	return row
}

// picWord says where an own article's picture comes from, in words.
func (s *server) picWord(lang string, o store.OwnArticle) string {
	switch o.PicSource {
	case "upload":
		return s.i18n.T(lang, "lib.pic.photo")
	case "web":
		return s.i18n.T(lang, "lib.pic.web")
	case "symbol":
		return s.i18n.T(lang, "lib.pic.symbol")
	}
	return s.i18n.T(lang, "lib.pic.none")
}

// sortItems puts articles in the alphabetical order of the page language.
func sortItems(lang string, items []articleItem) {
	c := collate.New(textlang.Make(lang))
	slices.SortStableFunc(items, func(a, b articleItem) int { return c.CompareString(a.Name(lang), b.Name(lang)) })
}

func (s *server) countWords(lang string, n int, one, many string) string {
	if n == 1 {
		return s.i18n.T(lang, one)
	}
	return s.i18n.Format(lang, i18n.Msg(many, "n", strconv.Itoa(n)))
}

func (s *server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	st, err := s.libState(p, lang)
	if err != nil {
		s.fail(w, "library state", err)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	f := r.URL.Query().Get("f")
	if f != "warn" && f != "hidden" && f != "auto" {
		f = "all"
	}
	items := st.items()
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Q": q, "F": f,
		"BackHref": listsSettingsPath, "BackText": s.i18n.T(lang, "lists.set.back"),
		"FreeText": s.freeText(st),
	}
	filterHref := func(f string) string {
		v := url.Values{"f": {f}}
		if q != "" {
			v.Set("q", q)
		}
		return "?" + v.Encode()
	}
	data["Filters"] = []map[string]any{
		{"Href": filterHref("all"), "Text": s.i18n.T(lang, "lib.filter.all"), "Current": f == "all"},
		{"Href": filterHref("warn"), "Text": s.i18n.T(lang, "lib.filter.warn"), "Current": f == "warn"},
		{"Href": filterHref("hidden"), "Text": s.i18n.T(lang, "lib.filter.hidden"), "Current": f == "hidden"},
		{"Href": filterHref("auto"), "Text": s.i18n.T(lang, "lib.filter.auto"), "Current": f == "auto"},
	}

	if q != "" || f != "all" {
		var hits []articleItem
		for _, it := range items {
			if q != "" && !library.Matches(it.Article, q) {
				continue
			}
			switch f {
			case "warn":
				if len(st.warnings(it)) == 0 {
					continue
				}
			case "hidden":
				if r := st.suggestion(it).Reason; r != library.ReasonHand && r != library.ReasonAllergy {
					continue
				}
			case "auto":
				if _, ok := st.always[it.ID]; !ok {
					continue
				}
			}
			hits = append(hits, it)
		}
		sortItems(lang, hits)
		rows := make([]artRow, len(hits))
		for i, it := range hits {
			rows[i] = s.artRowOf(st, it, true)
		}
		data["Flat"], data["Rows"] = true, rows
		switch {
		case q != "":
			data["ResultTitle"] = s.i18n.T(lang, "lib.result.title")
			head := "lib.result.count.many"
			if len(hits) == 1 {
				head = "lib.result.count.one"
			}
			data["Head"] = s.i18n.Format(lang, i18n.Msg(head, "n", strconv.Itoa(len(hits)), "q", q))
			data["ClearHref"] = libraryPath
			data["NoneText"] = s.i18n.Format(lang, i18n.Msg("lib.result.none", "q", q))
			data["NewHref"] = libraryPath + "/new?" + url.Values{"name": {q}}.Encode()
			data["NewText"] = s.i18n.Format(lang, i18n.Msg("lib.result.new", "q", q))
		case f == "auto":
			data["Auto"], data["ClearHref"] = true, libraryPath
			data["AutoResult"] = s.autoResult(st, hits)
			data["NoneText"] = s.i18n.T(lang, "always.res.none")
		default:
			data["ResultTitle"] = s.i18n.T(lang, "lib.filter."+f+".title")
			data["Head"] = s.countWords(lang, len(hits), "lib.filter."+f+".count.one", "lib.filter."+f+".count.many")
			data["ClearHref"] = libraryPath
			data["NoneText"] = s.i18n.T(lang, "lib.filter."+f+".none")
		}
		s.render(w, r, http.StatusOK, "library.html", data)
		return
	}

	var own []articleItem
	byDept := map[string][]articleItem{}
	for _, it := range items {
		if it.own != nil {
			own = append([]articleItem{it}, own...) // newest first
			continue
		}
		byDept[it.Dept] = append(byDept[it.Dept], it)
	}
	ownRows := make([]artRow, len(own))
	for i, it := range own {
		ownRows[i] = s.artRowOf(st, it, false)
	}
	var depts []deptCard
	for _, d := range library.Depts {
		list := byDept[d]
		if len(list) == 0 {
			continue
		}
		sortItems(lang, list)
		var hand, warn int
		rows := make([]artRow, len(list))
		for i, it := range list {
			rows[i] = s.artRowOf(st, it, false)
			if it.handHidden {
				hand++
			}
			if len(rows[i].Warns) > 0 {
				warn++
			}
		}
		meta := []string{s.countWords(lang, len(list), "lib.count.one", "lib.count.many")}
		if hand > 0 {
			meta = append(meta, s.i18n.Format(lang, i18n.Msg("lib.count.hidden", "n", strconv.Itoa(hand))))
		}
		if warn > 0 {
			meta = append(meta, s.i18n.Format(lang, i18n.Msg("lib.count.warn", "n", strconv.Itoa(warn))))
		}
		depts = append(depts, deptCard{Symbol: library.DeptSymbol(d), Name: s.i18n.T(lang, "dept."+d), Meta: strings.Join(meta, " · "), Rows: rows})
	}
	data["OwnRows"], data["Depts"] = ownRows, depts
	data["ShippedHint"] = s.i18n.Format(lang, i18n.Msg("lib.shipped.hint", "n", strconv.Itoa(len(library.Shipped()))))
	s.render(w, r, http.StatusOK, "library.html", data)
}

// listLink is a list an article currently stands on.
type listLink struct{ ID, Name string }

// listsWith returns the lists that have an entry of this text.
func (s *server) listsWith(p principal, name string, includeDone bool) ([]listLink, error) {
	lists, err := s.db.Lists(p.household.ID)
	if err != nil {
		return nil, err
	}
	want := library.Fold(name)
	var out []listLink
	for _, l := range lists {
		es, err := s.db.Entries(p.household.ID, l.ID)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(es, func(e list.Entry) bool { return (includeDone || !e.Done) && library.Fold(e.Text) == want }) {
			out = append(out, listLink{l.ID, l.Name})
		}
	}
	return out, nil
}

// dietGroups groups people by diet in the order of food.Diets: "Pescetarisch: ● Mama".
type dietGroup struct {
	Diet string
	Who  []personChip
}

func (s *server) dietGroups(st libState, people []library.Person) []dietGroup {
	var out []dietGroup
	for _, d := range food.Diets {
		var g dietGroup
		for _, x := range people {
			if x.Diet == d {
				g.Who = append(g.Who, st.chips[x.ID])
			}
		}
		if len(g.Who) > 0 {
			g.Diet = s.i18n.T(st.lang, "diet."+string(d))
			out = append(out, g)
		}
	}
	return out
}

func (s *server) handleArticle(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	st, err := s.libState(p, lang)
	if err != nil {
		s.fail(w, "library state", err)
		return
	}
	it, ok := st.item(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if it.own != nil {
		s.renderArticleForm(w, r, p, http.StatusOK, st, it.own, s.formOf(*it.own), nil, nil)
		return
	}
	s.renderShipped(w, r, p, http.StatusOK, st, it, nil)
}

func (s *server) renderShipped(w http.ResponseWriter, r *http.Request, p principal, status int, st libState, it articleItem, ap *autoPost) {
	lang := st.lang
	name := it.Name(lang)
	sug := st.suggestion(it)
	offered := s.offeredUnits(p, it.Article)
	other := it.En
	otherLang := "en"
	if lang == "en" {
		other, otherLang = it.De, "de"
	}
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID),
		"BackHref": libraryPath, "BackText": s.i18n.T(lang, "art.back"),
		"Name": name, "Symbol": symbolOf(it), "ID": it.ID,
		"From":  s.i18n.Format(lang, i18n.Msg("art.from.hejmo", "lang", s.i18n.T(lang, "art.lang."+otherLang), "name", other)),
		"Dept":  s.i18n.T(lang, "dept."+it.Dept),
		"Units": s.unitsWords(lang, offered), "UnitsHint": s.i18n.Format(lang, i18n.Msg("art.units.hint", "unit", s.unitName(lang, offered[0]))),
		"Kind": s.i18n.T(lang, "art.kind."+string(it.Kind)), "IsFood": it.Kind != library.NonFood,
		"Hidden": it.handHidden,
	}
	fit, notFit := library.FitsPeople(it.Article, st.persons)
	switch {
	case len(st.persons) == 0 || len(notFit) == 0:
		data["FitsAll"] = s.i18n.T(lang, "art.fits.all")
	case len(fit) == 0:
		data["FitsNobody"] = s.i18n.T(lang, "art.fits.nobody")
		data["NotFits"] = s.dietGroups(st, notFit)
	default:
		data["Fits"] = s.dietGroups(st, fit)
		data["NotFits"] = s.dietGroups(st, notFit)
	}
	if it.Kind != library.NonFood {
		data["Contains"] = s.i18n.T(lang, "art.contains.none")
		if len(it.Allergens) > 0 {
			data["Contains"] = s.allergenWords(lang, it.Allergens)
		}
	}
	data["Warns"] = s.warnLines(st, st.warnings(it))
	switch sug.Reason {
	case library.ReasonHand:
		data["Suggested"] = s.i18n.T(lang, "art.sug.hand")
	case library.ReasonAllergy:
		data["Suggested"] = s.i18n.Format(lang, i18n.Msg("art.sug.allergy", "names", s.joinWords(lang, st.names(sug.By))))
		data["SuggestedExp"] = true
	case library.ReasonDiet:
		data["Suggested"] = s.i18n.T(lang, "art.sug.diet")
	}
	off := s.i18n.Format(lang, i18n.Msg("art.hide.off", "name", name))
	switch sug.Reason {
	case library.ReasonAllergy:
		off = s.i18n.Format(lang, i18n.Msg("art.hide.off.allergy", "names", s.joinWords(lang, st.names(sug.By)), "name", name))
	case library.ReasonDiet:
		off = s.i18n.Format(lang, i18n.Msg("art.hide.off.diet", "name", name))
	}
	data["HideOn"] = s.i18n.Format(lang, i18n.Msg("art.hide.on", "name", name))
	data["HideOff"] = off
	data["HideAria"] = s.i18n.Format(lang, i18n.Msg("art.hide.aria", "name", name))
	lists, err := s.listsWith(p, name, false)
	if err != nil {
		s.fail(w, "lists", err)
		return
	}
	data["OnLists"] = s.listLinks(lang, lists)
	if ap != nil {
		data["Hidden"] = r.PostFormValue("hidden") != ""
	}
	card, err := s.autoCardFor(p, lang, it, ap)
	if err != nil {
		s.fail(w, "always card", err)
		return
	}
	data["Auto"] = card
	s.render(w, r, status, "article.html", data)
}

func (s *server) handleArticleHidden(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	a, ok := library.ShippedByID(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	hide := r.PostFormValue("hidden") != ""
	lang := s.langOf(r)
	var autoKey string
	var autoValues []string
	if f := readAuto(r); f.Present {
		it, _ := s.itemOfShipped(p, lang, id)
		key, values, ap, err := s.saveAuto(p, lang, it, f)
		if err != nil {
			s.fail(w, "save always on the list", err)
			return
		}
		if ap != nil {
			st, err := s.libState(p, lang)
			if err != nil {
				s.fail(w, "library state", err)
				return
			}
			s.renderShipped(w, r, p, http.StatusUnprocessableEntity, st, it, ap)
			return
		}
		autoKey, autoValues = key, values
	}
	if err := s.db.SetArticleHidden(p.household.ID, id, hide); err != nil {
		s.fail(w, "hide article", err)
		return
	}
	if autoKey != "" {
		s.redirectSavedFields(w, r, libraryPath+"/"+id, autoKey, autoValues...)
		return
	}
	key := "flash.article_shown"
	if hide {
		key = "flash.article_hidden"
	}
	s.redirectSavedNamed(w, r, libraryPath+"/"+id, key, a.Name(lang))
}

func (s *server) handleArticleCopy(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	a, ok := library.ShippedByID(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	own := store.OwnArticle{Name: a.Name(lang), Dept: a.Dept, Kind: string(a.Kind), Allergens: a.Allergens, Unit1: a.Units[0]}
	if len(a.Units) > 1 {
		own.Unit2 = a.Units[1]
	}
	if own.Unit1 == "" || !slices.Contains(s.activeUnits(p.household), own.Unit1) { // a copy carries the units too; a switched-off one gives way to piece
		own.Unit1 = s.offeredUnits(p, a)[0]
		own.Unit2 = ""
	}
	if own.Unit2 != "" && !slices.Contains(s.activeUnits(p.household), own.Unit2) {
		own.Unit2 = ""
	}
	saved, err := s.db.CreateArticle(p.household.ID, p.person.ID, own, nil)
	if err != nil {
		s.fail(w, "copy article", err)
		return
	}
	if err := s.db.SetArticleHidden(p.household.ID, a.ID, true); err != nil {
		s.fail(w, "hide copied article", err)
		return
	}
	if err := s.db.MoveAlwaysRule(p.household.ID, a.ID, saved.ID); err != nil { // "always on the list" goes with the copy
		s.fail(w, "move always rule", err)
		return
	}
	s.redirectSavedNamed(w, r, libraryPath+"/"+saved.ID, "flash.article_copied", a.Name(lang))
}

func (s *server) handleArticleDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	a, err := s.db.Article(p.household.ID, r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.db.DeleteArticle(p.household.ID, a.ID); err != nil {
		s.fail(w, "delete article", err)
		return
	}
	s.redirectSavedNamed(w, r, libraryPath, "flash.article_deleted", a.Name)
}

// handleArticlePicture serves the stored picture of an own article to any
// signed-in member: children see it in the tile view.
func (s *server) handleArticlePicture(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	b, err := s.db.ArticlePicture(p.household.ID, r.PathValue("id"))
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

// handleArticleSuggest answers the live list of similar articles under the
// name field of the own article form.
func (s *server) handleArticleSuggest(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	st, err := s.libState(p, lang)
	if err != nil {
		s.fail(w, "library state", err)
		return
	}
	items := st.items()
	arts := make([]library.Article, len(items))
	for i, it := range items {
		arts[i] = it.Article
	}
	type sugg struct {
		Href, Name, Sub, Symbol, Picture string
	}
	out := []sugg{}
	for _, a := range library.Similar(r.URL.Query().Get("q"), arts, lang, 5) {
		it, _ := st.item(a.ID)
		sub := []string{s.i18n.T(lang, "lib.from.hejmo"), s.i18n.T(lang, "dept."+it.Dept)}
		sg := sugg{Href: libraryPath + "/" + it.ID, Name: it.Name(lang), Symbol: symbolOf(it)}
		if it.own != nil {
			sub[0] = s.i18n.T(lang, "lib.from.own")
			if it.own.HasPicture {
				sg.Picture = pictureURL(*it.own)
			}
		}
		if it.handHidden {
			sub = append(sub, s.i18n.T(lang, "lib.tag.hand"))
		}
		sg.Sub = strings.Join(sub, " · ")
		out = append(out, sg)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": out, "count": fmt.Sprint(len(out))})
}

// listLinks is lists as links, joined like words; the names are escaped.
func (s *server) listLinks(lang string, lists []listLink) template.HTML {
	parts := make([]string, len(lists))
	for i, l := range lists {
		parts[i] = `<a href="/lists/` + html.EscapeString(l.ID) + `">` + html.EscapeString(l.Name) + `</a>`
	}
	return template.HTML(s.joinWords(lang, parts))
}

// itemOfShipped is a shipped article as an item of the library state.
func (s *server) itemOfShipped(p principal, lang, id string) (articleItem, error) {
	st, err := s.libState(p, lang)
	if err != nil {
		return articleItem{}, err
	}
	it, _ := st.item(id)
	return it, nil
}

// alwaysTag is the tag of an article with a rule: its rhythm and list in
// words, or why it is not running.
func (s *server) alwaysTag(st libState, r always.Rule) artTag {
	a := &alwaysCtx{s: s, lang: st.lang}
	switch {
	case r.Paused():
		return artTag{Text: s.i18n.T(st.lang, "always.tag.paused"), Class: "tag paused", Icon: "pause"}
	case !r.Enabled:
		return artTag{Text: s.i18n.T(st.lang, "always.tag.off.lib"), Class: "tag hidden-tag"}
	}
	return artTag{Text: a.rhythmOn(r), Class: "tag auto", Icon: "repeat"}
}

// autoResult is the sentence over the filter "Immer auf der Liste": how many
// articles have a rule, how many come by themselves, and which do not, by name.
func (s *server) autoResult(st libState, hits []articleItem) string {
	var running int
	var paused, off []string
	for _, it := range hits {
		switch r := st.always[it.ID]; {
		case r.Paused():
			paused = append(paused, it.Name(st.lang))
		case !r.Enabled:
			off = append(off, it.Name(st.lang))
		default:
			running++
		}
	}
	var parts []string
	switch {
	case running == 1:
		parts = append(parts, s.i18n.T(st.lang, "always.res.running.one"))
	case running > 1:
		parts = append(parts, s.i18n.Format(st.lang, i18n.Msg("always.res.running.many", "n", strconv.Itoa(running))))
	}
	for _, g := range []struct {
		names     []string
		one, many string
	}{{paused, "always.res.paused.one", "always.res.paused.many"}, {off, "always.res.off.one", "always.res.off.many"}} {
		key := g.many
		if len(g.names) == 1 {
			key = g.one
		}
		if len(g.names) > 0 {
			parts = append(parts, s.i18n.Format(st.lang, i18n.Msg(key, "names", s.joinWords(st.lang, g.names))))
		}
	}
	head := "always.res.many"
	if len(hits) == 1 {
		head = "always.res.one"
	}
	return s.i18n.Format(st.lang, i18n.Msg(head, "n", strconv.Itoa(len(hits)), "parts", strings.Join(parts, ", ")))
}
