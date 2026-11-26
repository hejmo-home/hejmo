package nameday

import (
	"strings"
	"testing"
)

const styleList = `{
 "list": "ukrainian_orthodox",
 "sources": {"ocu": "Calendar of the Orthodox Church of Ukraine"},
 "entries": [
  {"id":"nikolaj","name":"Nikolaj","saint":"Nikolaus von Myra","day":"12-06","usual":true,"source":"ocu"},
  {"id":"jekaterina","name":"Jekaterina","variants":["Екатерина"],"saint":"Katharina","day":"11-24","source":"ocu"},
  {"id":"foma","name":"Foma","saint":"Thomas","easter":7,"source":"ocu"},
  {"id":"georgij","name":"Georgij","saint":"Georg","day":"04-23","move":"easter_monday","source":"ocu"},
  {"id":"leap","name":"Leap","saint":"Leap","day":"02-29","source":"ocu"},
  {"id":"stefan","name":"Stefan","saint":"Stephan","day":"12-27","source":"ocu"}
 ]
}`

func styleCal(t *testing.T) *Calendar {
	t.Helper()
	c, err := Parse([]byte(styleList))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestJulianOffsetPerYear(t *testing.T) {
	for _, tc := range []struct {
		y, m, d int
		want    int
	}{
		{2026, 12, 6, 13}, {2099, 12, 31, 13}, {2100, 1, 1, 13}, {2100, 2, 28, 13},
		{2100, 2, 29, 14}, {2100, 3, 1, 14}, {2100, 12, 6, 14}, {2101, 1, 1, 14}, {2199, 12, 6, 14}, {2200, 2, 28, 14}, {2200, 3, 1, 15},
	} {
		if got := JulianOffset(tc.y, tc.m, tc.d); got != tc.want {
			t.Errorf("JulianOffset(%d-%02d-%02d) = %d, want %d", tc.y, tc.m, tc.d, got, tc.want)
		}
	}
	c := styleCal(t)
	nik, _ := c.Entry("nikolaj")
	if o := nik.OnStyle(2026, Julian); !o.Date.Equal(day(2026, 12, 19)) || !o.Church.Equal(day(2026, 12, 6)) || o.Offset != 13 {
		t.Fatalf("Nikolaus julian 2026: %+v", o)
	}
	if o := nik.OnStyle(2026, RevisedJulian); !o.Date.Equal(day(2026, 12, 6)) {
		t.Fatalf("Nikolaus revised 2026: %+v", o)
	}
	// 14 days from 01/03/2100 (Gregorian), the Julian 29 February 2100 included.
	if o := nik.OnStyle(2100, Julian); !o.Date.Equal(day(2100, 12, 20)) || o.Offset != 14 {
		t.Fatalf("Nikolaus julian 2100: %+v", o)
	}
	leap, _ := c.Entry("leap")
	if o := leap.OnStyle(2100, Julian); !o.Date.Equal(day(2100, 3, 14)) {
		t.Fatalf("Julian 29/02/2100 is 14/03/2100: %+v", o)
	}
	if o := leap.OnStyle(2027, Julian); !o.Date.Equal(day(2027, 3, 13)) { // 28 Feb Julian
		t.Fatalf("29 February in a common year: %+v", o)
	}
	// A day late in December lands in the next Gregorian year.
	st, _ := c.Entry("stefan")
	if o := st.OnStyle(2026, Julian); !o.Date.Equal(day(2027, 1, 9)) {
		t.Fatalf("Stephan julian: %+v", o)
	}
}

func TestEasterDaysAreTheSameInBothStyles(t *testing.T) {
	c := styleCal(t)
	foma, _ := c.Entry("foma")
	for y := 2024; y <= 2040; y++ {
		a, b := foma.OnStyle(y, RevisedJulian), foma.OnStyle(y, Julian)
		if !a.Date.Equal(b.Date) || !a.EasterDependent || !b.EasterDependent || !b.Church.IsZero() {
			t.Fatalf("%d: %+v vs %+v", y, a, b)
		}
	}
}

func TestMoveEasterMondayWorksInBothStyles(t *testing.T) {
	c := styleCal(t)
	g, _ := c.Entry("georgij")
	// 2027: Easter 2 May. 23 April revised is before Easter: moved to 3 May. Julian: 6 May is after Easter: stays.
	if o := g.OnStyle(2027, RevisedJulian); !o.Moved || !o.Date.Equal(day(2027, 5, 3)) {
		t.Fatalf("revised: %+v", o)
	}
	if o := g.OnStyle(2027, Julian); o.Moved || !o.Date.Equal(day(2027, 5, 6)) {
		t.Fatalf("julian: %+v", o)
	}
	// 2024: Easter 5 May. Julian 6 May is after Easter; revised 23 April moves to 6 May.
	if o := g.OnStyle(2024, RevisedJulian); !o.Moved || !o.Date.Equal(day(2024, 5, 6)) {
		t.Fatalf("revised 2024: %+v", o)
	}
	// A year in which Orthodox Easter is 6 May or later: the Julian day (6 May) moves to Easter Monday too.
	for y := 2024; y < 2100; y++ {
		if e := OrthodoxEaster(y); !e.Before(day(y, 5, 6)) {
			if o := g.OnStyle(y, Julian); !o.Moved || !o.Date.Equal(e.AddDate(0, 0, 1)) || !o.From.Equal(day(y, 5, 6)) {
				t.Fatalf("julian %d (Easter %s): %+v", y, e.Format("2006-01-02"), o)
			}
			return
		}
	}
	t.Fatal("no year found")
}

func TestStyleValidity(t *testing.T) {
	if !Julian.Valid() || !RevisedJulian.Valid() || Style("old").Valid() || Style("").Valid() {
		t.Fatal("Valid")
	}
	if ParseStyle("") != RevisedJulian || ParseStyle("julian") != Julian || ParseStyle("x") != RevisedJulian {
		t.Fatal("ParseStyle")
	}
}

func TestListsOrder(t *testing.T) {
	var got []string
	for _, l := range Lists() {
		got = append(got, string(l))
	}
	want := "catholic,protestant,bulgarian_orthodox,greek_orthodox,romanian_orthodox,ukrainian_orthodox"
	if strings.Join(got, ",") != want {
		t.Fatalf("Lists() = %v", got)
	}
}

func TestFollowsCalendar(t *testing.T) {
	for l, want := range map[List]Style{BulgarianOrthodox: RevisedJulian, GreekOrthodox: RevisedJulian, RomanianOrthodox: RevisedJulian} {
		got, ok := l.Calendar()
		if !ok || got != want {
			t.Errorf("%s: %v %v", l, got, ok)
		}
	}
	for _, l := range []List{UkrainianOrthodox, Catholic, Protestant} {
		if _, ok := l.Calendar(); ok {
			t.Errorf("%s has no fixed calendar", l)
		}
	}
	if !BulgarianOrthodox.Orthodox() || !UkrainianOrthodox.Orthodox() || Catholic.Orthodox() {
		t.Fatal("Orthodox")
	}
}

func TestNewListsAreSoundWithSourcesAndCyrillic(t *testing.T) {
	for _, l := range []List{BulgarianOrthodox, UkrainianOrthodox} {
		c, ok := Get(l)
		if !ok {
			t.Fatalf("%s not embedded", l)
		}
		if len(c.Entries) < 40 {
			t.Errorf("%s: %d entries", l, len(c.Entries))
		}
		cyr := 0
		for _, e := range c.Entries {
			if e.Source == "" || c.Sources[e.Source] == "" {
				t.Errorf("%s/%s: no source", l, e.ID)
			}
			for _, n := range append([]string{e.Name}, e.Variants...) {
				if strings.ContainsAny(n, "абвгдеёжзийклмнопрстуфхцчшщъыьэюяєіїґ") {
					cyr++
					break
				}
			}
		}
		if cyr < len(c.Entries)/2 {
			t.Errorf("%s: only %d of %d entries have a Cyrillic spelling", l, cyr, len(c.Entries))
		}
	}
	c, _ := Get(UkrainianOrthodox)
	if m, ok := c.Resolve("Катерина Іванова", "Mama"); !ok || m.Entries[0].Name != "Kateryna" {
		t.Fatalf("Cyrillic name: %+v %v", m, ok)
	}
	if m, ok := c.Resolve("Thomas Maier", "Papa"); !ok || m.Entries[0].ID == "" {
		t.Fatalf("Thomas: %v", ok)
	}
}

func TestParseRejectsAMissingSourceInTheNewLists(t *testing.T) {
	for _, l := range []string{"bulgarian_orthodox", "ukrainian_orthodox"} {
		raw := `{"list":"` + l + `","sources":{"a":"A"},"entries":[{"id":"x","name":"X","saint":"X","day":"01-01"}]}`
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("%s: a missing source must be an error", l)
		}
	}
}
