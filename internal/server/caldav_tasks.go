package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	ical "github.com/emersion/go-ical"
	webcal "github.com/emersion/go-webdav/caldav"

	hcal "github.com/hejmo-home/hejmo/internal/caldav"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

// Task lists over CalDAV (ADR 0018): one list per person, below the calendar
// home so that clients find it with the calendars:
//
//	/dav/{person}/calendars/tasks-{owner id}/            one person's task list
//	/dav/{person}/calendars/tasks-{owner id}/{task}.ics  one task
//
// A client sees a once task as it is and a repeating task as today's
// occurrence. It may change title, notes, the day of a once task and
// completion; creating and deleting are refused (403), so the list is
// restored at its next sync.
const taskListPrefix = "tasks-"

// recentDays is how long a done once task stays in its list.
const recentDays = 7

func isTaskPath(p string) bool { return davResource(p) == "tasks" }

// taskAddress is a parsed address below a task list.
type taskAddress struct {
	owner string // the person whose tasks the list holds
	task  string // "" for the list itself
}

func parseTaskPath(sess *davSession, p string) (taskAddress, bool) {
	rest, ok := strings.CutPrefix(p, davPrefix+"/"+sess.person.ID+"/calendars/"+taskListPrefix)
	if !ok {
		return taskAddress{}, false
	}
	parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] != "":
		return taskAddress{owner: parts[0]}, true
	case len(parts) == 2 && !strings.HasSuffix(rest, "/"):
		id, isICS := strings.CutSuffix(parts[1], ".ics")
		return taskAddress{owner: parts[0], task: id}, isICS && id != ""
	}
	return taskAddress{}, false
}

func (b *davBackend) taskListPath(sess *davSession, owner store.Person) string {
	return b.homePath(sess) + taskListPrefix + owner.ID + "/"
}

func (b *davBackend) toTaskList(sess *davSession, owner store.Person) webcal.Calendar {
	return webcal.Calendar{Path: b.taskListPath(sess, owner), Name: owner.Name, SupportedComponentSet: []string{ical.CompToDo}}
}

func (b *davBackend) taskLists(sess *davSession) ([]webcal.Calendar, error) {
	people, err := b.s.db.People(sess.house.ID)
	if err != nil {
		return nil, err
	}
	out := make([]webcal.Calendar, 0, len(people))
	for _, p := range people {
		out = append(out, b.toTaskList(sess, p))
	}
	return out, nil
}

// taskListAt resolves a path to the person whose list it is (404 for unknown ones).
func (b *davBackend) taskListAt(sess *davSession, p string) (store.Person, taskAddress, error) {
	addr, ok := parseTaskPath(sess, p)
	if !ok {
		return store.Person{}, taskAddress{}, httpError(http.StatusNotFound, "no such resource")
	}
	owner, err := b.s.db.Person(sess.house.ID, addr.owner)
	if errors.Is(err, store.ErrNotFound) {
		return store.Person{}, taskAddress{}, httpError(http.StatusNotFound, "no such task list")
	}
	return owner, addr, err
}

func (b *davBackend) getTaskList(sess *davSession, p string) (*webcal.Calendar, error) {
	owner, addr, err := b.taskListAt(sess, p)
	if err != nil {
		return nil, err
	}
	if addr.task != "" {
		return nil, httpError(http.StatusNotFound, "not a task list")
	}
	out := b.toTaskList(sess, owner)
	return &out, nil
}

// offered is one task as a client sees it today.
type offered struct {
	t    task.Task
	day  string // the occurrence: the task's own day for a once task, else today
	done bool
	at   time.Time
}

// offeredTasks lists what the person's task list shows: open once tasks and
// those done in the last week, and today's occurrence of every repeating task
// due today.
func (b *davBackend) offeredTasks(sess *davSession, owner store.Person) ([]offered, error) {
	zone := householdZone(sess.house)
	today := b.s.now().In(zone)
	todayDay := today.Format(task.DateLayout)
	recent := today.AddDate(0, 0, -recentDays).Format(task.DateLayout)
	all, err := b.s.db.Tasks(sess.house.ID)
	if err != nil {
		return nil, err
	}
	var out []offered
	for _, t := range all {
		if t.PersonID != owner.ID {
			continue
		}
		o := offered{t: t, day: todayDay}
		if t.Repeat == task.Once {
			o.day = t.Date
		} else if !t.DueOn(todayDay) {
			continue
		}
		at, done, err := b.s.db.TaskCompletedAt(t.ID, o.day)
		if err != nil {
			return nil, err
		}
		o.done, o.at = done, at
		if t.Repeat == task.Once && done && t.Date < recent {
			continue
		}
		out = append(out, o)
	}
	return out, nil
}

