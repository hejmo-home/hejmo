package server

import (
	"errors"
	"html"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

// tasksPhrase says how many tasks, for the sentence it sits in ("the 5 tasks").
func (s *server) tasksPhrase(lang, key string, n int) string {
	if n == 1 {
		return s.i18n.T(lang, key+".one")
	}
	return s.i18n.Format(lang, i18n.Msg(key+".many", "n", strconv.Itoa(n)))
}

// tplStepView is a step of a template as the pages list it.
type tplStepView struct {
	ID             string
	Nr             int
	Emoji, Title   string
	Stars          int
	Repeat         string
	Weekdays       []string
	CanUp, CanDown bool
}

func tplSteps(list []task.TemplateStep) []tplStepView {
	out := make([]tplStepView, 0, len(list))
	for i, st := range list {
		out = append(out, tplStepView{ID: st.ID, Nr: i + 1, Emoji: st.Emoji, Title: st.Title, Stars: st.Stars, Repeat: string(st.Repeat), Weekdays: st.Weekdays, CanUp: i > 0, CanDown: i < len(list)-1})
	}
	return out
}

// tplCard is a template as a card of the templates menu.
type tplCard struct {
	ID, Name, Part, State, Use string
	Own                        bool
	Steps                      []tplStepView
}

func (s *server) tplCardOf(lang string, t task.RoutineTemplate) tplCard {
	key := "routine.tpl.state.own"
	if t.Shipped {
		key = "routine.tpl.state.shipped"
	}
	return tplCard{ID: t.ID, Name: t.Name, Part: string(t.Part), Own: !t.Shipped, Steps: tplSteps(t.Steps),
		State: s.i18n.Format(lang, i18n.Msg(key, "part", s.i18n.T(lang, "routine.part."+string(t.Part)))),
		Use:   s.i18n.Format(lang, i18n.Msg("routine.tpl.use.label", "name", t.Name))}
}

func (s *server) handleRoutineTemplates(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	own, err := s.db.RoutineTemplates(p.household.ID)
	if err != nil {
		s.fail(w, "routine templates", err)
		return
	}
	var ownCards, shipped []tplCard
	for _, t := range own {
		ownCards = append(ownCards, s.tplCardOf(lang, t))
	}
	for _, t := range s.shippedTemplates(lang) {
		shipped = append(shipped, s.tplCardOf(lang, t))
	}
	s.render(w, r, http.StatusOK, "routine_templates.html", map[string]any{"Person": p.person, "Own": ownCards, "Shipped": shipped, "SyncStale": s.syncStale(p.household.ID)})
}

// tplPick is a template as a choice card on the New routine page.
type tplPick struct {
	ID, Name, Part, Count string
	Steps                 []string // each with its emoji; all but the last end in a comma
}

func (s *server) tplPicksOf(lang string, list []task.RoutineTemplate) []tplPick {
	out := make([]tplPick, 0, len(list))
	for _, t := range list {
		pick := tplPick{ID: t.ID, Name: t.Name, Part: s.i18n.T(lang, "routine.part."+string(t.Part)), Count: s.tasksPhrase(lang, "routine.tpl.pick.count", len(t.Steps))}
		for i, st := range t.Steps {
			label := st.Title
			if st.Emoji != "" {
				label = st.Emoji + " " + st.Title
			}
			if i < len(t.Steps)-1 {
				label += ","
			}
			pick.Steps = append(pick.Steps, label)
		}
		out = append(out, pick)
	}
	return out
}

// tplPickData adds the template choice to the data of the New routine page.
func (s *server) tplPickData(lang string, p principal, data map[string]any) error {
	own, err := s.db.RoutineTemplates(p.household.ID)
	if err != nil {
		return err
	}
	data["OwnTemplates"] = s.tplPicksOf(lang, own)
	data["ShippedTemplates"] = s.tplPicksOf(lang, s.shippedTemplates(lang))
	return nil
}

// tplPageState is what a refused form on a template page needs to show again.
type tplPageState struct {
	Add, Settings map[string]any
	Error, Field  string
}

func (s *server) renderTemplatePage(w http.ResponseWriter, r *http.Request, p principal, status int, st tplPageState) {
	lang := s.langOf(r)
	t, err := s.ownTemplate(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, errShippedTemplate) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "routine template", err)
		return
	}
	origin := ""
	if t.OriginPerson != "" {
		origin = s.i18n.Format(lang, i18n.Msg("routine.tpl.origin", "routine", t.OriginRoutine, "person", t.OriginPerson, "date", datefmt.Date(p.household.DateFormat, t.SavedAt.In(p.location()))))
	}
	add := st.Add
	if add == nil {
		add = map[string]any{"Repeat": "daily", "Stars": "1", "Weekdays": []string{}}
	}
	settings := st.Settings
	if settings == nil {
		settings = map[string]any{"Name": t.Name, "Part": string(t.Part)}
	}
	data := map[string]any{
		"Person": p.person, "Tpl": s.tplCardOf(lang, t), "Origin": origin, "Add": add, "Settings": settings,
		"Repeats": []string{"daily", "weekly"}, "Weekdays": taskWeekdays, "Parts": routineParts, "MaxStars": task.MaxStars,
		"Error": st.Error, "ErrorField": st.Field, "SyncStale": s.syncStale(p.household.ID),
		"DeleteExplain": s.i18n.Format(lang, i18n.Msg("routine.tpl.delete.explain", "name", t.Name)),
		"DeleteConfirm": s.i18n.Format(lang, i18n.Msg("routine.tpl.delete.confirm", "name", t.Name)),
	}
	s.render(w, r, status, "routine_template.html", data)
}

