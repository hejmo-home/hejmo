package caldav

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	ical "github.com/emersion/go-ical"
)

// A task goes out as a VTODO (ADR 0018): the box serves one task list per
// person. This file is pure, like the rest of the package.

// MaxTodoTitle and MaxTodoNotes bound what a client may save, in characters.
const (
	MaxTodoTitle = 120
	MaxTodoNotes = 2000
)

// Todo is what a CalDAV client sees of one task on one day. The box fills it
// from a task: a once task has Due (its own day), a repeating task has Start
// (today's occurrence) and no recurrence rule, so the client never expands
// anything itself.
type Todo struct {
	UID         string
	Title       string
	Notes       string
	Due         string // YYYY-MM-DD, once tasks only
	Start       string // YYYY-MM-DD, today's occurrence of a repeating task
	Done        bool
	CompletedAt time.Time
	Modified    time.Time // the task's last change; fixed so the same task renders the same bytes
}

func utcStamp(name string, t time.Time) *ical.Prop {
	p := ical.NewProp(name)
	p.Value = t.UTC().Format("20060102T150405Z")
	return p
}

func dayProp(name, day string) *ical.Prop {
	p := ical.NewProp(name)
	p.Params.Set(ical.ParamValue, "DATE")
	p.Value = strings.ReplaceAll(day, "-", "")
	return p
}

func textProp(c *ical.Component, name, value string) {
	p := ical.NewProp(name)
	p.Value = value
	c.Props.Set(p)
}

// Calendar renders the task as iCalendar. The same Todo always gives the same
// bytes, so the ETag follows the content.
func (t Todo) Calendar() (*ical.Calendar, error) {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, prodID)
	c := ical.NewComponent(ical.CompToDo)
	c.Props.SetText(ical.PropUID, t.UID)
	for _, name := range []string{ical.PropDateTimeStamp, ical.PropLastModified} {
		c.Props.Set(utcStamp(name, t.Modified))
	}
	c.Props.SetText(ical.PropSummary, t.Title)
	if t.Notes != "" {
		c.Props.SetText(ical.PropDescription, t.Notes)
	}
	if t.Start != "" {
		c.Props.Set(dayProp(ical.PropDateTimeStart, t.Start))
	}
	if t.Due != "" {
		c.Props.Set(dayProp(ical.PropDue, t.Due))
	}
	if t.Done {
		textProp(c, ical.PropStatus, "COMPLETED")
		textProp(c, ical.PropPercentComplete, "100")
		if !t.CompletedAt.IsZero() {
			c.Props.Set(utcStamp(ical.PropCompleted, t.CompletedAt))
		}
	} else {
		textProp(c, ical.PropStatus, "NEEDS-ACTION")
	}
	cal.Children = append(cal.Children, c)
	return cal, nil
}

// TodoEdit is what a client may change of a task: title, notes, the day of a
// once task and completion. Everything else it sends is ignored.
type TodoEdit struct {
	Title     string
	Notes     string
	Due       string // YYYY-MM-DD, "" if the client sent no due date
	Completed bool
}

// ParseTodo reads what a client saves: exactly one VTODO. Times are read in
// zone when they carry none.
func ParseTodo(cal *ical.Calendar, zone *time.Location) (TodoEdit, error) {
	bad := func(msg string) (TodoEdit, error) { return TodoEdit{}, fmt.Errorf("%w: %s", ErrInvalid, msg) }
	var todo *ical.Component
	for _, c := range cal.Children {
		switch c.Name {
		case ical.CompToDo:
			if todo != nil {
				return bad("one resource holds one task")
			}
			todo = c
		case ical.CompEvent, ical.CompJournal:
			return bad("a task list holds tasks only")
		}
	}
	if todo == nil {
		return bad("no task found")
	}
	title, _ := todo.Props.Text(ical.PropSummary)
	title = strings.TrimSpace(title)
	if title == "" {
		return bad("title required")
	}
	if utf8.RuneCountInString(title) > MaxTodoTitle {
		return bad(fmt.Sprintf("title may have at most %d characters", MaxTodoTitle))
	}
	notes, _ := todo.Props.Text(ical.PropDescription)
	notes = strings.TrimSpace(notes)
	if utf8.RuneCountInString(notes) > MaxTodoNotes {
		return bad(fmt.Sprintf("notes may have at most %d characters", MaxTodoNotes))
	}
	out := TodoEdit{Title: title, Notes: notes}
	if p := todo.Props.Get(ical.PropDue); p != nil {
		d, err := p.DateTime(zone)
		if err != nil {
			return bad("unreadable due date")
		}
		if p.ValueType() != ical.ValueDate {
			d = d.In(zone)
		}
		out.Due = d.Format("2006-01-02")
	}
	status, _ := todo.Props.Text(ical.PropStatus)
	switch strings.ToUpper(status) {
	case "COMPLETED":
		out.Completed = true
	case "":
		out.Completed = todo.Props.Get(ical.PropCompleted) != nil
	}
	return out, nil
}
