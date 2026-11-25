package store_test

import (
	"database/sql"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/badge"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

// Ticket 295: badges against a real SQLite file. Noon UTC keeps the day of
// the household (Europe/Berlin) the same as the UTC day.
var bt0 = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func (f taskFixture) badgeKeys(t *testing.T, person string) []badge.Key {
	t.Helper()
	list, err := f.db.Badges(f.h.ID, person)
	if err != nil {
		t.Fatal(err)
	}
	var keys []badge.Key
	for _, b := range list {
		keys = append(keys, b.Key)
	}
	return keys
}

func (f taskFixture) dailyTask(t *testing.T, stars int, title string) task.Task {
	t.Helper()
	k, err := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: title, Repeat: task.Daily, Date: "2026-11-01", Stars: stars})
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestATickAwardsTheBadgesItEarnsAndReturnsThem(t *testing.T) {
	f := newTaskFixture(t)
	k := f.dailyTask(t, 2, "Tidy room")
	got, err := f.db.TickTask(f.h.ID, k.ID, "2026-11-20", true, f.mia.ID, bt0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []badge.Key{badge.FirstStar}) {
		t.Fatalf("awarded = %v, want first_star", got)
	}
	if again, _ := f.db.TickTask(f.h.ID, k.ID, "2026-11-20", true, f.mia.ID, bt0); len(again) != 0 {
		t.Fatalf("a second tick of the same day awarded %v", again)
	}
	list, _ := f.db.Badges(f.h.ID, f.mia.ID)
	if len(list) != 1 || !list[0].EarnedAt.Equal(bt0) || list[0].Quiet {
		t.Fatalf("badges = %+v", list)
	}
}

func TestEveryWayToTickAwardsBadges(t *testing.T) {
	f := newTaskFixture(t)
	r, err := f.db.CreateRoutine(f.h.ID, task.Routine{PersonID: f.mia.ID, Name: "Morning", Part: task.Morning, Created: bt0.AddDate(0, 0, -3)})
	if err != nil {
		t.Fatal(err)
	}
	ids := f.routineSteps(t, r, "Teeth")
	sub, err := f.db.AddSubtask(f.h.ID, ids[0], task.Subtask{Title: "Brush"}, "2026-11-20", bt0)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := f.db.SetSubtaskDone(f.h.ID, ids[0], sub.ID, "2026-11-20", true, f.mia.ID, bt0)
	if err != nil {
		t.Fatal(err)
	}
	want := []badge.Key{badge.FirstRoutine, badge.FirstStar}
	if !slices.Equal(ch.Badges, want) {
		t.Fatalf("last subtask awarded %v, want %v", ch.Badges, want)
	}
}

