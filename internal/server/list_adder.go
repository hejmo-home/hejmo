package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// The add card, the quantity editor and the sentences around them (ticket 165,
// docs/design/preview-165.html). The web interface and the displays share
// them; only the paths and a few words differ.

// ---- words for quantities

// ambiguous says that the household has both the US and the UK unit of this
// name on: only then "(US)" and "(GB)" are written.
func (c *itemCtx) ambiguous(key string) bool {
	for _, pair := range [][2]string{{"floz_us", "floz_uk"}, {"pint_us", "pint_uk"}, {"gallon_us", "gallon_uk"}} {
		if key == pair[0] || key == pair[1] {
			return slices.Contains(c.active, pair[0]) && slices.Contains(c.active, pair[1])
		}
	}
	return false
}

func (c *itemCtx) suffix(key string) string {
	if !c.ambiguous(key) {
		return ""
	}
	switch {
	case strings.HasSuffix(key, "_us"):
		return " " + c.s.i18n.T(c.lang, "unit.suffix.us")
	case strings.HasSuffix(key, "_uk"):
		return " " + c.s.i18n.T(c.lang, "unit.suffix.uk")
	}
	return ""
}

// unitWord is the unit in a sentence: "Packung", "Packungen", "pint (US)".
func (c *itemCtx) unitWord(key string, plural bool) string {
	form := ".one"
	if plural {
		form = ".other"
	}
	return c.s.i18n.T(c.lang, "unit."+key+form) + c.suffix(key)
}

// qtyWords is a quantity in words: "6 Stück", "1,5 kg", "3 pints".
func (c *itemCtx) qtyWords(q list.Quantity) string {
	if q.None() {
		return ""
	}
	n := unit.Round(q.Amount(), q.Unit)
	return unit.Format(q.Amount(), q.Unit, c.lang) + " " + c.unitWord(q.Unit, n != 1)
}

func bold(s string) string { return "<b>" + html.EscapeString(s) + "</b>" }

func (c *itemCtx) boldQty(q list.Quantity) string { return bold(c.qtyWords(q)) }

func (c *itemCtx) msg(key string, kv ...string) string {
	return c.s.i18n.Format(c.lang, i18n.Msg(key, kv...))
}

// htmlMsg fills a text whose values are markup (already escaped) and escapes the rest.
func (c *itemCtx) htmlMsg(key string, kv ...string) template.HTML {
	text := html.EscapeString(c.s.i18n.T(c.lang, key))
	pairs := make([]string, 0, len(kv))
	for i := 0; i+1 < len(kv); i += 2 {
		pairs = append(pairs, "{"+kv[i]+"}", kv[i+1])
	}
	return template.HTML(repl(text, pairs...))
}

func (c *itemCtx) kindNoun(k unit.Kind) string { return c.s.i18n.T(c.lang, "lq.kind."+string(k)) }

func isMeasure(k unit.Kind) bool { return k == unit.Weight || k == unit.Volume }

// cannotConvert says in a few words why two kinds do not add up: "Wie viel Gramm in einer Packung sind, weiß Hejmo nicht."
func (c *itemCtx) cannotConvert(old, add list.Quantity) (a, b string, measure bool) {
	ok, ak := old.Kind(), add.Kind()
	switch {
	case isMeasure(ok) && !isMeasure(ak):
		return c.s.i18n.T(c.lang, "lq.kind."+string(ak)+".in"), c.kindNoun(ok), true
	case isMeasure(ak) && !isMeasure(ok):
		return c.s.i18n.T(c.lang, "lq.kind."+string(ok)+".in"), c.kindNoun(ak), true
	}
	return c.kindNoun(ok), c.kindNoun(ak), false
}

// entryLabel is the entry in words: "Mehl · Type 405", the notation everywhere.
func entryLabel(text, variety string) string {
	if variety == "" {
		return text
	}
	return text + " · " + variety
}

// varietyOf reads what the Sorte row posted: the chip chosen or, with "new",
// the text typed. isNew says the person chose "Neue Sorte".
func varietyOf(pick, text string) (variety string, isNew bool) {
	if pick == "new" {
		return list.NormalizeVariety(text), true
	}
	return list.NormalizeVariety(pick), false
}

// varietyView is the "Sorte:" row of the add card and of the editor.
type varietyView struct {
	PickName, TextName, TextID string
	Options                    []string // the varieties used before
	Picked                     string   // the value of the checked chip: "", a variety or "new"
	Text                       string   // the text of the new variety
	State                      string   // the closed row in words
	Chosen                     bool     // State is a variety: shown as a filled pill
	Error                      string
	Open                       bool
	Hint                       string
}

// varietyFields builds the Sorte row of an article. pick and text are what
// the form said ("" and "" for no variety); prefix is "" (add card) or "q" (editor).
func (c *itemCtx) varietyFields(art articleItem, prefix, pick, text, errKey string) *varietyView {
	v := &varietyView{PickName: prefix + "variety_pick", TextName: prefix + "variety", TextID: prefix + "variety", Options: c.varieties(art.ID),
		Hint: c.msg("lq.var.hint", "name", art.Name(c.lang))}
	cur, isNew := varietyOf(pick, text)
	if i := slices.IndexFunc(v.Options, func(o string) bool { return list.VarietyKey(o) == list.VarietyKey(cur) }); isNew && cur != "" && errKey == "" && i >= 0 {
		isNew, cur = false, v.Options[i] // a variety used before is its chip
	}
	if isNew {
		v.Picked, v.Text = "new", list.NormalizeVariety(text)
		if v.Text == "" && text != "" {
			v.Text = text // keep what was typed, spaces and all
		}
	} else if cur != "" {
		if i := slices.IndexFunc(v.Options, func(o string) bool { return list.VarietyKey(o) == list.VarietyKey(cur) }); i >= 0 {
			cur = v.Options[i]
		} else {
			v.Options = append([]string{cur}, v.Options...)
		}
		v.Picked = cur
	}
	switch {
	case isNew && cur == "":
		v.State = c.msg("lq.var.empty")
	case cur == "":
		v.State = c.msg("lq.var.none")
	default:
		v.State, v.Chosen = cur, true
	}
	if errKey != "" {
		v.Error, v.Open = c.s.i18n.T(c.lang, errKey), true
	}
	return v
}

