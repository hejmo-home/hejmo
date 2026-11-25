package religiousholiday_test

import (
	"testing"
	"time"

	rh "github.com/hejmo-home/hejmo/internal/religiousholiday"
)

func TestSkipLeavesOutDaysThatArePublicHolidaysAlreadyIncludingPartialOnes(t *testing.T) {
	t.Parallel()
	public := map[time.Time]bool{ // the value says: partial
		d(2026, 12, 25): false, d(2026, 12, 26): false, d(2026, 8, 15): true,
	}
	hs := []rh.Holiday{
		{Key: "christmas_days", Start: d(2026, 12, 25), End: d(2026, 12, 26)},
		{Key: "assumption", Start: d(2026, 8, 15), End: d(2026, 8, 15)},
		{Key: "christmas_eve", Start: d(2026, 12, 24), End: d(2026, 12, 24)},
		{Key: "half", Start: d(2026, 12, 26), End: d(2026, 12, 27)}, // only one of two days is public
	}
	kept, skipped := rh.Skip(hs, func(t time.Time) (bool, bool) { p, ok := public[t]; return p, ok })
	if len(kept) != 2 || kept[0].Key != "christmas_eve" || kept[1].Key != "half" {
		t.Errorf("kept %v", kept)
	}
	if len(skipped) != 2 || skipped[0].Key != "christmas_days" || skipped[0].Partial || skipped[1].Key != "assumption" || !skipped[1].Partial {
		t.Errorf("skipped %v", skipped)
	}
}

func TestPlanForDropsPublicHolidaysOfTheRegionAndKeepsTheRest(t *testing.T) {
	t.Parallel()
	plans := rh.PlanFor([]rh.Religion{rh.Christian}, rh.Options{}, "DE-BY", 2027, 2027)
	if len(plans) != 1 {
		t.Fatalf("%d plans", len(plans))
	}
	has := func(hs []rh.Holiday, key string) bool {
		for _, h := range hs {
			if h.Key == key {
				return true
			}
		}
		return false
	}
	var skipped []rh.Holiday
	partial := map[string]bool{}
	for _, s := range plans[0].Skipped {
		skipped = append(skipped, s.Holiday)
		partial[s.Key] = s.Partial
	}
	for _, key := range []string{"good_friday", "easter_monday", "ascension", "whit_monday", "corpus_christi", "all_saints", "epiphany", "christmas_days", "assumption"} {
		if !has(skipped, key) || has(plans[0].Kept, key) {
			t.Errorf("%s should be skipped in Bavaria", key)
		}
	}
	if !partial["assumption"] || partial["good_friday"] {
		t.Errorf("only Assumption is a partial holiday in Bavaria: %v", partial)
	}
	for _, key := range []string{"easter", "pentecost", "ash_wednesday", "christmas_eve", "advent1", "st_martin", "reformation"} {
		if !has(plans[0].Kept, key) {
			t.Errorf("%s should be in the calendar", key)
		}
	}
	// Without a region nothing is public.
	if p := rh.PlanFor([]rh.Religion{rh.Christian}, rh.Options{}, "", 2027, 2027); len(p[0].Skipped) != 0 {
		t.Errorf("skipped without a region: %v", p[0].Skipped)
	}
}
