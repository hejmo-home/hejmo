// Package task holds the rules of simple tasks (ADR 0015): what a task is and
// on which days it is due. It knows nothing about storage or HTTP.
//
// A task is stored like an iCalendar VTODO so that export (V1.x) needs no
// remodelling: Date is DTSTART (DUE for a once task), RRule is the RRULE and
// each done day is one completed occurrence.
//
// For a weekly task Date may fall on a day that is not one of its weekdays,
// yet in iCalendar DTSTART always counts as an occurrence. A VTODO export must
// therefore move DTSTART to the first day the task is due (see DueOn).
package task

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrInvalid marks input the caller can fix.
var ErrInvalid = errors.New("invalid task")

// Repeat says how often a task comes back.
type Repeat string

// Repetitions in V1.0.
const (
	Once   Repeat = "once"   // on Date only
	Daily  Repeat = "daily"  // every day from Date
	Weekly Repeat = "weekly" // on chosen weekdays from Date
)

// PartOfDay optionally places a task in the morning or the evening.
type PartOfDay string

// Parts of the day. The empty value means any time. AfterSchool exists only
// in routines (ADR 0018).
const (
	AnyTime     PartOfDay = ""
	Morning     PartOfDay = "morning"
	AfterSchool PartOfDay = "afterschool"
	Evening     PartOfDay = "evening"
)

// Rank orders the parts of a day as the day goes: morning, after school, any
// time, evening.
func (p PartOfDay) Rank() int {
	switch p {
	case Morning:
		return 0
	case AfterSchool:
		return 1
	case Evening:
		return 3
	}
	return 2
}

// Limits of the input, as in api/openapi.yaml (NewTask), counted in characters.
const (
	MaxTitle = 120
	MaxEmoji = 16
	// MaxRoutineName is the longest name of a routine, in characters.
	MaxRoutineName = 60
	// MaxSubtasks is the most subtasks one task can have (one level only).
	MaxSubtasks = 10
	// MaxStars is the largest star value of one task.
	MaxStars = 100
	// MaxBooking is the largest amount of one manual star booking, up or down.
	MaxBooking = 1000
	// MaxNote is the longest note of a manual star booking, in characters.
	MaxNote = 120
	// MaxNotes is the longest free-text notes field of a task, in characters.
	MaxNotes = 2000
)

// DefaultStars is the star value a new task gets: 1 for a child, 0 for an
// adult (ADR 0018). Parents can change it per task.
func DefaultStars(child bool) int {
	if child {
		return 1
	}
	return 0
}

// ValidateBooking reports why a manual star booking cannot be stored: it needs
// a non-zero amount (negative to deduct) and a short note.
func ValidateBooking(delta int, note string) error {
	bad := func(msg string) error { return fmt.Errorf("%w: %s", ErrInvalid, msg) }
	if delta == 0 || delta > MaxBooking || delta < -MaxBooking {
		return bad(fmt.Sprintf("amount must be between -%d and %d, not 0", MaxBooking, MaxBooking))
	}
	if strings.TrimSpace(note) == "" {
		return bad("note required")
	}
	if utf8.RuneCountInString(strings.TrimSpace(note)) > MaxNote {
		return bad(fmt.Sprintf("note may have at most %d characters", MaxNote))
	}
	return nil
}

// DateLayout is the layout of Task.Date and of days passed around.
const DateLayout = "2006-01-02"

// weekdayCodes are the RFC 5545 weekday codes in calendar order.
var weekdayCodes = []string{"MO", "TU", "WE", "TH", "FR", "SA", "SU"}

// Task is a simple task for one person.
type Task struct {
	ID        string
	PersonID  string
	Title     string
	Emoji     string
	Repeat    Repeat
	Weekdays  []string // RFC 5545 codes (MO..SU), only for Weekly
	Date      string   // YYYY-MM-DD in the household's time zone: the day of a once task, the first day of a repeating one
	PartOfDay PartOfDay
	Stars     int // credited when ticked off, deducted when unticked (ADR 0018); 0 allowed
	// RoutineID is the routine the task belongs to, empty for a single task. A
	// task belongs to at most one routine and takes its part of the day from it
	// (ADR 0018). Position is its place in the routine, from 1.
	RoutineID string
	Position  int
	// Notes is an optional free text (ADR 0018), editable from CalDAV clients.
	Notes string
	// UpdatedAt is the last change of the task itself (not of its done days).
	UpdatedAt time.Time
}

// Routine is the ordered sequence of one person's tasks for a part of the day
// (ADR 0018). The tasks carry their place; a person has at most one routine
// per part of the day.
type Routine struct {
	ID       string
	PersonID string
	Name     string
	Part     PartOfDay // Morning, AfterSchool or Evening
	// Created is when the routine was made; days before it do not count for
	// its week (ticket 294). Zero when storing: now.
	Created time.Time
}

// Validate reports why a routine cannot be stored.
func (r Routine) Validate() error {
	bad := func(field, msg string) error { return &InvalidError{Field: field, Msg: msg} }
	name := strings.TrimSpace(r.Name)
	if name == "" {
		return bad("name", "name required")
	}
	if utf8.RuneCountInString(name) > MaxRoutineName {
		return bad("name", fmt.Sprintf("name may have at most %d characters", MaxRoutineName))
	}
	if r.PersonID == "" {
		return bad("person", "person required")
	}
	switch r.Part {
	case Morning, AfterSchool, Evening:
	default:
		return bad("part", "part of day must be morning, afterschool or evening")
	}
	return nil
}