// ---- views

// qtyChip is one unit to choose.
type qtyChip struct {
	Key, Label, Sub string
	Checked         bool
	Size, Step      float64
	Kind            string
}

func (c *itemCtx) chip(key, chosen string) qtyChip {
	ch := qtyChip{Key: key, Label: c.s.i18n.T(c.lang, "unit."+key+".one") + c.suffix(key), Checked: key == chosen, Size: unit.Size(key), Step: unit.Step(key), Kind: string(unit.KindOf(key))}
	if key == "dozen" {
		ch.Sub = c.s.i18n.T(c.lang, "unit.dozen.sub")
	}
	return ch
}

// qtyView is the quantity part: stepper, unit chips and the sentence.
type qtyView struct {
	AmountID, AmountName, UnitName string
	Amount, Unit                   string
	Error                          string
	MinusOff                       bool
	Offered, More                  []qtyChip
	MoreOpen                       bool
	MoreChosen                     string
	Merge                          template.HTML
	MoreHint                       template.HTML
	Display                        bool
	WithMerge                      bool // the sentence sits inside the fieldset
}

// qtyFields builds the quantity part for an article (nil: free text) with the
// chosen amount and unit.
func (c *itemCtx) qtyFields(art *articleItem, amount, chosen string, display bool) qtyView {
	offered := c.offered(art)
	if chosen == "" || !unit.Valid(chosen) {
		chosen = c.preselected(art)
	}
	v := qtyView{AmountID: "amount", AmountName: "amount", UnitName: "unit", Amount: amount, Unit: chosen, Display: display, WithMerge: true}
	if display {
		v.MoreHint = template.HTML(html.EscapeString(c.msg("lq.qty.more_hint.display")))
	} else {
		link := `<a href="/settings/lists#units">` + html.EscapeString(c.msg("lq.qty.settings_link")) + `</a>`
		v.MoreHint = template.HTML(repl(html.EscapeString(c.s.i18n.T(c.lang, "lq.qty.more_hint")), "{link}", link))
	}
	for _, k := range offered {
		v.Offered = append(v.Offered, c.chip(k, chosen))
	}
	for _, k := range unit.All {
		if !slices.Contains(c.active, k) || slices.Contains(offered, k) {
			continue
		}
		ch := c.chip(k, chosen)
		v.More = append(v.More, ch)
		if ch.Checked {
			v.MoreOpen, v.MoreChosen = true, ch.Label
		}
	}
	if n, err := unit.Parse(amount); err == nil {
		_, ok := unit.StepDown(n, chosen)
		v.MinusOff = !ok
	} else {
		v.MinusOff = true
	}
	if !slices.ContainsFunc(append(slices.Clone(v.Offered), v.More...), func(u qtyChip) bool { return u.Checked }) {
		v.Offered[0].Checked, v.Unit = true, v.Offered[0].Key // a unit that is off gives way to the first one offered
	}
	return v
}

// pickedView is the chosen article in words.
type pickedView struct {
	ID, Name, Variety, Symbol, Picture, Sub string
}

func (c *itemCtx) picked(it articleItem, variety string) *pickedView {
	key := "lq.picked.lib"
	if it.own != nil {
		key = "lq.picked.own"
	}
	pv := &pickedView{ID: it.ID, Name: it.Name(c.lang), Variety: variety, Symbol: symbolOf(it), Sub: c.msg(key, "dept", c.s.i18n.T(c.lang, "dept."+it.Dept))}
	if it.own != nil && it.own.HasPicture {
		pv.Picture = pictureURL(*it.own)
	}
	return pv
}

// suggRow is one suggested article.
type suggRow struct {
	ID, Name, Variety, Sub, Symbol, Picture string
	OnList                                  string
	Warns                                   []template.HTML
	Idx                                     int
}

type suggView struct {
	Mode, Q, Intro string
	Rows           []suggRow
	Free           bool   // the last row: the typed text
	FreeName       string // „Ei“ als Text hinzufügen
	FreeSub        string
	Hidden         template.HTML
	Unchecked      []template.HTML
	Any            bool
}

// maxSuggestions is how many articles are suggested.
const maxSuggestions = 4

// onList is what already stands on the list for an article, in words.
func (c *itemCtx) onList(es []list.Entry, articleID, variety, text string) string {
	var parts []string
	has := false
	for _, e := range es {
		if !e.Same(articleID, variety, text) {
			continue
		}
		has = true
		if !e.Qty.None() {
			parts = append(parts, c.qtyWords(e.Qty))
		}
	}
	switch {
	case !has:
		return ""
	case len(parts) == 0:
		return c.msg("lq.sugg.onlist.noqty")
	}
	return c.msg("lq.sugg.onlist", "qty", c.s.joinWords(c.lang, parts))
}

