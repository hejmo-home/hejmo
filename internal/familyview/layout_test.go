package familyview_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/layout"
	"github.com/hejmo-home/hejmo/internal/meal"
)

// buildWith lays out the view with a layout made from the default by edit.
func buildWith(edit func(*layout.Layout), in familyview.Input) familyview.View {
	l := layout.Default([]string{"mama", "papa", "seb", "mia"})
	if edit != nil {
		edit(&l)
	}
	in.Layout = &l
	in.I18n = i18n.MustLoad()
	in.Lang, in.Region = "en", "DE-BY"
	if in.People == nil {
		in.People = family()
	}
	if in.Now.IsZero() {
		in.Now = at(14, 8, 0)
	}
	return familyview.Build(in)
}

func off(ids ...layout.BlockID) func(*layout.Layout) {
	return func(l *layout.Layout) {
		for i := range l.Blocks {
			if slices.Contains(ids, l.Blocks[i].ID) {
				l.Blocks[i].On = false
			}
		}
	}
}

func on(ids ...layout.BlockID) func(*layout.Layout) {
	return func(l *layout.Layout) {
		for i := range l.Blocks {
			if slices.Contains(ids, l.Blocks[i].ID) {
				l.Blocks[i].On = true
			}
		}
	}
}

func orderOf(bs []layout.BlockID) []string {
	var out []string
	for _, b := range bs {
		out = append(out, string(b))
	}
	return out
}

// Without a layout the view is the one of today (Q89).
func TestWithoutALayoutTheViewIsTodays(t *testing.T) {
	v := familyview.Build(familyview.Input{I18n: i18n.MustLoad(), Now: at(14, 8, 0), Lang: "en", Region: "DE-BY", People: family(),
		Weather: &familyview.Weather{TempC: 8, Icon: "clear"}, Meals: map[meal.Mealtime]string{meal.Dinner: "Pasta"}})
	same(t, orderOf(v.HeaderOrder), []string{"clock", "weather"})
	same(t, orderOf(v.ColumnBlocks), []string{"events", "tasks"})
	same(t, orderOf(v.FooterOrder), []string{"meals"})
	if v.Weather == nil || v.Meal == nil {
		t.Fatalf("view = %+v", v)
	}
}

// Header block: time and weather are switches with an order.
func TestHeaderBlocksAreSwitchedAndOrdered(t *testing.T) {
	w := &familyview.Weather{TempC: 8, Icon: "clear"}
	v := buildWith(off(layout.Clock), familyview.Input{Weather: w})
	same(t, orderOf(v.HeaderOrder), []string{"weather"})
	v = buildWith(off(layout.Weather), familyview.Input{Weather: w})
	if v.Weather != nil {
		t.Errorf("weather chip shown with its block off")
	}
	same(t, orderOf(v.HeaderOrder), []string{"clock"})
	v = buildWith(func(l *layout.Layout) { l.Blocks[0], l.Blocks[1] = l.Blocks[1], l.Blocks[0] }, familyview.Input{Weather: w})
	same(t, orderOf(v.HeaderOrder), []string{"weather", "clock"})
	// No reading: no weather chip even when the block is on.
	v = buildWith(nil, familyview.Input{})
	same(t, orderOf(v.HeaderOrder), []string{"clock"})
}

// Column blocks: events, routines (new, off), tasks with progress.
func TestColumnBlocksCanBeSwitchedOff(t *testing.T) {
	occs := []calendar.Occurrence{timed("Swimming", at(14, 16, 0), at(14, 17, 0), "mia"), timed("Tomorrow", at(15, 9, 0), at(15, 10, 0), "mia")}
	progress := map[string]familyview.Progress{"mia": {Done: 2, Total: 3}}
	in := familyview.Input{Occurrences: occs, TaskProgress: progress, ShowTomorrow: true, Now: at(14, 19, 0)}
	v := buildWith(nil, in)
	if len(column(t, v, "Mia").Events) != 1 || len(column(t, v, "Mia").Tomorrow) != 1 || column(t, v, "Mia").Progress == nil {
		t.Fatalf("default column = %+v", column(t, v, "Mia"))
	}
	v = buildWith(off(layout.Events), in)
	if c := column(t, v, "Mia"); len(c.Events) != 0 || len(c.Tomorrow) != 0 || c.Progress == nil {
		t.Errorf("events off = %+v", c)
	}
	v = buildWith(off(layout.Tasks), in)
	if c := column(t, v, "Mia"); len(c.Events) != 1 || c.Progress != nil {
		t.Errorf("tasks off = %+v", c)
	}
	same(t, orderOf(v.ColumnBlocks), []string{"events"})
}

// Q86: routines in the columns, one line per routine, off until switched on.
func TestRoutinesShowInTheColumnsWhenSwitchedOn(t *testing.T) {
	in := familyview.Input{Routines: map[string][]familyview.RoutineLine{"mia": {
		{Name: "Morning", Part: "morning", Done: 2, Total: 4}, {Name: "Evening", Part: "evening", Done: 3, Total: 3},
	}}}
	if got := column(t, buildWith(nil, in), "Mia").Routines; len(got) != 0 {
		t.Fatalf("routines shown by default: %v", got)
	}
	v := buildWith(on(layout.Routines), in)
	got := column(t, v, "Mia").Routines
	if len(got) != 2 || got[0].Name != "Morning" || got[0].Done != 2 || got[0].Total != 4 || got[1].AllDone() != true {
		t.Fatalf("routines = %+v", got)
	}
	if got := column(t, v, "Mama").Routines; len(got) != 0 {
		t.Errorf("Mama has none: %v", got)
	}
	same(t, orderOf(v.ColumnBlocks), []string{"events", "routines", "tasks"})
}

