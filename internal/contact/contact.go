// Package contact holds the rules for the address book (ticket 296, ADR 0021)
// that need no database: what a number, an e-mail address and a
// birthday may look like. The rules check; they never rewrite what a person
// typed (a number is kept as typed), except that surrounding spaces go.
package contact

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Limits of the form.
const (
	MaxName     = 40 // the Hejmo name
	MaxFullName = 120
	MaxNote     = 1000
	MaxOwnLabel = 30
	MaxRelation = 40 // the word of a relation (ticket 298)
	MaxLines    = 10 // numbers, and e-mail addresses, per contact
	MinYear     = 1900
)

// Kind is what a number or an e-mail address is for. Custom has a label of
// its own, chosen by the person.
type Kind string

// The kinds. Cell is "Mobil" and Home "Zuhause" for a number, "Privat" for an e-mail address.
const (
	Cell   Kind = "cell"
	Home   Kind = "home"
	Work   Kind = "work"
	Other  Kind = "other"
	Custom Kind = "custom"
)

// NumberKinds and EmailKinds are the kinds the form offers besides "own label".
var (
	NumberKinds = []Kind{Cell, Home, Work, Other}
	EmailKinds  = []Kind{Home, Work, Other}
)

// Line is one number or e-mail address with its kind. Label is set for Custom only.
type Line struct {
	Kind  Kind
	Label string
	Value string
	// E164 is a number in international form, read with the household's home country (ADR 0026); empty when
	// the number cannot be read, and always empty for an e-mail address. Callers do not set it: the store does.
	E164 string
}

// KindValid reports whether k is a known kind.
func KindValid(k Kind) bool {
	switch k {
	case Cell, Home, Work, Other, Custom:
		return true
	}
	return false
}

// CleanLines trims every line and drops those whose value is empty: empty
// lines are never stored.
func CleanLines(in []Line) []Line {
	var out []Line
	for _, l := range in {
		l.Value, l.Label = strings.TrimSpace(l.Value), strings.TrimSpace(l.Label)
		if l.Value == "" {
			continue
		}
		if l.Kind != Custom {
			l.Label = ""
		}
		out = append(out, l)
	}
	return out
}

// Runes counts characters.
func Runes(s string) int { return utf8.RuneCountInString(s) }

// CheckNumber checks a typed number: digits, a + only at the start, spaces and
// - / ( ) and dots, at least 3 digits (so 112 and 116117 pass). It returns ""
// when the number is fine, "char" with the first wrong character, or "digits".
func CheckNumber(v string) (code, bad string) {
	digits := 0
	for i, r := range v {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '+' && i == 0:
		case r == ' ' || r == '-' || r == '/' || r == '(' || r == ')' || r == '.':
		default:
			return "char", string(r)
		}
	}
	if digits < 3 {
		return "digits", ""
	}
	return "", ""
}

// trunkZero is the "(0)" of "+49 (0)171": a trunk prefix that is dropped when dialled from abroad.
var trunkZero = regexp.MustCompile(`\(\s*0\s*\)`)

// TelHref is the tel: link of a typed number: its digits and a leading +,
// nothing else. In international format (a leading +) the "(0)" trunk prefix is dropped.
func TelHref(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "+") {
		v = trunkZero.ReplaceAllString(v, "")
	}
	var b strings.Builder
	b.WriteString("tel:")
	for i, r := range v {
		if (r >= '0' && r <= '9') || (r == '+' && i == 0) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// CheckEmail checks an e-mail address: one @ with something before it, and a
// dot inside the domain. It returns "" when fine, "at" (no or several @, or
// nothing before it), "dot" (no dot in the domain, or at its edge) or "space".
func CheckEmail(v string) string {
	if strings.ContainsAny(v, " \t") {
		return "space"
	}
	local, domain, ok := strings.Cut(v, "@")
	if !ok || local == "" || strings.Contains(domain, "@") {
		return "at"
	}
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "dot"
	}
	return ""
}

// CheckBirthday checks a birthday given as day, month and optional year (0:
// not given). It returns "" when fine (also when nothing is given), "pair"
// (day and month belong together), "date" (such a day does not exist), "year"
// (before 1900 or after this year) or "future".
func CheckBirthday(day, month, year int, today time.Time) string {
	if day == 0 && month == 0 && year == 0 {
		return ""
	}
	if day == 0 || month == 0 {
		return "pair"
	}
	if month < 1 || month > 12 || day < 1 || day > DaysIn(month, year) {
		return "date"
	}
	if year == 0 {
		return ""
	}
	if year < MinYear || year > today.Year() {
		return "year"
	}
	if time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC).After(time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)) {
		return "future"
	}
	return ""
}

// DaysIn is the number of days of a month; without a year (0) February has 29.
func DaysIn(month, year int) int {
	if year == 0 {
		year = 2000 // a leap year: the 29th of February is a possible birthday
	}
	return time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// Age is the age in whole years on the day today, or -1 when the year is unknown.
func Age(day, month, year int, today time.Time) int {
	if year == 0 || day == 0 || month == 0 {
		return -1
	}
	age := today.Year() - year
	if int(today.Month()) < month || (int(today.Month()) == month && today.Day() < day) {
		age--
	}
	return age
}