func (s *server) handleRoutineTemplatePage(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.renderTemplatePage(w, r, p, http.StatusOK, tplPageState{})
	}
}

// templateFailure turns a store error of a form on a template page into the state to show again.
func templateFailure(err error, st tplPageState, nameField string) (tplPageState, bool) {
	var bad *task.InvalidError
	var exists *store.TemplateExistsError
	switch {
	case errors.As(err, &exists):
		st.Field, st.Error = nameField, "routine.tpl.error.name_taken"
	case errors.Is(err, store.ErrInvalid) && errors.As(err, &bad):
		st.Field = bad.Field
		switch bad.Field {
		case "name":
			st.Field, st.Error = nameField, "routine.tpl.error.name"
		case "part":
			st.Error = routineErrorKey("part")
		default:
			st.Error = taskErrorKey(bad.Field)
		}
	case errors.Is(err, store.ErrInvalid):
		st.Field, st.Error = "title", "tasks.error.invalid"
	default:
		return st, false
	}
	return st, true
}

// stepFromForm reads a step as the forms of a template send it.
func stepFromForm(r *http.Request) (task.TemplateStep, map[string]any) {
	typed := map[string]any{"Title": r.PostFormValue("title"), "Emoji": r.PostFormValue("emoji"), "Stars": r.PostFormValue("stars"),
		"Repeat": r.PostFormValue("repeat"), "Weekdays": r.PostForm["weekdays"]}
	st := task.TemplateStep{Title: r.PostFormValue("title"), Emoji: r.PostFormValue("emoji"), Stars: 1, Repeat: task.Repeat(r.PostFormValue("repeat"))}
	if st.Repeat == task.Weekly {
		st.Weekdays = r.PostForm["weekdays"]
	}
	if v := strings.TrimSpace(r.PostFormValue("stars")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			n = -1 // refused by validation, with the form kept
		}
		st.Stars = n
	}
	return st, typed
}

// templateRedirect shows the template's page with a success line naming a step or the template.
func (s *server) templateSaved(w http.ResponseWriter, r *http.Request, id, key, name string) {
	s.redirectSavedNamed(w, r, "/routines/templates/"+id, key, name)
}

