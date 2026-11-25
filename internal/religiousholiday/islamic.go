package religiousholiday

import (
	"time"

	hijri "github.com/hablullah/go-hijri"
)

// A Hijri year of the Diyanet table: the first day of Muharram, the evening of
// Mevlid Kandili (the night before 12 Rabi' al-awwal), the first day of
// Ramadan and of Shawwal, and the 10th of Dhu al-Hijja (Kurban Bayrami). The
// Diyanet İşleri Başkanlığı publishes these in its religious days table. They
// were entered without the table at hand, so every one is Unchecked until it
// is checked against the source (ticket 339: the product owner decides on the
// download).
type diyanetRow struct {
	hy                                      int
	newYear, mawlid, ramadan, shawwal, adha Entry
}

func diyanet(hy int, newYear, mawlid, ramadan, shawwal, adha string) diyanetRow {
	x := func(iso string) Entry { return e(SrcDiyanet, iso, true) }
	return diyanetRow{hy, x(newYear), x(mawlid), x(ramadan), x(shawwal), x(adha)}
}

var diyanetRows = []diyanetRow{
	diyanet(1442, "2020-08-20", "2020-10-28", "2021-04-13", "2021-05-13", "2021-07-20"),
	diyanet(1443, "2021-08-09", "2021-10-18", "2022-04-02", "2022-05-02", "2022-07-09"),
	diyanet(1444, "2022-07-30", "2022-10-07", "2023-03-23", "2023-04-21", "2023-06-28"),
	diyanet(1445, "2023-07-19", "2023-09-26", "2024-03-11", "2024-04-10", "2024-06-16"),
	diyanet(1446, "2024-07-07", "2024-09-15", "2025-03-01", "2025-03-30", "2025-06-06"),
	diyanet(1447, "2025-06-26", "2025-09-03", "2026-02-19", "2026-03-20", "2026-05-27"),
	diyanet(1448, "2026-06-16", "2026-08-24", "2027-02-08", "2027-03-09", "2027-05-16"),
	diyanet(1449, "2027-06-06", "2027-08-13", "2028-01-28", "2028-02-26", "2028-05-05"),
	diyanet(1450, "2028-05-26", "2028-08-01", "2029-01-16", "2029-02-14", "2029-04-24"),
	diyanet(1451, "2029-05-15", "2029-07-22", "2030-01-06", "2030-02-04", "2030-04-14"),
}

func diyanetEntries() []Entry {
	var out []Entry
	for _, r := range diyanetRows {
		out = append(out, r.newYear, r.mawlid, r.ramadan, r.shawwal, r.adha)
	}
	return out
}

// islamicYear is the dates one Hijri year gives: where the months start.
type islamicYear struct {
	hy                                      int
	newYear, mawlid, ramadan, shawwal, adha time.Time // mawlid is the evening before the 12th of Rabi' al-awwal
	src                                     string
	unchecked                               bool
}

// islamicYears lists the Hijri years of a calendar that reach into the Gregorian years.
func islamicYears(c IslamicCalendar, from, to int) []islamicYear {
	var out []islamicYear
	if c == Diyanet {
		for _, r := range diyanetRows {
			out = append(out, islamicYear{r.hy, r.newYear.Date, r.mawlid.Date, r.ramadan.Date, r.shawwal.Date, r.adha.Date, SrcDiyanet, r.newYear.Unchecked || r.mawlid.Unchecked || r.ramadan.Unchecked || r.shawwal.Unchecked || r.adha.Unchecked})
		}
		return out
	}
	// Umm al-Qura (go-hijri covers until 1500 AH, November 2077): Hijri years that start between the years before and after.
	start := func(hy, month int) time.Time {
		return day(hijri.UmmAlQuraDate{Day: 1, Month: int64(month), Year: int64(hy)}.ToGregorian())
	}
	for hy := hijriYearOf(from) - 2; hy <= hijriYearOf(to)+1 && hy < 1500; hy++ {
		out = append(out, islamicYear{hy, start(hy, 1), start(hy, 3).AddDate(0, 0, 10), start(hy, 9), start(hy, 10), start(hy, 12).AddDate(0, 0, 9), SrcUmmQura, false})
	}
	return out
}

