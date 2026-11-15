// Package quiet decides when a display is in quiet mode (ADR 0015): a daily
// window such as 22:00 to 06:00. It is pure: time in, answer out.
package quiet

import (
	"errors"
	"fmt"
	"time"
)

// Schedule is a display's quiet mode setting.
type Schedule struct {
	Enabled  bool
	From, To int // minutes after midnight
}

// ErrInvalid is returned for times that are not "HH:MM" or equal start and end.
var ErrInvalid = errors.New("quiet mode needs two different times as hh:mm")

// Parse builds a schedule from "HH:MM" strings. The times are validated even
// when the schedule is disabled, so a stored schedule is always complete.
func Parse(enabled bool, from, to string) (Schedule, error) {
	f, ok1 := minutes(from)
	t, ok2 := minutes(to)
	if !ok1 || !ok2 || f == t {
		return Schedule{}, ErrInvalid
	}
	return Schedule{Enabled: enabled, From: f, To: t}, nil
}

func minutes(s string) (int, bool) {
	var h, m int
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	if _, err := fmt.Sscanf(s, "%02d:%02d", &h, &m); err != nil || h > 23 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// FromText and ToText give the times as "HH:MM".
func (s Schedule) FromText() string { return fmt.Sprintf("%02d:%02d", s.From/60, s.From%60) }
func (s Schedule) ToText() string   { return fmt.Sprintf("%02d:%02d", s.To/60, s.To%60) }

func minuteOfDay(t time.Time) int { return t.Hour()*60 + t.Minute() }

// Active reports whether t (in the household's time zone) is inside the window.
func (s Schedule) Active(t time.Time) bool {
	if !s.Enabled {
		return false
	}
	m := minuteOfDay(t)
	if s.From < s.To {
		return m >= s.From && m < s.To
	}
	return m >= s.From || m < s.To
}

// Morning returns the day (midnight, in t's zone) the window ends on next: the
// coming morning the night page is about. At 23:00 on Monday and at 02:00 on
// Tuesday it is Tuesday. A window inside one day that starts at or after 12:00
// (20:00 to 23:30) is an evening window with no morning of its own, so the
// coming morning is always tomorrow's. One that starts before noon (00:00 to
// 06:00) ends this morning, and tomorrow's once it is over.
func (s Schedule) Morning(t time.Time) time.Time {
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	m := minuteOfDay(t)
	switch {
	case s.From > s.To: // wraps midnight: the window ends in the morning
		if m >= s.To {
			day = day.AddDate(0, 0, 1)
		}
	case s.From >= 12*60: // same-day evening window: no morning of its own
		day = day.AddDate(0, 0, 1)
	default: // same-day window starting before noon (00:00 to 06:00)
		if m >= s.To {
			day = day.AddDate(0, 0, 1)
		}
	}
	return day
}
