package meal

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Slot is one cell of a template's week: a weekday (1 Monday to 7 Sunday, as
// ISO 8601 has it) and a mealtime.
type Slot struct {
	Weekday  int
	Mealtime Mealtime
}

// At is the slot of a weekday (1 Monday to 7 Sunday) and a mealtime.
func At(weekday int, m Mealtime) Slot { return Slot{weekday, m} }

// MaxNameLength is the longest template name, counted in characters.
const MaxNameLength = 60

// MaxInterval is the longest repetition: every 52 weeks.
const MaxInterval = 52

const dateLayout = "2006-01-02"

// Template is a week pattern from Monday to Sunday that fills the meal plan by
// itself, every Interval weeks from the week of Start (a Monday, YYYY-MM-DD).
// It fills one mealtime, or all of them. Entries of mealtimes it does not fill
// are ignored; an empty entry leaves that meal free.
//
// A slot holds free text, or a recipe: Recipes maps the slot to the id of a
// recipe of the household. The text of a recipe slot is the recipe's name, kept
// so that a deleted recipe leaves the slot as that name in free text.
type Template struct {
	ID, Name     string
	AllMealtimes bool
	Mealtime     Mealtime // the one mealtime it fills when AllMealtimes is false
	Interval     int
	Start        string
	Entries      map[Slot]string
	Recipes      map[Slot]string // slot -> recipe id; the slot's entry is then the recipe's name
}

// Fills reports whether the template fills the mealtime.
func (t Template) Fills(m Mealtime) bool {
	return t.AllMealtimes || t.Mealtime == m
}

// Filled returns the entries that count: of mealtimes the template fills, with
// a text or a recipe (a recipe slot may still have no text).
func (t Template) Filled() map[Slot]string {
	out := map[Slot]string{}
	count := func(s Slot) bool { return s.Weekday >= 1 && s.Weekday <= 7 && t.Fills(s.Mealtime) }
	for s, text := range t.Entries {
		text = strings.TrimSpace(text)
		if text != "" && count(s) {
			out[s] = text
		}
	}
	for s, id := range t.Recipes {
		if id != "" && count(s) {
			out[s] = strings.TrimSpace(t.Entries[s])
		}
	}
	return out
}

// Validate checks the template. The error is an *InvalidError naming the field:
// "name", "mealtime", "every", "start" or "entries".
func (t Template) Validate() error {
	bad := func(field, msg string) error { return &InvalidError{field, msg} }
	if strings.TrimSpace(t.Name) == "" {
		return bad("name", "a template needs a name")
	}
	if utf8.RuneCountInString(strings.TrimSpace(t.Name)) > MaxNameLength {
		return bad("name", fmt.Sprintf("a name is at most %d characters", MaxNameLength))
	}
	if !t.AllMealtimes && !t.Mealtime.Valid() {
		return bad("mealtime", "choose a mealtime")
	}
	if t.Interval < 1 || t.Interval > MaxInterval {
		return bad("every", fmt.Sprintf("the interval is 1 to %d weeks", MaxInterval))
	}
	start, err := time.Parse(dateLayout, t.Start)
	if err != nil || start.Weekday() != time.Monday {
		return bad("start", "the start week is given by its Monday (YYYY-MM-DD)")
	}
	for _, text := range t.Entries {
		if _, err := CleanText(text); err != nil {
			return bad("entries", err.Error())
		}
	}
	if len(t.Filled()) == 0 {
		return bad("entries", "an empty template fills nothing: enter at least one meal")
	}
	return nil
}

func weeksBetween(from, to time.Time) int {
	return int(to.Sub(from).Hours()/24) / 7
}

// AppliesInWeek reports whether the template fills the week that starts on
// monday: the start week and every Interval-th week after it. Weeks count in
// whole weeks from the start, so the turn of a year (2026 has 53 weeks) needs
// no special case.
func (t Template) AppliesInWeek(monday time.Time) bool {
	start, err := time.Parse(dateLayout, t.Start)
	if err != nil || t.Interval < 1 {
		return false
	}
	n := weeksBetween(start, monday)
	return n >= 0 && n%t.Interval == 0
}

// Dish returns what the template fills in for a day and mealtime.
func (t Template) Dish(d time.Time, m Mealtime) (string, bool) {
	monday := d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
	if !t.AppliesInWeek(monday) {
		return "", false
	}
	wd := (int(d.Weekday())+6)%7 + 1
	text, ok := t.Filled()[Slot{wd, m}]
	return text, ok
}

// RecipeAt returns the id of the recipe the template fills in for a day and
// mealtime, "" when it fills in free text or nothing.
func (t Template) RecipeAt(d time.Time, m Mealtime) string {
	if _, ok := t.Dish(d, m); !ok {
		return ""
	}
	wd := (int(d.Weekday())+6)%7 + 1
	return t.Recipes[Slot{wd, m}]
}

// Weeks returns the Mondays of the next n weeks the template fills, from the
// week that starts on from (a Monday) on.
func (t Template) Weeks(from time.Time, n int) []time.Time {
	start, err := time.Parse(dateLayout, t.Start)
	if err != nil || t.Interval < 1 {
		return nil
	}
	w := start
	if from.After(start) {
		skip := (weeksBetween(start, from) + t.Interval - 1) / t.Interval
		w = start.AddDate(0, 0, 7*skip*t.Interval)
	}
	var out []time.Time
	for len(out) < n {
		out = append(out, w)
		w = w.AddDate(0, 0, 7*t.Interval)
	}
	return out
}

// Clash is the first place two templates fill the same meal: the week (its
// Monday), the mealtime and the weekdays (1 to 7) of that week and mealtime.
type Clash struct {
	Week     time.Time
	Mealtime Mealtime
	Weekdays []int
}

// FirstClash finds where two templates fill the same mealtime on the same
// weekday of the same week, counting only days from today on (a clash on a day
// that is over changes nothing). Only filled entries count.
func FirstClash(a, b Template, today time.Time) (Clash, bool) {
	fa, fb := a.Filled(), b.Filled()
	if len(fa) == 0 || len(fb) == 0 || a.Interval < 1 || b.Interval < 1 {
		return Clash{}, false
	}
	sa, errA := time.Parse(dateLayout, a.Start)
	sb, errB := time.Parse(dateLayout, b.Start)
	if errA != nil || errB != nil {
		return Clash{}, false
	}
	first := sa
	if sb.After(first) {
		first = sb
	}
	thisMonday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	if thisMonday.After(first) {
		first = thisMonday
	}
	// The weeks repeat after Interval(a) x Interval(b) weeks at the latest.
	for i, w := 0, first; i <= a.Interval*b.Interval; i, w = i+1, w.AddDate(0, 0, 7) {
		if !a.AppliesInWeek(w) || !b.AppliesInWeek(w) {
			continue
		}
		for _, m := range Mealtimes() {
			var days []int
			for wd := 1; wd <= 7; wd++ {
				if !w.AddDate(0, 0, wd-1).Before(today) {
					s := Slot{wd, m}
					_, inA := fa[s]
					_, inB := fb[s]
					if inA && inB {
						days = append(days, wd)
					}
				}
			}
			if len(days) > 0 {
				slices.Sort(days)
				return Clash{Week: w, Mealtime: m, Weekdays: days}, true
			}
		}
	}
	return Clash{}, false
}
