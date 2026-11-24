package server

import (
	"errors"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

type taskView struct {
	ID, Title, Emoji, Repeat, Date, Part string
	Weekdays                             []string
	Due, Done                            bool
	Stars                                int
	Subs                                 []subView // in their order, with their state today
	SubsDone                             int
}

// subView is a subtask as the pages show it.
type subView struct {
	ID, Title, Emoji string
	Done             bool
}

// withSubs adds a task's subtasks and how many are ticked off.
func (v taskView) withSubs(list []store.DaySubtask) taskView {
	for _, k := range list {
		v.Subs = append(v.Subs, subView{ID: k.ID, Title: k.Title, Emoji: k.Emoji, Done: k.Done})
		if k.Done {
			v.SubsDone++
		}
	}
	return v
}

// starCard is a person's star account on the tasks page.
type starCard struct {
	ID, Name, Color, Shape string
	Slot                   int
	Balance                int
	Entries                []starEntryView
	// Below says the balance is below zero and why (ticket 292).
	Below string
	// HasRewards: at least one reward is meant for the person, so the card has a goal line.
	HasRewards bool
	Goal       *starGoalLine // nil: no goal yet
	Poss       string        // the person's name in the possessive
}

// starGoalLine is the savings goal of a star card (ticket 293).
type starGoalLine struct {
	Name, Symbol, Picture string
	Have, Price, Left     int
	Reached               bool
}

type starEntryView struct {
	Delta, Label, By, When string
	ByRemoved              bool // the booker has been removed; By is their last name
	Redeemed               bool // a redemption: Label is "Eingelöst: symbol name" and By says who confirmed or redeemed it
}

// starLogLines is how many star log entries per person the tasks page shows.
const starLogLines = 20

type personTasks struct {
	ID, Name, Color string
	Empty           bool          // a child without tasks: the page suggests a routine template
	Routines        []routineCard // the routines first (ADR 0018), then the single tasks
	Tasks           []taskView
}

var taskWeekdays = []string{"MO", "TU", "WE", "TH", "FR", "SA", "SU"}

func newTaskView(f store.DateFormat, t task.Task, today string, done bool) taskView {
	v := taskView{ID: t.ID, Title: t.Title, Emoji: t.Emoji, Repeat: string(t.Repeat), Weekdays: t.Weekdays, Part: string(t.PartOfDay), Due: t.DueOn(today), Done: done, Stars: t.Stars}
	if d, err := time.Parse(task.DateLayout, t.Date); err == nil {
		v.Date = datefmt.Date(f, d)
	}
	return v
}

func (s *server) renderTasks(w http.ResponseWriter, r *http.Request, p principal, status int, form, starForm map[string]any) {
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	list, err := s.db.Tasks(p.household.ID)
	if err != nil {
		s.fail(w, "tasks", err)
		return
	}
	today := s.today(p)
	doneToday, err := s.db.TasksDoneOn(p.household.ID, today)
	if err != nil {
		s.fail(w, "task state", err)
		return
	}
	sums, err := s.db.StarSummaries(p.household.ID)
	if err != nil {
		s.fail(w, "stars", err)
		return
	}
	routines, err := s.db.Routines(p.household.ID)
	if err != nil {
		s.fail(w, "routines", err)
		return
	}
	subs, err := s.db.SubtasksByTask(p.household.ID, today)
	if err != nil {
		s.fail(w, "subtasks", err)
		return
	}
	weeks, err := s.routineWeeks(p.household, today)
	if err != nil {
		s.fail(w, "weeks", err)
		return
	}
	var cards []starCard
	var goals map[string]store.Goal
	var offered []store.Reward
	if p.person.Role != store.RoleChild {
		if goals, err = s.db.Goals(p.household.ID); err != nil {
			s.fail(w, "savings goals", err)
			return
		}
		if offered, err = s.db.Rewards(p.household.ID); err != nil {
			s.fail(w, "rewards", err)
			return
		}
	}
	var groups []personTasks
	choices := chipsOf(people)
	child := p.person.Role == store.RoleChild
	for _, person := range people {
		// A child's personal device shows the child's own tasks only (ADR 0015).
		if child && person.ID != p.person.ID {
			continue
		}
		if !child && sums[person.ID].Shown {
			acc, err := s.db.StarAccount(p.household.ID, person.ID, starLogLines)
			if err != nil {
				s.fail(w, "star log", err)
				return
			}
			card := s.starCardOf(r, p, person, acc)
			card.ID, card.Poss = person.ID, possessive(s.langOf(r), person.Name)
			for _, c := range choices {
				if c.ID == person.ID {
					card.Slot, card.Shape = c.Slot, c.Shape
				}
			}
			for _, rw := range offered {
				if !rw.Archived() && rw.MeantFor(person.ID) {
					card.HasRewards = true
				}
			}
			if g := goals[person.ID]; g.Active() {
				line := &starGoalLine{Name: g.Reward.Name, Symbol: g.Reward.Symbol, Picture: pictureOf(g.Reward), Have: acc.Balance, Price: g.Reward.Price}
				line.Reached, line.Left = acc.Balance >= line.Price, max(0, line.Price-acc.Balance)
				card.Goal = line
			}
			cards = append(cards, card)
		}
		g := personTasks{ID: person.ID, Name: person.Name, Color: person.Color}
		for _, rt := range routines {
			if rt.PersonID != person.ID {
				continue
			}
			var steps []task.Task
			for _, t := range list {
				if t.RoutineID == rt.ID {
					steps = append(steps, t)
				}
			}
			sort.SliceStable(steps, func(i, j int) bool { return steps[i].Position < steps[j].Position })
			card := routineCard{ID: rt.ID, Name: rt.Name, Part: string(rt.Part), Steps: routineSteps(p.household.DateFormat, steps, today, doneToday, subs), Week: weeks[rt.ID]}
			card.Done, card.Total = progressOfSteps(card.Steps)
			g.Routines = append(g.Routines, card)
		}
		sort.SliceStable(g.Routines, func(i, j int) bool {
			return task.PartOfDay(g.Routines[i].Part).Rank() < task.PartOfDay(g.Routines[j].Part).Rank()
		})
		for _, t := range list {
			if t.PersonID != person.ID || t.RoutineID != "" {
				continue
			}
			g.Tasks = append(g.Tasks, newTaskView(p.household.DateFormat, t, today, doneToday[t.ID]).withSubs(subs[t.ID]))
		}
		g.Empty = len(g.Tasks) == 0 && len(g.Routines) == 0 && person.Role == store.RoleChild
		if len(g.Tasks) > 0 || len(g.Routines) > 0 || g.Empty {
			groups = append(groups, g)
		}
	}
	if form == nil {
		form = map[string]any{"Repeat": "daily", "Date": today, "Weekdays": []string{}}
	}
	if starForm == nil {
		starForm = map[string]any{}
	}
	data := map[string]any{
		"Person": p.person, "Groups": groups, "StarCards": cards, "StarForm": starForm, "StarError": starForm["Error"], "People": choices, "Weekdays": taskWeekdays, "Repeats": []string{"once", "daily", "weekly"}, "Form": form, "Child": child, "MaxStars": task.MaxStars,
		"SyncStale": s.syncStale(p.household.ID), "Error": form["Error"], "ErrorField": form["ErrorField"],
		// The success line after saving a goal sits in the stars section (ticket 293).
		"SuccessInline":     strings.HasPrefix(s.pendingFlashKey(r), "flash.goal_"),
		"SuggestedTemplate": s.i18n.T(s.langOf(r), "routine.tpl."+task.Shipped[0].ID),
	}
	if child {
		due, err := s.db.TasksForDay(p.household.ID, today)
		if err != nil {
			s.fail(w, "tasks of the day", err)
			return
		}
		parts, progress := childParts(due, routines, p.person.ID, weeks)
		data["ChildParts"], data["ChildProgress"], data["ChildSlot"] = parts, progress, 0
		for _, c := range choices {
			if c.ID == p.person.ID {
				data["ChildSlot"] = c.Slot
			}
		}
		data["StarsShown"], data["StarsBalance"] = sums[p.person.ID].Shown, sums[p.person.ID].Balance
		// The way to the rewards appears only when something is meant for the child (ticket 291).
		offered, err := s.db.HasRewards(p.household.ID, p.person.ID)
		if err != nil {
			s.fail(w, "rewards", err)
			return
		}
		data["RewardLink"] = offered
		lang := s.langOf(r)
		balance := sums[p.person.ID].Balance
		data["RewardSub"] = template.HTML(html.EscapeString(s.i18n.T(lang, "rwd.link.sub.none")))
		if balance > 0 {
			data["RewardSub"] = formatHTML(s.i18n, lang, "rwd.link.sub", "stars", s.starsWords(lang, balance))
		}
		if goal, err := s.db.Goal(p.household.ID, p.person.ID); err != nil {
			s.fail(w, "savings goal", err)
			return
		} else if goal.Active() {
			// Naming the goal in words (ticket 293).
			if balance >= goal.Reward.Price {
				data["RewardSub"] = formatHTML(s.i18n, lang, "rwd.link.goal.enough", "stars", s.starsWords(lang, balance), "name", goal.Reward.Name)
			} else {
				data["RewardSub"] = formatHTML(s.i18n, lang, "rwd.link.goal.missing", "stars", s.starsWords(lang, balance), "name", goal.Reward.Name, "left", s.starsWords(lang, goal.Reward.Price-balance))
			}
		}
		data["Day"] = today
		asked, err := s.askedLines(p, lang)
		if err != nil {
			s.fail(w, "requests", err)
			return
		}
		data["AskedLines"] = asked
	}
	if !child {
		current, past, lastPast, err := s.pauseRows(s.langOf(r), p, today)
		if err != nil {
			s.fail(w, "pauses", err)
			return
		}
		data["Pauses"], data["PastPauses"], data["LastPause"] = current, past, lastPast
	}
	s.render(w, r, status, "tasks.html", data)
}

func (s *server) handleTasksPage(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.memberPage(w, r); ok {
		s.renderTasks(w, r, p, http.StatusOK, nil, nil)
	}
}

func (s *server) handleTaskAdd(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	in := task.Task{PersonID: r.PostFormValue("person"), Title: r.PostFormValue("title"), Emoji: r.PostFormValue("emoji"),
		Repeat: task.Repeat(r.PostFormValue("repeat")), Date: r.PostFormValue("date"), PartOfDay: task.PartOfDay(r.PostFormValue("part"))}
	if in.Repeat == task.Weekly {
		in.Weekdays = r.PostForm["weekdays"]
	}
	var stars *int
	if v := strings.TrimSpace(r.PostFormValue("stars")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			n = -1 // refused by validation, with the form kept
		}
		stars = &n
	}
	switch added, err := s.createTask(p, in, stars); {
	case err == nil:
		s.redirectSaved(w, r, "/tasks", "flash.task_created:"+added.ID)
	case errors.Is(err, store.ErrInvalid):
		var bad *task.InvalidError
		field := ""
		if errors.As(err, &bad) {
			field = bad.Field
		}
		form := map[string]any{"Error": taskErrorKey(field), "ErrorField": field, "Person": in.PersonID, "Title": in.Title, "Emoji": in.Emoji,
			"Repeat": string(in.Repeat), "Date": in.Date, "Part": string(in.PartOfDay), "Weekdays": in.Weekdays, "Stars": r.PostFormValue("stars")}
		s.renderTasks(w, r, p, http.StatusUnprocessableEntity, form, nil)
	default:
		s.fail(w, "add task", err)
	}
}

