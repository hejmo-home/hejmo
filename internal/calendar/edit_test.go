package calendar_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
)

// applyChange mimics the store: it applies a Change to an in-memory event set.
func applyChange(events []calendar.Event, ch calendar.Change) []calendar.Event {
	byID := map[string]calendar.Event{}
	var order []string
	for _, e := range events {
		byID[e.ID] = e
		order = append(order, e.ID)
	}
	for _, id := range ch.Delete {
		delete(byID, id)
	}
	for _, e := range ch.Update {
		byID[e.ID] = e
	}
	for _, e := range ch.Create {
		byID[e.ID] = e
		order = append(order, e.ID)
	}
	var out []calendar.Event
	for _, id := range order {
		if e, ok := byID[id]; ok {
			out = append(out, e)
			delete(byID, id)
		}
	}
	return out
}

var idCounter int

func newEditor() calendar.Editor {
	return calendar.Editor{NewID: func() string { idCounter++; return fmt.Sprintf("new%d", idCounter) }}
}

func str(s string) *string      { return &s }
func tm(t time.Time) *time.Time { return &t }

func football() []calendar.Event {
	return []calendar.Event{{ID: "m", Title: "Football", Start: at(2026, 11, 2, 16, 0), End: at(2026, 11, 2, 17, 0), RRule: "FREQ=WEEKLY"}}
}

func titledStarts(t *testing.T, events []calendar.Event) []string {
	t.Helper()
	occs, err := calendar.Expand(events, day(2026, 11, 1), day(2026, 12, 1))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, o := range occs {
		out = append(out, o.Start.In(berlin).Format("02.01. 15:04 ")+o.Title)
	}
	return out
}

// Acceptance (Calendar and sync): Given a series, when "this and following"
// is changed, then earlier occurrences stay unchanged.
func TestEditThisAndFollowingLeavesEarlierOccurrences(t *testing.T) {
	events := football()
	ch, err := newEditor().Edit(events, at(2026, 11, 16, 16, 0), calendar.ScopeFollowing,
		calendar.Patch{Title: str("Football (new coach)"), Start: tm(at(2026, 11, 16, 17, 0)), End: tm(at(2026, 11, 16, 18, 0))})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, titledStarts(t, applyChange(events, ch)), []string{
		"02.11. 16:00 Football", "09.11. 16:00 Football",
		"16.11. 17:00 Football (new coach)", "23.11. 17:00 Football (new coach)", "30.11. 17:00 Football (new coach)",
	})
}

func TestEditOnlyThisOccurrence(t *testing.T) {
	events := football()
	ch, err := newEditor().Edit(events, at(2026, 11, 9, 16, 0), calendar.ScopeThis,
		calendar.Patch{Title: str("Football match"), Start: tm(at(2026, 11, 9, 18, 0)), End: tm(at(2026, 11, 9, 20, 0))})
	if err != nil {
		t.Fatal(err)
	}
	events = applyChange(events, ch)
	equal(t, titledStarts(t, events), []string{
		"02.11. 16:00 Football", "09.11. 18:00 Football match", "16.11. 16:00 Football", "23.11. 16:00 Football", "30.11. 16:00 Football",
	})

	// Editing the same occurrence again updates the existing change, no duplicate.
	ch, _ = newEditor().Edit(events, at(2026, 11, 9, 16, 0), calendar.ScopeThis, calendar.Patch{Title: str("Cup match")})
	if len(ch.Create) != 0 || len(ch.Update) != 1 {
		t.Fatalf("second edit of same occurrence: %+v", ch)
	}
	equal(t, titledStarts(t, applyChange(events, ch))[1:2], []string{"09.11. 18:00 Cup match"})
}

