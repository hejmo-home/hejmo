package streak_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/streak"
	"github.com/hejmo-home/hejmo/internal/task"
)

// Week of Mon 9 to Sun 15 November 2026. A routine of Mia with one daily step
// and one step on Mon to Fri.
func routine(created string, done map[string][]string, pauses ...streak.Pause) streak.Routine {
	d := map[string]map[string]bool{}
	for id, days := range done {
		d[id] = map[string]bool{}
		for _, day := range days {
			d[id][day] = true
		}
	}
	return streak.Routine{
		PersonID: "mia", Created: created, Done: d, Pauses: pauses,
		Tasks: []task.Task{
			{ID: "a", Repeat: task.Weekly, Weekdays: []string{"MO", "TU", "WE", "TH", "FR"}, Date: "2026-11-01"},
		},
	}
}

func days(from string, n int) []string {
	t, _ := time.Parse(task.DateLayout, from)
	var out []string
	for i := range n {
		out = append(out, t.AddDate(0, 0, i).Format(task.DateLayout))
	}
	return out
}

func TestDayStates(t *testing.T) {
	t.Parallel()
	r := routine("2026-11-03", map[string][]string{"a": {"2026-11-09", "2026-11-14"}},
		streak.Pause{From: "2026-11-10", To: "2026-11-11", People: []string{"mia"}},
		streak.Pause{From: "2026-11-14", To: "2026-11-14"})
	want := map[string]streak.State{
		"2026-11-02": streak.Before, // before the routine was created
		"2026-11-09": streak.Done,
		"2026-11-10": streak.Paused, // not done on a pause day
		"2026-11-12": streak.Open,   // missed and future look alike
		"2026-11-14": streak.Off,    // Saturday: nothing due, even though ticked and paused
		"2026-11-15": streak.Off,
	}
	for day, state := range want {
		if got := r.State(day); got != state {
			t.Errorf("State(%s) = %s, want %s", day, got, state)
		}
	}
}

func TestADonePauseDayCountsAsDone(t *testing.T) {
	t.Parallel()
	r := routine("2026-11-01", map[string][]string{"a": {"2026-11-10"}}, streak.Pause{From: "2026-11-10", To: "2026-11-10"})
	if got := r.State("2026-11-10"); got != streak.Done {
		t.Fatalf("State = %s, want done", got)
	}
}

func TestAPauseOfAnotherPersonDoesNotPauseThisOne(t *testing.T) {
	t.Parallel()
	r := routine("2026-11-01", nil, streak.Pause{From: "2026-11-10", To: "2026-11-10", People: []string{"sebastian"}})
	if got := r.State("2026-11-10"); got != streak.Open {
		t.Fatalf("State = %s, want open", got)
	}
}

func TestADayIsDoneOnlyWhenEveryDueTaskIsDone(t *testing.T) {
	t.Parallel()
	r := routine("2026-11-01", map[string][]string{"a": {"2026-11-09"}})
	r.Tasks = append(r.Tasks, task.Task{ID: "b", Repeat: task.Daily, Date: "2026-11-01"})
	if got := r.State("2026-11-09"); got != streak.Open {
		t.Fatalf("one of two done: %s", got)
	}
	r.Done["b"] = map[string]bool{"2026-11-09": true}
	if got := r.State("2026-11-09"); got != streak.Done {
		t.Fatalf("both done: %s", got)
	}
	// Saturday: only the daily step is due.
	if got := r.State("2026-11-14"); got != streak.Open {
		t.Fatalf("Saturday with b due: %s", got)
	}
}

func TestWeekCountsTheWholeWeekMondayToSunday(t *testing.T) {
	t.Parallel()
	r := routine("2026-11-01", map[string][]string{"a": {"2026-11-09", "2026-11-10"}})
	w := r.Week("2026-11-11") // a Wednesday
	if w.Cells[0].Date != "2026-11-09" || w.Cells[6].Date != "2026-11-15" {
		t.Fatalf("cells = %v", w.Cells)
	}
	if !w.Cells[2].Today || w.Cells[1].Today {
		t.Errorf("today marker wrong: %v", w.Cells)
	}
	if w.Done != 2 || w.Total != 5 {
		t.Fatalf("done/total = %d/%d, want 2/5", w.Done, w.Total)
	}
	if k, _, _ := w.Summary(); k != "sum" {
		t.Errorf("summary kind = %q", k)
	}
}

func TestWeekDenominatorDropsUndonePauseDaysAndDaysBeforeTheRoutine(t *testing.T) {
	t.Parallel()
	// Created on Wednesday; Thursday paused and not done; Friday paused but done.
	r := routine("2026-11-11", map[string][]string{"a": {"2026-11-11", "2026-11-13"}},
		streak.Pause{From: "2026-11-12", To: "2026-11-13"})
	w := r.Week("2026-11-13")
	if w.Total != 2 || w.Done != 2 { // Wed and Fri
		t.Fatalf("done/total = %d/%d, want 2/2", w.Done, w.Total)
	}
	if k, _, _ := w.Summary(); k != "sum_all" {
		t.Errorf("kind = %q, want sum_all", k)
	}
}