func (s *server) handleTaskDone(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	switch _, err := s.tickTask(p, r.PathValue("id"), r.PostFormValue("done") == "1"); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, errNotYourTask):
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
	case err != nil:
		s.fail(w, "tick task", err)
	default:
		s.redirectSaved(w, r, "/tasks", doneKey(r))
	}
}

func (s *server) handleTaskDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	switch err := s.db.DeleteTask(p.household.ID, r.PathValue("id")); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, "delete task", err)
	default:
		s.redirectSaved(w, r, "/tasks", "flash.task_deleted")
	}
}

// doneKey is the success line for ticking a task off or reopening it.
func doneKey(r *http.Request) string {
	if r.PostFormValue("done") == "1" {
		return "flash.task_done"
	}
	return "flash.task_reopened"
}

// taskErrorKey is the text for an invalid task field. A field without its own
// text falls back to the general one.
func taskErrorKey(field string) string {
	switch field {
	case "title", "emoji", "person", "date", "weekdays", "repeat", "part", "stars":
		return "tasks.error." + field
	}
	return "tasks.error.invalid"
}

// starCardOf builds a person's star account for the page: balance and log, newest first.
func (s *server) starCardOf(r *http.Request, p principal, person store.Person, acc store.StarAccount) starCard {
	c := starCard{Name: person.Name, Color: person.Color, Balance: acc.Balance}
	lang := s.langOf(r)
	for _, e := range acc.Entries {
		label := e.Note
		if e.TaskID != "" || e.TaskTitle != "" {
			label = e.TaskTitle
			if e.Delta < 0 {
				label = s.i18n.Format(lang, i18n.Msg("tasks.stars.reopened", "title", e.TaskTitle))
			}
		}
		delta := negStars(e.Delta)
		if e.Delta > 0 {
			delta = "+" + delta
		}
		v := starEntryView{Delta: delta, Label: label, By: e.BookedByName, ByRemoved: e.BookedByRemoved, When: p.dateTime(e.At)}
		if e.RedemptionID != "" {
			v.Redeemed = true
			v.Label = s.i18n.Format(lang, i18n.Msg("rwd.log.redeemed", "symbol", e.RewardSymbol, "name", e.RewardName))
			key := "rwd.log.confirmed"
			if e.Direct {
				key = "rwd.log.direct"
			}
			v.By, v.ByRemoved = s.i18n.Format(lang, i18n.Msg(key, "name", e.BookedByName)), false
		}
		c.Entries = append(c.Entries, v)
	}
	if acc.Balance < 0 {
		task, _, err := s.db.BelowZeroCause(person.ID)
		if err != nil {
			task = ""
		}
		key := "rwd.log.below.short"
		if task != "" {
			key = "rwd.log.below"
		}
		c.Below = s.i18n.Format(lang, i18n.Msg(key, "task", task, "n", strconv.Itoa(-acc.Balance)))
	}
	return c
}

