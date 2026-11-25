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
	for _, id := range []string{SrcDiyanet, SrcLosar, SrcSGPC} {
		if _, ok := got[id]; !ok {
			t.Errorf("table %s is not listed", id)
		}
	}
	if ti := got[SrcDiyanet]; ti.From != 2020 || ti.To != 2030 || ti.UncheckedEntries != ti.Entries {
		t.Errorf("Diyanet: %+v", ti)
	}
}
