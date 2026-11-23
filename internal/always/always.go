// Package always holds the rules of articles that are always on the list
// (ADR 0019, ticket 168, decisions Q27 and Q43 to Q45): per article a rhythm
// like the meal plan templates (every n weeks, one weekday), the list the
// article goes on and an optional quantity, plus the household's time of day
// and its one holiday. It has no storage and no HTTP.
package always

import (
	"regexp"
	"time"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/meal"
)

// DateLayout is how days are written: 2026-11-16.
const DateLayout = "2006-01-02"

// DefaultAt is the time of day the articles are put on the lists (Q43).
const DefaultAt = "03:00"

// MaxEvery is the longest rhythm, in weeks (as the meal plan templates).
const MaxEvery = meal.MaxInterval

// FieldError names the field that is wrong; Code says how.
type FieldError struct{ Field, Code string }

func (e *FieldError) Error() string { return "always " + e.Field + ": " + e.Code }

// Rule says an article goes on a list on one weekday of every Every-th week.
type Rule struct {
	ArticleID string // key of a shipped article or id of an own one
	Enabled   bool
	// ListID is the list the article goes on; "" when that list was deleted,
	// which pauses the rule.
	ListID, ListName string
	Weekday          int    // 1 (Monday) to 7 (Sunday)
	Every            int    // 1 to MaxEvery weeks
	Start            string // the Monday of the first week
	// Since is the day the rule was set up: it is due only after it, so a rule
	// saved on its own day starts the next time (Q43).
	Since string
	// Qty is how much goes on the list; none for "without quantity".
	Qty list.Quantity
	// Lang is the language the entry's text is written in.
	Lang string
}

// Paused says the rule is on but has no list.
func (r Rule) Paused() bool { return r.Enabled && r.ListID == "" }

// Running says the rule is on and has a list.
func (r Rule) Running() bool { return r.Enabled && r.ListID != "" }

// Validate checks the fields of a rule.
func (r Rule) Validate() error {
	switch {
	case r.Weekday < 1 || r.Weekday > 7:
		return &FieldError{"weekday", "required"}
	case r.Every < 1 || r.Every > MaxEvery:
		return &FieldError{"every", "range"}
	}
	if _, err := time.Parse(DateLayout, r.Start); err != nil {
		return &FieldError{"start", "invalid"}
	}
	if r.Qty.Base < 0 || (r.Qty.Unit == "") != (r.Qty.Base == 0) {
		return &FieldError{"qty", "invalid"}
	}
	return nil
}

func (r Rule) template() meal.Template { return meal.Template{Start: r.Start, Interval: r.Every} }

// Due says whether the rule puts its article on the list on this day.
func (r Rule) Due(day time.Time) bool {
	if !r.Running() || r.Weekday != (int(day.Weekday())+6)%7+1 {
		return false
	}
	if since, err := time.Parse(DateLayout, r.Since); err == nil && !day.After(since) {
		return false
	}
	monday := day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
	return r.template().AppliesInWeek(monday)
}

// Next is the first day from the given one on (included) the rule is due.
// The rule's own state (on, list) is not asked: it is the day the rule would come.
func (r Rule) Next(from time.Time) (time.Time, bool) {
	if r.Validate() != nil {
		return time.Time{}, false
	}
	probe := r
	probe.Enabled, probe.ListID = true, "x"
	for i := 0; i <= (MaxEvery+1)*7*2; i++ {
		d := from.AddDate(0, 0, i)
		if probe.Due(d) {
			return d, true
		}
	}
	return time.Time{}, false
}

// Holiday is the one pause of all rules: both days are included.
type Holiday struct{ From, To string }

// On says a holiday is entered.
func (h Holiday) On() bool { return h.From != "" || h.To != "" }

// Covers says the day is in the holiday.
func (h Holiday) Covers(day time.Time) bool {
	from, e1 := time.Parse(DateLayout, h.From)
	to, e2 := time.Parse(DateLayout, h.To)
	return e1 == nil && e2 == nil && !day.Before(from) && !day.After(to)
}

// Expired says the holiday's last day is over.
func (h Holiday) Expired(today time.Time) bool {
	to, err := time.Parse(DateLayout, h.To)
	return err == nil && today.After(to)
}

// Validate checks a holiday that is switched on: both days, the last not
// before the first, and not already over.
func (h Holiday) Validate(today time.Time) error {
	from, e1 := time.Parse(DateLayout, h.From)
	to, e2 := time.Parse(DateLayout, h.To)
	switch {
	case e1 != nil:
		return &FieldError{"from", "from.required"}
	case e2 != nil:
		return &FieldError{"to", "to.required"}
	case to.Before(from):
		return &FieldError{"to", "to.before"}
	case to.Before(today):
		return &FieldError{"to", "to.past"}
	}
	return nil
}

var clock = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// ValidTime reports whether s is a time of day as 24-hour "03:00".
func ValidTime(s string) bool { return clock.MatchString(s) }

// Settings are the household's: the time of day and the holiday.
type Settings struct {
	At      string
	Holiday Holiday
}
