package server

import (
	"errors"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

const (
	// aheadWeeks is how many weeks "The coming weeks" lists.
	aheadWeeks = 6
	// startWeeks is how many weeks after the current one a start week can be.
	startWeeks = 52
	// hintWeeks is the most weeks the "fills weeks" hint lists.
	hintWeeks = 8
)

// tplField is the name of the form field of a template's weekday (1 Monday to
// 7 Sunday) and mealtime.
func tplField(weekday int, m meal.Mealtime) string { return fmt.Sprintf("tpl_%d_%s", weekday, m) }

func (s *server) tr(lang, key string, kv ...string) string {
	return s.i18n.Format(lang, i18n.Msg(key, kv...))
}

func (s *server) mealWeekday(lang string, weekday int) string {
	return s.i18n.T(lang, "meals.weekday."+strconv.Itoa(weekday%7))
}

// joinWords joins "A, B and C".
func (s *server) joinWords(lang string, items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " " + s.i18n.T(lang, "meals.tpl.and") + " " + items[len(items)-1]
}

// yearRange is the week that starts on monday with its year: "23.11. – 29.11.2026",
// and across New Year with both years.
func (s *server) yearRange(lang string, f store.DateFormat, monday time.Time) string {
	from, to := datefmt.RangeParts(f, monday, monday.AddDate(0, 0, 6))
	return fmt.Sprintf(s.i18n.T(lang, "meals.week.range"), from, to)
}

// weekRange is "23/11 – 29/11" for the week that starts on monday.
func (s *server) weekRange(lang string, f store.DateFormat, monday time.Time) string {
	return fmt.Sprintf(s.i18n.T(lang, "meals.week.range"), datefmt.Short(f, monday), datefmt.Short(f, monday.AddDate(0, 0, 6)))
}

// weekdaysOf lists the weekdays (1 to 7) a template has an entry for.
func weekdaysOf(t meal.Template) []int {
	seen := map[int]bool{}
	for s := range t.Filled() {
		seen[s.Weekday] = true
	}
	var days []int
	for d := range seen {
		days = append(days, d)
	}
	slices.Sort(days)
	return days
}

// onText is "on Monday to Friday": a run of three or more days is a range,
// anything else names the days in full.
func (s *server) onText(lang string, days []int) string {
	if n := len(days); n >= 3 && days[n-1]-days[0] == n-1 {
		return s.tr(lang, "meals.tpl.on.range", "from", s.mealWeekday(lang, days[0]), "to", s.mealWeekday(lang, days[n-1]))
	}
	return s.tr(lang, "meals.tpl.on.days", "days", s.namesOf(lang, days))
}

func (s *server) namesOf(lang string, days []int) string {
	names := make([]string, len(days))
	for i, d := range days {
		names[i] = s.mealWeekday(lang, d)
	}
	return s.joinWords(lang, names)
}

// fillsLabel says which mealtimes a template fills: "Dinner" or "All meals".
func (s *server) fillsLabel(lang string, t meal.Template) string {
	if t.AllMealtimes {
		return s.i18n.T(lang, "meals.tpl.all_meals")
	}
	return s.i18n.T(lang, "meals.mealtime."+string(t.Mealtime))
}

// fillsWord is fillsLabel for the middle of a sentence: "dinner", "all meals".
func (s *server) fillsWord(lang string, t meal.Template) string {
	if t.AllMealtimes {
		return s.i18n.T(lang, "meals.tpl.all_meals_lc")
	}
	return s.i18n.T(lang, "meals.tpl.mt."+string(t.Mealtime))
}

func thisMonday(p principal, s *server) time.Time { return weekOf(mustDay(s.today(p))) }

// scheduleText is "Every 2 weeks from week 46 (09/11/2026) · next in week 48 (23/11 – 29/11)".
func (s *server) scheduleText(lang string, f store.DateFormat, t meal.Template, this time.Time) string {
	start, _ := time.Parse(task.DateLayout, t.Start)
	args := []string{"week", strconv.Itoa(isoWeek(start)), "date", datefmt.Date(f, start)}
	head := s.tr(lang, "meals.tpl.sched.one", args...)
	if t.Interval > 1 {
		head = s.tr(lang, "meals.tpl.sched.n", append(args, "n", strconv.Itoa(t.Interval))...)
	}
	fills := t.AppliesInWeek(this)
	if fills && t.Interval == 1 {
		return head
	}
	from := this
	key := "meals.tpl.sched.next"
	if fills {
		from, key = this.AddDate(0, 0, 7), "meals.tpl.sched.fills"
	}
	next := t.Weeks(from, 1)
	if len(next) == 0 {
		return head
	}
	return head + s.tr(lang, key, "next", strconv.Itoa(isoWeek(next[0])), "range", s.weekRange(lang, f, next[0]))
}

type tplRow struct {
	ID, Name, Label, On, Schedule string
}

type aheadFill struct{ Name, Meal string }

type aheadWeek struct {
	No    int
	Range string
	This  bool
	Fills []aheadFill
}

// handleTemplatesPage is the Templates menu.
func (s *server) handleTemplatesPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.renderTemplates(w, r, p, "")
}

