// Package birthday holds the rules of the generated, read-only calendar
// Birthdays (ticket 302): how a contact's birthday becomes a yearly series,
// and when it carries an age. It knows nothing about storage or HTTP.
package birthday

import "strings"

// UnknownYearStart is the year a series starts in when the birth year is
// unknown. It is a leap year (so 29 February can start a series) before the
// first year a person can be born in (contact.MinYear), so it never clashes
// with a real birth year.
const UnknownYearStart = 1896

const idPrefix = "birthday-"

// EventID is the id of the event of a contact: one series per contact, so the
// box can rewrite it without a table of its own.
func EventID(contactID string) string { return idPrefix + contactID }

// IsEvent reports whether an event id is that of a birthday series.
func IsEvent(eventID string) bool { return strings.HasPrefix(eventID, idPrefix) }

// ContactID is the contact an event id belongs to.
func ContactID(eventID string) string { return strings.TrimPrefix(eventID, idPrefix) }

// Series says how a birthday repeats: the year the series starts in (the birth
// year, or UnknownYearStart when it is unknown) and its RRULE. 29 February
// repeats on the last day of February in years without one.
func Series(day, month, year int) (startYear int, rrule string) {
	startYear = year
	if year == 0 {
		startYear = UnknownYearStart
	}
	if day == 29 && month == 2 {
		return startYear, "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=-1"
	}
	return startYear, "FREQ=YEARLY"
}

// Age is the age a person turns in the year of an occurrence of a series that
// started in startYear. ok is false when the series started in
// UnknownYearStart: the birth year is unknown, so no age is shown.
func Age(startYear, occurrenceYear int) (age int, ok bool) {
	if startYear == UnknownYearStart {
		return 0, false
	}
	return occurrenceYear - startYear, true
}
