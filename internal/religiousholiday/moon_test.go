package religiousholiday_test

import (
	"testing"
	"time"

	rh "github.com/hejmo-home/hejmo/internal/religiousholiday"
)

// The table of the Universal House of Justice for 172-221 B.E. (2015 to 2064; copy at bahai-library.com) is the
// source of the Bahá'í days, checked against the official bahai.org calendar of 2026 (ticket 354).
func TestBahaiDaysAreTheTableOfTheUniversalHouseOfJustice(t *testing.T) {
	t.Parallel()
	hs := rh.Compute(rh.Bahai, rh.Options{}, 2010, 2070)
	if len(hs) != 50*9 {
		t.Errorf("%d Bahá'í days in 2010 to 2070, want the 9 of each of the 50 years 2015 to 2064", len(hs))
	}
	for _, h := range hs {
		if !h.Eve || h.Vary || h.Unchecked || h.Source != rh.SrcBahai {
			t.Errorf("%s %s: eve %v vary %v unchecked %v source %q", h.Key, h.Start.Format("2006-01-02"), h.Eve, h.Vary, h.Unchecked, h.Source)
		}
	}
	// The 2026 edition of bahai.org, day by day.
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
		if h := find(t, hs, c.key, 2026); !h.Start.Equal(c.want) {
			t.Errorf("%s: %s, want %s", c.key, h.Start.Format("2006-01-02"), c.want.Format("2006-01-02"))
		}
	}
	// The days that moved against the computed dates beyond the rule of the two offsets.
	for _, c := range []struct {
		key  string
		year int
		want time.Time
	}{
		{"birth_bab", 2053, d(2053, 11, 11)}, // computed before: 13 October
		{"birth_bahaullah", 2053, d(2053, 11, 12)},
		{"naw_ruz", 2059, d(2059, 3, 20)}, // computed before: 21 March
		{"ascension_bahaullah", 2059, d(2059, 5, 28)},
		{"naw_ruz", 2015, d(2015, 3, 21)},
		{"birth_bab", 2015, d(2015, 11, 13)},
		{"martyrdom_bab", 2064, d(2064, 7, 9)},
		{"birth_bahaullah", 2064, d(2064, 11, 11)},
	} {
		if h := find(t, hs, c.key, c.year); !h.Start.Equal(c.want) {
			t.Errorf("%s %d: %s, want %s", c.key, c.year, h.Start.Format("2006-01-02"), c.want.Format("2006-01-02"))
		}
	}
	// Part B of the table: the days after Naw-Rúz (20 or 21 March) are the same Gregorian day in every year that
	// shares Naw-Rúz, and the Twin Holy Birthdays are two following days of October or November.
	for y := 2015; y <= 2064; y++ {
		nr := find(t, hs, "naw_ruz", y).Start
		if nr.Month() != time.March || (nr.Day() != 20 && nr.Day() != 21) {
			t.Errorf("%d: Naw-Rúz %s", y, nr.Format("2006-01-02"))
		}
		for key, off := range map[string]int{"ridvan_first": 31, "ridvan_ninth": 39, "ridvan_twelfth": 42, "declaration_bab": 64, "ascension_bahaullah": 69, "martyrdom_bab": 111} {
			if got := find(t, hs, key, y).Start; !got.Equal(nr.AddDate(0, 0, off)) {
				t.Errorf("%s %d: %s, want %d days after Naw-Rúz", key, y, got.Format("2006-01-02"), off)
			}
		}
		b, u := find(t, hs, "birth_bab", y).Start, find(t, hs, "birth_bahaullah", y).Start
		if !u.Equal(b.AddDate(0, 0, 1)) || (b.Month() != time.October && b.Month() != time.November) {
			t.Errorf("%d: birthdays %s and %s", y, b.Format("2006-01-02"), u.Format("2006-01-02"))
		}
	}
	if got := rh.TableEnd(rh.Bahai, rh.Options{}); got != 2064 {
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
