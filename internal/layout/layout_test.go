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
		layout.Events: true, layout.Routines: false, layout.Tasks: true, layout.Stars: true,
		layout.Meals: true, layout.Tomorrow: true, layout.List: false, layout.Emergency: false,
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
	if got := ids(l.In(layout.Columns)); !slices.Equal(got, []layout.BlockID{layout.Events, layout.Routines, layout.Tasks, layout.Stars}) {
		t.Errorf("columns = %v", got)
	}
	if got := ids(l.In(layout.Footer)); !slices.Equal(got, []layout.BlockID{layout.Meals, layout.Tomorrow, layout.List, layout.Emergency}) {
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
	if got := ids(n.In(layout.Columns)); !slices.Equal(got, []layout.BlockID{layout.Tasks, layout.Events, layout.Routines, layout.Stars}) {
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
	// A household of one with "Whole family" is a family view with one column, as before.
	if _, ok := layout.Default([]string{"mama"}).Personal(); ok {
		t.Errorf("whole family of one is not a personal view")
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

	// The shift counts from a fixed epoch (day mod n), so it does not depend on
	// when a display was saved.
	l := layout.Layout{Whole: true, People: []string{"a", "b", "c"}, Rotate: true}
	for day, want := range map[int][]string{
		99:  {"a", "b", "c"},
		100: {"c", "a", "b"},
		101: {"b", "c", "a"},
		102: {"a", "b", "c"},
		98:  {"b", "c", "a"},
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

// Q91: every display with rotation on shows the same order on the same day, whenever
// it was saved: what is saved is the base order, the order of day 0.
func TestUnrotatingTodaysOrderGivesTheBaseOrder(t *testing.T) {
	for n := 1; n <= 5; n++ {
		shown := []string{"a", "b", "c", "d", "e"}[:n]
		for day := 20000; day < 20000+2*n; day++ {
			l := layout.Layout{People: layout.Unrotate(shown, day), Rotate: true}
			if got := l.Order(day); !slices.Equal(got, shown) {
				t.Errorf("n=%d day=%d: order = %v, want %v", n, day, got, shown)
			}
		}
	}
	// Two displays that show the same order on different days agree for good once
	// each saved the base of its day.
	a := layout.Layout{People: layout.Unrotate([]string{"a", "b", "c"}, 100), Rotate: true}
	b := layout.Layout{People: layout.Unrotate([]string{"c", "a", "b"}, 101), Rotate: true}
	for day := 100; day < 110; day++ {
		if !slices.Equal(a.Order(day), b.Order(day)) {
			t.Errorf("day %d: %v != %v", day, a.Order(day), b.Order(day))
		}
	}
}

// When the household changes the shift follows the new number of people (day mod
// n), so the order can jump more than one column that night. This pins it.
func TestTheRotationFollowsTheNumberOfPeople(t *testing.T) {
	three := layout.Layout{People: []string{"a", "b", "c"}, Rotate: true}
	four := layout.Layout{People: []string{"a", "b", "c", "d"}, Rotate: true}
	if got := three.Order(101); !slices.Equal(got, []string{"b", "c", "a"}) { // 101 mod 3 = 2
		t.Errorf("three = %v", got)
	}
	if got := four.Order(101); !slices.Equal(got, []string{"d", "a", "b", "c"}) { // 101 mod 4 = 1
		t.Errorf("four = %v", got)
	}
}

// Ticket 303: the emergency numbers block is new, so a stored layout gets it at
// the end of the footer, off.
func TestEmergencyNumbersJoinAnExistingFooterAtTheEndAndOff(t *testing.T) {
	l := layout.Layout{Whole: true, People: family, Blocks: []layout.Block{
		{ID: layout.List, On: true}, {ID: layout.Meals, On: true}, {ID: layout.Tomorrow, On: true},
	}}
	n := l.Normalize(family)
	if got := ids(n.In(layout.Footer)); !slices.Equal(got, []layout.BlockID{layout.List, layout.Meals, layout.Tomorrow, layout.Emergency}) {
		t.Errorf("footer = %v", got)
	}
	if n.Enabled(layout.Emergency) {
		t.Errorf("a block that was added later starts off")
	}
	if z, ok := layout.ZoneOf(layout.Emergency); !ok || z != layout.Footer {
		t.Errorf("zone = %v %v", z, ok)
	}
}

// Ticket 303: 3 contacts fit in one row in landscape, 2 per row in portrait, and
// four fit in two rows; more need more rows, and nothing is cut.
func TestEmergencyContactsThatFitAndTheRowsTheyNeed(t *testing.T) {
	for _, c := range []struct {
		portrait     bool
		fit, n, rows int
	}{
		{false, 3, 0, 0}, {false, 3, 1, 1}, {false, 3, 3, 1}, {false, 3, 4, 2}, {false, 3, 7, 3},
		{true, 4, 2, 1}, {true, 4, 4, 2}, {true, 4, 5, 3}, {true, 4, 8, 4},
	} {
		if got := layout.EmergencyFit(c.portrait); got != c.fit {
			t.Errorf("fit(portrait %v) = %d, want %d", c.portrait, got, c.fit)
		}
		if got := layout.EmergencyRows(c.n, c.portrait); got != c.rows {
			t.Errorf("rows(%d, portrait %v) = %d, want %d", c.n, c.portrait, got, c.rows)
		}
	}
}