func (b *davBackend) todoObject(sess *davSession, owner store.Person, o offered) (webcal.CalendarObject, error) {
	td := hcal.Todo{UID: o.t.ID, Title: o.t.Title, Notes: o.t.Notes, Done: o.done, CompletedAt: o.at, Modified: o.t.UpdatedAt}
	if o.t.Repeat == task.Once {
		td.Due = o.t.Date
	} else {
		td.Start = o.day
	}
	cal, err := td.Calendar()
	if err != nil {
		return webcal.CalendarObject{}, err
	}
	tag, err := hcal.ETag(cal)
	if err != nil {
		return webcal.CalendarObject{}, err
	}
	mod := o.t.UpdatedAt
	if o.at.After(mod) {
		mod = o.at
	}
	return webcal.CalendarObject{Path: b.taskListPath(sess, owner) + o.t.ID + ".ics", ModTime: mod, ETag: tag, Data: cal}, nil
}

func (b *davBackend) listTodos(sess *davSession, p string) ([]webcal.CalendarObject, error) {
	owner, _, err := b.taskListAt(sess, p)
	if err != nil {
		return nil, err
	}
	list, err := b.offeredTasks(sess, owner)
	if err != nil {
		return nil, err
	}
	out := make([]webcal.CalendarObject, 0, len(list))
	for _, o := range list {
		co, err := b.todoObject(sess, owner, o)
		if err != nil {
			return nil, err
		}
		out = append(out, co)
	}
	return out, nil
}

// offeredTask finds one task in the list it is asked for: nil if the list does
// not offer it today.
func (b *davBackend) offeredTask(sess *davSession, p string) (store.Person, *offered, error) {
	owner, addr, err := b.taskListAt(sess, p)
	if err != nil {
		return owner, nil, err
	}
	if addr.task == "" {
		return owner, nil, httpError(http.StatusNotFound, "not a task")
	}
	list, err := b.offeredTasks(sess, owner)
	if err != nil {
		return owner, nil, err
	}
	for _, o := range list {
		if o.t.ID == addr.task {
			return owner, &o, nil
		}
	}
	return owner, nil, nil
}

func (b *davBackend) getTodo(sess *davSession, p string) (*webcal.CalendarObject, error) {
	owner, o, err := b.offeredTask(sess, p)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, httpError(http.StatusNotFound, "no such task")
	}
	co, err := b.todoObject(sess, owner, *o)
	return &co, err
}

func (b *davBackend) putTodo(sess *davSession, p string, cal *ical.Calendar, opts *webcal.PutCalendarObjectOptions) (*webcal.CalendarObject, error) {
	owner, o, err := b.offeredTask(sess, p)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, httpError(http.StatusForbidden, "tasks are created in the box's web interface: a task needs its person")
	}
	edit, err := hcal.ParseTodo(cal, householdZone(sess.house))
	if err != nil {
		return nil, httpError(http.StatusBadRequest, err.Error())
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	// Reload under the lock: the precondition and the changes see the same task.
	_, o, err = b.offeredTask(sess, p)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, httpError(http.StatusPreconditionFailed, "the task is no longer on this list")
	}
	current, err := b.todoObject(sess, owner, *o)
	if err != nil {
		return nil, err
	}
	if err := checkETag(current.ETag, true, opts.IfMatch, opts.IfNoneMatch); err != nil {
		return nil, err
	}

	change := store.TaskEdit{Title: &edit.Title, Notes: &edit.Notes}
	if o.t.Repeat == task.Once && edit.Due != "" {
		change.Date = &edit.Due
	}
	if err := b.s.db.EditTask(sess.house.ID, o.t.ID, change); err != nil {
		if errors.Is(err, store.ErrInvalid) {
			return nil, httpError(http.StatusBadRequest, err.Error())
		}
		return nil, err
	}
	// Completion counts on the occurrence the client sees (a once task's done
	// day moved with its date). The stars are booked by SetTaskDone, which
	// credits and deducts once however often the client repeats itself.
	day := o.day
	if change.Date != nil {
		day = *change.Date
	}
	if edit.Completed != o.done {
		if err := b.s.db.SetTaskDone(sess.house.ID, o.t.ID, day, edit.Completed, sess.person.ID, b.s.now()); err != nil {
			if errors.Is(err, store.ErrInvalid) {
				return nil, httpError(http.StatusBadRequest, err.Error())
			}
			return nil, err
		}
	}
	_, saved, err := b.offeredTask(sess, p)
	if err != nil || saved == nil {
		return nil, fmt.Errorf("reload saved task: %v", err)
	}
	co, err := b.todoObject(sess, owner, *saved)
	return &co, err
}

func (b *davBackend) deleteTodo(sess *davSession, p string) error {
	_, o, err := b.offeredTask(sess, p)
	if err != nil {
		return err
	}
	if o == nil {
		return httpError(http.StatusNotFound, "no such task")
	}
	return httpError(http.StatusForbidden, "tasks are deleted in the box's web interface")
}
