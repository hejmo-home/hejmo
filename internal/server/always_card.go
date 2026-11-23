package server

import (
	"html"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/always"
	"github.com/hejmo-home/hejmo/internal/list"
)

// The card "Immer auf der Liste" on the page of an article (ticket 168,
// preview-168 screens 3 to 8): a switch and, when it is on, list, day, rhythm,
// quantity and the sentence that says in advance what happens.

// autoForm is what the card holds: the stored rule, or what was posted.
type autoForm struct {
	Present bool // the card was in the posted form
	On      bool
	List    string
	Weekday int // 0 for none
	Every   string
	Start   string // a Monday, YYYY-MM-DD
	QMode   string // "none" or "qty"
	Amount  string
	Unit    string
}

// autoPost is a posted card that was refused: what was chosen and what is wrong.
type autoPost struct {
	Form autoForm
	Errs map[string]string
}

type autoListChip struct {
	ID, Name    string
	Slot        int
	Shape       string
	Checked     bool
	Placeholder bool
}

type autoDayChip struct {
	Value   int
	Name    string
	Checked bool
}

type planLine struct {
	Class string
	HTML  template.HTML
}

// planView is the sentence block under the card: what will happen.
type planView struct {
	Stopped bool
	Head    string
	Lines   []planLine
}

type autoCardView struct {
	On                    bool
	OnText, OffText, Aria string
	Lists                 []autoListChip
	NoLists               bool
	Days                  []autoDayChip
	Every                 string
	Starts                []startOption
	QMode                 string
	Qty                   qtyView
	Plan                  planView
	Errs                  map[string]string
	Name, PlanURL         string
}

// readAuto reads the card from a posted form.
func readAuto(r *http.Request) autoForm {
	v := r.PostFormValue
	f := autoForm{Present: v("auto_card") != "", On: v("auto") != "", List: v("auto_list"), Every: strings.TrimSpace(v("auto_every")), Start: v("auto_start"), QMode: v("qmode"), Amount: v("amount"), Unit: v("unit")}
	f.Weekday, _ = strconv.Atoi(v("auto_weekday"))
	if f.QMode != "qty" {
		f.QMode = "none"
	}
	return f
}

// formOfRule is the card of a stored rule (nil: of an article without one).
func (a *alwaysCtx) formOfRule(art articleItem, r *always.Rule) autoForm {
	f := autoForm{Present: true, Every: "1", Start: weekOf(a.today).Format(always.DateLayout), QMode: "none", List: a.firstList()}
	amount, unit := "1", a.c.preselected(&art)
	f.Amount, f.Unit = amount, unit
	if r == nil {
		return f
	}
	f.On, f.List, f.Weekday, f.Every, f.Start = r.Enabled, r.ListID, r.Weekday, strconv.Itoa(r.Every), r.Start
	if !r.Qty.None() {
		f.QMode, f.Amount, f.Unit = "qty", amountText(r.Qty, a.lang), r.Qty.Unit
	}
	return f
}

// validate turns a posted card into a rule, or says what is wrong at which field.
func (a *alwaysCtx) validate(art articleItem, f autoForm, stored *always.Rule) (always.Rule, map[string]string) {
	errs := map[string]string{}
	r := always.Rule{ArticleID: art.ID, Enabled: true, Lang: a.lang, Weekday: f.Weekday}
	name := art.Name(a.lang)
	if l, ok := a.listByID(f.List); ok {
		r.ListID, r.ListName = l.ID, l.Name
	} else if stored != nil && stored.ListID == "" && stored.ListName != "" {
		errs["list"] = a.tr("always.err.list.gone", "list", stored.ListName, "name", name)
	} else {
		errs["list"] = a.tr("always.err.list")
	}
	if f.Weekday < 1 || f.Weekday > 7 {
		errs["weekday"] = a.tr("always.err.weekday", "name", name)
	}
	n, err := strconv.Atoi(f.Every)
	if err != nil || n < 1 || n > always.MaxEvery {
		errs["every"] = a.tr("always.err.every")
	}
	r.Every = n
	if d, err := time.Parse(always.DateLayout, f.Start); err != nil {
		errs["start"] = a.tr("always.err.start")
	} else {
		r.Start = weekOf(d).Format(always.DateLayout)
	}
	if f.QMode == "qty" {
		q, _, err := a.c.quantity(&art, f.Amount, f.Unit)
		if ie, ok := asItemError(err); ok {
			errs[string(ie.Field)] = a.s.i18n.T(a.lang, itemErrorKey(ie.Field, false))
		} else {
			r.Qty = q
		}
	}
	return r, errs
}

