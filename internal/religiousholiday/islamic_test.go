package religiousholiday_test

import (
	"fmt"
	"testing"
	"time"

	hijri "github.com/hablullah/go-hijri"
	rh "github.com/hejmo-home/hejmo/internal/religiousholiday"
)

func TestIslamicHolidaysFollowTheDiyanetTableByDefault(t *testing.T) {
	t.Parallel()
	hs := rh.Compute(rh.Muslim, rh.Options{}, 2027, 2027)
	for _, c := range []struct {
		key      string
		from, to time.Time
		own      rh.CalDate
	}{
		{"ramadan", d(2027, 2, 8), d(2027, 3, 8), rh.CalDate{Cal: "hijri", Day: 1, Month: 9, Year: 1448, EndDay: 29, EndMonth: 9, EndYear: 1448}},
		{"eid_al_fitr", d(2027, 3, 9), d(2027, 3, 11), rh.CalDate{Cal: "hijri", Day: 1, Month: 10, Year: 1448, EndDay: 3, EndMonth: 10, EndYear: 1448}},
		{"eid_al_adha", d(2027, 5, 16), d(2027, 5, 19), rh.CalDate{Cal: "hijri", Day: 10, Month: 12, Year: 1448, EndDay: 13, EndMonth: 12, EndYear: 1448}},
		{"islamic_new_year", d(2027, 6, 6), d(2027, 6, 6), rh.CalDate{Cal: "hijri", Day: 1, Month: 1, Year: 1449, EndDay: 1, EndMonth: 1, EndYear: 1449}},
		{"ashura", d(2027, 6, 15), d(2027, 6, 15), rh.CalDate{Cal: "hijri", Day: 10, Month: 1, Year: 1449, EndDay: 10, EndMonth: 1, EndYear: 1449}},
		{"mawlid", d(2027, 8, 13), d(2027, 8, 13), rh.CalDate{Cal: "hijri", Day: 12, Month: 3, Year: 1449, EndDay: 12, EndMonth: 3, EndYear: 1449}},
		{"lailat_al_qadr", d(2027, 3, 5), d(2027, 3, 5), rh.CalDate{Cal: "hijri", Day: 27, Month: 9, Year: 1448, EndDay: 27, EndMonth: 9, EndYear: 1448}},
	} {
		h := find(t, hs, c.key, 2027)
		if !h.Start.Equal(c.from) || !h.End.Equal(c.to) {
			t.Errorf("%s: %s to %s, want %s to %s", c.key, h.Start.Format("2006-01-02"), h.End.Format("2006-01-02"), c.from.Format("2006-01-02"), c.to.Format("2006-01-02"))
		}
		if h.Own != c.own {
			t.Errorf("%s: own date %+v, want %+v", c.key, h.Own, c.own)
		}
		if !h.Vary {
			t.Errorf("%s: moon sighting not flagged", c.key)
		}
		if h.Source != rh.SrcDiyanet || h.Unchecked {
			t.Errorf("%s: source %q unchecked %v (every entry is checked against the source)", c.key, h.Source, h.Unchecked)
		}
	}
	if h := find(t, hs, "mawlid", 2027); !h.Evening {
		t.Error("Mevlid is a night: the day named is the one the night begins on")
	}
}

func TestUmmAlQuraFollowsTheLibrary(t *testing.T) {
	t.Parallel()
	hs := rh.Compute(rh.Muslim, rh.Options{Islamic: rh.UmmAlQura}, 2027, 2027)
	fitr := find(t, hs, "eid_al_fitr", 2027)
	want := hijri.UmmAlQuraDate{Day: 1, Month: 10, Year: 1448}.ToGregorian()
	if !fitr.Start.Equal(day(want)) {
		t.Errorf("Eid al-Fitr: %v, want %v", fitr.Start, want)
	}
	if fitr.Source != rh.SrcUmmQura || fitr.Unchecked || !fitr.Vary {
		t.Errorf("source %q unchecked %v vary %v", fitr.Source, fitr.Unchecked, fitr.Vary)
	}
	if fitr.Days() != 3 || find(t, hs, "eid_al_adha", 2027).Days() != 4 {
		t.Error("Zuckerfest has 3 days and Opferfest 4")
	}
}

func day(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, time.UTC) }

