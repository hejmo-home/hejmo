package calendar

import (
	"sort"
	"time"
)

// Audience lists who a calendar is for. Empty = the whole family.
type Audience struct {
	People []string
	Groups []string
}

func (a Audience) empty() bool { return len(a.People) == 0 && len(a.Groups) == 0 }

// Viewer is the person whose view is shown, with the groups they belong to.
type Viewer struct {
	PersonID string
	Groups   []string
}

func (v Viewer) in(people, groups []string) bool {
	for _, p := range people {
		if p == v.PersonID {
			return true
		}
	}
	for _, g := range groups {
		for _, mine := range v.Groups {
			if g == mine {
				return true
			}
		}
	}
	return false
}

// Visible keeps the occurrences meant for v: the calendar's audience (if
// any) must include v, and so must the event's own audience (if any).
func Visible(occs []Occurrence, calendars map[string]Audience, v Viewer) []Occurrence {
	var out []Occurrence
	for _, o := range occs {
		if a, ok := calendars[o.CalendarID]; ok && !a.empty() && !v.in(a.People, a.Groups) {
			continue
		}
		if (len(o.People) > 0 || len(o.Groups) > 0) && !v.in(o.People, o.Groups) {
			continue
		}
		out = append(out, o)
	}
	return out
}

// Bar is a multi-day or all-day occurrence laid out on a 7-day week.
type Bar struct {
	Occurrence
	Col, Span, Lane int  // Col 0 = first day of the week
	ContinuesBefore bool // starts before this week
	ContinuesAfter  bool // ends after this week
}

// WeekBars lays out all-day and multi-day occurrences as continuous bars
// for the week starting at weekStart (midnight local). Overlapping bars get
// separate lanes.
func WeekBars(occs []Occurrence, weekStart time.Time) []Bar {
	var bars []Bar
	for _, o := range occs {
		first := daysBetween(weekStart, o.Start.In(weekStart.Location()))
		last := daysBetween(weekStart, lastDay(o, weekStart.Location()))
		if !o.AllDay && first == last {
			continue
		}
		if last < 0 || first > 6 {
			continue
		}
		b := Bar{Occurrence: o, ContinuesBefore: first < 0, ContinuesAfter: last > 6}
		first, last = max(first, 0), min(last, 6)
		b.Col, b.Span = first, last-first+1
		bars = append(bars, b)
	}
	sort.SliceStable(bars, func(i, j int) bool {
		if bars[i].Col != bars[j].Col {
			return bars[i].Col < bars[j].Col
		}
		return bars[i].Span > bars[j].Span
	})
	var laneEnds []int // last column used per lane
	for i := range bars {
		lane := 0
		for lane < len(laneEnds) && laneEnds[lane] >= bars[i].Col {
			lane++
		}
		if lane == len(laneEnds) {
			laneEnds = append(laneEnds, 0)
		}
		laneEnds[lane] = bars[i].Col + bars[i].Span - 1
		bars[i].Lane = lane
	}
	return bars
}

// lastDay is the date of the occurrence's last covered day.
func lastDay(o Occurrence, loc *time.Location) time.Time {
	end := o.End.In(loc)
	if o.AllDay || (end.Hour() == 0 && end.Minute() == 0 && end.Second() == 0 && end.After(o.Start)) {
		end = end.Add(-time.Nanosecond)
	}
	return time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, loc)
}
