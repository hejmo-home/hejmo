package religiousholiday_test

import (
	"testing"
	"time"

	rh "github.com/hejmo-home/hejmo/internal/religiousholiday"
)

// The official Bahá'í calendar of the Bahá'í World Centre gives 2026 (182-183 B.E.) only; no other year is
// checked, so no other year is in the table (ticket 354).
func TestBahaiDaysAreTheOfficialTableOf2026(t *testing.T) {
	t.Parallel()
	hs := rh.Compute(rh.Bahai, rh.Options{}, 2024, 2030)
	for _, c := range []struct {
		key  string
		want time.Time
	}{
		{"naw_ruz", d(2026, 3, 21)},
		{"ridvan_first", d(2026, 4, 21)},
		{"ridvan_ninth", d(2026, 4, 29)},
		{"ridvan_twelfth", d(2026, 5, 2)},
		{"declaration_bab", d(2026, 5, 24)}, // computed before: 23 May
		{"ascension_bahaullah", d(2026, 5, 29)},
		{"martyrdom_bab", d(2026, 7, 10)}, // computed before: 9 July
		{"birth_bab", d(2026, 11, 10)},
		{"birth_bahaullah", d(2026, 11, 11)},
	} {
		h := find(t, hs, c.key, 2026)
		if !h.Start.Equal(c.want) {
			t.Errorf("%s: %s, want %s", c.key, h.Start.Format("2006-01-02"), c.want.Format("2006-01-02"))
		}
		if !h.Eve || h.Vary || h.Unchecked || h.Source != rh.SrcBahai {
			t.Errorf("%s: eve %v vary %v unchecked %v source %q", c.key, h.Eve, h.Vary, h.Unchecked, h.Source)
		}
	}
	if len(hs) != 9 {
		t.Errorf("%d Bahá'í days in 2024 to 2030, want the 9 of 2026 only", len(hs))
	}
	if got := rh.TableEnd(rh.Bahai, rh.Options{}); got != 2026 {
		t.Errorf("the Bahá'í table ends in %d", got)
	}
}

func TestBuddhistDaysAreTheMayAndJulyFullMoonsLosarAndBodhiDay(t *testing.T) {
	t.Parallel()
	zone, _ := time.LoadLocation("Europe/Berlin")
	hs := rh.Compute(rh.Buddhist, rh.Options{Zone: zone}, 2027, 2027)
	if got := find(t, hs, "vesak", 2027).Start; !got.Equal(d(2027, 5, 20)) {
		t.Errorf("Vesak: %v", got)
	}
	if got := find(t, hs, "asalha_puja", 2027).Start; !got.Equal(d(2027, 7, 18)) {
		t.Errorf("Asalha Puja: %v", got)
	}
	if got := find(t, hs, "bodhi_day", 2027).Start; !got.Equal(d(2027, 12, 8)) {
		t.Errorf("Bodhi day: %v", got)
	}
	if got := find(t, hs, "bodhi_day", 2027).Source; got != rh.SrcFixed {
		t.Errorf("Bodhi day is a fixed date, its source is %q", got)
	}
	// No publisher page for Losar was readable (ticket 354): the unchecked table is gone, no Losar in the calendar.
	for _, h := range hs {
		if h.Key == "losar" {
			t.Errorf("Losar %v without a checked source", h.Start)
		}
	}
	if got := rh.TableEnd(rh.Buddhist, rh.Options{}); got != 0 {
		t.Errorf("no Losar table, table end %d", got)
	}
}

// Only what the two Jantris (Nanakshahi 557 and 558, 14 March 2025 to 13 March 2027) say is in the table.
func TestSikhDaysComeFromTheSGPCTableAndStopWhereItStops(t *testing.T) {
	t.Parallel()
	hs := rh.Compute(rh.Sikh, rh.Options{}, 2024, 2035)
	for _, c := range []struct {
		key  string
		year int
		want time.Time
	}{
		{"hola_mohalla", 2025, d(2025, 3, 15)}, // old: 14 March
		{"vaisakhi", 2025, d(2025, 4, 13)},     // old: 14 April
		{"bandi_chhor", 2025, d(2025, 10, 21)}, // old: 20 October
		{"guru_nanak", 2025, d(2025, 11, 5)},
		{"guru_gobind_singh", 2025, d(2025, 12, 27)},
		{"vaisakhi", 2026, d(2026, 4, 14)},
		{"bandi_chhor", 2026, d(2026, 11, 8)},
		{"guru_nanak", 2026, d(2026, 11, 24)},
		{"guru_gobind_singh", 2027, d(2027, 1, 15)},
	} {
		h := find(t, hs, c.key, c.year)
		if !h.Start.Equal(c.want) || h.Source != rh.SrcSGPC || h.Unchecked || h.Vary {
			t.Errorf("%s %d: %v source %q unchecked %v vary %v", c.key, c.year, h.Start, h.Source, h.Unchecked, h.Vary)
		}
	}
	if len(hs) != 9 {
		t.Errorf("%d Sikh days, want the 9 the Jantris give", len(hs))
	}
	if got := rh.TableEnd(rh.Sikh, rh.Options{}); got != 2027 {
		t.Errorf("SGPC table ends in %d", got)
	}
}

// Guru Gobind Singh's Prakash Purab of 6 January 2025 is in the Jantri of the year before, which the box has not
// checked: only the one of 27 December 2025 is left.
func TestSGPCTableHasOnlyThePrakashPurabOf27December2025(t *testing.T) {
	t.Parallel()
	var got []string
	for _, h := range rh.Compute(rh.Sikh, rh.Options{}, 2025, 2025) {
		if h.Key == "guru_gobind_singh" {
			got = append(got, h.Start.Format("2006-01-02"))
		}
	}
	if len(got) != 1 || got[0] != "2025-12-27" {
		t.Errorf("Guru Gobind Singh 2025: %v", got)
	}
}
