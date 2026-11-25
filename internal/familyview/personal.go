package familyview

import (
	"sort"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/layout"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/streak"
	"github.com/hejmo-home/hejmo/internal/task"
)

// PersonalDays is how many days the personal view covers: today and the next 6 (ADR 0015).
const PersonalDays = 7

// Task is one of the person's tasks for today, shown with a large checkbox.
type Task struct {
	ID, Title, Emoji string
	Part             string // "morning", "afterschool", "evening" or empty
	Done             bool
	Stars            int // credited when ticked off; 0 shows nothing
	// RoutineID is the routine the task is a step of, empty for a single task.
	// Position is its place there (from 1). Next is set by BuildPersonal on the
	// first open step of a routine.
	RoutineID string
	Position  int
	Next      bool
	// Subtasks are the items the task is split into, with their state today
	// (ADR 0018). Stars belong to the task only.
	Subtasks []Subtask
}

// Subtask is one subtask of a task for today. Next is set by Parts on the
// first open subtask of the next task of a routine, instead of on the task.
type Subtask struct {
	ID, Title, Emoji string
	Done             bool
	Next             bool
}

// SubtasksDoneCount is how many subtasks are ticked off, for templates.
func (t Task) SubtasksDoneCount() int { d, _ := t.SubtasksDone(); return d }

// SubtasksDone counts the subtasks ticked off and all subtasks.
func (t Task) SubtasksDone() (done, total int) {
	for _, s := range t.Subtasks {
		if s.Done {
			done++
		}
	}
	return done, len(t.Subtasks)
}

// Routine is a person's routine (ADR 0018) as the personal view needs it.
type Routine struct {
	ID, Name string
	Part     string // "morning", "afterschool" or "evening"
	// Week is the routine's week (ticket 294); nil while the person's switch
	// "week count and badges" is off.
	Week *streak.Week
}

// Stars is a person's star balance (ADR 0018). Shown is false for people with
// no task worth stars and no booking: the view shows nothing of stars then.
type Stars struct {
	Shown   bool
	Balance int
	// Goal is the savings goal (ticket 293); nil without one, or while stars are hidden.
	Goal *Goal
}

// Badge is an earned badge as the personal view shows it (ticket 295): its key
// picks the seal, Name is the text in the display's language.
type Badge struct{ Key, Name string }

// PersonalEvent is a line of the personal view.
type PersonalEvent struct {
	Title  string
	Time   string // empty for all-day events
	AllDay bool
	// Family is true when the event is for the whole household (it needs
	// PersonalInput.People to tell). The home page says "with the whole family".
	Family bool
}

// PersonalDay is one day of the personal view.
type PersonalDay struct {
	Date   time.Time
	Today  bool
	Events []PersonalEvent
}

// PersonalInput is everything BuildPersonal needs. Occurrences must cover
// today and the next 6 days; Tasks are the person's tasks due today.
type PersonalInput struct {
	Now    time.Time // in the household's time zone
	Person Person
	// People is the whole household, in display order. Optional: without it no
	// event is marked as being for the whole family.
	People      []Person
	Calendars   map[string]calendar.Audience
	Occurrences []calendar.Occurrence
	Tasks       []Task
	Routines    []Routine // the person's routines; their tasks are Tasks with a RoutineID
	Stars       Stars
	// Goal is the person's savings goal; it shows only while stars do.
	Goal *GoalInput
	// NewBadges and Badges are the person's earned badges, the new ones (today
	// or yesterday) apart, newest first. Both empty while the person's switch
	// "week count and badges" is off: the view then has no badges block.
	NewBadges, Badges []Badge
	// Parents are the parents' names in words ("Mama or Papa"): whom to ask
	// once the goal is reached.
	Parents string
	// TimeFormat is the household's "24h" (default) or "12h" (ADR 0016).
	TimeFormat store.TimeFormat
	// Layout is the display's layout; its header blocks (time, weather) apply
	// to the personal view too (Q130). Nil: the clock only.
	Layout          *layout.Layout
	Weather         *Weather
	TemperatureUnit store.TemperatureUnit
	// Home: the display's layout has exactly this person (Q88), so there is no
	// family view to go back to.
	Home bool
}

