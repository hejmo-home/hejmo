package familyview

import (
	"sort"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/wholine"
)

// NightInput is what the night page of quiet mode needs (ADR 0015).
type NightInput struct {
	Morning     time.Time // midnight of the coming morning, in the household's zone
	Occurrences []calendar.Occurrence
	TimeFormat  store.TimeFormat // the household's; empty means 24h
	// Waste lists the waste collections of that day (ticket 6.7 fills it).
	Waste []string
}

// NightView is the data of the night page: calm, about the coming morning.
type NightView struct {
	Weekday time.Weekday
	Date    time.Time
	First   *Event // the household's first timed event of the day; nil if none
	Waste   []string
}

// BuildNight lays out the night page.
func BuildNight(in NightInput) NightView {
	d0 := in.Morning
	d1 := d0.AddDate(0, 0, 1)
	v := NightView{Weekday: d0.Weekday(), Date: d0, Waste: in.Waste}
	occs := append([]calendar.Occurrence(nil), in.Occurrences...)
	sort.SliceStable(occs, func(i, j int) bool { return occs[i].Start.Before(occs[j].Start) })
	for _, o := range occs {
		if o.AllDay || o.Start.Before(d0) || !o.Start.Before(d1) {
			continue
		}
		e := card(o, d0.Location(), in.TimeFormat, wholine.Line{})
		v.First = &e
		break
	}
	return v
}
