package always_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/always"
	"github.com/hejmo-home/hejmo/internal/list"
)

func day(s string) time.Time {
	d, err := time.Parse(always.DateLayout, s)
	if err != nil {
		panic(err)
	}
	return d
}

// 16/11/2026 is a Monday.
func rule(every int, weekday int, start, since string) always.Rule {
	return always.Rule{ArticleID: "milk", Enabled: true, ListID: "l1", Weekday: weekday, Every: every, Start: start, Since: since}
}

func TestARuleIsDueOnItsWeekdayInEveryNthWeekFromTheStartWeek(t *testing.T) {
	r := rule(2, 1, "2026-11-16", "2026-11-01")
	for d, want := range map[string]bool{
		"2026-11-16": true,  // start week, Monday
		"2026-11-17": false, // wrong weekday
		"2026-11-23": false, // off week
		"2026-11-30": true,
		"2027-01-11": true,  // across the year turn: weeks count from the start
		"2026-11-09": false, // before the start week
	} {
		if got := r.Due(day(d)); got != want {
			t.Errorf("Due(%s) = %v, want %v", d, got, want)
		}
	}
}

func TestADayTheRuleWasSavedOnIsNotDueThatDayButTheNextTime(t *testing.T) {
	r := rule(1, 1, "2026-11-16", "2026-11-16") // saved on the Monday it is due
	if r.Due(day("2026-11-16")) {
		t.Error("saving on the chosen day starts next time")
	}
	if !r.Due(day("2026-11-23")) {
		t.Error("the next Monday is due")
	}
}

func TestOnlyARunningRuleIsDue(t *testing.T) {
	r := rule(1, 1, "2026-11-16", "2026-11-01")
	r.Enabled = false
	if r.Due(day("2026-11-23")) {
		t.Error("a rule that is off is never due")
	}
	r.Enabled, r.ListID = true, ""
	if r.Due(day("2026-11-23")) || !r.Paused() {
		t.Error("a rule without its list is paused and never due")
	}
}

func TestNextIsTheFirstDueDayFromADayOn(t *testing.T) {
	r := rule(4, 4, "2026-12-14", "2026-11-16") // every 4 weeks on Thursday, from week of 14/12
	got, ok := r.Next(day("2026-11-16"))
	if !ok || got.Format(always.DateLayout) != "2026-12-17" {
		t.Errorf("Next = %v %v", got, ok)
	}
	got, _ = r.Next(day("2026-12-18"))
	if got.Format(always.DateLayout) != "2027-01-14" {
		t.Errorf("Next after the first = %v", got)
	}
	if _, ok := (always.Rule{}).Next(day("2026-11-16")); ok {
		t.Error("an empty rule has no next day")
	}
}

func TestARuleChecksItsFields(t *testing.T) {
	good := rule(1, 1, "2026-11-16", "")
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for field, mut := range map[string]func(*always.Rule){
		"weekday": func(r *always.Rule) { r.Weekday = 0 },
		"every":   func(r *always.Rule) { r.Every = 0 },
		"every2":  func(r *always.Rule) { r.Every = 53 },
		"start":   func(r *always.Rule) { r.Start = "monday" },
		"qty":     func(r *always.Rule) { r.Qty = list.Quantity{Base: -1, Unit: "l"} },
	} {
		r := good
		mut(&r)
		if r.Validate() == nil {
			t.Errorf("%s: want an error", field)
		}
	}
	var fe *always.FieldError
	r := good
	r.Weekday = 9
	if err := r.Validate(); !asField(err, &fe) || fe.Field != "weekday" {
		t.Errorf("err = %v", err)
	}
}

func asField(err error, fe **always.FieldError) bool {
	e, ok := err.(*always.FieldError)
	*fe = e
	return ok
}

func TestAHolidayCoversBothDaysAndChecksItsDates(t *testing.T) {
	h := always.Holiday{From: "2026-12-19", To: "2027-01-03"}
	for d, want := range map[string]bool{"2026-12-18": false, "2026-12-19": true, "2027-01-03": true, "2027-01-04": false} {
		if got := h.Covers(day(d)); got != want {
			t.Errorf("Covers(%s) = %v", d, got)
		}
	}
	today := day("2026-11-16")
	for name, tc := range map[string]struct {
		h    always.Holiday
		code string
	}{
		"no first": {always.Holiday{To: "2026-12-01"}, "from.required"},
		"no last":  {always.Holiday{From: "2026-12-01"}, "to.required"},
		"before":   {always.Holiday{From: "2026-12-19", To: "2026-12-03"}, "to.before"},
		"past":     {always.Holiday{From: "2026-11-01", To: "2026-11-10"}, "to.past"},
	} {
		err := tc.h.Validate(today)
		fe, ok := err.(*always.FieldError)
		if !ok || fe.Code != tc.code {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if err := (always.Holiday{From: "2026-11-14", To: "2026-11-22"}).Validate(today); err != nil {
		t.Errorf("a holiday that runs now is fine: %v", err)
	}
	if !h.Expired(day("2027-01-04")) || h.Expired(day("2027-01-03")) {
		t.Error("a holiday is over after its last day")
	}
}

func TestTheTimeIsAClockTime(t *testing.T) {
	for s, want := range map[string]bool{"03:00": true, "23:59": true, "3:00": false, "24:00": false, "": false, "03:60": false} {
		if always.ValidTime(s) != want {
			t.Errorf("ValidTime(%q) != %v", s, want)
		}
	}
}