// handleStarBooking books stars by hand for a person: a parent's form on the tasks page.
func (s *server) handleStarBooking(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	person, note, amount := r.PostFormValue("person"), r.PostFormValue("note"), strings.TrimSpace(r.PostFormValue("delta"))
	delta, err := strconv.Atoi(amount)
	if err == nil {
		_, err = s.db.BookStars(p.household.ID, person, delta, note, p.person.ID, s.now())
	} else {
		err = store.ErrInvalid
	}
	switch {
	case err == nil:
		http.Redirect(w, r, "/tasks", http.StatusSeeOther)
	case errors.Is(err, store.ErrInvalid), errors.Is(err, store.ErrNotFound):
		s.renderTasks(w, r, p, http.StatusUnprocessableEntity, nil,
			map[string]any{"Error": "tasks.stars.error.invalid", "Person": person, "Delta": amount, "Note": note})
	default:
		s.fail(w, "book stars", err)
	}
}

// creditedView is what today's tick of a task credited, said in words on the
// detail page. HTML comes from the locales with the person's name escaped.
type creditedView struct {
	Stars int
	HTML  template.HTML
}

// handleTaskPage is the detail page of a task: what it is, its star value and,
// behind a confirmation, deleting it (ADR 0017: destructive actions only on
// detail pages).
func (s *server) handleTaskPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.renderTaskPageWith(w, r, p, http.StatusOK, nil, nil)
}

