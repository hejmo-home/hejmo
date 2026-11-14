package caldav_test

import (
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/hejmo-home/hejmo/internal/caldav"
	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/icsfeed"
)

var (
	stamp = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
)

// iCloudEvent is shaped like what iCloud keeps: an attendee, an alarm, a
// repeated STATUS line, a folded description and properties the box does not know.
const iCloudEvent = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Apple Inc.//iOS 18//EN\r\nCALSCALE:GREGORIAN\r\n" +
	"BEGIN:VEVENT\r\nUID:swim@icloud\r\nDTSTAMP:20261101T080000Z\r\nLAST-MODIFIED:20261101T080000Z\r\nSEQUENCE:2\r\n" +
	"DTSTART;TZID=Europe/Berlin:20261120T150000\r\nDTEND;TZID=Europe/Berlin:20261120T160000\r\n" +
	"SUMMARY:Swimming\r\nSTATUS:CONFIRMED\r\nSTATUS:CONFIRMED\r\nTRANSP:OPAQUE\r\n" +
	"ATTENDEE;CN=Papa;PARTSTAT=ACCEPTED:mailto:papa@example.org\r\n" +
	"DESCRIPTION:Bring a very long list of things to the pool because the coach wants everyone prepared and\r\n  on time\r\n" +
	"X-APPLE-TRAVEL-ADVISORY-BEHAVIOR:AUTOMATIC\r\n" +
	"BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT15M\r\nDESCRIPTION:Reminder\r\nEND:VALARM\r\n" +
	"END:VEVENT\r\nEND:VCALENDAR\r\n"

func objectOf(t *testing.T, raw string) caldav.Object {
	t.Helper()
	res, err := icsfeed.Parse([]byte(raw), icsfeed.Options{Zone: berlin, Now: time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	var o caldav.Object
	for _, it := range res.Items {
		if it.SeriesKey == "" {
			o.UID, o.Master = it.Key, it.Event
		} else {
			e := it.Event
			e.SeriesID = ""
			o.Overrides = append(o.Overrides, e)
		}
	}
	return o
}

var opts = caldav.PatchOptions{
	Zone: berlin, Now: stamp, AudienceLines: []string{"For: ", "Für: "},
	Audience: func(e calendar.Event) (string, string) {
		if len(e.People) == 0 && len(e.Groups) == 0 {
			return "", ""
		}
		return icsfeed.FormatAudience(e.People, e.Groups), "For: " + strings.Join(e.People, ", ")
	},
}

func patch(t *testing.T, raw string, o caldav.Object) string {
	t.Helper()
	out, err := caldav.Patch(raw, o, opts)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPatchOfAnUnchangedObjectIsByteIdentical(t *testing.T) {
	if got := patch(t, iCloudEvent, objectOf(t, iCloudEvent)); got != iCloudEvent {
		t.Fatalf("changed:\n%s", got)
	}
}

func TestChangingTheTitleKeepsAttendeesAlarmDuplicatesAndUnknownProperties(t *testing.T) {
	o := objectOf(t, iCloudEvent)
	o.Master.Title = "Swimming lesson"
	got := patch(t, iCloudEvent, o)

	want := strings.Replace(iCloudEvent, "SUMMARY:Swimming\r\n", "SUMMARY:Swimming lesson\r\n", 1)
	want = strings.Replace(want, "DTSTAMP:20261101T080000Z", "DTSTAMP:20261114T120000Z", 1)
	want = strings.Replace(want, "LAST-MODIFIED:20261101T080000Z", "LAST-MODIFIED:20261114T120000Z", 1)
	want = strings.Replace(want, "SEQUENCE:2", "SEQUENCE:3", 1)
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	for _, keep := range []string{"STATUS:CONFIRMED\r\nSTATUS:CONFIRMED", "ATTENDEE;CN=Papa;PARTSTAT=ACCEPTED:mailto:papa@example.org", "BEGIN:VALARM", "X-APPLE-TRAVEL-ADVISORY-BEHAVIOR:AUTOMATIC"} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %q", keep)
		}
	}
}

func TestAudienceGoesOutAsPropertyAndReadableLineWithTheTitleUnchanged(t *testing.T) {
	o := objectOf(t, iCloudEvent)
	o.Master.People = []string{"Mia", "Sebastian"}
	got := patch(t, iCloudEvent, o)
	if !strings.Contains(got, "X-HEJMO-AUDIENCE:person:Mia,person:Sebastian\r\n") {
		t.Fatalf("no audience property:\n%s", got)
	}
	if !strings.Contains(got, "SUMMARY:Swimming\r\n") {
		t.Fatalf("title changed:\n%s", got)
	}
	back := objectOf2(t, got)
	if back.Master.Description != "Bring a very long list of things to the pool because the coach wants everyone prepared and on time" ||
		len(back.Master.People) != 2 {
		t.Fatalf("round trip = %+v", back.Master)
	}
	if !strings.Contains(unfold(got), "\\n\\nFor: Mia\\, Sebastian") && !strings.Contains(unfold(got), "\\n\\nFor: Mia, Sebastian") {
		t.Fatalf("no readable line:\n%s", unfold(got))
	}
}

func objectOf2(t *testing.T, raw string) caldav.Object {
	t.Helper()
	res, err := icsfeed.Parse([]byte(raw), icsfeed.Options{Zone: berlin, Now: time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), AudienceLines: opts.AudienceLines})
	if err != nil {
		t.Fatal(err)
	}
	return caldav.Object{UID: res.Items[0].Key, Master: res.Items[0].Event}
}

func unfold(s string) string { return strings.ReplaceAll(s, "\r\n ", "") }

