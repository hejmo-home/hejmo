package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 293: the savings goal against a real SQLite file.

func (f rewardFixture) reward(t *testing.T, r store.Reward) store.Reward {
	t.Helper()
	got, err := f.db.CreateReward(f.h.ID, f.mama.ID, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (f rewardFixture) goalOf(t *testing.T, person string) store.Goal {
	t.Helper()
	g, err := f.db.Goal(f.h.ID, person)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestAPersonHasOneGoalAndSwitchingReplacesIt(t *testing.T) {
	f := newRewardFixture(t)
	kino := f.reward(t, store.Reward{Name: "Kino", Price: 30})
	eis := f.reward(t, store.Reward{Name: "Eis", Price: 8})
	if g := f.goalOf(t, f.mia.ID); g.Active() || g.Ended() {
		t.Fatalf("no goal yet: %+v", g)
	}
	if err := f.db.SetGoal(f.h.ID, f.mia.ID, kino.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetGoal(f.h.ID, f.mia.ID, eis.ID); err != nil {
		t.Fatal(err)
	}
	if g := f.goalOf(t, f.mia.ID); !g.Active() || g.Reward.ID != eis.ID || g.Reward.Name != "Eis" || g.SetAt.IsZero() {
		t.Errorf("goal = %+v", g)
	}
	var n int
	if err := f.db.SQL().QueryRow(`SELECT COUNT(*) FROM savings_goals WHERE person_id = ?`, f.mia.ID).Scan(&n); err != nil || n != 1 {
		t.Errorf("rows = %d %v", n, err)
	}
	if g := f.goalOf(t, f.sebastian.ID); g.Active() {
		t.Errorf("Sebastian has none: %+v", g)
	}
	if err := f.db.ClearGoal(f.h.ID, f.mia.ID); err != nil {
		t.Fatal(err)
	}
	if g := f.goalOf(t, f.mia.ID); g.Active() || g.Ended() {
		t.Errorf("cleared by hand leaves no line: %+v", g)
	}
}

func TestAGoalMustBeAnOfferedRewardMeantForThePerson(t *testing.T) {
	f := newRewardFixture(t)
	forSeb := f.reward(t, store.Reward{Name: "Kuscheltier", Price: 40, PersonIDs: []string{f.sebastian.ID}})
	archived := f.reward(t, store.Reward{Name: "Alt", Price: 5})
	if _, err := f.db.ArchiveReward(f.h.ID, archived.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	for name, rid := range map[string]string{"not meant for": forSeb.ID, "archived": archived.ID, "unknown": "nope"} {
		err := f.db.SetGoal(f.h.ID, f.mia.ID, rid)
		var fe *store.FieldError
		if !errors.As(err, &fe) || fe.Field != "reward_id" || !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if err := f.db.SetGoal(f.h.ID, "nobody", forSeb.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown person: %v", err)
	}
}

func endedWith(t *testing.T, f rewardFixture, person, reason, name string) {
	t.Helper()
	g := f.goalOf(t, person)
	if g.Active() || !g.Ended() || g.EndedReason != reason || g.EndedName != name || g.EndedAt.IsZero() {
		t.Errorf("want ended %q %q, got %+v", reason, name, g)
	}
}

func TestAGoalEndsWhenItsRewardIsRedeemedArchivedOrDeleted(t *testing.T) {
	f := newRewardFixture(t)
	a := f.reward(t, store.Reward{Name: "Kino", Price: 30})
	b := f.reward(t, store.Reward{Name: "Eis", Price: 8})
	c := f.reward(t, store.Reward{Name: "Ausflug", Price: 50})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.db.SetGoal(f.h.ID, f.mia.ID, a.ID))
	must(f.db.SetGoal(f.h.ID, f.sebastian.ID, a.ID))
	// An open or declined request ends nothing; a confirmed one ends it for that person only.
	must(f.db.InsertRedemption(a.ID, f.mia.ID, "open", time.Now()))
	if !f.goalOf(t, f.mia.ID).Active() {
		t.Fatal("an open request must not end the goal")
	}
	must(f.db.ExecUnchecked(`UPDATE reward_redemptions SET status = 'confirmed'`))
	endedWith(t, f, f.mia.ID, store.GoalRedeemed, "Kino")
	if !f.goalOf(t, f.sebastian.ID).Active() {
		t.Error("another person's redemption must not end Sebastian's goal")
	}
	must(f.db.InsertRedemption(a.ID, f.sebastian.ID, "confirmed", time.Now()))
	endedWith(t, f, f.sebastian.ID, store.GoalRedeemed, "Kino")

	must(f.db.SetGoal(f.h.ID, f.mia.ID, b.ID))
	_, err := f.db.ArchiveReward(f.h.ID, b.ID, time.Now())
	must(err)
	endedWith(t, f, f.mia.ID, store.GoalArchived, "Eis")

	must(f.db.SetGoal(f.h.ID, f.mia.ID, c.ID))
	must(f.db.DeleteReward(f.h.ID, c.ID))
	endedWith(t, f, f.mia.ID, store.GoalDeleted, "Ausflug")

	// A new goal clears the ended line.
	must(f.db.SetGoal(f.h.ID, f.mia.ID, f.reward(t, store.Reward{Name: "Zoo", Price: 20}).ID))
	if g := f.goalOf(t, f.mia.ID); !g.Active() || g.Ended() {
		t.Errorf("after a new goal: %+v", g)
	}
}

func TestAGoalEndsWhenThePersonIsNoLongerInTheAudience(t *testing.T) {
	f := newRewardFixture(t)
	r := f.reward(t, store.Reward{Name: "Kino", Price: 30, PersonIDs: []string{f.mia.ID, f.sebastian.ID}})
	if err := f.db.SetGoal(f.h.ID, f.mia.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetGoal(f.h.ID, f.sebastian.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	r.PersonIDs = []string{f.sebastian.ID}
	if _, err := f.db.UpdateReward(f.h.ID, r, store.KeepPicture, nil); err != nil {
		t.Fatal(err)
	}
	endedWith(t, f, f.mia.ID, store.GoalAudience, "Kino")
	if !f.goalOf(t, f.sebastian.ID).Active() {
		t.Error("Sebastian is still in the audience")
	}
	// Widening to the whole household ends nobody's goal.
	r.PersonIDs = nil
	if _, err := f.db.UpdateReward(f.h.ID, r, store.KeepPicture, nil); err != nil {
		t.Fatal(err)
	}
	if !f.goalOf(t, f.sebastian.ID).Active() {
		t.Error("the whole household includes Sebastian")
	}
}

func TestAGoalGoesWhenThePersonLeavesAndTheEndedLineLastsSevenDays(t *testing.T) {
	f := newRewardFixture(t)
	r := f.reward(t, store.Reward{Name: "Kino", Price: 30})
	if err := f.db.SetGoal(f.h.ID, f.mia.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ArchiveReward(f.h.ID, r.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	g := f.goalOf(t, f.mia.ID)
	if !g.EndedShown(g.EndedAt.Add(6 * 24 * time.Hour)) {
		t.Error("the line shows for 7 days")
	}
	if g.EndedShown(g.EndedAt.Add(7 * 24 * time.Hour)) {
		t.Error("and not longer")
	}
	if err := f.db.ExecUnchecked(`DELETE FROM household_members WHERE person_id = ?`, f.mia.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = f.db.SQL().QueryRow(`SELECT COUNT(*) FROM savings_goals WHERE person_id = ?`, f.mia.ID).Scan(&n)
	if n != 0 {
		t.Errorf("rows after leaving = %d", n)
	}
}

func TestGoalsOfTheHouseholdComeByPerson(t *testing.T) {
	f := newRewardFixture(t)
	r := f.reward(t, store.Reward{Name: "Kino", Price: 30})
	_ = f.db.SetGoal(f.h.ID, f.mia.ID, r.ID)
	all, err := f.db.Goals(f.h.ID)
	if err != nil || len(all) != 1 || all[f.mia.ID].Reward.Name != "Kino" {
		t.Fatalf("goals = %+v %v", all, err)
	}
}
