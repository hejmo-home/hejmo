package server

import (
	"errors"
	"html"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

// routineParts are the parts of the day a routine can have, in the order of a day.
var routineParts = []string{string(task.Morning), string(task.AfterSchool), string(task.Evening)}

// routineStep is a task of a routine as the tasks page and the routine page list it.
type routineStep struct {
	taskView
	Nr             int
	CanUp, CanDown bool
}

// routineCard is a routine with its steps on the tasks page.
type routineCard struct {
	ID, Name, Part string
	Done, Total    int // of the steps due today
	Steps          []routineStep
}

// routineSteps numbers the tasks of a routine, in their order.
func routineSteps(f store.DateFormat, list []task.Task, today string, doneToday map[string]bool) []routineStep {
	out := make([]routineStep, 0, len(list))
	for i, t := range list {
		out = append(out, routineStep{taskView: newTaskView(f, t, today, doneToday[t.ID]), Nr: i + 1, CanUp: i > 0, CanDown: i < len(list)-1})
	}
	return out
}

// progressOfSteps counts the steps due today and the ones done.
func progressOfSteps(steps []routineStep) (done, total int) {
	for _, st := range steps {
		if st.Due {
			total++
			if st.Done {
				done++
			}
		}
	}
	return done, total
}

// partWord is a part of the day as an adverb: "morgens", "nach der Schule".
func (s *server) partWord(lang, part string) string {
	return s.i18n.T(lang, "routine.part_adv."+part)
}

// defaultRoutineName is the name a routine gets when none is typed.
func (s *server) defaultRoutineName(lang, part string) string {
	return s.i18n.T(lang, "routine.default_name."+part)
}

// isDefaultRoutineName reports whether a name is the stock name of one of the
// parts in this language. The form shows the stock name of the chosen part;
// when another part is chosen without touching the name, the stock name of the
// chosen part is used instead, so no script is needed.
func (s *server) isDefaultRoutineName(lang, name string) bool {
	for _, part := range routineParts {
		if name == s.defaultRoutineName(lang, part) {
			return true
		}
	}
	return false
}

// routineErrorKey is the text for an invalid routine field.
func routineErrorKey(field string) string {
	switch field {
	case "name", "person", "part":
		return "routine.error." + field
	}
	return "routine.error.part"
}

// existsHTML is the error "Mia already has a routine for the morning" with a
// link to the routine in the way. HTML comes from the locales with the names
// escaped.
func (s *server) existsHTML(lang, personName string, exists *store.RoutineExistsError) template.HTML {
	open := `<a href="/routines/` + html.EscapeString(exists.Existing.ID) + `">` + html.EscapeString(s.i18n.T(lang, "routine.error.exists.open")) + `</a>`
	text := html.EscapeString(s.i18n.T(lang, "routine.error.exists"))
	r := strings.NewReplacer(
		"{person}", html.EscapeString(personName),
		"{part}", html.EscapeString(s.partWord(lang, string(exists.Existing.Part))),
		"{routine}", html.EscapeString(exists.Existing.Name),
		"{open}", open)
	// The locale text itself is escaped first, so only the placeholders carry markup.
	return template.HTML(r.Replace(strings.NewReplacer("&#34;", `"`, "&#39;", "'").Replace(text)))
}

func personNamedIn(people []store.Person, id string) (store.Person, bool) {
	for _, p := range people {
		if p.ID == id {
			return p, true
		}
	}
	return store.Person{}, false
}

// renderRoutineNew renders the form for a new routine; form holds what was typed.
func (s *server) renderRoutineNew(w http.ResponseWriter, r *http.Request, p principal, status int, form map[string]any) {
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	lang := s.langOf(r)
	if form == nil {
		form = map[string]any{}
	}
	if _, ok := form["Part"]; !ok {
		form["Part"] = string(task.Morning)
	}
	if _, ok := form["Name"]; !ok {
		form["Name"] = s.defaultRoutineName(lang, form["Part"].(string))
	}
	data := map[string]any{"Person": p.person, "People": chipsOf(people), "Form": form, "Parts": routineParts,
		"Error": form["Error"], "ErrorField": form["ErrorField"], "ErrorHTML": form["ErrorHTML"], "SyncStale": s.syncStale(p.household.ID)}
	s.render(w, r, status, "routine_new.html", data)
}

func (s *server) handleRoutineNewPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	form := map[string]any{}
	if who := r.URL.Query().Get("person"); who != "" {
		form["Person"] = who
	}
	s.renderRoutineNew(w, r, p, http.StatusOK, form)
}

