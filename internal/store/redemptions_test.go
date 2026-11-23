package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/reward"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

// Ticket 292: asking for, confirming, declining and directly redeeming rewards
// against a real SQLite file.

var rt0 = time.Date(2026, 11, 23, 16, 5, 0, 0, time.UTC)

func (f rewardFixture) stars(t *testing.T, p store.Person, n int) {
	t.Helper()
	if _, err := f.db.BookStars(f.h.ID, p.ID, n, "test", f.mama.ID, rt0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func (f rewardFixture) reward(t *testing.T, r store.Reward) store.Reward {
	t.Helper()
	got, err := f.db.CreateReward(f.h.ID, f.mama.ID, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (f rewardFixture) balance(t *testing.T, p store.Person) int {
	t.Helper()
	acc, err := f.db.StarAccount(f.h.ID, p.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	return acc.Balance
}

func TestAskingSnapshotsTheRewardAndBooksNothing(t *testing.T) {
	f := newRewardFixture(t)
	f.stars(t, f.sebastian, 12)
	rw := f.reward(t, eis())
	r, err := f.db.AskReward(f.h.ID, f.sebastian.ID, rw.ID, rt0, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != store.RedemptionOpen || r.Name != "Eis aus der Eisdiele" || r.Symbol != "🍦" || r.Price != 8 || !r.AskedAt.Equal(rt0) {
		t.Fatalf("request = %+v", r)
	}
	// A later price change does not touch the open request.
	rw.Price = 10
	if _, err := f.db.UpdateReward(f.h.ID, rw, store.KeepPicture, nil); err != nil {
		t.Fatal(err)
	}
	open, _ := f.db.OpenRedemptions(f.h.ID)
	if len(open) != 1 || open[0].Price != 8 {
		t.Fatalf("open = %+v", open)
	}
	if got := f.balance(t, f.sebastian); got != 12 {
		t.Fatalf("balance after asking = %d, want 12", got)
	}
}

func TestAnAskNeedsFreeStarsAndAtMostOneOpenRequestPerRewardAndPerson(t *testing.T) {
	f := newRewardFixture(t)
	f.stars(t, f.sebastian, 12)
	eisR := f.reward(t, eis())
	spiel := f.reward(t, store.Reward{Name: "Spieleabend", Symbol: "🎲", Price: 5})
	if _, err := f.db.AskReward(f.h.ID, f.sebastian.ID, eisR.ID, rt0, time.UTC); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.AskReward(f.h.ID, f.sebastian.ID, eisR.ID, rt0, time.UTC); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second request for the same reward = %v, want a conflict", err)
	}
	var re *store.RedeemError
	if _, err := f.db.AskReward(f.h.ID, f.sebastian.ID, spiel.ID, rt0, time.UTC); !errors.As(err, &re) || re.Check.Reason != reward.NotEnoughStars || re.Check.Missing != 1 {
		t.Fatalf("12 stars, 8 set aside, 5 asked = %v, want one star missing", err)
	}
}

func TestConfirmingBooksTheStarsOnceAndASecondDecisionBooksNothing(t *testing.T) {
	f := newRewardFixture(t)
	f.stars(t, f.sebastian, 12)
	r, _ := f.db.AskReward(f.h.ID, f.sebastian.ID, f.reward(t, eis()).ID, rt0, time.UTC)
	at := rt0.Add(6 * time.Minute)
	done, err := f.db.ConfirmRedemption(f.h.ID, r.ID, f.papa.ID, at, time.UTC)
	if err != nil || done.Status != store.RedemptionConfirmed || done.DecidedBy != f.papa.ID || !done.DecidedAt.Equal(at) {
		t.Fatalf("confirm = %+v %v", done, err)
	}
	if got := f.balance(t, f.sebastian); got != 4 {
		t.Fatalf("balance = %d, want 4", got)
	}
	acc, _ := f.db.StarAccount(f.h.ID, f.sebastian.ID, 20)
	e := acc.Entries[0]
	if e.Delta != -8 || e.RewardName != "Eis aus der Eisdiele" || e.RewardSymbol != "🍦" || e.Direct || e.BookedByName != "Papa" {
		t.Fatalf("log entry = %+v", e)
	}
	_, err = f.db.ConfirmRedemption(f.h.ID, r.ID, f.mama.ID, at.Add(time.Minute), time.UTC)
	var de *store.DecidedError
	if !errors.As(err, &de) || de.Redemption.DecidedByName != "Papa" || !de.Redemption.DecidedAt.Equal(at) {
		t.Fatalf("second decision = %v, want already decided by Papa", err)
	}
	if got := f.balance(t, f.sebastian); got != 4 {
		t.Fatalf("balance after the second decision = %d, want 4", got)
	}
}

func TestConfirmingChecksBalanceAndLimitAgainAndLeavesTheRequestOpen(t *testing.T) {
	f := newRewardFixture(t)
	f.stars(t, f.mia, 9)
	rw := f.reward(t, store.Reward{Name: "Pizza", Symbol: "🍕", Price: 8, Limit: reward.Limit{Period: reward.Day, Count: 1}})
	r, _ := f.db.AskReward(f.h.ID, f.mia.ID, rw.ID, rt0, time.UTC)
	f.stars(t, f.mia, -4) // a task was unticked in between
	var re *store.RedeemError
	if _, err := f.db.ConfirmRedemption(f.h.ID, r.ID, f.mama.ID, rt0, time.UTC); !errors.As(err, &re) || re.Check.Reason != reward.NotEnoughStars || re.Have != 5 {
		t.Fatalf("confirm with 5 stars = %v", err)
	}
	open, _ := f.db.OpenRedemptions(f.h.ID)
	if len(open) != 1 || f.balance(t, f.mia) != 5 {
		t.Fatalf("the request must stay open and nothing be booked: %+v", open)
	}
	f.stars(t, f.mia, 20)
	if _, err := f.db.RedeemNow(f.h.ID, rw.ID, f.mia.ID, f.mama.ID, rt0, time.UTC); err != nil { // confirms the open request
		t.Fatal(err)
	}
	r2, err := f.db.AskReward(f.h.ID, f.mia.ID, rw.ID, rt0, time.UTC)
	if !errors.As(err, &re) || re.Check.Reason != reward.LimitReached {
		t.Fatalf("asking again the same day = %+v %v, want the limit", r2, err)
	}
}

func TestDecliningKeepsTheStarsAndTheNoteAndAChildCanWithdrawOnlyWhileOpen(t *testing.T) {
	f := newRewardFixture(t)
	f.stars(t, f.sebastian, 12)
	rw := f.reward(t, eis())
	r, _ := f.db.AskReward(f.h.ID, f.sebastian.ID, rw.ID, rt0, time.UTC)
	d, err := f.db.DeclineRedemption(f.h.ID, r.ID, f.mama.ID, "Erst am Wochenende", rt0.Add(time.Minute))
	if err != nil || d.Status != store.RedemptionDeclined || d.Note != "Erst am Wochenende" || d.DeclineReason != store.DeclinedByParent {
		t.Fatalf("decline = %+v %v", d, err)
	}
	if f.balance(t, f.sebastian) != 12 {
		t.Fatal("declining must keep the stars")
	}
	var de *store.DecidedError
	if _, err := f.db.WithdrawRedemption(f.h.ID, r.ID, f.sebastian.ID, rt0.Add(2*time.Minute)); !errors.As(err, &de) {
		t.Fatalf("withdrawing a decided request = %v", err)
	}
	r2, _ := f.db.AskReward(f.h.ID, f.sebastian.ID, rw.ID, rt0.Add(time.Hour), time.UTC)
	w, err := f.db.WithdrawRedemption(f.h.ID, r2.ID, f.sebastian.ID, rt0.Add(2*time.Hour))
	if err != nil || w.Status != store.RedemptionWithdrawn {
		t.Fatalf("withdraw = %+v %v", w, err)
	}
	if _, err := f.db.WithdrawRedemption(f.h.ID, r2.ID, f.mia.ID, rt0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("withdrawing somebody else's request = %v", err)
	}
	if _, err := f.db.DeclineRedemption(f.h.ID, r.ID, f.mama.ID, string(make([]byte, 0)), rt0); !errors.As(err, &de) {
		t.Fatalf("declining twice = %v", err)
	}
}

func TestADirectRedemptionBooksNowOrConfirmsTheOpenRequestInsteadOfBookingTwice(t *testing.T) {
	f := newRewardFixture(t)
	f.stars(t, f.sebastian, 30)
	f.stars(t, f.mia, 3)
	kino := f.reward(t, store.Reward{Name: "Kino", Symbol: "🎬", Price: 30})
	var re *store.RedeemError
	if _, err := f.db.RedeemNow(f.h.ID, kino.ID, f.mia.ID, f.mama.ID, rt0, time.UTC); !errors.As(err, &re) || re.Check.Missing != 27 {
		t.Fatalf("direct redemption without enough stars = %v", err)
	}
	r, err := f.db.RedeemNow(f.h.ID, kino.ID, f.sebastian.ID, f.mama.ID, rt0, time.UTC)
	if err != nil || !r.Direct || r.Status != store.RedemptionConfirmed || r.Name != "Kino" {
		t.Fatalf("direct = %+v %v", r, err)
	}
	acc, _ := f.db.StarAccount(f.h.ID, f.sebastian.ID, 5)
	if acc.Balance != 0 || !acc.Entries[0].Direct || acc.Entries[0].Delta != -30 {
		t.Fatalf("account = %+v", acc)
	}
	// With an open request for the same reward the request is confirmed, not a second booking.
	f.stars(t, f.sebastian, 40)
	ask, _ := f.db.AskReward(f.h.ID, f.sebastian.ID, kino.ID, rt0, time.UTC)
	got, err := f.db.RedeemNow(f.h.ID, kino.ID, f.sebastian.ID, f.papa.ID, rt0.Add(time.Minute), time.UTC)
	if err != nil || got.ID != ask.ID || got.Direct {
		t.Fatalf("redeem with an open request = %+v %v, want the request confirmed", got, err)
	}
	if f.balance(t, f.sebastian) != 10 {
		t.Fatalf("balance = %d, want 10 (booked once)", f.balance(t, f.sebastian))
	}
}

func TestArchivingOrDeletingARewardDeclinesItsOpenRequestsWithAReasonTheChildSees(t *testing.T) {
	f := newRewardFixture(t)
	f.stars(t, f.sebastian, 12)
	f.stars(t, f.mia, 12)
	a := f.reward(t, eis())
	b := f.reward(t, store.Reward{Name: "Spieleabend", Symbol: "🎲", Price: 5})
	ra, _ := f.db.AskReward(f.h.ID, f.sebastian.ID, a.ID, rt0, time.UTC)
	rb, _ := f.db.AskReward(f.h.ID, f.mia.ID, b.ID, rt0, time.UTC)
	if _, err := f.db.ArchiveReward(f.h.ID, a.ID, rt0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := f.db.DeleteReward(f.h.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ra.ID, rb.ID} {
		got, err := f.db.Redemption(f.h.ID, id)
		if err != nil || got.Status != store.RedemptionDeclined || got.DeclineReason != store.DeclinedRewardGone {
			t.Fatalf("request %s = %+v %v", id, got, err)
		}
	}
	if open, _ := f.db.OpenRedemptions(f.h.ID); len(open) != 0 {
		t.Fatalf("open = %+v", open)
	}
	if f.balance(t, f.sebastian) != 12 || f.balance(t, f.mia) != 12 {
		t.Fatal("the stars stay")
	}
}

func TestUntickingWithBelowZeroOffDeductsAtMostTheBalanceAndRedeemingNeverGoesBelowZero(t *testing.T) {
	f := newRewardFixture(t)
	task, err := f.db.AddTask(f.h.ID, task.Task{PersonID: f.sebastian.ID, Title: "Zimmer aufräumen", Repeat: task.Once, Date: "2026-11-23", Stars: 3})
	if err != nil {
		t.Fatal(err)
	}
	day := "2026-11-23"
	if err := f.db.SetTaskDone(f.h.ID, task.ID, day, true, f.sebastian.ID, rt0); err != nil {
		t.Fatal(err)
	}
	r, _ := f.db.AskReward(f.h.ID, f.sebastian.ID, f.reward(t, store.Reward{Name: "Kleines Eis", Symbol: "🍦", Price: 2}).ID, rt0, time.UTC)
	if _, err := f.db.ConfirmRedemption(f.h.ID, r.ID, f.mama.ID, rt0, time.UTC); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetBelowZero(f.h.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetTaskDone(f.h.ID, task.ID, day, false, f.mama.ID, rt0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := f.balance(t, f.sebastian); got != 0 {
		t.Fatalf("balance with the switch off = %d, want 0", got)
	}
	h, _ := f.db.Household(f.h.ID)
	if h.BelowZero {
		t.Fatal("the switch must be off")
	}
}

func TestUntickingWithBelowZeroOnMayTakeTheBalanceBelowZero(t *testing.T) {
	f := newRewardFixture(t)
	h, _ := f.db.Household(f.h.ID)
	if !h.BelowZero {
		t.Fatal("below zero is allowed by default")
	}
	task, _ := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: "Zimmer aufräumen", Repeat: task.Once, Date: "2026-11-23", Stars: 3})
	_ = f.db.SetTaskDone(f.h.ID, task.ID, "2026-11-23", true, f.mia.ID, rt0)
	r, _ := f.db.AskReward(f.h.ID, f.mia.ID, f.reward(t, store.Reward{Name: "Kleines Eis", Symbol: "🍦", Price: 3}).ID, rt0, time.UTC)
	if _, err := f.db.ConfirmRedemption(f.h.ID, r.ID, f.mama.ID, rt0, time.UTC); err != nil {
		t.Fatal(err)
	}
	_ = f.db.SetTaskDone(f.h.ID, task.ID, "2026-11-23", false, f.mama.ID, rt0.Add(time.Hour))
	if got := f.balance(t, f.mia); got != -3 {
		t.Fatalf("balance = %d, want -3", got)
	}
}
