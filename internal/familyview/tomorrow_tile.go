package familyview

import (
	"fmt"
	"sort"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
)

const maxTileLines = 3 // ADR 0016: at most three lines in the tomorrow tile

// WasteTitles lists the waste collections of the days [from, to): one title
// each, in order of start, the same title from several calendars once.
func WasteTitles(waste []calendar.Occurrence, from, to time.Time) []string {
	occs := append([]calendar.Occurrence(nil), waste...)
	sort.SliceStable(occs, func(i, j int) bool { return occs[i].Start.Before(occs[j].Start) })
	var out []string
	seen := map[string]bool{}
	for _, o := range occs {
		if !o.Start.Before(to) || !o.End.After(from) || seen[o.Title] {
			continue
		}
		seen[o.Title] = true
		out = append(out, o.Title)
	}
	return out
}

// tomorrowTile builds the footer's tomorrow tile (ADR 0016): one line per waste
// collection tomorrow, the parent's note as the last line, three lines at most,
// highlighted from 18:00 when it holds a collection, nil when empty. The note
// of tomorrow replaces the note of today from 18:00.
func tomorrowTile(in Input, now, d1 time.Time) *Tile {
	collections := WasteTitles(in.Waste, d1, d1.AddDate(0, 0, 1))
	day := now
	if now.Hour() >= tomorrowFromHour {
		day = d1
	}
	note := in.DayNotes[day.Format("2006-01-02")]
	if len(collections) == 0 && note == "" {
		return nil
	}
	room := maxTileLines
	if note != "" {
		room--
	}
	var lines []string
	if len(collections) > room {
		room-- // one line for "+N more"
		lines = append(lines, collections[:room]...)
		lines = append(lines, fmt.Sprintf(in.MoreFormat, len(collections)-room))
	} else {
		lines = collections
	}
	if note != "" {
		lines = append(lines, note)
	}
	return &Tile{Label: in.TomorrowLabel, Lines: lines, Alert: len(collections) > 0 && now.Hour() >= tomorrowFromHour}
}
