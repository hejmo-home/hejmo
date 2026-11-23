package server

import (
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/streak"
)

// routineWeeks counts the week of every routine whose owner has the switch
// "week count and badges" on, by routine id (ticket 294). today is the day in
// the household's time zone. Routines of people with the switch off are not in
// the result: nothing shows them anywhere, but their days keep being counted,
// because the count is computed from the ticked days whenever it is asked for.
func (s *server) routineWeeks(h store.Household, today string) (map[string]*streak.Week, error) {
	people, err := s.db.People(h.ID)
	if err != nil {
		return nil, err
	}
	shown := map[string]bool{}
	for _, p := range people {
		shown[p.ID] = p.StreaksShown
	}
	routines, err := s.db.Routines(h.ID)
	if err != nil {
		return nil, err
	}
	out := map[string]*streak.Week{}
	any := false
	for _, r := range routines {
		any = any || shown[r.PersonID]
	}
	if !any {
		return out, nil
	}
	tasks, err := s.db.Tasks(h.ID)
	if err != nil {
		return nil, err
	}
	done, err := s.db.RoutineDone(h.ID)
	if err != nil {
		return nil, err
	}
	pauses, err := s.db.Pauses(h.ID)
	if err != nil {
		return nil, err
	}
	loc := time.UTC
	if l, err := time.LoadLocation(h.Timezone); err == nil {
		loc = l
	}
	for _, r := range routines {
		if !shown[r.PersonID] {
			continue
		}
		sr := streak.Routine{PersonID: r.PersonID, Created: streak.DayOf(r.Created, loc), Done: done, Pauses: pauses}
		for _, t := range tasks {
			if t.RoutineID == r.ID {
				sr.Tasks = append(sr.Tasks, t)
			}
		}
		w := sr.Week(today)
		out[r.ID] = &w
	}
	return out, nil
}

// plainText drops the bold markers (**) of a text of the locales, for the
// quiet lines of the routine card on the Tasks page.
func plainText(s string) string { return strings.ReplaceAll(s, "**", "") }
