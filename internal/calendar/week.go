package calendar

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/teambition/rrule-go"
)

// StartOfWeek is the Monday (midnight, local) of the week that contains t.
func StartOfWeek(t time.Time) time.Time {
	y, m, d := t.Date()
	back := (int(t.Weekday()) + 6) % 7
	return time.Date(y, m, d-back, 0, 0, 0, 0, t.Location())
}

// WeekDays returns the timed occurrences that begin and end on one day, for
// each of the seven days from weekStart, in time order. What WeekBars lays
// out as a bar (all-day and multi-day occurrences) is not included.
func WeekDays(occs []Occurrence, weekStart time.Time) [7][]Occurrence {
	var days [7][]Occurrence
	for _, o := range occs {
		first := DaysBetween(weekStart, o.Start.In(weekStart.Location()))
		last := DaysBetween(weekStart, LastDay(o, weekStart.Location()))
		if o.AllDay || first != last || first < 0 || first > 6 {
			continue
		}
		days[first] = append(days[first], o)
	}
	for i := range days {
		sort.SliceStable(days[i], func(a, b int) bool { return days[i][a].Start.Before(days[i][b].Start) })
	}
	return days
}

// OccurrenceOf finds the occurrence of events (a single event, or a series
// master with its changed occurrences) whose original start is occStart: the
// changed occurrence if there is one, else the one the series generates.
func OccurrenceOf(events []Event, occStart time.Time) (Occurrence, bool) {
	master, overrides, err := split(events)
	if err != nil {
		return Occurrence{}, false
	}
	if !master.Recurring() {
		if master.Start.Equal(occStart) {
			return occurrence(master, master.Start, master.RecurrenceID, master.SeriesID), true
		}
		return Occurrence{}, false
	}
	for _, o := range overrides {
		if o.RecurrenceID.Equal(occStart) {
			return occurrence(o, o.Start, o.RecurrenceID, master.ID), true
		}
	}
	if !isOccurrence(master, overrides, occStart) {
		return Occurrence{}, false
	}
	return occurrence(master, occStart, occStart, master.ID), true
}

// OriginalStart is the start the occurrence has in its series (the
// recurrence id), or its own start for a single event: what identifies it
// when it is edited or deleted.
func (o Occurrence) OriginalStart() time.Time {
	if o.SeriesID != "" {
		return o.RecurrenceID
	}
	return o.Start
}

// Repeat is one of the series presets the web interface offers.
type Repeat string

// Presets. RepeatCustom is not offered: it names a rule that no preset
// describes (a count, a day of the month from the end, ...), which an edit
// keeps as it is.
const (
	RepeatNone     Repeat = "none"
	RepeatDaily    Repeat = "daily"
	RepeatWeekdays Repeat = "weekdays"
	RepeatWeekly   Repeat = "weekly"
	RepeatBiweekly Repeat = "biweekly"
	RepeatMonthly  Repeat = "monthly"
	RepeatYearly   Repeat = "yearly"
	RepeatCustom   Repeat = "custom"
)

// Repeats lists the presets in the order they are offered.
var Repeats = []Repeat{RepeatNone, RepeatDaily, RepeatWeekdays, RepeatWeekly, RepeatBiweekly, RepeatMonthly, RepeatYearly}

var presetRules = map[Repeat]string{
	RepeatDaily:    "FREQ=DAILY",
	RepeatWeekdays: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR",
	RepeatWeekly:   "FREQ=WEEKLY",
	RepeatBiweekly: "FREQ=WEEKLY;INTERVAL=2",
	RepeatMonthly:  "FREQ=MONTHLY",
	RepeatYearly:   "FREQ=YEARLY",
}

const (
	untilLayout     = "20060102T150405Z"
	untilDateLayout = "20060102"
)

// untilValue writes UNTIL in the value type of DTSTART (RFC 5545, 3.3.10): a
// date for an all-day series, naming its last day in the zone of until (so
// until must be in the zone of the series), and a UTC date-time for a timed
// one.
func untilValue(until time.Time, allDay bool) string {
	if allDay {
		return until.Format(untilDateLayout)
	}
	return until.UTC().Format(untilLayout)
}

// AllDayRule writes the UNTIL of an RRULE value of an all-day series in loc,
// the zone of the series, as a date naming its last day. A rule without a
// date-time UNTIL is returned as it is.
func AllDayRule(rule string, loc *time.Location) (string, error) {
	if !dateTimeUntil.MatchString(rule) {
		return rule, nil
	}
	opt, err := ParseRule(rule, loc)
	if err != nil {
		return "", err
	}
	until := opt.Until.In(loc)
	opt.Until = time.Time{}
	return withUntil(opt.RRuleString(), until, true), nil
}

var dateTimeUntil = regexp.MustCompile(`(?i)UNTIL=\d{8}T`)

// withUntil appends UNTIL to a rule that has none.
func withUntil(rule string, until time.Time, allDay bool) string {
	return rule + ";UNTIL=" + untilValue(until, allDay)
}

// ParseRule reads an RRULE value of a series in loc. A date-only UNTIL names
// the last day of the series, so it counts until that day ends in loc.
func ParseRule(rule string, loc *time.Location) (*rrule.ROption, error) {
	opt, err := rrule.StrToROptionInLocation(rule, loc)
	if err != nil {
		return nil, err
	}
	if !opt.Until.IsZero() && dateOnlyUntil.MatchString(rule) {
		opt.Until = opt.Until.AddDate(0, 0, 1).Add(-time.Second)
	}
	return opt, nil
}

var dateOnlyUntil = regexp.MustCompile(`(?i)UNTIL=\d{8}(;|$)`)

// RepeatRule returns the RRULE value of a preset, ending at until (the last
// moment of the series) unless it is the zero time. RepeatNone gives "". An
// all-day series ends with a date, a timed one with a UTC date-time.
func RepeatRule(r Repeat, until time.Time, allDay bool) (string, error) {
	if r == RepeatNone {
		return "", nil
	}
	rule, ok := presetRules[r]
	if !ok {
		return "", fmt.Errorf("calendar: unknown repeat %q", r)
	}
	if !until.IsZero() {
		rule = withUntil(rule, until, allDay)
	}
	return rule, nil
}

// RepeatOf says which preset an RRULE value is, and when it ends (zero if
// never), as the last moment of the end day when the rule names a date. loc is
// the zone of the series. Anything no preset describes is RepeatCustom.
func RepeatOf(rule string, loc *time.Location) (Repeat, time.Time) {
	if rule == "" {
		return RepeatNone, time.Time{}
	}
	opt, err := ParseRule(rule, loc)
	if err != nil || opt.Count > 0 {
		return RepeatCustom, time.Time{}
	}
	until := opt.Until.In(loc)
	opt.Until = time.Time{}
	canon := opt.RRuleString()
	for r, preset := range presetRules {
		p, _ := rrule.StrToROption(preset)
		if strings.EqualFold(p.RRuleString(), canon) {
			return r, until
		}
	}
	return RepeatCustom, time.Time{}
}
