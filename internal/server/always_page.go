package server

import (
	"html"
	"html/template"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/always"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Settings, Lists, Immer auf der Liste (ticket 168, preview-168 screens 9 to 16):
// what comes by itself, the next two weeks, what is not running, and the time
// and the holiday for all articles.

const alwaysPath = "/settings/lists/always"

// alwaysRule is a rule with the article it belongs to.
type alwaysRule struct {
	always.Rule
	Art articleItem
}

// rules returns the household's rules whose article still exists, in the order they were made.
func (a *alwaysCtx) rules() ([]alwaysRule, error) {
	rs, err := a.s.db.AlwaysRules(a.p.household.ID)
	if err != nil {
		return nil, err
	}
	var out []alwaysRule
	for _, r := range rs {
		if it, ok := a.c.st.item(r.ArticleID); ok {
			out = append(out, alwaysRule{r, it})
		}
	}
	return out, nil
}

func (a *alwaysCtx) ruleName(r alwaysRule) string { return r.Art.Name(a.lang) }

// alwaysRow is one article on the overview, the library or a list.
type alwaysRow struct {
	Href, Symbol, Picture, Name string
	Meta1, Meta2                []string // the parts of the two meta lines
	Tag, TagClass               string   // "Angehalten: Liste gelöscht" in a tag
	TagPaused                   bool
}

func (a *alwaysCtx) rowBase(r alwaysRule) alwaysRow {
	row := alwaysRow{Href: libraryPath + "/" + r.ArticleID, Name: a.ruleName(r), Symbol: symbolOf(r.Art)}
	if r.Art.own != nil && r.Art.own.HasPicture {
		row.Picture = pictureURL(*r.Art.own)
	}
	return row
}

// nextLine is "Nächstes Mal: Montag, 23.11." (or "Das erste Mal: …"), with
// "(nach dem Urlaub)" when the holiday moved it.
func (a *alwaysCtx) nextLine(r always.Rule) string {
	d, afterHoliday, ok := a.next(r)
	if !ok {
		return ""
	}
	key := "always.next"
	if first, ok := r.Next(a.todayAfterSince(r)); ok && first.Equal(d) && !afterHoliday && !a.hasRun(r) {
		key = "always.next.first"
	}
	line := a.tr(key, "day", a.dayName(d))
	if afterHoliday {
		line += " " + a.tr("always.next.after_holiday")
	}
	return line
}

// todayAfterSince is the first day a rule can be due.
func (a *alwaysCtx) todayAfterSince(r always.Rule) time.Time {
	since, err := time.Parse(always.DateLayout, r.Since)
	if err != nil {
		return a.today
	}
	return since.AddDate(0, 0, 1)
}

// hasRun says the rule was due on a day that is over already, so its next
// time is not the first.
func (a *alwaysCtx) hasRun(r always.Rule) bool {
	first, ok := r.Next(a.todayAfterSince(r))
	return ok && first.Before(a.from())
}

func (a *alwaysCtx) runningRow(r alwaysRule) alwaysRow {
	row := a.rowBase(r)
	row.Meta1 = []string{a.tr("always.meta.rhythm", "text", a.rhythmOn(r.Rule)), a.qtyOrNone(r.Qty)}
	if n := a.nextLine(r.Rule); n != "" {
		row.Meta2 = []string{n}
	}
	return row
}

func (a *alwaysCtx) stoppedRow(r alwaysRule) alwaysRow {
	row := a.rowBase(r)
	if r.Paused() {
		row.Tag, row.TagClass, row.TagPaused = a.tr("always.tag.paused"), "tag paused", true
		row.Meta1 = []string{a.tr("always.stopped.gone", "list", r.ListName, "again", lowerFirst(a.rhythm(r.Every, r.Weekday)))}
		return row
	}
	row.Tag, row.TagClass = a.tr("always.tag.off"), "tag hidden-tag"
	row.Meta1 = []string{a.tr("always.stopped.off", "text", lowerFirst(a.rhythmOn(r.Rule))), a.qtyOrNone(r.Qty)}
	return row
}

// aheadDay is one day of "Die nächsten zwei Wochen".
type aheadDay struct {
	Name   string
	Today  bool
	Groups []aheadGroup
	Free   []string
}

type aheadGroup struct{ List, Text string }

const aheadDays = 14

func (a *alwaysCtx) ahead(rules []alwaysRule) ([]aheadDay, error) {
	runs, err := a.s.db.AlwaysRuns(a.p.household.ID, a.todayStr())
	if err != nil {
		return nil, err
	}
	hol := a.set.Holiday
	var out []aheadDay
	for i := 0; i < aheadDays; i++ {
		d := a.today.AddDate(0, 0, i)
		day := aheadDay{Name: a.dayName(d), Today: i == 0}
		ran := i == 0 && a.lastRun == a.todayStr()
		switch {
		case ran && hol.On() && hol.Covers(d):
			day.Free = a.holidayFree(rules, d, true)
		case ran:
			day.Groups, day.Free = a.ranGroups(runs)
		case hol.On() && hol.Covers(d):
			day.Free = a.holidayFree(rules, d, false)
		default:
			day.Groups = a.dueGroups(rules, d)
		}
		if len(day.Groups) > 0 || len(day.Free) > 0 {
			out = append(out, day)
		}
	}
	return out, nil
}

// dueGroups is what comes on a day, by list: "Milch (3 l), Vollkornbrot".
func (a *alwaysCtx) dueGroups(rules []alwaysRule, d time.Time) []aheadGroup {
	var order []string
	items := map[string][]string{}
	for _, r := range rules {
		if !r.Due(d) {
			continue
		}
		name := a.ruleName(r)
		if !r.Qty.None() {
			name += " (" + a.qty(r.Qty) + ")"
		}
		if _, ok := items[r.ListID]; !ok {
			order = append(order, r.ListID)
		}
		items[r.ListID] = append(items[r.ListID], name)
	}
	var out []aheadGroup
	for _, id := range order {
		l, _ := a.listByID(id)
		out = append(out, aheadGroup{l.Name, strings.Join(items[id], ", ")})
	}
	return out
}

// ranGroups says what the rules did today, by list.
func (a *alwaysCtx) ranGroups(runs []store.AlwaysRun) (groups []aheadGroup, free []string) {
	var order []string
	parts := map[string][]string{}
	for _, r := range runs {
		switch r.Action {
		case "exists":
			free = append(free, a.tr("always.run.exists", "text", r.Text))
			continue
		case "new":
			p := r.Text
			if !r.Total.None() {
				p += " (" + a.qty(r.Total) + ")"
			}
			parts[r.ListID] = append(parts[r.ListID], a.tr("always.run.new", "text", p))
		case "filled":
			parts[r.ListID] = append(parts[r.ListID], a.tr("always.run.filled", "text", r.Text, "qty", a.qty(r.Part)))
		default:
			parts[r.ListID] = append(parts[r.ListID], a.tr("always.run.added", "text", r.Text, "qty", a.qty(r.Part)))
		}
		if !slices.Contains(order, r.ListID) {
			order = append(order, r.ListID)
		}
	}
	for _, id := range order {
		if l, ok := a.listByID(id); ok {
			groups = append(groups, aheadGroup{l.Name, strings.Join(parts[id], ", ")})
		}
	}
	return groups, free
}

// holidayFree names what did not come on a day of the holiday.
func (a *alwaysCtx) holidayFree(rules []alwaysRule, d time.Time, past bool) []string {
	var names []string
	for _, r := range rules {
		probe := r.Rule
		probe.Since = "" // the holiday decides, not the day the rule was made
		if probe.Due(d) {
			names = append(names, a.ruleName(r))
		}
	}
	if len(names) == 0 {
		return nil
	}
	key := "always.holiday.future"
	if past {
		key = "always.holiday.past"
	}
	if len(names) > 1 {
		key += ".many"
	}
	return []string{a.tr(key, "names", a.names(names))}
}

// holidayNote is the line at the top of the overview and the list pages while a holiday is entered.
type holidayNote struct{ Head, Text string }

func (a *alwaysCtx) holidayNote(forList string) *holidayNote {
	h := a.holiday()
	if !h.On() {
		return nil
	}
	from, _ := time.Parse(always.DateLayout, h.From)
	to, _ := time.Parse(always.DateLayout, h.To)
	after := to.AddDate(0, 0, 1)
	if a.today.Before(from) {
		return &holidayNote{Head: a.tr("always.note.planned"), Text: a.tr("always.note.planned.text", "from", a.dayName(from), "to", a.dayName(to))}
	}
	return &holidayNote{Head: a.tr("always.note.running", "to", a.dayName(to)), Text: a.tr("always.note.running.text", "after", a.dayName(after))}
}

// holidayPlan is the sentence block under the holiday form for what was typed.
func (a *alwaysCtx) holidayPlan(fromS, toS string, rules []alwaysRule) planView {
	h := always.Holiday{From: fromS, To: toS}
	if err := h.Validate(a.today); err != nil || fromS == "" || toS == "" {
		return planView{Stopped: true, Head: a.tr("always.plan.notyet"), Lines: []planLine{{"hint", template.HTML(html.EscapeString(a.tr("always.hplan.notyet.hint")))}}}
	}
	from, _ := time.Parse(always.DateLayout, h.From)
	to, _ := time.Parse(always.DateLayout, h.To)
	after := a.dayName(to.AddDate(0, 0, 1))
	if !a.today.Before(from) {
		return planView{Stopped: true, Head: a.tr("always.hplan.running", "to", a.dayName(to)), Lines: []planLine{{"hint", template.HTML(html.EscapeString(a.tr("always.hplan.running.hint", "after", after)))}}}
	}
	pv := planView{Stopped: true, Head: a.tr("always.hplan.head", "from", a.dayName(from), "to", a.dayName(to))}
	if fall := a.fallOut(rules, from, to); fall != "" {
		pv.Lines = append(pv.Lines, planLine{"weeks-hint", template.HTML(html.EscapeString(fall))})
	}
	pv.Lines = append(pv.Lines, planLine{"hint", template.HTML(html.EscapeString(a.tr("always.hplan.hint", "after", after, "from", a.date(from))))})
	return pv
}

// fallOut names, by name, what does not come during the holiday and on which days.
func (a *alwaysCtx) fallOut(rules []alwaysRule, from, to time.Time) string {
	type group struct {
		names []string
		dates []string
	}
	var groups []*group
	for _, r := range rules {
		if !r.Running() {
			continue
		}
		var dates []string
		probe := r.Rule
		probe.Since = ""
		for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
			if probe.Due(d) {
				dates = append(dates, a.date(d))
			}
		}
		if len(dates) == 0 {
			continue
		}
		var g *group
		for _, x := range groups {
			if slices.Equal(x.dates, dates) {
				g = x
			}
		}
		if g == nil {
			g = &group{dates: dates}
			groups = append(groups, g)
		}
		g.names = append(g.names, a.ruleName(r))
	}
	if len(groups) == 0 {
		return ""
	}
	parts := make([]string, len(groups))
	for i, g := range groups {
		parts[i] = a.tr("always.hplan.fall.part", "names", a.names(g.names), "dates", a.names(g.dates))
	}
	return endSentence(a.tr("always.hplan.fall", "parts", strings.Join(parts, ", ")))
}

