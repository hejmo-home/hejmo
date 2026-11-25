package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/reward"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The savings goal in the web interface (ticket 293, ADR 0015): a person sets
// and clears their own goal from /tasks/rewards; parents choose for anyone on
// /tasks/rewards/goal/{person}.

const goalPath = rewardsPath + "/goal/"

// withGoalPages sends /tasks/rewards/goal/... to the goal handlers before the
// rewards' own routes: "goal/{person}" and "{id}/picture" would overlap.
func (s *server) withGoalPages(rewards http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+goalPath+"clear", s.handleGoalClear)
	mux.HandleFunc("GET "+goalPath+"{person}", s.handleGoalPage)
	mux.HandleFunc("POST "+goalPath+"{person}", s.handleGoalSave)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, goalPath) {
			mux.ServeHTTP(w, r)
			return
		}
		rewards.ServeHTTP(w, r)
	})
}

// handleGoalSet makes a reward the goal of the person who asks: "Save for this".
func (s *server) handleGoalSet(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	rw, err := s.db.Reward(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && !rw.VisibleTo(p.person.ID, isChild(p))) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "reward", err)
		return
	}
	old, err := s.db.Goal(p.household.ID, p.person.ID)
	if err != nil {
		s.fail(w, "savings goal", err)
		return
	}
	switch err := s.db.SetGoal(p.household.ID, p.person.ID, rw.ID); {
	case err == nil:
	case errors.Is(err, store.ErrInvalid): // archived or no longer meant for them meanwhile: the page shows what is
		http.Redirect(w, r, rewardsPath, http.StatusSeeOther)
		return
	default:
		s.fail(w, "set savings goal", err)
		return
	}
	if old.Active() && old.Reward.ID != rw.ID {
		s.redirectSavedFields(w, r, rewardsPath, "flash.goal_switched", rw.Name, old.Reward.Name)
		return
	}
	s.redirectSavedFields(w, r, rewardsPath, "flash.goal_set", rw.Name)
}

// handleGoalClear ends the goal of the person who asks. Their stars stay.
func (s *server) handleGoalClear(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	if err := s.db.ClearGoal(p.household.ID, p.person.ID); err != nil {
		s.fail(w, "clear savings goal", err)
		return
	}
	s.redirectSaved(w, r, rewardsPath, "flash.goal_cleared")
}

// goalOption is one radio card of the parents' goal page.
type goalOption struct {
	ID, Name, Symbol, Picture, Price, Note string
	Checked                                bool
}

// goalPersonPage loads the person of a goal page for a parent: 404 if gone.
func (s *server) goalPersonPage(w http.ResponseWriter, r *http.Request) (principal, store.Person, bool) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return principal{}, store.Person{}, false
	}
	person, err := s.db.Person(p.household.ID, r.PathValue("person"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return principal{}, store.Person{}, false
	}
	if err != nil {
		s.fail(w, "person", err)
		return principal{}, store.Person{}, false
	}
	return p, person, true
}

func (s *server) handleGoalPage(w http.ResponseWriter, r *http.Request) {
	if p, person, ok := s.goalPersonPage(w, r); ok {
		s.renderGoalPage(w, r, p, person, http.StatusOK, "")
	}
}

// renderGoalPage shows the rewards meant for the person, by price. bad is the
// error sentence for the field, if any.
func (s *server) renderGoalPage(w http.ResponseWriter, r *http.Request, p principal, person store.Person, status int, bad string) {
	lang := s.langOf(r)
	list, err := s.db.RewardsFor(p.household.ID, person.ID)
	if err != nil {
		s.fail(w, "rewards", err)
		return
	}
	goal, err := s.db.Goal(p.household.ID, person.ID)
	if err != nil {
		s.fail(w, "savings goal", err)
		return
	}
	balance, states, err := s.rewardStates(p, person.ID, list)
	if err != nil {
		s.fail(w, "reward states", err)
		return
	}
	poss := possessive(lang, person.Name)
	var opts []goalOption
	for _, rw := range list {
		st := states[rw.ID]
		o := goalOption{ID: rw.ID, Name: rw.Name, Symbol: rw.Symbol, Picture: pictureOf(rw), Price: s.starsWords(lang, rw.Price), Checked: goal.Active() && goal.Reward.ID == rw.ID}
		parts := []string{s.i18n.T(lang, "goalpage.enough")}
		if !st.Affordable {
			key := "goalpage.missing.other"
			if st.Missing == 1 {
				key = "goalpage.missing.one"
			}
			parts[0] = s.i18n.Format(lang, i18n.Msg(key, "n", strconv.Itoa(st.Missing)))
		}
		switch {
		case st.LimitReached:
			parts = append(parts, s.i18n.Format(lang, i18n.Msg("goalpage.used."+string(rw.Limit.Period), "when", s.againWords(p, lang, st.AvailableAgain))))
		case rw.Limit.Period != reward.NoLimit:
			parts = append(parts, s.limitWords(lang, rw.Limit))
		}
		o.Note = strings.Join(parts, " · ")
		opts = append(opts, o)
	}
	hasDevice, err := s.hasOwnDevice(p, person)
	if err != nil {
		s.fail(w, "personal devices", err)
		return
	}
	s.render(w, r, status, "reward_goal.html", map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Title": s.i18n.Format(lang, i18n.Msg("goalpage.title", "name", person.Name)),
		"Name": person.Name, "Poss": poss, "Stars": s.starsWords(lang, balance), "Options": opts, "NoneChecked": !goal.Active(), "Bad": bad, "OwnDevice": hasDevice,
		"Action": goalPath + person.ID,
	})
}

// hasOwnDevice reports whether the person has a personal device or signs in themselves (a parent),
// so that they can change their own goal.
func (s *server) hasOwnDevice(p principal, person store.Person) (bool, error) {
	devices, err := s.db.PersonalDevices(p.household.ID)
	if err != nil {
		return false, err
	}
	for _, d := range devices {
		if d.PersonID == person.ID {
			return true, nil
		}
	}
	return person.Role == store.RoleParent, nil
}

func (s *server) handleGoalSave(w http.ResponseWriter, r *http.Request) {
	p, person, ok := s.goalPersonPage(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	_ = r.ParseForm()
	id := r.PostFormValue("reward")
	if id == "" {
		if err := s.db.ClearGoal(p.household.ID, person.ID); err != nil {
			s.fail(w, "clear savings goal", err)
			return
		}
		s.redirectSavedFields(w, r, "/tasks#stars", "flash.goal_cleared_for", person.Name)
		return
	}
	switch err := s.db.SetGoal(p.household.ID, person.ID, id); {
	case err == nil:
		rw, err := s.db.Reward(p.household.ID, id)
		if err != nil {
			s.fail(w, "reward", err)
			return
		}
		s.redirectSavedFields(w, r, "/tasks#stars", "flash.goal_set_for", possessive(lang, person.Name), rw.Name)
	case errors.Is(err, store.ErrInvalid):
		name, key := "", "goalpage.error.audience"
		if rw, err := s.db.Reward(p.household.ID, id); err == nil {
			name = rw.Name
			if rw.Archived() {
				key = "goalpage.error.archived"
			}
		}
		s.renderGoalPage(w, r, p, person, http.StatusUnprocessableEntity, s.i18n.Format(lang, i18n.Msg(key, "reward", name, "name", person.Name)))
	default:
		s.fail(w, "set savings goal", err)
	}
}
