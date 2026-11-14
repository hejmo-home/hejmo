// Package familyview builds the view model of the display's family view: the
// data for one screen, independent of how it is rendered (ADR 0015, 0016).
package familyview

import (
	"fmt"
	"sort"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/holiday"
)

// Person is one column of the family view. Slot (1..6) picks the person's
// colour tokens; Shape is the black-and-white marker.
type Person struct {
	ID, Name string
	Slot     int
	Shape    string
	Groups   []string // subgroups the person belongs to
}

// Input is everything Build needs. Occurrences must cover today and tomorrow.
type Input struct {
	Now         time.Time // in the household's time zone
	Lang        string    // "de" or "en", for holiday names
	Region      string    // home region (ISO 3166-2)
	People      []Person  // display order
	Calendars   map[string]calendar.Audience
	Occurrences []calendar.Occurrence
	// ShowTomorrow is the display's setting "show tomorrow block", off by
	// default (ADR 0016). Without it nothing of tomorrow is built.
	ShowTomorrow bool
}

// Event is a card in a person's column.
type Event struct {
	Title string
	Time  string // "09:00–10:00", 24 hours until the time-format setting exists (5.8)
}

// Progress is a person's task progress today (data arrives with 6.6).
type Progress struct{ Done, Total int }

// Percent is the filled share of the bar, 0 to 100.
func (p Progress) Percent() int {
	if p.Total <= 0 {
		return 0
	}
	return min(100, p.Done*100/p.Total)
}

// Column is one person's column.
type Column struct {
	Person   Person
	Events   []Event
	Tomorrow []Event   // from 18:00: the first timed events of the next day
	Progress *Progress // nil: no bar
}

// Marker identifies a person on a strip item.
type Marker struct {
	Name  string
	Slot  int
	Shape string
}

// Strip item kinds.
const (
	KindEvent   = "event"
	KindHoliday = "holiday"
)

// StripItem is an entry of the family strip.
type StripItem struct {
	Kind            string
	Title           string
	Time            string // timed whole-household events only
	AllDay          bool
	Partial         bool // partial public holiday
	Markers         []Marker
	DayIndex        int // multi-day: this is day DayIndex of DayCount (1-based)
	DayCount        int
	ContinuesBefore bool
	ContinuesAfter  bool
}

// Tile is a footer tile (content arrives with package 6).
type Tile struct {
	Label string
	Lines []string
	Alert bool
}

// View is the data of one family view screen.
type View struct {
	Weekday       time.Weekday
	Week          int
	Date          time.Time
	Clock         string // "15:04"; the display ticks it itself between renders
	ClockZone     string // IANA name of the household time zone, for that ticking
	Weather       string // empty until 5.8
	Strip         []StripItem
	StripTomorrow []StripItem
	ShowTomorrow  bool
	Columns       []Column
	Scroll        bool // 7 or more people: the columns scroll
	Meal          *Tile
	TomorrowTile  *Tile
}

const (
	tomorrowFromHour = 18 // the tomorrow block appears from 18:00
	maxTomorrow      = 3  // timed events per column in the tomorrow block
	maxNarrowColumns = 6  // from seven people the columns scroll
)