func TestSummaryWording(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		r      streak.Routine
		today  string
		kind   string
		dn, tn int
	}{
		{"none done yet", routine("2026-11-01", nil), "2026-11-09", "sum_zero", 0, 5},
		{"all done", routine("2026-11-01", map[string][]string{"a": days("2026-11-09", 5)}), "2026-11-13", "sum_all", 5, 5},
		{"everything paused", routine("2026-11-01", nil, streak.Pause{From: "2026-11-09", To: "2026-11-15"}), "2026-11-10", "sum_pause", 0, 0},
		{"no due day at all", streak.Routine{PersonID: "mia", Created: "2026-11-01"}, "2026-11-10", "", 0, 0},
	}
	for _, c := range cases {
		k, d, n := c.r.Week(c.today).Summary()
		if k != c.kind || d != c.dn || n != c.tn {
			t.Errorf("%s: got %q %d/%d, want %q %d/%d", c.name, k, d, n, c.kind, c.dn, c.tn)
		}
	}
}

func TestBestStreakSkipsOffDaysAndUndonePauseDays(t *testing.T) {
	t.Parallel()
	// Done Mon 2 to Fri 6 (five days), Mon 9 paused and not done, Tue 10 and Wed 11 done.
	// Saturday and Sunday are off. The run continues over the weekend and the pause: 7.
	done := append(days("2026-11-02", 5), "2026-11-10", "2026-11-11")
	r := routine("2026-11-01", map[string][]string{"a": done}, streak.Pause{From: "2026-11-09", To: "2026-11-09"})
	if got := r.Best("2026-11-11"); got != 7 {
		t.Fatalf("Best = %d, want 7", got)
	}
}

func TestBestStreakEndsAtAMissedDay(t *testing.T) {
	t.Parallel()
	// Done Mon-Wed, missed Thu, done Fri: best is 3 even though Friday is done.
	r := routine("2026-11-01", map[string][]string{"a": {"2026-11-09", "2026-11-10", "2026-11-11", "2026-11-13"}})
	if got := r.Best("2026-11-13"); got != 3 {
		t.Fatalf("Best = %d, want 3", got)
	}
}

func TestBestStreakNeverDropsBecauseTheDayIsStillToCome(t *testing.T) {
	t.Parallel()
	r := routine("2026-11-01", map[string][]string{"a": {"2026-11-09", "2026-11-10"}})
	if got := r.Best("2026-11-11"); got != 2 { // Wednesday is open, not missed
		t.Fatalf("Best = %d, want 2", got)
	}
}

func TestUntickingTodayLowersBothNumbers(t *testing.T) {
	t.Parallel()
	r := routine("2026-11-01", map[string][]string{"a": {"2026-11-09", "2026-11-10"}})
	if w := r.Week("2026-11-10"); w.Done != 2 || w.Best != 2 {
		t.Fatalf("ticked: %d %d", w.Done, w.Best)
	}
	delete(r.Done["a"], "2026-11-10")
	if w := r.Week("2026-11-10"); w.Done != 1 || w.Best != 1 {
		t.Fatalf("unticked: %d %d", w.Done, w.Best)
	}
}

func TestDayOfUsesTheHouseholdTimeZone(t *testing.T) {
	t.Parallel()
	berlin, _ := time.LoadLocation("Europe/Berlin")
	auckland, _ := time.LoadLocation("Pacific/Auckland")
	at := time.Date(2026, 11, 9, 23, 30, 0, 0, time.UTC)
	if got := streak.DayOf(at, berlin); got != "2026-11-10" {
		t.Errorf("Berlin = %s, want 2026-11-10", got)
	}
	if got := streak.DayOf(at, time.UTC); got != "2026-11-09" {
		t.Errorf("UTC = %s", got)
	}
	if got := streak.DayOf(at, auckland); got != "2026-11-10" {
		t.Errorf("Auckland = %s", got)
	}
}

func TestPauseValidation(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		p     streak.Pause
		field string
		code  string
	}{
		{"order", streak.Pause{From: "2026-11-10", To: "2026-11-09"}, "to", "order"},
		{"long", streak.Pause{From: "2026-01-01", To: "2027-01-03"}, "to", "long"},
		{"bad from", streak.Pause{From: "x", To: "2026-11-09"}, "from", "date"},
		{"bad to", streak.Pause{From: "2026-11-09", To: ""}, "to", "date"},
	} {
		err := c.p.Validate()
		pe, ok := err.(*streak.PauseError)
		if !ok || pe.Field != c.field || pe.Code != c.code {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
	if err := (streak.Pause{From: "2026-01-01", To: "2027-01-01"}).Validate(); err != nil { // 366 days
		t.Errorf("366 days: %v", err)
	}
	if err := (streak.Pause{From: "2026-11-09", To: "2026-11-09"}).Validate(); err != nil {
		t.Errorf("one day: %v", err)
	}
}