func (s *server) renderTemplates(w http.ResponseWriter, r *http.Request, p principal, successText string) {
	lang := s.langOf(r)
	list, err := s.db.MealTemplates(p.household.ID)
	if err != nil {
		s.fail(w, "meal templates", err)
		return
	}
	this := thisMonday(p, s)
	rows := make([]tplRow, 0, len(list))
	for _, t := range list {
		rows = append(rows, tplRow{ID: t.ID, Name: t.Name, Label: s.fillsLabel(lang, t), On: s.onText(lang, weekdaysOf(t)), Schedule: s.scheduleText(lang, p.household.DateFormat, t, this)})
	}
	var ahead []aheadWeek
	for i := 0; i < aheadWeeks && len(list) > 0; i++ {
		monday := this.AddDate(0, 0, 7*i)
		week := aheadWeek{No: isoWeek(monday), Range: s.weekRange(lang, p.household.DateFormat, monday), This: i == 0}
		for _, t := range list {
			if t.AppliesInWeek(monday) {
				week.Fills = append(week.Fills, aheadFill{t.Name, s.fillsWord(lang, t)})
			}
		}
		ahead = append(ahead, week)
	}
	data := map[string]any{"Templates": rows, "Ahead": ahead}
	if successText != "" {
		data["SuccessText"] = successText
	}
	s.render(w, r, http.StatusOK, "meal_templates.html", data)
}

// templateSummary is the line above the meal plan week that names the
// templates which fill it, each linking to its page. It is empty when the
// household has no templates at all.
func (s *server) templateSummary(p principal, lang string, monday time.Time) (template.HTML, error) {
	list, err := s.db.MealTemplates(p.household.ID)
	if err != nil || len(list) == 0 {
		return "", err
	}
	var links []string
	for _, t := range list {
		if t.AppliesInWeek(monday) {
			links = append(links, fmt.Sprintf(`<a href="/meals/templates/%s">%s</a> <span class="mt-of">(%s)</span>`, html.EscapeString(t.ID), html.EscapeString(t.Name), html.EscapeString(s.fillsWord(lang, t))))
		}
	}
	key := "meals.tpl.sum.many"
	switch len(links) {
	case 0:
		key = "meals.tpl.sum.none"
	case 1:
		key = "meals.tpl.sum.one"
	}
	text := html.EscapeString(s.i18n.T(lang, key))
	return template.HTML(strings.Replace(text, "{list}", s.joinWords(lang, links), 1)), nil //nolint:gosec // names are escaped above
}

// tplForm is what the form of a template shows.
type tplForm struct {
	ID, Name, Fills, Mealtime, Every, Start string
	Values                                  map[string]string
}

func templateFormOf(t meal.Template) tplForm {
	f := tplForm{ID: t.ID, Name: t.Name, Fills: "one", Mealtime: string(t.Mealtime), Every: strconv.Itoa(t.Interval), Start: t.Start, Values: map[string]string{}}
	if t.AllMealtimes {
		f.Fills, f.Mealtime = "all", string(meal.Dinner)
	}
	for sl, text := range t.Filled() {
		f.Values[tplField(sl.Weekday, sl.Mealtime)] = text
	}
	return f
}

type startOption struct {
	Value, Label string
	Selected     bool
}

// startOptions lists the current week and the 52 after it; a start week that
// is already over stays in the list while its template is edited.
func (s *server) startOptions(lang string, f store.DateFormat, this time.Time, selected string) []startOption {
	var out []startOption
	label := func(m time.Time) string {
		return s.tr(lang, "meals.tpl.start.option", "week", strconv.Itoa(isoWeek(m)), "from", datefmt.Short(f, m), "to", datefmt.Short(f, m.AddDate(0, 0, 6)))
	}
	if d, err := time.Parse(task.DateLayout, selected); err == nil && d.Before(this) {
		out = append(out, startOption{d.Format(task.DateLayout), label(d), true})
	}
	found := false
	for i := 0; i <= startWeeks; i++ {
		m := this.AddDate(0, 0, 7*i)
		v := m.Format(task.DateLayout)
		found = found || v == selected
		out = append(out, startOption{v, label(m), v == selected})
	}
	if !found && len(out) > 0 && out[0].Selected == false {
		if d, err := time.Parse(task.DateLayout, selected); err == nil && d.After(this) { // a start week further ahead, set through the API
			out = append(out, startOption{d.Format(task.DateLayout), label(d), true})
		}
	}
	return out
}