func TestABadgeIsAwardedOncePerPersonUnderConcurrentTicks(t *testing.T) {
	f := newTaskFixture(t)
	const n = 8
	var ids []string
	for i := range n {
		ids = append(ids, f.dailyTask(t, 1, "Task "+string(rune('A'+i))).ID)
	}
	var mu sync.Mutex
	var awarded []badge.Key
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			keys, err := f.db.TickTask(f.h.ID, id, "2026-11-20", true, f.mia.ID, bt0)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			awarded = append(awarded, keys...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if !slices.Equal(awarded, []badge.Key{badge.FirstStar}) {
		t.Fatalf("awarded across all ticks = %v, want first_star exactly once", awarded)
	}
	if got := f.badgeKeys(t, f.mia.ID); !slices.Equal(got, []badge.Key{badge.FirstStar}) {
		t.Fatalf("stored = %v", got)
	}
}

func TestABadgeStaysAfterUntickingOrWhenTheBalanceDrops(t *testing.T) {
	f := newTaskFixture(t)
	k := f.dailyTask(t, 2, "Tidy room")
	if _, err := f.db.TickTask(f.h.ID, k.ID, "2026-11-20", true, f.mia.ID, bt0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.TickTask(f.h.ID, k.ID, "2026-11-20", false, f.mia.ID, bt0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if f.balance(t, f.mia.ID) != 0 {
		t.Fatalf("balance after unticking = %d", f.balance(t, f.mia.ID))
	}
	if got := f.badgeKeys(t, f.mia.ID); !slices.Equal(got, []badge.Key{badge.FirstStar}) {
		t.Fatalf("badges after unticking = %v", got)
	}
	// 100 stars by hand, then a parent takes them back: the badge stays.
	if _, err := f.db.BookStars(f.h.ID, f.mia.ID, 100, "holiday", f.mama.ID, bt0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.BookStars(f.h.ID, f.mia.ID, -100, "correction", f.mama.ID, bt0.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := f.badgeKeys(t, f.mia.ID); !slices.Contains(got, badge.Stars100) || !slices.Contains(got, badge.FirstStar) {
		t.Fatalf("badges after the balance dropped = %v", got)
	}
}

func TestRedeemedStarsStillCountForTheStarBadges(t *testing.T) {
	f := newRewardFixture(t)
	if _, err := f.db.BookStars(f.h.ID, f.mia.ID, 90, "earned", f.mama.ID, rt0.Add(-3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	rw := f.reward(t, store.Reward{Name: "Cinema", Symbol: "🎬", Price: 60})
	if _, err := f.db.RedeemNow(f.h.ID, rw.ID, f.mia.ID, f.mama.ID, rt0.Add(-2*time.Hour), time.UTC); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.BookStars(f.h.ID, f.mia.ID, 20, "more", f.mama.ID, rt0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	// 110 earned in all, but the balance is only 50.
	if bal := f.balance(t, f.mia); bal != 50 {
		t.Fatalf("balance = %d", bal)
	}
	list, _ := f.db.Badges(f.h.ID, f.mia.ID)
	var keys []badge.Key
	for _, b := range list {
		keys = append(keys, b.Key)
	}
	for _, k := range []badge.Key{badge.FirstStar, badge.Stars100, badge.FirstReward} {
		if !slices.Contains(keys, k) {
			t.Errorf("missing %s in %v", k, keys)
		}
	}
	if slices.Contains(keys, badge.GoalReached) {
		t.Errorf("goal_reached without a savings goal: %v", keys)
	}
}

func TestRedeemingTheSavingsGoalAwardsGoalReached(t *testing.T) {
	f := newRewardFixture(t)
	if _, err := f.db.BookStars(f.h.ID, f.mia.ID, 30, "earned", f.mama.ID, rt0.Add(-3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	rw := f.reward(t, store.Reward{Name: "Cinema", Symbol: "🎬", Price: 20})
	if err := f.db.SetGoal(f.h.ID, f.mia.ID, rw.ID); err != nil {
		t.Fatal(err)
	}
	ask, err := f.db.AskReward(f.h.ID, f.mia.ID, rw.ID, rt0.Add(-time.Hour), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ConfirmRedemption(f.h.ID, ask.ID, f.mama.ID, rt0, time.UTC); err != nil {
		t.Fatal(err)
	}
	list, _ := f.db.Badges(f.h.ID, f.mia.ID)
	var keys []badge.Key
	for _, b := range list {
		keys = append(keys, b.Key)
	}
	if !slices.Contains(keys, badge.FirstReward) || !slices.Contains(keys, badge.GoalReached) {
		t.Fatalf("badges = %v", keys)
	}
}

func TestBadgesAreAwardedQuietlyWhileTheSwitchIsOffAndNeverBecomeNew(t *testing.T) {
	f := newTaskFixture(t)
	if err := f.db.SetStreaksShown(f.h.ID, f.mia.ID, false); err != nil {
		t.Fatal(err)
	}
	k := f.dailyTask(t, 1, "Tidy room")
	if got, err := f.db.TickTask(f.h.ID, k.ID, "2026-11-20", true, f.mia.ID, bt0); err != nil || len(got) != 1 {
		t.Fatalf("awarded = %v, %v (a badge is awarded while the switch is off)", got, err)
	}
	if err := f.db.SetStreaksShown(f.h.ID, f.mia.ID, true); err != nil {
		t.Fatal(err)
	}
	list, _ := f.db.Badges(f.h.ID, f.mia.ID)
	if len(list) != 1 || !list[0].Quiet {
		t.Fatalf("badges = %+v, want one quiet badge", list)
	}
	if list[0].New(bt0.Add(time.Hour), time.UTC) {
		t.Fatal("a badge earned while the switch was off is new")
	}
	// Earned with the switch on, the same day: new.
	if _, err := f.db.BookStars(f.h.ID, f.mia.ID, 100, "x", f.mama.ID, bt0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	list, _ = f.db.Badges(f.h.ID, f.mia.ID)
	var fresh []badge.Key
	for _, b := range list {
		if b.New(bt0.Add(2*time.Hour), time.UTC) {
			fresh = append(fresh, b.Key)
		}
	}
	if !slices.Equal(fresh, []badge.Key{badge.Stars100}) {
		t.Fatalf("new = %v, want stars_100 only", fresh)
	}
}

func TestBadgesAreListedNewestFirstAndInCatalogueOrderWithinAMoment(t *testing.T) {
	f := newTaskFixture(t)
	if _, err := f.db.BookStars(f.h.ID, f.mia.ID, 1, "a", f.mama.ID, bt0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.BookStars(f.h.ID, f.mia.ID, 600, "b", f.mama.ID, bt0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	want := []badge.Key{badge.Stars100, badge.Stars500, badge.FirstStar}
	if got := f.badgeKeys(t, f.mia.ID); !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestAnUpdatedBoxAwardsTheHistoryOnce(t *testing.T) {
	db, path := openTemp(t)
	h, mama := seedParent(t, db)
	mia, _ := db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	if _, err := db.BookStars(h.ID, mia.ID, 5, "x", mama.ID, bt0); err != nil {
		t.Fatal(err)
	}
	// As if the history existed before the update: no badges stored yet.
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`DELETE FROM badges`); err != nil {
		t.Fatal(err)
	}
	later := bt0.Add(48 * time.Hour)
	if err := db.AwardAllBadges(later); err != nil {
		t.Fatal(err)
	}
	list, _ := db.Badges(h.ID, mia.ID)
	if len(list) != 1 || list[0].Key != badge.FirstStar || !list[0].EarnedAt.Equal(later) {
		t.Fatalf("badges = %+v, want first_star dated at the update", list)
	}
	if err := db.AwardAllBadges(later.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if list, _ = db.Badges(h.ID, mia.ID); len(list) != 1 || !list[0].EarnedAt.Equal(later) {
		t.Fatalf("a second run changed %+v", list)
	}
}

func TestBadgesBelongToTheHouseholdsPeople(t *testing.T) {
	f := newTaskFixture(t)
	if _, err := f.db.Badges(f.h.ID, "nobody"); err == nil {
		t.Fatal("badges of an unknown person")
	}
}
