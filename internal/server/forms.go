package server

import "github.com/hejmo-home/hejmo/internal/store"

// personChip is a person as the shared chips show them: the name with the
// person's marker (colour slot and black-and-white shape, as on the displays).
type personChip struct {
	ID, Name, Shape string
	Slot            int
}

// chipsOf returns the chips for people in household order; slot and shape
// follow the person's colour and marker (a person without them: their place),
// as in the family view.
// chipOf is the chip of the person with the given id (zero when none).
func chipOf(chips []personChip, id string) personChip {
	for _, c := range chips {
		if c.ID == id {
			return c
		}
	}
	return personChip{}
}

func chipsOf(people []store.Person) []personChip {
	out := make([]personChip, 0, len(people))
	for i, p := range people {
		_, shape := displayedLook(p, i)
		out = append(out, personChip{ID: p.ID, Name: p.Name, Shape: shape, Slot: slotOf(p, i)})
	}
	return out
}
