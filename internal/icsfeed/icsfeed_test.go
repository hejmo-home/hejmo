package icsfeed_test

import (
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/icsfeed"
)

var (
	berlin = mustZone("Europe/Berlin")
	now    = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
)

func mustZone(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func feed(events ...string) []byte {
	return []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n" + strings.Join(events, "") + "END:VCALENDAR\r\n")
}

func vevent(lines ...string) string {
	return "BEGIN:VEVENT\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VEVENT\r\n"
}

func parse(t *testing.T, data []byte) icsfeed.Result {
	t.Helper()
	res, err := icsfeed.Parse(data, icsfeed.Options{Zone: berlin, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// withSeriesIDs mimics what the store does: ids from keys.
func withSeriesIDs(res icsfeed.Result) []calendar.Event {
	ids := map[string]string{}
	for _, it := range res.Items {
		ids[it.Key] = "id-" + it.Key
	}
	var out []calendar.Event
	for _, it := range res.Items {
		e := it.Event
		e.ID = ids[it.Key]
		if it.SeriesKey != "" {
			e.SeriesID = ids[it.SeriesKey]
		}
		out = append(out, e)
	}
	return out
}

func november(t *testing.T, res icsfeed.Result) []calendar.Occurrence {
	t.Helper()
	occs, err := calendar.Expand(withSeriesIDs(res), time.Date(2026, 11, 1, 0, 0, 0, 0, berlin), time.Date(2026, 12, 1, 0, 0, 0, 0, berlin))
	if err != nil {
		t.Fatal(err)
	}
	return occs
}

// Acceptance: a weekly series with one cancelled occurrence shows the series
// without that occurrence.
func TestWeeklySeriesWithCancelledOccurrence(t *testing.T) {
	res := parse(t, feed(
		vevent("UID:choir@example", "SUMMARY:Choir", "DTSTART;TZID=Europe/Berlin:20261102T170000", "DTEND;TZID=Europe/Berlin:20261102T180000",
			"RRULE:FREQ=WEEKLY;COUNT=4", "EXDATE;TZID=Europe/Berlin:20261109T170000"),
	))
	var got []string
	for _, o := range november(t, res) {
		got = append(got, o.Start.Format("01-02T15:04"))
	}
	if want := "11-02T17:00 11-16T17:00 11-23T17:00"; strings.Join(got, " ") != want {
		t.Fatalf("occurrences = %v, want %s", got, want)
	}
}

func TestCancelledOverrideCancelsTheOccurrence(t *testing.T) {
	res := parse(t, feed(
		vevent("UID:a", "SUMMARY:Choir", "DTSTART;TZID=Europe/Berlin:20261102T170000", "DTEND;TZID=Europe/Berlin:20261102T180000", "RRULE:FREQ=WEEKLY;COUNT=3"),
		vevent("UID:a", "SUMMARY:Choir", "RECURRENCE-ID;TZID=Europe/Berlin:20261109T170000", "DTSTART;TZID=Europe/Berlin:20261109T170000", "DTEND;TZID=Europe/Berlin:20261109T180000", "STATUS:CANCELLED"),
	))
	if occs := november(t, res); len(occs) != 2 {
		t.Fatalf("got %d occurrences, want 2: %+v", len(occs), occs)
	}
}

func TestChangedOccurrenceLinksToSeries(t *testing.T) {
	res := parse(t, feed(
		vevent("UID:a", "SUMMARY:Choir", "DTSTART;TZID=Europe/Berlin:20261102T170000", "DTEND;TZID=Europe/Berlin:20261102T180000", "RRULE:FREQ=WEEKLY;COUNT=3"),
		vevent("UID:a", "SUMMARY:Choir (moved)", "RECURRENCE-ID;TZID=Europe/Berlin:20261109T170000", "DTSTART;TZID=Europe/Berlin:20261109T190000", "DTEND;TZID=Europe/Berlin:20261109T200000"),
	))
	if len(res.Items) != 2 || res.Items[1].SeriesKey != "a" || res.Items[1].Key == res.Items[0].Key {
		t.Fatalf("items = %+v", res.Items)
	}
	var titles []string
	for _, o := range november(t, res) {
		titles = append(titles, o.Start.Format("15:04")+" "+o.Title)
	}
	if want := "17:00 Choir|19:00 Choir (moved)|17:00 Choir"; strings.Join(titles, "|") != want {
		t.Fatalf("got %v, want %s", titles, want)
	}
}

// Acceptance: Windows zone names become IANA zones; floating and unknown
// zones use the box's zone.
func TestTimeZones(t *testing.T) {
	cases := []struct {
		name, dtstart, zone string
		note                bool
	}{
		{"windows", "DTSTART;TZID=W. Europe Standard Time:20261102T170000", "Europe/Berlin", false},
		{"windows quoted", `DTSTART;TZID="Pacific Standard Time":20261102T170000`, "America/Los_Angeles", false},
		{"iana", "DTSTART;TZID=Europe/London:20261102T170000", "Europe/London", false},
		{"floating", "DTSTART:20261102T170000", "Europe/Berlin", false},
		{"unknown", "DTSTART;TZID=Mars/Olympus:20261102T170000", "Europe/Berlin", true},
		{"utc", "DTSTART:20261102T170000Z", "UTC", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := parse(t, feed(vevent("UID:x", "SUMMARY:X", c.dtstart)))
			if len(res.Items) != 1 {
				t.Fatalf("items = %d", len(res.Items))
			}
			e := res.Items[0].Event
			if got := e.Start.Location().String(); got != c.zone {
				t.Errorf("zone = %s, want %s", got, c.zone)
			}
			if e.Start.Hour() != 17 {
				t.Errorf("wall clock changed: %v", e.Start)
			}
			if c.note != (len(res.Notes) > 0) {
				t.Errorf("notes = %v, want note: %v", res.Notes, c.note)
			}
		})
	}
}

func TestEveryWindowsZoneIsLoadable(t *testing.T) {
	for win, iana := range icsfeed.WindowsZones {
		if _, err := time.LoadLocation(iana); err != nil {
			t.Errorf("%s -> %s: %v", win, iana, err)
		}
	}
}

// Acceptance: events older than 12 months are not stored, unless they belong
// to a series reaching into the window.
func TestImportWindow(t *testing.T) {
	res := parse(t, feed(
		vevent("UID:old", "SUMMARY:Old", "DTSTART;TZID=Europe/Berlin:20250101T100000", "DTEND;TZID=Europe/Berlin:20250101T110000"),
		vevent("UID:edge", "SUMMARY:Edge", "DTSTART;TZID=Europe/Berlin:20251120T100000", "DTEND;TZID=Europe/Berlin:20251120T110000"),
		vevent("UID:lingering", "SUMMARY:Lingering", "DTSTART;TZID=Europe/Berlin:20240101T100000", "DTEND;TZID=Europe/Berlin:20240101T110000", "RRULE:FREQ=YEARLY"),
		vevent("UID:ended", "SUMMARY:Ended", "DTSTART;TZID=Europe/Berlin:20240101T100000", "DTEND;TZID=Europe/Berlin:20240101T110000", "RRULE:FREQ=WEEKLY;COUNT=5"),
		vevent("UID:ended", "SUMMARY:Ended", "RECURRENCE-ID;TZID=Europe/Berlin:20240108T100000", "DTSTART;TZID=Europe/Berlin:20240108T120000", "DTEND;TZID=Europe/Berlin:20240108T130000"),
	))
	var got []string
	for _, it := range res.Items {
		got = append(got, it.Event.Title)
	}
	if want := "Edge,Lingering"; strings.Join(got, ",") != want {
		t.Fatalf("kept %v, want %s", got, want)
	}
}

func TestAllDayEventsAndDefaults(t *testing.T) {
	res := parse(t, feed(
		vevent("UID:h", "SUMMARY:Autumn break", "DTSTART;VALUE=DATE:20261102", "DTEND;VALUE=DATE:20261107", "DESCRIPTION:Line one\\nLine two", "LOCATION:School\\, main hall"),
		vevent("UID:d", "SUMMARY:Holiday", "DTSTART;VALUE=DATE:20261224"),
		vevent("UID:p", "SUMMARY:Pickup", "DTSTART;TZID=Europe/Berlin:20261103T150000", "DURATION:PT45M"),
	))
	if len(res.Items) != 3 {
		t.Fatalf("items = %d", len(res.Items))
	}
	a, b, p := res.Items[0].Event, res.Items[1].Event, res.Items[2].Event
	if !a.AllDay || a.End.Format("2006-01-02") != "2026-11-07" || a.Description != "Line one\nLine two" || a.Location != "School, main hall" {
		t.Errorf("all-day = %+v", a)
	}
	if !b.AllDay || b.End.Format("2006-01-02") != "2026-12-25" {
		t.Errorf("default all-day end = %+v", b)
	}
	if p.End.Sub(p.Start) != 45*time.Minute {
		t.Errorf("duration end = %v", p.End.Sub(p.Start))
	}
}

func TestNoTitleCancelledAndBrokenEvents(t *testing.T) {
	res := parse(t, feed(
		vevent("UID:1", "DTSTART;TZID=Europe/Berlin:20261103T150000"),
		vevent("UID:2", "SUMMARY:Cancelled", "DTSTART;TZID=Europe/Berlin:20261103T150000", "STATUS:CANCELLED"),
		vevent("UID:3", "SUMMARY:No start"),
		vevent("UID:4", "SUMMARY:Bad rule", "DTSTART;TZID=Europe/Berlin:20261103T150000", "RRULE:FREQ=SOMETIMES"),
		vevent("UID:5", "SUMMARY:Fine", "DTSTART;TZID=Europe/Berlin:20261103T150000"),
	))
	var got []string
	for _, it := range res.Items {
		if it.Key == "1" && it.Event.Title != "" {
			t.Errorf("title of the untitled event = %q, want empty (the placeholder is localised when shown)", it.Event.Title)
		}
		got = append(got, it.Key)
	}
	if strings.Join(got, ",") != "1,5" {
		t.Fatalf("kept %v", got)
	}
	if len(res.Notes) != 2 || res.Notes[0].Key != "sync.note.no_start" || res.Notes[1].Key != "sync.note.bad_rrule" {
		t.Fatalf("notes = %v, want one each for the event without start and the bad rule", res.Notes)
	}
	if res.Notes[0].Params["label"] != "No start" || res.Notes[1].Params["detail"] == "" {
		t.Errorf("note params = %v", res.Notes)
	}
}

func TestNotACalendar(t *testing.T) {
	if _, err := icsfeed.Parse([]byte("<html>login</html>"), icsfeed.Options{Zone: berlin, Now: now}); err == nil {
		t.Fatal("want an error for a non-calendar body")
	}
}

func TestNormalizeURL(t *testing.T) {
	for in, want := range map[string]string{
		"webcal://example.org/a.ics":   "https://example.org/a.ics",
		"WEBCAL://example.org/a.ics":   "https://example.org/a.ics",
		"webcals://example.org/a.ics":  "https://example.org/a.ics",
		" https://example.org/a.ics ":  "https://example.org/a.ics",
		"http://example.org/a.ics?x=1": "http://example.org/a.ics?x=1",
	} {
		got, err := icsfeed.NormalizeURL(in)
		if err != nil || got != want {
			t.Errorf("NormalizeURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "ftp://example.org/a.ics", "file:///etc/passwd", "example.org/a.ics", "https://"} {
		if _, err := icsfeed.NormalizeURL(bad); err == nil {
			t.Errorf("NormalizeURL(%q) accepted", bad)
		}
	}
}
