// Package badge holds the rules of the ten badges (ticket 295): the fixed
// catalogue and when a person has earned each. It knows nothing about storage
// or HTTP. A badge is earned once and never taken away; that is the store's
// job (a unique key per person), here only what has been earned so far is
// worked out from the history.
//
// The idea of milestone badges was compared with Yuvomi (MIT,
// github.com/ulsklyc/yuvomi); no code is ported.
package badge

import (
	"slices"
	"time"

	"github.com/hejmo-home/hejmo/internal/streak"
	"github.com/hejmo-home/hejmo/internal/task"
)

// Key names a badge. It is stored, sent over the API and names the texts
// "badge.<key>.name" and "badge.<key>.how" in the locales.
type Key string

// The ten badges.
const (
	FirstRoutine Key = "first_routine"
	WholeDay     Key = "whole_day"
	WholeWeek    Key = "whole_week"
	Streak7      Key = "streak_7"
	Streak30     Key = "streak_30"
	FirstStar    Key = "first_star"
	Stars100     Key = "stars_100"
	Stars500     Key = "stars_500"
	FirstReward  Key = "first_reward"
	GoalReached  Key = "goal_reached"
)

// Catalogue is every badge, in the order the pages list them.
var Catalogue = []Key{FirstRoutine, WholeDay, WholeWeek, Streak7, Streak30, FirstStar, Stars100, Stars500, FirstReward, GoalReached}

// Valid reports whether k is one of the ten.
func (k Key) Valid() bool { return slices.Contains(Catalogue, k) }

// Index is the place of k in the catalogue, or len(Catalogue) when unknown.
func (k Key) Index() int {
	if i := slices.Index(Catalogue, k); i >= 0 {
		return i
	}
	return len(Catalogue)
}

// Reachable says whether a person can still earn k: routine badges need a
// routine, star badges a task worth stars or a star booking, reward badges a
// reward meant for them.
func Reachable(k Key, hasRoutine, hasStars, hasRewards bool) bool {
	switch k {
	case FirstRoutine, WholeDay, WholeWeek, Streak7, Streak30:
		return hasRoutine
	case FirstStar, Stars100, Stars500:
		return hasStars
	case FirstReward, GoalReached:
		return hasRewards
	}
	return false
}

// Input is the history of one person. Today is YYYY-MM-DD in the household's
// time zone.
type Input struct {
	Today string
	// Routines are the person's routines with their ticked days and the
	// household's pauses.
	Routines []streak.Routine
	// StarsEarned is every star earned so far: ticks and the parents' positive
	// bookings. Redemptions do not lower it.
	StarsEarned int
	// Redeemed is set once any redemption of the person was confirmed.
	Redeemed bool
	// GoalRedeemed is set once the person redeemed their savings goal.
	GoalRedeemed bool
}

// Earned returns the badges the history has earned, in catalogue order.
func Earned(in Input) []Key {
	var out []Key
	add := func(ok bool, k Key) {
		if ok {
			out = append(out, k)
		}
	}
	best, wholeDay, wholeWeek, anyDone := 0, false, false, false
	for _, r := range in.Routines {
		best = max(best, r.Best(in.Today))
	}
	wholeDay, anyDone = days(in)
	wholeWeek = weeks(in)
	add(anyDone, FirstRoutine)
	add(wholeDay, WholeDay)
	add(wholeWeek, WholeWeek)
	add(best >= 7, Streak7)
	add(best >= 30, Streak30)
	add(in.StarsEarned >= 1, FirstStar)
	add(in.StarsEarned >= 100, Stars100)
	add(in.StarsEarned >= 500, Stars500)
	add(in.Redeemed, FirstReward)
	add(in.GoalRedeemed, GoalReached)
	return out
}

// firstDay is the earliest day any routine counts from.
func firstDay(rs []streak.Routine) (time.Time, bool) {
	var first time.Time
	for _, r := range rs {
		t, err := time.Parse(task.DateLayout, r.Created)
		if err != nil {
			continue
		}
		if first.IsZero() || t.Before(first) {
			first = t
		}
	}
	return first, !first.IsZero()
}

// days looks for a day on which every routine due was done (and at least two
// were due) and for any routine done on any day. A routine whose day was a
// pause day that was not done is not due that day.
func days(in Input) (wholeDay, anyDone bool) {
	first, ok := firstDay(in.Routines)
	last, err := time.Parse(task.DateLayout, in.Today)
	if !ok || err != nil {
		return false, false
	}
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		day := d.Format(task.DateLayout)
		due, open := 0, 0
		for _, r := range in.Routines {
			switch r.State(day) {
			case streak.Done:
				due++
				anyDone = true
			case streak.Open:
				due++
				open++
			}
		}
		if due >= 2 && open == 0 {
			wholeDay = true
		}
	}
	return wholeDay, anyDone
}

// weeks looks for a routine done on all its counting days of one week, Monday
// to Sunday. Days off and pause days that were not
// done do not count; days still to come do, so a week is never whole early.
// Only weeks starting on or after the routine's first Monday are looked at.
func weeks(in Input) bool {
	last, err := time.Parse(task.DateLayout, in.Today)
	if err != nil {
		return false
	}
	for _, r := range in.Routines {
		from, err := time.Parse(task.DateLayout, r.Created)
		if err != nil {
			continue
		}
		// The first week that counts starts on the routine's first Monday on
		// or after its creation: a week it was not there for is not whole.
		monday := from.AddDate(0, 0, (8-int(from.Weekday()))%7)
		for ; !monday.After(last); monday = monday.AddDate(0, 0, 7) {
			done, open := 0, 0
			for i := range 7 {
				switch r.State(monday.AddDate(0, 0, i).Format(task.DateLayout)) {
				case streak.Done:
					done++
				case streak.Open:
					open++
				}
			}
			if done > 0 && open == 0 {
				return true
			}
		}
	}
	return false
}

// New reports whether a badge counts as new: earned today or yesterday in the
// household's time zone, and earned while the person's switch was on (quiet
// says it was off). Nothing is "new" for longer than two days.
func New(earned, now time.Time, quiet bool, loc *time.Location) bool {
	if quiet {
		return false
	}
	y, m, d := now.In(loc).Date()
	yesterday := time.Date(y, m, d-1, 0, 0, 0, 0, loc)
	return !earned.In(loc).Before(yesterday)
}