// suggest builds the suggestions for what was typed: up to four articles that
// fit the household's diets, the articles hidden because of an allergy, the
// last row that adds the text as it is, and the free-text allergies nobody can check.
func (c *itemCtx) suggest(es []list.Entry, typed, mode string) suggView {
	v := suggView{Mode: mode, Q: strings.TrimSpace(typed)}
	if len([]rune(library.Fold(typed))) < 2 {
		return v
	}
	v.Any = true
	var all []library.Article
	byID := map[string]articleItem{}
	for _, it := range c.st.items() {
		all = append(all, it.Article)
		byID[it.ID] = it
	}
	var shown []articleItem
	var hidden []articleItem
	var offered []library.Article // the articles that may be suggested at all
	for _, it := range c.st.items() {
		if c.st.suggestion(it).Suggested {
			offered = append(offered, it.Article)
		}
	}
	for _, a := range library.Complete(typed, all, 200) {
		it := byID[a.ID]
		switch sg := c.st.suggestion(it); {
		case sg.Suggested:
			if len(shown) < maxSuggestions {
				shown = append(shown, it)
			}
		case sg.Reason == library.ReasonAllergy:
			hidden = append(hidden, it)
		}
	}
	// "Mehl Type 405": the article and its variety, after the articles the text
	// completes, and never when the whole text is an article's name.
	lead, typedVariety := articleItem{}, ""
	if _, named := library.Resolve(typed, all); !named {
		if a, rest, ok := library.LeadingName(typed, offered); ok {
			lead, typedVariety = byID[a.ID], rest
			if !slices.ContainsFunc(shown, func(it articleItem) bool { return it.ID == lead.ID }) {
				shown = append(shown, lead)
			}
		}
	}
	// One row per article, then one per variety: the one typed first, the
	// article without one, then the varieties the household used, those on the list first.
	row := func(it articleItem, variety string) suggRow {
		r := suggRow{ID: it.ID, Name: it.Name(c.lang), Variety: variety, Symbol: symbolOf(it), Sub: c.s.i18n.T(c.lang, "dept."+it.Dept)}
		if it.own != nil {
			r.Sub = c.s.i18n.T(c.lang, "lib.from.own") + " · " + r.Sub
			r.Sub = strings.ToUpper(r.Sub[:1]) + r.Sub[1:]
			if it.own.HasPicture {
				r.Picture = pictureURL(*it.own)
			}
		}
		r.OnList = c.onList(es, it.ID, variety, r.Name)
		r.Warns = c.s.warnLines(c.st, c.st.warnings(it))
		return r
	}
	var rows []suggRow
	for _, it := range shown {
		known := c.varieties(it.ID)
		slices.SortStableFunc(known, func(a, b string) int { return onListRank(es, it.ID, a) - onListRank(es, it.ID, b) })
		if typedVariety != "" && it.ID == lead.ID {
			r := row(it, typedVariety)
			key := "lq.var.sub.new"
			if slices.ContainsFunc(known, func(k string) bool { return list.VarietyKey(k) == list.VarietyKey(typedVariety) }) {
				key = "lq.var.sub.known"
			}
			r.Sub = c.msg(key, "name", r.Name) + " · " + r.Sub
			rows = append(rows, r)
		}
		rows = append(rows, row(it, ""))
		for _, k := range known {
			if typedVariety != "" && it.ID == lead.ID && list.VarietyKey(k) == list.VarietyKey(typedVariety) {
				continue
			}
			r := row(it, k)
			r.Sub = c.msg("lq.var.sub.known", "name", r.Name) + " · " + r.Sub
			rows = append(rows, r)
		}
	}
	if len(rows) > maxSuggestions {
		rows = rows[:maxSuggestions]
	}
	withVariety := map[string]bool{}
	for _, r := range rows {
		withVariety[r.ID] = withVariety[r.ID] || r.Variety != ""
	}
	for i := range rows {
		if rows[i].Variety == "" && withVariety[rows[i].ID] {
			rows[i].Sub = c.msg("lq.var.sub.none") + " · " + rows[i].Sub
		}
		rows[i].Idx = i
	}
	v.Rows = rows
	tap := "lq.sugg.tap.quick"
	if mode == "qty" {
		tap = "lq.sugg.tap.qty"
	}
	switch n := len(rows); {
	case n == 0:
		v.Intro = c.msg("lq.sugg.none")
	case n == 1:
		v.Intro = c.msg("lq.sugg.count.one", "q", v.Q, "tap", c.msg(tap))
	default:
		v.Intro = c.msg("lq.sugg.count.many", "q", v.Q, "n", fmt.Sprint(n), "tap", c.msg(tap))
	}
	v.Free, v.FreeName, v.FreeSub = true, c.msg("lq.sugg.free", "q", v.Q), c.msg("lq.sugg.free.sub")
	if len(hidden) > 0 {
		v.Hidden = c.hiddenLine(hidden)
	}
	for _, p := range c.s.freeText(c.st) {
		who := fmt.Sprintf(`<span class="who"><span class="marker" data-person="%d" data-shape="%s" aria-hidden="true"></span>%s</span>`, p.Chip.Slot, html.EscapeString(p.Chip.Shape), html.EscapeString(p.Chip.Name))
		line := htmlReplace(html.EscapeString(c.msg("lq.sugg.unchecked", "who", "\x00w", "text", "\x00t")), "\x00w", who, "\x00t", html.EscapeString(p.Text))
		v.Unchecked = append(v.Unchecked, template.HTML(line))
	}
	return v
}

// onListRank puts the varieties that stand on the list before the others.
func onListRank(es []list.Entry, articleID, variety string) int {
	if slices.ContainsFunc(es, func(e list.Entry) bool {
		return !e.Done && e.ArticleID == articleID && list.VarietyKey(e.Variety) == list.VarietyKey(variety)
	}) {
		return 0
	}
	return 1
}

func htmlReplace(s string, pairs ...string) string { return strings.NewReplacer(pairs...).Replace(s) }

