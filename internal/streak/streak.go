// Package streak counts how often a routine was completed (ticket 294): the
// state of each day, the week Monday to Sunday and the best streak. It knows
// nothing about storage or HTTP; days are YYYY-MM-DD strings in the
// household's time zone.
//
// A day counts for a routine when at least one of its tasks is due that day.
// It is done when every task due that day is ticked off (a task with subtasks
// is ticked off when all its subtasks are). A missed day and a day still to
// come are both just open: nothing here can fall to zero.
//
// The idea of one count per routine, with days off and pause days that do not
// break it, was compared with Yuvomi (MIT, github.com/ulsklyc/yuvomi); no code
// is ported.
package streak

import (
	"time"

	"github.com/hejmo-home/hejmo/internal/task"
)

// State is what one day of a routine is.
type State string

// The states of a day.
const (
	Done   State = "done"   // everything due was ticked off (also on a pause day)
	Open   State = "open"   // still to come or missed: the same on purpose
	Paused State = "pause"  // a pause day of the person that was not done
	Off    State = "off"    // nothing is due
	Before State = "before" // before the routine was created
)

// MaxPauseDays is the longest span of one pause, in days.
const MaxPauseDays = 366

// Pause is a span of days (first and last included) on which a routine does
// not count. People empty means the whole family.
type Pause struct {
	ID       string
	From, To string
	People   []string
}

// Covers reports whether the pause applies to the person on the day.
func (p Pause) Covers(person, day string) bool {
	if day < p.From || day > p.To {
		return false
	}
	if len(p.People) == 0 {
		return true
	}
	for _, id := range p.People {
		if id == person {
			return true
		}
	}
	return false
}

// PauseError says what is wrong with a pause: Field is "from" or "to", Code
// "date", "order" (to before from) or "long" (more than MaxPauseDays).
type PauseError struct{ Field, Code string }

func (e *PauseError) Error() string { return "invalid pause: " + e.Field + " " + e.Code }

// Validate reports why a pause cannot be stored. Past days are fine.
func (p Pause) Validate() error {
	from, err := time.Parse(task.DateLayout, p.From)
	if err != nil {
		return &PauseError{"from", "date"}
	}
	to, err := time.Parse(task.DateLayout, p.To)
	if err != nil {
		return &PauseError{"to", "date"}
	}
	if to.Before(from) {
		return &PauseError{"to", "order"}
	}
	if int(to.Sub(from).Hours()/24)+1 > MaxPauseDays {
		return &PauseError{"to", "long"}
	}
	return nil
}

// DayOf is the day of an instant in the household's time zone.
func DayOf(t time.Time, loc *time.Location) string { return t.In(loc).Format(task.DateLayout) }

// Routine is everything the count of one routine needs.
type Routine struct {
	PersonID string
	// Created is the first day that counts: the day the routine was created.
	Created string
	Tasks   []task.Task
	// Done holds the ticked-off days per task id.
	Done   map[string]map[string]bool
	Pauses []Pause
}

func (r Routine) paused(day string) bool {
	for _, p := range r.Pauses {
		if p.Covers(r.PersonID, day) {
			return true
		}
	}
	return false
}

// State is the state of one day.
func (r Routine) State(day string) State {
	if day < r.Created {
		return Before
	}
	due, done := 0, 0
	for _, t := range r.Tasks {
		if t.DueOn(day) {
			due++
			if r.Done[t.ID][day] {
				done++
			}
		}
	}
	switch {
	case due == 0:
		return Off
	case done == due:
		return Done
	case r.paused(day):
		return Paused
	}
	return Open
}

// Cell is one day of the week strip.
type Cell struct {
	Date    string
	Weekday int    // 0 Monday to 6 Sunday
	Dow     int    // time.Weekday: 0 Sunday to 6 Saturday, for the names of the days
	Code    string // RFC 5545 weekday code: MO to SU
	State   State
	// Paused is set on every day the person has a pause day, done or not, so a
	// page can say a done day was a pause day.
	Paused bool
	Today  bool
}

var weekdayCodes = [7]string{"MO", "TU", "WE", "TH", "FR", "SA", "SU"}

// Week is the week of a routine, Monday to Sunday, and its numbers.
type Week struct {
	Cells [7]Cell
	// Done of Total days: Total is the days the routine applies to minus the
	// pause days that were not done; Paused is how many of those there are.
	Done, Total, Paused int
	// Best is the longest run of done days, over everything counted so far.
	Best int
}

// Week lays out the week that contains today.
func (r Routine) Week(today string) Week {
	var w Week
	t, err := time.Parse(task.DateLayout, today)
	if err != nil {
		return w
	}
	monday := t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7))
	for i := range w.Cells {
		day := monday.AddDate(0, 0, i).Format(task.DateLayout)
		c := Cell{Date: day, Weekday: i, Dow: (i + 1) % 7, Code: weekdayCodes[i], State: r.State(day), Today: day == today}
		c.Paused = c.State != Before && c.State != Off && r.paused(day)
		w.Cells[i] = c
		switch c.State {
		case Done:
			w.Done++
			w.Total++
		case Open:
			w.Total++
		case Paused:
			w.Paused++
		}
	}
	w.Best = r.Best(today)
	return w
}

// Best is the longest run of consecutive days that were done, from the day the
// routine was created up to today. Days with nothing due and pause days that
// were not done neither extend nor end a run; an open day ends it.
func (r Routine) Best(today string) int {
	from, err := time.Parse(task.DateLayout, r.Created)
	if err != nil {
		return 0
	}
	to, err := time.Parse(task.DateLayout, today)
	if err != nil {
		return 0
	}
	best, run := 0, 0
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		switch r.State(d.Format(task.DateLayout)) {
		case Done:
			run++
			best = max(best, run)
		case Open:
			run = 0
		}
	}
	return best
}

// Summary says which sentence the week gets (a locale key week.<kind>) and its
// numbers: "sum" (x of y days done), "sum_zero" (y days with the routine; never
// "0 of y"), "sum_all", "sum_pause" (nothing counts this week because of pause
// days) or "" (nothing is due this week: say nothing).
func (w Week) Summary() (kind string, done, total int) {
	switch {
	case w.Total == 0 && w.Paused > 0:
		return "sum_pause", 0, 0
	case w.Total == 0:
		return "", 0, 0
	case w.Done == 0:
		return "sum_zero", 0, w.Total
	case w.Done == w.Total:
		return "sum_all", w.Done, w.Total
	}
	return "sum", w.Done, w.Total
}

// Sentence is a Summary for templates: Kind as in Summary.
type Sentence struct {
	Kind        string
	Done, Total int
}

// Sum is Summary as one value, for templates.
func (w Week) Sum() Sentence {
	k, d, n := w.Summary()
	return Sentence{k, d, n}
}

// Key is the locale key of the sentence, week.<kind>, with ".one" when the
// week has a single day.
func (s Sentence) Key() string {
	if s.Total == 1 && (s.Kind == "sum_zero" || s.Kind == "sum_all") {
		return "week." + s.Kind + ".one"
	}
	return "week." + s.Kind
}

// BestKey is the locale key of the best streak line, singular for one day.
func (w Week) BestKey() string {
	if w.Best == 1 {
		return "week.best.one"
	}
	return "week.best.other"
}
