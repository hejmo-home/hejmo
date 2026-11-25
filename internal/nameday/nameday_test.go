package nameday

import (
	"testing"
	"time"
)

const testList = `{
 "list": "greek_orthodox",
 "sources": {"cog": "Calendar of the Church of Greece"},
 "entries": [
  {"id":"thomas-apostle","name":"Thomas","saint":"Apostle Thomas","day":"07-03","usual":true,"source":"cog"},
  {"id":"thomas-aquinas","name":"Thomas","saint":"Thomas Aquinas","day":"01-28","source":"cog"},
  {"id":"sophia","name":"Sophia","variants":["Sophie","Sofia"],"saint":"Sophia","day":"09-17","source":"cog"},
  {"id":"charlotte","name":"Charlotte","saint":"Charlotte","day":"07-17","source":"cog"},
  {"id":"jonas","name":"Jonas","gloss":"Jona","variants":["Jona"],"saint":"Jonah","day":"09-21","source":"cog"},
  {"id":"gertrud","name":"Gertrud","variants":["Gertrude"],"saint":"Gertrude","day":"03-17","source":"cog"},
  {"id":"georgios","name":"Georgios","variants":["Georg","George","Jorgos"],"saint":"Great Martyr George","day":"04-23","move":"easter_monday","source":"cog"},
  {"id":"thomas-sunday","name":"Thomas","saint":"Thomas Sunday","easter":7,"source":"cog"},
  {"id":"jose","name":"José","saint":"Joseph","day":"03-19","source":"cog"},
  {"id":"juergen","name":"Jürgen","saint":"Georg","day":"04-23","source":"cog"}
 ]
}`

