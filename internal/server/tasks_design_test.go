package server_test

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/task"
)

// Design pass 6.6 (docs/design/preview-37.html): the progress block of a column
// says it in words, text before the bar, and the whole block opens the
// personal view.
func TestFamilyProgressBlockIsALinkWithWordsBeforeTheBar(t *testing.T) {
	t.Parallel()
	cases := []struct {
		lang        string
		done, total int
		text        string
		allDone     bool
	}{
		{"en", 3, 5, "3 of 5 tasks done", false},
		{"en", 0, 1, "0 of 1 task done", false},
		{"en", 2, 2, "All tasks done", true},
		{"en", 1, 1, "All tasks done", true},
		{"de", 3, 5, "3 von 5 Aufgaben erledigt", false},
		{"de", 0, 1, "0 von 1 Aufgabe erledigt", false},
		{"de", 2, 2, "Alle Aufgaben erledigt", true},
	}
	for _, c := range cases {
		v := familyview.Build(familyview.Input{Lang: c.lang, Region: "DE-BY", People: []familyview.Person{{ID: "mia", Name: "Mia", Slot: 4, Shape: "square-empty"}}})
		v.Columns[0].Progress = &familyview.Progress{Done: c.done, Total: c.total}
		out, err := server.RenderFamilyView(c.lang, v)
		if err != nil {
			t.Fatal(err)
		}
		d := regexp.MustCompile(`data-progress="(\d+/\d+)"`).FindStringSubmatch(out)
		if d == nil || d[1] != strings.Join([]string{strconv.Itoa(c.done), strconv.Itoa(c.total)}, "/") {
			t.Errorf("%v: data-progress = %v", c, d)
		}
		if !strings.Contains(out, `<a class="pbar" href="/display/person/mia" data-progress=`) {
			t.Errorf("%v: the block is not a link to the personal view:\n%s", c, out)
		}
		i, j := strings.Index(out, `<span class="count">`), strings.Index(out, `<span class="progress" role="img" aria-label="`+strconv.Itoa(c.done)+"/"+strconv.Itoa(c.total)+`">`)
		if i < 0 || j < i || !strings.Contains(out[i:j], c.text+"</span>") {
			t.Errorf("%v: the words %q do not come before the bar:\n%s", c, c.text, out)
		}
		if has := strings.Contains(out, " data-all-done"); has != c.allDone {
			t.Errorf("%v: data-all-done = %v", c, has)
		}
		if has := strings.Contains(out[i:j], "<svg"); has != c.allDone {
			t.Errorf("%v: tick icon = %v", c, has)
		}
		if strings.Contains(out, ">"+strconv.Itoa(c.done)+"/"+strconv.Itoa(c.total)+"<") {
			t.Errorf("%v: the visible text is still done/total", c)
		}
	}
}

