// Package layout is the model of a display's layout (ticket 180, ADR 0020): which
// people a display shows and in which order, and which layout blocks are on in
// each of the three zones (ADR 0016). It is pure: the store keeps it, the
// family view and the settings page read it.
package layout

import (
	"errors"
	"slices"
	"time"
)

// Zone is one of the three fixed parts of the family view (Q85).
type Zone string

// The zones.
const (
	Header  Zone = "header"
	Columns Zone = "columns"
	Footer  Zone = "footer"
)

// Zones lists the zones from top to bottom.
func Zones() []Zone { return []Zone{Header, Columns, Footer} }

// BlockID names a layout block. More blocks are added by adding a row to the
// catalogue below (Q86).
type BlockID string

// The blocks.
const (
	Clock    BlockID = "clock"
	Weather  BlockID = "weather"
	Events   BlockID = "events"
	Routines BlockID = "routines"
	Tasks    BlockID = "tasks"
	Meals    BlockID = "meals"
	Tomorrow BlockID = "tomorrow"
	List     BlockID = "list"
)

type spec struct {
	ID        BlockID
	Zone      Zone
	DefaultOn bool
}

// catalogue is every block in its default order. The new blocks, routines and
// one list, start off so a new display looks like today (Q89, Q128).
var catalogue = []spec{
	{Clock, Header, true}, {Weather, Header, true},
	{Events, Columns, true}, {Routines, Columns, false}, {Tasks, Columns, true},
	{Meals, Footer, true}, {Tomorrow, Footer, true}, {List, Footer, false},
}

// ZoneOf is the zone a block belongs to; false for an unknown block.
func ZoneOf(id BlockID) (Zone, bool) {
	for _, s := range catalogue {
		if s.ID == id {
			return s.Zone, true
		}
	}
	return "", false
}

// Known reports whether id is a block.
func Known(id BlockID) bool { _, ok := ZoneOf(id); return ok }

// Block is a block with its switch.
type Block struct {
	ID BlockID
	On bool
}

// MaxListEntries is how many open entries the list block shows in full (Q129).
const MaxListEntries = 8

// Layout is the layout of one display.
type Layout struct {
	// Whole is "Ganze Familie": people added to the household later join at the end.
	Whole bool
	// People are the chosen people in the order they were saved on OrderDay.
	People []string
	// Rotate moves every person one column on each day (Q91).
	Rotate   bool
	OrderDay int
	// Blocks are in order inside their zone.
	Blocks []Block
	// ListID is the list of the block "one list"; ListName is its name when it
	// was chosen, so a deleted list can be named.
	ListID, ListName string
}

// ErrNoPeople is returned for a layout without a person.
var ErrNoPeople = errors.New("a layout needs at least one person")

// Default is the layout of a new display: today's (Q89).
func Default(household []string) Layout {
	l := Layout{Whole: true, People: slices.Clone(household)}
	for _, s := range catalogue {
		l.Blocks = append(l.Blocks, Block{s.ID, s.DefaultOn})
	}
	return l
}

// Validate refuses a layout nobody sees.
func (l Layout) Validate() error {
	if len(l.People) == 0 {
		return ErrNoPeople
	}
	return nil
}

// Normalize fits l to the household as it is now: people who are gone drop out,
// with Whole the people missing join at the end, and every block of the
// catalogue is there once, in its zone, unknown ones dropped. If nobody is left
// the display shows the whole family.
func (l Layout) Normalize(household []string) Layout {
	out := l
	out.People = nil
	for _, p := range l.People {
		if slices.Contains(household, p) && !slices.Contains(out.People, p) {
			out.People = append(out.People, p)
		}
	}
	if l.Whole || len(out.People) == 0 {
		out.Whole = true
		for _, p := range household {
			if !slices.Contains(out.People, p) {
				out.People = append(out.People, p)
			}
		}
	}
	out.Blocks = nil
	for _, z := range Zones() {
		seen := map[BlockID]bool{}
		for _, b := range l.Blocks {
			if zone, ok := ZoneOf(b.ID); ok && zone == z && !seen[b.ID] {
				seen[b.ID] = true
				out.Blocks = append(out.Blocks, b)
			}
		}
		for _, s := range catalogue {
			if s.Zone == z && !seen[s.ID] {
				out.Blocks = append(out.Blocks, Block{s.ID, s.DefaultOn})
			}
		}
	}
	return out
}

// In lists the blocks of a zone in order.
func (l Layout) In(z Zone) []Block {
	var out []Block
	for _, b := range l.Blocks {
		if zone, _ := ZoneOf(b.ID); zone == z {
			out = append(out, b)
		}
	}
	return out
}

// Enabled reports whether a block is on. A block the layout does not list
// follows its default.
func (l Layout) Enabled(id BlockID) bool {
	for _, b := range l.Blocks {
		if b.ID == id {
			return b.On
		}
	}
	for _, s := range catalogue {
		if s.ID == id {
			return s.DefaultOn
		}
	}
	return false
}

// Personal reports whether the display shows one person's personal view (Q88):
// exactly one person is chosen. A household of one with "Whole family" on is a
// family view with one column, as before.
func (l Layout) Personal() (string, bool) {
	if len(l.People) == 1 && !l.Whole {
		return l.People[0], true
	}
	return "", false
}

// Visible is how many columns fit side by side: 6 in landscape, 4 in portrait (Q87).
func Visible(portrait bool) int {
	if portrait {
		return 4
	}
	return 6
}

// rotationFrom is the hour at which the rotation day changes (Q91).
const rotationFrom = 3

// RotationDay numbers the days of the daily rotation; a new day starts at 03:00
// in now's time zone, the same for every display.
func RotationDay(now time.Time) int {
	t := now.Add(-rotationFrom * time.Hour)
	return int(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

// Order is the order of the columns on a rotation day: with Rotate every person
// moves one column on for each day since OrderDay, the rightmost to the left.
func (l Layout) Order(day int) []string {
	n := len(l.People)
	out := slices.Clone(l.People)
	if !l.Rotate || n < 2 {
		return out
	}
	shift := ((day-l.OrderDay)%n + n) % n
	for i, p := range l.People {
		out[(i+shift)%n] = p
	}
	return out
}
