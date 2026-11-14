package holiday

import "time"

// Swiss holidays per canton. Source: SECO list (Art. 20a ArG, 12.12.2024)
// checked against cantonal law, researched 14/11/2026 (see ADR 0011).
// Counted: canton-wide holidays, including official rest days that are not
// Sunday-equivalent under the Labour Act. Not counted: days observed only in
// some districts or municipalities (AG and FR set most holidays per district),
// and half days (SO, 1 May afternoon).

var allCH = []string{"CH"}

func cantons(codes ...string) []string {
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = "CH-" + c
	}
	return out
}

// nthWeekday returns the n-th weekday wd of month (n >= 1).
func nthWeekday(y int, m time.Month, wd time.Weekday, n int) time.Time {
	d := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	for d.Weekday() != wd {
		d = d.AddDate(0, 0, 1)
	}
	return d.AddDate(0, 0, 7*(n-1))
}

// naefelserFahrt: first Thursday in April; a week later if that is Maundy Thursday.
func naefelserFahrt(y int) time.Time {
	d := nthWeekday(y, time.April, time.Thursday, 1)
	if d.Equal(Easter(y).AddDate(0, 0, -3)) {
		d = d.AddDate(0, 0, 7)
	}
	return d
}

// jeuneGenevois: Thursday after the first Sunday in September.
func jeuneGenevois(y int) time.Time {
	return nthWeekday(y, time.September, time.Sunday, 1).AddDate(0, 0, 4)
}

// bettagsmontag: Monday after the third Sunday in September.
func bettagsmontag(y int) time.Time {
	return nthWeekday(y, time.September, time.Sunday, 3).AddDate(0, 0, 1)
}

var swissRules = []rule{
	{nameDE: "Neujahr", nameEN: "New Year's Day", date: fixed(time.January, 1), regions: allCH},
	{nameDE: "Berchtoldstag", nameEN: "St Berchtold's Day", date: fixed(time.January, 2), regions: cantons("BE", "JU", "TG", "VD")},
	{nameDE: "Berchtoldstag", nameEN: "St Berchtold's Day", date: fixed(time.January, 2), regions: cantons("NE"), when: onMonday},
	{nameDE: "Heilige Drei Könige", nameEN: "Epiphany", date: fixed(time.January, 6), regions: cantons("SZ", "TI", "UR")},
	{nameDE: "Jahrestag der Ausrufung der Republik", nameEN: "Republic Day", date: fixed(time.March, 1), regions: cantons("NE")},
	{nameDE: "Josefstag", nameEN: "St Joseph's Day", date: fixed(time.March, 19), regions: cantons("NW", "SZ", "TI", "UR", "VS")},
	{nameDE: "Näfelser Fahrt", nameEN: "Näfels Pilgrimage", date: naefelserFahrt, regions: cantons("GL")},
	{nameDE: "Karfreitag", nameEN: "Good Friday", date: easterOffset(-2), regions: cantons("AG", "AI", "AR", "BE", "BL", "BS", "FR", "GE", "GL", "GR", "JU", "LU", "NE", "NW", "OW", "SG", "SH", "SO", "SZ", "TG", "UR", "VD", "ZG", "ZH")},
	{nameDE: "Ostermontag", nameEN: "Easter Monday", date: easterOffset(1), regions: cantons("AI", "AR", "BE", "BL", "BS", "GE", "GL", "GR", "JU", "SG", "SH", "SZ", "TG", "TI", "UR", "VD", "ZH")},
	{nameDE: "Tag der Arbeit", nameEN: "Labour Day", date: fixed(time.May, 1), regions: cantons("BL", "BS", "JU", "NE", "SH", "TG", "TI", "ZH")},
	{nameDE: "Auffahrt", nameEN: "Ascension Day", date: easterOffset(39), regions: allCH},
	{nameDE: "Pfingstmontag", nameEN: "Whit Monday", date: easterOffset(50), regions: cantons("AI", "AR", "BE", "BL", "BS", "GE", "GL", "GR", "JU", "SG", "SH", "SZ", "TG", "TI", "UR", "VD", "ZH")},
	{nameDE: "Fronleichnam", nameEN: "Corpus Christi", date: easterOffset(60), regions: cantons("AI", "JU", "LU", "NW", "OW", "SZ", "TI", "UR", "VS", "ZG")},
	{nameDE: "Fête de l'Indépendance jurassienne", nameEN: "Jura Independence Day", date: fixed(time.June, 23), regions: cantons("JU")},
	{nameDE: "Peter und Paul", nameEN: "Saints Peter and Paul", date: fixed(time.June, 29), regions: cantons("TI")},
	{nameDE: "Bundesfeier", nameEN: "Swiss National Day", date: fixed(time.August, 1), regions: allCH},
	{nameDE: "Mariä Himmelfahrt", nameEN: "Assumption Day", date: fixed(time.August, 15), regions: cantons("AI", "JU", "LU", "NW", "OW", "SZ", "TI", "UR", "VS", "ZG")},
	{nameDE: "Jeûne genevois", nameEN: "Geneva Fast", date: jeuneGenevois, regions: cantons("GE")},
	{nameDE: "Bettagsmontag", nameEN: "Federal Fast Monday", date: bettagsmontag, regions: cantons("VD")},
	{nameDE: "Bruder Klaus", nameEN: "St Nicholas of Flüe", date: fixed(time.September, 25), regions: cantons("OW")},
	{nameDE: "Allerheiligen", nameEN: "All Saints' Day", date: fixed(time.November, 1), regions: cantons("AI", "GL", "JU", "LU", "NW", "OW", "SG", "SZ", "TI", "UR", "VS", "ZG")},
	{nameDE: "Mariä Empfängnis", nameEN: "Immaculate Conception", date: fixed(time.December, 8), regions: cantons("AI", "LU", "NW", "OW", "SZ", "TI", "UR", "VS", "ZG")},
	{nameDE: "Weihnachten", nameEN: "Christmas Day", date: fixed(time.December, 25), regions: allCH},
	{nameDE: "Stephanstag", nameEN: "St Stephen's Day", date: fixed(time.December, 26), regions: cantons("BE", "BL", "BS", "GL", "GR", "LU", "SG", "SH", "SZ", "TG", "TI", "UR", "ZH")},
	// AR and AI drop 26.12 when Christmas falls on a Monday or Friday (no three rest days in a row).
	{nameDE: "Stephanstag", nameEN: "St Stephen's Day", date: fixed(time.December, 26), regions: cantons("AI", "AR"), when: christmasMidweek},
	{nameDE: "Stephanstag", nameEN: "St Stephen's Day", date: fixed(time.December, 26), regions: cantons("NE"), when: onMonday},
	{nameDE: "Restauration de la République", nameEN: "Restoration of the Republic", date: fixed(time.December, 31), regions: cantons("GE")},
}

func onMonday(d time.Time) bool { return d.Weekday() == time.Monday }

func christmasMidweek(d time.Time) bool {
	wd := d.AddDate(0, 0, -1).Weekday()
	return wd != time.Monday && wd != time.Friday
}
