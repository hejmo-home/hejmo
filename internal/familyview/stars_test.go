package familyview_test

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/layout"
)

// Ticket 293 (Q108): the stars block of the columns.

func starsInput() familyview.Input {
	return familyview.Input{Stars: map[string]familyview.StarsInput{
		"seb": {Balance: 12, Shown: true, Goal: &familyview.GoalInput{Name: "Kino mit Popcorn", Symbol: "🎬", Price: 30}},
		"mia": {Balance: 14, Shown: true},
	}}
}

func TestTheColumnShowsTheBalanceAndTheProgressTowardsTheGoal(t *testing.T) {
	v := buildWith(nil, starsInput())
	same(t, orderOf(v.ColumnBlocks), []string{"events", "tasks", "stars"})
	seb := column(t, v, "Sebastian").Stars
	if seb == nil || seb.Balance != 12 || seb.Goal == nil {
		t.Fatalf("Sebastian = %+v", seb)
	}
	g := seb.Goal
	if g.Name != "Kino mit Popcorn" || g.Symbol != "🎬" || g.Price != 30 || g.Balance != 12 || g.Reached || g.Pct != 40 {
		t.Errorf("goal = %+v", g)
	}
	// Without a goal only the count shows, and nothing is suggested instead.
	mia := column(t, v, "Mia").Stars
	if mia == nil || mia.Balance != 14 || mia.Goal != nil {
		t.Errorf("Mia = %+v", mia)
	}
}

func TestAReachedGoalShowsTheRealBalance(t *testing.T) {
	in := starsInput()
	in.Stars["seb"] = familyview.StarsInput{Balance: 27, Shown: true, Goal: &familyview.GoalInput{Name: "Kino", Price: 25}}
	g := column(t, buildWith(nil, in), "Sebastian").Stars.Goal
	if !g.Reached || g.Pct != 100 || g.Balance != 27 {
		t.Errorf("reached goal = %+v", g)
	}
	in.Stars["seb"] = familyview.StarsInput{Balance: 25, Shown: true, Goal: &familyview.GoalInput{Name: "Kino", Price: 25}}
	if g := column(t, buildWith(nil, in), "Sebastian").Stars.Goal; !g.Reached || g.Pct != 100 {
		t.Errorf("exactly the price = %+v", g)
	}
}

func TestBelowZeroTheBarIsEmptyAndPeopleWithoutStarsGetNoBlock(t *testing.T) {
	in := starsInput()
	in.Stars["seb"] = familyview.StarsInput{Balance: -2, Shown: true, Goal: &familyview.GoalInput{Name: "Kino", Price: 30}}
	in.Stars["papa"] = familyview.StarsInput{Balance: 0, Shown: false}
	v := buildWith(nil, in)
	if s := column(t, v, "Sebastian").Stars; s == nil || s.Balance != -2 || s.Goal == nil || s.Goal.Pct != 0 || s.Goal.Reached {
		t.Errorf("below zero = %+v", s)
	}
	if s := column(t, v, "Papa").Stars; s != nil {
		t.Errorf("stars hidden for Papa: %+v", s)
	}
	if s := column(t, v, "Mama").Stars; s != nil {
		t.Errorf("no stars entry for Mama: %+v", s)
	}
}

func TestSwitchingTheStarsBlockOffRemovesItAndPeopleKeepTheirOrder(t *testing.T) {
	v := buildWith(off(layout.Stars), starsInput())
	if column(t, v, "Sebastian").Stars != nil {
		t.Error("block off, column still has stars")
	}
	same(t, orderOf(v.ColumnBlocks), []string{"events", "tasks"})
	// More stars never move a column (no ranking).
	in := starsInput()
	in.Stars["mia"] = familyview.StarsInput{Balance: 999, Shown: true}
	v = buildWith(nil, in)
	var names []string
	for _, c := range v.Columns {
		names = append(names, c.Person.Name)
	}
	same(t, names, []string{"Mama", "Papa", "Sebastian", "Mia"})
}

func TestThePersonalViewCarriesTheGoalOnlyWhileStarsShow(t *testing.T) {
	goal := &familyview.GoalInput{Name: "Kino mit Popcorn", Symbol: "🎬", Price: 30}
	v := familyview.BuildPersonal(familyview.PersonalInput{Now: at(14, 8, 0), Person: family()[2], Stars: familyview.Stars{Shown: true, Balance: 14}, Goal: goal})
	if g := v.Stars.Goal; g == nil || g.Pct != 46 || g.Reached || g.Balance != 14 {
		t.Errorf("goal = %+v", g)
	}
	v = familyview.BuildPersonal(familyview.PersonalInput{Now: at(14, 8, 0), Person: family()[2], Stars: familyview.Stars{Shown: true, Balance: 31}, Goal: goal})
	if g := v.Stars.Goal; g == nil || !g.Reached || g.Pct != 100 {
		t.Errorf("reached = %+v", g)
	}
	v = familyview.BuildPersonal(familyview.PersonalInput{Now: at(14, 8, 0), Person: family()[2], Stars: familyview.Stars{Shown: false}, Goal: goal})
	if v.Stars.Goal != nil {
		t.Errorf("hidden stars carry no goal: %+v", v.Stars.Goal)
	}
	v = familyview.BuildPersonal(familyview.PersonalInput{Now: at(14, 8, 0), Person: family()[2], Stars: familyview.Stars{Shown: true, Balance: 14}})
	if v.Stars.Goal != nil {
		t.Errorf("no goal, nothing is suggested: %+v", v.Stars.Goal)
	}
}
