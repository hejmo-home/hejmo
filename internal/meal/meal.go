// Package meal holds the rules of the shared meal plan (ADR 0015, 0016): the
// mealtimes, the cut-off time per mealtime and the text of an entry. It knows
// nothing about storage or HTTP.
//
// The plan has one free-text entry per day and mealtime. Per-person plans and
// recipes stay out of V1.0, but nothing here blocks them: an entry is a plain
// (day, mealtime, text) value.
package meal

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Mealtime is one of the four times of day a meal can be planned for.
type Mealtime string

// Mealtimes of the plan.
const (
	Breakfast Mealtime = "breakfast"
	Lunch     Mealtime = "lunch"
	Snack     Mealtime = "snack"
	Dinner    Mealtime = "dinner"
)

// Mealtimes lists the mealtimes in the order of the day.
func Mealtimes() []Mealtime { return []Mealtime{Breakfast, Lunch, Snack, Dinner} }

// Valid reports whether m is a known mealtime.
func (m Mealtime) Valid() bool {
	for _, k := range Mealtimes() {
		if m == k {
			return true
		}
	}
	return false
}

// MaxText is the longest entry, counted in characters (api/openapi.yaml).
const MaxText = 200

// InvalidError says which field of the input was refused: a mealtime name
// ("breakfast", "lunch", "snack", "dinner") for a cut-off time, "text" or
// "mealtime" or "date" for an entry.
type InvalidError struct{ Field, Msg string }

func (e *InvalidError) Error() string { return e.Msg }

// CleanText trims an entry and checks it. An empty result is fine: it means
// "nothing planned".
func CleanText(s string) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > MaxText {
		return "", &InvalidError{"text", fmt.Sprintf("a meal is at most %d characters", MaxText)}
	}
	if strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return "", &InvalidError{"text", "a meal is one line of text"}
	}
	return s, nil
}

// EventTitle is the title of a meal's all-day event in the generated calendar:
// "<Mealtime>: <text>". label is the mealtime's name in the household's language.
func EventTitle(label, text string) string { return label + ": " + text }

// Cutoffs holds, per mealtime, the time of day ("HH:MM", 24-hour) from which
// the mealtime is no longer shown on displays (ADR 0016). Empty means no
// cut-off: the mealtime is shown for the rest of the day.
type Cutoffs struct {
	Breakfast, Lunch, Snack, Dinner string
}

// DefaultCutoffs are the defaults of ADR 0016: breakfast 10:00, lunch 13:30,
// snack 16:30, dinner none.
func DefaultCutoffs() Cutoffs {
	return Cutoffs{Breakfast: "10:00", Lunch: "13:30", Snack: "16:30"}
}

// For returns the cut-off time of a mealtime, "" for none.
func (c Cutoffs) For(m Mealtime) string {
	switch m {
	case Breakfast:
		return c.Breakfast
	case Lunch:
		return c.Lunch
	case Snack:
		return c.Snack
	case Dinner:
		return c.Dinner
	}
	return ""
}

// With returns c with the cut-off of one mealtime replaced.
func (c Cutoffs) With(m Mealtime, at string) Cutoffs {
	switch m {
	case Breakfast:
		c.Breakfast = at
	case Lunch:
		c.Lunch = at
	case Snack:
		c.Snack = at
	case Dinner:
		c.Dinner = at
	}
	return c
}

var clock = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// ValidTime reports whether s is a cut-off time: "HH:MM" on a 24-hour clock,
// or empty for none.
func ValidTime(s string) bool { return s == "" || clock.MatchString(s) }

// Validate checks every cut-off; the error names the mealtime that is wrong.
func (c Cutoffs) Validate() error {
	for _, m := range Mealtimes() {
		if !ValidTime(c.For(m)) {
			return &InvalidError{string(m), "a cut-off time is hh:mm on a 24-hour clock, or empty for none"}
		}
	}
	return nil
}