// hiddenLine names the articles that are hidden because of an allergy, and
// who has it, with the allergens.
func (c *itemCtx) hiddenLine(hidden []articleItem) template.HTML {
	var names []string
	by := map[string]bool{}
	var who []string
	allergens := map[string]bool{}
	for i, it := range hidden {
		if i < 5 {
			names = append(names, html.EscapeString(it.Name(c.lang)))
		}
		for _, id := range c.st.hiding.HiddenFor(it.Article, c.st.persons) {
			if !by[id] {
				by[id] = true
				who = append(who, id)
			}
		}
		for _, ws := range c.st.warnings(it) {
			for _, a := range ws.Allergens {
				allergens[a] = true
			}
		}
	}
	var keys []string
	for a := range allergens {
		keys = append(keys, a)
	}
	key := "lq.sugg.hidden.one"
	if len(who) > 1 {
		key = "lq.sugg.hidden.many"
	}
	line := htmlMsgJoin(c.s.i18n.T(c.lang, "lq.sugg.hidden"), "{names}", c.s.joinWords(c.lang, names))
	line += " " + htmlMsgJoin(c.s.i18n.T(c.lang, key), "{allergens}", html.EscapeString(c.s.allergenWords(c.lang, keys)), "{who}", string(c.s.whoHTML(c.st, who)))
	return template.HTML(`<svg class="icon" width="18" height="18" viewBox="0 0 24 24" aria-hidden="true"><path d="M3 12s3.5-6 9-6 9 6 9 6-3.5 6-9 6-9-6-9-6z"/><circle cx="12" cy="12" r="2.6"/><path d="M4 4l16 16"/></svg><span><span class="tag exp">` + html.EscapeString(c.s.i18n.T(c.lang, "lib.tag.exp")) + `</span> ` + line + `</span>`)
}

// htmlMsgJoin fills the placeholders of a plain text with values that are markup already (escaped by the caller); the text is escaped here.
func htmlMsgJoin(text string, pairs ...string) string {
	return repl(html.EscapeString(text), pairs...)
}

// ---- sentences before saving

// addSentence says what adding the article with this quantity will do, or ""
// when there is nothing to say.
func (c *itemCtx) addSentence(es []list.Entry, art *articleItem, text string, in itemIn) template.HTML {
	if !in.Qty {
		return ""
	}
	q, _, err := c.quantity(art, in.Amount, in.Unit)
	if err != nil {
		return ""
	}
	artID := ""
	if art != nil {
		artID = art.ID
		text = art.Name(c.lang)
	}
	plan := list.PlanAdd(es, artID, in.Variety, text, q, c.show(art))
	switch plan.Action {
	case list.AddedTo:
		return c.htmlMsg("lq.merge.same", "now", c.boldQty(plan.Before), "add", c.boldQty(q), "total", c.boldQty(plan.Result))
	case list.AddedNew:
		if plan.After == "" {
			if other, ok := otherVariety(es, artID, in.Variety); ok {
				return c.otherVarietySentence(other, text, in.Variety)
			}
			return ""
		}
		var old list.Quantity
		for _, e := range es {
			if e.ID == plan.After {
				old = e.Qty
			}
		}
		if old.None() {
			return ""
		}
		a, b, measure := c.cannotConvert(old, q)
		key := "lq.merge.other.plain"
		if measure {
			key = "lq.merge.other.measure"
		}
		return c.htmlMsg(key, "now", c.boldQty(old), "a", html.EscapeString(a), "b", html.EscapeString(b), "text", html.EscapeString(text), "x", c.boldQty(old), "y", c.boldQty(plan.Result), "unit", html.EscapeString(c.s.i18n.T(c.lang, "unit."+old.Unit+".one")+c.suffix(old.Unit)))
	}
	return ""
}

// otherVariety finds an open entry of the same article with another variety.
func otherVariety(es []list.Entry, articleID, variety string) (list.Entry, bool) {
	if articleID == "" {
		return list.Entry{}, false
	}
	at := slices.IndexFunc(es, func(e list.Entry) bool {
		return !e.Done && e.ArticleID == articleID && list.VarietyKey(e.Variety) != list.VarietyKey(variety)
	})
	if at < 0 {
		return list.Entry{}, false
	}
	return es[at], true
}

// otherVarietySentence says that another variety of the article stands on the
// list and why the new one gets its own entry.
func (c *itemCtx) otherVarietySentence(other list.Entry, name, variety string) template.HTML {
	with := c.msg("lq.var.without")
	if other.Variety != "" {
		with = c.msg("lq.var.with", "variety", other.Variety)
	}
	v := variety
	if v == "" {
		v = c.msg("lq.var.no")
	}
	if other.Qty.None() {
		return c.htmlMsg("lq.merge.other.variety.noqty", "name", bold(name), "other", html.EscapeString(with), "variety", html.EscapeString(v))
	}
	return c.htmlMsg("lq.merge.other.variety", "name", bold(name), "other", html.EscapeString(with), "now", c.boldQty(other.Qty), "variety", html.EscapeString(v))
}

