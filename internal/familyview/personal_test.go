package familyview_test

import (
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/familyview"
)

func personal(now func() (int, int, int), occs []calendar.Occurrence, tasks ...familyview.Task) familyview.PersonalView {
	d, h, m := now()
	mia := family()[3]
	return familyview.BuildPersonal(familyview.PersonalInput{
		Now: at(d, h, m), Person: mia, Calendars: map[string]calendar.Audience{"cal": {}},
		Occurrences: occs, Tasks: tasks,
	})
}

func today(d, h, m int) func() (int, int, int) { return func() (int, int, int) { return d, h, m } }

func dayTitles(d familyview.PersonalDay) string {
	var out []string
	for _, e := range d.Events {
		out = append(out, e.Title)
	}
	return strings.Join(out, "|")
}

// 5.6 (ADR 0015): the personal view covers today and the next 6 days.
func TestPersonalViewCoversTodayAndSixMoreDays(t *testing.T) {
	v := personal(today(14, 9, 0), nil)
	if len(v.Days) != 7 {
		t.Fatalf("days = %d, want 7", len(v.Days))
	}
	if !v.Days[0].Today || v.Days[1].Today {
		t.Error("only the first day is today")
	}
	if got := v.Days[6].Date; got.Day() != 20 {
		t.Errorf("last day = %v, want 20/11", got)
	}
	if v.Person.Name != "Mia" {
		t.Errorf("person = %q", v.Person.Name)
	}
}

// 5.6: the person's events land on their days, in order; events of others and
// events beyond the 7 days stay out.
func TestPersonalViewShowsOnlyThePersonsEventsPerDay(t *testing.T) {
	v := personal(today(14, 9, 0), []calendar.Occurrence{
		timed("Swimming", at(14, 16, 0), at(14, 17, 0), "mia"),
		timed("Dentist", at(14, 9, 0), at(14, 10, 0), "mama"),
		timed("Piano", at(17, 15, 0), at(17, 16, 0), "mia"),
		timed("School play", at(17, 10, 30), at(17, 11, 30), "mia"),
		timed("Family lunch", at(15, 12, 0), at(15, 13, 0)),
		timed("Too far", at(21, 10, 0), at(21, 11, 0), "mia"),
	})
	if got := dayTitles(v.Days[0]); got != "Swimming" {
		t.Errorf("today = %q", got)
	}
	if got := dayTitles(v.Days[1]); got != "Family lunch" {
		t.Errorf("tomorrow = %q (whole-household events belong to everybody)", got)
	}
	if got := dayTitles(v.Days[3]); got != "School play|Piano" {
		t.Errorf("day 4 = %q", got)
	}
	if v.Days[0].Events[0].Time != "16:00–17:00" {
		t.Errorf("time = %q", v.Days[0].Events[0].Time)
	}
	for i := 4; i < 7; i++ {
		if len(v.Days[i].Events) != 0 {
			t.Errorf("day %d should be empty", i+1)
		}
	}
}

// 5.6: all-day events come first and a multi-day event shows on each day it covers.
func TestPersonalViewListsAllDayFirstAndMultiDayOnEachDay(t *testing.T) {
	v := personal(today(14, 9, 0), []calendar.Occurrence{
		timed("Football", at(14, 8, 0), at(14, 9, 0), "mia"),
		{EventID: "trip", CalendarID: "cal", Title: "Class trip", AllDay: true, Start: at(14, 0, 0), End: at(16, 0, 0), People: []string{"mia"}},
		{EventID: "camp", CalendarID: "cal", Title: "Camp", Start: at(15, 18, 0), End: at(17, 10, 0), People: []string{"mia"}},
	})
	if got := dayTitles(v.Days[0]); got != "Class trip|Football" {
		t.Errorf("today = %q", got)
	}
	if !v.Days[0].Events[0].AllDay || v.Days[0].Events[0].Time != "" {
		t.Errorf("all-day event = %+v", v.Days[0].Events[0])
	}
	if got := dayTitles(v.Days[1]); got != "Class trip|Camp" {
		t.Errorf("day 2 = %q", got)
	}
	if got := dayTitles(v.Days[3]); got != "Camp" {
		t.Errorf("day 4 = %q", got)
	}
}

// 5.6 (ADR 0015): tasks for today come with their state, morning before any time before evening.
func TestPersonalViewListsTodaysTasksMorningFirst(t *testing.T) {
	v := personal(today(14, 9, 0), nil,
		familyview.Task{ID: "3", Title: "Pack bag", Part: "evening"},
		familyview.Task{ID: "2", Title: "Homework", Done: true},
		familyview.Task{ID: "1", Title: "Brush teeth", Emoji: "🪥", Part: "morning"})
	var ids []string
	for _, tk := range v.Tasks {
		ids = append(ids, tk.ID)
	}
	if strings.Join(ids, ",") != "1,2,3" {
		t.Errorf("order = %v", ids)
	}
	if !v.Tasks[1].Done || v.Tasks[0].Done {
		t.Error("done state lost")
	}
}