// The personal view: tasks first, grouped by part of the day, with the
// progress in words on top.
func TestPersonalViewTasksTile(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	mk := func(title, part string) task.Task {
		tk, err := f.db.AddTask(f.householdID, task.Task{PersonID: f.people["Mia"].ID, Title: title, Emoji: "🪥", PartOfDay: task.PartOfDay(part), Repeat: task.Daily, Date: "2026-11-14"})
		if err != nil {
			t.Fatal(err)
		}
		return tk
	}
	mk("Piano", "evening")
	morning := mk("Teeth", "morning")
	mk("Cat", "")
	if err := f.db.SetTaskDone(f.householdID, morning.ID, "2026-11-14", true, "", f.at(14, 8, 0)); err != nil {
		t.Fatal(err)
	}
	body := f.personalPage(t, "en", "Mia", 14, 9).body
	for _, want := range []string{
		`class="pv" data-person="`, `<aside class="pv-tasks"`, `<div class="tasks-head"><div class="lab">Tasks today</div><div class="count">1 of 3 tasks done</div><div class="progress" role="img" aria-label="1/3">`,
		`<div class="list">`, `</svg>Morning</div>`, `<div class="lab">Any time today</div>`, `</svg>Evening</div>`, `class="icon part"`,
		`<span class="box" aria-hidden="true"><svg`, `<span class="emoji" aria-hidden="true">🪥</span><span class="title">Teeth</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("personal view lacks %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, "data-no-tasks") {
		t.Error("data-no-tasks with tasks")
	}
	if strings.Contains(body, `class="title">🪥`) {
		t.Error("the emoji is in the title and would be struck through")
	}
	if a, m := strings.Index(body, `class="pv-tasks"`), strings.Index(body, `class="pv-days"`); a < 0 || m < a {
		t.Error("the tasks tile does not come before the days")
	}
	if i, j, k := strings.Index(body, ">Morning<"), strings.Index(body, ">Any time today<"), strings.Index(body, ">Evening<"); !(i < j && j < k) {
		t.Error("parts not in the order morning, any time, evening")
	}
	de := f.personalPage(t, "de", "Mia", 14, 9).body
	for _, want := range []string{"1 von 3 Aufgaben erledigt", ">Morgens<", ">Irgendwann heute<", ">Abends<", `data-tick-error="Nicht gespeichert: Die Box ist nicht erreichbar. Tippe noch einmal."`} {
		if !strings.Contains(de, want) {
			t.Errorf("German personal view lacks %s", want)
		}
	}
	if !strings.Contains(body, `data-tick-error="Not saved: the box cannot be reached. Tap again."`) {
		t.Error("no tick error text for the script")
	}
}

// One part of the day shows no heading; all done shows the success line.
func TestPersonalViewSinglePartAndAllDone(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	a := f.task(t, "Mia", "Homework", task.Daily)
	b := f.task(t, "Mia", "Cat", task.Daily)
	before := f.personalPage(t, "en", "Mia", 14, 9).body
	if strings.Contains(before, `<section class="part"><div class="lab">`) || strings.Contains(before, `class="msg ok"`) {
		t.Errorf("single part shows a heading or success early:\n%s", before)
	}
	for _, tk := range []task.Task{a, b} {
		if err := f.db.SetTaskDone(f.householdID, tk.ID, "2026-11-14", true, "", f.at(14, 8, 0)); err != nil {
			t.Fatal(err)
		}
	}
	after := f.personalPage(t, "en", "Mia", 14, 9).body
	if !strings.Contains(after, `<p class="msg ok" role="status"><svg`) || !strings.Contains(after, "All 2 tasks done for today</p>") || !strings.Contains(after, `aria-label="2/2"`) {
		t.Errorf("all done:\n%s", after)
	}
	if strings.Contains(after, `<div class="count">`) {
		t.Error("count words shown next to the success line")
	}
	de := f.personalPage(t, "de", "Mia", 14, 9).body
	if !strings.Contains(de, "Alle 2 Aufgaben für heute erledigt</p>") {
		t.Error("German success line missing")
	}
}

func TestPersonalViewWithoutTasks(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	body := f.personalPage(t, "en", "Mia", 14, 9).body
	for _, want := range []string{`<div class="pv-body" data-no-tasks>`, `<div class="tasks-head"><div class="lab">Tasks today</div></div>`, `<p class="none">No tasks today</p>`} {
		if !strings.Contains(body, want) {
			t.Errorf("empty state lacks %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, `class="progress"`) {
		t.Error("progress bar without tasks")
	}
}

// A failed tick shows an error under the task and keeps the old state
// (display.js, nothing to render on the server side).
// Smoke check only: it looks for the markers in display.js, it does not run the script.
func TestDisplayScriptReportsFailedTicks(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("../../web/static/display.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"data-tick-error", `"ferr"`, `"alert"`, `.big-check`, `"submit"`, "r.redirected"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("display.js lacks %s", want)
		}
	}
}
