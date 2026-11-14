package holiday_test

import (
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/holiday"
	"github.com/hejmo-home/hejmo/internal/region"
)

func dates(hs []holiday.Holiday) []string {
	var out []string
	for _, h := range hs {
		out = append(out, h.Date.Format("02.01."))
	}
	return out
}

func TestEaster(t *testing.T) {
	for year, want := range map[int]string{2024: "31.03.", 2025: "20.04.", 2026: "05.04.", 2027: "28.03.", 2038: "25.04."} {
		if got := holiday.Easter(year).Format("02.01."); got != want {
			t.Errorf("Easter(%d) = %s, want %s", year, got, want)
		}
	}
}

// Acceptance (Calendar and sync): Given home region Bavaria, when the calendar
// is shown, then Bavarian holidays are included and no others.
func TestBavaria2026(t *testing.T) {
	got := dates(holiday.For("DE-BY", 2026))
	want := []string{"01.01.", "06.01.", "03.04.", "06.04.", "01.05.", "14.05.", "25.05.", "04.06.", "15.08.", "03.10.", "01.11.", "25.12.", "26.12."}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("DE-BY 2026 = %v\nwant          %v", got, want)
	}
}

func TestOtherGermanStates2026(t *testing.T) {
	cases := map[string][]string{
		// Reformation Day, no Corpus Christi
		"DE-HH": {"01.01.", "03.04.", "06.04.", "01.05.", "14.05.", "25.05.", "03.10.", "31.10.", "25.12.", "26.12."},
		// Women's Day
		"DE-BE": {"01.01.", "08.03.", "03.04.", "06.04.", "01.05.", "14.05.", "25.05.", "03.10.", "25.12.", "26.12."},
		// Day of Repentance and Prayer
		"DE-SN": {"01.01.", "03.04.", "06.04.", "01.05.", "14.05.", "25.05.", "03.10.", "31.10.", "18.11.", "25.12.", "26.12."},
		// Children's Day
		"DE-TH": {"01.01.", "03.04.", "06.04.", "01.05.", "14.05.", "25.05.", "20.09.", "03.10.", "31.10.", "25.12.", "26.12."},
		// Easter and Whit Sunday
		"DE-BB": {"01.01.", "03.04.", "05.04.", "06.04.", "01.05.", "14.05.", "24.05.", "25.05.", "03.10.", "31.10.", "25.12.", "26.12."},
	}
	for code, want := range cases {
		if got := dates(holiday.For(code, 2026)); strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%s 2026 = %v\nwant           %v", code, got, want)
		}
	}
}

func TestAustria2026(t *testing.T) {
	want := []string{"01.01.", "06.01.", "06.04.", "01.05.", "14.05.", "25.05.", "04.06.", "15.08.", "26.10.", "01.11.", "08.12.", "25.12.", "26.12."}
	for _, code := range []string{"AT-9", "AT-7"} {
		if got := dates(holiday.For(code, 2026)); strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%s 2026 = %v", code, got)
		}
	}
}

func TestEveryRegionHasHolidaysWithNames(t *testing.T) {
	for _, r := range region.All() {
		hs := holiday.For(r.Code, 2026)
		if len(hs) < 4 {
			t.Errorf("%s: only %d holidays", r.Code, len(hs))
		}
		for _, h := range hs {
			if h.NameDE == "" || h.NameEN == "" {
				t.Errorf("%s: unnamed holiday on %s", r.Code, h.Date)
			}
		}
	}
	if hs := holiday.For("XX-YY", 2026); hs != nil {
		t.Errorf("unknown region: %v", hs)
	}
}

func TestBetween(t *testing.T) {
	hs := holiday.Between("DE-BY", mustDate("2026-12-20"), mustDate("2027-01-07"))
	if got := strings.Join(dates(hs), " "); got != "25.12. 26.12. 01.01. 06.01." {
		t.Fatalf("Between = %s", got)
	}
}