func (s *server) handleRoutineCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	lang := s.langOf(r)
	in := task.Routine{PersonID: r.PostFormValue("person"), Name: strings.TrimSpace(r.PostFormValue("name")), Part: task.PartOfDay(r.PostFormValue("part"))}
	typed := in.Name
	if in.Name == "" || s.isDefaultRoutineName(lang, in.Name) {
		if in.Part == task.Morning || in.Part == task.AfterSchool || in.Part == task.Evening {
			in.Name = s.defaultRoutineName(lang, string(in.Part))
		}
	}
	rt, err := s.db.CreateRoutine(p.household.ID, in)
	if err == nil {
		s.redirectSavedNamed(w, r, "/routines/"+rt.ID, "flash.routine_created", rt.Name)
		return
	}
	form := map[string]any{"Person": in.PersonID, "Part": string(in.Part), "Name": typed}
	var exists *store.RoutineExistsError
	var bad *task.InvalidError
	switch {
	case errors.As(err, &exists):
		people, _ := s.db.People(p.household.ID)
		who, _ := personNamedIn(people, in.PersonID)
		form["ErrorField"], form["ErrorHTML"], form["Error"] = "part", s.existsHTML(lang, who.Name, exists), "routine.error.part"
	case errors.Is(err, store.ErrInvalid) && errors.As(err, &bad):
		form["ErrorField"], form["Error"] = bad.Field, routineErrorKey(bad.Field)
	case errors.Is(err, store.ErrInvalid):
		form["ErrorField"], form["Error"] = "part", "routine.error.part"
	default:
		s.fail(w, "create routine", err)
		return
	}
	if form["Name"] == "" {
		delete(form, "Name") // shows the stock name of the part again
	}
	s.renderRoutineNew(w, r, p, http.StatusUnprocessableEntity, form)
}

// routinePageState is what a refused form on the routine page needs to show again.
type routinePageState struct {
	Add      map[string]any // what was typed in "Add task"
	Settings map[string]any // what was typed under "Name and part of the day"
	Error    string         // locale key
	Field    string
	HTML     template.HTML // an error that carries a link
}

// renderRoutine renders the routine page of the routine in the path.
func (s *server) renderRoutine(w http.ResponseWriter, r *http.Request, p principal, status int, st routinePageState, flashName string) {
	rt, err := s.db.Routine(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "routine", err)
		return
	}
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	owner, _ := personNamedIn(people, rt.PersonID)
	var chip personChip
	for _, c := range chipsOf(people) {
		if c.ID == rt.PersonID {
			chip = c
		}
	}
	list, err := s.db.RoutineTasks(p.household.ID, rt.ID)
	if err != nil {
		s.fail(w, "routine tasks", err)
		return
	}
	today := s.today(p)
	doneToday, err := s.db.TasksDoneOn(p.household.ID, today)
	if err != nil {
		s.fail(w, "task state", err)
		return
	}
	steps := routineSteps(p.household.DateFormat, list, today, doneToday)
	all, err := s.db.Tasks(p.household.ID)
	if err != nil {
		s.fail(w, "tasks", err)
		return
	}
	var free []taskView
	for _, t := range all {
		if t.PersonID == rt.PersonID && t.RoutineID == "" {
			free = append(free, newTaskView(p.household.DateFormat, t, today, doneToday[t.ID]))
		}
	}
	done, total := progressOfSteps(steps)
	add := st.Add
	if add == nil {
		add = map[string]any{"Source": "new", "Repeat": "daily", "Date": today, "Weekdays": []string{}, "Stars": strconv.Itoa(task.DefaultStars(owner.Role == store.RoleChild))}
	}
	settings := st.Settings
	if settings == nil {
		settings = map[string]any{"Name": rt.Name, "Part": string(rt.Part)}
	}
	data := map[string]any{
		"Person": p.person, "Routine": rt, "Owner": chip, "OwnerName": owner.Name, "Steps": steps, "Free": free, "Done": done, "Total": total,
		"Add": add, "Settings": settings, "Repeats": []string{"daily", "weekly", "once"}, "Weekdays": taskWeekdays, "Parts": routineParts, "MaxStars": task.MaxStars,
		"Error": st.Error, "ErrorField": st.Field, "ErrorHTML": st.HTML, "SyncStale": s.syncStale(p.household.ID),
		"DissolveAny": rt.Part == task.AfterSchool, "TaskTitles": titlesOf(steps),
	}
	if flashName != "" {
		data["SuccessText"] = flashName
	}
	s.render(w, r, status, "routine.html", data)
}