// renderTaskPage renders the detail page of the task in the path. After a
// refused star value it shows what was typed (typed) and the error at the field.
func (s *server) renderTaskPage(w http.ResponseWriter, r *http.Request, p principal, status int, typed, errKey string) {
	s.renderTaskPageWith(w, r, p, status, &starsForm{Typed: typed, ErrKey: errKey}, nil)
}

// starsForm is a star value that was refused: what was typed and the text.
type starsForm struct{ Typed, ErrKey string }

// renderTaskPageWith renders the task page with, if given, a refused star
// value or a refused subtask form and its error at the field.
func (s *server) renderTaskPageWith(w http.ResponseWriter, r *http.Request, p principal, status int, stars *starsForm, sub *subtaskForm) {
	t, err := s.db.Task(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "task", err)
		return
	}
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	var owner personChip
	for _, c := range chipsOf(people) {
		if c.ID == t.PersonID {
			owner = c
		}
	}
	today := s.today(p)
	doneToday, err := s.db.TasksDoneOn(p.household.ID, today)
	if err != nil {
		s.fail(w, "task state", err)
		return
	}
	data := map[string]any{
		"Person": p.person, "Task": newTaskView(p.household.DateFormat, t, today, doneToday[t.ID]), "Owner": owner, "SyncStale": s.syncStale(p.household.ID),
		"MaxStars": task.MaxStars, "StarsValue": strconv.Itoa(t.Stars),
	}
	if stars != nil && stars.ErrKey != "" {
		data["StarsValue"], data["Error"], data["ErrorField"] = stars.Typed, stars.ErrKey, "stars"
	}
	lang := s.langOf(r)
	data["SubForm"] = map[string]string{}
	if sub != nil {
		data["SubForm"] = map[string]string{"Title": sub.Title, "Emoji": sub.Emoji}
		data["SubEditID"] = sub.ID
		data["Error"], data["ErrorField"] = sub.Error, sub.Field
	}
	if err := s.subtaskCard(data, p, t, owner.Name, lang); err != nil {
		s.fail(w, "subtasks", err)
		return
	}
	if t.RoutineID != "" {
		rt, err := s.db.Routine(p.household.ID, t.RoutineID)
		if err != nil {
			s.fail(w, "routine", err)
			return
		}
		list, err := s.db.RoutineTasks(p.household.ID, rt.ID)
		if err != nil {
			s.fail(w, "routine tasks", err)
			return
		}
		data["Routine"], data["RoutinePlace"], data["RoutineTotal"] = rt, t.Position, len(list)
		lang := s.langOf(r)
		link := `<a href="/routines/` + html.EscapeString(rt.ID) + `">` + html.EscapeString(rt.Name) + `</a>`
		data["RoutinePart"] = string(rt.Part)
		data["RoutineIn"] = template.HTML(strings.NewReplacer("{routine}", link, "{person}", html.EscapeString(owner.Name),
			"{place}", strconv.Itoa(t.Position), "{total}", strconv.Itoa(len(list))).Replace(html.EscapeString(s.i18n.T(lang, "routine.in"))))
		key := "routine.out.explain"
		if rt.Part == task.AfterSchool {
			key += ".any"
		}
		data["RoutineOut"] = s.i18n.Format(lang, i18n.Msg(key, "title", t.Title, "person", owner.Name, "part", s.partWord(lang, string(rt.Part))))
	}
	if credited, done, err := s.db.TaskDoneStars(t.ID, today); err != nil {
		s.fail(w, "credited stars", err)
		return
	} else if done {
		cn := s.creditedNotice(s.langOf(r), owner.Name, credited)
		if bal, err := s.balanceOfPerson(p, owner.ID); err == nil && !p.household.BelowZero && credited > 0 && bal < credited {
			// With the switch off, reopening deducts at most what the person has (ticket 292).
			cn.HTML += template.HTML(" " + s.i18n.Format(s.langOf(r), i18n.Msg("tasks.stars.credited.floor", "balance", strconv.Itoa(max(bal, 0)))))
		}
		data["Credited"] = cn
	}
	s.render(w, r, status, "task.html", data)
}

