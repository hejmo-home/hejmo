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
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/layout"
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/wholine"
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
	Now    time.Time // in the household's time zone
	Lang   string    // "de" or "en", for holiday names
	Region string    // home region (ISO 3166-2)
	People []Person  // the people of the columns, in column order
	// Everyone is the whole household, for who an event is meant for and what
	// counts as the whole family (Q87). Nil: People are everyone.
	Everyone []Person
	Groups   []wholine.Group // subgroups with their members, for the who-line
	// I18n words the who-line; required, whoLine panics without it.
	I18n        *i18n.Bundle
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
	// Meals is the effective meal per mealtime today (a plain entry, and later
	// a template's default unless overridden for the day); empty text counts
	// as not planned. MealCutoffs are the household's cut-off times.
	Meals       map[meal.Mealtime]string
	MealCutoffs meal.Cutoffs
	// TaskProgress is each person's task progress today, by person ID. A
	// person without tasks due today has no entry (or a zero total) and
	// shows no bar (ADR 0016).
	TaskProgress map[string]Progress
	// HasLists: the household has at least one list. The header then shows the
	// Lists button (ticket 6.2); without a list there is nothing to open.
	HasLists bool
	// Layout is the display's layout (ticket 180): which blocks are on and in
	// which order. Nil is today's layout. People must already be in the order
	// of the columns (layout.Order).
	Layout *layout.Layout
	// Routines are each person's routines due today, by person ID.
	Routines map[string][]RoutineLine
	// List is the list of the block "one list"; nil when the block has none.
	List *ListInput
}

// RoutineLine is one routine of a person today with its progress (Q86).
type RoutineLine struct {
	Name  string
	Part  string // "morning", "afterschool" or "evening"
	Done  int
	Total int
}

// AllDone reports whether every step of the routine is ticked off.
func (r RoutineLine) AllDone() bool { return r.Total > 0 && r.Done >= r.Total }

// ListInput is the list of the footer block "one list": its open entries in the list's order.
type ListInput struct {
	ID, Name string
	Open     []string
}

// ListTile is the list tile of the footer: up to layout.MaxListEntries open
// entries in full; More says how many open entries are not shown (Q129).
type ListTile struct {
	ID, Name string
	Entries  []string
	More     int
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
	// WhoParts say in words who the event is for, leaving out the column's own
	// person (Q62); empty on a card for that person alone. One part per name, so
	// the page keeps each name whole; wholine.Plain joins them.
	WhoParts []wholine.Part
}

// Progress is a person's task progress today: tasks done of tasks due (ticket 6.6, ADR 0016).
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
	Routines []RoutineLine
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
	Partial         bool           // partial public holiday
	WhoParts        []wholine.Part // who it is for, in square brackets, shown after the title; empty for the whole family
	Who             string         // WhoParts without the brackets, as plain text
	DayIndex        int            // multi-day: this is day DayIndex of DayCount (1-based)
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

// MealEntry is one row of the meal tile.
type MealEntry struct {
	Mealtime meal.Mealtime
	Dish     string
}

