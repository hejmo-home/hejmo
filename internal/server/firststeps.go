package server

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Step states, in words on the card (ticket 133, design pass 2).
const (
	stepOpen    = "open"
	stepDone    = "done"
	stepSkipped = "skipped"
)

// firstStepsView is the "First steps" card on the start page after the setup
// wizard: six steps that fill the box with the family's life, in a sensible
// order. Each step says its state in words, only the first open step has the
// main button, and every open step can be skipped. The card stays until a
// parent hides it, so it can say "everything done" and be closed.
type firstStepsView struct {
	Steps []firstStep
	// Bar is the state of each part of the progress bar.
	Bar []string
	// Count is "2 of 6 done, 1 skipped" in the page language.
	Count string
	// Settled is true when every step is done or skipped.
	Settled bool
}

// firstStep is one step of the card, with everything the template prints.
type firstStep struct {
	ID, State, Title string
	StateText        string
	// Text is the explanation of an open step, "8 persons:" above the chips of
	// the family step, or the line of a skipped step.
	Text string
	// Chips lists the family on the done family step.
	Chips []personChip
	// Subs are the parts of a step with several, each with a tick or an open dot.
	Subs []fsSub
	// Rows are the calendars with who each one is for.
	Rows []fsRow
	// Actions are the links of the step; the first one is the main button on
	// the first open step.
	Actions []fsAction
	// Skip offers "Skip" (open step), Undo offers "Do it after all" (skipped step).
	Skip, Undo bool
	// Primary marks the first open step: the first action is a button.
	Primary bool
}

type fsSub struct {
	Done bool
	Text string
}

type fsRow struct{ Name, Source, For string }

type fsAction struct{ Href, Label string }

// firstSteps builds the card for a parent, or nil when there is nothing to show.
func (s *server) firstSteps(p store.Person, h store.Household, lang string) (*firstStepsView, error) {
	if p.Role != store.RoleParent || h.FirstStepsHidden {
		return nil, nil
	}
	people, err := s.db.People(h.ID)
	if err != nil {
		return nil, err
	}
	t := func(key string) string { return s.i18n.T(lang, key) }
	f := func(key string, kv ...string) string { return s.i18n.Format(lang, i18n.Msg(key, kv...)) }
	steps := make([]firstStep, len(store.FirstStepIDs))
	for i, id := range store.FirstStepIDs {
		steps[i] = firstStep{ID: id, Title: t("firststeps." + id + ".title")}
	}
	if err := s.fillFamily(&steps[0], people, f); err != nil {
		return nil, err
	}
	if err := s.fillParents(&steps[1], h, p, people, lang, f); err != nil {
		return nil, err
	}
	if err := s.fillCalendars(&steps[2], h, people, f, t); err != nil {
		return nil, err
	}
	if err := s.fillTasks(&steps[3], h, people, f); err != nil {
		return nil, err
	}
	if err := s.fillMeals(&steps[4], h, f); err != nil {
		return nil, err
	}
	if err := s.fillDisplay(&steps[5], h, f); err != nil {
		return nil, err
	}
	v := &firstStepsView{Steps: steps}
	open, done, skipped := 0, 0, 0
	for i := range v.Steps {
		st := &v.Steps[i]
		switch {
		case st.State == stepDone:
			done++
		case slices.Contains(h.FirstStepsSkipped, st.ID):
			st.State = stepSkipped
			skipped++
		default:
			st.State = stepOpen
			st.Skip = true
			st.Primary = open == 0
			open++
		}
		st.StateText = t("firststeps.state." + st.State)
		if st.State == stepSkipped {
			st.Undo = true
			st.Text, st.Subs, st.Rows, st.Chips, st.Actions = t("firststeps.skipped.text"), nil, nil, nil, nil
		}
		v.Bar = append(v.Bar, st.State)
	}
	v.Settled = open == 0
	if skipped > 0 {
		v.Count = f("firststeps.count.skipped", "n", strconv.Itoa(done), "k", strconv.Itoa(skipped))
	} else {
		v.Count = f("firststeps.count", "n", strconv.Itoa(done))
	}
	return v, nil
}

// settle marks a step done or leaves it open, with the actions that fit.
func settle(st *firstStep, done bool, doneAct fsAction, openActs ...fsAction) {
	if done {
		st.State = stepDone
		st.Actions = []fsAction{doneAct}
		return
	}
	st.State = stepOpen
	st.Actions = openActs
}