// alwaysForm is what the form "Für alle Artikel" shows.
type alwaysForm struct {
	At, From, To string
	On           bool
}

type alwaysErrs struct{ At, From, To string }

func (a *alwaysCtx) formOfSettings() alwaysForm {
	h := a.holiday()
	return alwaysForm{At: a.set.At, From: h.From, To: h.To, On: h.On()}
}

func (s *server) renderAlways(w http.ResponseWriter, r *http.Request, p principal, status int, posted *alwaysForm, errs alwaysErrs) {
	lang := s.langOf(r)
	a, err := s.alwaysContext(p, lang)
	if err != nil {
		s.fail(w, "always context", err)
		return
	}
	rules, err := a.rules()
	if err != nil {
		s.fail(w, "always rules", err)
		return
	}
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID),
		"BackHref": listsSettingsPath, "BackText": s.i18n.T(lang, "lists.set.back"),
		"At": a.at(), "Empty": len(rules) == 0, "HolidayNote": a.holidayNote(""),
		"LibraryHref": libraryPath, "PlanURL": alwaysPath + "/plan",
	}
	var running, stopped []alwaysRow
	for _, ru := range rules {
		if ru.Running() {
			running = append(running, a.runningRow(ru))
		} else {
			stopped = append(stopped, a.stoppedRow(ru))
		}
	}
	ahead, err := a.ahead(rules)
	if err != nil {
		s.fail(w, "always ahead", err)
		return
	}
	form := a.formOfSettings()
	if posted != nil {
		form = *posted
	}
	data["Running"], data["Stopped"], data["Ahead"] = running, stopped, ahead
	data["Form"], data["Errs"] = form, errs
	data["HolidayState"] = a.tr("always.holiday.on.unset")
	if h := a.holiday(); posted == nil && h.On() {
		from, _ := time.Parse(always.DateLayout, h.From)
		to, _ := time.Parse(always.DateLayout, h.To)
		if a.today.Before(from) {
			data["HolidayState"] = a.tr("always.holiday.on.range", "from", a.date(from), "to", a.date(to))
		} else {
			data["HolidayState"] = a.tr("always.holiday.on.until", "to", a.dayName(to))
		}
	}
	data["HolidayPlan"] = a.holidayPlan(form.From, form.To, rules)
	data["TimeHint"] = a.tr("always.time.hint", "at", a.at())
	data["Lead"] = a.tr("always.lead", "at", a.at())
	s.render(w, r, status, "always.html", data)
}