// creditedNotice says what today's tick credited and what reopening deducts.
func (s *server) creditedNotice(lang, owner string, stars int) creditedView {
	name := html.EscapeString(owner)
	var text string
	switch stars {
	case 0:
		text = s.i18n.T(lang, "tasks.stars.credited.zero")
	case 1:
		text = fmt.Sprintf(s.i18n.T(lang, "tasks.stars.credited.one"), name)
	default:
		text = fmt.Sprintf(s.i18n.T(lang, "tasks.stars.credited.other"), name, stars)
	}
	return creditedView{Stars: stars, HTML: template.HTML(text)}
}

// starsSavedKey is the text of the success line after a changed star value:
// no stars, one, or several.
func starsSavedKey(n int) string {
	switch n {
	case 0:
		return "tasks.stars.saved.zero"
	case 1:
		return "tasks.stars.saved.one"
	}
	return "tasks.stars.saved.other"
}

// handleTaskStars changes a task's star value from its detail page. The value
// counts from the next tick; credited ticks keep theirs.
func (s *server) handleTaskStars(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	typed := strings.TrimSpace(r.PostFormValue("stars"))
	n, convErr := strconv.Atoi(typed)
	id := r.PathValue("id")
	err := convErr
	if err == nil {
		err = s.db.SetTaskStars(p.household.ID, id, n)
	} else {
		err = store.ErrInvalid // not a whole number: refused with the field kept
	}
	switch {
	case err == nil:
		s.redirectSaved(w, r, "/tasks/"+id, starsSavedKey(n)+":"+strconv.Itoa(n))
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrInvalid):
		s.renderTaskPage(w, r, p, http.StatusUnprocessableEntity, typed, "tasks.error.stars")
	default:
		s.fail(w, "set task stars", err)
	}
}

// askedLines names each open request of a child on their tasks page, for the way to the rewards.
func (s *server) askedLines(p principal, lang string) ([]string, error) {
	reqs, err := s.db.RedemptionsOf(p.household.ID, p.person.ID, s.now())
	if err != nil {
		return nil, err
	}
	people, err := s.db.People(p.household.ID)
	if err != nil {
		return nil, err
	}
	parents := s.joinNames(lang, "rwd.or", s.parentNames(people))
	var out []string
	for _, d := range reqs {
		if d.Status == store.RedemptionOpen {
			out = append(out, s.i18n.Format(lang, i18n.Msg("rwd.link.asked", "reward", d.Name, "parents", parents)))
		}
	}
	return out, nil
}
