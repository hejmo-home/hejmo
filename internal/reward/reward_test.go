package reward_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/reward"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func at(loc *time.Location, y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, loc)
}

// Limits count confirmed redemptions per person and calendar period.
func TestNoLimitIsNeverReached(t *testing.T) {
	t.Parallel()
	loc := mustLoc(t, "Europe/Berlin")
	now := at(loc, 2026, time.November, 25, 10, 0)
	var many []time.Time
	for i := 0; i < 50; i++ {
		many = append(many, now)
	}
	st := reward.Evaluate(reward.Limit{}, many, now, loc)
	if st.Reached || !st.AvailableAgain.IsZero() {
		t.Fatalf("status = %+v, want open", st)
	}
}

func TestWeekRunsMondayToSundayInTheHouseholdZone(t *testing.T) {
	t.Parallel()
	loc := mustLoc(t, "Europe/Berlin")
	limit := reward.Limit{Period: reward.Week, Count: 1}
	// Wednesday 25/11/2026: redeemed on Monday 23/11 00:00 local, which is Sunday 22:00 UTC.
	now := at(loc, 2026, time.November, 25, 10, 0)
	monday := at(loc, 2026, time.November, 23, 0, 0)
	st := reward.Evaluate(limit, []time.Time{monday.UTC()}, now, loc)
	if !st.Reached {
		t.Fatal("a redemption at Monday 00:00 local counts in this week")
	}
	if want := at(loc, 2026, time.November, 30, 0, 0); !st.AvailableAgain.Equal(want) {
		t.Fatalf("available again = %v, want %v", st.AvailableAgain, want)
	}
	// Sunday 22/11 23:59 local belongs to the week before.
	sunday := at(loc, 2026, time.November, 22, 23, 59)
	if st := reward.Evaluate(limit, []time.Time{sunday}, now, loc); st.Reached {
		t.Fatal("a redemption on the Sunday before must not count")
	}
}

func TestWeekBoundaryUsesHouseholdZoneNotUTC(t *testing.T) {
	t.Parallel()
	loc := mustLoc(t, "Pacific/Auckland") // UTC+13 in November: Monday morning there is still Sunday in UTC
	limit := reward.Limit{Period: reward.Week, Count: 1}
	now := at(loc, 2026, time.November, 24, 9, 0)      // Tuesday
	redeemed := at(loc, 2026, time.November, 23, 1, 0) // Monday 01:00 local = Sunday 12:00 UTC
	if !reward.Evaluate(limit, []time.Time{redeemed}, now, loc).Reached {
		t.Fatal("Monday 01:00 local is in this week")
	}
}

func TestMonthBoundariesIncludingDecemberAndFebruary(t *testing.T) {
	t.Parallel()
	loc := mustLoc(t, "Europe/Berlin")
	limit := reward.Limit{Period: reward.Month, Count: 2}
	now := at(loc, 2026, time.December, 31, 23, 30)
	first := at(loc, 2026, time.December, 1, 0, 0)
	st := reward.Evaluate(limit, []time.Time{first, first.Add(time.Hour)}, now, loc)
	if !st.Reached {
		t.Fatal("two in December reach a limit of two")
	}
	if want := at(loc, 2027, time.January, 1, 0, 0); !st.AvailableAgain.Equal(want) {
		t.Fatalf("available again = %v, want %v", st.AvailableAgain, want)
	}
	nov := at(loc, 2026, time.November, 30, 23, 59)
	if reward.Evaluate(limit, []time.Time{nov, nov, nov}, now, loc).Reached {
		t.Fatal("November redemptions do not count in December")
	}
	feb := at(loc, 2027, time.February, 10, 12, 0)
	st = reward.Evaluate(reward.Limit{Period: reward.Month, Count: 1}, []time.Time{feb}, feb, loc)
	if want := at(loc, 2027, time.March, 1, 0, 0); !st.AvailableAgain.Equal(want) {
		t.Fatalf("february: available again = %v, want %v", st.AvailableAgain, want)
	}
}

func TestDayAcrossDaylightSavingChange(t *testing.T) {
	t.Parallel()
	loc := mustLoc(t, "Europe/Berlin")
	limit := reward.Limit{Period: reward.Day, Count: 1}
	now := at(loc, 2026, time.October, 25, 15, 0) // the day clocks go back: 25 hours
	earlier := at(loc, 2026, time.October, 25, 0, 30)
	st := reward.Evaluate(limit, []time.Time{earlier}, now, loc)
	if !st.Reached {
		t.Fatal("same calendar day")
	}
	if want := at(loc, 2026, time.October, 26, 0, 0); !st.AvailableAgain.Equal(want) {
		t.Fatalf("available again = %v, want %v", st.AvailableAgain, want)
	}
	if reward.Evaluate(limit, []time.Time{at(loc, 2026, time.October, 24, 23, 59)}, now, loc).Reached {
		t.Fatal("yesterday does not count")
	}
}