func (s *server) handleTemplateAddStep(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	id := r.PathValue("id")
	t, err := s.ownTemplate(p.household.ID, id)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, errShippedTemplate) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "routine template", err)
		return
	}
	step, typed := stepFromForm(r)
	if _, err := s.db.AddTemplateStep(p.household.ID, t.ID, step); err == nil {
		s.templateSaved(w, r, t.ID, "flash.rtpl_step_added", strings.TrimSpace(step.Title))
		return
	} else if st, handled := templateFailure(err, tplPageState{Add: typed}, "title"); handled {
		s.renderTemplatePage(w, r, p, http.StatusUnprocessableEntity, st)
	} else {
		s.fail(w, "add template step", err)
	}
}

func (s *server) handleTemplateSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	t, err := s.ownTemplate(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, errShippedTemplate) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "routine template", err)
		return
	}
	name, part := strings.TrimSpace(r.PostFormValue("tname")), task.PartOfDay(r.PostFormValue("part"))
	err = s.db.UpdateRoutineTemplate(p.household.ID, t.ID, name, part)
	if err == nil {
		s.templateSaved(w, r, t.ID, "flash.rtpl_saved", name)
		return
	}
	st, handled := templateFailure(err, tplPageState{Settings: map[string]any{"Name": name, "Part": string(part)}}, "tname")
	if !handled {
		s.fail(w, "update routine template", err)
		return
	}
	s.renderTemplatePage(w, r, p, http.StatusUnprocessableEntity, st)
}

func (s *server) handleTemplateMove(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	t, err := s.ownTemplate(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, errShippedTemplate) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "routine template", err)
		return
	}
	at := -1
	for i, st := range t.Steps {
		if st.ID == r.PostFormValue("step") {
			at = i
		}
	}
	to := -1
	switch r.PostFormValue("direction") {
	case "up":
		to = at - 1
	case "down":
		to = at + 1
	}
	if at < 0 || to < 0 || to >= len(t.Steps) {
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
		return
	}
	order := make([]string, len(t.Steps))
	for i, st := range t.Steps {
		order[i] = st.ID
	}
	order[at], order[to] = order[to], order[at]
	switch err := s.db.SetTemplateOrder(p.household.ID, t.ID, order); {
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
	case err != nil:
		s.fail(w, "reorder routine template", err)
	default:
		s.redirectSavedFields(w, r, "/routines/templates/"+t.ID, "flash.rtpl_moved", t.Steps[at].Title, strconv.Itoa(to+1), strconv.Itoa(len(t.Steps)))
	}
}

func (s *server) handleRoutineTemplateDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	t, err := s.ownTemplate(p.household.ID, r.PathValue("id"))
	if err == nil {
		err = s.db.DeleteRoutineTemplate(p.household.ID, t.ID)
	}
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, errShippedTemplate):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, "delete routine template", err)
	default:
		s.redirectSavedNamed(w, r, "/routines/templates", "flash.rtpl_deleted", t.Name)
	}
}

// templateStep finds the step of the path in the template of the path.
func (s *server) templateStep(w http.ResponseWriter, r *http.Request, p principal) (task.RoutineTemplate, task.TemplateStep, int, bool) {
	t, err := s.ownTemplate(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, errShippedTemplate) {
		http.NotFound(w, r)
		return t, task.TemplateStep{}, 0, false
	} else if err != nil {
		s.fail(w, "routine template", err)
		return t, task.TemplateStep{}, 0, false
	}
	for i, st := range t.Steps {
		if st.ID == r.PathValue("step") {
			return t, st, i, true
		}
	}
	http.NotFound(w, r)
	return t, task.TemplateStep{}, 0, false
}