// Build lays out today's family view.
func Build(in Input) View {
	now := in.Now
	d0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	d1 := d0.AddDate(0, 0, 1)
	_, week := now.ISOWeek()
	v := View{Weekday: now.Weekday(), Week: week, Date: d0, Clock: now.Format("15:04"), ClockZone: now.Location().String()}
	v.ShowTomorrow = in.ShowTomorrow && now.Hour() >= tomorrowFromHour
	v.Scroll = len(in.People) > maxNarrowColumns
	for _, p := range in.People {
		v.Columns = append(v.Columns, Column{Person: p})
	}
	loc := d0.Location()
	occs := append([]calendar.Occurrence(nil), in.Occurrences...)
	sort.SliceStable(occs, func(i, j int) bool { return occs[i].Start.Before(occs[j].Start) })
	var bars, lines []StripItem
	for _, o := range occs {
		if !o.Start.Before(d1) || !o.End.After(d0) {
			continue
		}
		whole := wholeHousehold(in, o)
		bar := o.AllDay || multiDay(o, loc)
		if !bar && !whole {
			for i, p := range in.People {
				if sees(in, p, o) {
					v.Columns[i].Events = append(v.Columns[i].Events, card(o, loc))
				}
			}
			continue
		}
		item := StripItem{Kind: KindEvent, Title: o.Title, AllDay: o.AllDay}
		if !whole {
			for _, p := range in.People {
				if sees(in, p, o) {
					item.Markers = append(item.Markers, Marker{Name: p.Name, Slot: p.Slot, Shape: p.Shape})
				}
			}
			if len(item.Markers) == 0 {
				continue
			}
		}
		if bar {
			first, last := firstDay(o, loc), calendar.LastDay(o, loc)
			item.DayIndex, item.DayCount = calendar.DaysBetween(first, d0)+1, calendar.DaysBetween(first, last)+1
			item.ContinuesBefore, item.ContinuesAfter = first.Before(d0), last.After(d0)
			if item.DayCount == 1 {
				item.DayIndex = 0
			}
			bars = append(bars, item)
			continue
		}
		item.Time = clock(o, loc)
		lines = append(lines, item)
	}
	if v.ShowTomorrow {
		d2 := d1.AddDate(0, 0, 1)
		for _, o := range occs {
			if o.Start.Before(d1) || !o.Start.Before(d2) {
				continue
			}
			if wholeHousehold(in, o) {
				item := StripItem{Kind: KindEvent, Title: o.Title, AllDay: o.AllDay}
				if !o.AllDay {
					item.Time = clock(o, loc)
				}
				v.StripTomorrow = append(v.StripTomorrow, item)
				continue
			}
			if o.AllDay || multiDay(o, loc) {
				continue
			}
			for i, p := range in.People {
				if sees(in, p, o) && len(v.Columns[i].Tomorrow) < maxTomorrow {
					v.Columns[i].Tomorrow = append(v.Columns[i].Tomorrow, card(o, loc))
				}
			}
		}
	}
	for _, h := range holiday.Between(in.Region, d0, d1) {
		name := h.NameEN
		if in.Lang == "de" {
			name = h.NameDE
		}
		v.Strip = append(v.Strip, StripItem{Kind: KindHoliday, Title: name, AllDay: true, Partial: h.Partial})
	}
	v.Strip = append(append(v.Strip, bars...), lines...)
	return v
}

// clock formats the times in the household's zone, whatever zone the event is
// stored in (synced events often arrive as UTC or with their own TZID).
func clock(o calendar.Occurrence, loc *time.Location) string {
	return fmt.Sprintf("%s–%s", o.Start.In(loc).Format("15:04"), o.End.In(loc).Format("15:04"))
}

func card(o calendar.Occurrence, loc *time.Location) Event {
	return Event{Title: o.Title, Time: clock(o, loc)}
}

func wholeHousehold(in Input, o calendar.Occurrence) bool {
	a := in.Calendars[o.CalendarID]
	return len(a.People) == 0 && len(a.Groups) == 0 && len(o.People) == 0 && len(o.Groups) == 0
}

// sees reports whether p is in the effective audience of o (ADR 0011).
func sees(in Input, p Person, o calendar.Occurrence) bool {
	return len(calendar.Visible([]calendar.Occurrence{o}, in.Calendars, calendar.Viewer{PersonID: p.ID, Groups: p.Groups})) == 1
}

// multiDay reports whether a timed occurrence covers more than one day.
func multiDay(o calendar.Occurrence, loc *time.Location) bool {
	return firstDay(o, loc) != calendar.LastDay(o, loc)
}

func firstDay(o calendar.Occurrence, loc *time.Location) time.Time {
	s := o.Start.In(loc)
	return time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, loc)
}
