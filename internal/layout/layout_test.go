package layout_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/layout"
)

var family = []string{"mama", "papa", "sebastian", "mia"}

func ids(bs []layout.Block) []layout.BlockID {
	var out []layout.BlockID
	for _, b := range bs {
		out = append(out, b.ID)
	}
	return out
}

// Q89: a new display starts with today's layout: all people, all current
// blocks; the two new blocks start off (Q128).
func TestADefaultLayoutLooksLikeTodaysDisplay(t *testing.T) {
	l := layout.Default(family)
	if !l.Whole || l.Rotate || !slices.Equal(l.People, family) {
		t.Fatalf("default = %+v", l)
	}
	want := map[layout.BlockID]bool{
		layout.Clock: true, layout.Weather: true,
		layout.Events: true, layout.Routines: false, layout.Tasks: true,
		layout.Meals: true, layout.Tomorrow: true, layout.List: false,
	}
	for id, on := range want {
		if l.Enabled(id) != on {
			t.Errorf("%s enabled = %v, want %v", id, l.Enabled(id), on)
		}
	}
	// Q85: three zones, each with its blocks in the order of the design.
	if got := ids(l.In(layout.Header)); !slices.Equal(got, []layout.BlockID{layout.Clock, layout.Weather}) {
		t.Errorf("header = %v", got)
	}
	if got := ids(l.In(layout.Columns)); !slices.Equal(got, []layout.BlockID{layout.Events, layout.Routines, layout.Tasks}) {
		t.Errorf("columns = %v", got)
	}
	if got := ids(l.In(layout.Footer)); !slices.Equal(got, []layout.BlockID{layout.Meals, layout.Tomorrow, layout.List}) {
		t.Errorf("footer = %v", got)
	}
}

// Q86: more blocks can be added later; a stored layout that does not know a
// block gets it at the end of its zone, off for the new ones; an unknown stored
// block is dropped.
func TestNormalizeCompletesBlocksAndDropsUnknownOnes(t *testing.T) {
	l := layout.Layout{Whole: true, People: family, Blocks: []layout.Block{
		{ID: layout.Weather, On: false}, {ID: "future", On: true}, {ID: layout.Clock, On: true}, {ID: layout.Tasks, On: true},
	}}
	n := l.Normalize(family)
	if got := ids(n.In(layout.Header)); !slices.Equal(got, []layout.BlockID{layout.Weather, layout.Clock}) {
		t.Errorf("header = %v", got)
	}
	if n.Enabled(layout.Weather) {
		t.Errorf("the stored switch was lost")
	}
	if got := ids(n.In(layout.Columns)); !slices.Equal(got, []layout.BlockID{layout.Tasks, layout.Events, layout.Routines}) {
		t.Errorf("columns = %v", got)
	}
	if n.Enabled(layout.Events) != true || n.Enabled(layout.Routines) != false {
		t.Errorf("missing blocks start with their default: events %v routines %v", n.Enabled(layout.Events), n.Enabled(layout.Routines))
	}
	for _, b := range n.Blocks {
		if b.ID == "future" {
			t.Errorf("unknown block kept")
		}
	}
}

// Q87: "Ganze Familie" adds new people at the end; chosen people who are gone drop out.
func TestWholeFamilyAddsNewPeopleAtTheEndAndSingleChoiceDoesNot(t *testing.T) {
	whole := layout.Layout{Whole: true, People: []string{"papa", "gone", "mama"}}
	got := whole.Normalize(family).People
	if !slices.Equal(got, []string{"papa", "mama", "sebastian", "mia"}) {
		t.Errorf("whole family = %v", got)
	}
	some := layout.Layout{People: []string{"mia", "gone", "mama"}}
	if got := some.Normalize(family).People; !slices.Equal(got, []string{"mia", "mama"}) {
		t.Errorf("some people = %v", got)
	}
	// Nobody left (everyone chosen was deleted): the display shows the whole family, never nothing.
	none := layout.Layout{People: []string{"gone"}}.Normalize(family)
	if !none.Whole || !slices.Equal(none.People, family) {
		t.Errorf("nobody left = %+v", none)
	}
}

func TestAtLeastOnePersonIsNeeded(t *testing.T) {
	if err := (layout.Layout{}).Validate(); !errors.Is(err, layout.ErrNoPeople) {
		t.Fatalf("Validate = %v", err)
	}
	if err := layout.Default(family).Validate(); err != nil {
		t.Fatal(err)
	}
}

// Q88: exactly one person chosen: the display shows that person's personal view.
func TestOnePersonIsThePersonalView(t *testing.T) {
	if id, ok := (layout.Layout{People: []string{"mia"}}).Personal(); !ok || id != "mia" {
		t.Errorf("one person = %q %v", id, ok)
	}
	if _, ok := layout.Default(family).Personal(); ok {
		t.Errorf("a family is not a personal view")
	}
}

// Q87: 6 columns side by side in landscape, 4 in portrait.
func TestColumnsThatFitSideBySide(t *testing.T) {
	if layout.Visible(false) != 6 || layout.Visible(true) != 4 {
		t.Fatalf("visible = %d, %d", layout.Visible(false), layout.Visible(true))
	}
}

// Q91 a: round-robin, every day each person moves one column on, the rightmost
// goes to the left; it changes at 03:00.
func TestDailyRotationMovesEveryoneOneColumnOnAtThreeInTheNight(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	d := func(day, h, m int) int { return layout.RotationDay(time.Date(2026, 11, day, h, m, 0, 0, berlin)) }
	if d(17, 2, 59) != d(16, 12, 0) {
		t.Errorf("02:59 is still the day before")
	}
	if d(17, 3, 0) != d(16, 12, 0)+1 {
		t.Errorf("03:00 starts the next day")
	}
	if d(18, 0, 0) != d(17, 4, 0) {
		t.Errorf("midnight does not change it")
	}

	l := layout.Layout{Whole: true, People: []string{"a", "b", "c"}, Rotate: true, OrderDay: 100}
	for day, want := range map[int][]string{
		100: {"a", "b", "c"},
		101: {"c", "a", "b"},
		102: {"b", "c", "a"},
		103: {"a", "b", "c"},
		99:  {"b", "c", "a"},
	} {
		if got := l.Order(day); !slices.Equal(got, want) {
			t.Errorf("day %d = %v, want %v", day, got, want)
		}
	}
	// Off: always the saved order.
	l.Rotate = false
	if got := l.Order(105); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("without rotation = %v", got)
	}
	// One person never moves.
	one := layout.Layout{People: []string{"a"}, Rotate: true}
	if got := one.Order(7); !slices.Equal(got, []string{"a"}) {
		t.Errorf("one person = %v", got)
	}
}
