package server

import (
	"errors"
	"slices"

	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

// orderedPeople are the people named in order, in that order (ticket 180).
func orderedPeople(all []familyview.Person, order []string) []familyview.Person {
	out := make([]familyview.Person, 0, len(order))
	for _, id := range order {
		if i := slices.IndexFunc(all, func(p familyview.Person) bool { return p.ID == id }); i >= 0 {
			out = append(out, all[i])
		}
	}
	return out
}

// routineLines are the routines due today with their progress, by person (the
// block "Routines" of the columns, Q86): a routine none of whose steps is due
// today has no line.
func (s *server) routineLines(h store.Household, due []store.DayTask) (map[string][]familyview.RoutineLine, error) {
	routines, err := s.db.Routines(h.ID)
	if err != nil {
		return nil, err
	}
	out := map[string][]familyview.RoutineLine{}
	for _, r := range routines {
		line := familyview.RoutineLine{Name: r.Name, Part: string(r.Part)}
		for _, t := range due {
			if t.RoutineID == r.ID {
				line.Total++
				if t.Done {
					line.Done++
				}
			}
		}
		if line.Total > 0 {
			out[r.PersonID] = append(out[r.PersonID], line)
		}
	}
	return out, nil
}

// listTileInput is the list of the block "one list": nil when none is chosen
// or the chosen one was deleted (the display then shows no list).
func (s *server) listTileInput(h store.Household, id string) (*familyview.ListInput, error) {
	if id == "" {
		return nil, nil
	}
	l, err := s.db.List(h.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entries, err := s.db.Entries(h.ID, id)
	if err != nil {
		return nil, err
	}
	return &familyview.ListInput{ID: l.ID, Name: l.Name, Open: openTexts(entries)}, nil
}

func openTexts(entries []list.Entry) []string {
	var out []string
	for _, e := range entries {
		if !e.Done {
			out = append(out, e.Text)
		}
	}
	return out
}

// pickList is the list a layout names, or the first list of the household when
// the block is on and none was chosen. ok is false when there is none.
func (s *server) pickList(householdID, chosen string) (id, name string, ok bool, err error) {
	lists, err := s.db.Lists(householdID)
	if err != nil {
		return "", "", false, err
	}
	for _, l := range lists {
		if l.ID == chosen {
			return l.ID, l.Name, true, nil
		}
	}
	if len(lists) > 0 && chosen == "" {
		return lists[0].ID, lists[0].Name, true, nil
	}
	return "", "", false, nil
}