func (s *server) renderTemplateStep(w http.ResponseWriter, r *http.Request, p principal, status int, t task.RoutineTemplate, step task.TemplateStep, at int, form map[string]any, errKey, errField string) {
	lang := s.langOf(r)
	if form == nil {
		form = map[string]any{"Title": step.Title, "Emoji": step.Emoji, "Stars": strconv.Itoa(step.Stars), "Repeat": string(step.Repeat), "Weekdays": step.Weekdays}
	}
	data := map[string]any{
		"Person": p.person, "Tpl": t, "Step": step, "Form": form, "Repeats": []string{"daily", "weekly"}, "Weekdays": taskWeekdays, "MaxStars": task.MaxStars, "Last": len(t.Steps) == 1,
		"BackHref": "/routines/templates/" + t.ID, "BackText": t.Name,
		"Lead":    s.i18n.Format(lang, i18n.Msg("routine.tpl.step.lead", "nr", strconv.Itoa(at+1), "total", strconv.Itoa(len(t.Steps)), "name", t.Name)),
		"Explain": s.i18n.Format(lang, i18n.Msg("routine.tpl.step.remove.explain", "title", step.Title)),
		"Confirm": s.i18n.Format(lang, i18n.Msg("routine.tpl.step.remove.confirm", "title", step.Title)),
		"Error":   errKey, "ErrorField": errField, "SyncStale": s.syncStale(p.household.ID),
	}
	s.render(w, r, status, "routine_template_step.html", data)
}

func (s *server) handleTemplateStepPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if t, step, at, ok := s.templateStep(w, r, p); ok {
		s.renderTemplateStep(w, r, p, http.StatusOK, t, step, at, nil, "", "")
	}
}

func (s *server) handleTemplateStepSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	t, old, at, ok := s.templateStep(w, r, p)
	if !ok {
		return
	}
	step, typed := stepFromForm(r)
	step.ID = old.ID
	err := s.db.UpdateTemplateStep(p.household.ID, t.ID, step)
	if err == nil {
		s.templateSaved(w, r, t.ID, "flash.rtpl_step_saved", strings.TrimSpace(step.Title))
		return
	}
	st, handled := templateFailure(err, tplPageState{}, "title")
	if !handled {
		s.fail(w, "update template step", err)
		return
	}
	s.renderTemplateStep(w, r, p, http.StatusUnprocessableEntity, t, old, at, typed, st.Error, st.Field)
}

func (s *server) handleTemplateStepDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	t, step, _, ok := s.templateStep(w, r, p)
	if !ok {
		return
	}
	if err := s.db.DeleteTemplateStep(p.household.ID, t.ID, step.ID); errors.Is(err, store.ErrInvalid) {
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
		return
	} else if err != nil {
		s.fail(w, "delete template step", err)
		return
	}
	s.templateSaved(w, r, t.ID, "flash.rtpl_step_removed", step.Title)
}

// saveSuggestion is a free name next to a taken one: "Evening routine of Mia, 2".
func saveSuggestion(taken map[string]bool, name string) string {
	for n := 2; ; n++ {
		suffix := ", " + strconv.Itoa(n)
		base := name
		if over := utf8.RuneCountInString(base) + utf8.RuneCountInString(suffix) - task.MaxRoutineName; over > 0 {
			base = string([]rune(base)[:utf8.RuneCountInString(base)-over])
		}
		if c := base + suffix; !taken[strings.ToLower(c)] {
			return c
		}
	}
}

