package religiousholiday_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/holiday"
	"github.com/hejmo-home/hejmo/internal/region"
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
	kept, skipped := rh.Skip(hs, func(_ string, t time.Time) (bool, bool) { p, ok := public[t]; return p, ok })
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

// A public holiday is skipped because it IS the holiday, not because another festival falls on its date.
func TestAFestivalOnTheDateOfAPublicHolidayIsKeptUnlessItIsThatHoliday(t *testing.T) {
	t.Parallel()
	keptOn := func(rel rh.Religion, o rh.Options, region string, key string, day time.Time) (kept, skipped bool) {
		for _, p := range rh.PlanFor([]rh.Religion{rel}, o, region, day.Year(), day.Year()) {
			for _, h := range p.Kept {
				kept = kept || (h.Key == key && h.Start.Equal(day))
			}
			for _, s := range p.Skipped {
				skipped = skipped || (s.Key == key && s.Start.Equal(day))
			}
		}
		return kept, skipped
	}
	for _, c := range []struct {
		name   string
		rel    rh.Religion
		o      rh.Options
		region string
		key    string
		day    time.Time
		kept   bool
	}{
		{"Vesak 2026 is 1 May, Labour Day, in Bavaria", rh.Buddhist, rh.Options{}, "DE-BY", "vesak", d(2026, 5, 1), true},
		{"Vesak 2026 is 1 May, Labour Day, in Hamburg", rh.Buddhist, rh.Options{}, "DE-HH", "vesak", d(2026, 5, 1), true},
		{"Yom Kippur 2033 is 3 October, German Unity Day", rh.Jewish, rh.Options{}, "DE-BY", "yom_kippur", d(2033, 10, 3), true},
		{"Harvest thanksgiving 2027 is 3 October, German Unity Day", rh.Christian, rh.Options{}, "DE-BY", "harvest_thanks", d(2027, 10, 3), true},
		{"Orthodox Theophany on 6 January is no Epiphany of the Catholic calendar", rh.Orthodox, rh.Options{}, "DE-BY", "theophany", d(2027, 1, 6), true},
		{"Christmas is Christmas", rh.Orthodox, rh.Options{}, "DE-BY", "christmas", d(2027, 12, 25), false},
	} {
		kept, skipped := keptOn(c.rel, c.o, c.region, c.key, c.day)
		if kept != c.kept || skipped == c.kept {
			t.Errorf("%s: kept %v, skipped %v, want kept %v", c.name, kept, skipped, c.kept)
		}
	}
}

// Only Christian days are ever public holidays: no other religion loses a day to a public holiday of any region.
func TestNoFestivalOfAnotherReligionIsEverSkipped(t *testing.T) {
	t.Parallel()
	place := &rh.Place{Lat: 48.1371, Lon: 11.5754}
	for _, region := range []string{"DE-BY", "DE-HH", "DE-BE", "AT", "CH-ZH"} {
		for _, rel := range []rh.Religion{rh.Alevi, rh.Bahai, rh.Buddhist, rh.Hindu, rh.Muslim, rh.Jewish, rh.Sikh} {
			for _, p := range rh.PlanFor([]rh.Religion{rel}, rh.Options{Place: place, Zone: time.UTC}, region, 2025, 2035) {
				for _, s := range p.Skipped {
					t.Errorf("%s in %s: %s on %s is left out as a public holiday", rel, region, s.Key, s.Start.Format("2006-01-02"))
				}
			}
		}
	}
}

// The names that say which public holiday is which festival are the names of package holiday: a renamed holiday
// there must not turn a skipped day into a doubled one.
func TestEveryPublicNameOfAFestivalIsAHolidayOfSomeRegion(t *testing.T) {
	t.Parallel()
	known := map[string]bool{}
	for _, r := range region.All() {
		for _, h := range holiday.For(r.Code, 2027) {
			known[h.NameDE] = true
		}
	}
	for _, key := range []string{"epiphany", "good_friday", "easter", "easter_monday", "ascension", "pentecost", "whit_monday", "corpus_christi",
		"assumption", "reformation", "all_saints", "repentance_day", "christmas_days", "christmas"} {
		names := rh.PublicNames(key)
		if len(names) == 0 {
			t.Errorf("%s has no public holiday names", key)
		}
		found := false
		for _, n := range names {
			found = found || known[n]
		}
		if !found {
			t.Errorf("%s: none of %v is the name of a public holiday of any region", key, names)
		}
	}
}