func testCal(t *testing.T) *Calendar {
	t.Helper()
	c, err := Parse([]byte(testList))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestOrthodoxEaster(t *testing.T) {
	for year, want := range map[int]string{
		2020: "2020-04-19", 2023: "2023-04-16", 2024: "2024-05-05", 2025: "2025-04-20",
		2026: "2026-04-12", 2027: "2027-05-02", 2028: "2028-04-16", 2029: "2029-04-08", 2030: "2030-04-28",
	} {
		if got := OrthodoxEaster(year).Format("2006-01-02"); got != want {
			t.Errorf("OrthodoxEaster(%d) = %s, want %s", year, got, want)
		}
	}
}

func TestResolveFirstGivenName(t *testing.T) {
	c := testCal(t)
	cases := []struct {
		full, hejmo string
		name        string
		part        int
		ok          bool
	}{
		{"Thomas Maier", "Papa", "Thomas", 0, true},
		{"", "Gertrud", "Gertrud", 0, true},
		{"Charlotte-Sophie Maier", "Charlotte-Sophie", "Charlotte", 1, true}, // the whole name is not in the list: the first part is
		{"Mia Maier", "Mia", "", 0, false},
		{"Dr. Thomas Maier", "Tom", "Thomas", 0, true}, // a title is not a given name
		{"Kinderarztpraxis Dr. Okonkwo-Schmidt", "Kinderarzt", "", 0, false},
		{"jose Garcia", "x", "jose", 0, true}, // case and accent
		{"JÓSE Garcia", "x", "JÓSE", 0, true}, // case and accent
		{"Juergen Müller", "x", "", 0, false}, // "Juergen" is not recorded as a form of Jürgen
		{"Jürgen Müller", "x", "Jürgen", 0, true},
	}
	for _, tc := range cases {
		m, ok := c.Resolve(tc.full, tc.hejmo)
		if ok != tc.ok {
			t.Errorf("Resolve(%q, %q) ok = %v, want %v", tc.full, tc.hejmo, ok, tc.ok)
			continue
		}
		if ok && (m.Part != tc.part && tc.part != -1) {
			t.Errorf("Resolve(%q, %q) part = %d", tc.full, tc.hejmo, m.Part)
		}
	}
	// Double names: the whole name first, then each part in order.
	m, ok := c.Resolve("Charlotte-Sophie Maier", "Charlotte-Sophie")
	if !ok || m.Part != 1 || m.Entries[0].ID != "charlotte" || m.Matched != "Charlotte" || m.Parts != 2 {
		t.Fatalf("double name: %+v %v", m, ok)
	}
	m, ok = c.Resolve("Mia-Sophie Maier", "Mia-Sophie")
	if !ok || m.Part != 2 || m.Entries[0].ID != "sophia" || m.Matched != "Sophie" {
		t.Fatalf("second part: %+v %v", m, ok)
	}
	// A hyphenated name in the list wins over its parts.
	if _, ok := c.Resolve("Mia Maier", "Mia"); ok {
		t.Fatal("Mia has no name day in this list")
	}
}

func TestSameNameManyDays(t *testing.T) {
	c := testCal(t)
	m, ok := c.Resolve("Thomas Maier", "Papa")
	if !ok || len(m.Entries) != 3 {
		t.Fatalf("Thomas has three days here: %+v", m)
	}
	if m.Entries[0].ID != "thomas-apostle" {
		t.Fatalf("the usual day comes first, got %s", m.Entries[0].ID)
	}
}

func TestLookupAndSuggestions(t *testing.T) {
	c := testCal(t)
	if got := c.Lookup("sofie"); got != nil {
		t.Fatalf("Sofie is not a recorded form: %v", got) // folding does not invent letters
	}
	if got := c.Lookup("SOPHIÉ"); len(got) != 1 || got[0].ID != "sophia" {
		t.Fatalf("Lookup(SOPHIÉ) = %v", got) // case and accent do not matter
	}
	names := c.Names()
	for _, want := range []string{"Georgios", "Georg", "Sophie", "Thomas"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("suggestions miss %q: %v", want, names)
		}
	}
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestOccurrenceFixedEasterAndMoved(t *testing.T) {
	c := testCal(t)
	get := func(id string) Entry {
		e, ok := c.Entry(id)
		if !ok {
			t.Fatal(id)
		}
		return e
	}
	// A fixed day repeats every year as it is.
	if o := get("thomas-apostle").On(2027); !o.Date.Equal(day(2027, 7, 3)) || o.Moved || o.EasterDependent {
		t.Fatalf("fixed: %+v", o)
	}
	if !get("thomas-apostle").Yearly() || get("thomas-sunday").Yearly() || get("georgios").Yearly() {
		t.Fatal("only a fixed day without a move rule is a yearly series")
	}
	// Thomas Sunday is the Sunday after Easter.
	if o := get("thomas-sunday").On(2027); !o.Date.Equal(day(2027, 5, 9)) || !o.EasterDependent || !o.Easter.Equal(day(2027, 5, 2)) {
		t.Fatalf("easter offset: %+v", o)
	}
	// 23 April before Easter moves to Easter Monday; after Easter it stays.
	o := get("georgios").On(2027)
	if !o.Moved || !o.Date.Equal(day(2027, 5, 3)) || !o.From.Equal(day(2027, 4, 23)) || !o.Easter.Equal(day(2027, 5, 2)) {
		t.Fatalf("moved: %+v", o)
	}
	o = get("georgios").On(2026) // Easter 12 April
	if o.Moved || !o.Date.Equal(day(2026, 4, 23)) {
		t.Fatalf("not moved: %+v", o)
	}
	// On Easter Sunday itself the day is not before Easter.
	if o := get("georgios").On(2024); !o.Moved {
		t.Fatalf("Easter 2024 is 5 May: 23 April moves: %+v", o)
	}
}

func TestParseRejectsBadData(t *testing.T) {
	for name, raw := range map[string]string{
		"no source":    `{"list":"catholic","sources":{"a":"A"},"entries":[{"id":"x","name":"X","saint":"X","day":"01-01"}]}`,
		"unknown src":  `{"list":"catholic","sources":{"a":"A"},"entries":[{"id":"x","name":"X","saint":"X","day":"01-01","source":"b"}]}`,
		"no day":       `{"list":"catholic","sources":{"a":"A"},"entries":[{"id":"x","name":"X","saint":"X","source":"a"}]}`,
		"bad day":      `{"list":"catholic","sources":{"a":"A"},"entries":[{"id":"x","name":"X","saint":"X","day":"13-01","source":"a"}]}`,
		"duplicate id": `{"list":"catholic","sources":{"a":"A"},"entries":[{"id":"x","name":"X","saint":"X","day":"01-01","source":"a"},{"id":"x","name":"Y","saint":"Y","day":"01-02","source":"a"}]}`,
		"unknown list": `{"list":"nope","sources":{"a":"A"},"entries":[]}`,
	} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestEmbeddedListsAreSound(t *testing.T) {
	for _, l := range Lists() {
		c, ok := Get(l)
		if !ok {
			t.Fatalf("%s: not embedded", l)
		}
		if len(c.Entries) < 40 {
			t.Errorf("%s: only %d entries", l, len(c.Entries))
		}
		for _, e := range c.Entries {
			if e.Source == "" || c.Sources[e.Source] == "" {
				t.Errorf("%s/%s: no source", l, e.ID)
			}
			if !e.Yearly() && l != GreekOrthodox && l != RomanianOrthodox {
				t.Errorf("%s/%s: only the Orthodox lists move with Easter", l, e.ID)
			}
			for y := 2024; y <= 2032; y++ {
				if o := e.On(y); o.Date.IsZero() {
					t.Errorf("%s/%s: no date in %d", l, e.ID, y)
				}
			}
		}
	}
}

func TestEventIDs(t *testing.T) {
	a, b, c := EventID("c1", 0), EventID("c1", 2027), EventID("c2", 0)
	if a == b || a == c || len(a) != 36 || a[14] != '5' {
		t.Fatalf("ids: %s %s %s", a, b, c)
	}
	if a != EventID("c1", 0) {
		t.Fatal("not deterministic")
	}
}