func TestEditAllOccurrences(t *testing.T) {
	events := football()
	ch, err := newEditor().Edit(events, at(2026, 11, 16, 16, 0), calendar.ScopeAll,
		calendar.Patch{Title: str("Soccer"), Start: tm(at(2026, 11, 16, 16, 30)), End: tm(at(2026, 11, 16, 17, 30))})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, titledStarts(t, applyChange(events, ch)), []string{
		"02.11. 16:30 Soccer", "09.11. 16:30 Soccer", "16.11. 16:30 Soccer", "23.11. 16:30 Soccer", "30.11. 16:30 Soccer",
	})
}

func TestFollowingSplitKeepsTotalCount(t *testing.T) {
	events := []calendar.Event{{ID: "m", Title: "Course", Start: at(2026, 11, 2, 19, 0), End: at(2026, 11, 2, 20, 0), RRule: "FREQ=WEEKLY;COUNT=4"}}
	ch, err := newEditor().Edit(events, at(2026, 11, 16, 19, 0), calendar.ScopeFollowing, calendar.Patch{Title: str("Course, room 2")})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, titledStarts(t, applyChange(events, ch)), []string{
		"02.11. 19:00 Course", "09.11. 19:00 Course", "16.11. 19:00 Course, room 2", "23.11. 19:00 Course, room 2",
	})
}

func TestFollowingSplitKeepsLaterChangedOccurrences(t *testing.T) {
	events := football()
	ch, _ := newEditor().Edit(events, at(2026, 11, 23, 16, 0), calendar.ScopeThis, calendar.Patch{Title: str("Tournament")})
	events = applyChange(events, ch)
	ch, err := newEditor().Edit(events, at(2026, 11, 16, 16, 0), calendar.ScopeFollowing, calendar.Patch{Location: str("New pitch")})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, titledStarts(t, applyChange(events, ch)), []string{
		"02.11. 16:00 Football", "09.11. 16:00 Football", "16.11. 16:00 Football", "23.11. 16:00 Tournament", "30.11. 16:00 Football",
	})
}

func TestDeleteScopes(t *testing.T) {
	cases := map[calendar.Scope][]string{
		calendar.ScopeThis:      {"02.11. 16:00 Football", "09.11. 16:00 Football", "23.11. 16:00 Football", "30.11. 16:00 Football"},
		calendar.ScopeFollowing: {"02.11. 16:00 Football", "09.11. 16:00 Football"},
		calendar.ScopeAll:       nil,
	}
	for scope, want := range cases {
		t.Run(string(scope), func(t *testing.T) {
			events := football()
			ch, err := newEditor().Delete(events, at(2026, 11, 16, 16, 0), scope)
			if err != nil {
				t.Fatal(err)
			}
			equal(t, titledStarts(t, applyChange(events, ch)), want)
		})
	}
}

func TestDeleteFollowingFromFirstOccurrenceDeletesSeries(t *testing.T) {
	events := football()
	ch, _ := newEditor().Delete(events, at(2026, 11, 2, 16, 0), calendar.ScopeFollowing)
	if len(ch.Delete) != 1 || ch.Delete[0] != "m" {
		t.Fatalf("change = %+v, want master deleted", ch)
	}
}

func TestEditSingleEvent(t *testing.T) {
	events := []calendar.Event{{ID: "d", Title: "Dentist", Start: at(2026, 11, 18, 10, 0), End: at(2026, 11, 18, 11, 0)}}
	ch, err := newEditor().Edit(events, at(2026, 11, 18, 10, 0), calendar.ScopeAll, calendar.Patch{Start: tm(at(2026, 11, 19, 10, 0)), End: tm(at(2026, 11, 19, 11, 0))})
	if err != nil || len(ch.Update) != 1 || !ch.Update[0].Start.Equal(at(2026, 11, 19, 10, 0)) {
		t.Fatalf("edit single = %+v, %v", ch, err)
	}
}

func TestEditUnknownOccurrence(t *testing.T) {
	if _, err := newEditor().Edit(football(), at(2026, 11, 17, 16, 0), calendar.ScopeThis, calendar.Patch{Title: str("x")}); err == nil {
		t.Fatal("editing a non-existent occurrence succeeded")
	}
}
