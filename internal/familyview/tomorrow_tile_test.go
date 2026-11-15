package familyview_test

import (
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/familyview"
)

func pickup(title string, day int) calendar.Occurrence {
	return calendar.Occurrence{EventID: title, CalendarID: "waste", Title: title, AllDay: true, Start: at(day, 0, 0), End: at(day+1, 0, 0)}
}

func tile(now time.Time, notes map[string]string, waste ...calendar.Occurrence) *familyview.Tile {
	return familyview.Build(familyview.Input{Now: now, Lang: "en", Region: "DE-BY", People: family(),
		Waste: waste, DayNotes: notes, TomorrowLabel: "Tomorrow", MoreFormat: "+%d more"}).TomorrowTile
}

func lines(t *familyview.Tile) string {
	if t == nil {
		return "<hidden>"
	}
	return strings.Join(t.Lines, "|")
}

// 6.7: one line per collection tomorrow, shown all day, highlighted from 18:00.
func TestTomorrowTileShowsCollectionsAllDayAndHighlightsFromSix(t *testing.T) {
	paper, residual := pickup("Paper bin", 16), pickup("Residual waste", 16)
	morning := tile(at(15, 9, 0), nil, paper, residual)
	if lines(morning) != "Paper bin|Residual waste" || morning.Alert || morning.Label != "Tomorrow" {
		t.Fatalf("morning = %v alert=%v", lines(morning), morning.Alert)
	}
	if evening := tile(at(15, 18, 0), nil, paper, residual); !evening.Alert {
		t.Fatal("not highlighted at 18:00")
	}
	if late := tile(at(15, 17, 59), nil, paper); late.Alert {
		t.Fatal("highlighted before 18:00")
	}
}

// 6.7: no collection tomorrow means no reminder; a passed collection disappears.
func TestTomorrowTileIsHiddenWithoutACollectionTomorrow(t *testing.T) {
	if got := lines(tile(at(15, 19, 0), nil, pickup("Paper bin", 17))); got != "<hidden>" {
		t.Fatalf("collection in two days: %s", got)
	}
	if got := lines(tile(at(16, 8, 0), nil, pickup("Paper bin", 16))); got != "<hidden>" {
		t.Fatalf("collection today (day has come): %s", got)
	}
	if got := lines(tile(at(17, 8, 0), nil, pickup("Paper bin", 16))); got != "<hidden>" {
		t.Fatalf("collection passed: %s", got)
	}
}

// ADR 0016: the note is the last line; before 18:00 today's note, from 18:00 tomorrow's replaces it.
func TestTomorrowTileNoteTimingAndNoHighlightWithoutCollection(t *testing.T) {
	notes := map[string]string{"2026-11-15": "Mia: swimming bag", "2026-11-16": "Papa: parents' evening"}
	if got := lines(tile(at(15, 10, 0), notes)); got != "Mia: swimming bag" {
		t.Fatalf("before 18:00: %s", got)
	}
	evening := tile(at(15, 18, 0), notes)
	if lines(evening) != "Papa: parents' evening" || evening.Alert {
		t.Fatalf("from 18:00: %s alert=%v", lines(evening), evening.Alert)
	}
	withWaste := tile(at(15, 18, 0), notes, pickup("Paper bin", 16))
	if lines(withWaste) != "Paper bin|Papa: parents' evening" || !withWaste.Alert {
		t.Fatalf("with collection: %s alert=%v", lines(withWaste), withWaste.Alert)
	}
}

// ADR 0016: at most three lines; more collections become "+N more", the note is never hidden.
func TestTomorrowTileLimitsLinesAndKeepsTheNote(t *testing.T) {
	four := []calendar.Occurrence{pickup("Paper bin", 16), pickup("Residual waste", 16), pickup("Organic waste", 16), pickup("Yellow bag", 16)}
	if got := lines(tile(at(15, 9, 0), nil, four...)); got != "Paper bin|Residual waste|+2 more" {
		t.Fatalf("four collections: %s", got)
	}
	three := four[:3]
	if got := lines(tile(at(15, 9, 0), nil, three...)); got != "Paper bin|Residual waste|Organic waste" {
		t.Fatalf("three collections: %s", got)
	}
	notes := map[string]string{"2026-11-16": "Bring bags"}
	if got := lines(tile(at(15, 19, 0), notes, three...)); got != "Paper bin|+2 more|Bring bags" {
		t.Fatalf("three collections and a note: %s", got)
	}
	if got := lines(tile(at(15, 19, 0), notes, four[:2]...)); got != "Paper bin|Residual waste|Bring bags" {
		t.Fatalf("two collections and a note: %s", got)
	}
}

// Two feeds may both list the same bin: it is one line.
func TestTomorrowTileMergesSameTitles(t *testing.T) {
	if got := lines(tile(at(15, 9, 0), nil, pickup("Paper bin", 16), pickup("Paper bin", 16))); got != "Paper bin" {
		t.Fatal(got)
	}
}
