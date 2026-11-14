package caldav_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	ical "github.com/emersion/go-ical"

	"github.com/hejmo-home/hejmo/internal/caldav"
	"github.com/hejmo-home/hejmo/internal/calendar"
)

var berlin = mustZone("Europe/Berlin")

func mustZone(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func at(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, berlin)
}

func text(t *testing.T, cal *ical.Calendar) string {
	t.Helper()
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func roundTrip(t *testing.T, o caldav.Object) caldav.Object {
	t.Helper()
	cal, err := o.Calendar()
	if err != nil {
		t.Fatal(err)
	}
	// Through the wire format, as a phone would see it.
	decoded, err := ical.NewDecoder(strings.NewReader(text(t, cal))).Decode()
	if err != nil {
		t.Fatalf("own output does not parse: %v\n%s", err, text(t, cal))
	}
	got, err := caldav.Parse(decoded, berlin)
	if err != nil {
		t.Fatalf("parse own output: %v\n%s", err, text(t, cal))
	}
	return got
}

func TestTimedSeriesSurvivesTheRoundTrip(t *testing.T) {
	master := calendar.Event{
		ID: "choir", Title: "Choir, \"Tuesday\"; with commas", Description: "Bring sheet music\nand water", Location: "School hall",
		Start: at(2026, 11, 3, 17, 0), End: at(2026, 11, 3, 18, 0), RRule: "FREQ=WEEKLY;COUNT=6",
		ExDates: []time.Time{at(2026, 11, 10, 17, 0)},
	}
	got := roundTrip(t, caldav.Object{Master: master})
	g := got.Master
	if g.Title != master.Title || g.Description != master.Description || g.Location != master.Location || g.RRule != master.RRule {
		t.Fatalf("got %+v", g)
	}
	if !g.Start.Equal(master.Start) || !g.End.Equal(master.End) || g.Start.Location().String() != "Europe/Berlin" {
		t.Fatalf("times: %v - %v in %v", g.Start, g.End, g.Start.Location())
	}
	if len(g.ExDates) != 1 || !g.ExDates[0].Equal(master.ExDates[0]) {
		t.Fatalf("exdates = %v", g.ExDates)
	}
	if got.UID != "choir" {
		t.Fatalf("uid = %q", got.UID)
	}
}

func TestOutputDefinesItsTimeZoneIncludingDaylightSaving(t *testing.T) {
	cal, err := caldav.Object{Master: calendar.Event{ID: "x", Title: "X", Start: at(2026, 7, 1, 9, 0), End: at(2026, 7, 1, 10, 0)}}.Calendar()
	if err != nil {
		t.Fatal(err)
	}
	out := text(t, cal)
	for _, want := range []string{"BEGIN:VTIMEZONE", "TZID:Europe/Berlin", "BEGIN:DAYLIGHT", "BEGIN:STANDARD", "TZOFFSETTO:+0200", "TZOFFSETTO:+0100", "DTSTART;TZID=Europe/Berlin:20260701T090000"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestAllDayEventKeepsDatesAndExclusiveEnd(t *testing.T) {
	e := calendar.Event{ID: "trip", Title: "Trip", AllDay: true,
		Start: time.Date(2026, 12, 24, 0, 0, 0, 0, berlin), End: time.Date(2026, 12, 27, 0, 0, 0, 0, berlin)}
	cal, _ := caldav.Object{Master: e}.Calendar()
	out := text(t, cal)
	if !strings.Contains(out, "DTSTART;VALUE=DATE:20261224") || !strings.Contains(out, "DTEND;VALUE=DATE:20261227") {
		t.Fatalf("all-day dates wrong:\n%s", out)
	}
	if strings.Contains(out, "VTIMEZONE") {
		t.Fatalf("all-day only calendar needs no time zone:\n%s", out)
	}
	g := roundTrip(t, caldav.Object{Master: e}).Master
	if !g.AllDay || !g.Start.Equal(e.Start) || !g.End.Equal(e.End) {
		t.Fatalf("got %+v", g)
	}
}

func TestChangedOccurrenceTravelsWithItsSeries(t *testing.T) {
	master := calendar.Event{ID: "m", Title: "Choir", Start: at(2026, 11, 3, 17, 0), End: at(2026, 11, 3, 18, 0), RRule: "FREQ=WEEKLY"}
	moved := calendar.Event{ID: "o", Title: "Choir (hall B)", SeriesID: "m", RecurrenceID: at(2026, 11, 10, 17, 0),
		Start: at(2026, 11, 10, 18, 0), End: at(2026, 11, 10, 19, 0)}
	got := roundTrip(t, caldav.Object{Master: master, Overrides: []calendar.Event{moved}})
	if len(got.Overrides) != 1 {
		t.Fatalf("overrides = %+v", got.Overrides)
	}
	o := got.Overrides[0]
	if o.Title != "Choir (hall B)" || !o.RecurrenceID.Equal(moved.RecurrenceID) || !o.Start.Equal(moved.Start) {
		t.Fatalf("override = %+v", o)
	}
}

func TestParseReadsUTCAndWindowsZones(t *testing.T) {
	src := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:u1\r\nSUMMARY:Call\r\n" +
		"DTSTART:20261102T160000Z\r\nDTEND:20261102T170000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	cal, _ := ical.NewDecoder(strings.NewReader(src)).Decode()
	got, err := caldav.Parse(cal, berlin)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Master.Start.Equal(time.Date(2026, 11, 2, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("start = %v", got.Master.Start)
	}
}

func TestParseKeepsOldEventsThatAFeedWouldDrop(t *testing.T) {
	src := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:old\r\nSUMMARY:Old\r\n" +
		"DTSTART:20150102T160000Z\r\nDTEND:20150102T170000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	cal, _ := ical.NewDecoder(strings.NewReader(src)).Decode()
	if _, err := caldav.Parse(cal, berlin); err != nil {
		t.Fatalf("an event from 2015 written by a phone must be kept: %v", err)
	}
}

func TestParseRejectsWhatIsNotOneEvent(t *testing.T) {
	two := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:a\r\nSUMMARY:A\r\nDTSTART:20261102T160000Z\r\nDTEND:20261102T170000Z\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:b\r\nSUMMARY:B\r\nDTSTART:20261103T160000Z\r\nDTEND:20261103T170000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	todo := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VTODO\r\nUID:t\r\nSUMMARY:T\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
	for name, src := range map[string]string{"two UIDs": two, "task only": todo} {
		cal, _ := ical.NewDecoder(strings.NewReader(src)).Decode()
		if _, err := caldav.Parse(cal, berlin); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestETagChangesWithContentOnly(t *testing.T) {
	e := calendar.Event{ID: "e", Title: "A", Start: at(2026, 11, 3, 17, 0), End: at(2026, 11, 3, 18, 0)}
	tag := func(o caldav.Object) string {
		cal, err := o.Calendar()
		if err != nil {
			t.Fatal(err)
		}
		v, err := caldav.ETag(cal)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if tag(caldav.Object{Master: e}) != tag(caldav.Object{Master: e}) {
		t.Fatal("etag is not stable")
	}
	changed := e
	changed.Title = "B"
	if tag(caldav.Object{Master: e}) == tag(caldav.Object{Master: changed}) {
		t.Fatal("etag did not change with the title")
	}
}

func TestGroupJoinsChangedOccurrencesToTheirSeries(t *testing.T) {
	master := calendar.Event{ID: "m", Title: "Choir", RRule: "FREQ=WEEKLY"}
	single := calendar.Event{ID: "s", Title: "Dentist"}
	moved := calendar.Event{ID: "o", Title: "Choir B", SeriesID: "m"}
	objs := caldav.Group([]calendar.Event{master, single, moved})
	if len(objs) != 2 || objs[0].Master.ID != "m" || len(objs[0].Overrides) != 1 || objs[1].Master.ID != "s" {
		t.Fatalf("objects = %+v", objs)
	}
}
