package familyview

import (
	"sort"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/task"
)

// PersonalDays is how many days the personal view covers: today and the next 6 (ADR 0015).
const PersonalDays = 7

// Task is one of the person's tasks for today, shown with a large checkbox.
type Task struct {
	ID, Title, Emoji string
	Part             string // "morning", "evening" or empty
	Done             bool
	Stars            int // credited when ticked off; 0 shows nothing
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
	Now         time.Time // in the household's time zone
	Person      Person
	Calendars   map[string]calendar.Audience
	Occurrences []calendar.Occurrence
	Tasks       []Task
	Stars       Stars
}

// PersonalView is the data of one person's screen on a display.
type PersonalView struct {
	Person    Person
	Date      time.Time
	Clock     string // "15:04"; the display ticks it itself between renders
	ClockZone string // IANA name of the household time zone, for that ticking
	Days      []PersonalDay
	Tasks     []Task
	Stars     Stars
}

// BuildPersonal lays out one person's view: their events for today and the
// next 6 days and their tasks for today (ADR 0015).
func BuildPersonal(in PersonalInput) PersonalView {
	now := in.Now
	loc := now.Location()
	d0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	v := PersonalView{Person: in.Person, Date: d0, Clock: now.Format("15:04"), ClockZone: loc.String(), Stars: in.Stars}
	occs := append([]calendar.Occurrence(nil), in.Occurrences...)
	sort.SliceStable(occs, func(i, j int) bool { return occs[i].Start.Before(occs[j].Start) })
	finput := Input{Calendars: in.Calendars}
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
			switch {
			case o.AllDay:
				allDay = append(allDay, PersonalEvent{Title: o.Title, AllDay: true})
			case !multiDay(o, loc):
				timedEvents = append(timedEvents, PersonalEvent{Title: o.Title, Time: clock(o, loc)})
			case firstDay(o, loc).Equal(day):
				timedEvents = append(timedEvents, PersonalEvent{Title: o.Title, Time: o.Start.In(loc).Format("15:04")})
			case calendar.LastDay(o, loc).Equal(day) && !endsAtMidnight(o, loc):
				timedEvents = append(timedEvents, PersonalEvent{Title: o.Title, Time: "–" + o.End.In(loc).Format("15:04")})
			default:
				allDay = append(allDay, PersonalEvent{Title: o.Title, AllDay: true})
			}
		}
		d.Events = append(allDay, timedEvents...)
		v.Days = append(v.Days, d)
	}
	rank := map[task.PartOfDay]int{task.Morning: 0, task.AnyTime: 1, task.Evening: 2}
	v.Tasks = append([]Task(nil), in.Tasks...)
	sort.SliceStable(v.Tasks, func(i, j int) bool {
		return rank[task.PartOfDay(v.Tasks[i].Part)] < rank[task.PartOfDay(v.Tasks[j].Part)]
	})
	return v
}

// endsAtMidnight reports whether the event ends exactly at 00:00, so its last
// day is covered completely.
func endsAtMidnight(o calendar.Occurrence, loc *time.Location) bool {
	e := o.End.In(loc)
	return e.Hour() == 0 && e.Minute() == 0 && e.Second() == 0
}