// hijriYearOf is the Hijri year that begins in the Gregorian year or just before it.
func hijriYearOf(y int) int { return int(float64(y-622)*33/32) + 1 }

func hijriCal(d, m, y, ed, em, ey int) CalDate {
	return CalDate{Cal: "hijri", Day: d, Month: m, Year: y, EndDay: ed, EndMonth: em, EndYear: ey}
}

// islamicHolidays makes the holidays of one Hijri year; all of them depend on moon sighting.
func islamicHolidays(y islamicYear) []Holiday {
	mk := func(key string, from, to time.Time, own CalDate) Holiday {
		return Holiday{Key: key, Start: from, End: to, Vary: true, Own: own, Source: y.src, Unchecked: y.unchecked}
	}
	nRamadan := int(y.shawwal.Sub(y.ramadan).Hours() / 24) // 29 or 30 days
	qadr := mk("lailat_al_qadr", y.ramadan.AddDate(0, 0, 25), y.ramadan.AddDate(0, 0, 25), hijriCal(27, 9, y.hy, 27, 9, y.hy))
	qadr.Evening = true
	mawlid := mk("mawlid", y.mawlid, y.mawlid, hijriCal(12, 3, y.hy, 12, 3, y.hy))
	mawlid.Evening = true
	ramadan := mk("ramadan", y.ramadan, y.shawwal.AddDate(0, 0, -1), hijriCal(1, 9, y.hy, nRamadan, 9, y.hy))
	ramadan.ShowOwn = true
	newYear := mk("islamic_new_year", y.newYear, y.newYear, hijriCal(1, 1, y.hy, 1, 1, y.hy))
	newYear.ShowOwn = true
	return []Holiday{
		newYear,
		mk("ashura", y.newYear.AddDate(0, 0, 9), y.newYear.AddDate(0, 0, 9), hijriCal(10, 1, y.hy, 10, 1, y.hy)),
		mawlid, ramadan, qadr,
		mk("eid_al_fitr", y.shawwal, y.shawwal.AddDate(0, 0, 2), hijriCal(1, 10, y.hy, 3, 10, y.hy)),
		mk("eid_al_adha", y.adha, y.adha.AddDate(0, 0, 3), hijriCal(10, 12, y.hy, 13, 12, y.hy)),
	}
}

// muslim lists the Islamic holidays after the calendar the household chose.
func muslim(o Options, from, to int) []Holiday {
	var out []Holiday
	for _, y := range islamicYears(o.islamic(), from, to) {
		out = append(out, inYears(islamicHolidays(y), from, to)...)
	}
	return out
}

// alevi lists the days of the Alevi calendar: fixed days after the Alevi federation AABF,
// and the Muharrem fast and Aşure after the Islamic calendar the household chose.
func alevi(o Options, from, to int) []Holiday {
	var out []Holiday
	fixed := func(key string, from, to time.Time) Holiday {
		return Holiday{Key: key, Start: from, End: to, Source: SrcAABF, Unchecked: true}
	}
	for y := from; y <= to; y++ {
		out = append(out,
			fixed("hizir_fast", date(y, time.February, 13), date(y, time.February, 15)),
			fixed("nevruz", date(y, time.March, 21), date(y, time.March, 21)),
			fixed("hidrellez", date(y, time.May, 6), date(y, time.May, 6)),
		)
	}
	for _, iy := range islamicYears(o.islamic(), from, to) {
		// Twelve days of fast from the 1st of Muharrem; Aşure Günü follows on the 13th.
		fast := Holiday{Key: "muharrem_fast", Start: iy.newYear, End: iy.newYear.AddDate(0, 0, 11), Vary: true, ShowOwn: true,
			Own: hijriCal(1, 1, iy.hy, 12, 1, iy.hy), Source: SrcAABF, Unchecked: true}
		ashure := Holiday{Key: "ashure", Start: iy.newYear.AddDate(0, 0, 12), End: iy.newYear.AddDate(0, 0, 12), Vary: true,
			Own: hijriCal(13, 1, iy.hy, 13, 1, iy.hy), Source: SrcAABF, Unchecked: true}
		out = append(out, inYears([]Holiday{fast, ashure}, from, to)...)
	}
	return out
}
