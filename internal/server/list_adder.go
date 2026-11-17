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
	ID, Name, Symbol, Picture, Sub string
}

func (c *itemCtx) picked(it articleItem) *pickedView {
	key := "lq.picked.lib"
	if it.own != nil {
		key = "lq.picked.own"
	}
	pv := &pickedView{ID: it.ID, Name: it.Name(c.lang), Symbol: symbolOf(it), Sub: c.msg(key, "dept", c.s.i18n.T(c.lang, "dept."+it.Dept))}
	if it.own != nil && it.own.HasPicture {
		pv.Picture = pictureURL(*it.own)
	}
	return pv
}

// suggRow is one suggested article.
type suggRow struct {
	ID, Name, Sub, Symbol, Picture string
	OnList                         string
	Warns                          []template.HTML
	Idx                            int
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
func (c *itemCtx) onList(es []list.Entry, articleID, text string) string {
	var parts []string
	has := false
	for _, e := range es {
		if !e.Same(articleID, text) {
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
	for i, it := range shown {
		row := suggRow{ID: it.ID, Idx: i, Name: it.Name(c.lang), Symbol: symbolOf(it), Sub: c.s.i18n.T(c.lang, "dept."+it.Dept)}
		if it.own != nil {
			row.Sub = c.s.i18n.T(c.lang, "lib.from.own") + " · " + row.Sub
			row.Sub = strings.ToUpper(row.Sub[:1]) + row.Sub[1:]
			if it.own.HasPicture {
				row.Picture = pictureURL(*it.own)
			}
		}
		row.OnList = c.onList(es, it.ID, row.Name)
		row.Warns = c.s.warnLines(c.st, c.st.warnings(it))
		v.Rows = append(v.Rows, row)
	}
	tap := "lq.sugg.tap.quick"
	if mode == "qty" {
		tap = "lq.sugg.tap.qty"
	}
	switch n := len(shown); {
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
	plan := list.PlanAdd(es, artID, text, q, c.show(art))
	switch plan.Action {
	case list.AddedTo:
		return c.htmlMsg("lq.merge.same", "now", c.boldQty(plan.Before), "add", c.boldQty(q), "total", c.boldQty(plan.Result))
	case list.AddedNew:
		if plan.After == "" {
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

// editSentence says what saving the editor will do.
func (c *itemCtx) editSentence(es []list.Entry, e list.Entry, art *articleItem, none bool, in itemIn) template.HTML {
	var to list.Quantity
	if !none {
		q, _, err := c.quantity(art, in.Amount, in.Unit)
		if err != nil {
			return ""
		}
		to = q
	}
	plan, err := list.PlanSet(es, e.ID, to, c.show(art))
	if err != nil {
		return ""
	}
	before := bold(c.s.i18n.T(c.lang, "lq.entry.qty"))
	if !plan.Before.None() {
		before = c.boldQty(plan.Before)
	}
	text := html.EscapeString(e.Text)
	switch {
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
	Kind      string // "add" or "edit"
	Action    string // list.AddAction or list.SetAction
	EntryID   string
	Text      string
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
	text := html.EscapeString(o.Text)
	var rv resultView
	switch o.Kind + ":" + o.Action {
	case "add:" + string(list.AddedNew):
		if o.Total.None() {
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
		case o.Total.None():
			rv.Line = c.htmlMsg("lq.res.set.none_after", "text", bold(o.Text), "before", html.EscapeString(c.qtyWords(o.Before)))
		case o.Before.None():
			rv.Line = c.htmlMsg("lq.res.set.none_before", "text", bold(o.Text), "after", c.boldQty(o.Total))
		default:
			rv.Line = c.htmlMsg("lq.res.set", "text", bold(o.Text), "after", c.boldQty(o.Total), "before", html.EscapeString(c.qtyWords(o.Before)))
		}
	case "edit:" + string(list.SetMerged):
		if o.Added.None() {
			rv.Line = c.htmlMsg("lq.res.merged.none", "text", bold(o.Text), "qty", c.boldQty(o.Total), "old", html.EscapeString(c.qtyWords(o.Old)))
		} else {
			rv.Line = c.htmlMsg("lq.res.merged", "text", bold(o.Text), "added", html.EscapeString(c.qtyWords(o.Added)), "other", html.EscapeString(c.qtyWords(o.Other)), "total", c.boldQty(o.Total), "old", html.EscapeString(c.qtyWords(o.Old)))
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
	Qty                   qtyView
	Sugg                  template.HTML
	Result                *resultView
}

// editorView is the quantity editor of one entry.
type editorView struct {
	Anchor, Action, Cancel string
	Mode                   string // "qty" or "none"
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
type editPost struct{ EntryID, Mode, Amount, Unit string }

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
		ev := entryView{ID: e.ID, Text: e.Text, Done: e.Done, Added: e.ID == o.Added, Qty: c.qtyWords(e.Qty)}
		if e.Done {
			ticked = append(ticked, ev)
			continue
		}
		ev.Suggest = o.Parent || o.Display
		if ev.Suggest {
			ev.Label = c.msg("lq.entry.add", "text", e.Text)
			if !e.Qty.None() {
				ev.Label = c.msg("lq.entry.change", "text", e.Text, "qty", ev.Qty)
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
	var errText string
	if o.Post != nil && o.Post.EntryID == e.ID {
		mode, amount, chosen = o.Post.Mode, o.Post.Amount, o.Post.Unit
		if o.ErrField == "edit-"+string(fieldAmount) || o.ErrField == "edit-"+string(fieldUnit) {
			errText = c.s.i18n.T(c.lang, o.ErrKey)
		}
	}
	q := c.qtyFields(art, amount, chosen, o.Display)
	q.AmountID, q.UnitName, q.WithMerge, q.Error = "qa-"+e.ID, "qunit", false, errText
	ed := &editorView{Anchor: e.ID, Action: o.Base + "/entries/" + e.ID + "/quantity", Mode: mode, Qty: q, Hide: o.Hide, Display: o.Display,
		Cancel: o.Base + "#e-" + e.ID}
	if o.Hide {
		ed.Cancel = o.Base + "?ticked=hide#e-" + e.ID
	}
	ed.Merge = c.editSentence(es, e, art, mode == "none", itemIn{Amount: amount, Unit: chosen})
	return ed
}

type adderOpts struct {
	Base            string
	Form            map[string]any
	Out             *outcome
	ErrField        string
	Editing, Parent bool
	Display         bool
}

// adder builds the add card from what the page was asked for.
func (c *itemCtx) adder(es []list.Entry, o adderOpts) adderView {
	str := func(k string) string { v, _ := o.Form[k].(string); return v }
	a := adderView{Mode: str("Mode"), Text: str("Text"), ArticleID: str("Article"), Secondary: o.Editing}
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
			a.Picked = c.picked(it)
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
		a.Qty.Merge = c.addSentence(es, art, name, itemIn{Qty: true, Amount: amount, Unit: a.Qty.Unit})
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
	o := outcome{Kind: "add", Action: string(res.Plan.Action), EntryID: res.Entry.ID, Text: res.Entry.Text, ArticleID: rs.Item.ArticleID,
		Free: rs.Article == nil, Added: rs.Item.Qty, Before: res.Plan.Before, Total: res.Entry.Qty}
	if res.Plan.Action == list.AddedNew {
		es, err := c.s.db.Entries(c.p.household.ID, listID)
		if err != nil {
			return o, err
		}
		for _, e := range es {
			if e.ID != res.Entry.ID && e.Same(rs.Item.ArticleID, rs.Item.Text) && !e.Qty.None() {
				o.Beside = append(o.Beside, e.Qty)
			}
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
		cur := es[at].Qty
		if keepsAmount(cur, to, chosen) || (!cur.None() && cur.Kind() == to.Kind() && chosen != cur.Unit && to.Amount() == unit.Round(cur.Amount(), cur.Unit)) {
			to.Base = cur.Base
		}
	}
	res, err := c.s.db.SetQuantity(c.p.household.ID, listID, entryID, to, chosen, c.show(art))
	if err != nil {
		return res, outcome{}, err
	}
	pl := res.Plan
	o := outcome{Kind: "edit", Action: string(pl.Action), EntryID: pl.Entry.ID, Text: pl.Entry.Text, ArticleID: pl.Entry.ArticleID, Added: to, Before: pl.Before, Total: pl.Result}
	if pl.Action == list.SetMerged {
		o.EntryID, o.Other, o.Old = pl.Into.ID, pl.Into.Qty, pl.Before
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
		if id := q.Get("entry"); id != "" { // the editor of an entry: its sentence only
			at := slices.IndexFunc(es, func(e list.Entry) bool { return e.ID == id })
			if at >= 0 {
				e := es[at]
				chosen := q.Get("unit")
				out = c.editSentence(es, e, c.articleOf(e), q.Get("none") == "1", itemIn{Amount: amount, Unit: chosen})
			}
			break
		}
		text := q.Get("text")
		qv := c.qtyFields(art, amount, q.Get("unit"), display)
		qv.Merge = c.addSentence(es, art, text, itemIn{Qty: true, Amount: amount, Unit: qv.Unit})
		if q.Get("part") == "merge" {
			out = qv.Merge
			break
		}
		if art != nil {
			out = s.fragment(lang, "picked", c.picked(*art))
		}
		out += s.fragment(lang, "qtyFields", qv)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(out))
}