// editSentence says what saving the editor will do. in.Variety is the variety
// the entry will have.
func (c *itemCtx) editSentence(es []list.Entry, e list.Entry, art *articleItem, none bool, in itemIn) template.HTML {
	var to list.Quantity
	if !none {
		q, _, err := c.quantity(art, in.Amount, in.Unit)
		if err != nil {
			return ""
		}
		to = q
	}
	plan, err := list.PlanSet(es, e.ID, to, in.Variety, c.show(art))
	if err != nil {
		return ""
	}
	before := bold(c.s.i18n.T(c.lang, "lq.entry.qty"))
	if !plan.Before.None() {
		before = c.boldQty(plan.Before)
	}
	text := html.EscapeString(entryLabel(e.Text, plan.Variety))
	switch {
	case plan.Action == list.SetOnly && plan.Variety != list.NormalizeVariety(e.Variety) && plan.Before == plan.Result:
		return c.htmlMsg("lq.edit.variety", "text", bold(entryLabel(e.Text, plan.Variety)))
	case plan.Action == list.SetMerged && to.None():
		return c.htmlMsg("lq.edit.none_merge", "before", before, "other", c.boldQty(plan.Into.Qty))
	case plan.Action == list.SetMerged:
		if !plan.Before.None() && plan.Before.Kind() != to.Kind() {
			a, b, _ := c.cannotConvert(plan.Before, to)
			return c.htmlMsg("lq.edit.merge", "a", html.EscapeString(strings.ToUpper(a[:1])+a[1:]), "b", html.EscapeString(b), "before", c.boldQty(plan.Before), "after", c.boldQty(to), "text", text, "other", c.boldQty(plan.Into.Qty), "total", c.boldQty(plan.Result))
		}
		return c.htmlMsg("lq.edit.merge_same", "before", before, "after", c.boldQty(to), "text", text, "other", c.boldQty(plan.Into.Qty), "total", c.boldQty(plan.Result))
	case to.None() && plan.Before.None():
		return ""
	case to.None():
		return c.htmlMsg("lq.edit.none_after", "before", c.boldQty(plan.Before), "text", text)
	case plan.Before.None():
		return c.htmlMsg("lq.edit.none_before", "after", c.boldQty(plan.Result))
	case plan.Before.Kind() != to.Kind():
		a, b, _ := c.cannotConvert(plan.Before, to)
		return c.htmlMsg("lq.edit.other_kind", "before", c.boldQty(plan.Before), "after", c.boldQty(plan.Result), "a", html.EscapeString(strings.ToUpper(a[:1])+a[1:]), "b", html.EscapeString(b))
	}
	return c.htmlMsg("lq.edit.set", "before", c.boldQty(plan.Before), "after", c.boldQty(plan.Result))
}

// ---- what happened, for the line after saving

// outcome is what an add or a change did, kept for the redirect that follows.
type outcome struct {
	Kind    string // "add" or "edit"
	Action  string // list.AddAction or list.SetAction
	EntryID string
	Text    string
	Variety string
	NextTo  string // new entry: the entry of the same article with another variety it stands next to
	// Was is the entry as it was written before a change of its variety.
	Was       string
	ArticleID string
	Free      bool
	Added     list.Quantity
	Before    list.Quantity
	Total     list.Quantity
	Other     list.Quantity   // merged: what the other entry had
	Old       list.Quantity   // merged: what the entry that went away had
	Beside    []list.Quantity // new entry: the entries of the other kind it stands next to
	LibOffer  bool            // offer to add the text to the library
}

func (s *server) putOutcome(o outcome) string {
	raw, _ := json.Marshal(o)
	return s.flashTexts.put(string(raw), s.now())
}

func (s *server) takeOutcome(token string) (outcome, bool) {
	text, ok := s.flashTexts.take(token, s.now())
	if !ok {
		return outcome{}, false
	}
	var o outcome
	return o, json.Unmarshal([]byte(text), &o) == nil
}

// peekOutcome is takeOutcome for a display, whose view is asked for again and
// again while the line stays.
func (s *server) peekOutcome(token string) (outcome, bool) {
	text, ok := s.flashTexts.peek(token, s.now())
	if !ok {
		return outcome{}, false
	}
	var o outcome
	return o, json.Unmarshal([]byte(text), &o) == nil
}

// resultView is a success line with what goes with it.
type resultView struct {
	Line  template.HTML
	Warns []template.HTML
	Act   *resultAct
}

type resultAct struct{ Href, Label string }

// result builds the success line of an outcome. display is true on a display,
// where parents manage the library: it only says the sentence.
func (c *itemCtx) result(o outcome, display bool) resultView {
	label := entryLabel(o.Text, o.Variety)
	text := html.EscapeString(label)
	var rv resultView
	switch o.Kind + ":" + o.Action {
	case "add:" + string(list.AddedNew):
		if o.NextTo != "" && len(o.Beside) == 0 {
			rv.Line = c.htmlMsg("lq.res.new.variety", "text", text, "other", html.EscapeString(o.NextTo))
		} else if o.Total.None() {
			rv.Line = c.htmlMsg("lq.res.new", "text", text)
		} else {
			rv.Line = c.htmlMsg("lq.res.new.qty", "qty", c.boldQty(o.Total), "text", text)
			if len(o.Beside) > 0 {
				a, b := c.kindNoun(o.Beside[0].Kind()), c.kindNoun(o.Total.Kind())
				var others []string
				for _, q := range o.Beside {
					others = append(others, c.boldQty(q))
				}
				rv.Line += " " + c.htmlMsg("lq.res.new.beside", "other", c.s.joinWords(c.lang, others), "a", html.EscapeString(a), "b", html.EscapeString(b))
			}
		}
	case "add:" + string(list.AddedTo):
		rv.Line = c.htmlMsg("lq.res.added_to", "add", c.boldQty(o.Added), "text", text, "before", c.boldQty(o.Before), "total", c.boldQty(o.Total))
	case "add:" + string(list.Filled):
		rv.Line = c.htmlMsg("lq.res.filled", "text", text, "qty", c.boldQty(o.Total))
	case "add:" + string(list.Exists):
		rv.Line = c.htmlMsg("lq.res.exists", "text", text)
	case "edit:" + string(list.SetOnly):
		switch {
		case o.Was != "":
			rv.Line = c.htmlMsg("lq.res.set.variety", "text", bold(label), "was", html.EscapeString(o.Was))
		case o.Total.None():
			rv.Line = c.htmlMsg("lq.res.set.none_after", "text", bold(label), "before", html.EscapeString(c.qtyWords(o.Before)))
		case o.Before.None():
			rv.Line = c.htmlMsg("lq.res.set.none_before", "text", bold(label), "after", c.boldQty(o.Total))
		default:
			rv.Line = c.htmlMsg("lq.res.set", "text", bold(label), "after", c.boldQty(o.Total), "before", html.EscapeString(c.qtyWords(o.Before)))
		}
	case "edit:" + string(list.SetMerged):
		if o.Added.None() {
			rv.Line = c.htmlMsg("lq.res.merged.none", "text", bold(label), "qty", c.boldQty(o.Total), "old", html.EscapeString(c.qtyWords(o.Old)))
		} else {
			rv.Line = c.htmlMsg("lq.res.merged", "text", bold(label), "added", html.EscapeString(c.qtyWords(o.Added)), "other", html.EscapeString(c.qtyWords(o.Other)), "total", c.boldQty(o.Total), "old", html.EscapeString(c.qtyWords(o.Old)))
		}
	}
	if o.Kind == "add" {
		if o.Free {
			key := "lq.res.free"
			if display {
				key = "lq.res.free.display"
			}
			rv.Line += " " + c.htmlMsg(key)
			if o.LibOffer && !display {
				rv.Act = &resultAct{Href: "/settings/lists/library/new?" + url.Values{"name": {o.Text}, "list": {c.listID}, "entry": {o.EntryID}}.Encode(), Label: c.msg("lq.res.library")}
			}
		} else if it, ok := c.article(o.ArticleID); ok && o.Action != string(list.Exists) {
			rv.Warns = c.s.warnLines(c.st, c.st.warnings(it))
		}
	}
	return rv
}