// Every Diyanet date must be within a day or two of Umm al-Qura: a typo in the table shows here.
func TestDiyanetAndUmmAlQuraNeverDifferByMoreThanADay(t *testing.T) {
	t.Parallel()
	for y := 2021; y <= 2035; y++ {
		a := rh.Compute(rh.Muslim, rh.Options{}, y, y)
		if len(a) == 0 {
			t.Fatalf("%d: no Diyanet holidays", y)
		}
		uq := map[string]rh.Holiday{}
		for _, h := range rh.Compute(rh.Muslim, rh.Options{Islamic: rh.UmmAlQura}, y, y) {
			uq[h.Key+h.Start.Format("2006")+fmt.Sprint(h.Own.Year)] = h
		}
		for _, h := range a {
			other, ok := uq[h.Key+h.Start.Format("2006")+fmt.Sprint(h.Own.Year)]
			if !ok {
				t.Errorf("%d %s: Umm al-Qura has no match", y, h.Key)
				continue
			}
			if diff := h.Start.Sub(other.Start).Hours() / 24; diff < -1 || diff > 1 {
				t.Errorf("%d %s: Diyanet %s, Umm al-Qura %s", y, h.Key, h.Start.Format("2006-01-02"), other.Start.Format("2006-01-02"))
			}
		}
	}
}

func TestDiyanetEndsWhereItsTableEnds(t *testing.T) {
	t.Parallel()
	if got := rh.TableEnd(rh.Muslim, rh.Options{}); got != 2035 {
		t.Errorf("Diyanet table ends in %d", got)
	}
	if hs := rh.Compute(rh.Muslim, rh.Options{}, 2036, 2045); len(hs) != 0 {
		t.Errorf("%d Diyanet holidays after the table", len(hs))
	}
	if got := rh.TableEnd(rh.Muslim, rh.Options{Islamic: rh.UmmAlQura}); got != 0 {
		t.Errorf("Umm al-Qura is computed: table end %d", got)
	}
}

func TestAlevisFollowTheChosenIslamicCalendarForMuharrem(t *testing.T) {
	t.Parallel()
	hs := rh.Compute(rh.Alevi, rh.Options{}, 2027, 2027)
	for _, key := range []string{"hizir_fast", "nevruz", "hidrellez", "muharrem_fast", "ashure"} {
		h := find(t, hs, key, 2027)
		if h.Source != rh.SrcAABF || !h.Unchecked {
			t.Errorf("%s: source %q unchecked %v", key, h.Source, h.Unchecked)
		}
		if wantVary := key == "muharrem_fast" || key == "ashure"; h.Vary != wantVary {
			t.Errorf("%s: vary %v", key, h.Vary)
		}
	}
	if got := find(t, hs, "nevruz", 2027).Start; !got.Equal(d(2027, 3, 21)) {
		t.Errorf("Nevruz: %v", got)
	}
	m := find(t, hs, "muharrem_fast", 2027)
	if !m.Start.Equal(d(2027, 6, 6)) || m.Days() != 12 {
		t.Errorf("Muharrem fast: %v for %d days", m.Start, m.Days())
	}
	uq := rh.Compute(rh.Alevi, rh.Options{Islamic: rh.UmmAlQura}, 2027, 2027)
	if find(t, uq, "muharrem_fast", 2027).Source != rh.SrcAABF {
		t.Error("the fixed days stay AABF whatever the Islamic calendar")
	}
}

// Every date of the Diyanet table as the Diyanet lists give it (2020 and 2021 from the query form, 2022 to 2035 from
// the yearly lists), where it differs from what the table held before ticket 354 (old date in the comment).
func TestDiyanetDatesAreThoseOfTheSource(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		key  string
		year int
		want time.Time
	}{
		{"eid_al_adha", 2020, d(2020, 7, 31)}, // new: Hijri year 1441 was missing
		{"eid_al_fitr", 2020, d(2020, 5, 24)},
		{"ramadan", 2020, d(2020, 4, 24)},
		{"islamic_new_year", 2021, d(2021, 8, 10)}, // old 9 August
		{"mawlid", 2021, d(2021, 10, 17)},          // old 18 October
		{"mawlid", 2024, d(2024, 9, 14)},           // old 15 September
		{"islamic_new_year", 2028, d(2028, 5, 25)}, // old 26 May
		{"mawlid", 2028, d(2028, 8, 2)},            // old 1 August
		{"islamic_new_year", 2029, d(2029, 5, 14)}, // old 15 May
		{"mawlid", 2029, d(2029, 7, 23)},           // old 22 July
		{"ramadan", 2030, d(2030, 1, 5)},           // old 6 January
		{"eid_al_adha", 2030, d(2030, 4, 13)},      // old 14 April
		{"islamic_new_year", 2035, d(2035, 3, 11)},
		{"mawlid", 2035, d(2035, 5, 19)},
		{"ramadan", 2035, d(2035, 11, 1)},
		{"eid_al_fitr", 2035, d(2035, 12, 1)},
	} {
		h := find(t, rh.Compute(rh.Muslim, rh.Options{}, c.year, c.year), c.key, c.year)
		if !h.Start.Equal(c.want) {
			t.Errorf("%s %d: %s, want %s", c.key, c.year, h.Start.Format("2006-01-02"), c.want.Format("2006-01-02"))
		}
	}
}
