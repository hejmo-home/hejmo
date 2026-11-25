package server

import (
	"time"

	"github.com/hejmo-home/hejmo/internal/reward"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The reward catalogue (ticket 291): what a child sees of each reward, and the
// pieces the web pages and the REST handlers share.

// rewardState is where one person stands against one reward: can they pay for
// it, and have they used up its limit in the current calendar period.
type rewardState struct {
	Affordable     bool
	Missing        int // stars still missing while not affordable
	LimitReached   bool
	AvailableAgain time.Time
}

// rewardStates evaluates rewards for one person at the box clock, in the household time zone.
func (s *server) rewardStates(p principal, personID string, list []store.Reward) (balance int, states map[string]rewardState, err error) {
	sums, err := s.db.StarSummaries(p.household.ID)
	if err != nil {
		return 0, nil, err
	}
	balance = sums[personID].Balance
	reserved, err := s.db.ReservedStars(personID) // stars set aside for open requests cannot be spent again (ticket 292)
	if err != nil {
		return 0, nil, err
	}
	free := reward.Free(balance, reserved)
	times, err := s.db.ConfirmedRedemptions(p.household.ID)
	if err != nil {
		return 0, nil, err
	}
	states = map[string]rewardState{}
	for _, r := range list {
		st := reward.Evaluate(r.Limit, times[r.ID][personID], s.now(), p.location())
		rs := rewardState{Affordable: free >= r.Price, LimitReached: st.Reached, AvailableAgain: st.AvailableAgain}
		if !rs.Affordable {
			rs.Missing = r.Price - free
		}
		states[r.ID] = rs
	}
	return balance, states, nil
}

// redeemedCounts is how often each reward was redeemed (confirmed), by anyone, also people who left.
func (s *server) redeemedCounts(p principal) (map[string]int, error) {
	return s.db.ConfirmedRedemptionCounts(p.household.ID)
}

func isChild(p principal) bool { return p.person.Role == store.RoleChild }
