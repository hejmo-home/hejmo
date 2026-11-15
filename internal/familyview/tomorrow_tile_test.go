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
		Waste: waste, DayNotes: notes, TomorrowLabel: "Tomorrow", TodayLabel: "Today"}).TomorrowTile
}

func lines(t *familyview.Tile) string {
	if t == nil {
		return "<hidden>"
	}
	var texts []string
	for _, l := range t.Lines {
		texts = append(texts, l.Text)
	}
	return strings.Join(texts, "|")
}

// kinds lists the kind of each line: w for a waste collection, n for the note, t for a note of today.
func kinds(t *familyview.Tile) string {
	out := ""
	for _, l := range t.Lines {
		switch {
		case l.Kind == familyview.LineWaste:
			out += "w"
		case l.Kind == familyview.LineNote && l.NoteToday:
			out += "t"
		case l.Kind == familyview.LineNote:
			out += "n"
		default:
			out += "?"
		}
	}
	return out
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

// ADR 0016 (design pass): the room is three lines minus one for the note. Collections that fit get one line
// each; when they do not, they share one line with every title named. There is no "+N more".
func TestTomorrowTileCollectionsShareOneLineWhenTheyDoNotFit(t *testing.T) {
	four := []calendar.Occurrence{pickup("Paper bin", 16), pickup("Residual waste", 16), pickup("Organic waste", 16), pickup("Yellow bag", 16)}
	all := "Paper bin, Residual waste, Organic waste, Yellow bag"
	if got := tile(at(15, 9, 0), nil, four...); lines(got) != all || kinds(got) != "w" {
		t.Fatalf("four collections: %s %s", lines(got), kinds(got))
	}
	three := four[:3]
	if got := tile(at(15, 9, 0), nil, three...); lines(got) != "Paper bin|Residual waste|Organic waste" || kinds(got) != "www" {
		t.Fatalf("three collections: %s %s", lines(got), kinds(got))
	}
	notes := map[string]string{"2026-11-16": "Bring bags"}
	if got := tile(at(15, 19, 0), notes, three...); lines(got) != "Paper bin, Residual waste, Organic waste|Bring bags" || kinds(got) != "wn" {
		t.Fatalf("three collections and a note: %s %s", lines(got), kinds(got))
	}
	if got := tile(at(15, 19, 0), notes, four[:2]...); lines(got) != "Paper bin|Residual waste|Bring bags" || kinds(got) != "wwn" {
		t.Fatalf("two collections and a note: %s %s", lines(got), kinds(got))
	}
	if got := tile(at(15, 19, 0), notes, four...); lines(got) != all+"|Bring bags" {
		t.Fatalf("four collections and a note: %s", lines(got))
	}
	for _, got := range []*familyview.Tile{tile(at(15, 9, 0), nil, four...), tile(at(15, 19, 0), notes, four...)} {
		if strings.Contains(lines(got), "more") {
			t.Fatalf("a count replaces titles: %s", lines(got))
		}
	}
}

// Design pass: a line of today's note is marked, and the tile is labelled "Today" when it holds only that note.
func TestTomorrowTileLabelAndTodaysNote(t *testing.T) {
	notes := map[string]string{"2026-11-15": "Mia: swimming bag", "2026-11-16": "Papa: parents' evening"}
	only := tile(at(15, 10, 0), notes)
	if only.Label != "Today" || kinds(only) != "t" {
		t.Fatalf("only today's note: label %q kinds %s", only.Label, kinds(only))
	}
	next := tile(at(15, 10, 0), notes, pickup("Paper bin", 16))
	if next.Label != "Tomorrow" || kinds(next) != "wt" {
		t.Fatalf("today's note next to a collection: label %q kinds %s", next.Label, kinds(next))
	}
	evening := tile(at(15, 18, 0), notes)
	if evening.Label != "Tomorrow" || kinds(evening) != "n" {
		t.Fatalf("tomorrow's note from 18:00: label %q kinds %s", evening.Label, kinds(evening))
	}
	if only.Alert || next.Alert || evening.Alert {
		t.Fatal("highlighted without the reminder")
	}
	if got := tile(at(15, 18, 0), nil, pickup("Paper bin", 16)); got.Label != "Tomorrow" || !got.Alert || !got.HasWaste() {
		t.Fatalf("reminder: %+v", got)
	}
}

// Two feeds may both list the same bin: it is one line.
func TestTomorrowTileMergesSameTitles(t *testing.T) {
	if got := lines(tile(at(15, 9, 0), nil, pickup("Paper bin", 16), pickup("Paper bin", 16))); got != "Paper bin" {
		t.Fatal(got)
	}
}