func isoYear(monday time.Time) int { return monday.AddDate(0, 0, 3).Year() }

// weeksHint spells out the weeks a template fills: "Fills week 47 (this week,
// from today), 49, 51, 53, then week 2 of 2027 (11/01/2027), and so on." The
// interval counts weeks from the start week, so the turn of the year shows.
// form.js builds the same sentence while the fields change.
func (s *server) weeksHint(lang string, f store.DateFormat, every int, start string, this time.Time) template.HTML {
	st, err := time.Parse(task.DateLayout, start)
	if err != nil || every < 1 || every > meal.MaxInterval {
		return ""
	}
	weeks := meal.Template{Start: st.Format(task.DateLayout), Interval: every}.Weeks(this, 60)
	year := isoYear(weeks[0])
	var b strings.Builder
	b.WriteString(html.EscapeString(s.i18n.T(lang, "meals.tpl.hint.lead")))
	n, capped := 0, false
	for _, w := range weeks {
		if isoYear(w) != year {
			break
		}
		if n == hintWeeks {
			capped = true
			break
		}
		if n > 0 {
			b.WriteString(",")
		}
		b.WriteString(" ")
		fmt.Fprintf(&b, "<b>%d</b>", isoWeek(w))
		if n == 0 && w.Equal(this) {
			b.WriteString(html.EscapeString(s.i18n.T(lang, "meals.tpl.hint.this")))
		}
		n++
	}
	if !capped {
		for _, w := range weeks {
			if isoYear(w) != year {
				then := html.EscapeString(s.i18n.T(lang, "meals.tpl.hint.then"))
				then = strings.NewReplacer("{week}", fmt.Sprintf("<b>%d</b>", isoWeek(w)), "{year}", strconv.Itoa(isoYear(w)), "{date}", datefmt.Date(f, w)).Replace(then)
				b.WriteString(then)
				break
			}
		}
	}
	b.WriteString(html.EscapeString(s.i18n.T(lang, "meals.tpl.hint.tail")))
	return template.HTML(b.String()) //nolint:gosec // every part is escaped or a number
}

// formErrors says what to show wrong in the form of a template.
type formErrors struct {
	Name, Every, Entries, Mealtime bool
	Field                          string // the meal field that is too long
	Clash                          *store.ClashError
}