func (s *server) fillFamily(st *firstStep, people []store.Person, f func(string, ...string) string) error {
	done := len(people) > 1 // the first parent exists from the setup
	settle(st, done, fsAction{"/people", f("firststeps.family.change")}, fsAction{"/people", f("firststeps.family.action")})
	if done {
		st.Chips = chipsOf(people)
		st.Text = f("firststeps.family.count", "n", strconv.Itoa(len(people)))
	} else {
		st.Text = f("firststeps.family.text")
	}
	return nil
}

// fillParents is the second parent and phones step. The second parent is a
// parent other than the one looking at Home: who set the box up is not stored,
// and the order of the people can change. The parent counts once they have an
// account, so an invitation that was accepted counts (ticket 145); while a link
// is open the step says until when it works and leads to it.
func (s *server) fillParents(st *firstStep, h store.Household, viewer store.Person, people []store.Person, lang string, f func(string, ...string) string) error {
	var withAccount []string
	var waiting *store.Person // the first other adult without an account
	var openInv *store.Invitation
	for i, p := range people {
		if p.Role != store.RoleParent || p.ID == viewer.ID {
			continue
		}
		state, err := s.signInOf(h.ID, p)
		if err != nil {
			return err
		}
		if state.Kind == signInAccount {
			withAccount = append(withAccount, p.Name)
		} else if waiting == nil || (state.Kind == signInOpen && openInv == nil) {
			waiting = &people[i]
			openInv = nil
			if state.Kind == signInOpen {
				inv := state.Invite
				openInv = &inv
			}
		}
	}
	phones, err := s.db.Phones(h.ID)
	if err != nil {
		return err
	}
	var list []string
	for _, ph := range phones {
		list = append(list, ph.PersonName+" ("+ph.Name+")")
	}
	hasParent, hasPhone := len(withAccount) > 0, len(phones) > 0
	var acts []fsAction
	var waitingText string
	switch {
	case hasParent:
	case openInv != nil:
		acts = append(acts, fsAction{"/people/" + waiting.ID + "/invitation", f("firststeps.parents.view")})
		waitingText = f("firststeps.parents.invited", "name", waiting.Name, "date", s.validUntil(lang, h, openInv.ExpiresAt))
	case waiting != nil:
		acts = append(acts, fsAction{"/people/" + waiting.ID + "/invitation", f("firststeps.parents.invite", "name", waiting.Name)})
		waitingText = f("firststeps.parents.entered", "name", waiting.Name)
	default:
		acts = append(acts, fsAction{"/people", f("firststeps.parents.add")})
	}
	if !hasPhone {
		acts = append(acts, fsAction{"/phones", f("firststeps.parents.phone")})
	}
	settle(st, hasParent && hasPhone, fsAction{"/phones", f("firststeps.parents.manage")}, acts...)
	if hasParent || hasPhone || waitingText != "" {
		st.Subs = []fsSub{{hasParent, f("firststeps.parents.person.open")}, {hasPhone, f("firststeps.parents.phones.open")}}
		if hasParent {
			st.Subs[0].Text = f("firststeps.parents.person", "names", strings.Join(withAccount, ", "))
		} else if waitingText != "" {
			st.Subs[0].Text = waitingText
		}
		if hasPhone {
			st.Subs[1].Text = f("firststeps.parents.phones", "list", strings.Join(list, ", "))
		}
	} else {
		st.Text = f("firststeps.parents.text")
	}
	return nil
}

func (s *server) fillCalendars(st *firstStep, h store.Household, people []store.Person, f func(string, ...string) string, t func(string) string) error {
	conns, err := s.db.Connections(h.ID)
	if err != nil {
		return err
	}
	synced, err := s.db.SyncedCalendars(h.ID)
	if err != nil {
		return err
	}
	settle(st, len(conns) > 0 || len(synced) > 0, fsAction{"/calendars", f("firststeps.calendars.change")}, fsAction{"/calendars", f("firststeps.calendars.action")})
	if st.State != stepDone {
		st.Text = f("firststeps.calendars.text")
		return nil
	}
	cals, err := s.db.Calendars(h.ID)
	if err != nil {
		return err
	}
	groups, err := s.db.Groups(h.ID)
	if err != nil {
		return err
	}
	for _, c := range cals {
		if c.Kind == "meals" {
			continue
		}
		st.Rows = append(st.Rows, fsRow{Name: c.Name, Source: "(" + t("firststeps.source."+c.Kind) + ")", For: audienceText(c.Audience.People, c.Audience.Groups, people, groups, f)})
	}
	return nil
}

