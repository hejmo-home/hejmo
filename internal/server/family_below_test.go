package server_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/layout"
	"github.com/hejmo-home/hejmo/internal/server"
)

var belowRe = regexp.MustCompile(`(?s)<a class="fv-below"[^>]*>.*?</a>`)

func belowView(order ...layout.BlockID) familyview.View {
	v := familyview.Build(familyview.Input{I18n: i18n.MustLoad(), Now: time.Date(2026, 11, 14, 8, 0, 0, 0, berlinTZ), Lang: "en", Region: "DE-BY", People: []familyview.Person{
		{ID: "a", Name: "Mia", Slot: 4, Shape: "square-empty"}}})
	v.FooterOrder = order
	return v
}

// Ticket 208: on a phone the tiles sit below the columns; the line above the
// columns names them in the footer's order and jumps to them.
func TestBelowLineNamesTheRenderedTilesInFooterOrder(t *testing.T) {
	t.Parallel()
	v := belowView(layout.BlockID("list"), layout.BlockID("tomorrow"), layout.BlockID("meals"))
	out, err := server.RenderFamilyView("en", v)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "fv-below") || strings.Contains(out, "fv-footer") {
		t.Errorf("no tile, yet a below line or footer:\n%s", out)
	}
	v.Meal = &familyview.MealTile{Label: "Dinner", Entries: []familyview.MealEntry{{Mealtime: "dinner", Dish: "Pasta bake"}}}
	v.TomorrowTile = &familyview.Tile{Label: "Tomorrow", Lines: []familyview.Line{{Kind: familyview.LineWaste, Text: "Paper bin"}}}
	v.List = &familyview.ListTile{ID: "l1", Name: "Shopping", Entries: []string{"Milk"}}
	out, _ = server.RenderFamilyView("en", v)
	line := belowRe.FindString(out)
	want := `<a class="fv-below" href="#fv-footer"><span class="t"><span class="lab">Below the columns:</span> <b>Shopping</b><span class="sep"> · </span><b>Tomorrow</b><span class="sep"> · </span><b>Dinner</b></span></a>`
	if line != want {
		t.Errorf("below line:\n got %s\nwant %s", line, want)
	}
	if strings.Contains(line, "flag") {
		t.Error("put-out pill without a highlighted tomorrow tile")
	}
	if i, j := strings.Index(out, "fv-below"), strings.Index(out, `<main class="fv-grid"`); i < 0 || i > j {
		t.Error("the line is not before the columns")
	}
	if !strings.Contains(out, `<footer class="fv-footer" id="fv-footer" data-footer>`) {
		t.Error("footer has no id fv-footer")
	}

	v.TomorrowTile.Alert = true
	out, _ = server.RenderFamilyView("en", v)
	if line := belowRe.FindString(out); !strings.Contains(line, `<b>Tomorrow</b> <span class="flag">Put out tonight</span>`) {
		t.Errorf("no put-out pill after the tomorrow label:\n%s", line)
	}

	v.List, v.TomorrowTile = nil, nil
	out, _ = server.RenderFamilyView("de", v)
	if line := belowRe.FindString(out); !strings.Contains(line, "Unter den Spalten:") || strings.Contains(line, "·") || !strings.Contains(line, "<b>Dinner</b>") {
		t.Errorf("one tile, German:\n%s", line)
	}
}
