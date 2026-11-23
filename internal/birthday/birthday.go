// Package birthday holds the rules of the generated, read-only calendar
// Birthdays (ticket 302): how a contact's birthday becomes a yearly series,
// and when it carries an age. It knows nothing about storage or HTTP.
package birthday

import (
	"crypto/sha1" //nolint:gosec // UUIDv5, see EventID
	"fmt"
)

// UnknownYearStart is the year a series starts in when the birth year is
// unknown. It is a leap year (so 29 February can start a series) before the
// first year a person can be born in (contact.MinYear), so it never clashes
// with a real birth year.
const UnknownYearStart = 1896

// eventNamespace is the UUID namespace of the event ids (the URL namespace of
// RFC 4122, the contact id being the name in it).
var eventNamespace = [16]byte{0x6b, 0xa7, 0xb8, 0x11, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}

// EventID is the id of the event of a contact: a UUIDv5 of the contact id, so
// it is a valid event id in the API (format uuid), the same on every start of
// the box, and the box can rewrite the series without a table of its own.
func EventID(contactID string) string {
	h := sha1.New() //nolint:gosec // UUIDv5 is defined with SHA-1; no security use
	h.Write(eventNamespace[:])
	h.Write([]byte(contactID))
	var b [16]byte
	copy(b[:], h.Sum(nil))
	b[6] = b[6]&0x0f | 0x50 // version 5
	b[8] = b[8]&0x3f | 0x80 // variant RFC 4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

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
