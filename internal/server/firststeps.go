package server

import (
	"net/http"

	"github.com/hejmo-home/hejmo/internal/store"
)

// firstStepsView is the "First steps" card on the start page after the
// setup wizard (ticket 133): what a new family does next to fill the box with
// their life, in a sensible order. It shows what is done and what is not, only
// the first open step has the main button, and it disappears when everything is
// done or a parent hid it.
type firstStepsView struct {
	// LeadKey is the sentence under the heading; it counts what is left.
	LeadKey string
	// People are everyone in the household, listed once the family is entered.
	People    []personChip
	Family    firstStep
	Calendars firstStep
	Devices   firstStep
}

type firstStep struct {
	Done bool
	// Primary marks the first open step, the one with the main button.
	Primary bool
}

// firstSteps builds the card for a parent, or nil when there is nothing to show.
func (s *server) firstSteps(p store.Person, h store.Household) (*firstStepsView, error) {
	if p.Role != store.RoleParent || h.FirstStepsHidden {
		return nil, nil
	}
	people, err := s.db.People(h.ID)
	if err != nil {
		return nil, err
	}
	conns, err := s.db.Connections(h.ID)
	if err != nil {
		return nil, err
	}
	synced, err := s.db.SyncedCalendars(h.ID)
	if err != nil {
		return nil, err
	}
	displays, err := s.db.Displays(h.ID)
	if err != nil {
		return nil, err
	}
	phones, err := s.db.Phones(h.ID)
	if err != nil {
		return nil, err
	}
	v := &firstStepsView{}
	v.Family.Done = len(people) > 1 // the first parent exists from the setup
	v.Calendars.Done = len(conns) > 0 || len(synced) > 0
	v.Devices.Done = len(displays) > 0 || len(phones) > 0
	open := 0
	for _, st := range []*firstStep{&v.Family, &v.Calendars, &v.Devices} {
		if st.Done {
			continue
		}
		st.Primary = open == 0
		open++
	}
	if open == 0 {
		return nil, nil
	}
	v.LeadKey = map[int]string{3: "firststeps.lead.3", 2: "firststeps.lead.2", 1: "firststeps.lead.1"}[open]
	if v.Family.Done {
		v.People = chipsOf(people)
	}
	return v, nil
}

// handleFirstStepsHide hides the first steps card for the whole household.
func (s *server) handleFirstStepsHide(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if err := s.db.SetFirstStepsHidden(p.household.ID, true); err != nil {
		s.fail(w, "hide first steps", err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