func TestRemovingTheAudienceRemovesPropertyAndLine(t *testing.T) {
	o := objectOf(t, iCloudEvent)
	o.Master.People = []string{"Mia"}
	with := patch(t, iCloudEvent, o)
	o2 := objectOf2(t, with)
	o2.Master.People = nil
	got := patch(t, with, o2)
	if strings.Contains(got, "X-HEJMO") || strings.Contains(got, "For:") {
		t.Fatalf("audience remains:\n%s", got)
	}
}

func TestMovingAnEventReplacesStartAndEndAndDropsADuration(t *testing.T) {
	raw := strings.Replace(iCloudEvent, "DTEND;TZID=Europe/Berlin:20261120T160000\r\n", "DURATION:PT1H\r\n", 1)
	o := objectOf(t, raw)
	o.Master.Start = o.Master.Start.Add(24 * time.Hour)
	o.Master.End = o.Master.End.Add(25 * time.Hour)
	got := patch(t, raw, o)
	for _, want := range []string{"DTSTART;TZID=Europe/Berlin:20261121T150000\r\n", "DTEND;TZID=Europe/Berlin:20261121T170000\r\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "DURATION:PT1H") {
		t.Errorf("duration kept")
	}
}

const series = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\n" +
	"BEGIN:VEVENT\r\nUID:choir@x\r\nDTSTAMP:20261101T080000Z\r\nDTSTART;TZID=Europe/Berlin:20261102T170000\r\nDTEND;TZID=Europe/Berlin:20261102T180000\r\nRRULE:FREQ=WEEKLY;COUNT=6\r\nSUMMARY:Choir\r\nATTENDEE:mailto:a@example.org\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:choir@x\r\nDTSTAMP:20261101T080000Z\r\nRECURRENCE-ID;TZID=Europe/Berlin:20261109T170000\r\nDTSTART;TZID=Europe/Berlin:20261109T180000\r\nDTEND;TZID=Europe/Berlin:20261109T190000\r\nSUMMARY:Choir (late)\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestSeriesEditsRoundTripRRuleExdateAndRecurrenceID(t *testing.T) {
	o := objectOf(t, series)
	// "only this" deleted: EXDATE on the master, the override stays untouched.
	o.Master.ExDates = []time.Time{time.Date(2026, 11, 16, 17, 0, 0, 0, berlin)}
	// "only this" edited: a new changed occurrence.
	ov := o.Master
	ov.RRule, ov.ExDates = "", nil
	ov.Title = "Choir (concert)"
	ov.RecurrenceID = time.Date(2026, 11, 23, 17, 0, 0, 0, berlin)
	ov.Start, ov.End = ov.RecurrenceID, ov.RecurrenceID.Add(2*time.Hour)
	o.Overrides = append(o.Overrides, ov)
	got := patch(t, series, o)

	back := objectOf2Series(t, got)
	if len(back.Master.ExDates) != 1 || !back.Master.ExDates[0].Equal(o.Master.ExDates[0]) || back.Master.RRule != "FREQ=WEEKLY;COUNT=6" {
		t.Fatalf("master = %+v", back.Master)
	}
	if len(back.Overrides) != 2 {
		t.Fatalf("overrides = %+v", back.Overrides)
	}
	if !strings.Contains(got, "ATTENDEE:mailto:a@example.org") || !strings.Contains(got, "RECURRENCE-ID;TZID=Europe/Berlin:20261123T170000") {
		t.Fatalf("got:\n%s", got)
	}

	// "this and all following"/"all": a series change and a removed override.
	o2 := back
	o2.Master.RRule = "FREQ=WEEKLY;COUNT=3"
	o2.Overrides = o2.Overrides[:0]
	for _, x := range back.Overrides {
		if x.RecurrenceID.Day() != 9 {
			o2.Overrides = append(o2.Overrides, x)
		}
	}
	got2 := patch(t, got, o2)
	if strings.Contains(got2, "Choir (late)") || !strings.Contains(got2, "RRULE:FREQ=WEEKLY;COUNT=3") || !strings.Contains(got2, "Choir (concert)") {
		t.Fatalf("got:\n%s", got2)
	}
}

func objectOf2Series(t *testing.T, raw string) caldav.Object { return objectOf(t, raw) }

func TestANewObjectIsRenderedWithTimeZoneAndAudience(t *testing.T) {
	o := caldav.Object{UID: "new-1", Master: calendar.Event{ID: "new-1", Title: "Piano, grade 2", AllDay: false,
		Start: time.Date(2026, 12, 1, 16, 0, 0, 0, berlin), End: time.Date(2026, 12, 1, 17, 0, 0, 0, berlin), People: []string{"Mia"}}}
	got := patch(t, "", o)
	for _, want := range []string{"BEGIN:VTIMEZONE", "UID:new-1", "SUMMARY:Piano\\, grade 2", "X-HEJMO-AUDIENCE:person:Mia", "DTSTART;TZID=Europe/Berlin:20261201T160000", "DESCRIPTION:For: Mia"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}

func TestMovingToAnUnlistedZoneAddsItsDefinition(t *testing.T) {
	o := objectOf(t, iCloudEvent)
	ny, _ := time.LoadLocation("America/New_York")
	o.Master.Start = time.Date(2026, 11, 20, 10, 0, 0, 0, ny)
	o.Master.End = time.Date(2026, 11, 20, 11, 0, 0, 0, ny)
	got := patch(t, iCloudEvent, o)
	if !strings.Contains(got, "TZID:America/New_York") || !strings.Contains(got, "DTSTART;TZID=America/New_York:20261120T100000") {
		t.Fatalf("got:\n%s", got)
	}
}
