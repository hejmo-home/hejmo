package graph_test

import (
	"testing"
	"time"

	"github.com/teambition/rrule-go"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/graph"
)

var berlin, _ = time.LoadLocation("Europe/Berlin")

func TestRecurrenceBecomesAnRRule(t *testing.T) {
	cases := []struct {
		name string
		rec  graph.Recurrence
		want string
	}{
		{"daily", graph.Recurrence{Pattern: graph.Pattern{Type: "daily", Interval: 2}, Range: graph.Range{Type: "noEnd"}}, "FREQ=DAILY;INTERVAL=2"},
		{"weekly days", graph.Recurrence{Pattern: graph.Pattern{Type: "weekly", Interval: 1, DaysOfWeek: []string{"monday", "wednesday"}, FirstDayOfWeek: "monday"}, Range: graph.Range{Type: "numbered", NumberOfOccurrences: 10}}, "FREQ=WEEKLY;INTERVAL=1;BYDAY=MO,WE;WKST=MO;COUNT=10"},
		{"monthly day", graph.Recurrence{Pattern: graph.Pattern{Type: "absoluteMonthly", Interval: 1, DayOfMonth: 15}, Range: graph.Range{Type: "noEnd"}}, "FREQ=MONTHLY;INTERVAL=1;BYMONTHDAY=15"},
		{"monthly second tuesday", graph.Recurrence{Pattern: graph.Pattern{Type: "relativeMonthly", Interval: 1, DaysOfWeek: []string{"tuesday"}, Index: "second"}, Range: graph.Range{Type: "noEnd"}}, "FREQ=MONTHLY;INTERVAL=1;BYDAY=2TU"},
		{"monthly last friday", graph.Recurrence{Pattern: graph.Pattern{Type: "relativeMonthly", Interval: 1, DaysOfWeek: []string{"friday"}, Index: "last"}, Range: graph.Range{Type: "noEnd"}}, "FREQ=MONTHLY;INTERVAL=1;BYDAY=-1FR"},
		{"yearly", graph.Recurrence{Pattern: graph.Pattern{Type: "absoluteYearly", Interval: 1, DayOfMonth: 24, Month: 12}, Range: graph.Range{Type: "noEnd"}}, "FREQ=YEARLY;INTERVAL=1;BYMONTH=12;BYMONTHDAY=24"},
		{"until", graph.Recurrence{Pattern: graph.Pattern{Type: "daily", Interval: 1}, Range: graph.Range{Type: "endDate", EndDate: "2027-03-01"}}, "FREQ=DAILY;INTERVAL=1;UNTIL=20270301T225959Z"},
	}
	for _, c := range cases {
		got, err := graph.RRuleOf(c.rec, berlin)
		if err != nil || got != c.want {
			t.Errorf("%s: RRuleOf = %q, %v; want %q", c.name, got, err, c.want)
		}
		if err := calendar.ValidateRRule(got); err != nil {
			t.Errorf("%s: %q is not a valid RRULE: %v", c.name, got, err)
		}
	}
}

func TestRRuleBecomesARecurrence(t *testing.T) {
	for _, rule := range []string{"FREQ=DAILY;INTERVAL=2", "FREQ=WEEKLY;INTERVAL=1;BYDAY=MO,WE;COUNT=10", "FREQ=MONTHLY;INTERVAL=1;BYMONTHDAY=15",
		"FREQ=MONTHLY;INTERVAL=1;BYDAY=2TU", "FREQ=YEARLY;INTERVAL=1;BYMONTH=12;BYMONTHDAY=24", "FREQ=WEEKLY;BYDAY=MO;UNTIL=20270301T225959Z"} {
		start := time.Date(2026, 11, 16, 9, 0, 0, 0, berlin)
		rec, err := graph.RecurrenceOf(rule, start)
		if err != nil {
			t.Errorf("%s: %v", rule, err)
			continue
		}
		back, err := graph.RRuleOf(*rec, berlin)
		if err != nil {
			t.Errorf("%s: back: %v", rule, err)
			continue
		}
		a, b := canon(t, rule), canon(t, back)
		if a != b {
			t.Errorf("%s -> %+v -> %s", rule, rec, back)
		}
	}
	if _, err := graph.RecurrenceOf("FREQ=HOURLY", time.Now()); err == nil {
		t.Error("an hourly rule has no Outlook form, want an error")
	}
}

