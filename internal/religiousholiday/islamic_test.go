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
		if h.Source != rh.SrcDiyanet || !h.Unchecked {
			t.Errorf("%s: source %q unchecked %v (the table was not yet checked against its source)", c.key, h.Source, h.Unchecked)
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
	for y := 2021; y <= 2030; y++ {
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
	if got := rh.TableEnd(rh.Muslim, rh.Options{}); got != 2030 {
		t.Errorf("Diyanet table ends in %d", got)
	}
	if hs := rh.Compute(rh.Muslim, rh.Options{}, 2031, 2040); len(hs) != 0 {
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
