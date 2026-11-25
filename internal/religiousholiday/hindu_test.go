package religiousholiday_test

import (
	"testing"
	"time"

	rh "github.com/hejmo-home/hejmo/internal/religiousholiday"
)

func delhi(t *testing.T) rh.Options {
	t.Helper()
	zone, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	return rh.Options{Place: &rh.Place{Lat: 28.6139, Lon: 77.2090}, Zone: zone}
}

func munich(t *testing.T) rh.Options {
	t.Helper()
	zone, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	return rh.Options{Place: &rh.Place{Lat: 48.137, Lon: 11.575}, Zone: zone}
}

func TestHinduNeedsAHomeLocation(t *testing.T) {
	t.Parallel()
	if hs := rh.Compute(rh.Hindu, rh.Options{}, 2026, 2027); len(hs) != 0 {
		t.Errorf("%d Hindu holidays without a place", len(hs))
	}
}

var publishedHindu = []struct {
	key  string
	want time.Time
}{
	{"makar_sankranti", d(2024, 1, 15)}, {"makar_sankranti", d(2025, 1, 14)}, {"makar_sankranti", d(2026, 1, 14)},
	{"maha_shivaratri", d(2024, 3, 8)}, {"maha_shivaratri", d(2025, 2, 26)}, {"maha_shivaratri", d(2026, 2, 15)},
	{"holi", d(2024, 3, 25)}, {"holi", d(2025, 3, 14)},
	{"rama_navami", d(2024, 4, 17)}, {"rama_navami", d(2025, 4, 6)}, {"rama_navami", d(2026, 3, 26)},
	{"raksha_bandhan", d(2024, 8, 19)}, {"raksha_bandhan", d(2025, 8, 9)}, {"raksha_bandhan", d(2026, 8, 28)},
	{"ganesh_chaturthi", d(2024, 9, 7)}, {"ganesh_chaturthi", d(2025, 8, 27)}, {"ganesh_chaturthi", d(2026, 9, 14)},
	{"navaratri", d(2024, 10, 3)}, {"navaratri", d(2025, 9, 22)}, {"navaratri", d(2026, 10, 11)},
	{"dussehra", d(2024, 10, 12)}, {"dussehra", d(2025, 10, 2)}, {"dussehra", d(2026, 10, 20)},
	{"diwali", d(2024, 10, 31)}, {"diwali", d(2025, 10, 20)}, {"diwali", d(2026, 11, 8)},
}

// The dates of published Panchang calendars (Drik Panchang, for New Delhi) for
// 2024 to 2026, which the computation must reproduce for the place they are
// published for. (Holi 2026 and Raksha Bandhan 2025 depend on the Bhadra
// rule, which is not modelled; the former is left out.)
func TestHinduHolidaysMatchPublishedCalendarsForNewDelhi(t *testing.T) {
	t.Parallel()
	hs := rh.Compute(rh.Hindu, delhi(t), 2024, 2026)
	for _, c := range publishedHindu {
		h := find(t, hs, c.key, c.want.Year())
		if !h.Start.Equal(c.want) {
			t.Errorf("%s %d: %s, want %s", c.key, c.want.Year(), h.Start.Format("2006-01-02"), c.want.Format("2006-01-02"))
		}
		if h.Vary || h.Source != rh.SrcPanchang || h.Unchecked {
			t.Errorf("%s: vary %v source %q unchecked %v", c.key, h.Vary, h.Source, h.Unchecked)
		}
	}
}

// For a place in Germany the day depends on sunrise, sunset and the shifted
// hours there, so it can differ by a day from the one published for India.
func TestHinduHolidaysInMunichAreWithinADayOfNewDelhi(t *testing.T) {
	t.Parallel()
	hs := rh.Compute(rh.Hindu, munich(t), 2024, 2026)
	for _, c := range publishedHindu {
		h := find(t, hs, c.key, c.want.Year())
		if diff := h.Start.Sub(c.want).Hours() / 24; diff < -1 || diff > 1 {
			t.Errorf("%s %d: %s in Munich, %s in New Delhi", c.key, c.want.Year(), h.Start.Format("2006-01-02"), c.want.Format("2006-01-02"))
		}
	}
}

func TestNavaratriHasNineDays(t *testing.T) {
	t.Parallel()
	n := find(t, rh.Compute(rh.Hindu, delhi(t), 2026, 2026), "navaratri", 2026)
	if n.Days() != 9 || !n.End.Equal(d(2026, 10, 19)) {
		t.Errorf("Navaratri 2026: %v to %v", n.Start, n.End)
	}
}