// InvalidError says why a task cannot be stored and which field is wrong
// (title, emoji, notes, person, date, weekdays, repeat or part). It matches
// ErrInvalid with errors.Is.
type InvalidError struct{ Field, Msg string }

func (e *InvalidError) Error() string { return ErrInvalid.Error() + ": " + e.Msg }
func (e *InvalidError) Unwrap() error { return ErrInvalid }

// Validate reports why a task cannot be stored.
func (t Task) Validate() error {
	bad := func(field, msg string) error { return &InvalidError{Field: field, Msg: msg} }
	if strings.TrimSpace(t.Title) == "" {
		return bad("title", "title required")
	}
	if utf8.RuneCountInString(strings.TrimSpace(t.Title)) > MaxTitle {
		return bad("title", fmt.Sprintf("title may have at most %d characters", MaxTitle))
	}
	if utf8.RuneCountInString(t.Emoji) > MaxEmoji {
		return bad("emoji", fmt.Sprintf("emoji may have at most %d characters", MaxEmoji))
	}
	if utf8.RuneCountInString(t.Notes) > MaxNotes {
		return bad("notes", fmt.Sprintf("notes may have at most %d characters", MaxNotes))
	}
	if t.Stars < 0 || t.Stars > MaxStars {
		return bad("stars", fmt.Sprintf("stars must be between 0 and %d", MaxStars))
	}
	if t.PersonID == "" {
		return bad("person", "person required")
	}
	if _, err := time.Parse(DateLayout, t.Date); err != nil {
		return bad("date", "date must be YYYY-MM-DD")
	}
	switch t.Repeat {
	case Once, Daily:
		if len(t.Weekdays) > 0 {
			return bad("weekdays", "weekdays only apply to weekly tasks")
		}
	case Weekly:
		if len(t.Weekdays) == 0 {
			return bad("weekdays", "a weekly task needs at least one weekday")
		}
		seen := map[string]bool{}
		for _, d := range t.Weekdays {
			if weekdayIndex(d) < 0 {
				return bad("weekdays", "unknown weekday "+d)
			}
			if seen[d] {
				return bad("weekdays", "weekday "+d+" given twice")
			}
			seen[d] = true
		}
	default:
		return bad("repeat", "repeat must be once, daily or weekly")
	}
	switch t.PartOfDay {
	case AnyTime, Morning, Evening:
	case AfterSchool:
		if t.RoutineID == "" {
			return bad("part", "after school exists only in routines")
		}
	default:
		return bad("part", "part of day must be morning or evening")
	}
	return nil
}

func weekdayIndex(code string) int {
	for i, c := range weekdayCodes {
		if c == code {
			return i
		}
	}
	return -1
}

// DueOn reports whether the task is due on the day (YYYY-MM-DD).
func (t Task) DueOn(day string) bool {
	d, err := time.Parse(DateLayout, day)
	if err != nil {
		return false
	}
	switch t.Repeat {
	case Once:
		return day == t.Date
	case Daily:
		return day >= t.Date
	case Weekly:
		if day < t.Date {
			return false
		}
		code := weekdayCodes[(int(d.Weekday())+6)%7]
		for _, w := range t.Weekdays {
			if w == code {
				return true
			}
		}
	}
	return false
}

// RRule is the iCalendar recurrence rule, empty for a once task.
func (t Task) RRule() string {
	switch t.Repeat {
	case Daily:
		return "FREQ=DAILY"
	case Weekly:
		days := append([]string(nil), t.Weekdays...)
		sort.Slice(days, func(i, j int) bool { return weekdayIndex(days[i]) < weekdayIndex(days[j]) })
		return "FREQ=WEEKLY;BYDAY=" + strings.Join(days, ",")
	}
	return ""
}

// ParseRRule reads a rule written by RRule into Repeat and Weekdays.
func ParseRRule(rule string) (Task, error) {
	switch {
	case rule == "":
		return Task{Repeat: Once}, nil
	case rule == "FREQ=DAILY":
		return Task{Repeat: Daily}, nil
	}
	if days, ok := strings.CutPrefix(rule, "FREQ=WEEKLY;BYDAY="); ok {
		t := Task{Repeat: Weekly, Weekdays: strings.Split(days, ",")}
		for _, d := range t.Weekdays {
			if weekdayIndex(d) < 0 {
				return Task{}, fmt.Errorf("%w: unknown weekday %q", ErrInvalid, d)
			}
		}
		return t, nil
	}
	return Task{}, fmt.Errorf("%w: unsupported rule %q", ErrInvalid, rule)
}

// Subtask is one of the items a task is split into (ADR 0018): a title with an
// optional emoji, ticked off on its own for each occurrence of the task. It
// has no stars of its own and no subtasks. Position is its place, from 1.
type Subtask struct {
	ID       string
	TaskID   string
	Title    string
	Emoji    string
	Position int
}

// Validate reports why a subtask cannot be stored.
func (s Subtask) Validate() error {
	bad := func(field, msg string) error { return &InvalidError{Field: field, Msg: msg} }
	title := strings.TrimSpace(s.Title)
	if title == "" {
		return bad("title", "title required")
	}
	if utf8.RuneCountInString(title) > MaxTitle {
		return bad("title", fmt.Sprintf("title may have at most %d characters", MaxTitle))
	}
	if utf8.RuneCountInString(strings.TrimSpace(s.Emoji)) > MaxEmoji {
		return bad("emoji", fmt.Sprintf("emoji may have at most %d characters", MaxEmoji))
	}
	return nil
}