func TestCountBelowLimitIsNotReached(t *testing.T) {
	t.Parallel()
	loc := mustLoc(t, "Europe/Berlin")
	now := at(loc, 2026, time.November, 25, 10, 0)
	limit := reward.Limit{Period: reward.Week, Count: 2}
	st := reward.Evaluate(limit, []time.Time{now}, now, loc)
	if st.Reached {
		t.Fatal("one of two is not reached")
	}
	if !st.AvailableAgain.IsZero() {
		t.Fatal("no available_again while open")
	}
	if !reward.Evaluate(limit, []time.Time{now, now.Add(-time.Hour)}, now, loc).Reached {
		t.Fatal("two of two is reached")
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	ok := reward.Input{Name: "Eis", Symbol: "🍦", Price: 8, Limit: reward.Limit{Period: reward.Week, Count: 1}}
	if errs := ok.Validate(); len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
	cases := []struct {
		name   string
		mutate func(*reward.Input)
		field  string
	}{
		{"empty name", func(i *reward.Input) { i.Name = "  " }, "name"},
		{"long name", func(i *reward.Input) { i.Name = string(make([]rune, 61)) }, "name"},
		{"price zero", func(i *reward.Input) { i.Price = 0 }, "price"},
		{"price 1000", func(i *reward.Input) { i.Price = 1000 }, "price"},
		{"unknown period", func(i *reward.Input) { i.Limit.Period = "year" }, "limit_period"},
		{"count zero with period", func(i *reward.Input) { i.Limit.Count = 0 }, "limit_count"},
		{"count 100", func(i *reward.Input) { i.Limit.Count = 100 }, "limit_count"},
		{"count without period", func(i *reward.Input) { i.Limit = reward.Limit{Count: 3} }, "limit_count"},
	}
	for _, c := range cases {
		in := ok
		c.mutate(&in)
		errs := in.Validate()
		if len(errs) == 0 || errs[0].Field != c.field {
			t.Errorf("%s: errors = %v, want field %s", c.name, errs, c.field)
		}
	}
	n := reward.Input{Name: " Eis ", Price: 3}.Normalized()
	if n.Name != "Eis" || n.Symbol != reward.DefaultSymbol || n.Limit.Period != reward.NoLimit {
		t.Fatalf("normalized = %+v", n)
	}
}

// Ticket 292: free stars, the check before a redemption, and how many stars
// unticking deducts.

func TestAnAskNeedsThePriceInFreeStarsAfterTheOtherOpenRequests(t *testing.T) {
	cases := []struct {
		balance, reserved, price int
		want                     bool
	}{
		{12, 0, 8, true},
		{12, 8, 4, true},
		{12, 8, 5, false},
		{12, 0, 12, true},
		{-3, 0, 1, false},
	}
	for _, c := range cases {
		if got := reward.Free(c.balance, c.reserved) >= c.price; got != c.want {
			t.Errorf("balance %d reserved %d price %d: affordable = %v, want %v", c.balance, c.reserved, c.price, got, c.want)
		}
	}
	if got := reward.Free(12, 20); got != 0 {
		t.Errorf("free stars never go below zero: %d", got)
	}
}

func TestRedeemingNeedsTheBalanceAndAnUnusedLimitAndNamesTheFirstReason(t *testing.T) {
	loc := time.UTC
	day := time.Date(2026, 11, 23, 12, 0, 0, 0, loc)
	used := []time.Time{day.Add(-time.Hour)}
	lim := reward.Limit{Period: reward.Day, Count: 1}
	if got := reward.CanRedeem(8, 12, lim, nil, day, loc); !got.OK() {
		t.Fatalf("enough stars and a fresh limit: %+v", got)
	}
	got := reward.CanRedeem(8, 5, lim, nil, day, loc)
	if got.OK() || got.Reason != reward.NotEnoughStars || got.Missing != 3 {
		t.Fatalf("5 stars for 8: %+v", got)
	}
	got = reward.CanRedeem(5, 12, lim, used, day, loc)
	if got.Reason != reward.LimitReached || !got.AvailableAgain.Equal(time.Date(2026, 11, 24, 0, 0, 0, 0, loc)) {
		t.Fatalf("limit used up: %+v", got)
	}
}

func TestUntickingDeductsWhatWasCreditedOrAtMostTheBalanceWhenBelowZeroIsOff(t *testing.T) {
	cases := []struct {
		credited, balance int
		below             bool
		want              int
	}{
		{3, 10, true, 3},
		{3, 1, true, 3},
		{3, -2, true, 3},
		{3, 10, false, 3},
		{3, 1, false, 1},
		{3, 0, false, 0},
		{3, -2, false, 0},
	}
	for _, c := range cases {
		if got := reward.Deduction(c.credited, c.balance, c.below); got != c.want {
			t.Errorf("credited %d balance %d belowZero %v: deducts %d, want %d", c.credited, c.balance, c.below, got, c.want)
		}
	}
}