// render runs a sub-template to markup.
func (s *server) fragment(lang, name string, data any) template.HTML {
	var buf bytes.Buffer
	if err := s.execute(&buf, name, lang, data); err != nil {
		s.logger.Error("render "+name, "err", err)
		return ""
	}
	return template.HTML(buf.String())
}

// adderView is the add card.
type adderView struct {
	Mode, Text, ArticleID string
	TextError             string
	Autofocus, Secondary  bool
	Picked                *pickedView
	Variety               *varietyView // the Sorte row, only while an article is chosen
	Qty                   qtyView
	Sugg                  template.HTML
	Result                *resultView
}

// editorView is the quantity editor of one entry.
type editorView struct {
	Anchor, Action, Cancel string
	Mode                   string       // "qty" or "none"
	Variety                *varietyView // the Sorte row, only for an entry with an article
	Qty                    qtyView
	Merge                  template.HTML
	Hide, Display          bool
}

// ---- the entries of a list with suggestions

// articleOf is the article an entry was made from, if the library still has it.
func (c *itemCtx) articleOf(e list.Entry) *articleItem {
	if it, ok := c.article(e.ArticleID); ok {
		return &it
	}
	return nil
}

// editPost is what the editor of a quantity posted, kept to show it again after an error.
// editPost is what the editor of a quantity posted. Converted is set by the
// script of the page, which converts the number itself when a unit of the same
// kind is tapped; without it the number is still the one of the old unit.
type editPost struct {
	EntryID, Mode, Amount, Unit string
	Converted                   bool
	// HasVariety says the editor posted the Sorte row; VarietyPick and Variety are its values.
	HasVariety           bool
	VarietyPick, Variety string
}

// editPostFrom reads what the editor of an entry posted.
func editPostFrom(r *http.Request, id string) *editPost {
	return &editPost{EntryID: id, Mode: r.PostFormValue("qmode"), Amount: r.PostFormValue("amount"), Unit: r.PostFormValue("qunit"), Converted: r.PostFormValue("converted") == "1",
		HasVariety: r.PostForm.Has("qvariety_pick"), VarietyPick: r.PostFormValue("qvariety_pick"), Variety: r.PostFormValue("qvariety")}
}

func editPostOf(form map[string]any) *editPost {
	v, _ := form["EditPost"].(*editPost)
	return v
}

// editingOpen reports whether one of the entries has its editor open.
func editingOpen(open []entryView) bool {
	return slices.ContainsFunc(open, func(e entryView) bool { return e.Editing })
}

// hideQuery is the part of an address that keeps ticked-off entries hidden.
func hideQuery(r *http.Request) string {
	if hideTicked(r) {
		return "&ticked=hide"
	}
	return ""
}

type entryOpts struct {
	Base             string
	Hide, Parent     bool // Parent: the person may change quantities (on a display everyone may)
	Display          bool
	Added, Edit      string // the entry to frame, the entry whose editor is open
	Out              *outcome
	Post             *editPost
	ErrField, ErrKey string
	HideQ            string
}

func (c *itemCtx) entryViews(es []list.Entry, o entryOpts) (open, ticked []entryView) {
	editID := o.Edit
	if o.Post != nil {
		editID = o.Post.EntryID
	}
	if o.Out != nil && o.Out.EntryID != "" && o.Added == "" {
		o.Added = o.Out.EntryID
	}
	for _, e := range es {
		ev := entryView{ID: e.ID, Text: e.Text, Variety: e.Variety, ArticleID: e.ArticleID, Done: e.Done, Added: e.ID == o.Added, Qty: c.qtyWords(e.Qty)}
		if e.Done {
			ticked = append(ticked, ev)
			continue
		}
		ev.Suggest = o.Parent || o.Display
		if ev.Suggest {
			free := ""
			if e.ArticleID == "" {
				free = ".free"
			}
			ev.Label = c.msg("lq.entry.add"+free, "text", ev.Plain())
			if !e.Qty.None() {
				ev.Label = c.msg("lq.entry.change"+free, "text", ev.Plain(), "qty", ev.Qty)
			}
			ev.Href = "?qty=" + url.QueryEscape(e.ID) + o.HideQ + "#e-" + e.ID
		}
		if it := c.articleOf(e); it != nil {
			ev.Warns = c.s.warnLines(c.st, c.st.warnings(*it))
		}
		if o.Out != nil && o.Out.Kind == "edit" && o.Out.EntryID == e.ID {
			rv := c.result(*o.Out, o.Display)
			ev.Result = &rv
		}
		if ev.Suggest && e.ID == editID {
			ev.Editing, ev.Added = true, false
			ev.Editor = c.editor(es, e, o)
		}
		open = append(open, ev)
	}
	return open, ticked
}