// card builds the card from a form and the errors at its fields.
func (a *alwaysCtx) card(art articleItem, f autoForm, errs map[string]string, stored *always.Rule) autoCardView {
	name := art.Name(a.lang)
	v := autoCardView{On: f.On, Every: f.Every, QMode: f.QMode, Errs: errs, Name: name, NoLists: len(a.lists) == 0, PlanURL: libraryPath + "/plan/" + art.ID}
	v.OnText = a.tr("always.card.on", "name", name)
	if stored != nil && stored.Paused() && stored.Enabled {
		v.OnText = a.tr("always.card.paused")
	}
	v.OffText = a.tr("always.card.off", "name", name)
	v.Aria = a.tr("always.card.aria", "name", name)
	for _, l := range a.lists {
		v.Lists = append(v.Lists, autoListChip{ID: l.ID, Name: l.Name, Slot: list.Slot(l.Color), Shape: l.Symbol, Checked: l.ID == f.List})
	}
	for d := 1; d <= 7; d++ {
		v.Days = append(v.Days, autoDayChip{Value: d, Name: a.weekday(d), Checked: d == f.Weekday})
	}
	this := weekOf(a.today)
	v.Starts = a.s.startOptions(a.lang, a.p.household.DateFormat, this, f.Start)
	for i := range v.Starts {
		if v.Starts[i].Value == this.Format(always.DateLayout) {
			v.Starts[i].Label += a.tr("always.start.this")
		}
	}
	q := a.c.qtyFields(&art, f.Amount, f.Unit, false)
	q.WithMerge = false
	if m := errs["amount"]; m != "" {
		q.Error = m
	} else if m := errs["unit"]; m != "" {
		q.Error = m
	}
	v.Qty = q
	v.Plan = a.plan(art, f, stored)
	return v
}

// plan says in advance what will happen with the card as it is now.
func (a *alwaysCtx) plan(art articleItem, f autoForm, stored *always.Rule) planView {
	name := art.Name(a.lang)
	if _, ok := a.listByID(f.List); !ok && stored != nil && stored.Paused() {
		hint := a.tr("always.plan.paused.hint")
		if stored.Weekday >= 1 && stored.Weekday <= 7 {
			hint = a.tr("always.plan.paused.hint.day", "day", a.weekday(stored.Weekday))
		}
		return planView{Stopped: true, Head: a.tr("always.plan.paused", "name", name), Lines: []planLine{{"hint", template.HTML(html.EscapeString(hint))}}}
	}
	r, errs := a.validate(art, f, stored)
	if len(errs) > 0 {
		return planView{Stopped: true, Head: a.tr("always.plan.notyet"), Lines: []planLine{{"hint", template.HTML(html.EscapeString(a.tr("always.plan.notyet.hint", "name", name)))}}}
	}
	r.Since = a.todayStr()
	head := a.tr("always.plan.head", "rhythm", a.rhythm(r.Every, r.Weekday), "list", r.ListName)
	if !r.Qty.None() {
		head = a.tr("always.plan.head.qty", "rhythm", a.rhythm(r.Every, r.Weekday), "list", r.ListName, "qty", a.qty(r.Qty))
	}
	pv := planView{Head: head}
	if first, ok := r.Next(a.today.AddDate(0, 0, 1)); ok {
		var then []string
		from := first.AddDate(0, 0, 1)
		for len(then) < 2 {
			d, ok := r.Next(from)
			if !ok {
				break
			}
			then = append(then, a.date(d))
			from = d.AddDate(0, 0, 1)
			if r.Every == 1 {
				break
			}
		}
		when := ""
		switch len(then) {
		case 0:
			when = a.tr("always.plan.when.only", "first", a.dayName(first), "at", a.at())
		case 1:
			when = a.tr("always.plan.when.one", "first", a.dayName(first), "at", a.at(), "d1", then[0])
		default:
			when = a.tr("always.plan.when.two", "first", a.dayName(first), "at", a.at(), "d1", then[0], "d2", then[1])
		}
		probe := r
		probe.Since = ""
		if probe.Due(a.today) {
			when += " " + a.tr("always.plan.today", "day", a.weekday(r.Weekday))
		}
		pv.Lines = append(pv.Lines, planLine{"weeks-hint", template.HTML(html.EscapeString(when))})
	}
	key := "always.plan.rule.noqty"
	kv := []string{"at", a.at()}
	if !r.Qty.None() {
		key, kv = "always.plan.rule.qty", append(kv, "qty", a.qty(r.Qty))
	}
	text := html.EscapeString(a.s.i18n.T(a.lang, key))
	pairs := []string{"{link}", `<a href="/settings/lists/always#urlaub">` + html.EscapeString(a.tr("always.plan.link")) + `</a>`}
	for i := 0; i+1 < len(kv); i += 2 {
		pairs = append(pairs, "{"+kv[i]+"}", html.EscapeString(kv[i+1]))
	}
	pv.Lines = append(pv.Lines, planLine{"hint", template.HTML(repl(text, pairs...))})
	return pv
}