// audienceText words who a calendar is for: the names in household order, the
// whole family when nobody is named or everybody is.
func audienceText(ids, groupIDs []string, people []store.Person, groups []store.Group, f func(string, ...string) string) string {
	var names []string
	for _, p := range people {
		if slices.Contains(ids, p.ID) {
			names = append(names, p.Name)
		}
	}
	for _, g := range groups {
		if slices.Contains(groupIDs, g.ID) {
			names = append(names, g.Name)
		}
	}
	if len(ids) == 0 && len(groupIDs) == 0 || len(names) >= len(people) && len(groupIDs) == 0 {
		return f("firststeps.calendars.for_all")
	}
	return f("firststeps.calendars.for", "names", strings.Join(names, ", "))
}

func (s *server) fillTasks(st *firstStep, h store.Household, people []store.Person, f func(string, ...string) string) error {
	count, by, err := s.db.CountTasks(h.ID)
	if err != nil {
		return err
	}
	settle(st, count > 0, fsAction{"/tasks", f("firststeps.tasks.change")}, fsAction{"/tasks", f("firststeps.tasks.action")})
	if count == 0 {
		st.Text = f("firststeps.tasks.text")
		return nil
	}
	var names []string
	for _, p := range people {
		if by[p.ID] > 0 {
			names = append(names, p.Name)
		}
	}
	key := "firststeps.tasks.summary.other"
	if count == 1 {
		key = "firststeps.tasks.summary.one"
	}
	st.Subs = []fsSub{{true, f(key, "n", strconv.Itoa(count), "names", strings.Join(names, ", "))}}
	return nil
}

func (s *server) fillMeals(st *firstStep, h store.Household, f func(string, ...string) string) error {
	count, err := s.db.CountMeals(h.ID)
	if err != nil {
		return err
	}
	// The shopping list (#32) and the meal plan templates (#131) belong to this step.
	more := []fsAction{{"/lists", f("firststeps.meals.lists")}, {"/meals/templates", f("firststeps.meals.templates")}}
	settle(st, count > 0, fsAction{"/meals", f("firststeps.meals.change")}, fsAction{"/meals", f("firststeps.meals.action")})
	st.Actions = append(st.Actions, more...)
	if count == 0 {
		st.Text = f("firststeps.meals.text")
		return nil
	}
	key := "firststeps.meals.summary.other"
	if count == 1 {
		key = "firststeps.meals.summary.one"
	}
	st.Subs = []fsSub{{true, f(key, "n", strconv.Itoa(count))}}
	return nil
}

func (s *server) fillDisplay(st *firstStep, h store.Household, f func(string, ...string) string) error {
	all, err := s.db.Displays(h.ID)
	if err != nil {
		return err
	}
	var names []string
	for _, d := range all {
		if d.Paired {
			names = append(names, d.Name)
		}
	}
	settle(st, len(names) > 0, fsAction{"/displays", f("firststeps.display.change")}, fsAction{"/displays", f("firststeps.display.action")})
	switch {
	case len(names) == 0:
		st.Text = f("firststeps.display.text")
	case len(names) == 1:
		st.Subs = []fsSub{{true, f("firststeps.display.one", "name", names[0])}}
	default:
		st.Subs = []fsSub{{true, f("firststeps.display.other", "names", strings.Join(names, ", "))}}
	}
	return nil
}

// firstStepAction is a post of the card's buttons for one step: skip or undo.
func (s *server) handleFirstStepSkip(w http.ResponseWriter, r *http.Request) {
	s.setStepSkipped(w, r, true)
}
func (s *server) handleFirstStepUndo(w http.ResponseWriter, r *http.Request) {
	s.setStepSkipped(w, r, false)
}

func (s *server) setStepSkipped(w http.ResponseWriter, r *http.Request, skipped bool) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if err := s.db.SetFirstStepSkipped(p.household.ID, r.PostFormValue("step"), skipped); err != nil {
		if errors.Is(err, store.ErrInvalid) {
			http.Error(w, "unknown step", http.StatusUnprocessableEntity)
			return
		}
		s.fail(w, "skip first step", err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleFirstStepsHide hides the first steps card for the whole household; Home
// then says where the steps are.
func (s *server) handleFirstStepsHide(w http.ResponseWriter, r *http.Request) {
	s.setFirstStepsHidden(w, r, true, "flash.first_steps_hidden")
}

// handleFirstStepsShow shows the card again (Settings).
func (s *server) handleFirstStepsShow(w http.ResponseWriter, r *http.Request) {
	s.setFirstStepsHidden(w, r, false, "flash.first_steps_shown")
}

func (s *server) setFirstStepsHidden(w http.ResponseWriter, r *http.Request, hidden bool, flash string) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if err := s.db.SetFirstStepsHidden(p.household.ID, hidden); err != nil {
		s.fail(w, "first steps visibility", err)
		return
	}
	s.redirectSaved(w, r, "/", flash)
}