func titlesOf(steps []routineStep) []string {
	out := make([]string, 0, len(steps))
	for _, st := range steps {
		title := st.Title
		if st.Emoji != "" {
			title = st.Emoji + " " + title
		}
		out = append(out, title)
	}
	return out
}

func (s *server) handleRoutinePage(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.renderRoutine(w, r, p, http.StatusOK, routinePageState{}, "")
	}
}

// routineFailure turns a store error of a form on the routine page into the state to show again.
func (s *server) routineFailure(lang, personName string, err error, st routinePageState, fromTask bool) (routinePageState, bool) {
	var exists *store.RoutineExistsError
	var bad *task.InvalidError
	switch {
	case errors.As(err, &exists):
		st.Field, st.Error, st.HTML = "part", "routine.error.part", s.existsHTML(lang, personName, exists)
	case errors.Is(err, store.ErrInvalid) && errors.As(err, &bad):
		st.Field = bad.Field
		if fromTask {
			st.Error = taskErrorKey(bad.Field)
		} else {
			st.Error = routineErrorKey(bad.Field)
		}
	case errors.Is(err, store.ErrInvalid), errors.Is(err, store.ErrConflict):
		st.Field, st.Error = "task", "routine.error.task"
	default:
		return st, false
	}
	return st, true
}

func (s *server) handleRoutineAddTask(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	id := r.PathValue("id")
	rt, err := s.db.Routine(p.household.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "routine", err)
		return
	}
	people, _ := s.db.People(p.household.ID)
	owner, _ := personNamedIn(people, rt.PersonID)
	lang := s.langOf(r)
	typed := map[string]any{"Source": r.PostFormValue("source"), "Title": r.PostFormValue("title"), "Emoji": r.PostFormValue("emoji"), "Stars": r.PostFormValue("stars"),
		"Repeat": r.PostFormValue("repeat"), "Date": r.PostFormValue("date"), "Weekdays": r.PostForm["weekdays"], "Task": r.PostFormValue("task")}
	if typed["Source"] != "existing" {
		typed["Source"] = "new"
	}
	var name string
	if typed["Source"] == "existing" {
		var t task.Task
		if t, err = s.db.Task(p.household.ID, r.PostFormValue("task")); err == nil {
			name = t.Title
			err = s.db.AddTaskToRoutine(p.household.ID, id, t.ID)
		} else if errors.Is(err, store.ErrNotFound) {
			err = store.ErrInvalid
		}
	} else {
		in := task.Task{PersonID: rt.PersonID, RoutineID: id, Title: r.PostFormValue("title"), Emoji: r.PostFormValue("emoji"),
			Repeat: task.Repeat(r.PostFormValue("repeat")), Date: r.PostFormValue("date")}
		if in.Repeat == task.Weekly {
			in.Weekdays = r.PostForm["weekdays"]
		}
		var stars *int
		if v := strings.TrimSpace(r.PostFormValue("stars")); v != "" {
			n, convErr := strconv.Atoi(v)
			if convErr != nil {
				n = -1 // refused by validation, with the form kept
			}
			stars = &n
		}
		var t task.Task
		t, err = s.createTask(p, in, stars)
		name = t.Title
	}
	if err == nil {
		s.redirectSavedNamed(w, r, "/routines/"+id, "flash.routine_task_added", name)
		return
	}
	st, handled := s.routineFailure(lang, owner.Name, err, routinePageState{Add: typed}, typed["Source"] == "new")
	if !handled {
		s.fail(w, "add task to routine", err)
		return
	}
	s.renderRoutine(w, r, p, http.StatusUnprocessableEntity, st, "")
}