// editor builds the editor of one entry's quantity: its quantity now, or what
// was posted when the last save was refused.
func (c *itemCtx) editor(es []list.Entry, e list.Entry, o entryOpts) *editorView {
	art := c.articleOf(e)
	mode, amount, chosen := "qty", "1", c.preselected(art)
	if !e.Qty.None() {
		amount, chosen = amountText(e.Qty, c.lang), e.Qty.Unit
	}
	var errText, varErr string
	pick, vtext, variety := e.Variety, "", e.Variety
	if o.Post != nil && o.Post.EntryID == e.ID {
		mode, amount, chosen = o.Post.Mode, o.Post.Amount, o.Post.Unit
		if o.ErrField == "edit-"+string(fieldAmount) || o.ErrField == "edit-"+string(fieldUnit) {
			errText = c.s.i18n.T(c.lang, o.ErrKey)
		}
		if o.Post.HasVariety {
			pick, vtext = o.Post.VarietyPick, o.Post.Variety
			variety, _ = varietyOf(pick, vtext)
		}
		if o.ErrField == "edit-"+string(fieldVariety) {
			varErr = o.ErrKey
		}
	}
	q := c.qtyFields(art, amount, chosen, o.Display)
	q.AmountID, q.UnitName, q.WithMerge, q.Error = "qa-"+e.ID, "qunit", false, errText
	ed := &editorView{Anchor: e.ID, Action: o.Base + "/entries/" + e.ID + "/quantity", Mode: mode, Qty: q, Hide: o.Hide, Display: o.Display,
		Cancel: o.Base + "#e-" + e.ID}
	if o.Hide {
		ed.Cancel = o.Base + "?ticked=hide#e-" + e.ID
	}
	if art != nil {
		ed.Variety = c.varietyFields(*art, "q", pick, vtext, varErr)
	} else {
		variety = ""
	}
	ed.Merge = c.editSentence(es, e, art, mode == "none", itemIn{Amount: amount, Unit: chosen, Variety: variety})
	return ed
}

type adderOpts struct {
	Base            string
	Form            map[string]any
	Out             *outcome
	ErrField        string
	ErrKey          string // the text of a variety error
	Editing, Parent bool
	Display         bool
}

// adder builds the add card from what the page was asked for.
func (c *itemCtx) adder(es []list.Entry, o adderOpts) adderView {
	str := func(k string) string { v, _ := o.Form[k].(string); return v }
	a := adderView{Mode: str("Mode"), Text: str("Text"), ArticleID: str("Article"), Secondary: o.Editing}
	vpick, vtext := str("VarietyPick"), str("Variety")
	variety, _ := varietyOf(vpick, vtext)
	if a.Mode != "qty" {
		a.Mode = "quick"
	}
	amount, chosen := str("Amount"), str("Unit")
	if amount == "" && o.ErrField != string(fieldAmount) {
		amount = "1"
	}
	var art *articleItem
	if a.ArticleID != "" {
		if it, ok := c.article(a.ArticleID); ok {
			art = &it
			a.Picked = c.picked(it, variety)
			varErr := ""
			if o.ErrField == string(fieldVariety) {
				varErr = o.ErrKey
			}
			a.Variety = c.varietyFields(it, "", vpick, vtext, varErr)
		} else {
			a.ArticleID = ""
		}
	}
	a.Qty = c.qtyFields(art, amount, chosen, o.Display)
	switch o.ErrField {
	case string(fieldAmount):
		a.Qty.Error = c.s.i18n.T(c.lang, itemErrorKey(fieldAmount, o.Display))
	case string(fieldUnit):
		a.Qty.Error = c.s.i18n.T(c.lang, itemErrorKey(fieldUnit, o.Display))
	case string(fieldText), string(fieldItem):
		a.TextError = c.s.i18n.T(c.lang, itemErrorKey(itemField(o.ErrField), o.Display))
	}
	if a.Mode == "qty" {
		name := a.Text
		a.Qty.Merge = c.addSentence(es, art, name, itemIn{Qty: true, Amount: amount, Unit: a.Qty.Unit, Variety: variety})
	}
	if o.Out != nil && o.Out.Kind == "add" {
		rv := c.result(*o.Out, o.Display)
		a.Result = &rv
	}
	a.Autofocus = a.Result != nil || a.TextError != "" || o.Display && o.ErrField != ""
	return a
}

// addOutcome describes what adding did, for the line that follows.
func (c *itemCtx) addOutcome(listID string, rs resolved, res store.AddResult) (outcome, error) {
	o := outcome{Kind: "add", Action: string(res.Plan.Action), EntryID: res.Entry.ID, Text: res.Entry.Text, Variety: res.Entry.Variety, ArticleID: rs.Item.ArticleID,
		Free: rs.Article == nil, Added: rs.Item.Qty, Before: res.Plan.Before, Total: res.Entry.Qty}
	if res.Plan.Action == list.AddedNew {
		es, err := c.s.db.Entries(c.p.household.ID, listID)
		if err != nil {
			return o, err
		}
		for _, e := range es {
			if e.ID != res.Entry.ID && e.Same(rs.Item.ArticleID, rs.Item.Variety, rs.Item.Text) && !e.Qty.None() {
				o.Beside = append(o.Beside, e.Qty)
			}
		}
		if other, ok := otherVariety(es, rs.Item.ArticleID, rs.Item.Variety); ok && other.ID != res.Entry.ID {
			o.NextTo = entryLabel(other.Text, other.Variety)
		}
	}
	return o, nil
}