// PersonalView is the data of one person's screen on a display.
type PersonalView struct {
	Person      Person
	Date        time.Time
	Clock       string // household time format; the display ticks it itself between renders
	ClockZone   string // IANA name of the household time zone, for that ticking
	ClockFormat string // "24h" or "12h"
	Days        []PersonalDay
	Tasks       []Task
	Stars       Stars
	Routines    []Routine
	Parents     string // whom to ask once the goal is reached
	// NewBadges and Badges: see PersonalInput. The view shows a badges block at
	// the foot of the days when either holds something.
	NewBadges, Badges []Badge
	Weather           *WeatherChip
	// HeaderOrder names the header blocks that are on, in order.
	HeaderOrder []layout.BlockID
	// Home: this is the display's home screen, with no way back to a family view.
	Home bool
}

// BuildPersonal lays out one person's view: their events for today and the
// next 6 days and their tasks for today (ADR 0015).
func BuildPersonal(in PersonalInput) PersonalView {
	now := in.Now
	loc := now.Location()
	d0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	v := PersonalView{Person: in.Person, Date: d0, Clock: ClockText(now, in.TimeFormat), ClockZone: loc.String(), ClockFormat: clockFormat(in.TimeFormat), Stars: in.Stars, Parents: in.Parents, Home: in.Home, NewBadges: in.NewBadges, Badges: in.Badges}
	if in.Stars.Shown {
		v.Stars.Goal = goalOf(in.Goal, in.Stars.Balance)
	}
	if in.Layout == nil {
		v.HeaderOrder = []layout.BlockID{layout.Clock}
	} else {
		if in.Layout.Enabled(layout.Weather) {
			v.Weather = Chip(in.Weather, in.TemperatureUnit)
		}
		for _, b := range in.Layout.In(layout.Header) {
			if b.On && (b.ID != layout.Weather || v.Weather != nil) {
				v.HeaderOrder = append(v.HeaderOrder, b.ID)
			}
		}
	}
	occs := append([]calendar.Occurrence(nil), in.Occurrences...)
	sort.SliceStable(occs, func(i, j int) bool { return occs[i].Start.Before(occs[j].Start) })
	finput := Input{Calendars: in.Calendars, People: in.People}
	var mine []calendar.Occurrence
	for _, o := range occs {
		if sees(finput, in.Person, o) {
			mine = append(mine, o)
		}
	}
	for i := range PersonalDays {
		day := d0.AddDate(0, 0, i)
		next := day.AddDate(0, 0, 1)
		d := PersonalDay{Date: day, Today: i == 0}
		var allDay, timedEvents []PersonalEvent
		for _, o := range mine {
			if !o.Start.Before(next) || !o.End.After(day) {
				continue
			}
			fam := len(in.People) > 0 && wholeHousehold(finput, o)
			switch {
			case o.AllDay:
				allDay = append(allDay, PersonalEvent{Title: o.Title, AllDay: true, Family: fam})
			case !multiDay(o, loc):
				timedEvents = append(timedEvents, PersonalEvent{Title: o.Title, Time: clock(o, loc, in.TimeFormat), Family: fam})
			case firstDay(o, loc).Equal(day):
				timedEvents = append(timedEvents, PersonalEvent{Title: o.Title, Time: o.Start.In(loc).Format(timeLayout(in.TimeFormat)), Family: fam})
			case calendar.LastDay(o, loc).Equal(day) && !endsAtMidnight(o, loc):
				timedEvents = append(timedEvents, PersonalEvent{Title: o.Title, Time: "–" + o.End.In(loc).Format(timeLayout(in.TimeFormat)), Family: fam})
			default:
				allDay = append(allDay, PersonalEvent{Title: o.Title, AllDay: true, Family: fam})
			}
		}
		d.Events = append(allDay, timedEvents...)
		v.Days = append(v.Days, d)
	}
	v.Routines = in.Routines
	v.Tasks = append([]Task(nil), in.Tasks...)
	sort.SliceStable(v.Tasks, func(i, j int) bool {
		return task.PartOfDay(v.Tasks[i].Part).Rank() < task.PartOfDay(v.Tasks[j].Part).Rank()
	})
	return v
}

