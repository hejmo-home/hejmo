package nameday

import (
	"encoding/json"
	"os"
	"testing"
)

// TestOrthodoxListsAreCorrectedAgainstTheirSources pins every entry of the Bulgarian and Ukrainian lists and of the
// Slava list whose day the check against the calendars of 2026 corrected (ticket 366), and every entry that no
// source confirmed. Bulgarian: bg-patriarshia.bg/calendar/2026-1 to 2026-12. Ukrainian: the calendar of the OCU for
// 2026 (kalendar_2026.pdf). Serbian: the saint pages of spc.rs/sr/news/kalendar/.
func TestOrthodoxListsAreCorrectedAgainstTheirSources(t *testing.T) {
	moved := map[List]map[string]string{ // new id -> new day
		BulgarianOrthodox: {
			"sawa-01-12": "01-12", "kiril-05-11": "05-11", "metodi-05-11": "05-11", "wladimir-07-15": "07-15",
			"romil-01-16": "01-16", "gawrail-03-26": "03-26",
		},
		UkrainianOrthodox: {
			"iwan-09-26": "09-26", "maksym-05-14": "05-14", "walentyna-02-10": "02-10", "switlana-03-20": "03-20",
			"wenedykt-03-14": "03-14", "wenjamin-03-31": "03-31",
		},
	}
	gone := map[List][]string{
		BulgarianOrthodox: {"sawa-01-14", "kiril-05-24", "metodi-05-24", "wladimir-11-21", "romil-10-01", "gawrail-11-08",
			"georgi-11-03", "maksim-03-09", "rafail-11-08"},
		UkrainianOrthodox: {"iwan-10-09", "maksym-03-09", "walentyna-07-23", "switlana-02-20", "wenedykt-01-14",
			"wenjamin-10-02", "oleksandr-11-23", "oleksandra-11-23"},
	}
	for l, m := range moved {
		c, ok := Get(l)
		if !ok {
			t.Fatal(l)
		}
		for id, want := range m {
			if e, ok := c.Entry(id); !ok || e.Day != want || e.Unchecked {
				t.Errorf("%s/%s: %+v %v, want day %s", l, id, e, ok, want)
			}
		}
	}
	for l, ids := range gone {
		c, _ := Get(l)
		for _, id := range ids {
			if _, ok := c.Entry(id); ok {
				t.Errorf("%s/%s: should be corrected or removed", l, id)
			}
		}
	}
	// With Nevsky gone (the calendar of the OCU does not name him), Alexander of Constantinople is the only Alexander.
	uk, _ := Get(UkrainianOrthodox)
	if e, ok := uk.Entry("oleksandr-08-30"); !ok || !e.Usual {
		t.Errorf("oleksandr-08-30: %+v %v, want the usual day", e, ok)
	}
	if m, ok := uk.Resolve("Oleksandr Kowalenko", "Sascha"); !ok || len(m.Entries) != 1 || m.Entries[0].ID != "oleksandr-08-30" {
		t.Errorf("Oleksandr: %+v %v", m, ok)
	}
}

func TestSlavaListIsCorrectedAgainstItsSource(t *testing.T) {
	l := Slavas()
	for id, want := range map[string]string{"vasilije": "04-29", "ivanjdan": "06-24"} { // Julian days: they were the Gregorian ones
		if e, ok := l.Entry(id); !ok || e.Day != want {
			t.Errorf("%s: %+v %v, want day %s", id, e, ok, want)
		}
	}
	for _, id := range []string{"ignjatije", "stefan", "andrija", "varvara", "spiridon"} { // no page of the source names them
		if _, ok := l.Entry(id); ok {
			t.Errorf("%s: no source confirms it, so it is removed", id)
		}
	}
	if o, _ := l.Entry("ivanjdan"); !o.SlavaOn(2026).Date.Equal(day(2026, 7, 7)) {
		t.Errorf("Ivanjdan 2026: %+v", o.SlavaOn(2026))
	}
}

// The Russian list has no source we can read (patriarchia.ru is behind a bot check), so it is not offered until
// ticket 374 has checked it: it is not a list of this version, not in the binary, and its data waits unchecked.
func TestRussianListIsWithheldUntilItIsChecked(t *testing.T) {
	for _, l := range Lists() {
		if l == RussianOrthodox {
			t.Fatal("the Russian list is offered")
		}
	}
	if RussianOrthodox.Valid() || RussianOrthodox.Orthodox() {
		t.Error("the Russian list is a list of this version")
	}
	if _, ok := Get(RussianOrthodox); ok {
		t.Error("the Russian list is loaded")
	}
	entries, err := files.ReadDir("data")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "russian_orthodox.json" {
			t.Error("the Russian data is embedded, so it would ship in a release")
		}
	}
	raw, err := os.ReadFile("pending/russian_orthodox.json")
	if err != nil {
		t.Fatalf("the Russian data is kept for ticket 374: %v", err)
	}
	var in struct {
		List    string  `json:"list"`
		Entries []Entry `json:"entries"`
	}
	if err := json.Unmarshal(raw, &in); err != nil || in.List != "russian_orthodox" || len(in.Entries) < 40 {
		t.Fatalf("pending list: %v %q %d entries", err, in.List, len(in.Entries))
	}
	for _, e := range in.Entries {
		if !e.Unchecked {
			t.Errorf("%s: the withheld list is unchecked and must say so", e.ID)
		}
	}
}
