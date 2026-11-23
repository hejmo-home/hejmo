// Package reward holds the rules of the reward catalogue (ticket 291): what a
// valid reward is and when a person has used up its limit. A limit counts
// confirmed redemptions per person and reward within the current calendar day,
// week (Monday to Sunday) or month in the household's time zone; periods are
// calendar periods, not rolling windows.
package reward

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Limits of the fields.
const (
	MaxName   = 60
	MinPrice  = 1
	MaxPrice  = 999
	MaxCount  = 99
	MaxPeople = 100
	// DefaultSymbol is what a reward shows when nothing else was chosen, so a
	// reward never has no picture: the gift.
	DefaultSymbol = "🎁"
)

// Period is the calendar period a limit counts in.
type Period string

// The periods.
const (
	NoLimit Period = "none"
	Day     Period = "day"
	Week    Period = "week"
	Month   Period = "month"
)

// Valid reports whether p is one of the four periods.
func (p Period) Valid() bool { return p == NoLimit || p == Day || p == Week || p == Month }

// Limit says how often one person may redeem a reward in one period. The zero
// value is no limit; Count is 0 without a period.
type Limit struct {
	Period Period
	Count  int
}

func (l Limit) none() bool { return l.Period == "" || l.Period == NoLimit }

// Status is where a person stands against a limit.
type Status struct {
	// Reached says the limit is used up in the current period.
	Reached bool
	// AvailableAgain is the start of the next period while Reached, else zero.
	AvailableAgain time.Time
}

// Bounds returns the start of the period that holds at and the start of the
// next one, in loc.
func Bounds(p Period, at time.Time, loc *time.Location) (start, next time.Time) {
	t := at.In(loc)
	y, m, d := t.Date()
	switch p {
	case Day:
		return time.Date(y, m, d, 0, 0, 0, 0, loc), time.Date(y, m, d+1, 0, 0, 0, 0, loc)
	case Week:
		back := (int(t.Weekday()) + 6) % 7 // Monday is 0
		return time.Date(y, m, d-back, 0, 0, 0, 0, loc), time.Date(y, m, d-back+7, 0, 0, 0, 0, loc)
	case Month:
		return time.Date(y, m, 1, 0, 0, 0, 0, loc), time.Date(y, m+1, 1, 0, 0, 0, 0, loc)
	}
	return time.Time{}, time.Time{}
}

// Evaluate counts the confirmed redemptions (one time each) of one person that
// fall into the period of now and says whether the limit is used up.
func Evaluate(l Limit, confirmed []time.Time, now time.Time, loc *time.Location) Status {
	if l.none() || l.Count < 1 {
		return Status{}
	}
	start, next := Bounds(l.Period, now, loc)
	n := 0
	for _, at := range confirmed {
		if !at.Before(start) && at.Before(next) {
			n++
		}
	}
	if n < l.Count {
		return Status{}
	}
	return Status{Reached: true, AvailableAgain: next}
}

// Input is what a parent enters for a reward.
type Input struct {
	Name   string
	Symbol string
	Price  int
	Limit  Limit
}

// FieldErr says which field is wrong and why.
type FieldErr struct{ Field, Code string }

func (e FieldErr) Error() string { return e.Field + ": " + e.Code }

// Normalized trims the name, fills the default symbol and the period, and
// clears the count when there is no period.
func (in Input) Normalized() Input {
	in.Name = strings.TrimSpace(in.Name)
	if in.Symbol == "" {
		in.Symbol = DefaultSymbol
	}
	if in.Limit.Period == "" {
		in.Limit.Period = NoLimit
	}
	return in
}

// Validate returns the problems of the normalized input, in field order.
func (in Input) Validate() []FieldErr {
	in = in.Normalized()
	var errs []FieldErr
	switch n := utf8.RuneCountInString(in.Name); {
	case n == 0:
		errs = append(errs, FieldErr{"name", "required"})
	case n > MaxName:
		errs = append(errs, FieldErr{"name", "long"})
	}
	if in.Price < MinPrice || in.Price > MaxPrice {
		errs = append(errs, FieldErr{"price", "range"})
	}
	switch {
	case !in.Limit.Period.Valid():
		errs = append(errs, FieldErr{"limit_period", "unknown"})
	case in.Limit.Period == NoLimit && in.Limit.Count != 0:
		errs = append(errs, FieldErr{"limit_count", "without_period"})
	case in.Limit.Period != NoLimit && (in.Limit.Count < 1 || in.Limit.Count > MaxCount):
		errs = append(errs, FieldErr{"limit_count", "range"})
	}
	return errs
}

// Free is what a person may still ask for: the balance minus the stars set
// aside for their other open requests, never below zero (ticket 292).
func Free(balance, reserved int) int { return max(0, balance-reserved) }

// Reason says why a person cannot redeem a reward now.
type Reason string

// The reasons, the first that applies.
const (
	NoReason       Reason = ""
	NotEnoughStars Reason = "stars"
	LimitReached   Reason = "limit"
)

// Check is the answer of CanRedeem.
type Check struct {
	Reason Reason
	// Missing is the number of stars still missing with NotEnoughStars.
	Missing int
	// AvailableAgain is the start of the next period with LimitReached.
	AvailableAgain time.Time
}

// OK reports that the person can redeem.
func (c Check) OK() bool { return c.Reason == NoReason }

// CanRedeem checks a redemption: the balance must cover the price (redeeming
// never takes it below zero) and the limit of the period must not be used up.
// confirmed are the times of the person's confirmed redemptions of the reward.
func CanRedeem(price, balance int, l Limit, confirmed []time.Time, now time.Time, loc *time.Location) Check {
	if balance < price {
		return Check{Reason: NotEnoughStars, Missing: price - balance}
	}
	if st := Evaluate(l, confirmed, now, loc); st.Reached {
		return Check{Reason: LimitReached, AvailableAgain: st.AvailableAgain}
	}
	return Check{}
}

// Deduction is how many stars unticking a task takes back: all it credited,
// or with below-zero switched off at most what the person has.
func Deduction(credited, balance int, belowZero bool) int {
	if belowZero {
		return credited
	}
	return min(credited, max(0, balance))
}
