package nameday

import "testing"

func TestSlavaListHasSourcesAndSaints(t *testing.T) {
	l := Slavas()
	if len(l.Entries) < 25 {
		t.Fatalf("%d slavas", len(l.Entries))
	}
	for _, e := range l.Entries {
		if e.Source == "" || l.Sources[e.Source] == "" {
			t.Errorf("%s: no source", e.ID)
		}
		if e.Name == "" || e.Saint == "" || e.SaintDE == "" {
			t.Errorf("%s: name, saint (Serbian) and saint in German are required", e.ID)
		}
		if !e.Unchecked {
			t.Errorf("%s: not checked against the source", e.ID)
		}
	}
	nik, ok := l.Entry("nikola")
	if !ok || nik.Name != "Nikoljdan" || nik.Saint != "Sveti Nikola" || nik.SaintDE != "Nikolaus von Myra" {
		t.Fatalf("nikola: %+v %v", nik, ok)
	}
	if _, ok := l.Entry("spas"); !ok {
		t.Fatal("Spasovdan")
	}
}

func TestSlavaIsJulianWhateverTheHouseholdStyle(t *testing.T) {
	nik, _ := Slavas().Entry("nikola")
	for _, y := range []int{2026, 2027, 2099} {
		if o := nik.SlavaOn(y); !o.Date.Equal(day(y, 12, 19)) || !o.Church.Equal(day(y, 12, 6)) {
			t.Errorf("%d: %+v", y, o)
		}
	}
	if o := nik.SlavaOn(2100); !o.Date.Equal(day(2100, 12, 20)) {
		t.Fatalf("2100: %+v", o)
	}
	spas, _ := Slavas().Entry("spas")
	if o := spas.SlavaOn(2027); !o.Date.Equal(day(2027, 6, 10)) || !o.EasterDependent { // Easter 2 May, +39
		t.Fatalf("Spasovdan 2027: %+v", o)
	}
}

func TestOwnSlava(t *testing.T) {
	for _, tc := range []struct {
		m, d int
		ok   bool
		day  string
	}{{12, 19, true, "12-19"}, {6, 31, false, ""}, {2, 30, false, ""}, {2, 29, true, "02-29"}, {13, 1, false, ""}, {0, 5, false, ""}, {1, 0, false, ""}} {
		day, ok := OwnDay(tc.m, tc.d)
		if ok != tc.ok || day != tc.day {
			t.Errorf("OwnDay(%d,%d) = %q %v", tc.m, tc.d, day, ok)
		}
	}
	if MaxOwnSlavaName != 60 {
		t.Fatal("max 60")
	}
}

func TestSlavaEventIDsAreStableAndNotNameDayIDs(t *testing.T) {
	a, b := SlavaEventID("h1", 0), SlavaEventID("h1", 0)
	if a != b || a == SlavaEventID("h2", 0) || a == SlavaEventID("h1", 2027) || a == EventID("h1", 0) {
		t.Fatalf("ids: %s", a)
	}
}