func (s *server) handleRoutineSaveTemplate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	id := r.PathValue("id")
	if _, err := s.db.Routine(p.household.ID, id); errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "routine", err)
		return
	}
	lang := s.langOf(r)
	name := strings.TrimSpace(r.PostFormValue("tname"))
	tpl, err := s.db.SaveRoutineAsTemplate(p.household.ID, id, name)
	if err == nil {
		s.redirectSavedFields(w, r, "/routines/"+id, "flash.routine_template_saved", tpl.Name, tpl.ID)
		return
	}
	st := routinePageState{Save: map[string]any{"Name": name}, Field: "tname"}
	var exists *store.TemplateExistsError
	var bad *task.InvalidError
	switch {
	case errors.As(err, &exists):
		own, _ := s.db.RoutineTemplates(p.household.ID)
		taken := map[string]bool{}
		for _, t := range own {
			taken[strings.ToLower(t.Name)] = true
		}
		st.Error = "routine.tpl.error.name_taken"
		st.ErrorText = s.i18n.Format(lang, i18n.Msg(st.Error, "name", name, "suggestion", saveSuggestion(taken, name)))
	case errors.Is(err, store.ErrInvalid) && errors.As(err, &bad) && bad.Field == "name":
		st.Error = "routine.tpl.error.name"
	case errors.Is(err, store.ErrInvalid):
		st.Error = "routine.tpl.save.empty"
	default:
		s.fail(w, "save routine as template", err)
		return
	}
	s.renderRoutine(w, r, p, http.StatusUnprocessableEntity, st, "")
}

// templateSavedHTML is the success line after saving a template: it names the
// template and leads straight to using it. The values travel in the box's
// memory; the line is built from them with everything escaped.
func (s *server) templateSavedHTML(lang, token string) template.HTML {
	v, ok := s.fieldValues("flash.routine_template_saved", token)
	if !ok {
		return ""
	}
	line := html.EscapeString(s.i18n.Format(lang, i18n.Msg("flash.routine_template_saved", "name", v["name"], "use", "\x00")))
	link := `<a href="/routines/new?template=` + html.EscapeString(v["id"]) + `">` + html.EscapeString(s.i18n.T(lang, "routine.tpl.use")) + `</a>`
	return template.HTML(strings.Replace(line, "\x00", link, 1))
}

// saveBox is what the routine page needs to offer "Save as template".
type saveBox struct {
	Name    string
	Count   int
	Tasks   string // "the 3 tasks"
	PartAdv string
	LeftOut string // the notice naming the once-only tasks that stay out, or empty
}

func (s *server) saveBoxOf(lang string, p principal, rt task.Routine, owner string, steps []routineStep) saveBox {
	b := saveBox{PartAdv: s.partWord(lang, string(rt.Part))}
	var out []string
	for _, st := range steps {
		if st.Repeat == string(task.Once) {
			title := st.Title
			if st.Emoji != "" {
				title = st.Emoji + " " + title
			}
			out = append(out, s.i18n.Format(lang, i18n.Msg("routine.tpl.left_out.item", "title", title, "date", st.Date)))
			continue
		}
		b.Count++
	}
	b.Tasks = s.tasksPhrase(lang, "routine.tpl.the_tasks", b.Count)
	if len(out) > 0 {
		key := "routine.tpl.left_out.many"
		if len(out) == 1 {
			key = "routine.tpl.left_out.one"
		}
		b.LeftOut = s.i18n.Format(lang, i18n.Msg(key, "list", strings.Join(out, ", ")))
	}
	b.Name = s.defaultTemplateName(lang, rt.Name, owner)
	return b
}

// appliedFlash redirects to a routine made from a template and says what
// happened: the routine is the person's own now and keeps no link.
func (s *server) appliedFlash(w http.ResponseWriter, r *http.Request, p principal, lang string, tpl task.RoutineTemplate, rt task.Routine, person string) {
	count, _ := s.db.RoutineTasks(p.household.ID, rt.ID)
	tasks := s.tasksPhrase(lang, "routine.tpl.with", len(count))
	if !tpl.Shipped && tpl.OriginPerson != "" {
		s.redirectSavedFields(w, r, "/routines/"+rt.ID, "flash.routine_applied_own", possessive(lang, person), rt.Name, person, tasks, tpl.Name, possessive(lang, tpl.OriginPerson))
		return
	}
	s.redirectSavedFields(w, r, "/routines/"+rt.ID, "flash.routine_applied", possessive(lang, person), rt.Name, person, tasks, tpl.Name)
}