func (s *server) handleAlways(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.renderAlways(w, r, p, http.StatusOK, nil, alwaysErrs{})
}

func (s *server) handleAlwaysSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	a, err := s.alwaysContext(p, lang)
	if err != nil {
		s.fail(w, "always context", err)
		return
	}
	_ = r.ParseForm()
	form := alwaysForm{At: strings.TrimSpace(r.PostFormValue("at")), On: r.PostFormValue("holiday") != "", From: r.PostFormValue("holiday_from"), To: r.PostFormValue("holiday_to")}
	var errs alwaysErrs
	if !always.ValidTime(form.At) {
		errs.At = a.tr("always.err.at")
	}
	if form.On {
		h := always.Holiday{From: form.From, To: form.To}
		if err := h.Validate(a.today); err != nil {
			fe, _ := err.(*always.FieldError)
			switch fe.Code {
			case "from.required":
				errs.From = a.tr("always.err.from")
			case "to.required":
				errs.To = a.tr("always.err.to")
			case "to.before":
				from, _ := time.Parse(always.DateLayout, form.From)
				errs.To = a.tr("always.err.to.before", "from", a.dayName(from))
			default:
				errs.To = a.tr("always.err.to.past")
			}
		}
	}
	if errs != (alwaysErrs{}) {
		s.renderAlways(w, r, p, http.StatusUnprocessableEntity, &form, errs)
		return
	}
	st := always.Settings{At: form.At}
	if form.On {
		st.Holiday = always.Holiday{From: form.From, To: form.To}
	}
	if err := s.db.SetAlwaysSettings(p.household.ID, st); err != nil {
		s.fail(w, "save always settings", err)
		return
	}
	a.set = st
	switch h := st.Holiday; {
	case !h.On():
		s.redirectSavedFields(w, r, alwaysPath, "flash.always_saved", a.atOf(st.At))
	default:
		from, _ := time.Parse(always.DateLayout, h.From)
		to, _ := time.Parse(always.DateLayout, h.To)
		if a.today.Before(from) {
			s.redirectSavedFields(w, r, alwaysPath, "flash.always_holiday_planned", a.dayName(from), endSentence(a.dayName(to)))
		} else {
			s.redirectSavedFields(w, r, alwaysPath, "flash.always_holiday_running", endSentence(a.dayName(to)))
		}
	}
}

