package caldav_test

import (
	"strings"
	"testing"
	"time"

	ical "github.com/emersion/go-ical"

	"github.com/hejmo-home/hejmo/internal/caldav"
)

func todoText(t *testing.T, td caldav.Todo) string {
	t.Helper()
	cal, err := td.Calendar()
	if err != nil {
		t.Fatal(err)
	}
	return text(t, cal)
}

func TestOpenOnceTaskIsAVTODOWithTitleNotesAndDueDate(t *testing.T) {
	out := todoText(t, caldav.Todo{UID: "t1", Title: "Pack swim bag", Notes: "Towel, goggles", Due: "2026-11-20", Modified: at(2026, 11, 14, 12, 0)})
	for _, want := range []string{"BEGIN:VTODO", "UID:t1", "SUMMARY:Pack swim bag", "DESCRIPTION:Towel\\, goggles", "DUE;VALUE=DATE:20261120", "STATUS:NEEDS-ACTION", "DTSTAMP:20261114T110000Z"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "COMPLETED") || strings.Contains(out, "RRULE") {
		t.Errorf("an open once task has neither COMPLETED nor RRULE:\n%s", out)
	}
}

func TestDoneTaskIsCompletedWithItsTimestamp(t *testing.T) {
	out := todoText(t, caldav.Todo{UID: "t1", Title: "x", Due: "2026-11-20", Done: true, CompletedAt: at(2026, 11, 20, 8, 30), Modified: at(2026, 11, 14, 12, 0)})
	for _, want := range []string{"STATUS:COMPLETED", "COMPLETED:20261120T073000Z", "PERCENT-COMPLETE:100"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

// A repeating task shows today's occurrence: a plain VTODO of today, no rule.
func TestRepeatingTaskShowsTodaysOccurrenceWithoutARule(t *testing.T) {
	out := todoText(t, caldav.Todo{UID: "t2", Title: "Feed the cat", Start: "2026-11-16", Modified: at(2026, 11, 14, 12, 0)})
	if !strings.Contains(out, "DTSTART;VALUE=DATE:20261116") || strings.Contains(out, "RRULE") || strings.Contains(out, "DUE") {
		t.Errorf("unexpected occurrence:\n%s", out)
	}
}

func TestSameTaskRendersToTheSameETag(t *testing.T) {
	td := caldav.Todo{UID: "t1", Title: "x", Due: "2026-11-20", Modified: at(2026, 11, 14, 12, 0)}
	etag := func(td caldav.Todo) string {
		cal, _ := td.Calendar()
		e, err := caldav.ETag(cal)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	if etag(td) != etag(td) {
		t.Fatal("ETag changes between renders")
	}
	done := td
	done.Done, done.CompletedAt = true, at(2026, 11, 15, 8, 0)
	if etag(td) == etag(done) {
		t.Fatal("ETag does not follow completion")
	}
}

func parseTodo(t *testing.T, body string) (caldav.TodoEdit, error) {
	t.Helper()
	cal, err := ical.NewDecoder(strings.NewReader(body)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return caldav.ParseTodo(cal, berlin)
}

func vtodo(lines ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//EN\r\nBEGIN:VTODO\r\nUID:t1\r\nDTSTAMP:20261114T120000Z\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
}

func TestParseReadsTitleNotesDueAndCompletion(t *testing.T) {
	got, err := parseTodo(t, vtodo("SUMMARY:New title", "DESCRIPTION:Some notes", "DUE;VALUE=DATE:20261125", "STATUS:COMPLETED", "COMPLETED:20261120T073000Z"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "New title" || got.Notes != "Some notes" || got.Due != "2026-11-25" || !got.Completed {
		t.Fatalf("got %+v", got)
	}
}

func TestParseReadsADueTimeInTheHouseholdZone(t *testing.T) {
	// 23:30 UTC on the 20th is already the 21st in Berlin.
	got, err := parseTodo(t, vtodo("SUMMARY:x", "DUE:20261120T233000Z"))
	if err != nil || got.Due != "2026-11-21" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestParseTreatsMissingStatusAsOpenAndCompletedPropertyAsDone(t *testing.T) {
	open, err := parseTodo(t, vtodo("SUMMARY:x"))
	if err != nil || open.Completed || open.Due != "" || open.Notes != "" {
		t.Fatalf("open: %+v, %v", open, err)
	}
	done, err := parseTodo(t, vtodo("SUMMARY:x", "COMPLETED:20261120T073000Z"))
	if err != nil || !done.Completed {
		t.Fatalf("done: %+v, %v", done, err)
	}
	back, err := parseTodo(t, vtodo("SUMMARY:x", "STATUS:NEEDS-ACTION"))
	if err != nil || back.Completed {
		t.Fatalf("needs-action: %+v, %v", back, err)
	}
}

func TestParseRefusesWhatIsNotOneTodo(t *testing.T) {
	for name, body := range map[string]string{
		"an event":    "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:e\r\nDTSTAMP:20261114T120000Z\r\nDTSTART:20261120T090000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n",
		"two todos":   strings.Replace(vtodo("SUMMARY:a"), "END:VCALENDAR\r\n", "BEGIN:VTODO\r\nUID:t2\r\nDTSTAMP:20261114T120000Z\r\nSUMMARY:b\r\nEND:VTODO\r\nEND:VCALENDAR\r\n", 1),
		"no title":    vtodo("DESCRIPTION:only notes"),
		"blank title": vtodo("SUMMARY:   "),
	} {
		if _, err := parseTodo(t, body); err == nil || !strings.Contains(err.Error(), caldav.ErrInvalid.Error()) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

var _ = time.UTC