func TestSingleEventKeepsItsZoneAndAllDay(t *testing.T) {
	ev := graph.Event{ID: "a1", Subject: "Elternabend", IsAllDay: false,
		Start: &graph.DateTimeZone{DateTime: "2026-11-19T19:30:00.0000000", TimeZone: "W. Europe Standard Time"},
		End:   &graph.DateTimeZone{DateTime: "2026-11-19T21:00:00.0000000", TimeZone: "W. Europe Standard Time"},
		Body:  &graph.Body{Content: "Aula"}, Location: &graph.Location{DisplayName: "Schule"}}
	got, err := graph.ToEvent(ev, berlin)
	if err != nil {
		t.Fatal(err)
	}
	if got.Event.Title != "Elternabend" || got.Event.Location != "Schule" || got.Event.Description != "Aula" || got.Key != "a1" {
		t.Fatalf("event = %+v", got)
	}
	if got.Event.Start.Format(time.RFC3339) != "2026-11-19T19:30:00+01:00" || got.Event.Start.Location().String() != "Europe/Berlin" {
		t.Errorf("start = %v", got.Event.Start)
	}
	day := graph.Event{ID: "a2", Subject: "Ferien", IsAllDay: true,
		Start: &graph.DateTimeZone{DateTime: "2026-12-24T00:00:00.0000000", TimeZone: "UTC"},
		End:   &graph.DateTimeZone{DateTime: "2026-12-25T00:00:00.0000000", TimeZone: "UTC"}}
	d, err := graph.ToEvent(day, berlin)
	if err != nil || !d.Event.AllDay || d.Event.End.Sub(d.Event.Start) != 24*time.Hour {
		t.Fatalf("all-day = %+v, %v", d.Event, err)
	}
}

func TestAnEventWithAttendeesIsAnInvitation(t *testing.T) {
	ev := graph.Event{ID: "a3", Subject: "Elternabend",
		Start:     &graph.DateTimeZone{DateTime: "2026-11-19T19:30:00", TimeZone: "UTC"},
		End:       &graph.DateTimeZone{DateTime: "2026-11-19T20:30:00", TimeZone: "UTC"},
		Attendees: []graph.Attendee{{}}, Organizer: &graph.Recipient{EmailAddress: graph.Email{Name: "Huber, Annemarie", Address: "h@example.org"}},
		ResponseStatus: &graph.ResponseStatus{Response: "accepted"}, WebLink: "https://outlook.live.com/owa/?itemid=a3"}
	got, err := graph.ToEvent(ev, berlin)
	if err != nil || got.Event.Invitation == nil {
		t.Fatalf("no invitation: %+v, %v", got.Event, err)
	}
	if i := got.Event.Invitation; i.Organizer != "Huber, Annemarie" || i.Response != "accepted" || i.WebLink != ev.WebLink {
		t.Errorf("invitation = %+v", i)
	}
	ev.Attendees = nil
	if got, _ := graph.ToEvent(ev, berlin); got.Event.Invitation != nil {
		t.Errorf("an event without attendees is not an invitation")
	}
}

