package badge_test

import (
	"slices"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/badge"
	"github.com/hejmo-home/hejmo/internal/streak"
	"github.com/hejmo-home/hejmo/internal/task"
)

func daysFrom(from string, n int) []string {
	t, _ := time.Parse(task.DateLayout, from)
	var out []string
	for i := range n {
		out = append(out, t.AddDate(0, 0, i).Format(task.DateLayout))
	}
	return out
}

// A routine with one daily step "id", created on created, done on the given days.
func daily(id, created string, done []string, pauses ...streak.Pause) streak.Routine {
	d := map[string]bool{}
	for _, day := range done {
		d[day] = true
	}
	return streak.Routine{
		PersonID: "mia", Created: created, Done: map[string]map[string]bool{id: d}, Pauses: pauses,
		Tasks: []task.Task{{ID: id, Repeat: task.Daily, Date: created}},
	}
}

func has(keys []badge.Key, k badge.Key) bool { return slices.Contains(keys, k) }

func TestTheCatalogueIsTenFixedBadgesInOrder(t *testing.T) {
	t.Parallel()
	want := []badge.Key{"first_routine", "whole_day", "whole_week", "streak_7", "streak_30", "first_star", "stars_100", "stars_500", "first_reward", "goal_reached"}
	if !slices.Equal(badge.Catalogue, want) {
		t.Fatalf("catalogue = %v", badge.Catalogue)
	}
	for _, k := range want {
		if !k.Valid() {
			t.Errorf("%s is not valid", k)
		}
	}
	if badge.Key("gold").Valid() {
		t.Error("an unknown key is valid")
	}
}

func TestNothingYetEarnsNothing(t *testing.T) {
	t.Parallel()
	if got := badge.Earned(badge.Input{Today: "2026-11-25"}); len(got) != 0 {
		t.Fatalf("earned %v", got)
	}
}

func TestFirstRoutineNeedsAWholeRoutineDay(t *testing.T) {
	t.Parallel()
	r := daily("a", "2026-11-20", nil)
	if has(badge.Earned(badge.Input{Today: "2026-11-25", Routines: []streak.Routine{r}}), badge.FirstRoutine) {
		t.Fatal("awarded without a done day")
	}
	r = daily("a", "2026-11-20", []string{"2026-11-22"})
	if !has(badge.Earned(badge.Input{Today: "2026-11-25", Routines: []streak.Routine{r}}), badge.FirstRoutine) {
		t.Fatal("not awarded after a done day")
	}
}

func TestWholeDayNeedsEveryRoutineOfTheDayAndAtLeastTwo(t *testing.T) {
	t.Parallel()
	a := daily("a", "2026-11-20", []string{"2026-11-22"})
	if has(badge.Earned(badge.Input{Today: "2026-11-25", Routines: []streak.Routine{a}}), badge.WholeDay) {
		t.Fatal("one routine is not a whole day")
	}
	b := daily("b", "2026-11-20", nil)
	if has(badge.Earned(badge.Input{Today: "2026-11-25", Routines: []streak.Routine{a, b}}), badge.WholeDay) {
		t.Fatal("one of two done is not a whole day")
	}
	b = daily("b", "2026-11-20", []string{"2026-11-22"})
	if !has(badge.Earned(badge.Input{Today: "2026-11-25", Routines: []streak.Routine{a, b}}), badge.WholeDay) {
		t.Fatal("both done is a whole day")
	}
}

func TestAPausedRoutineDoesNotCountTowardAWholeDay(t *testing.T) {
	t.Parallel()
	p := streak.Pause{From: "2026-11-22", To: "2026-11-22"}
	a := daily("a", "2026-11-20", []string{"2026-11-22"}, p)
	b := daily("b", "2026-11-20", nil, p) // paused and not done: not due that day
	if has(badge.Earned(badge.Input{Today: "2026-11-25", Routines: []streak.Routine{a, b}}), badge.WholeDay) {
		t.Fatal("a paused routine made one routine a whole day")
	}
}

func TestWholeWeekNeedsEveryCountingDayMondayToSunday(t *testing.T) {
	t.Parallel()
	// Mon 9 to Sun 15 November 2026.
	week := daysFrom("2026-11-09", 7)
	r := daily("a", "2026-11-01", week[:6])
	if has(badge.Earned(badge.Input{Today: "2026-11-15", Routines: []streak.Routine{r}}), badge.WholeWeek) {
		t.Fatal("six of seven days is not a whole week")
	}
	r = daily("a", "2026-11-01", week)
	if !has(badge.Earned(badge.Input{Today: "2026-11-15", Routines: []streak.Routine{r}}), badge.WholeWeek) {
		t.Fatal("seven of seven is a whole week")
	}
	// Midweek the rest of the week is still open: not yet.
	if has(badge.Earned(badge.Input{Today: "2026-11-11", Routines: []streak.Routine{daily("a", "2026-11-01", week[:3])}}), badge.WholeWeek) {
		t.Fatal("awarded on Wednesday for a daily routine")
	}
}

func TestWholeWeekDropsPauseDays(t *testing.T) {
	t.Parallel()
	week := daysFrom("2026-11-09", 7)
	r := daily("a", "2026-11-01", append(slices.Clone(week[:2]), week[4:]...), streak.Pause{From: "2026-11-11", To: "2026-11-12"})
	if !has(badge.Earned(badge.Input{Today: "2026-11-15", Routines: []streak.Routine{r}}), badge.WholeWeek) {
		t.Fatal("pause days must drop out of the week")
	}
}

