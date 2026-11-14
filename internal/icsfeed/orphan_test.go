package icsfeed_test

import "testing"

func TestCancelledMasterDropsItsChangedOccurrences(t *testing.T) {
	res := parse(t, feed(
		vevent("UID:a", "SUMMARY:Swim", "DTSTART;TZID=Europe/Berlin:20261102T170000", "DTEND;TZID=Europe/Berlin:20261102T180000", "RRULE:FREQ=WEEKLY;COUNT=3", "STATUS:CANCELLED"),
		vevent("UID:a", "SUMMARY:Swim moved", "RECURRENCE-ID;TZID=Europe/Berlin:20261109T170000", "DTSTART;TZID=Europe/Berlin:20261109T190000", "DTEND;TZID=Europe/Berlin:20261109T200000"),
	))
	if len(res.Items) != 0 {
		t.Fatalf("items = %+v, want none", res.Items)
	}
}

func TestChangedOccurrenceOfUnreadableSeriesStaysASingleEvent(t *testing.T) {
	res := parse(t, feed(
		vevent("UID:a", "SUMMARY:Swim", "DTSTART;TZID=Europe/Berlin:20261102T170000", "RRULE:FREQ=SOMETIMES"),
		vevent("UID:a", "SUMMARY:Swim moved", "RECURRENCE-ID;TZID=Europe/Berlin:20261109T170000", "DTSTART;TZID=Europe/Berlin:20261109T190000", "DTEND;TZID=Europe/Berlin:20261109T200000"),
	))
	if len(res.Items) != 1 || res.Items[0].SeriesKey != "" || res.Items[0].Event.SeriesID != "" {
		t.Fatalf("items = %+v", res.Items)
	}
}
