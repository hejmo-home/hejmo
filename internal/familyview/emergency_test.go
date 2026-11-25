package familyview_test

import (
	"slices"
	"testing"

	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/layout"
)

func sampleEmergency() []familyview.EmergencyInput {
	return []familyview.EmergencyInput{
		{ID: "c1", Name: "Notruf", Initials: "N", Numbers: []familyview.EmergencyNumberInput{{Kind: "Feuerwehr und Rettung", Value: "112"}, {Kind: "Polizei", Value: "110"}}},
		{ID: "c2", Name: "Oma Gerda", PhotoURL: "/display/contacts/c2/photo?v=1", Initials: "OG", Numbers: []familyview.EmergencyNumberInput{{Kind: "Mobil", Value: "0171 3456 789"}}},
		{ID: "papa", Name: "Papa", Person: true, Slot: 2, Shape: "circle-empty", Initials: "P", Numbers: []familyview.EmergencyNumberInput{{Kind: "Mobil", Value: "0151 9876 5432"}}},
	}
}

// Ticket 303: the tile shows every contact with every number, in the order given,
// each number in its digit groups, so a line can break between groups only.
func TestEmergencyTileShowsEveryContactAndEveryNumberInDigitGroups(t *testing.T) {
	v := buildWith(on(layout.Emergency), familyview.Input{Emergency: sampleEmergency()})
	if v.Emergency == nil {
		t.Fatal("no emergency tile")
	}
	var names []string
	for _, c := range v.Emergency.Contacts {
		names = append(names, c.Name)
	}
	if !slices.Equal(names, []string{"Notruf", "Oma Gerda", "Papa"}) {
		t.Fatalf("contacts = %v", names)
	}
	notruf := v.Emergency.Contacts[0]
	if len(notruf.Numbers) != 2 || notruf.Numbers[0].Kind != "Feuerwehr und Rettung" || !slices.Equal(notruf.Numbers[1].Groups, []string{"110"}) {
		t.Errorf("notruf numbers = %+v", notruf.Numbers)
	}
	oma := v.Emergency.Contacts[1]
	if !slices.Equal(oma.Numbers[0].Groups, []string{"0171", "3456", "789"}) {
		t.Errorf("groups = %v", oma.Numbers[0].Groups)
	}
	if oma.Numbers[0].Kind != "" {
		t.Errorf("a single number carries no kind, got %q", oma.Numbers[0].Kind)
	}
	if !v.Emergency.Contacts[2].Person || v.Emergency.Contacts[2].Slot != 2 {
		t.Errorf("a person keeps the marker: %+v", v.Emergency.Contacts[2])
	}
}

// Ticket 303: off by default, in the footer order at its place when on.
func TestEmergencyTileIsOffByDefaultAndTakesItsPlaceInTheFooter(t *testing.T) {
	in := familyview.Input{Emergency: sampleEmergency()}
	if v := buildWith(nil, in); v.Emergency != nil || slices.Contains(orderOf(v.FooterOrder), "emergency") {
		t.Errorf("a new display shows no emergency numbers: %+v %v", v.Emergency, v.FooterOrder)
	}
	v := buildWith(func(l *layout.Layout) {
		on(layout.Emergency)(l)
		// emergency first in the footer
		var foot, rest []layout.Block
		for _, b := range l.Blocks {
			if z, _ := layout.ZoneOf(b.ID); z == layout.Footer {
				if b.ID == layout.Emergency {
					foot = append([]layout.Block{b}, foot...)
				} else {
					foot = append(foot, b)
				}
			} else {
				rest = append(rest, b)
			}
		}
		l.Blocks = append(rest, foot...)
	}, in)
	if got := orderOf(v.FooterOrder); got[0] != "emergency" {
		t.Errorf("footer order = %v", got)
	}
}

// Ticket 303: a contact without a number is left out; with none left the tile is hidden.
func TestEmergencyTileLeavesOutContactsWithoutNumbersAndHidesWhenEmpty(t *testing.T) {
	in := familyview.Input{Emergency: append(sampleEmergency(), familyview.EmergencyInput{ID: "c9", Name: "Frau Huber", Initials: "FH"})}
	v := buildWith(on(layout.Emergency), in)
	if v.Emergency == nil || len(v.Emergency.Contacts) != 3 {
		t.Fatalf("tile = %+v", v.Emergency)
	}
	v = buildWith(on(layout.Emergency), familyview.Input{Emergency: []familyview.EmergencyInput{{ID: "c9", Name: "Frau Huber"}}})
	if v.Emergency != nil {
		t.Errorf("no contact with a number: no tile, got %+v", v.Emergency)
	}
	if got := orderOf(v.FooterOrder); slices.Contains(got, "emergency") {
		t.Errorf("an empty tile is not in the footer order: %v", got)
	}
}