func (s *server) handleRoutineMove(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	id := r.PathValue("id")
	list, err := s.db.RoutineTasks(p.household.ID, id)
	if err != nil {
		s.fail(w, "routine tasks", err)
		return
	}
	if _, err := s.db.Routine(p.household.ID, id); errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "routine", err)
		return
	}
	at := -1
	for i, t := range list {
		if t.ID == r.PostFormValue("task") {
			at = i
		}
	}
	to := at
	switch r.PostFormValue("direction") {
	case "up":
		to = at - 1
	case "down":
		to = at + 1
	default:
		to = -1
	}
	if at < 0 || to < 0 || to >= len(list) {
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
		return
	}
	order := make([]string, len(list))
	for i, t := range list {
		order[i] = t.ID
	}
	order[at], order[to] = order[to], order[at]
	switch err := s.db.SetRoutineOrder(p.household.ID, id, order); {
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
	case err != nil:
		s.fail(w, "reorder routine", err)
	default:
		text := list[at].Title + "\x1f" + strconv.Itoa(to+1) + "\x1f" + strconv.Itoa(len(list))
		s.setFlash(w, r, "flash.routine_moved:"+s.flashTexts.put(text, s.now()))
		http.Redirect(w, r, "/routines/"+id, http.StatusSeeOther)
	}
}

// routineMovedText is the success line after a step moved: it names the step
// and its new place. The step's title travels in the box's memory, not in the cookie.
func (s *server) routineMovedText(lang, token string) string {
	text, ok := s.flashTexts.take(token, s.now())
	parts := strings.Split(text, "\x1f")
	if !ok || len(parts) != 3 {
		return ""
	}
	return s.i18n.Format(lang, i18n.Msg("flash.routine_moved", "title", parts[0], "place", parts[1], "total", parts[2]))
}

func (s *server) handleRoutineSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	id := r.PathValue("id")
	rt, err := s.db.Routine(p.household.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "routine", err)
		return
	}
	name, part := strings.TrimSpace(r.PostFormValue("name")), task.PartOfDay(r.PostFormValue("part"))
	err = s.db.UpdateRoutine(p.household.ID, id, name, part)
	if err == nil {
		s.redirectSavedNamed(w, r, "/routines/"+id, "flash.routine_saved", name)
		return
	}
	people, _ := s.db.People(p.household.ID)
	owner, _ := personNamedIn(people, rt.PersonID)
	st, handled := s.routineFailure(s.langOf(r), owner.Name, err, routinePageState{Settings: map[string]any{"Name": name, "Part": string(part)}}, false)
	if !handled {
		s.fail(w, "update routine", err)
		return
	}
	s.renderRoutine(w, r, p, http.StatusUnprocessableEntity, st, "")
}

func (s *server) handleRoutineDissolve(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	rt, err := s.db.Routine(p.household.ID, id)
	if err == nil {
		err = s.db.DissolveRoutine(p.household.ID, id)
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, "dissolve routine", err)
	default:
		s.redirectSavedNamed(w, r, "/tasks", "flash.routine_dissolved", rt.Name)
	}
}

// handleTaskLeaveRoutine takes a task out of its routine, from the task's detail page.
func (s *server) handleTaskLeaveRoutine(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	t, err := s.db.Task(p.household.ID, id)
	if err == nil {
		err = s.db.RemoveTaskFromRoutine(p.household.ID, id)
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
	case err != nil:
		s.fail(w, "take task out of routine", err)
	default:
		s.redirectSavedNamed(w, r, "/tasks/"+id, "flash.routine_task_out", t.Title)
	}
}

// childParts lays out a child's tasks for today as the display does: parts of
// the day with their routines. It is what a child's tasks page shows.
func childParts(due []store.DayTask, routines []task.Routine, personID string) ([]familyview.TaskPart, familyview.Progress) {
	var tasks []familyview.Task
	for _, t := range due {
		if t.PersonID == personID {
			tasks = append(tasks, viewTask(t))
		}
	}
	v := familyview.PersonalView{Tasks: tasks, Routines: viewRoutines(routines, personID)}
	return v.Parts(), familyview.ProgressOf(tasks)
}
