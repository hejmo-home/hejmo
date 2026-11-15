// Package datefmt writes dates in numbers the way the household wants them
// (ticket #147): day first "24.12.2026" or month first "12/24/2026". It is the
// only place that knows these layouts; machine formats (API, CalDAV, iCalendar)
// stay ISO 8601 and do not come through here. Dates in words are not its job.
package datefmt

import (
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

const nbsp = " "

// Date is the full date: 24.12.2026 or 12/24/2026.
func Date(f store.DateFormat, t time.Time) string {
	if f == store.DateMDY {
		return t.Format("01/02/2006")
	}
	return t.Format("02.01.2006")
}

// Short is the date without its year: 24.12. or 12/24.
func Short(f store.DateFormat, t time.Time) string {
	if f == store.DateMDY {
		return t.Format("01/02")
	}
	return t.Format("02.01.")
}

// RangeParts are the two ends of a range of days: the first without its year,
// the second in full; across New Year both ends carry their year.
func RangeParts(f store.DateFormat, a, b time.Time) (from, to string) {
	if a.Year() != b.Year() {
		return Date(f, a), Date(f, b)
	}
	return Short(f, a), Date(f, b)
}

// Range is the range joined with an en dash.
func Range(f store.DateFormat, a, b time.Time) string {
	from, to := RangeParts(f, a, b)
	return from + " – " + to
}

func clock(tf store.TimeFormat, t time.Time, seconds bool) string {
	switch {
	case tf == store.Time12h && seconds:
		return t.Format("3:04:05") + nbsp + t.Format("PM")
	case tf == store.Time12h:
		return t.Format("3:04") + nbsp + t.Format("PM")
	case seconds:
		return t.Format("15:04:05")
	}
	return t.Format("15:04")
}

// Clock is the time of day in the household's clock; the 12-hour form never
// breaks before AM or PM.
func Clock(tf store.TimeFormat, t time.Time) string { return clock(tf, t, false) }

// ClockSeconds is the time of day with seconds in the household's clock; the
// 12-hour form never breaks before AM or PM.
func ClockSeconds(tf store.TimeFormat, t time.Time) string { return clock(tf, t, true) }

// DateTime is the date and the time, joined by a no-break space so that the two
// never wrap apart.
func DateTime(f store.DateFormat, tf store.TimeFormat, t time.Time) string {
	return Date(f, t) + nbsp + clock(tf, t, false)
}

// DateTimeSeconds is DateTime with seconds: the times of sync runs and sync
// problems, where the second tells which run it was.
func DateTimeSeconds(f store.DateFormat, tf store.TimeFormat, t time.Time) string {
	return Date(f, t) + nbsp + clock(tf, t, true)
}

// When is the date and the time with seconds in one phrase, a comma between
// them ("24.12.2026, 18:30:15"): how overview rows and detail pages say when
// something happened. The no-break space after the comma keeps the two together.
func When(f store.DateFormat, tf store.TimeFormat, t time.Time) string {
	return Date(f, t) + "," + nbsp + clock(tf, t, true)
}
