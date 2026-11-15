// Package familyview builds the view model of the display's family view: the
// data for one screen, independent of how it is rendered (ADR 0015, 0016).
package familyview

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/holiday"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Person is one column of the family view. Slot (1..8) picks the person's
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
	// TimeFormat is the household's "24h" (default) or "12h" (ADR 0016).
	TimeFormat store.TimeFormat
	// Weather is the current weather at home; nil hides the chip.
	// TemperatureUnit is "c" (default) or "f".
	Weather         *Weather
	TemperatureUnit store.TemperatureUnit
	// Waste holds the occurrences of the waste collection calendars (ADR 0015),
	// kept apart from Occurrences: they feed only the tomorrow tile.
	Waste []calendar.Occurrence
	// DayNotes are the parents' notes by day (YYYY-MM-DD).
	DayNotes map[string]string
	// TomorrowLabel and TodayLabel come from the translations: the tile is
	// labelled "Today" when it holds only today's note.
	TomorrowLabel, TodayLabel string
	// SyncStale: a synced calendar has gone 24 hours without a successful
	// sync (ADR 0012). The header then shows an info symbol.
	SyncStale bool
}

// Weather is a reading for the weather chip; Icon names the symbol.
type Weather struct {
	TempC float64
	Icon  string
}

// WeatherChip is the weather chip as shown: Text is the temperature with its
// unit, Icon the symbol.
type WeatherChip struct {
	Text, Icon string
}

// Event is a card in a person's column.
type Event struct {
	Title string
	Time  string // "09:00–10:00" or "9:00 AM–10:00 AM", by the household time format
	// Markers are the people of the event in display order, so black-and-white
	// mode tells them apart by shape.
	Markers []Marker
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

// Line kinds of a footer tile: a plain line (the meal tile) has no kind.
const (
	LineWaste = "waste"
	LineNote  = "note"
)

// Line is one line of a footer tile. The kind lets the template draw a waste
// collection and the parents' note differently.
type Line struct {
	Kind string
	Text string
	// NoteToday marks the note of today (before 18:00), not of tomorrow.
	NoteToday bool
}

// Tile is a footer tile.
type Tile struct {
	Label string
	Lines []Line
	Alert bool
}

// HasWaste reports whether the tile holds a waste collection.
func (t Tile) HasWaste() bool {
	for _, l := range t.Lines {
		if l.Kind == LineWaste {
			return true
		}
	}
	return false
}

// View is the data of one family view screen.
type View struct {
	Weekday       time.Weekday
	Week          int
	Date          time.Time
	Clock         string       // household time format; the display ticks it itself between renders
	ClockZone     string       // IANA name of the household time zone, for that ticking
	ClockFormat   string       // "24h" or "12h"
	Weather       *WeatherChip // nil: no reading, no chip
	Strip         []StripItem
	StripTomorrow []StripItem
	ShowTomorrow  bool
	SyncStale     bool // header notice: calendar not up to date
	Columns       []Column
	Meal          *Tile
	TomorrowTile  *Tile
}

const (
	tomorrowFromHour = 18 // the tomorrow block appears from 18:00
	maxTomorrow      = 3  // timed events per column in the tomorrow block
)

// Build lays out today's family view.
func Build(in Input) View {
	now := in.Now
	d0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	d1 := d0.AddDate(0, 0, 1)
	_, week := now.ISOWeek()
	v := View{Weekday: now.Weekday(), Week: week, Date: d0, Clock: ClockText(now, in.TimeFormat), ClockZone: now.Location().String(), ClockFormat: clockFormat(in.TimeFormat)}
	v.Weather = Chip(in.Weather, in.TemperatureUnit)
	v.ShowTomorrow = in.ShowTomorrow && now.Hour() >= tomorrowFromHour
	v.SyncStale = in.SyncStale
	v.TomorrowTile = tomorrowTile(in, now, d1)
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
					v.Columns[i].Events = append(v.Columns[i].Events, card(o, loc, in.TimeFormat, markers(in, o)))
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
		item.Time = clock(o, loc, in.TimeFormat)
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
					item.Time = clock(o, loc, in.TimeFormat)
				}
				v.StripTomorrow = append(v.StripTomorrow, item)
				continue
			}
			if o.AllDay || multiDay(o, loc) {
				continue
			}
			for i, p := range in.People {
				if sees(in, p, o) && len(v.Columns[i].Tomorrow) < maxTomorrow {
					v.Columns[i].Tomorrow = append(v.Columns[i].Tomorrow, card(o, loc, in.TimeFormat, markers(in, o)))
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
func clock(o calendar.Occurrence, loc *time.Location, format store.TimeFormat) string {
	l := layout(format)
	return fmt.Sprintf("%s–%s", o.Start.In(loc).Format(l), o.End.In(loc).Format(l))
}

func card(o calendar.Occurrence, loc *time.Location, format store.TimeFormat, m []Marker) Event {
	return Event{Title: o.Title, Time: clock(o, loc, format), Markers: m}
}

// ClockText is the time of day in the household's time format ("24h" or "12h").
func ClockText(t time.Time, format store.TimeFormat) string { return t.Format(layout(format)) }

// clockFormat names the time format for the display's script; 24h is the default.
func clockFormat(format store.TimeFormat) string {
	if format == store.Time12h {
		return "12h"
	}
	return "24h"
}

// layout is the time layout of the household's time format.
func layout(format store.TimeFormat) string {
	if format == store.Time12h {
		return "3:04 PM"
	}
	return "15:04"
}

// Chip builds the weather chip, or nil without a reading.
func Chip(w *Weather, unit store.TemperatureUnit) *WeatherChip {
	if w == nil {
		return nil
	}
	t, sym := w.TempC, "C"
	if unit == store.Fahrenheit {
		t, sym = t*9/5+32, "F"
	}
	n := int(math.Round(t)) // math.Round(-0.4) is -0, which int() turns into 0
	return &WeatherChip{Text: fmt.Sprintf("%d°%s", n, sym), Icon: w.Icon}
}

// markers lists the people who see o, in display order.
func markers(in Input, o calendar.Occurrence) []Marker {
	var out []Marker
	for _, p := range in.People {
		if sees(in, p, o) {
			out = append(out, Marker{Name: p.Name, Slot: p.Slot, Shape: p.Shape})
		}
	}
	return out
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
