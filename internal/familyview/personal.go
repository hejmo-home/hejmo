package familyview

import (
	"sort"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/store"
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
}

// Stars is a person's star balance (ADR 0018). Shown is false for people with
// no task worth stars and no booking: the view shows nothing of stars then.
type Stars struct {
	Shown   bool
	Balance int
}

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
	// TimeFormat is the household's "24h" (default) or "12h" (ADR 0016).
	TimeFormat store.TimeFormat
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
}

// BuildPersonal lays out one person's view: their events for today and the
// next 6 days and their tasks for today (ADR 0015).
func BuildPersonal(in PersonalInput) PersonalView {
	now := in.Now
	loc := now.Location()
	d0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	v := PersonalView{Person: in.Person, Date: d0, Clock: ClockText(now, in.TimeFormat), ClockZone: loc.String(), ClockFormat: clockFormat(in.TimeFormat), Stars: in.Stars}
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
				timedEvents = append(timedEvents, PersonalEvent{Title: o.Title, Time: o.Start.In(loc).Format(layout(in.TimeFormat)), Family: fam})
			case calendar.LastDay(o, loc).Equal(day) && !endsAtMidnight(o, loc):
				timedEvents = append(timedEvents, PersonalEvent{Title: o.Title, Time: "–" + o.End.In(loc).Format(layout(in.TimeFormat)), Family: fam})
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
				box = &RoutineBox{ID: r.ID, Name: r.Name}
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