// changeQuantity applies what the editor posted.
func (c *itemCtx) changeQuantity(listID, entryID string, post *editPost) (store.SetResult, outcome, error) {
	es, err := c.s.db.Entries(c.p.household.ID, listID)
	if err != nil {
		return store.SetResult{}, outcome{}, err
	}
	at := slices.IndexFunc(es, func(e list.Entry) bool { return e.ID == entryID })
	if at < 0 || es[at].Done {
		return store.SetResult{}, outcome{}, list.ErrNotOpen
	}
	art := c.articleOf(es[at])
	var variety *string
	if post.HasVariety && art != nil {
		v, isNew := varietyOf(post.VarietyPick, post.Variety)
		if err := list.ValidateVariety(v); err != nil && (isNew || v != "") {
			return store.SetResult{}, outcome{}, &itemError{fieldVariety, err.(*list.InvalidError).Msg}
		}
		variety = &v
	}
	var to list.Quantity
	chosen := ""
	if post.Mode != "none" {
		if to, chosen, err = c.quantity(art, post.Amount, post.Unit); err != nil {
			return store.SetResult{}, outcome{}, err
		}
		// A tap on a unit of the same kind converts the number and keeps the amount
		// (500 g to 0,5 kg; 5 pieces to 0,4167 dozen): a number that is only that
		// conversion, rounded as the page writes it, is the amount itself, not its rounding.
		// Without JavaScript the number is not converted by the page: the unit changed
		// and the number is still the old one as it was shown, so it is converted here.
		// A post of the script says so (Converted); then the number typed is the amount.
		cur := es[at].Qty
		if keepsAmount(cur, to, chosen, false) || (!post.Converted && !cur.None() && cur.Kind() == to.Kind() && chosen != cur.Unit && to.Amount() == unit.Round(cur.Amount(), cur.Unit)) {
			to.Base = cur.Base
		}
	}
	res, err := c.s.db.ChangeEntry(c.p.household.ID, listID, entryID, to, chosen, variety, c.show(art))
	if err != nil {
		return res, outcome{}, err
	}
	pl := res.Plan
	o := outcome{Kind: "edit", Action: string(pl.Action), EntryID: pl.Entry.ID, Text: pl.Entry.Text, Variety: pl.Variety, ArticleID: pl.Entry.ArticleID, Added: to, Before: pl.Before, Total: pl.Result}
	if pl.Action == list.SetMerged {
		o.EntryID, o.Other, o.Old = pl.Into.ID, pl.Into.Qty, pl.Before
	} else if list.VarietyKey(pl.Variety) != list.VarietyKey(pl.Entry.Variety) && pl.Before == pl.Result {
		o.Was = entryLabel(pl.Entry.Text, pl.Entry.Variety)
	}
	return res, o, nil
}

// writeFragment answers the script of the add card with markup alone.
func (s *server) writeFragment(w http.ResponseWriter, r *http.Request, p principal, l list.List, kind string) {
	s.writeFragmentLang(w, r, p, l, kind, s.langOf(r))
}

func (s *server) writeFragmentLang(w http.ResponseWriter, r *http.Request, p principal, l list.List, kind, lang string) {
	c, err := s.itemContext(p, lang)
	if err != nil {
		s.fail(w, "library state", err)
		return
	}
	c.listID = l.ID
	es, err := s.db.Entries(p.household.ID, l.ID)
	if err != nil {
		s.fail(w, "entries", err)
		return
	}
	q := r.URL.Query()
	display := strings.HasPrefix(r.URL.Path, "/display/")
	var out template.HTML
	switch kind {
	case "suggest":
		out = s.fragment(lang, "sugg", c.suggest(es, q.Get("q"), q.Get("mode")))
	default:
		amount := q.Get("amount")
		if amount == "" {
			amount = "1"
		}
		var art *articleItem
		if it, ok := c.article(q.Get("article")); ok {
			art = &it
		}
		// The Sorte row as it stands: the chip chosen and the text typed; "variety" alone is a variety to put in.
		vpick, vtext := q.Get("variety_pick"), q.Get("variety")
		if !q.Has("variety_pick") && vtext != "" {
			vpick = "new"
		}
		variety, _ := varietyOf(vpick, vtext)
		if id := q.Get("entry"); id != "" { // the editor of an entry: its sentence only
			at := slices.IndexFunc(es, func(e list.Entry) bool { return e.ID == id })
			if at >= 0 {
				e := es[at]
				chosen := q.Get("unit")
				if !q.Has("variety_pick") && !q.Has("variety") {
					variety = e.Variety
				}
				if c.articleOf(e) == nil {
					variety = ""
				}
				out = c.editSentence(es, e, c.articleOf(e), q.Get("none") == "1", itemIn{Amount: amount, Unit: chosen, Variety: variety})
			}
			break
		}
		text := q.Get("text")
		if art == nil {
			variety = ""
		}
		qv := c.qtyFields(art, amount, q.Get("unit"), display)
		qv.Merge = c.addSentence(es, art, text, itemIn{Qty: true, Amount: amount, Unit: qv.Unit, Variety: variety})
		if q.Get("part") == "merge" {
			out = qv.Merge
			break
		}
		if art != nil {
			out = s.fragment(lang, "picked", c.picked(*art, variety))
			out += s.fragment(lang, "variety", c.varietyFields(*art, "", vpick, vtext, ""))
		}
		out += s.fragment(lang, "qtyFields", qv)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(out))
}