// endsAtMidnight reports whether the event ends exactly at 00:00, so its last
// day is covered completely.
func endsAtMidnight(o calendar.Occurrence, loc *time.Location) bool {
	e := o.End.In(loc)
	return e.Hour() == 0 && e.Minute() == 0 && e.Second() == 0
}

// Progress is the person's task progress today, the same figure the family
// view shows in their column.
func (v PersonalView) Progress() Progress { return ProgressOf(v.Tasks) }

// ProgressOf counts the done tasks of a person's tasks for today.
func ProgressOf(tasks []Task) Progress {
	p := Progress{Total: len(tasks)}
	for _, t := range tasks {
		if t.Done {
			p.Done++
		}
	}
	return p
}

// RoutineBox is a routine on the personal view: its steps in the parents'
// order with its own progress. Done steps keep their place.
type RoutineBox struct {
	ID, Name string
	Tasks    []Task
	Progress Progress
	AllDone  bool
	Week     *streak.Week // nil while the switch is off
}

// TaskPart is the content of one part of the day: the person's routine for it,
// if it has a step due today, then the single tasks in the stored order.
type TaskPart struct {
	Part    string // "morning", "afterschool", "" (any time today) or "evening"
	Routine *RoutineBox
	Tasks   []Task
}

// Parts groups the tasks by part of the day, in the order morning, after
// school, any time, evening, leaving out the parts without tasks. A step of a
// routine the view does not know is shown like a single task.
func (v PersonalView) Parts() []TaskPart {
	known := map[string]Routine{}
	for _, r := range v.Routines {
		known[r.ID] = r
	}
	var parts []TaskPart
	for _, part := range []task.PartOfDay{task.Morning, task.AfterSchool, task.AnyTime, task.Evening} {
		p := TaskPart{Part: string(part)}
		var box *RoutineBox
		for _, t := range v.Tasks {
			if t.Part != p.Part {
				continue
			}
			r, inRoutine := known[t.RoutineID]
			if !inRoutine || t.RoutineID == "" {
				p.Tasks = append(p.Tasks, t)
				continue
			}
			if box == nil {
				box = &RoutineBox{ID: r.ID, Name: r.Name, Week: r.Week}
			}
			box.Tasks = append(box.Tasks, t)
		}
		if box != nil {
			sort.SliceStable(box.Tasks, func(i, j int) bool { return box.Tasks[i].Position < box.Tasks[j].Position })
			box.Progress = ProgressOf(box.Tasks)
			box.AllDone = box.Progress.Done == box.Progress.Total
			if !box.AllDone {
				for i := range box.Tasks {
					if !box.Tasks[i].Done {
						box.Tasks[i].markNext()
						break
					}
				}
			}
			p.Routine = box
		}
		if p.Routine != nil || len(p.Tasks) > 0 {
			parts = append(parts, p)
		}
	}
	return parts
}

// Percent is the filled share of the progress bar, 0 to 100.
func (v PersonalView) Percent() int { return v.Progress().Percent() }

// markNext marks a task as the next step. A task with subtasks gives the mark
// to its first open subtask, so a child who cannot read sees where to go on.
func (t *Task) markNext() {
	subs := append([]Subtask(nil), t.Subtasks...)
	for i := range subs {
		if !subs[i].Done {
			subs[i].Next = true
			t.Subtasks = subs
			return
		}
	}
	t.Next = true
}
