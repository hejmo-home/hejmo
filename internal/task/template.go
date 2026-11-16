package task

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// A routine template is a routine without a person (ADR 0018): the part of the
// day and the steps of a routine, in order. Using one makes an independent
// copy, so changing the copy never changes the template and the other way
// round. Hejmo ships some (Shipped); parents save their own from a routine.

// TemplateStep is one step of a routine template: a title, an emoji, a star
// value and a repetition. A template step is never once-only (a date makes no
// sense in a reusable template) and has no subtasks.
type TemplateStep struct {
	ID       string
	Title    string
	Emoji    string
	Stars    int
	Repeat   Repeat   // Daily or Weekly
	Weekdays []string // RFC 5545 codes (MO..SU), only for Weekly
}

// Validate reports why a step cannot be stored. The rules are those of a task.
func (s TemplateStep) Validate() error {
	if s.Repeat == Once {
		return &InvalidError{Field: "repeat", Msg: "a template step cannot be once-only"}
	}
	return Task{PersonID: "template", Title: s.Title, Emoji: s.Emoji, Stars: s.Stars, Repeat: s.Repeat, Weekdays: s.Weekdays, Date: "2000-01-01"}.Validate()
}

// RoutineTemplate is a routine without a person. Part is a suggestion: using
// the template may pick any part of the day.
type RoutineTemplate struct {
	ID      string
	Name    string
	Part    PartOfDay // Morning, AfterSchool or Evening
	Steps   []TemplateStep
	Shipped bool // comes with Hejmo and cannot be changed

	// Where an own template was saved from, as the names were then.
	OriginRoutine, OriginPerson string
	SavedAt                     time.Time
}

// Validate reports why a template cannot be stored.
func (t RoutineTemplate) Validate() error {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		return &InvalidError{Field: "name", Msg: "name required"}
	}
	if utf8.RuneCountInString(name) > MaxRoutineName {
		return &InvalidError{Field: "name", Msg: fmt.Sprintf("name may have at most %d characters", MaxRoutineName)}
	}
	switch t.Part {
	case Morning, AfterSchool, Evening:
	default:
		return &InvalidError{Field: "part", Msg: "part of day must be morning, afterschool or evening"}
	}
	return nil
}

// ShippedStep is a step of a shipped template. Its title is the locale text
// "routine.tpl.<template id>.<key>"; nil Weekdays mean every day.
type ShippedStep struct {
	Key      string
	Emoji    string
	Stars    int
	Weekdays []string
}

// Repeat is Weekly when the step has weekdays, else Daily.
func (s ShippedStep) Repeat() Repeat {
	if len(s.Weekdays) > 0 {
		return Weekly
	}
	return Daily
}

// ShippedTemplate is a template that comes with Hejmo. Its name is the locale
// text "routine.tpl.<id>", so it shows and copies in the language in use.
type ShippedTemplate struct {
	ID    string
	Part  PartOfDay
	Steps []ShippedStep
}

var schoolDays = []string{"MO", "TU", "WE", "TH", "FR"}

// Shipped are the templates that come with Hejmo, in the order they are listed.
var Shipped = []ShippedTemplate{
	{ID: "school-morning", Part: Morning, Steps: []ShippedStep{
		{Key: "dress", Emoji: "👕", Stars: 1},
		{Key: "breakfast", Emoji: "🥣", Stars: 1},
		{Key: "teeth", Emoji: "🪥", Stars: 1},
		{Key: "bag", Emoji: "🎒", Stars: 1, Weekdays: schoolDays},
		{Key: "shoes", Emoji: "👟", Stars: 1, Weekdays: schoolDays},
	}},
	{ID: "daycare-morning", Part: Morning, Steps: []ShippedStep{
		{Key: "toilet", Emoji: "🚽", Stars: 1},
		{Key: "dress", Emoji: "👕", Stars: 1},
		{Key: "breakfast", Emoji: "🥣", Stars: 1},
		{Key: "teeth", Emoji: "🪥", Stars: 1},
		{Key: "jacket", Emoji: "🧢", Stars: 1, Weekdays: schoolDays},
	}},
	{ID: "school-afternoon", Part: AfterSchool, Steps: []ShippedStep{
		{Key: "unpack", Emoji: "🎒", Stars: 1, Weekdays: schoolDays},
		{Key: "lunchbox", Emoji: "🍎", Stars: 1, Weekdays: schoolDays},
		{Key: "homework", Emoji: "✏️", Stars: 2, Weekdays: schoolDays},
		{Key: "room", Emoji: "🧸", Stars: 1, Weekdays: schoolDays},
	}},
	{ID: "evening", Part: Evening, Steps: []ShippedStep{
		{Key: "toys", Emoji: "🧸", Stars: 1},
		{Key: "wash", Emoji: "🛁", Stars: 1},
		{Key: "pyjamas", Emoji: "🩳", Stars: 1},
		{Key: "teeth", Emoji: "🪥", Stars: 1},
		{Key: "story", Emoji: "📖", Stars: 0},
	}},
}

// ShippedByID finds a shipped template.
func ShippedByID(id string) (ShippedTemplate, bool) {
	for _, t := range Shipped {
		if t.ID == id {
			return t, true
		}
	}
	return ShippedTemplate{}, false
}
