package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	ical "github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

// Task lists over CalDAV (ADR 0018, ticket 7.5). The test box's clock is
// Saturday 14/11/2026 12:00 UTC.

type davTaskBox struct {
	c       *apiClient
	h       store.Household
	mia     store.Person
	mama    store.Person
	swim    task.Task // once, 20/11, 2 stars
	cat     task.Task // daily, 1 star
	monday  task.Task // weekly on Mondays: not due on the test day
	ph      *phone
	listing string // Mia's task list path
}

func newDavTaskBox(t *testing.T) davTaskBox {
	t.Helper()
	c := signedInAPI(t)
	h, _ := familyOf(t, c)
	mia, err := c.b.db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	if err != nil {
		t.Fatal(err)
	}
	add := func(k task.Task) task.Task {
		k.PersonID = mia.ID
		out, err := c.b.db.AddTask(h.ID, k)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	people, _ := c.b.db.People(h.ID)
	tb := davTaskBox{c: c, h: h, mia: mia}
	for _, p := range people {
		if p.Name == "Mama" {
			tb.mama = p
		}
	}
	tb.swim = add(task.Task{Title: "Pack swim bag", Notes: "Towel", Repeat: task.Once, Date: "2026-11-20", Stars: 2})
	tb.cat = add(task.Task{Title: "Feed the cat", Repeat: task.Daily, Date: "2026-11-01", Stars: 1})
	tb.monday = add(task.Task{Title: "Piano", Repeat: task.Weekly, Weekdays: []string{"MO"}, Date: "2026-11-01", Stars: 1})
	tb.ph = c.phone(t, "calendars tasks")
	tb.listing = tb.ph.calendarNamed("Mia").Path
	return tb
}

func (tb davTaskBox) href(k task.Task) string { return tb.listing + k.ID + ".ics" }

func todoICS(uid string, lines ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//EN\r\nBEGIN:VTODO\r\nUID:" + uid + "\r\nDTSTAMP:20261114T120000Z\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
}

// todos queries a task list the way Reminders does and returns title -> VTODO.
func (p *phone) todos(path string) map[string]*ical.Component {
	p.t.Helper()
	objs, err := p.client.QueryCalendar(context.Background(), path, &caldav.CalendarQuery{
		CompFilter: caldav.CompFilter{Name: "VCALENDAR", Comps: []caldav.CompFilter{{Name: "VTODO"}}},
	})
	if err != nil {
		p.t.Fatalf("query %s: %v", path, err)
	}
	out := map[string]*ical.Component{}
	for _, o := range objs {
		for _, c := range o.Data.Children {
			if c.Name == ical.CompToDo {
				s, _ := c.Props.Text(ical.PropSummary)
				out[s] = c
			}
		}
	}
	return out
}

func (tb davTaskBox) put(href, body string, header map[string]string) int {
	h := map[string]string{"Content-Type": icsType}
	for k, v := range header {
		h[k] = v
	}
	status, _, _ := tb.ph.raw("PUT", href, body, h)
	return status
}

func (tb davTaskBox) balance(t *testing.T) int {
	t.Helper()
	acc, err := tb.c.b.db.StarAccount(tb.h.ID, tb.mia.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	return acc.Balance
}

// Acceptance: the box's CalDAV interface offers one task list per person.
func TestCalDAVOffersOneTaskListPerPersonWithTheTasksDueToday(t *testing.T) {
	t.Parallel()
	tb := newDavTaskBox(t)
	cals, err := tb.ph.calendars()
	if err != nil {
		t.Fatal(err)
	}
	lists := map[string]caldav.Calendar{}
	for _, cal := range cals {
		lists[cal.Name] = cal
	}
	for _, name := range []string{"Family", "Mama", "Mia"} {
		if _, ok := lists[name]; !ok {
			t.Fatalf("no %q among %v", name, lists)
		}
	}
	if got := lists["Mia"].SupportedComponentSet; len(got) != 1 || got[0] != "VTODO" {
		t.Fatalf("a task list holds tasks only, got %v", got)
	}
	if got := lists["Family"].SupportedComponentSet; len(got) != 1 || got[0] != "VEVENT" {
		t.Fatalf("the family calendar holds events only, got %v", got)
	}

	todos := tb.ph.todos(tb.listing)
	if len(todos) != 2 || todos["Pack swim bag"] == nil || todos["Feed the cat"] == nil {
		t.Fatalf("Mia's list = %v; want her once task and today's occurrence of the daily one, not the Monday task", todos)
	}
	swim := todos["Pack swim bag"]
	if due := swim.Props.Get(ical.PropDue); due == nil || due.Value != "20261120" {
		t.Errorf("DUE = %v", due)
	}
	if d, _ := swim.Props.Text(ical.PropDescription); d != "Towel" {
		t.Errorf("DESCRIPTION = %q", d)
	}
	cat := todos["Feed the cat"]
	if start := cat.Props.Get(ical.PropDateTimeStart); start == nil || start.Value != "20261114" || cat.Props.Get(ical.PropRecurrenceRule) != nil {
		t.Errorf("a repeating task shows today's occurrence: DTSTART %v, RRULE %v", start, cat.Props.Get(ical.PropRecurrenceRule))
	}
	if status, _, _ := tb.ph.raw("GET", tb.href(tb.monday), "", nil); status != http.StatusNotFound {
		t.Errorf("a repeating task not due today = %d, want 404", status)
	}
	if got := tb.ph.titles(lists["Family"].Path); len(got) != 0 {
		t.Errorf("task lists leaked into the calendar: %v", got)
	}
}

// Acceptance: Given a device token with only calendars scope, then task lists
// are not visible.
func TestTaskListsNeedTheTasksScope(t *testing.T) {
	t.Parallel()
	tb := newDavTaskBox(t)
	only := tb.c.phone(t, "calendars")
	cals, _ := only.calendars()
	for _, cal := range cals {
		if cal.Name == "Mia" || cal.Name == "Mama" {
			t.Fatalf("task list %q visible with the calendars scope only", cal.Name)
		}
	}
	if len(cals) != 1 {
		t.Fatalf("calendars = %v", cals)
	}
	for _, c := range []struct{ method, path string }{{"PROPFIND", tb.listing}, {"GET", tb.href(tb.swim)}, {"REPORT", tb.listing}} {
		if status, _, _ := only.raw(c.method, c.path, "", map[string]string{"Depth": "1"}); status != http.StatusForbidden {
			t.Errorf("%s %s with calendars only = %d, want 403", c.method, c.path, status)
		}
	}
	// And the other way round: tasks without calendars sees task lists only.
	tasksOnly := tb.c.phone(t, "tasks")
	cals, err := tasksOnly.calendars()
	if err != nil {
		t.Fatal(err)
	}
	for _, cal := range cals {
		if cal.Name == "Family" {
			t.Fatal("the family calendar is visible with the tasks scope only")
		}
	}
	if len(cals) != 2 {
		t.Fatalf("task lists = %v, want Mama and Mia", cals)
	}
	if status, _, _ := tasksOnly.raw("GET", tb.ph.calendarNamed("Family").Path, "", nil); status != http.StatusForbidden {
		t.Errorf("the family calendar with tasks only = %d, want 403", status)
	}
}

func TestChildTokensAreRefusedForTaskLists(t *testing.T) {
	t.Parallel()
	tb := newDavTaskBox(t)
	_ = tb.c.b.db.CreateAccount(store.Account{PersonID: tb.mia.ID, Username: "mia", PasswordHash: "x", TOTPSecret: "S"})
	_, _ = tb.c.b.db.CreateAPIToken(store.APIToken{Name: "kid", Hash: auth.HashToken("hejmo_kid"), PersonID: tb.mia.ID, Scopes: "calendars tasks"})
	if status, _, _ := tb.c.phoneWith(t, "mia", "hejmo_kid").raw("PROPFIND", "/dav/", "", map[string]string{"Depth": "0"}); status != http.StatusForbidden {
		t.Fatalf("child's token = %d, want 403", status)
	}
}

// Acceptance: Given Mia's task completed in the iPhone Reminders app, then
// after the next sync it is done in Hejmo and her stars are credited exactly once.
func TestCompletingATaskInRemindersTicksItAndCreditsStarsOnce(t *testing.T) {
	t.Parallel()
	tb := newDavTaskBox(t)
	done := todoICS(tb.cat.ID, "SUMMARY:Feed the cat", "DTSTART;VALUE=DATE:20261114", "STATUS:COMPLETED", "COMPLETED:20261114T110000Z")
	for i := 0; i < 3; i++ { // clients resend; stars are credited once
		if status := tb.put(tb.href(tb.cat), done, nil); status != http.StatusCreated && status != http.StatusNoContent {
			t.Fatalf("complete #%d = %d", i, status)
		}
	}
	if ok, _ := tb.c.b.db.TaskDone(tb.cat.ID, "2026-11-14"); !ok {
		t.Fatal("the task is not done in Hejmo")
	}
	if got := tb.balance(t); got != 1 {
		t.Fatalf("Mia's stars = %d, want 1 (credited once)", got)
	}
	if st, _ := tb.ph.todos(tb.listing)["Feed the cat"].Props.Text(ical.PropStatus); st != "COMPLETED" {
		t.Errorf("next sync shows STATUS %q", st)
	}
	// Un-completing deducts again, once.
	open := todoICS(tb.cat.ID, "SUMMARY:Feed the cat", "STATUS:NEEDS-ACTION")
	tb.put(tb.href(tb.cat), open, nil)
	tb.put(tb.href(tb.cat), open, nil)
	if ok, _ := tb.c.b.db.TaskDone(tb.cat.ID, "2026-11-14"); ok {
		t.Fatal("still done after un-completing")
	}
	if got := tb.balance(t); got != 0 {
		t.Fatalf("stars after un-completing = %d, want 0", got)
	}
}

// Completing a task in Reminders goes through SetTaskDone, so it ticks all
// subtasks too, and un-completing a done task unticks them again (ticket 7.4).
func TestCompletingATaskInRemindersTicksAndUntickItsSubtasks(t *testing.T) {
	t.Parallel()
	tb := newDavTaskBox(t)
	for _, title := range []string{"Food", "Water"} {
		if _, err := tb.c.b.db.AddSubtask(tb.h.ID, tb.cat.ID, task.Subtask{Title: title}, "2026-11-14", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	allDone := func(want bool) {
		t.Helper()
		subs, err := tb.c.b.db.Subtasks(tb.h.ID, tb.cat.ID, "2026-11-14")
		if err != nil || len(subs) != 2 {
			t.Fatalf("subtasks = %v, %v", subs, err)
		}
		for _, k := range subs {
			if k.Done != want {
				t.Fatalf("subtask %q done = %v, want %v", k.Title, k.Done, want)
			}
		}
	}
	tb.put(tb.href(tb.cat), todoICS(tb.cat.ID, "SUMMARY:Feed the cat", "DTSTART;VALUE=DATE:20261114", "STATUS:COMPLETED", "COMPLETED:20261114T110000Z"), nil)
	allDone(true)
	tb.put(tb.href(tb.cat), todoICS(tb.cat.ID, "SUMMARY:Feed the cat", "STATUS:NEEDS-ACTION"), nil)
	allDone(false)
}

func TestCompletingAOnceTaskCreditsItsStarsOnItsOwnDay(t *testing.T) {
	t.Parallel()
	tb := newDavTaskBox(t)
	tb.put(tb.href(tb.swim), todoICS(tb.swim.ID, "SUMMARY:Pack swim bag", "DUE;VALUE=DATE:20261120", "STATUS:COMPLETED"), nil)
	if ok, _ := tb.c.b.db.TaskDone(tb.swim.ID, "2026-11-20"); !ok {
		t.Fatal("the once task is not done")
	}
	if got := tb.balance(t); got != 2 {
		t.Fatalf("stars = %d, want 2", got)
	}
}

// Acceptance: Given a title changed in the Reminders app, then Hejmo shows the new title.
func TestChangedTitleNotesAndDueDateReachTheBoxAndNothingElseDoes(t *testing.T) {
	t.Parallel()
	tb := newDavTaskBox(t)
	body := todoICS(tb.swim.ID, "SUMMARY:Pack the swimming bag", "DESCRIPTION:Towel and goggles", "DUE;VALUE=DATE:20261125", "STATUS:NEEDS-ACTION",
		"PRIORITY:1", "RRULE:FREQ=DAILY", "X-APPLE-SORT-ORDER:5")
	if status := tb.put(tb.href(tb.swim), body, nil); status != http.StatusCreated && status != http.StatusNoContent {
		t.Fatalf("save = %d", status)
	}
	got, err := tb.c.b.db.Task(tb.h.ID, tb.swim.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Pack the swimming bag" || got.Notes != "Towel and goggles" || got.Date != "2026-11-25" {
		t.Fatalf("task = %+v", got)
	}
	if got.Repeat != task.Once || got.Stars != 2 || got.PersonID != tb.mia.ID {
		t.Fatalf("a client changed what it cannot: %+v", got)
	}
	// The title shows on the box's web pages too.
	if page := tb.c.b.get("/tasks"); !strings.Contains(page.body, "Pack the swimming bag") {
		t.Error("the new title is not on the tasks page")
	}
}

func TestARepeatingTasksDayCannotBeChangedFromOutside(t *testing.T) {
	t.Parallel()
	tb := newDavTaskBox(t)
	tb.put(tb.href(tb.cat), todoICS(tb.cat.ID, "SUMMARY:Feed the cats", "DUE;VALUE=DATE:20271224"), nil)
	got, _ := tb.c.b.db.Task(tb.h.ID, tb.cat.ID)
	if got.Title != "Feed the cats" || got.Date != "2026-11-01" || got.Repeat != task.Daily {
		t.Fatalf("task = %+v", got)
	}
}

// Acceptance: Given a task created or deleted in the Reminders app, then the
// box refuses it and the list is restored at the next sync.
func TestCreatingAndDeletingTasksFromOutsideAreRefused(t *testing.T) {
	t.Parallel()
	tb := newDavTaskBox(t)
	if status := tb.put(tb.listing+"new-task.ics", todoICS("new-task", "SUMMARY:Buy milk"), map[string]string{"If-None-Match": "*"}); status != http.StatusForbidden {
		t.Fatalf("create = %d, want 403", status)
	}
	all, _ := tb.c.b.db.Tasks(tb.h.ID)
	if len(all) != 3 {
		t.Fatalf("a refused create changed the box: %d tasks", len(all))
	}
	for _, k := range []task.Task{tb.swim, tb.cat} {
		if status, _, _ := tb.ph.raw("DELETE", tb.href(k), "", nil); status != http.StatusForbidden {
			t.Fatalf("delete %s = %d, want 403", k.Title, status)
		}
	}
	if todos := tb.ph.todos(tb.listing); len(todos) != 2 {
		t.Fatalf("the list is not as before: %v", todos)
	}
	if status, _, _ := tb.ph.raw("GET", tb.listing+"new-task.ics", "", nil); status != http.StatusNotFound {
		t.Fatalf("the refused task exists: %d", status)
	}
}

func TestTaskWritesNeedTheWriteScopeAndAnInvalidSaveChangesNothing(t *testing.T) {
	t.Parallel()
	tb := newDavTaskBox(t)
	ro := tb.c.phone(t, "tasks:read")
	if status, _, _ := ro.raw("PUT", tb.href(tb.swim), todoICS(tb.swim.ID, "SUMMARY:Hacked"), map[string]string{"Content-Type": icsType}); status != http.StatusForbidden {
		t.Fatalf("PUT with tasks:read = %d, want 403", status)
	}
	for name, body := range map[string]string{
		"no title":    todoICS(tb.swim.ID, "DESCRIPTION:x"),
		"an event":    eventICS(tb.swim.ID, "Not a task", "20261120T090000Z", "20261120T100000Z"),
		"not a todo":  "garbage",
		"long title":  todoICS(tb.swim.ID, "SUMMARY:"+strings.Repeat("x", 121)),
		"bad due day": todoICS(tb.swim.ID, "SUMMARY:ok", "DUE;VALUE=DATE:2026"),
	} {
		if status := tb.put(tb.href(tb.swim), body, nil); status != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", name, status)
		}
	}
	if got, _ := tb.c.b.db.Task(tb.h.ID, tb.swim.ID); got.Title != "Pack swim bag" {
		t.Fatalf("an invalid save changed the task: %+v", got)
	}
}

func TestStaleTaskWritesAreRefusedLikeEvents(t *testing.T) {
	t.Parallel()
	tb := newDavTaskBox(t)
	_, hdr, _ := tb.ph.raw("GET", tb.href(tb.swim), "", nil)
	first := hdr.Get("ETag")
	if first == "" {
		t.Fatal("no ETag")
	}
	if status := tb.put(tb.href(tb.swim), todoICS(tb.swim.ID, "SUMMARY:One", "DUE;VALUE=DATE:20261120"), map[string]string{"If-Match": first}); status >= 300 {
		t.Fatalf("current ETag = %d", status)
	}
	if status := tb.put(tb.href(tb.swim), todoICS(tb.swim.ID, "SUMMARY:Two", "DUE;VALUE=DATE:20261120"), map[string]string{"If-Match": first}); status != http.StatusPreconditionFailed {
		t.Fatalf("stale ETag = %d, want 412", status)
	}
}

// Someone else's list or a task of another list is not reachable by address.
func TestATaskIsOnlyReachableThroughItsPersonsList(t *testing.T) {
	t.Parallel()
	tb := newDavTaskBox(t)
	mama := strings.Replace(tb.listing, tb.mia.ID, tb.mama.ID, 1)
	if status, _, _ := tb.ph.raw("GET", mama+tb.swim.ID+".ics", "", nil); status != http.StatusNotFound {
		t.Fatalf("Mia's task through Mama's list = %d, want 404", status)
	}
	if status := tb.put(mama+tb.swim.ID+".ics", todoICS(tb.swim.ID, "SUMMARY:Moved"), nil); status != http.StatusForbidden {
		t.Fatalf("PUT through Mama's list = %d, want 403", status)
	}
}

// A client completing (or moving) an overdue once task gets its saved state
// back, even though the finished task falls out of the listing window.
func TestCompletingAnOverdueOnceTaskSucceedsAndRetriesAreHarmless(t *testing.T) {
	t.Parallel()
	tb := newDavTaskBox(t)
	old, err := tb.c.b.db.AddTask(tb.h.ID, task.Task{PersonID: tb.mia.ID, Title: "Old chore", Repeat: task.Once, Date: "2026-11-01", Stars: 3})
	if err != nil {
		t.Fatal(err)
	}
	body := todoICS(old.ID, "SUMMARY:Old chore", "DUE;VALUE=DATE:20261101", "STATUS:COMPLETED")
	for i := 0; i < 2; i++ {
		if status := tb.put(tb.href(old), body, nil); status != http.StatusCreated && status != http.StatusNoContent {
			t.Fatalf("complete #%d = %d", i, status)
		}
	}
	if got := tb.balance(t); got != 3 {
		t.Fatalf("stars = %d, want 3", got)
	}
	// Moving a done once task's due date far back also answers normally.
	moved := todoICS(tb.swim.ID, "SUMMARY:Pack swim bag", "DUE;VALUE=DATE:20261101", "STATUS:COMPLETED")
	if status := tb.put(tb.href(tb.swim), moved, nil); status != http.StatusCreated && status != http.StatusNoContent {
		t.Fatalf("move = %d", status)
	}
}
