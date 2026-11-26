package religiousholiday

import "testing"

// A table entry without a source is a bug: the box says where every date comes
// from (ticket 339).
func TestEveryTableEntryHasASource(t *testing.T) {
	t.Parallel()
	for id, entries := range allTables() {
		for _, e := range entries {
			if e.Source == "" {
				t.Errorf("table %s: entry %s has no source", id, e.Date.Format("2006-01-02"))
			}
			if id != SrcAABF && e.Source != id {
				t.Errorf("table %s: entry %s names source %q", id, e.Date.Format("2006-01-02"), e.Source)
			}
		}
	}
}

func TestTablesAreListedForTheLicencesPage(t *testing.T) {
	t.Parallel()
	got := map[string]TableInfo{}
	for _, ti := range Tables() {
		got[ti.ID] = ti
		if ti.Entries == 0 || ti.From == 0 || ti.To < ti.From {
			t.Errorf("table %s: %+v", ti.ID, ti)
		}
	}
	for _, id := range []string{SrcDiyanet, SrcBahai, SrcSGPC} {
		if _, ok := got[id]; !ok {
			t.Errorf("table %s is not listed", id)
		}
	}
	if _, ok := got[SrcLosar]; ok {
		t.Error("Losar has no checked source: no table")
	}
	for id, want := range map[string][2]int{SrcDiyanet: {2020, 2035}, SrcBahai: {2015, 2064}, SrcSGPC: {2025, 2027}} {
		if ti := got[id]; ti.From != want[0] || ti.To != want[1] || ti.UncheckedEntries != 0 {
			t.Errorf("%s: %+v, want %d to %d, nothing unchecked", id, ti, want[0], want[1])
		}
	}
}

// Every Hijri year of the Diyanet table is a real lunar year: Ramadan has 29 or 30 days, the Eid al-Adha follows
// 67 to 69 days after Eid al-Fitr, and the new year 19 to 21 days after the Eid al-Adha. A mistyped date shows here.
func TestDiyanetRowsHoldTogether(t *testing.T) {
	t.Parallel()
	days := func(a, b Entry) int { return int(b.Date.Sub(a.Date).Hours() / 24) }
	for i, r := range diyanetRows {
		if d := days(r.ramadan, r.shawwal); d != 29 && d != 30 {
			t.Errorf("%d: Ramadan has %d days", r.hy, d)
		}
		if !r.adha.Date.IsZero() {
			if d := days(r.shawwal, r.adha); d < 67 || d > 69 {
				t.Errorf("%d: %d days from Eid al-Fitr to Eid al-Adha", r.hy, d)
			}
		}
		if i+1 < len(diyanetRows) && !r.adha.Date.IsZero() {
			if d := days(r.adha, diyanetRows[i+1].newYear); d < 19 || d > 21 {
				t.Errorf("%d: %d days from Eid al-Adha to the next new year", r.hy, d)
			}
		}
		if !r.newYear.Date.IsZero() {
			if d := days(r.newYear, r.mawlid); d < 68 || d > 71 {
				t.Errorf("%d: %d days from the new year to Mevlid", r.hy, d)
			}
		}
	}
}