// Footer: the tiles follow the order of the blocks; a block off or without content leaves no tile.
func TestFooterTilesFollowTheBlocks(t *testing.T) {
	in := familyview.Input{Meals: map[meal.Mealtime]string{meal.Dinner: "Pasta"}, Waste: []calendar.Occurrence{
		{CalendarID: "w", Title: "Paper", Start: at(15, 0, 0), End: at(16, 0, 0), AllDay: true}}}
	v := buildWith(nil, in)
	same(t, orderOf(v.FooterOrder), []string{"meals", "tomorrow"})
	v = buildWith(off(layout.Meals), in)
	if v.Meal != nil {
		t.Errorf("meal tile with its block off")
	}
	same(t, orderOf(v.FooterOrder), []string{"tomorrow"})
	v = buildWith(off(layout.Tomorrow), in)
	if v.TomorrowTile != nil {
		t.Errorf("tomorrow tile with its block off")
	}
	v = buildWith(func(l *layout.Layout) { l.Blocks[5], l.Blocks[6] = l.Blocks[6], l.Blocks[5] }, in)
	same(t, orderOf(v.FooterOrder), []string{"tomorrow", "meals"})
}

func openEntries(n int) []string {
	var out []string
	for i := 1; i <= n; i++ {
		out = append(out, fmt.Sprintf("Item %d", i))
	}
	return out
}

// Q86, Q129: "one list" in the footer shows up to 8 open entries in full, then says how many are left.
func TestTheListTileShowsUpToEightEntries(t *testing.T) {
	list := func(n int) *familyview.ListInput {
		return &familyview.ListInput{ID: "L1", Name: "Shopping", Open: openEntries(n)}
	}
	if v := buildWith(nil, familyview.Input{List: list(3)}); v.List != nil {
		t.Fatalf("list tile with its block off")
	}
	v := buildWith(on(layout.List), familyview.Input{List: list(3)})
	if v.List == nil || v.List.ID != "L1" || v.List.Name != "Shopping" || len(v.List.Entries) != 3 || v.List.More != 0 {
		t.Fatalf("tile = %+v", v.List)
	}
	same(t, orderOf(v.FooterOrder), []string{"list"})
	if v := buildWith(on(layout.List), familyview.Input{List: list(8)}); len(v.List.Entries) != 8 || v.List.More != 0 {
		t.Errorf("eight = %+v", v.List)
	}
	v = buildWith(on(layout.List), familyview.Input{List: list(13)})
	if len(v.List.Entries) != 8 || v.List.More != 5 || v.List.Entries[7] != "Item 8" {
		t.Errorf("thirteen = %+v", v.List)
	}
	// An empty list is hidden like any empty tile; so is no list at all (deleted).
	if v := buildWith(on(layout.List), familyview.Input{List: list(0)}); v.List != nil || len(v.FooterOrder) != 0 {
		t.Errorf("empty list shown: %+v", v)
	}
	if v := buildWith(on(layout.List), familyview.Input{}); v.List != nil {
		t.Errorf("no list shown: %+v", v.List)
	}
}

// Q88, Q130: a personal view has the header of the display: the time and weather
// switches still apply, so the personal header gains the weather chip.
func TestThePersonalViewFollowsTheHeaderBlocks(t *testing.T) {
	build := func(edit func(*layout.Layout)) familyview.PersonalView {
		l := layout.Default([]string{"mia"})
		if edit != nil {
			edit(&l)
		}
		return familyview.BuildPersonal(familyview.PersonalInput{Now: at(14, 9, 0), Person: family()[3], Layout: &l, Weather: &familyview.Weather{TempC: 8, Icon: "clear"}, Home: true})
	}
	v := build(nil)
	same(t, orderOf(v.HeaderOrder), []string{"clock", "weather"})
	if v.Weather == nil || v.Weather.Text != "8°C" || !v.Home {
		t.Errorf("view = %+v", v)
	}
	v = build(off(layout.Weather))
	if v.Weather != nil {
		t.Errorf("weather shown with its block off")
	}
	same(t, orderOf(v.HeaderOrder), []string{"clock"})
	same(t, orderOf(build(off(layout.Clock)).HeaderOrder), []string{"weather"})
	// Without a layout the header is the one of today: the clock.
	plain := familyview.BuildPersonal(familyview.PersonalInput{Now: at(14, 9, 0), Person: family()[3]})
	same(t, orderOf(plain.HeaderOrder), []string{"clock"})
}

// Q87: whole-family events always show; a display with only some people does not
// mistake an event for those people for one for the whole family.
func TestAnEventForTheShownPeopleIsNotAWholeFamilyEvent(t *testing.T) {
	all := family()
	shown := []familyview.Person{all[0], all[3]} // Mama and Mia
	occs := []calendar.Occurrence{
		timed("Walk", at(14, 10, 0), at(14, 11, 0), "mama", "mia", "papa"),
		timed("Picnic", at(14, 12, 0), at(14, 13, 0)),
	}
	v := buildWith(nil, familyview.Input{People: shown, Everyone: all, Occurrences: occs})
	same(t, titles(column(t, v, "Mama").Events), []string{"Walk"})
	if len(v.Strip) != 1 || v.Strip[0].Title != "Picnic" {
		t.Errorf("strip = %+v, want only the whole-family event", v.Strip)
	}
	if len(column(t, v, "Mama").Events[0].WhoParts) == 0 {
		t.Errorf("the card names who the event is for")
	}
}