// MealTile is the meal tile of the footer (ADR 0016): the mealtimes of today
// that are still to come. Label is "Meals today" for several entries, or the
// mealtime's word for one. The tile is never highlighted.
type MealTile struct {
	Label   string
	Entries []MealEntry
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
	HasLists      bool // header button to the lists
	Columns       []Column
	Meal          *MealTile // nil: nothing planned that is still to come
	TomorrowTile  *Tile
	List          *ListTile
	// HeaderOrder, ColumnBlocks and FooterOrder name the blocks that are on, in
	// order (ticket 180). Weather without a reading is left out of the header; a
	// footer block without content (nothing planned, an empty list) has no tile.
	HeaderOrder, ColumnBlocks, FooterOrder []layout.BlockID
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
	lay := layout.Default(nil)
	if in.Layout != nil {
		lay = *in.Layout
	}
	if lay.Enabled(layout.Weather) {
		v.Weather = Chip(in.Weather, in.TemperatureUnit)
	}
	for _, b := range lay.In(layout.Header) {
		if b.On && (b.ID != layout.Weather || v.Weather != nil) {
			v.HeaderOrder = append(v.HeaderOrder, b.ID)
		}
	}
	for _, b := range lay.In(layout.Columns) {
		if b.On {
			v.ColumnBlocks = append(v.ColumnBlocks, b.ID)
		}
	}
	events := lay.Enabled(layout.Events)
	v.ShowTomorrow = in.ShowTomorrow && now.Hour() >= tomorrowFromHour
	v.SyncStale = in.SyncStale
	v.HasLists = in.HasLists
	if lay.Enabled(layout.Tomorrow) {
		v.TomorrowTile = tomorrowTile(in, now, d1)
	}
	for _, p := range in.People {
		col := Column{Person: p}
		if pr, ok := in.TaskProgress[p.ID]; ok && pr.Total > 0 && lay.Enabled(layout.Tasks) {
			col.Progress = &pr
		}
		if lay.Enabled(layout.Routines) {
			col.Routines = in.Routines[p.ID]
		}
		v.Columns = append(v.Columns, col)
	}
	if lay.Enabled(layout.Meals) {
		v.Meal = mealTile(in)
	}
	if lay.Enabled(layout.List) && in.List != nil && len(in.List.Open) > 0 {
		t := &ListTile{ID: in.List.ID, Name: in.List.Name, Entries: in.List.Open}
		if len(t.Entries) > layout.MaxListEntries {
			t.More = len(t.Entries) - layout.MaxListEntries
			t.Entries = t.Entries[:layout.MaxListEntries]
		}
		v.List = t
	}
	for _, b := range lay.In(layout.Footer) {
		if b.On {
			v.FooterOrder = append(v.FooterOrder, b.ID)
		}
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
				if events && sees(in, p, o) {
					v.Columns[i].Events = append(v.Columns[i].Events, card(o, loc, in.TimeFormat, whoLine(in, o, p.ID)))
				}
			}
			continue
		}
		item := StripItem{Kind: KindEvent, Title: o.Title, AllDay: o.AllDay}
		if !whole {
			audience := audienceOf(in, o)
			if len(audience) == 0 {
				continue
			}
			if l := whoLine(in, o, ""); len(l.BracketParts) > 0 {
				item.WhoParts = append(append([]wholine.Part{{Text: "["}}, l.BracketParts...), wholine.Part{Text: "]"})
				item.Who = l.InBrackets
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
				if events && sees(in, p, o) && len(v.Columns[i].Tomorrow) < maxTomorrow {
					v.Columns[i].Tomorrow = append(v.Columns[i].Tomorrow, card(o, loc, in.TimeFormat, whoLine(in, o, p.ID)))
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
	l := timeLayout(format)
	return fmt.Sprintf("%s–%s", o.Start.In(loc).Format(l), o.End.In(loc).Format(l))
}

func card(o calendar.Occurrence, loc *time.Location, format store.TimeFormat, who wholine.Line) Event {
	return Event{Title: o.Title, Time: clock(o, loc, format), WhoParts: who.Parts}
}

// ClockText is the time of day in the household's time format ("24h" or "12h").
func ClockText(t time.Time, format store.TimeFormat) string { return t.Format(timeLayout(format)) }

// ClockFormat names the time format for the display's script ("24h" or "12h").
func ClockFormat(format store.TimeFormat) string { return clockFormat(format) }

// clockFormat names the time format for the display's script; 24h is the default.
func clockFormat(format store.TimeFormat) string {
	if format == store.Time12h {
		return "12h"
	}
	return "24h"
}

// timeLayout is the time layout of the household's time format.
func timeLayout(format store.TimeFormat) string {
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

// audienceOf lists the IDs of the people who see o, in display order.
func audienceOf(in Input, o calendar.Occurrence) []string {
	all := in.everyone()
	viewers := make([]calendar.Viewer, len(all))
	for i, p := range all {
		viewers[i] = calendar.Viewer{PersonID: p.ID, Groups: p.Groups}
	}
	return calendar.ViewersOf(o, in.Calendars, viewers)
}

// whoLine words who o is for; omit is the person of the column it is shown in.
func whoLine(in Input, o calendar.Occurrence, omit string) wholine.Line {
	all := in.everyone()
	household := make([]wholine.Person, len(all))
	for i, p := range all {
		household[i] = wholine.Person{ID: p.ID, Name: p.Name}
	}
	if in.I18n == nil {
		panic("familyview: Input.I18n is required to word the who-line")
	}
	return wholine.Of(wholine.Input{Household: household, Groups: in.Groups, Audience: audienceOf(in, o), Omit: omit}, in.I18n, in.Lang)
}

// wholeHousehold reports whether o is meant for the whole household: it has no
// audience, or its effective audience lists everyone. Either way it belongs to
// the family strip, never to a column.
func wholeHousehold(in Input, o calendar.Occurrence) bool {
	a := in.Calendars[o.CalendarID]
	if len(a.People) == 0 && len(a.Groups) == 0 && len(o.People) == 0 && len(o.Groups) == 0 {
		return true
	}
	return len(in.everyone()) > 0 && len(audienceOf(in, o)) == len(in.everyone())
}

// everyone is the whole household.
func (in Input) everyone() []Person {
	if in.Everyone != nil {
		return in.Everyone
	}
	return in.People
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

// mealTile keeps every mealtime planned for today whose cut-off is empty or
// later than now, in the order of the day; nil when none is left.
func mealTile(in Input) *MealTile {
	hhmm := in.Now.Format("15:04")
	var t MealTile
	for _, m := range meal.Mealtimes() {
		dish := in.Meals[m]
		if dish == "" {
			continue
		}
		// Cut-offs are stored as zero-padded HH:MM, so the string compare is a time compare.
		if cut := in.MealCutoffs.For(m); cut != "" && cut <= hhmm {
			continue
		}
		t.Entries = append(t.Entries, MealEntry{Mealtime: m, Dish: dish})
	}
	switch len(t.Entries) {
	case 0:
		return nil
	case 1:
		t.Label = in.I18n.T(in.Lang, "meals.mealtime."+string(t.Entries[0].Mealtime))
	default:
		t.Label = in.I18n.T(in.Lang, "family.meals_today")
	}
	return &t
}