func TestSeriesIsOneObjectWithOverridesAndRemovedOccurrences(t *testing.T) {
	master := graph.Event{ID: "m1", Type: "seriesMaster", Subject: "Fußball",
		Start:      &graph.DateTimeZone{DateTime: "2026-11-02T15:00:00", TimeZone: "W. Europe Standard Time"},
		End:        &graph.DateTimeZone{DateTime: "2026-11-02T16:00:00", TimeZone: "W. Europe Standard Time"},
		Recurrence: &graph.Recurrence{Pattern: graph.Pattern{Type: "weekly", Interval: 1, DaysOfWeek: []string{"monday"}, FirstDayOfWeek: "monday"}, Range: graph.Range{Type: "noEnd"}}}
	moved := graph.Event{ID: "i2", SeriesMasterID: "m1", Type: "exception", Subject: "Fußball (Halle)", OriginalStart: "2026-11-16T14:00:00Z",
		Start: &graph.DateTimeZone{DateTime: "2026-11-16T16:00:00", TimeZone: "W. Europe Standard Time"},
		End:   &graph.DateTimeZone{DateTime: "2026-11-16T17:00:00", TimeZone: "W. Europe Standard Time"}}
	plain := func(day string, id string) graph.Event {
		return graph.Event{ID: id, SeriesMasterID: "m1", Type: "occurrence", Subject: "Fußball",
			Start: &graph.DateTimeZone{DateTime: day + "T15:00:00", TimeZone: "W. Europe Standard Time"},
			End:   &graph.DateTimeZone{DateTime: day + "T16:00:00", TimeZone: "W. Europe Standard Time"}}
	}
	// 2 Nov, 16 Nov (moved), 30 Nov present; 9 Nov was deleted from the series.
	instances := []graph.Event{plain("2026-11-02", "i1"), moved, plain("2026-11-23", "i3"), plain("2026-11-30", "i4")}
	from, to := time.Date(2026, 11, 1, 0, 0, 0, 0, berlin), time.Date(2026, 12, 1, 0, 0, 0, 0, berlin)
	items, err := graph.SeriesItems(master, instances, berlin, from, to)
	if err != nil {
		t.Fatal(err)
	}
	var m *graph.Item
	var overrides []graph.Item
	for i := range items {
		if items[i].SeriesKey == "" {
			m = &items[i]
		} else {
			overrides = append(overrides, items[i])
		}
	}
	if m == nil || m.Key != "m1" || m.Event.RRule == "" {
		t.Fatalf("master = %+v", m)
	}
	if len(m.Event.ExDates) != 1 || m.Event.ExDates[0].Format("2006-01-02") != "2026-11-09" {
		t.Errorf("exdates = %v, want only 9 Nov", m.Event.ExDates)
	}
	if len(overrides) != 1 || overrides[0].Key != "m1#2026-11-16T14:00:00Z" || overrides[0].Event.Title != "Fußball (Halle)" || overrides[0].Event.Start.Hour() != 16 {
		t.Errorf("overrides = %+v", overrides)
	}
}

func TestBoxEventBecomesAGraphBody(t *testing.T) {
	e := calendar.Event{Title: "Zahnarzt", Description: "Kontrolle", Location: "Praxis",
		Start: time.Date(2026, 11, 20, 8, 30, 0, 0, berlin), End: time.Date(2026, 11, 20, 9, 0, 0, 0, berlin), RRule: "FREQ=WEEKLY;BYDAY=FR"}
	body, err := graph.BodyOf(e, "Für: Mama")
	if err != nil {
		t.Fatal(err)
	}
	if body["subject"] != "Zahnarzt" || body["isAllDay"] != false {
		t.Errorf("body = %+v", body)
	}
	start := body["start"].(map[string]any)
	if start["dateTime"] != "2026-11-20T08:30:00" || start["timeZone"] != "W. Europe Standard Time" {
		t.Errorf("start = %+v", start)
	}
	if b := body["body"].(map[string]any); b["contentType"] != "text" || b["content"] != "Kontrolle\nFür: Mama" {
		t.Errorf("body text = %+v", b)
	}
	if body["recurrence"] == nil {
		t.Errorf("no recurrence in %+v", body)
	}
}

func canon(t *testing.T, rule string) string {
	t.Helper()
	o, err := rrule.StrToROption(rule)
	if err != nil {
		t.Fatalf("%s: %v", rule, err)
	}
	return o.RRuleString()
}