// firstText is "Montag, 23.11." for the success line after saving, and whether
// it is the day itself that was chosen (so that today nothing changes).
func (a *alwaysCtx) firstText(r always.Rule) (string, bool) {
	r.Since = a.todayStr()
	first, ok := r.Next(a.today.AddDate(0, 0, 1))
	if !ok {
		return "", false
	}
	probe := r
	probe.Since = ""
	return endSentence(a.dayName(first)), probe.Due(a.today)
}

// storedRule is the rule of an article, nil without one.
func (s *server) storedRule(p principal, id string) (*always.Rule, error) {
	r, ok, err := s.db.AlwaysRule(p.household.ID, id)
	if err != nil || !ok {
		return nil, err
	}
	return &r, nil
}

// autoCardFor builds the card of an article for its page: from the refused post
// when there is one, else from the stored rule.
func (s *server) autoCardFor(p principal, lang string, art articleItem, ap *autoPost) (autoCardView, error) {
	a, err := s.alwaysContext(p, lang)
	if err != nil {
		return autoCardView{}, err
	}
	stored, err := s.storedRule(p, art.ID)
	if err != nil {
		return autoCardView{}, err
	}
	if ap != nil {
		return a.card(art, ap.Form, ap.Errs, stored), nil
	}
	return a.card(art, a.formOfRule(art, stored), nil, stored), nil
}

// saveAuto applies a posted card that passed validation and says what to
// show afterwards: the key and values of the success line, or "" when the card changed nothing worth saying.
func (s *server) saveAuto(p principal, lang string, art articleItem, f autoForm) (key string, values []string, ap *autoPost, err error) {
	a, err := s.alwaysContext(p, lang)
	if err != nil {
		return "", nil, nil, err
	}
	stored, err := s.storedRule(p, art.ID)
	if err != nil {
		return "", nil, nil, err
	}
	name := art.Name(lang)
	if !f.On {
		if stored != nil && stored.Enabled {
			if err := s.db.DisableAlwaysRule(p.household.ID, art.ID); err != nil {
				return "", nil, nil, err
			}
			return "flash.auto_off", []string{name}, nil, nil
		}
		return "", nil, nil, nil
	}
	r, errs := a.validate(art, f, stored)
	if len(errs) > 0 {
		return "", nil, &autoPost{Form: f, Errs: errs}, nil
	}
	saved, err := s.db.SaveAlwaysRule(p.household.ID, r, a.todayStr())
	if err != nil {
		return "", nil, nil, err
	}
	first, today := a.firstText(saved)
	key = "flash.auto_saved"
	if today {
		key = "flash.auto_saved_today"
	}
	return key, []string{name, lowerFirst(a.rhythm(saved.Every, saved.Weekday)), a.at(), saved.ListName, first}, nil, nil
}

// autoFlashFields names the placeholders of the success lines of the card.
func init() {
	flashFields["flash.auto_saved"] = []string{"name", "rhythm", "at", "list", "first"}
	flashFields["flash.auto_saved_today"] = []string{"name", "rhythm", "at", "list", "first"}
	flashFields["flash.auto_off"] = []string{"name"}
}

// handleAutoPlan answers the sentence block of the card for what is chosen so
// far, for the page's script: the rules live here, the script only asks.
func (s *server) handleAutoPlan(w http.ResponseWriter, r *http.Request) {
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
	art, ok := st.item(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	a, err := s.alwaysContext(p, lang)
	if err != nil {
		s.fail(w, "always context", err)
		return
	}
	stored, err := s.storedRule(p, art.ID)
	if err != nil {
		s.fail(w, "always rule", err)
		return
	}
	q := r.URL.Query()
	f := autoForm{On: true, List: q.Get("list"), Every: q.Get("every"), Start: q.Get("start"), QMode: q.Get("qmode"), Amount: q.Get("amount"), Unit: q.Get("unit")}
	f.Weekday, _ = strconv.Atoi(q.Get("weekday"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(s.fragment(lang, "autoPlan", a.plan(art, f, stored))))
}

// endSentence makes sure a sentence ends with a full stop: a date in numbers
// ("23.11.") already has one.
func endSentence(s string) string {
	if strings.HasSuffix(s, ".") {
		return s
	}
	return s + "."
}
