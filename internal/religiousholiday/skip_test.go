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