func (s *server) renderTemplateForm(w http.ResponseWriter, r *http.Request, p principal, status int, f tplForm, errs formErrors, original string) {
	lang := s.langOf(r)
	this := thisMonday(p, s)
	if f.Start == "" {
		f.Start = this.Format(task.DateLayout)
	}
	every, _ := strconv.Atoi(strings.TrimSpace(f.Every))
	var days []map[string]any
	errDay, errMealtime := 0, ""
	for wd := 1; wd <= 7; wd++ {
		var cells []map[string]any
		for _, m := range meal.Mealtimes() {
			name := tplField(wd, m)
			cells = append(cells, map[string]any{"Name": name, "Mealtime": string(m), "Value": f.Values[name]})
			if errs.Field == name {
				errDay, errMealtime = wd, string(m)
			}
		}
		days = append(days, map[string]any{"Weekday": wd % 7, "Cells": cells})
	}
	title := s.i18n.T(lang, "meals.tpl.new")
	action := "/meals/templates"
	if f.ID != "" {
		title, action = original, "/meals/templates/"+f.ID
	}
	data := map[string]any{
		"Title": title, "Edit": f.ID != "", "ID": f.ID, "OriginalName": original, "Action": action,
		"Name": f.Name, "Fills": f.Fills, "Mealtime": f.Mealtime, "Every": f.Every, "Mealtimes": meal.Mealtimes(),
		"StartOptions": s.startOptions(lang, p.household.DateFormat, this, f.Start), "Today": this.Format(task.DateLayout), "DateFormat": string(p.household.DateFormat), "WeeksHint": s.weeksHint(lang, p.household.DateFormat, every, f.Start, this),
		"Days": days, "ErrName": errs.Name, "ErrEvery": errs.Every, "ErrMealtime": errs.Mealtime, "ErrField": errs.Field,
	}
	switch {
	case errs.Clash != nil:
		var names, sentences []string
		for _, c := range errs.Clash.Clashes {
			quoted := s.quoted(lang, c.Template.Name)
			names = append(names, quoted)
			sentences = append(sentences, s.tr(lang, "meals.tpl.clash.sentence", "template", quoted, "meal", s.i18n.T(lang, "meals.tpl.clash.mt."+string(c.Clash.Mealtime)),
				"days", s.namesOf(lang, c.Clash.Weekdays), "week", strconv.Itoa(isoWeek(c.Clash.Week)),
				"range", s.yearRange(lang, p.household.DateFormat, c.Clash.Week)))
		}
		data["ErrClash"] = sentences
		data["TopError"] = template.HTML(fmt.Sprintf(`%s %s (%s <a href="#schedule">%s</a>). %s`, html.EscapeString(s.i18n.T(lang, "meals.tpl.clash.top.before")),
			html.EscapeString(s.joinWords(lang, names)), html.EscapeString(s.i18n.T(lang, "meals.tpl.clash.top.see")), html.EscapeString(s.i18n.T(lang, "meals.tpl.schedule.legend")),
			html.EscapeString(s.i18n.T(lang, "meals.tpl.clash.top.after")))) //nolint:gosec // every part is escaped
	case errs.Entries:
		data["TopError"] = template.HTML(html.EscapeString(s.i18n.T(lang, "meals.tpl.error.entries"))) //nolint:gosec // escaped
	case errs.Field != "":
		data["TopError"] = template.HTML(fmt.Sprintf(`%s <a href="#%s">%s</a>%s`, html.EscapeString(s.i18n.T(lang, "meals.error.form.before")), html.EscapeString(errs.Field),
			html.EscapeString(fmt.Sprintf(s.i18n.T(lang, "meals.error.field"), s.mealWeekday(lang, errDay), s.i18n.T(lang, "meals.mealtime."+errMealtime))),
			html.EscapeString(s.i18n.T(lang, "meals.error.form.after")))) //nolint:gosec // escaped
	}
	s.render(w, r, status, "meal_template.html", data)
}

// quoted puts a name in the quotation marks of the language.
func (s *server) quoted(lang, name string) string {
	if lang == "de" {
		return "„" + name + "“"
	}
	return "“" + name + "”"
}

func (s *server) handleTemplateNew(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	this := thisMonday(p, s).Format(task.DateLayout)
	s.renderTemplateForm(w, r, p, http.StatusOK, tplForm{Fills: "one", Mealtime: string(meal.Dinner), Every: "1", Start: this, Values: map[string]string{}}, formErrors{}, "")
}

func (s *server) handleTemplatePage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	t, err := s.db.MealTemplate(p.household.ID, r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, "meal template", err)
	default:
		s.renderTemplateForm(w, r, p, http.StatusOK, templateFormOf(t), formErrors{}, t.Name)
	}
}

// readTemplateForm reads the form of a template; the entries of every
// mealtime are kept in the form, the template ignores the ones it does not fill.
func (s *server) readTemplateForm(r *http.Request, p principal) (meal.Template, tplForm, formErrors) {
	_ = r.ParseForm()
	f := tplForm{ID: r.PathValue("id"), Name: strings.TrimSpace(r.PostFormValue("name")), Fills: r.PostFormValue("fills"), Mealtime: r.PostFormValue("mealtime"),
		Every: strings.TrimSpace(r.PostFormValue("every")), Start: r.PostFormValue("start"), Values: map[string]string{}}
	if f.Fills != "all" {
		f.Fills = "one"
	}
	start := thisMonday(p, s)
	if d, err := time.Parse(task.DateLayout, f.Start); err == nil {
		start = weekOf(d)
	}
	f.Start = start.Format(task.DateLayout)
	every, _ := strconv.Atoi(f.Every)
	t := meal.Template{ID: f.ID, Name: f.Name, AllMealtimes: f.Fills == "all", Mealtime: meal.Mealtime(f.Mealtime), Interval: every, Start: f.Start, Entries: map[meal.Slot]string{}}
	var errs formErrors
	for wd := 1; wd <= 7; wd++ {
		for _, m := range meal.Mealtimes() {
			name := tplField(wd, m)
			text := r.PostFormValue(name)
			f.Values[name] = text
			clean, err := meal.CleanText(text)
			if err != nil && errs.Field == "" {
				errs.Field = name
			}
			t.Entries[meal.Slot{Weekday: wd, Mealtime: m}] = clean
		}
	}
	if f.Mealtime == "" && !t.AllMealtimes {
		f.Mealtime = string(meal.Dinner)
		t.Mealtime = meal.Dinner
	}
	return t, f, errs
}