func TestStreakBadgesUseTheBestStreakOfAnyRoutine(t *testing.T) {
	t.Parallel()
	seven := daysFrom("2026-11-01", 7)
	r := daily("a", "2026-11-01", seven)
	got := badge.Earned(badge.Input{Today: "2026-11-25", Routines: []streak.Routine{r}})
	if !has(got, badge.Streak7) || has(got, badge.Streak30) {
		t.Fatalf("seven days: %v", got)
	}
	// Another routine with a shorter run does not matter; the best one counts.
	short := daily("b", "2026-11-01", seven[:2])
	got = badge.Earned(badge.Input{Today: "2026-11-25", Routines: []streak.Routine{short, r}})
	if !has(got, badge.Streak7) {
		t.Fatalf("best of two routines: %v", got)
	}
	thirty := daily("a", "2026-10-01", daysFrom("2026-10-01", 30))
	if got := badge.Earned(badge.Input{Today: "2026-11-25", Routines: []streak.Routine{thirty}}); !has(got, badge.Streak30) {
		t.Fatalf("thirty days: %v", got)
	}
}

func TestPauseDaysDoNotBreakAStreak(t *testing.T) {
	t.Parallel()
	// Done 1-3 and 6-9 November, paused 4-5 and not done: 7 in a row.
	done := append(daysFrom("2026-11-01", 3), daysFrom("2026-11-06", 4)...)
	r := daily("a", "2026-11-01", done, streak.Pause{From: "2026-11-04", To: "2026-11-05"})
	if !has(badge.Earned(badge.Input{Today: "2026-11-10", Routines: []streak.Routine{r}}), badge.Streak7) {
		t.Fatal("a pause broke the streak")
	}
}

func TestStarBadgesCountStarsEarnedInAll(t *testing.T) {
	t.Parallel()
	cases := []struct {
		earned int
		want   []badge.Key
		not    []badge.Key
	}{
		{0, nil, []badge.Key{badge.FirstStar}},
		{1, []badge.Key{badge.FirstStar}, []badge.Key{badge.Stars100}},
		{100, []badge.Key{badge.FirstStar, badge.Stars100}, []badge.Key{badge.Stars500}},
		{500, []badge.Key{badge.FirstStar, badge.Stars100, badge.Stars500}, nil},
	}
	for _, c := range cases {
		got := badge.Earned(badge.Input{Today: "2026-11-25", StarsEarned: c.earned})
		for _, k := range c.want {
			if !has(got, k) {
				t.Errorf("%d stars: missing %s in %v", c.earned, k, got)
			}
		}
		for _, k := range c.not {
			if has(got, k) {
				t.Errorf("%d stars: %s must not be earned", c.earned, k)
			}
		}
	}
}

func TestRewardBadges(t *testing.T) {
	t.Parallel()
	if got := badge.Earned(badge.Input{Today: "2026-11-25", Redeemed: true}); !has(got, badge.FirstReward) || has(got, badge.GoalReached) {
		t.Fatalf("redeemed: %v", got)
	}
	if got := badge.Earned(badge.Input{Today: "2026-11-25", Redeemed: true, GoalRedeemed: true}); !has(got, badge.GoalReached) {
		t.Fatalf("goal redeemed: %v", got)
	}
}

func TestEarnedComesInCatalogueOrder(t *testing.T) {
	t.Parallel()
	got := badge.Earned(badge.Input{Today: "2026-11-25", StarsEarned: 600, Redeemed: true, GoalRedeemed: true})
	want := []badge.Key{badge.FirstStar, badge.Stars100, badge.Stars500, badge.FirstReward, badge.GoalReached}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestReachable(t *testing.T) {
	t.Parallel()
	only := func(r, s, w bool) []badge.Key {
		var out []badge.Key
		for _, k := range badge.Catalogue {
			if badge.Reachable(k, r, s, w) {
				out = append(out, k)
			}
		}
		return out
	}
	if got := only(true, false, false); !slices.Equal(got, badge.Catalogue[:5]) {
		t.Errorf("routine only: %v", got)
	}
	if got := only(false, true, false); !slices.Equal(got, badge.Catalogue[5:8]) {
		t.Errorf("stars only: %v", got)
	}
	if got := only(false, false, true); !slices.Equal(got, badge.Catalogue[8:]) {
		t.Errorf("rewards only: %v", got)
	}
	if got := only(false, false, false); len(got) != 0 {
		t.Errorf("nothing: %v", got)
	}
	if got := only(true, true, true); len(got) != 10 {
		t.Errorf("everything: %v", got)
	}
}

func TestNewMeansEarnedTodayOrYesterdayWhileTheSwitchWasOn(t *testing.T) {
	t.Parallel()
	loc, _ := time.LoadLocation("Europe/Berlin")
	now := time.Date(2026, 11, 25, 9, 0, 0, 0, loc)
	cases := []struct {
		name   string
		earned time.Time
		quiet  bool
		want   bool
	}{
		{"today", time.Date(2026, 11, 25, 0, 5, 0, 0, loc), false, true},
		{"yesterday morning", time.Date(2026, 11, 24, 0, 5, 0, 0, loc), false, true},
		{"two days ago", time.Date(2026, 11, 23, 23, 59, 0, 0, loc), false, false},
		{"today but earned while the switch was off", now, true, false},
		// 23:30 UTC on the 23rd is already the 24th in Berlin: yesterday, not older.
		{"time zone of the household", time.Date(2026, 11, 23, 23, 30, 0, 0, time.UTC), false, true},
	}
	for _, c := range cases {
		if got := badge.New(c.earned, now, c.quiet, loc); got != c.want {
			t.Errorf("%s: New = %v, want %v", c.name, got, c.want)
		}
	}
}
