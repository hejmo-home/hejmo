package religiousholiday_test

import (
	"testing"
	"time"

	rh "github.com/hejmo-home/hejmo/internal/religiousholiday"
)

func TestBahaiDaysFollowTheEquinoxInTehran(t *testing.T) {
	t.Parallel()
	hs := rh.Compute(rh.Bahai, rh.Options{}, 2026, 2027)
	for _, c := range []struct {
		key  string
		year int
		want time.Time
	}{
		{"naw_ruz", 2026, d(2026, 3, 21)}, // the equinox is after sunset in Tehran
		{"naw_ruz", 2027, d(2027, 3, 21)},
		{"ridvan_first", 2026, d(2026, 4, 21)},
		{"ridvan_ninth", 2026, d(2026, 4, 29)},
		{"ridvan_twelfth", 2026, d(2026, 5, 2)},
		{"declaration_bab", 2026, d(2026, 5, 23)},
		{"ascension_bahaullah", 2026, d(2026, 5, 29)},
		{"martyrdom_bab", 2026, d(2026, 7, 9)},
		{"birth_bab", 2026, d(2026, 11, 10)},
		{"birth_bahaullah", 2026, d(2026, 11, 11)},
	} {
		h := find(t, hs, c.key, c.year)
		if !h.Start.Equal(c.want) {
			t.Errorf("%s %d: %s, want %s", c.key, c.year, h.Start.Format("2006-01-02"), c.want.Format("2006-01-02"))
		}
		if !h.Eve || h.Vary || h.Source != rh.SrcBahai {
			t.Errorf("%s: eve %v vary %v source %q", c.key, h.Eve, h.Vary, h.Source)
		}
	}
	// Earlier years the Twin Holy Birthdays are known: 2024 on 2 and 3 November, 2025 on 22 and 23 October.
	earlier := rh.Compute(rh.Bahai, rh.Options{}, 2024, 2025)
	if got := find(t, earlier, "birth_bab", 2024).Start; !got.Equal(d(2024, 11, 2)) {
		t.Errorf("2024: %v", got)
	}
	if got := find(t, earlier, "birth_bab", 2025).Start; !got.Equal(d(2025, 10, 22)) {
		t.Errorf("2025: %v", got)
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
	losar := find(t, hs, "losar", 2027)
	if !losar.Start.Equal(d(2027, 2, 7)) || losar.Days() != 3 || losar.Source != rh.SrcLosar || !losar.Unchecked {
		t.Errorf("Losar: %v for %d days from %q, unchecked %v", losar.Start, losar.Days(), losar.Source, losar.Unchecked)
	}
	if got := rh.TableEnd(rh.Buddhist, rh.Options{}); got != 2029 {
		t.Errorf("Losar table ends in %d", got)
	}
}

func TestSikhDaysComeFromTheSGPCTableAndStopWhereItStops(t *testing.T) {
	t.Parallel()
	hs := rh.Compute(rh.Sikh, rh.Options{}, 2027, 2035)
	for key, want := range map[string]time.Time{
		"guru_gobind_singh": d(2027, 1, 15), "hola_mohalla": d(2027, 3, 23), "vaisakhi": d(2027, 4, 14),
		"bandi_chhor": d(2027, 10, 29), "guru_nanak": d(2027, 11, 14),
	} {
		h := find(t, hs, key, 2027)
		if !h.Start.Equal(want) || h.Source != rh.SrcSGPC || !h.Unchecked || h.Vary {
			t.Errorf("%s: %v source %q unchecked %v vary %v", key, h.Start, h.Source, h.Unchecked, h.Vary)
		}
	}
	for _, h := range hs {
		if h.Start.Year() > rh.TableEnd(rh.Sikh, rh.Options{}) {
			t.Errorf("%s after the end of the table", h.Key)
		}
	}
	if got := rh.TableEnd(rh.Sikh, rh.Options{}); got != 2027 {
		t.Errorf("SGPC table ends in %d", got)
	}
}
