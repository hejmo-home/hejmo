package familyview_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"

	"github.com/hejmo-home/hejmo/internal/familyview"
)

func morning(day int) time.Time { return at(day, 0, 0) }

// ADR 0015: the night page names the coming morning's date, the household's
// first timed event of that day and its waste collection.
func TestNightShowsTheDateAndTheFirstTimedEventOfTheMorning(t *testing.T) {
	v := familyview.BuildNight(familyview.NightInput{
		Morning: morning(15),
		Occurrences: []calendar.Occurrence{
			timed("Lunch", at(15, 12, 0), at(15, 13, 0)),
			timed("School run", at(15, 7, 45), at(15, 8, 15), "mia"),
			timed("Today's late thing", at(14, 23, 0), at(14, 23, 30)),
			timed("Monday", at(16, 6, 0), at(16, 7, 0)),
		},
		Waste: []string{"Paper"},
	})
	if v.Weekday != time.Sunday || v.Date.Day() != 15 {
		t.Fatalf("date = %v %v", v.Weekday, v.Date)
	}
	if v.First == nil || v.First.Title != "School run" || v.First.Time != "07:45–08:15" {
		t.Fatalf("first = %+v", v.First)
	}
	if len(v.Waste) != 1 || v.Waste[0] != "Paper" {
		t.Fatalf("waste = %v", v.Waste)
	}
}

func TestNightIgnoresAllDayEventsAndHasNoEventWhenNoneIsTimed(t *testing.T) {
	allDay := timed("Birthday", at(15, 0, 0), at(16, 0, 0))
	allDay.AllDay = true
	v := familyview.BuildNight(familyview.NightInput{Morning: morning(15), Occurrences: []calendar.Occurrence{allDay}})
	if v.First != nil || len(v.Waste) != 0 {
		t.Fatalf("view = %+v", v)
	}
}