// handleAlwaysPlan answers the sentence under the holiday for what was typed.
func (s *server) handleAlwaysPlan(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	a, err := s.alwaysContext(p, lang)
	if err != nil {
		s.fail(w, "always context", err)
		return
	}
	rules, err := a.rules()
	if err != nil {
		s.fail(w, "always rules", err)
		return
	}
	q := r.URL.Query()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(s.fragment(lang, "autoPlan", a.holidayPlan(q.Get("from"), q.Get("to"), rules))))
}

func init() {
	flashFields["flash.always_saved"] = []string{"at"}
	flashFields["flash.always_holiday_planned"] = []string{"from", "to"}
	flashFields["flash.always_holiday_running"] = []string{"to"}
}

// hubMeta is one line of the row "Immer auf der Liste" in Settings, Lists.
type hubMeta struct {
	Text    string
	Stopped bool
}

// hubRow says in words what comes by itself, when next, and what is not running.
func (a *alwaysCtx) hubRow(rules []alwaysRule) []hubMeta {
	var out []hubMeta
	if h := a.holiday(); h.On() {
		from, _ := time.Parse(always.DateLayout, h.From)
		to, _ := time.Parse(always.DateLayout, h.To)
		if a.today.Before(from) {
			out = append(out, hubMeta{a.tr("always.row.holiday.planned", "from", a.dayName(from), "to", a.dayName(to)), true})
		} else {
			out = append(out, hubMeta{a.tr("always.row.holiday", "to", a.dayName(to)), true})
		}
	}
	var running, paused, off []string
	type due struct {
		day  time.Time
		name string
		list string
	}
	var nexts []due
	for _, r := range rules {
		name := a.ruleName(r)
		switch {
		case r.Running():
			running = append(running, name)
			if d, _, ok := a.next(r.Rule); ok {
				nexts = append(nexts, due{d, name, r.ListName})
			}
		case r.Paused():
			paused = append(paused, a.tr("always.row.paused.item", "name", name))
		default:
			off = append(off, name)
		}
	}
	switch len(running) {
	case 0:
		if len(paused)+len(off) == 0 {
			out = append(out, hubMeta{a.tr("always.row.none"), false})
		}
	case 1:
		out = append(out, hubMeta{a.tr("always.row.running.one", "names", running[0]), false})
	default:
		out = append(out, hubMeta{a.tr("always.row.running.many", "names", a.names(running)), false})
	}
	if len(nexts) > 0 {
		slices.SortStableFunc(nexts, func(x, y due) int { return x.day.Compare(y.day) })
		var lists []string
		by := map[string][]string{}
		for _, n := range nexts {
			if !n.day.Equal(nexts[0].day) {
				break
			}
			if _, ok := by[n.list]; !ok {
				lists = append(lists, n.list)
			}
			by[n.list] = append(by[n.list], n.name)
		}
		parts := make([]string, len(lists))
		for i, l := range lists {
			parts[i] = a.tr("always.row.next.part", "names", a.names(by[l]), "list", l)
		}
		out = append(out, hubMeta{a.tr("always.row.next", "day", a.dayName(nexts[0].day), "at", a.at(), "what", strings.Join(parts, ", ")), false})
	}
	var stopped []string
	if len(paused) > 0 {
		stopped = append(stopped, a.tr("always.row.paused", "items", a.names(paused)))
	}
	if len(off) > 0 {
		stopped = append(stopped, a.tr("always.row.off", "names", a.names(off)))
	}
	if len(stopped) > 0 {
		out = append(out, hubMeta{strings.Join(stopped, " "), true})
	}
	return out
}

// hubRowFor builds the row for the settings page.
func (s *server) hubRowFor(p principal, lang string) ([]hubMeta, error) {
	a, err := s.alwaysContext(p, lang)
	if err != nil {
		return nil, err
	}
	rules, err := a.rules()
	if err != nil {
		return nil, err
	}
	return a.hubRow(rules), nil
}
