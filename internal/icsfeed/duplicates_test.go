package icsfeed_test

import (
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/icsfeed"
)

// Real servers hold objects that break the iCalendar cardinality rules. The
// box reads them: where a single-valued property occurs more than once, the
// first one counts and the rest are ignored.
func TestParseTakesTheFirstOfRepeatedSingleValuedProperties(t *testing.T) {
	zone, _ := time.LoadLocation("Europe/Berlin")
	opts := icsfeed.Options{Zone: zone, Now: time.Date(2026, 11, 14, 12, 0, 0, 0, zone)}
	lines := func(l ...string) []byte { return []byte(strings.Join(l, "\r\n") + "\r\n") }

	tests := []struct {
		name  string
		data  []byte
		title string
		start time.Time
		items int
	}{
		{"two STATUS lines", lines("BEGIN:VCALENDAR", "VERSION:2.0", "BEGIN:VEVENT", "UID:a@t", "DTSTART:20261121T150000Z", "DTEND:20261121T160000Z", "STATUS:CONFIRMED", "STATUS:TENTATIVE", "SUMMARY:Piano", "END:VEVENT", "END:VCALENDAR"),
			"Piano", time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC), 1},
		{"three METHOD lines", lines("BEGIN:VCALENDAR", "VERSION:2.0", "METHOD:PUBLISH", "METHOD:REQUEST", "METHOD:PUBLISH", "BEGIN:VEVENT", "UID:b@t", "DTSTART:20261122T150000Z", "SUMMARY:Party", "END:VEVENT", "END:VCALENDAR"),
			"Party", time.Date(2026, 11, 22, 15, 0, 0, 0, time.UTC), 1},
		{"two DTSTART, SUMMARY and DTEND lines", lines("BEGIN:VCALENDAR", "VERSION:2.0", "BEGIN:VEVENT", "UID:c@t", "DTSTART:20261123T150000Z", "DTSTART:20261124T150000Z", "DTEND:20261123T170000Z", "DTEND:20261123T160000Z", "SUMMARY:First", "SUMMARY:Second", "END:VEVENT", "END:VCALENDAR"),
			"First", time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC), 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := icsfeed.Parse(tc.data, opts)
			if err != nil {
				t.Fatalf("Parse() = %v, want the event read", err)
			}
			if len(res.Items) != tc.items || len(res.Notes) != 0 {
				t.Fatalf("Parse() = %d items, notes %v; want %d items and no notes", len(res.Items), res.Notes, tc.items)
			}
			if e := res.Items[0].Event; e.Title != tc.title || !e.Start.Equal(tc.start) {
				t.Fatalf("event = %q at %v, want %q at %v", e.Title, e.Start, tc.title, tc.start)
			}
		})
	}
}

func TestParseTreatsAFirstCancelledStatusAsCancelled(t *testing.T) {
	zone := time.UTC
	data := []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:d@t\r\nDTSTART:20261123T150000Z\r\nSTATUS:CANCELLED\r\nSTATUS:CONFIRMED\r\nSUMMARY:Off\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	res, err := icsfeed.Parse(data, icsfeed.Options{Zone: zone, Now: time.Date(2026, 11, 14, 0, 0, 0, 0, zone)})
	if err != nil || len(res.Items) != 0 {
		t.Fatalf("Parse() = %+v, %v; want the cancelled event dropped", res, err)
	}
}

func TestParseStillRejectsTextThatIsNoCalendar(t *testing.T) {
	if _, err := icsfeed.Parse([]byte("this is not a calendar"), icsfeed.Options{}); err == nil {
		t.Fatal("Parse() accepted text that is not iCalendar")
	}
}