// saveTemplatePage saves the form of a new template or, with an id in the path, of an existing one.
func (s *server) saveTemplatePage(w http.ResponseWriter, r *http.Request, p principal) {
	t, f, errs := s.readTemplateForm(r, p)
	original := f.Name
	if f.ID != "" {
		if cur, err := s.db.MealTemplate(p.household.ID, f.ID); err == nil {
			original = cur.Name
		} else if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		} else {
			s.fail(w, "meal template", err)
			return
		}
	}
	if errs.Field == "" {
		if err := t.Validate(); err != nil {
			var bad *meal.InvalidError
			if errors.As(err, &bad) {
				switch bad.Field {
				case "name":
					errs.Name = true
				case "every":
					errs.Every = true
				case "entries":
					errs.Entries = true
				case "mealtime":
					errs.Mealtime = true
				}
			}
		}
	}
	if errs != (formErrors{}) {
		s.renderTemplateForm(w, r, p, http.StatusUnprocessableEntity, f, errs, original)
		return
	}
	var saved meal.Template
	var err error
	if f.ID == "" {
		saved, err = s.db.CreateMealTemplate(p.household.ID, t, s.mealOptions(s.langOf(r), p))
	} else {
		saved, err = s.db.UpdateMealTemplate(p.household.ID, t, s.mealOptions(s.langOf(r), p))
	}
	var clash *store.ClashError
	switch {
	case err == nil:
		s.redirectSaved(w, r, "/meals/templates", "flash.template_saved:"+saved.ID)
	case errors.As(err, &clash):
		s.renderTemplateForm(w, r, p, http.StatusConflict, f, formErrors{Clash: clash}, original)
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrInvalid):
		s.renderTemplateForm(w, r, p, http.StatusUnprocessableEntity, f, formErrors{Entries: true}, original)
	default:
		s.fail(w, "save meal template", err)
	}
}

func (s *server) handleTemplateCreate(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.saveTemplatePage(w, r, p)
	}
}

func (s *server) handleTemplateSave(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.saveTemplatePage(w, r, p)
	}
}

func (s *server) handleTemplateDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	t, err := s.db.MealTemplate(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "meal template", err)
		return
	}
	if err := s.db.DeleteMealTemplate(p.household.ID, t.ID, s.mealOptions(s.langOf(r), p)); err != nil {
		s.fail(w, "delete meal template", err)
		return
	}
	// The menu answers directly: the name is the household's text, which a
	// success cookie never carries.
	s.renderTemplates(w, r, p, s.tr(s.langOf(r), "flash.template_deleted", "name", t.Name))
}

// rollMealPlan keeps the meal-plan calendar reaching a year ahead as the days
// go by: once a day per household it writes the meals the templates fill in
// for the days that came into reach.
func (s *server) rollMealPlan(h store.Household, lang string) {
	p := principal{household: h}
	today := s.today(p)
	if prev, ok := s.rolled.Load(h.ID); ok && prev == today {
		return
	}
	if err := s.db.RefreshMealPlan(h.ID, s.mealOptions(lang, p)); err != nil {
		s.logger.Error("refresh meal plan", "err", err)
		return
	}
	s.rolled.Store(h.ID, today)
}

// templateSavedText and restoredText are the success lines that need a name
// from the household's data.
func (s *server) templateSavedText(lang, id string) string {
	h, err := s.db.HomeHousehold()
	if err != nil {
		return s.i18n.T(lang, "flash.saved")
	}
	t, err := s.db.MealTemplate(h.ID, id)
	if err != nil {
		return s.i18n.T(lang, "flash.saved")
	}
	return s.tr(lang, "flash.template_saved", "name", t.Name)
}

func (s *server) restoredText(lang, field string) string {
	day, mt, ok := parseMealField(field)
	h, err := s.db.HomeHousehold()
	if !ok || err != nil {
		return s.i18n.T(lang, "flash.meals_saved")
	}
	d := mustDay(day)
	list, err := s.db.MealTemplates(h.ID)
	if err != nil {
		return s.i18n.T(lang, "flash.meals_saved")
	}
	for _, t := range list {
		if _, ok := t.Dish(d, mt); ok {
			return s.tr(lang, "flash.meal_restored", "weekday", s.mealWeekday(lang, int(d.Weekday())), "meal", s.i18n.T(lang, "meals.restored.meal."+string(mt)),
				"template", t.Name, "week", strconv.Itoa(isoWeek(weekOf(d))))
		}
	}
	return fmt.Sprintf(s.i18n.T(lang, "flash.meals_saved"), isoWeek(weekOf(d)))
}
